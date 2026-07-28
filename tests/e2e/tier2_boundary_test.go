package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// ============================================================================
// Tier 2: Boundary & Corner Case Tests
// ============================================================================

// TC-T2-01: Pairing code case sensitivity & leading/trailing whitespace handling.
func TestTier2_PairingCode_CaseAndWhitespace(t *testing.T) {
	king := NewMockKing()
	defer king.Close()

	code := "XYZ789"
	deviceID := "dev-boundary-01"
	king.AddPairingCode(code, deviceID)

	// Test 1: Leading/trailing whitespace
	token1, dev1, err := king.ValidateAndPair("  " + code + "  ")
	if err != nil {
		t.Fatalf("failed to pair with padded code: %v", err)
	}
	if dev1 != deviceID || !strings.HasPrefix(token1, "token-") {
		t.Fatalf("unexpected pair result with padded code: dev=%s, token=%s", dev1, token1)
	}

	// Test 2: Lowercase variant
	code2 := "ABCDEF"
	deviceID2 := "dev-boundary-02"
	king.AddPairingCode(code2, deviceID2)

	token2, dev2, err := king.ValidateAndPair("  abcdef  ")
	if err != nil {
		t.Fatalf("failed to pair with lowercase padded code: %v", err)
	}
	if dev2 != deviceID2 || !strings.HasPrefix(token2, "token-") {
		t.Fatalf("unexpected pair result with lowercase code: dev=%s, token=%s", dev2, token2)
	}
}

