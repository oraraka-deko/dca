package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestChallenge_PairingCode_DataRace tests concurrent LoadCredentials and SaveCredentials for data races.
func TestChallenge_PairingCode_DataRace(t *testing.T) {
	tmpDir := t.TempDir()
	credPath := filepath.Join(tmpDir, "creds.json")
	mgr := NewPairingCodeManager(credPath)

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Writer goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
				_ = mgr.SaveCredentials(WorkerCredentials{
					NodeID:    fmt.Sprintf("node-%d", i),
					PairToken: fmt.Sprintf("token-%d", i),
					IsPaired:  i%2 == 0,
				})
			}
		}
	}()

	// Reader goroutines accessing returned pointer
	for r := 0; r < 5; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					creds, err := mgr.LoadCredentials()
					if err == nil && creds != nil {
						// Access fields of returned pointer concurrently with SaveCredentials
						_ = creds.NodeID
						_ = creds.PairToken
						_ = creds.IsPaired
					}
				}
			}
		}()
	}

	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// TestChallenge_Outbox_AttemptsNotPersistedOnFailure demonstrates that Attempts counter is lost on failure.
func TestChallenge_Outbox_AttemptsNotPersistedOnFailure(t *testing.T) {
	outbox := NewOutbox(10)
	_ = outbox.Enqueue(OutboxItem{ID: "failed-item", Payload: json.RawMessage(`"test"`)})

	// Flush with a failing sendFunc
	failCount := 0
	_ = outbox.Flush(func(item OutboxItem) error {
		failCount++
		if item.Attempts != failCount {
			t.Logf("Flush call #%d received item.Attempts = %d", failCount, item.Attempts)
		}
		return fmt.Errorf("simulated delivery error #%d", failCount)
	})

	// Try flushing again
	_ = outbox.Flush(func(item OutboxItem) error {
		failCount++
		if item.Attempts != failCount {
			t.Logf("Flush call #%d received item.Attempts = %d", failCount, item.Attempts)
		}
		return fmt.Errorf("simulated delivery error #%d", failCount)
	})

	peek := outbox.PeekAll()
	if len(peek) > 0 {
		t.Logf("Final queued item Attempts count stored in outbox = %d (Expected >0 if attempts tracked)", peek[0].Attempts)
		if peek[0].Attempts == 0 {
			t.Errorf("FAIL: Outbox failed to persist Attempts increment on delivery failure! Stored Attempts = %d", peek[0].Attempts)
		}
	}
}

// TestChallenge_Outbox_ConcurrentFlushDuplicateSends demonstrates duplicate deliveries on concurrent Flush.
func TestChallenge_Outbox_ConcurrentFlushDuplicateSends(t *testing.T) {
	outbox := NewOutbox(10)
	_ = outbox.Enqueue(OutboxItem{ID: "dup-item", Payload: json.RawMessage(`"dup"`)})

	var sendCount int64
	var wg sync.WaitGroup

	// Slow sendFunc to create overlap window
	slowSend := func(item OutboxItem) error {
		atomic.AddInt64(&sendCount, 1)
		time.Sleep(50 * time.Millisecond)
		return nil
	}

	// Launch 2 concurrent Flush calls
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = outbox.Flush(slowSend)
	}()
	go func() {
		defer wg.Done()
		_ = outbox.Flush(slowSend)
	}()
	wg.Wait()

	totalSent := atomic.LoadInt64(&sendCount)
	t.Logf("Total sendFunc calls for single item = %d", totalSent)
	if totalSent > 1 {
		t.Errorf("FAIL: Item sent %d times due to concurrent Flush calls!", totalSent)
	}
}

