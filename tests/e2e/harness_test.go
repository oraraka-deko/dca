package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestJSONRPCHelpers(t *testing.T) {
	req, err := NewJSONRPCRequest(1, "tools/call", map[string]string{"name": "test_tool"})
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	if req.JSONVersion != "2.0" || req.Method != "tools/call" {
		t.Fatalf("unexpected request properties: %+v", req)
	}

	reqBytes, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	id, isResp, parsedReq, parsedResp, parseErr := ParseJSONRPCMessage(reqBytes)
	if parseErr != nil {
		t.Fatalf("parse failed: %v", parseErr)
	}
	if isResp || parsedReq == nil || parsedResp != nil {
		t.Fatalf("expected request message, got isResp=%v", isResp)
	}
	if fmtID(id) != "1" {
		t.Fatalf("expected ID 1, got %v", id)
	}

	resp, err := NewJSONRPCResponse(1, map[string]string{"status": "ok"})
	if err != nil {
		t.Fatalf("failed to create response: %v", err)
	}
	AssertJSONRPCResponse(t, resp, 1, false)

	errResp, err := NewJSONRPCErrorResponse(1, -32601, "Method not found", nil)
	if err != nil {
		t.Fatalf("failed to create error response: %v", err)
	}
	AssertJSONRPCError(t, errResp, -32601)
}

func fmtID(id interface{}) string {
	if id == nil {
		return ""
	}
	return jsonNumber(id)
}

func jsonNumber(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestPairingCodeGenerator(t *testing.T) {
	code := GeneratePairingCode()
	if len(code) != 6 {
		t.Fatalf("expected code length 6, got %d (%s)", len(code), code)
	}

	codes := make(map[string]bool)
	for i := 0; i < 100; i++ {
		c := GeneratePairingCode()
		if len(c) != 6 {
			t.Fatalf("invalid generated code length: %s", c)
		}
		codes[c] = true
	}
	if len(codes) < 90 {
		t.Fatalf("expected high entropy in pairing codes, got %d unique codes out of 100", len(codes))
	}
}

func TestMockKingAndMockWorkerE2E(t *testing.T) {
	king := NewMockKing(WithRecoveryWindow(2 * time.Second))
	defer king.Close()

	nodeID := "node-001"
	pairCode := "ABC123"
	king.AddPairingCode(pairCode, nodeID)

	token, devID, err := king.ValidateAndPair(pairCode)
	if err != nil {
		t.Fatalf("failed to pair device: %v", err)
	}
	if devID != nodeID || token == "" {
		t.Fatalf("invalid pair result: devID=%s, token=%s", devID, token)
	}

	// Single-use token check
	_, _, err = king.ValidateAndPair(pairCode)
	if err == nil {
		t.Fatalf("expected error when re-using consumed pairing code")
	}

	worker := NewMockWorker(nodeID, king.WSSURL+"/register", token)
	defer worker.Stop()

	worker.RegisterTool("tools/call", func(params json.RawMessage) (interface{}, error) {
		var p map[string]string
		_ = json.Unmarshal(params, &p)
		return map[string]string{"echo": p["name"]}, nil
	})

	if err := worker.Connect(); err != nil {
		t.Fatalf("failed worker connection: %v", err)
	}

	headers, ok := king.GetRegisteredHeaders(nodeID)
	if !ok || headers.Get("X-Node-ID") != nodeID {
		t.Fatalf("king failed to record worker registration headers: %+v", headers)
	}

	reqPayload, _ := NewJSONRPCRequest(100, "tools/call", map[string]string{"name": "hello_world"})
	reqBody, _ := json.Marshal(reqPayload)

	ingressURL := king.URL + "/" + nodeID + "/mcp"
	resp, err := http.Post(ingressURL, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("ingress HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", resp.StatusCode)
	}

	var jsonResp JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&jsonResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	AssertJSONRPCResponse(t, &jsonResp, 100, false)

	var resData map[string]string
	_ = json.Unmarshal(jsonResp.Result, &resData)
	if resData["echo"] != "hello_world" {
		t.Fatalf("expected echo 'hello_world', got '%s'", resData["echo"])
	}
}

func TestWorkerOutboxSessionResumption(t *testing.T) {
	king := NewMockKing(WithRecoveryWindow(3 * time.Second))
	defer king.Close()

	nodeID := "node-outbox"
	token := "token-outbox"
	king.RegisterDeviceToken(nodeID, token)

	worker := NewMockWorker(nodeID, king.WSSURL+"/register", token)
	defer worker.Stop()

	worker.RegisterTool("tools/call", func(params json.RawMessage) (interface{}, error) {
		return map[string]string{"status": "delayed_result"}, nil
	})

	if err := worker.Connect(); err != nil {
		t.Fatalf("failed worker connection: %v", err)
	}

	// Disconnect worker to simulate network drop
	worker.Disconnect()

	// Dispatch HTTP ingress call asynchronously while worker is disconnected
	ingressURL := king.URL + "/" + nodeID + "/mcp"
	reqPayload, _ := NewJSONRPCRequest(200, "tools/call", map[string]string{"op": "run"})
	reqBody, _ := json.Marshal(reqPayload)

	resultChan := make(chan *http.Response, 1)
	errChan := make(chan error, 1)

	go func() {
		r, err := http.Post(ingressURL, "application/json", bytes.NewReader(reqBody))
		if err != nil {
			errChan <- err
			return
		}
		resultChan <- r
	}()

	// Wait brief moment for King to assign UUID and send to tunnel (which fails or waits)
	time.Sleep(100 * time.Millisecond)

	// Reconnect worker
	if err := worker.Reconnect(); err != nil {
		t.Fatalf("failed worker reconnect: %v", err)
	}

	select {
	case err := <-errChan:
		t.Fatalf("ingress call error: %v", err)
	case resp := <-resultChan:
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected HTTP 200 after session resumption, got %d", resp.StatusCode)
		}
		var jsonResp JSONRPCResponse
		_ = json.NewDecoder(resp.Body).Decode(&jsonResp)
		AssertJSONRPCResponse(t, &jsonResp, 200, false)
	case <-time.After(4 * time.Second):
		t.Fatalf("timed out waiting for HTTP ingress response")
	}
}

func TestCLIRunner(t *testing.T) {
	runner := NewCLIRunner("go")
	stdout, stderr, exitCode, err := runner.Run("version")
	if err != nil || exitCode != 0 {
		t.Fatalf("CLIRunner failed: err=%v, exitCode=%d, stderr=%s", err, exitCode, stderr)
	}
	if !bytes.Contains([]byte(stdout), []byte("go version")) {
		t.Fatalf("unexpected stdout: %s", stdout)
	}
}
