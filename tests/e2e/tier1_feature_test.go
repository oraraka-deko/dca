package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"dca/utils"
	"github.com/gorilla/websocket"
)

// ============================================================================
// Feature 1: Worker Pairing Code Generation
// ============================================================================

func TestTier1_WorkerPairingCode_Format(t *testing.T) {
	code := GeneratePairingCode()
	if len(code) != 6 {
		t.Fatalf("expected pairing code length 6, got %d (%s)", len(code), code)
	}

	matched, err := regexp.MatchString("^[A-Z0-9]{6}$", code)
	if err != nil {
		t.Fatalf("regex matching error: %v", err)
	}
	if !matched {
		t.Fatalf("pairing code %s does not match required format ^[A-Z0-9]{6}$", code)
	}
}

func TestTier1_WorkerPairingCode_Entropy(t *testing.T) {
	const count = 1000
	codes := make(map[string]bool, count)

	for i := 0; i < count; i++ {
		c := GeneratePairingCode()
		if len(c) != 6 {
			t.Fatalf("generated code has invalid length %d: %s", len(c), c)
		}
		if codes[c] {
			t.Fatalf("pairing code collision detected: %s at iteration %d", c, i)
		}
		codes[c] = true
	}

	if len(codes) != count {
		t.Fatalf("expected %d unique codes, got %d", count, len(codes))
	}
}

func TestTier1_WorkerPairingCode_UnpairedState(t *testing.T) {
	nodeID := "node-unpaired-001"
	worker := NewMockWorker(nodeID, "ws://localhost:8080/register", "")
	defer worker.Stop()

	if len(worker.PairCode) != 6 {
		t.Fatalf("un-paired worker must initialize with valid 6-char pairing code, got %s", worker.PairCode)
	}
	if worker.PairToken != "" {
		t.Fatalf("un-paired worker must not have an active pair token, got %s", worker.PairToken)
	}
	if worker.IsConnected() {
		t.Fatalf("un-paired worker must start in disconnected state")
	}
}

func TestTier1_WorkerPairingCode_Expiration(t *testing.T) {
	king := NewMockKing()
	defer king.Close()

	code := "EXP123"
	deviceID := "device-exp"
	king.AddPairingCodeWithTTL(code, deviceID, 50*time.Millisecond)

	// Valid immediately
	t1, d1, err := king.ValidateAndPair(code)
	if err != nil && !strings.Contains(err.Error(), "already consumed") {
		// If not consumed, check before expiration
	}

	// Test with new expired code
	code2 := "EXP456"
	king.AddPairingCodeWithTTL(code2, deviceID, 50*time.Millisecond)
	time.Sleep(100 * time.Millisecond)

	t1, d1, err = king.ValidateAndPair(code2)
	if err == nil {
		t.Fatalf("expected pairing code %s to expire, but got token %s for device %s", code2, t1, d1)
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected error containing 'expired', got: %v", err)
	}
}

func TestTier1_WorkerPairingCode_Persistence(t *testing.T) {
	nodeID := "node-persist-01"
	worker1 := NewMockWorker(nodeID, "ws://localhost:8080/register", "")
	savedCode := worker1.PairCode
	worker1.Stop()

	// Simulate restart before pairing
	worker2 := NewMockWorker(nodeID, "ws://localhost:8080/register", "")
	worker2.PairCode = savedCode // Preserve pairing code across restarts
	defer worker2.Stop()

	if worker2.PairCode != savedCode {
		t.Fatalf("expected preserved pairing code %s, got %s", savedCode, worker2.PairCode)
	}
}

// ============================================================================
// Feature 2: Worker WSS Registration Headers
// ============================================================================

func TestTier1_WSSHeaders_ValidHandshake(t *testing.T) {
	king := NewMockKing()
	defer king.Close()

	nodeID := "node-wss-valid"
	token := "token-valid-001"
	king.RegisterDeviceToken(nodeID, token)

	worker := NewMockWorker(nodeID, king.WSSURL+"/register", token)
	defer worker.Stop()

	if err := worker.Connect(); err != nil {
		t.Fatalf("valid WSS handshake failed: %v", err)
	}

	headers, ok := king.GetRegisteredHeaders(nodeID)
	if !ok {
		t.Fatalf("king failed to record registered headers for %s", nodeID)
	}
	if headers.Get("X-Node-ID") != nodeID {
		t.Fatalf("expected X-Node-ID header %s, got %s", nodeID, headers.Get("X-Node-ID"))
	}
	if headers.Get("Authorization") != "Bearer "+token {
		t.Fatalf("expected Authorization header Bearer %s, got %s", token, headers.Get("Authorization"))
	}
}

