package utils

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestEmpirical_Outbox_ConcurrentFlush_DuplicateSend tests whether concurrent Flush calls
// cause duplicate item delivery over the network.
func TestEmpirical_Outbox_ConcurrentFlush_DuplicateSend(t *testing.T) {
	outbox := NewOutbox(100)
	for i := 1; i <= 10; i++ {
		_ = outbox.Enqueue(OutboxItem{ID: fmt.Sprintf("msg-%d", i), Payload: json.RawMessage(`"data"`)})
	}

	var sentMu sync.Mutex
	sentItems := make([]string, 0)

	mockSend := func(item OutboxItem) error {
		// Simulate network latency during send
		time.Sleep(2 * time.Millisecond)
		sentMu.Lock()
		sentItems = append(sentItems, item.ID)
		sentMu.Unlock()
		return nil
	}

	var wg sync.WaitGroup
	// Launch 5 concurrent flushers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = outbox.Flush(mockSend)
		}()
	}
	wg.Wait()

	sentMu.Lock()
	totalSent := len(sentItems)
	counts := make(map[string]int)
	for _, id := range sentItems {
		counts[id]++
	}
	sentMu.Unlock()

	duplicates := 0
	for id, count := range counts {
		if count > 1 {
			duplicates += (count - 1)
			t.Logf("BUG FOUND: Item %s was sent %d times (duplicate delivery!)", id, count)
		}
	}

	if duplicates > 0 {
		t.Errorf("EMPIRICAL FAILURE: Outbox.Flush is not safe for concurrent callers! Sent %d items for 10 enqueued items (%d duplicates)", totalSent, duplicates)
	}
}

// TestEmpirical_Outbox_AttemptsCounter_Bug tests if item.Attempts is preserved when sendFunc fails.
func TestEmpirical_Outbox_AttemptsCounter_Bug(t *testing.T) {
	outbox := NewOutbox(10)
	_ = outbox.Enqueue(OutboxItem{ID: "retry-item", Payload: json.RawMessage(`"fail"`), Attempts: 0})

	failSend := func(item OutboxItem) error {
		return errors.New("simulated network error")
	}

	// Try flushing 3 times with failing sendFunc
	for i := 0; i < 3; i++ {
		_ = outbox.Flush(failSend)
	}

	items := outbox.PeekAll()
	if len(items) == 0 {
		t.Fatalf("Item missing from outbox")
	}

	if items[0].Attempts != 3 {
		t.Errorf("BUG FOUND: Item Attempts counter in Outbox queue is %d; expected 3 (Attempts modified on local copy instead of queue item)", items[0].Attempts)
	}
}

// TestEmpirical_Outbox_ItemDrop_Under_FullQueue tests item drops when queue capacity is reached.
func TestEmpirical_Outbox_ItemDrop_Under_FullQueue(t *testing.T) {
	maxSize := 10
	outbox := NewOutbox(maxSize)

	for i := 1; i <= 20; i++ {
		_ = outbox.Enqueue(OutboxItem{ID: fmt.Sprintf("drop-msg-%d", i)})
	}

	if outbox.Len() > maxSize {
		t.Errorf("Expected max size %d, got %d", maxSize, outbox.Len())
	}

	items := outbox.PeekAll()
	// First 10 items (drop-msg-1 to drop-msg-10) should have been silently dropped
	dropped := 0
	for i := 1; i <= 10; i++ {
		targetID := fmt.Sprintf("drop-msg-%d", i)
		found := false
		for _, it := range items {
			if it.ID == targetID {
				found = true
				break
			}
		}
		if !found {
			dropped++
		}
	}

	t.Logf("EMPIRICAL FINDING: Outbox dropped %d out of 20 items when capacity exceeded %d", dropped, maxSize)
	if dropped != 10 {
		t.Errorf("Expected 10 dropped items, got %d", dropped)
	}
}