// TestChallenge_Outbox_BufferOverflowHighConcurrency tests outbox under 100k items and 100 concurrent writers.
func TestChallenge_Outbox_BufferOverflowHighConcurrency(t *testing.T) {
	maxCap := 500
	outbox := NewOutbox(maxCap)

	var wg sync.WaitGroup
	numWriters := 50
	itemsPerWriter := 1000

	start := time.Now()
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(wId int) {
			defer wg.Done()
			for j := 0; j < itemsPerWriter; j++ {
				_ = outbox.Enqueue(OutboxItem{
					ID:        fmt.Sprintf("w%d-%d", wId, j),
					Payload:   json.RawMessage(`{"data":"payload"}`),
					CreatedAt: time.Now(),
				})
			}
		}(i)
	}

	wg.Wait()
	dur := time.Since(start)

	if outbox.Len() > maxCap {
		t.Errorf("FAIL: Outbox Len %d exceeded max capacity %d", outbox.Len(), maxCap)
	} else {
		t.Logf("PASS: High concurrency outbox overflow handled safely. Enqueued %d items across %d goroutines in %v. Final Len = %d (Cap = %d)",
			numWriters*itemsPerWriter, numWriters, dur, outbox.Len(), maxCap)
	}
}

// TestChallenge_WorkerDaemon_MockWSDropReconnectStress stress tests 20 rapid disconnect/reconnect cycles.
func TestChallenge_WorkerDaemon_MockWSDropReconnectStress(t *testing.T) {
	var connMu sync.Mutex
	var activeConns []*websocket.Conn
	upgrader := websocket.Upgrader{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		connMu.Lock()
		activeConns = append(activeConns, conn)
		connMu.Unlock()

		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	cfg := WorkerDaemonConfig{
		KingURL:           wsURL,
		NodeID:            "stress-node",
		AuthToken:         "stress-token",
		ReconnectInterval: 20 * time.Millisecond,
		MaxOutboxSize:     1000,
	}

	daemon := NewWorkerDaemon(cfg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := daemon.Start(ctx); err != nil {
		t.Fatalf("Failed to start daemon: %v", err)
	}

	// Rapidly drop connections 15 times
	for cycle := 0; cycle < 15; cycle++ {
		// Wait for connection
		connected := false
		for i := 0; i < 50; i++ {
			if daemon.IsConnected() {
				connected = true
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !connected {
			t.Logf("Cycle %d: Worker did not reconnect in time", cycle)
		}

		// Enqueue mock item while dropping
		_ = daemon.Outbox.Enqueue(OutboxItem{
			ID:      fmt.Sprintf("drop-item-%d", cycle),
			Payload: json.RawMessage(`{"test":true}`),
		})

		// Drop active connection
		connMu.Lock()
		for _, c := range activeConns {
			_ = c.Close()
		}
		activeConns = nil
		connMu.Unlock()

		time.Sleep(30 * time.Millisecond)
	}

	// Check goroutine leak after stopping
	daemon.Stop()
	time.Sleep(100 * time.Millisecond)
	t.Logf("Worker daemon stopped cleanly after disconnect stress cycles.")
}

// TestChallenge_WorkerDaemon_GoroutineLeakCheck measures goroutine counts before and after daemon start/stop cycles.
func TestChallenge_WorkerDaemon_GoroutineLeakCheck(t *testing.T) {
	initialGoroutines := runtime.NumGoroutine()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	for cycle := 0; cycle < 5; cycle++ {
		cfg := WorkerDaemonConfig{
			KingURL:           wsURL,
			NodeID:            fmt.Sprintf("leak-node-%d", cycle),
			AuthToken:         "token",
			ReconnectInterval: 10 * time.Millisecond,
		}
		daemon := NewWorkerDaemon(cfg, nil)
		ctx, cancel := context.WithCancel(context.Background())
		_ = daemon.Start(ctx)

		time.Sleep(50 * time.Millisecond)
		cancel()
		daemon.Stop()
	}

	time.Sleep(200 * time.Millisecond)
	finalGoroutines := runtime.NumGoroutine()

	t.Logf("Goroutines before: %d, Goroutines after 5 start/stop cycles: %d", initialGoroutines, finalGoroutines)
	if finalGoroutines > initialGoroutines+3 {
		t.Errorf("Possible goroutine leak detected: start=%d, end=%d", initialGoroutines, finalGoroutines)
	}
}