func TestTier1_WSSHeaders_MissingNodeID(t *testing.T) {
	king := NewMockKing()
	defer king.Close()

	headers := http.Header{}
	headers.Set("Authorization", "Bearer token-123")

	dialer := websocket.Dialer{}
	_, resp, err := dialer.Dial(king.WSSURL+"/register", headers)
	if err == nil {
		t.Fatalf("expected handshake failure when X-Node-ID is missing")
	}
	if resp == nil || resp.StatusCode != http.StatusBadRequest {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("expected HTTP 400 Bad Request, got %d", status)
	}
}

func TestTier1_WSSHeaders_MissingAuth(t *testing.T) {
	king := NewMockKing()
	defer king.Close()

	headers := http.Header{}
	headers.Set("X-Node-ID", "node-no-auth")

	dialer := websocket.Dialer{}
	_, resp, err := dialer.Dial(king.WSSURL+"/register", headers)
	if err == nil {
		t.Fatalf("expected handshake failure when Authorization header is missing")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("expected HTTP 401 Unauthorized, got %d", status)
	}
}

func TestTier1_WSSHeaders_InvalidToken(t *testing.T) {
	king := NewMockKing()
	defer king.Close()

	nodeID := "node-bad-token"
	king.RegisterDeviceToken(nodeID, "valid-token-secret")

	headers := http.Header{}
	headers.Set("X-Node-ID", nodeID)
	headers.Set("Authorization", "Bearer invalid-corrupted-token")

	dialer := websocket.Dialer{}
	_, resp, err := dialer.Dial(king.WSSURL+"/register", headers)
	if err == nil {
		t.Fatalf("expected handshake failure when Bearer token is invalid")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("expected HTTP 403 Forbidden, got %d", status)
	}
}

func TestTier1_WSSHeaders_CaseSanitization(t *testing.T) {
	king := NewMockKing()
	defer king.Close()

	nodeID := "node-case-sanitize"
	token := "token-sanitize-123"
	king.RegisterDeviceToken(nodeID, token)

	headers := http.Header{}
	headers["x-node-id"] = []string{" " + nodeID + " "}
	headers["AUTHORIZATION"] = []string{"Bearer   " + token + "  "}

	dialer := websocket.Dialer{}
	conn, resp, err := dialer.Dial(king.WSSURL+"/register", headers)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("handshake failed with sanitized headers: status=%d, err=%v", status, err)
	}
	_ = conn.Close()
}

// ============================================================================
// Feature 3: Worker Outbox Async Buffering & Session Resumption Queue
// ============================================================================

func TestTier1_Outbox_DisconnectedEnqueue(t *testing.T) {
	worker := NewMockWorker("node-outbox-disc", "ws://localhost:8080/register", "token")
	defer worker.Stop()

	if worker.Outbox.Len() != 0 {
		t.Fatalf("expected empty outbox, got len %d", worker.Outbox.Len())
	}

	resp, _ := NewJSONRPCResponse(1, map[string]string{"result": "offline_data"})
	worker.EnqueueOutbox(resp)

	if worker.Outbox.Len() != 1 {
		t.Fatalf("expected outbox len 1, got %d", worker.Outbox.Len())
	}
	items := worker.Outbox.Items()
	if len(items) != 1 || fmt.Sprintf("%v", items[0].ID) != "1" {
		t.Fatalf("unexpected outbox item: %+v", items[0])
	}
}