// TestEmpirical_WorkerDaemon_RapidDisconnectReconnect stress-tests WorkerDaemon and Outbox under rapid WSS disconnects.
func TestEmpirical_WorkerDaemon_RapidDisconnectReconnect(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var activeConnMu sync.Mutex
	var activeConns []*websocket.Conn

	var receivedCount int64
	receivedIDs := make(map[string]int)
	var recMu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		activeConnMu.Lock()
		activeConns = append(activeConns, conn)
		activeConnMu.Unlock()

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				break
			}
			var resp JSONRPCResponse
			if err := json.Unmarshal(msg, &resp); err == nil {
				atomic.AddInt64(&receivedCount, 1)
				recMu.Lock()
				idStr := fmt.Sprintf("%v", resp.ID)
				receivedIDs[idStr]++
				recMu.Unlock()
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	cfg := WorkerDaemonConfig{
		KingURL:           wsURL,
		NodeID:            "stress-node-1",
		AuthToken:         "stress-token",
		ReconnectInterval: 10 * time.Millisecond,
		MaxOutboxSize:     1000,
	}

	daemon := NewWorkerDaemon(cfg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := daemon.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer daemon.Stop()

	// Wait for initial connection
	for i := 0; i < 50; i++ {
		if daemon.IsConnected() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	totalMessages := 100
	var enqueueWg sync.WaitGroup

	// Background worker enqueuing messages
	enqueueWg.Add(1)
	go func() {
		defer enqueueWg.Done()
		for i := 1; i <= totalMessages; i++ {
			msgID := fmt.Sprintf("stress-%d", i)
			payload, _ := json.Marshal(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      msgID,
				Result:  json.RawMessage(`"ok"`),
			})
			_ = daemon.Outbox.Enqueue(OutboxItem{
				ID:        msgID,
				Payload:   payload,
				CreatedAt: time.Now(),
			})
			time.Sleep(1 * time.Millisecond)
		}
	}()

	// Rapidly sever active connections 15 times
	for i := 0; i < 15; i++ {
		time.Sleep(5 * time.Millisecond)
		activeConnMu.Lock()
		for _, c := range activeConns {
			_ = c.Close()
		}
		activeConns = nil
		activeConnMu.Unlock()
	}

	enqueueWg.Wait()

	// Wait for worker daemon to reconnect and flush outbox
	for i := 0; i < 100; i++ {
		if daemon.IsConnected() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := daemon.FlushOutbox(); err != nil {
		t.Logf("Final FlushOutbox error: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	recMu.Lock()
	totalRec := len(receivedIDs)
	duplicates := 0
	for _, cnt := range receivedIDs {
		if cnt > 1 {
			duplicates += (cnt - 1)
		}
	}
	recMu.Unlock()

	t.Logf("EMPIRICAL STRESS TEST RESULTS:")
	t.Logf("Enqueued messages: %d", totalMessages)
	t.Logf("Unique messages received by King: %d", totalRec)
	t.Logf("Duplicate deliveries: %d", duplicates)
	t.Logf("Outbox queue remaining: %d", daemon.Outbox.Len())

	if totalRec < totalMessages {
		t.Errorf("EMPIRICAL FAILURE: Messages were lost under rapid disconnect! Expected %d unique, got %d", totalMessages, totalRec)
	}
}

// TestEmpirical_PairingCode_RandomnessAndBounds tests entropy, distribution, and edge bounds of pairing codes.
func TestEmpirical_PairingCode_RandomnessAndBounds(t *testing.T) {
	numSamples := 50000
	codes := make([]string, numSamples)
	seen := make(map[string]int)

	charCounts := make(map[rune]int)
	posCharCounts := make([]map[rune]int, PairingCodeLength)
	for i := 0; i < PairingCodeLength; i++ {
		posCharCounts[i] = make(map[rune]int)
	}

	for i := 0; i < numSamples; i++ {
		code, err := GeneratePairingCode()
		if err != nil {
			t.Fatalf("GeneratePairingCode failed: %v", err)
		}
		if len(code) != PairingCodeLength {
			t.Fatalf("Code length invalid: got %d, want %d", len(code), PairingCodeLength)
		}
		if !ValidatePairingCode(code) {
			t.Fatalf("Code failed validation: %s", code)
		}
		codes[i] = code
		seen[code]++

		for pos, ch := range code {
			charCounts[ch]++
			posCharCounts[pos][ch]++
		}
	}

	// 1. Check Collision Rate
	collisions := numSamples - len(seen)
	t.Logf("Generated %d codes, Unique: %d, Collisions: %d", numSamples, len(seen), collisions)
	// Expected collision rate for 50k samples out of 2.17B is ~ (50000^2)/(2 * 2.176e9) ≈ 0.57 collisions
	if collisions > 10 {
		t.Errorf("High collision rate detected: %d collisions in %d samples", collisions, numSamples)
	}

	// 2. Uniform Distribution (Chi-Square Test across character set)
	// 36 characters in charset. Expected count per char = (50,000 * 6) / 36 = 8,333.33
	expectedPerChar := float64(numSamples*PairingCodeLength) / float64(len(PairingCodeCharset))
	var chiSquare float64
	for _, ch := range PairingCodeCharset {
		cnt := float64(charCounts[ch])
		diff := cnt - expectedPerChar
		chiSquare += (diff * diff) / expectedPerChar
	}
	// Degrees of freedom = 35. Critical value for p = 0.001 (df=35) is ~66.5
	t.Logf("Chi-Square statistic for character distribution: %.4f (Expected around 35)", chiSquare)
	if chiSquare > 75.0 {
		t.Errorf("EMPIRICAL WARNING: Non-uniform character distribution detected (Chi-Square = %.4f)", chiSquare)
	}

	// 3. Format Bounds & Edge Cases Validation
	boundCases := []struct {
		input string
		want  bool
	}{
		{"ABCDEF", true},
		{"123456", true},
		{"000000", true},
		{"ZZZZZZ", true},
		{"abc-def", true},
		{" A1B2C3 ", true},
		{"A-B-C-D-E-F", true}, // Multiple hyphens handled by ReplaceAll
		{"ABCDE", false},     // 5 chars
		{"ABCDEFG", false},   // 7 chars
		{"ABCDE!", false},    // special char
		{"ABC DE", false},    // internal space
		{"", false},
		{"      ", false},
		{"\x00\x00\x00\x00\x00\x00", false},
	}

	for _, tc := range boundCases {
		got := ValidatePairingCode(tc.input)
		if got != tc.want {
			t.Errorf("ValidatePairingCode(%q) = %v; want %v", tc.input, got, tc.want)
		}
	}

	// 4. FormatPairingCode edge bounds
	if got := FormatPairingCode("123456"); got != "123-456" {
		t.Errorf("FormatPairingCode(123456) = %q; want 123-456", got)
	}
	if got := FormatPairingCode("TOO_LONG_STRING"); got != "TOO_LONG_STRING" {
		t.Errorf("FormatPairingCode(TOO_LONG_STRING) = %q; want TOO_LONG_STRING", got)
	}
}
