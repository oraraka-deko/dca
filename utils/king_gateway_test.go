package utils

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestKingGateway_Register_ValidHandshake(t *testing.T) {
	pm := NewPairingManager()
	_ = pm.AddPairingCode("ABC123", "node-001")
	token, _, err := pm.ValidateAndPair("ABC123")
	if err != nil {
		t.Fatalf("Failed to issue token: %v", err)
	}

	kg := NewKingGateway(nil)
	kg.PairingMgr = pm

	server := httptest.NewServer(http.HandlerFunc(kg.HandleRegister))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	headers := http.Header{}
	headers.Set("X-Node-ID", "node-001")
	headers.Set("Authorization", "Bearer "+token)

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Failed to establish WebSocket connection: %v (resp status: %v)", err, resp.Status)
	}
	defer conn.Close()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("Expected status 101 Switching Protocols, got %d", resp.StatusCode)
	}

	// Verify ActiveConns entry
	val, ok := kg.ActiveConns.Load("node-001")
	if !ok || val == nil {
		t.Fatal("Expected node-001 to be stored in ActiveConns")
	}

	wc := val.(*WorkerConn)
	if wc.NodeID != "node-001" {
		t.Errorf("Expected WorkerConn NodeID 'node-001', got %q", wc.NodeID)
	}
}

func TestKingGateway_Register_MissingNodeID(t *testing.T) {
	kg := NewKingGateway(nil)
	server := httptest.NewServer(http.HandlerFunc(kg.HandleRegister))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	headers := http.Header{}
	headers.Set("Authorization", "Bearer token-123")

	_, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err == nil {
		t.Fatal("Expected error due to missing X-Node-ID, got nil")
	}
	if resp != nil && resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected HTTP 400 Bad Request, got %d", resp.StatusCode)
	}
}

func TestKingGateway_Register_MissingAuth(t *testing.T) {
	kg := NewKingGateway(nil)
	server := httptest.NewServer(http.HandlerFunc(kg.HandleRegister))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	headers := http.Header{}
	headers.Set("X-Node-ID", "node-001")

	_, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err == nil {
		t.Fatal("Expected error due to missing Authorization header, got nil")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected HTTP 401 Unauthorized, got %d", resp.StatusCode)
	}
}

func TestKingGateway_Register_InvalidToken(t *testing.T) {
	validator := func(nodeID, token string) bool {
		return token == "valid-token" && nodeID == "node-001"
	}
	kg := NewKingGateway(validator)
	server := httptest.NewServer(http.HandlerFunc(kg.HandleRegister))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	headers := http.Header{}
	headers.Set("X-Node-ID", "node-001")
	headers.Set("Authorization", "Bearer invalid-token")

	_, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err == nil {
		t.Fatal("Expected error due to invalid token, got nil")
	}
	if resp != nil && resp.StatusCode != http.StatusForbidden {
		t.Errorf("Expected HTTP 403 Forbidden, got %d", resp.StatusCode)
	}
}

func TestKingGateway_Register_HeaderSanitization(t *testing.T) {
	validator := func(nodeID, token string) bool {
		return nodeID == "node-clean" && token == "token-clean"
	}
	kg := NewKingGateway(validator)
	server := httptest.NewServer(http.HandlerFunc(kg.HandleRegister))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	headers := http.Header{}
	// Case-insensitive header keys with leading/trailing spaces
	headers.Set("x-node-id", "  node-clean  ")
	headers.Set("authorization", "  Bearer   token-clean   ")

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Header sanitization failed to accept valid credentials: %v", err)
	}
	defer conn.Close()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("Expected HTTP 101, got %d", resp.StatusCode)
	}
}

func TestKingGateway_Register_ReconnectionPreemption(t *testing.T) {
	validator := func(nodeID, token string) bool { return true }
	kg := NewKingGateway(validator)
	server := httptest.NewServer(http.HandlerFunc(kg.HandleRegister))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	headers := http.Header{}
	headers.Set("X-Node-ID", "node-reconnect")
	headers.Set("Authorization", "Bearer token-123")

	conn1, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("First connection failed: %v", err)
	}
	defer conn1.Close()

	time.Sleep(20 * time.Millisecond)

	// Second connection with same node-id
	conn2, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Second connection failed: %v", err)
	}
	defer conn2.Close()

	time.Sleep(50 * time.Millisecond)

	// Verify ActiveConns points to conn2
	val, ok := kg.ActiveConns.Load("node-reconnect")
	if !ok || val == nil {
		t.Fatal("ActiveConns missing entry after reconnect")
	}
	wc := val.(*WorkerConn)
	if wc.Conn == conn1 {
		t.Error("ActiveConns still holds conn1; expected preemption by conn2")
	}
}

func TestKingGateway_ProtocolInversionHandshake(t *testing.T) {
	kg := NewKingGateway(func(nodeID, token string) bool { return true })
	server := httptest.NewServer(http.HandlerFunc(kg.HandleRegister))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	headers := http.Header{}
	headers.Set("X-Node-ID", "node-handshake")
	headers.Set("Authorization", "Bearer token-init")

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	// Read first frame (initialize)
	_, p1, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("Failed reading initialize request: %v", err)
	}

	var req1 JSONRPCRequest
	if err := json.Unmarshal(p1, &req1); err != nil {
		t.Fatalf("Invalid initialize JSON-RPC payload: %v", err)
	}
	if req1.Method != "initialize" {
		t.Errorf("Expected method 'initialize', got %q", req1.Method)
	}

	// Read second frame (tools/list)
	_, p2, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("Failed reading tools/list request: %v", err)
	}

	var req2 JSONRPCRequest
	if err := json.Unmarshal(p2, &req2); err != nil {
		t.Fatalf("Invalid tools/list JSON-RPC payload: %v", err)
	}
	if req2.Method != "tools/list" {
		t.Errorf("Expected method 'tools/list', got %q", req2.Method)
	}
}

func TestKingGateway_ConcurrentWrites(t *testing.T) {
	kg := NewKingGateway(func(nodeID, token string) bool { return true })
	server := httptest.NewServer(http.HandlerFunc(kg.HandleRegister))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	headers := http.Header{}
	headers.Set("X-Node-ID", "node-concurrent")
	headers.Set("Authorization", "Bearer token-conc")

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	time.Sleep(20 * time.Millisecond)

	val, ok := kg.ActiveConns.Load("node-concurrent")
	if !ok {
		t.Fatal("Node missing from ActiveConns")
	}
	wc := val.(*WorkerConn)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			req := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      id,
				"method":  "ping",
			}
			_ = wc.WriteJSONRPC(req)
		}(i)
	}

	wg.Wait()
}