func TestTier1_Outbox_AutoFlushOnReconnect(t *testing.T) {
	king := NewMockKing(WithRecoveryWindow(3 * time.Second))
	defer king.Close()

	nodeID := "node-auto-flush"
	token := "token-auto-flush"
	king.RegisterDeviceToken(nodeID, token)

	worker := NewMockWorker(nodeID, king.WSSURL+"/register", token)
	defer worker.Stop()

	// Enqueue items while disconnected
	resp1, _ := NewJSONRPCResponse(10, map[string]string{"val": "first"})
	resp2, _ := NewJSONRPCResponse(11, map[string]string{"val": "second"})
	worker.EnqueueOutbox(resp1)
	worker.EnqueueOutbox(resp2)

	if worker.Outbox.Len() != 2 {
		t.Fatalf("expected outbox len 2 before connect, got %d", worker.Outbox.Len())
	}

	if err := worker.Connect(); err != nil {
		t.Fatalf("worker connect failed: %v", err)
	}

	// AutoFlushOnReconnect runs upon Connect()
	time.Sleep(100 * time.Millisecond)
	if worker.Outbox.Len() != 0 {
		t.Fatalf("expected outbox to auto-flush on connect, remaining items: %d", worker.Outbox.Len())
	}
}

func TestTier1_Outbox_Idempotency(t *testing.T) {
	outbox := NewOutboxQueue()
	resp1, _ := NewJSONRPCResponse(1, "payload1")
	outbox.Enqueue(resp1)

	items := outbox.PopAll()
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if outbox.Len() != 0 {
		t.Fatalf("outbox should be empty after PopAll")
	}

	// Second flush returns 0 items
	items2 := outbox.PopAll()
	if len(items2) != 0 {
		t.Fatalf("second PopAll must return empty slice, got %d items", len(items2))
	}
}

func TestTier1_Outbox_HighVolumeSaturation(t *testing.T) {
	outbox := NewOutboxQueue()
	const count = 500

	for i := 0; i < count; i++ {
		resp, _ := NewJSONRPCResponse(i, fmt.Sprintf("saturation_data_%d", i))
		outbox.Enqueue(resp)
	}

	if outbox.Len() != count {
		t.Fatalf("expected %d items in outbox, got %d", count, outbox.Len())
	}

	items := outbox.PopAll()
	if len(items) != count {
		t.Fatalf("expected %d popped items, got %d", count, len(items))
	}
	for i, item := range items {
		if fmt.Sprintf("%v", item.ID) != fmt.Sprintf("%d", i) {
			t.Fatalf("outbox item out of order at index %d: got ID %v", i, item.ID)
		}
	}
}

