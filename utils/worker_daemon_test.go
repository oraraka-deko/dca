package utils

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWorkerDaemon_LifecycleAndHeaders(t *testing.T) {
	headerChan := make(chan http.Header, 1)

	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headerChan <- r.Header.Clone()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Keep connection open until context cancelled
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
		NodeID:            "test-node-777",
		AuthToken:         "secret-bearer-token",
		ReconnectInterval: 50 * time.Millisecond,
		MaxOutboxSize:     100,
	}

	daemon := NewWorkerDaemon(cfg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := daemon.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer daemon.Stop()

	select {
	case h := <-headerChan:
		if got := h.Get("X-Node-ID"); got != "test-node-777" {
			t.Errorf("X-Node-ID header = %q; want %q", got, "test-node-777")
		}
		if got := h.Get("Authorization"); got != "Bearer secret-bearer-token" {
			t.Errorf("Authorization header = %q; want %q", got, "Bearer secret-bearer-token")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for WebSocket handshake")
	}

	// Verify connected state
	for i := 0; i < 20; i++ {
		if daemon.IsConnected() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !daemon.IsConnected() {
		t.Error("Expected daemon.IsConnected() to be true")
	}
}

func TestWorkerDaemon_ToolExecutionAndOutboxFlush(t *testing.T) {
	upgrader := websocket.Upgrader{}
	receivedChan := make(chan JSONRPCResponse, 10)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send a JSON-RPC request to the worker
		req := JSONRPCRequest{
			JSONRPC: "2.0",
			ID:      "req-101",
			Method:  "ping",
		}
		reqBytes, _ := json.Marshal(req)
		_ = conn.WriteMessage(websocket.TextMessage, reqBytes)

		// Read responses from worker
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				break
			}
			var resp JSONRPCResponse
			if err := json.Unmarshal(msg, &resp); err == nil {
				receivedChan <- resp
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	mcpWrapper := NewMCPServerWrapper(ServerConfig{Port: 0})

	cfg := WorkerDaemonConfig{
		KingURL:           wsURL,
		NodeID:            "test-worker-node",
		AuthToken:         "test-token",
		ReconnectInterval: 50 * time.Millisecond,
		ExecutionTimeout:  5 * time.Second,
	}

	daemon := NewWorkerDaemon(cfg, mcpWrapper)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := daemon.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer daemon.Stop()

	select {
	case resp := <-receivedChan:
		if resp.ID != "req-101" {
			t.Errorf("Response ID = %v; want req-101", resp.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Timeout waiting for JSON-RPC response from WorkerDaemon")
	}
}

func TestWorkerDaemon_SessionResumptionOnDisconnect(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var connMu sync.Mutex
	var activeConn *websocket.Conn

	receivedResponses := make([]JSONRPCResponse, 0)
	var respMu sync.Mutex

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		connMu.Lock()
		activeConn = conn
		connMu.Unlock()

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				break
			}
			var resp JSONRPCResponse
			if err := json.Unmarshal(msg, &resp); err == nil {
				respMu.Lock()
				receivedResponses = append(receivedResponses, resp)
				respMu.Unlock()
			}
		}
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	cfg := WorkerDaemonConfig{
		KingURL:           wsURL,
		NodeID:            "resumption-node",
		AuthToken:         "token-1",
		ReconnectInterval: 50 * time.Millisecond,
	}

	daemon := NewWorkerDaemon(cfg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = daemon.Start(ctx)
	defer daemon.Stop()

	// Wait for connection
	for i := 0; i < 20; i++ {
		if daemon.IsConnected() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Force close socket to simulate network drop
	connMu.Lock()
	if activeConn != nil {
		activeConn.Close()
	}
	connMu.Unlock()

	// Enqueue responses directly into Outbox while disconnected
	_ = daemon.Outbox.Enqueue(OutboxItem{
		ID:      "offline-1",
		Payload: json.RawMessage(`{"jsonrpc":"2.0","id":"offline-1","result":"data1"}`),
	})
	_ = daemon.Outbox.Enqueue(OutboxItem{
		ID:      "offline-2",
		Payload: json.RawMessage(`{"jsonrpc":"2.0","id":"offline-2","result":"data2"}`),
	})

	// Wait for reconnect and flush
	time.Sleep(400 * time.Millisecond)

	respMu.Lock()
	defer respMu.Unlock()

	foundOffline1 := false
	foundOffline2 := false
	for _, r := range receivedResponses {
		if r.ID == "offline-1" {
			foundOffline1 = true
		}
		if r.ID == "offline-2" {
			foundOffline2 = true
		}
	}

	if !foundOffline1 || !foundOffline2 {
		t.Errorf("Expected both offline responses flushed upon reconnect. Got: %v", receivedResponses)
	}
}

func TestWorkerDaemon_ToolPanicRecovery(t *testing.T) {
	daemon := NewWorkerDaemon(WorkerDaemonConfig{}, nil)
	daemon.ctx = context.Background()

	// Simulate panic during request handling
	req := JSONRPCRequest{JSONRPC: "2.0", ID: "panic-req", Method: "panic_test"}
	daemon.executionWithTimeout([]byte(`{"jsonrpc":"2.0","id":"panic-req","method":"panic_test"}`), req)

	if daemon.Outbox.Len() != 1 {
		t.Fatalf("Expected 1 item in outbox after panic recovery, got %d", daemon.Outbox.Len())
	}

	item, _ := daemon.Outbox.Dequeue()
	var resp JSONRPCResponse
	_ = json.Unmarshal(item.Payload, &resp)

	if resp.Error == nil || !strings.Contains(resp.Error.Message, "Panic") {
		t.Errorf("Expected panic error message in response, got: %v", resp)
	}
}