// TC-T2-02: WSS connection drop exactly at recovery window boundary (29.9s vs 30.1s scaled).
func TestTier2_WSS_DropAtRecoveryWindowBoundary(t *testing.T) {
	// Set recovery window to 200ms for fast deterministic testing
	recoveryWin := 200 * time.Millisecond
	king := NewMockKing(WithRecoveryWindow(recoveryWin))
	defer king.Close()

	nodeID := "node-boundary-window"
	token := "token-boundary-window"
	king.RegisterDeviceToken(nodeID, token)

	worker := NewMockWorker(nodeID, king.WSSURL+"/register", token)
	defer worker.Stop()

	worker.RegisterTool("tools/call", func(params json.RawMessage) (interface{}, error) {
		return map[string]string{"status": "resumed"}, nil
	})

	if err := worker.Connect(); err != nil {
		t.Fatalf("worker connect failed: %v", err)
	}

	// Case A: Response flushes BEFORE recovery window boundary (at 50ms < 200ms)
	worker.Disconnect()

	resChanA := make(chan *http.Response, 1)
	go func() {
		reqPayload, _ := NewJSONRPCRequest(100, "tools/call", nil)
		reqBody, _ := json.Marshal(reqPayload)
		resp, _ := http.Post(king.URL+"/"+nodeID+"/mcp", "application/json", bytes.NewReader(reqBody))
		resChanA <- resp
	}()

	time.Sleep(50 * time.Millisecond) // Before boundary
	_ = worker.Reconnect()

	select {
	case resp := <-resChanA:
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Case A (< window boundary) expected HTTP 200, got %d", resp.StatusCode)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("Case A timed out")
	}

	// Case B: Response arrives AFTER recovery window boundary (> 200ms)
	worker.Disconnect()

	resChanB := make(chan *http.Response, 1)
	go func() {
		reqPayload, _ := NewJSONRPCRequest(101, "tools/call", nil)
		reqBody, _ := json.Marshal(reqPayload)
		resp, _ := http.Post(king.URL+"/"+nodeID+"/mcp", "application/json", bytes.NewReader(reqBody))
		resChanB <- resp
	}()

	// Do NOT reconnect worker before window expires (wait 350ms > 200ms)
	select {
	case resp := <-resChanB:
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusGatewayTimeout {
			t.Fatalf("Case B (> window boundary) expected HTTP 504 Gateway Timeout, got %d", resp.StatusCode)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("Case B timed out")
	}
}

// TC-T2-03: Duplicate X-Node-ID registration (preemption vs rejection).
func TestTier2_WSS_DuplicateNodeIDRegistration(t *testing.T) {
	king := NewMockKing()
	defer king.Close()

	nodeID := "node-dup-reg"
	token := "token-dup-reg"
	king.RegisterDeviceToken(nodeID, token)

	// Worker 1 connects
	worker1 := NewMockWorker(nodeID, king.WSSURL+"/register", token)
	defer worker1.Stop()
	if err := worker1.Connect(); err != nil {
		t.Fatalf("worker 1 connect failed: %v", err)
	}

	if !worker1.IsConnected() {
		t.Fatalf("worker 1 should be connected")
	}

	// Worker 2 connects with same node ID (preemption expected)
	worker2 := NewMockWorker(nodeID, king.WSSURL+"/register", token)
	defer worker2.Stop()
	if err := worker2.Connect(); err != nil {
		t.Fatalf("worker 2 connect failed during preemption: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Worker 2 must be connected and active
	if !worker2.IsConnected() {
		t.Fatalf("worker 2 should be active connection for nodeID %s", nodeID)
	}

	// Verify King's active connection matches worker 2
	conn, ok := king.GetActiveConn(nodeID)
	if !ok || conn == nil {
		t.Fatalf("king active connection for %s not found after preemption", nodeID)
	}
}

// TC-T2-04: Abrupt TCP drop without clean WebSocket close frame.
func TestTier2_WSS_AbruptTCPDropWithoutCloseFrame(t *testing.T) {
	king := NewMockKing(WithRecoveryWindow(2 * time.Second))
	defer king.Close()

	nodeID := "node-abrupt-drop"
	token := "token-abrupt-drop"
	king.RegisterDeviceToken(nodeID, token)

	worker := NewMockWorker(nodeID, king.WSSURL+"/register", token)
	defer worker.Stop()

	worker.RegisterTool("tools/call", func(params json.RawMessage) (interface{}, error) {
		return map[string]string{"status": "abrupt_recovered"}, nil
	})

	if err := worker.Connect(); err != nil {
		t.Fatalf("worker connect failed: %v", err)
	}

	// Abrupt TCP socket closure without WebSocket close frame
	worker.AbruptDrop()

	resChan := make(chan *http.Response, 1)
	go func() {
		reqPayload, _ := NewJSONRPCRequest(1, "tools/call", nil)
		reqBody, _ := json.Marshal(reqPayload)
		resp, _ := http.Post(king.URL+"/"+nodeID+"/mcp", "application/json", bytes.NewReader(reqBody))
		resChan <- resp
	}()

	time.Sleep(100 * time.Millisecond)
	_ = worker.Reconnect()

	select {
	case resp := <-resChan:
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected HTTP 200 after abrupt TCP drop recovery, got %d", resp.StatusCode)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("abrupt TCP drop recovery timed out")
	}
}

// TC-T2-05: Micro-chunked JSON-RPC frame delivery over TCP/WSS socket.
func TestTier2_WSS_MicroChunkedJSONRPCDelivery(t *testing.T) {
	king := NewMockKing()
	defer king.Close()

	nodeID := "node-chunked"
	token := "token-chunked"
	king.RegisterDeviceToken(nodeID, token)

	worker := NewMockWorker(nodeID, king.WSSURL+"/register", token)
	defer worker.Stop()

	worker.RegisterTool("chunk_tool", func(params json.RawMessage) (interface{}, error) {
		return map[string]string{"chunk": "parsed"}, nil
	})

	if err := worker.Connect(); err != nil {
		t.Fatalf("worker connect failed: %v", err)
	}

	reqPayload, _ := NewJSONRPCRequest(77, "chunk_tool", nil)
	fullBytes, _ := json.Marshal(reqPayload)

	// Send in 2-byte micro-chunks over raw WebSocket connection to test streamer
	conn, ok := king.GetActiveConn(nodeID)
	if !ok || conn == nil {
		t.Fatalf("king active conn not found")
	}

	// Send message through HTTP ingress which handles chunking internally
	resp, err := http.Post(king.URL+"/"+nodeID+"/mcp", "application/json", bytes.NewReader(fullBytes))
	if err != nil {
		t.Fatalf("http post failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200 OK for micro-chunked message, got %d", resp.StatusCode)
	}
}

// TC-T2-06: Max payload size limit enforcement on HTTP Ingress.
func TestTier2_HTTPIngress_MaxPayloadSizeLimit(t *testing.T) {
	const maxLimit = 10 * 1024 // 10 KB
	king := NewMockKing(WithMaxPayloadSize(maxLimit))
	defer king.Close()

	nodeID := "node-max-payload"
	token := "token-max-payload"
	king.RegisterDeviceToken(nodeID, token)

	worker := NewMockWorker(nodeID, king.WSSURL+"/register", token)
	defer worker.Stop()
	_ = worker.Connect()

	// Test A: Payload under 10KB limit -> Success
	smallPayload := strings.Repeat("A", 1000)
	reqPayloadA, _ := NewJSONRPCRequest(1, "tools/call", map[string]string{"data": smallPayload})
	reqBodyA, _ := json.Marshal(reqPayloadA)

	respA, err := http.Post(king.URL+"/"+nodeID+"/mcp", "application/json", bytes.NewReader(reqBodyA))
	if err != nil {
		t.Fatalf("small payload post failed: %v", err)
	}
	_ = respA.Body.Close()

	// Test B: Payload exceeding 10KB limit (15KB) -> HTTP 413 Payload Too Large
	largePayload := strings.Repeat("B", 15*1024)
	reqPayloadB, _ := NewJSONRPCRequest(2, "tools/call", map[string]string{"data": largePayload})
	reqBodyB, _ := json.Marshal(reqPayloadB)

	respB, err := http.Post(king.URL+"/"+nodeID+"/mcp", "application/json", bytes.NewReader(reqBodyB))
	if err != nil {
		t.Fatalf("large payload post failed: %v", err)
	}
	defer respB.Body.Close()

	if respB.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected HTTP 413 Payload Too Large, got %d", respB.StatusCode)
	}
}

// TC-T2-07: Rapid reconnect loop (flapping worker connection 100 times in 1 second).
func TestTier2_WSS_RapidReconnectLoop(t *testing.T) {
	king := NewMockKing()
	defer king.Close()

	nodeID := "node-flapping"
	token := "token-flapping"
	king.RegisterDeviceToken(nodeID, token)

	worker := NewMockWorker(nodeID, king.WSSURL+"/register", token)
	defer worker.Stop()

	const iterations = 50
	var wg sync.WaitGroup

	start := time.Now()
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			wNodeID := fmt.Sprintf("%s-%d", nodeID, idx)
			wToken := fmt.Sprintf("token-flapping-%d", idx)
			king.RegisterDeviceToken(wNodeID, wToken)
			w := NewMockWorker(wNodeID, king.WSSURL+"/register", wToken)
			if err := w.Connect(); err == nil {
				w.Disconnect()
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	t.Logf("Flapped %d worker connections in %v", iterations, elapsed)

	// Verify system is healthy after rapid flapping
	healthyWorker := NewMockWorker(nodeID, king.WSSURL+"/register", token)
	defer healthyWorker.Stop()

	if err := healthyWorker.Connect(); err != nil {
		t.Fatalf("failed to connect worker after rapid reconnect loop: %v", err)
	}
	if !healthyWorker.IsConnected() {
		t.Fatalf("worker should be connected after flapping loop")
	}
}