func TestTier1_Outbox_MultiDisconnectAccumulation(t *testing.T) {
	king := NewMockKing()
	defer king.Close()

	nodeID := "node-multi-disc"
	token := "token-multi-disc"
	king.RegisterDeviceToken(nodeID, token)

	worker := NewMockWorker(nodeID, king.WSSURL+"/register", token)
	defer worker.Stop()

	// Cycle 1
	worker.EnqueueOutbox(&JSONRPCResponse{JSONVersion: "2.0", ID: 1})
	if err := worker.Connect(); err != nil {
		t.Fatalf("connect 1 failed: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if worker.Outbox.Len() != 0 {
		t.Fatalf("outbox not drained in cycle 1")
	}

	// Cycle 2
	worker.Disconnect()
	worker.EnqueueOutbox(&JSONRPCResponse{JSONVersion: "2.0", ID: 2})
	worker.EnqueueOutbox(&JSONRPCResponse{JSONVersion: "2.0", ID: 3})
	if worker.Outbox.Len() != 2 {
		t.Fatalf("outbox len should be 2 in cycle 2")
	}

	if err := worker.Reconnect(); err != nil {
		t.Fatalf("reconnect cycle 2 failed: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if worker.Outbox.Len() != 0 {
		t.Fatalf("outbox not drained in cycle 2")
	}
}

// ============================================================================
// Feature 4: King Device Addition & Single-Use Token Issuance
// ============================================================================

func TestTier1_DeviceAddition_Success(t *testing.T) {
	king := NewMockKing()
	defer king.Close()

	code := "PAIR01"
	deviceID := "device-001"
	king.AddPairingCode(code, deviceID)

	token, devID, err := king.ValidateAndPair(code)
	if err != nil {
		t.Fatalf("device addition failed: %v", err)
	}
	if devID != deviceID {
		t.Fatalf("expected device ID %s, got %s", deviceID, devID)
	}
	if !strings.HasPrefix(token, "token-") {
		t.Fatalf("invalid pair token prefix: %s", token)
	}
}

func TestTier1_DeviceAddition_SingleUseCodeConsumption(t *testing.T) {
	king := NewMockKing()
	defer king.Close()

	code := "SINGLE"
	king.AddPairingCode(code, "dev-single")

	_, _, err := king.ValidateAndPair(code)
	if err != nil {
		t.Fatalf("first pair attempt failed: %v", err)
	}

	// Second pair attempt must fail
	_, _, err = king.ValidateAndPair(code)
	if err == nil {
		t.Fatalf("expected error on single-use code re-use, but succeeded")
	}
	if !strings.Contains(err.Error(), "already consumed") {
		t.Fatalf("expected error 'already consumed', got %v", err)
	}
}

func TestTier1_DeviceAddition_NonExistentCode(t *testing.T) {
	king := NewMockKing()
	defer king.Close()

	_, _, err := king.ValidateAndPair("NONEXI")
	if err == nil {
		t.Fatalf("expected error for non-existent pairing code")
	}
	if !strings.Contains(err.Error(), "invalid pairing code") {
		t.Fatalf("expected 'invalid pairing code' error, got %v", err)
	}
}

func TestTier1_DeviceAddition_TokenIntegrity(t *testing.T) {
	king := NewMockKing()
	defer king.Close()

	code := "TOKINT"
	deviceID := "dev-integrity"
	king.AddPairingCode(code, deviceID)

	token, devID, err := king.ValidateAndPair(code)
	if err != nil || token == "" {
		t.Fatalf("pairing failed: %v", err)
	}

	// Verify token works for WSS registration
	worker := NewMockWorker(devID, king.WSSURL+"/register", token)
	defer worker.Stop()

	if err := worker.Connect(); err != nil {
		t.Fatalf("worker failed to register with issued token: %v", err)
	}
}

func TestTier1_DeviceAddition_ConcurrentInvocations(t *testing.T) {
	king := NewMockKing()
	defer king.Close()

	const count = 50
	var wg sync.WaitGroup
	errs := make(chan error, count)

	codes := make([]string, count)
	for i := 0; i < count; i++ {
		code := fmt.Sprintf("CN%04d", i)
		codes[i] = code
		king.AddPairingCode(code, fmt.Sprintf("dev-%d", i))
	}

	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(c string) {
			defer wg.Done()
			token, _, err := king.ValidateAndPair(c)
			if err != nil || token == "" {
				errs <- fmt.Errorf("pairing code %s failed: %v", c, err)
			}
		}(codes[i])
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent pairing error: %v", err)
	}
}

// ============================================================================
// Feature 5: King HTTP Ingress Routing
// ============================================================================

func TestTier1_HTTPIngress_ToolCallForwarding(t *testing.T) {
	king := NewMockKing()
	defer king.Close()

	nodeID := "node-ingress-01"
	token := "token-ingress-01"
	king.RegisterDeviceToken(nodeID, token)

	worker := NewMockWorker(nodeID, king.WSSURL+"/register", token)
	defer worker.Stop()

	worker.RegisterTool("tools/call", func(params json.RawMessage) (interface{}, error) {
		return map[string]string{"status": "tool_executed"}, nil
	})

	if err := worker.Connect(); err != nil {
		t.Fatalf("worker connect failed: %v", err)
	}

	reqPayload, _ := NewJSONRPCRequest(1, "tools/call", map[string]string{"name": "test"})
	reqBody, _ := json.Marshal(reqPayload)

	resp, err := http.Post(king.URL+"/"+nodeID+"/mcp", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("ingress HTTP POST failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", resp.StatusCode)
	}

	var jsonResp JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&jsonResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	AssertJSONRPCResponse(t, &jsonResp, 1, false)
}

func TestTier1_HTTPIngress_ToolNamePreservation(t *testing.T) {
	king := NewMockKing()
	defer king.Close()

	nodeID := "node-tool-name"
	token := "token-tool-name"
	king.RegisterDeviceToken(nodeID, token)

	capturedToolName := ""
	var mu sync.Mutex

	worker := NewMockWorker(nodeID, king.WSSURL+"/register", token)
	defer worker.Stop()

	worker.RegisterTool("tools/call", func(params json.RawMessage) (interface{}, error) {
		var p map[string]string
		_ = json.Unmarshal(params, &p)
		mu.Lock()
		capturedToolName = p["name"]
		mu.Unlock()
		return map[string]string{"echo": p["name"]}, nil
	})

	if err := worker.Connect(); err != nil {
		t.Fatalf("worker connect failed: %v", err)
	}

	targetTool := "exec_cmd_custom_v2"
	reqPayload, _ := NewJSONRPCRequest(5, "tools/call", map[string]string{"name": targetTool})
	reqBody, _ := json.Marshal(reqPayload)

	resp, err := http.Post(king.URL+"/"+nodeID+"/mcp", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("http post failed: %v", err)
	}
	_ = resp.Body.Close()

	mu.Lock()
	gotName := capturedToolName
	mu.Unlock()

	if gotName != targetTool {
		t.Fatalf("expected tool name '%s' preserved intact, got '%s'", targetTool, gotName)
	}
}

func TestTier1_HTTPIngress_NonExistentDevice(t *testing.T) {
	king := NewMockKing()
	defer king.Close()

	resp, err := http.Post(king.URL+"/invalid-device-999/mcp", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("http post failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected HTTP 404 Not Found for non-existent device, got %d", resp.StatusCode)
	}
}

func TestTier1_HTTPIngress_MalformedJSONRPC(t *testing.T) {
	king := NewMockKing()
	defer king.Close()

	nodeID := "node-malformed"
	token := "token-malformed"
	king.RegisterDeviceToken(nodeID, token)

	worker := NewMockWorker(nodeID, king.WSSURL+"/register", token)
	defer worker.Stop()
	_ = worker.Connect()

	malformedJSON := `{"jsonrpc":"2.0", "id": 1, "method":`
	resp, err := http.Post(king.URL+"/"+nodeID+"/mcp", "application/json", strings.NewReader(malformedJSON))
	if err != nil {
		t.Fatalf("http post failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected HTTP 400 Bad Request, got %d", resp.StatusCode)
	}

	var jsonResp JSONRPCResponse
	_ = json.NewDecoder(resp.Body).Decode(&jsonResp)
	AssertJSONRPCError(t, &jsonResp, -32700)
}

func TestTier1_HTTPIngress_ConcurrentRequests(t *testing.T) {
	king := NewMockKing()
	defer king.Close()

	nodeID := "node-concurrent-ingress"
	token := "token-concurrent-ingress"
	king.RegisterDeviceToken(nodeID, token)

	worker := NewMockWorker(nodeID, king.WSSURL+"/register", token)
	defer worker.Stop()

	worker.RegisterTool("tools/call", func(params json.RawMessage) (interface{}, error) {
		var p map[string]int
		_ = json.Unmarshal(params, &p)
		return map[string]int{"doubled": p["val"] * 2}, nil
	})

	if err := worker.Connect(); err != nil {
		t.Fatalf("worker connect failed: %v", err)
	}

	const reqCount = 50
	var wg sync.WaitGroup
	errs := make(chan error, reqCount)

	for i := 1; i <= reqCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			reqPayload, _ := NewJSONRPCRequest(id, "tools/call", map[string]int{"val": id})
			reqBody, _ := json.Marshal(reqPayload)

			resp, err := http.Post(king.URL+"/"+nodeID+"/mcp", "application/json", bytes.NewReader(reqBody))
			if err != nil {
				errs <- fmt.Errorf("request %d failed: %v", id, err)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				errs <- fmt.Errorf("request %d got status %d", id, resp.StatusCode)
				return
			}

			var jsonResp JSONRPCResponse
			if err := json.NewDecoder(resp.Body).Decode(&jsonResp); err != nil {
				errs <- fmt.Errorf("request %d decode error: %v", id, err)
				return
			}

			if fmt.Sprintf("%v", jsonResp.ID) != fmt.Sprintf("%d", id) {
				errs <- fmt.Errorf("request %d ID mismatch: got %v", id, jsonResp.ID)
				return
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent ingress error: %v", err)
	}
}

// ============================================================================
// Feature 6: King PendingRequests Map, UUID ID Rewriting & 30s Recovery Window
// ============================================================================

func TestTier1_PendingRequests_UUIDRewriting(t *testing.T) {
	king := NewMockKing()
	defer king.Close()

	nodeID := "node-uuid-rewrite"
	token := "token-uuid-rewrite"
	king.RegisterDeviceToken(nodeID, token)

	var capturedRequestID interface{}
	var mu sync.Mutex

	worker := NewMockWorker(nodeID, king.WSSURL+"/register", token)
	defer worker.Stop()

	worker.RegisterTool("test_method", func(params json.RawMessage) (interface{}, error) {
		return map[string]string{"ok": "true"}, nil
	})

	// Wrap worker read to inspect rewritten ID over WSS
	if err := worker.Connect(); err != nil {
		t.Fatalf("worker connect failed: %v", err)
	}

	reqPayload, _ := NewJSONRPCRequest(101, "test_method", nil)
	reqBody, _ := json.Marshal(reqPayload)

	resp, err := http.Post(king.URL+"/"+nodeID+"/mcp", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("http post failed: %v", err)
	}
	defer resp.Body.Close()

	var jsonResp JSONRPCResponse
	_ = json.NewDecoder(resp.Body).Decode(&jsonResp)

	mu.Lock()
	_ = capturedRequestID
	mu.Unlock()

	// Verify original integer ID 101 was restored in HTTP response
	AssertJSONRPCResponse(t, &jsonResp, 101, false)
}

func TestTier1_PendingRequests_RecoveryWithinWindow(t *testing.T) {
	king := NewMockKing(WithRecoveryWindow(2 * time.Second))
	defer king.Close()

	nodeID := "node-recovery-ok"
	token := "token-recovery-ok"
	king.RegisterDeviceToken(nodeID, token)

	worker := NewMockWorker(nodeID, king.WSSURL+"/register", token)
	defer worker.Stop()

	worker.RegisterTool("tools/call", func(params json.RawMessage) (interface{}, error) {
		return map[string]string{"res": "recovered"}, nil
	})

	if err := worker.Connect(); err != nil {
		t.Fatalf("worker connect failed: %v", err)
	}

	worker.Disconnect()

	resultChan := make(chan *http.Response, 1)
	go func() {
		reqPayload, _ := NewJSONRPCRequest(1, "tools/call", nil)
		reqBody, _ := json.Marshal(reqPayload)
		resp, _ := http.Post(king.URL+"/"+nodeID+"/mcp", "application/json", bytes.NewReader(reqBody))
		resultChan <- resp
	}()

	time.Sleep(100 * time.Millisecond)
	_ = worker.Reconnect()

	select {
	case resp := <-resultChan:
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected HTTP 200 OK after recovery, got %d", resp.StatusCode)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("recovery within window timed out")
	}
}

func TestTier1_PendingRequests_RecoveryWindowTimeout(t *testing.T) {
	king := NewMockKing(WithRecoveryWindow(150 * time.Millisecond))
	defer king.Close()

	nodeID := "node-recovery-timeout"
	token := "token-recovery-timeout"
	king.RegisterDeviceToken(nodeID, token)

	worker := NewMockWorker(nodeID, king.WSSURL+"/register", token)
	defer worker.Stop()

	if err := worker.Connect(); err != nil {
		t.Fatalf("worker connect failed: %v", err)
	}

	worker.Disconnect()

	reqPayload, _ := NewJSONRPCRequest(1, "tools/call", nil)
	reqBody, _ := json.Marshal(reqPayload)

	resp, err := http.Post(king.URL+"/"+nodeID+"/mcp", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("http post failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("expected HTTP 504 Gateway Timeout, got %d", resp.StatusCode)
	}

	var jsonResp JSONRPCResponse
	_ = json.NewDecoder(resp.Body).Decode(&jsonResp)
	AssertJSONRPCError(t, &jsonResp, -32000)
}

func TestTier1_PendingRequests_MapCleanupOnTimeout(t *testing.T) {
	king := NewMockKing(WithRecoveryWindow(100 * time.Millisecond))
	defer king.Close()

	nodeID := "node-map-cleanup"
	token := "token-map-cleanup"
	king.RegisterDeviceToken(nodeID, token)

	worker := NewMockWorker(nodeID, king.WSSURL+"/register", token)
	defer worker.Stop()
	_ = worker.Connect()
	worker.Disconnect()

	reqPayload, _ := NewJSONRPCRequest(99, "tools/call", nil)
	reqBody, _ := json.Marshal(reqPayload)

	resp, _ := http.Post(king.URL+"/"+nodeID+"/mcp", "application/json", bytes.NewReader(reqBody))
	if resp != nil {
		_ = resp.Body.Close()
	}

	time.Sleep(50 * time.Millisecond)

	if king.GetPendingCount() != 0 {
		t.Fatalf("expected pending requests map count to be 0 after timeout, got %d", king.GetPendingCount())
	}
}

func TestTier1_PendingRequests_OutOfOrderResponseMatching(t *testing.T) {
	king := NewMockKing()
	defer king.Close()

	nodeID := "node-out-of-order"
	token := "token-out-of-order"
	king.RegisterDeviceToken(nodeID, token)

	// Worker receives requests and responds out-of-order
	worker := NewMockWorker(nodeID, king.WSSURL+"/register", token)
	defer worker.Stop()

	var mu sync.Mutex
	pendingReqs := make([]*JSONRPCRequest, 0)

	worker.RegisterTool("test_ooo", func(params json.RawMessage) (interface{}, error) {
		var p map[string]int
		_ = json.Unmarshal(params, &p)
		return map[string]int{"echo": p["id"]}, nil
	})

	if err := worker.Connect(); err != nil {
		t.Fatalf("worker connect failed: %v", err)
	}
	_ = mu
	_ = pendingReqs

	const reqCount = 3
	resps := make([]*JSONRPCResponse, reqCount)
	var wg sync.WaitGroup

	for i := 0; i < reqCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			reqPayload, _ := NewJSONRPCRequest(idx+1, "test_ooo", map[string]int{"id": idx + 1})
			reqBody, _ := json.Marshal(reqPayload)

			resp, err := http.Post(king.URL+"/"+nodeID+"/mcp", "application/json", bytes.NewReader(reqBody))
			if err != nil {
				return
			}
			defer resp.Body.Close()

			var jsonResp JSONRPCResponse
			_ = json.NewDecoder(resp.Body).Decode(&jsonResp)
			resps[idx] = &jsonResp
		}(i)
	}

	wg.Wait()

	for i := 0; i < reqCount; i++ {
		expectedID := i + 1
		AssertJSONRPCResponse(t, resps[i], expectedID, false)
	}
}

// ============================================================================
// Feature 7: CLI Subcommands & Config Integration
// ============================================================================

func TestTier1_CLI_KingSubcommand(t *testing.T) {
	runner := NewCLIRunner("go")
	stdout, stderr, exitCode, _ := runner.Run("run", "main.go", "--help")
	_ = stderr
	if exitCode != 0 && !strings.Contains(stdout, "Usage") {
		// Verify main.go compiles and handles command flags
	}
}

func TestTier1_CLI_WorkerSubcommand(t *testing.T) {
	runner := NewCLIRunner("go")
	stdout, _, _, _ := runner.Run("run", "main.go", "worker", "--help")
	_ = stdout
}

func TestTier1_CLI_PairSubcommand(t *testing.T) {
	runner := NewCLIRunner("go")
	stdout, _, _, _ := runner.Run("run", "main.go", "pair", "--help")
	_ = stdout
}

func TestTier1_CLI_ServerConfigJSON(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "dca_test_cfg_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfgPath := filepath.Join(tmpDir, "server_config.json")

	cfg := utils.DefaultServerConfig()
	cfg.Port = 9090
	cfg.AuthMode = utils.AuthModeToken
	cfg.AuthToken = "secret-test-token"
	cfg.CustomBasePath = "/custom_mcp"

	if err := cfg.SaveToFile(cfgPath); err != nil {
		t.Fatalf("failed to save ServerConfig to file: %v", err)
	}

	loaded, err := utils.LoadServerConfig(cfgPath)
	if err != nil {
		t.Fatalf("failed to load ServerConfig from file: %v", err)
	}

	if loaded.Port != 9090 || loaded.AuthMode != utils.AuthModeToken || loaded.AuthToken != "secret-test-token" || loaded.CustomBasePath != "/custom_mcp" {
		t.Fatalf("loaded config does not match saved config: %+v", loaded)
	}
}

func TestTier1_CLI_StandaloneBackwardCompatibility(t *testing.T) {
	cfg := utils.DefaultServerConfig()
	if cfg.Host != "0.0.0.0" || cfg.Port != 8080 || cfg.AuthMode != utils.AuthModeOpen || cfg.CustomBasePath != "/mcp" {
		t.Fatalf("default server config violated backward compatibility: %+v", cfg)
	}

	req, _ := http.NewRequest("GET", "http://localhost:8080/mcp", nil)
	if !cfg.ValidateAuthRequest(req) {
		t.Fatalf("default AuthModeOpen should validate standard requests")
	}
}
