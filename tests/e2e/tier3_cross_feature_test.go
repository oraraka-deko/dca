package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"testing"
	"time"
)

// TestTier3_FullLifecycleResilience tests the full lifecycle sequence:
// 1. Un-paired worker generates 6-char pairing code.
// 2. King add-device consumes code and issues single-use token.
// 3. Worker registers over WSS at /register with X-Node-ID and token.
// 4. Client POSTs MCP tool request to King /<device_id>/mcp.
// 5. King rewrites request ID to random UUID and tunnels to worker over WSS.
// 6. WSS drops mid-tool-execution; worker places result in Outbox queue.
// 7. Worker reconnects WSS within recovery window.
// 8. Outbox flushes queued response over WSS.
// 9. King matches UUID in PendingRequests map, restores original request ID, and returns HTTP response to client.
func TestTier3_FullLifecycleResilience(t *testing.T) {
	// Step 1: Un-paired worker generates 6-char pairing code
	pairCode := GeneratePairingCode()
	if len(pairCode) != 6 {
		t.Fatalf("expected pairing code length 6, got %d (%s)", len(pairCode), pairCode)
	}
	codeRegex := regexp.MustCompile(`^[A-Z0-9]{6}$`)
	if !codeRegex.MatchString(pairCode) {
		t.Fatalf("pairing code %s does not match pattern ^[A-Z0-9]{6}$", pairCode)
	}

	deviceID := "node-lifecycle-001"

	// Initialize MockKing with a 10s recovery window
	king := NewMockKing(WithRecoveryWindow(10 * time.Second))
	defer king.Close()

	king.AddPairingCode(pairCode, deviceID)

	// Step 2: King add-device consumes code and issues single-use token
	token, pairedDeviceID, err := king.ValidateAndPair(pairCode)
	if err != nil {
		t.Fatalf("failed to validate and pair device: %v", err)
	}
	if pairedDeviceID != deviceID {
		t.Fatalf("expected paired device ID %s, got %s", deviceID, pairedDeviceID)
	}
	if token == "" {
		t.Fatalf("expected non-empty pair token")
	}

	// Verify single-use token / pairing code consumption enforcement
	_, _, reUseErr := king.ValidateAndPair(pairCode)
	if reUseErr == nil {
		t.Fatalf("expected error when re-using consumed pairing code %s", pairCode)
	}

	// Step 3: Worker registers over WSS at /register with X-Node-ID and token
	worker := NewMockWorker(deviceID, king.WSSURL+"/register", token)
	defer worker.Stop()

	// Tool handler mid-execution drop trigger
	toolExecuted := make(chan struct{}, 1)
	worker.RegisterTool("tools/call", func(params json.RawMessage) (interface{}, error) {
		toolExecuted <- struct{}{}
		// Step 6: Simulate WSS drop mid-tool-execution
		worker.Disconnect()

		var p map[string]interface{}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"status": "success",
			"cmd":    p["cmd"],
		}, nil
	})

	if err := worker.Connect(); err != nil {
		t.Fatalf("failed to connect worker over WSS: %v", err)
	}

	// Verify headers recorded by King
	headers, registered := king.GetRegisteredHeaders(deviceID)
	if !registered {
		t.Fatalf("king failed to record registered headers for worker %s", deviceID)
	}
	if headers.Get("X-Node-ID") != deviceID {
		t.Fatalf("expected X-Node-ID header %s, got %s", deviceID, headers.Get("X-Node-ID"))
	}
	if headers.Get("Authorization") != "Bearer "+token {
		t.Fatalf("expected Authorization Bearer header with token %s, got %s", token, headers.Get("Authorization"))
	}

	// Step 4 & 5: Client POSTs MCP tool request to King /<device_id>/mcp
	// King rewrites request ID to random UUID and tunnels over WSS
	origReqID := 4242
	reqPayload, err := NewJSONRPCRequest(origReqID, "tools/call", map[string]string{"cmd": "run_diagnostics"})
	if err != nil {
		t.Fatalf("failed to build JSON-RPC request: %v", err)
	}
	reqBytes, err := json.Marshal(reqPayload)
	if err != nil {
		t.Fatalf("failed to marshal request payload: %v", err)
	}

	type httpResult struct {
		resp *http.Response
		err  error
	}
	resChan := make(chan httpResult, 1)

	go func() {
		client := &http.Client{Timeout: 12 * time.Second}
		resp, err := client.Post(king.URL+"/"+deviceID+"/mcp", "application/json", bytes.NewReader(reqBytes))
		resChan <- httpResult{resp: resp, err: err}
	}()

	// Step 6: Wait for tool execution to be triggered and connection dropped
	select {
	case <-toolExecuted:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for worker tool execution")
	}

	// Brief pause to allow handleRequest to finish and enqueue to Outbox
	time.Sleep(100 * time.Millisecond)

	if worker.Outbox.Len() == 0 {
		t.Fatalf("expected outbox to buffer response during WSS disconnect, got 0 items")
	}

	// Step 7: Worker reconnects WSS within recovery window
	if err := worker.Reconnect(); err != nil {
		t.Fatalf("failed to reconnect worker within recovery window: %v", err)
	}

	// Step 8 & 9: Outbox flushes queued response over WSS, King matches UUID in PendingRequests,
	// restores original request ID, and returns HTTP response to client.
	select {
	case res := <-resChan:
		if res.err != nil {
			t.Fatalf("HTTP ingress call failed: %v", res.err)
		}
		defer res.resp.Body.Close()

		if res.resp.StatusCode != http.StatusOK {
			t.Fatalf("expected HTTP 200 OK, got status code %d", res.resp.StatusCode)
		}

		var jsonResp JSONRPCResponse
		if err := json.NewDecoder(res.resp.Body).Decode(&jsonResp); err != nil {
			t.Fatalf("failed to decode ingress HTTP JSON-RPC response: %v", err)
		}

		AssertJSONRPCResponse(t, &jsonResp, origReqID, false)

		var result map[string]interface{}
		if err := json.Unmarshal(jsonResp.Result, &result); err != nil {
			t.Fatalf("failed to unmarshal JSON-RPC result data: %v", err)
		}

		if result["status"] != "success" || result["cmd"] != "run_diagnostics" {
			t.Fatalf("unexpected tool output in result payload: %+v", result)
		}

	case <-time.After(8 * time.Second):
		t.Fatalf("timed out waiting for HTTP response from King ingress")
	}

	if count := king.GetPendingCount(); count != 0 {
		t.Fatalf("expected 0 pending requests on King after completion, got %d", count)
	}
}

// TestTier3_MultiWorkerRouteIsolation tests multiple Workers (A and B) registered simultaneously
// on a single King instance with concurrent interleaved MCP HTTP ingress calls to /<device_id_A>/mcp
// and /<device_id_B>/mcp, verifying complete route isolation, correct response routing, and zero tool crosstalk.
func TestTier3_MultiWorkerRouteIsolation(t *testing.T) {
	king := NewMockKing(WithRecoveryWindow(10 * time.Second))
	defer king.Close()

	nodeA := "worker-node-alpha"
	nodeB := "worker-node-beta"

	codeA := GeneratePairingCode()
	codeB := GeneratePairingCode()

	king.AddPairingCode(codeA, nodeA)
	king.AddPairingCode(codeB, nodeB)

	tokenA, _, errA := king.ValidateAndPair(codeA)
	tokenB, _, errB := king.ValidateAndPair(codeB)
	if errA != nil || errB != nil {
		t.Fatalf("failed to pair workers: errA=%v, errB=%v", errA, errB)
	}

	workerA := NewMockWorker(nodeA, king.WSSURL+"/register", tokenA)
	workerB := NewMockWorker(nodeB, king.WSSURL+"/register", tokenB)
	defer workerA.Stop()
	defer workerB.Stop()

	// Register distinct tool responses for Worker A and Worker B
	workerA.RegisterTool("tools/call", func(params json.RawMessage) (interface{}, error) {
		var p map[string]interface{}
		_ = json.Unmarshal(params, &p)
		return map[string]interface{}{
			"worker":  "WorkerA",
			"node_id": nodeA,
			"req_tag": p["tag"],
		}, nil
	})

	workerB.RegisterTool("tools/call", func(params json.RawMessage) (interface{}, error) {
		var p map[string]interface{}
		_ = json.Unmarshal(params, &p)
		return map[string]interface{}{
			"worker":  "WorkerB",
			"node_id": nodeB,
			"req_tag": p["tag"],
		}, nil
	})

	if err := workerA.Connect(); err != nil {
		t.Fatalf("failed workerA connect: %v", err)
	}
	if err := workerB.Connect(); err != nil {
		t.Fatalf("failed workerB connect: %v", err)
	}

	totalRequestsPerWorker := 50
	var wg sync.WaitGroup
	errsChan := make(chan string, totalRequestsPerWorker*2)

	httpClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			MaxIdleConnsPerHost: 50,
		},
	}

	// Dispatch interleaved concurrent requests to Worker A and Worker B
	for i := 1; i <= totalRequestsPerWorker; i++ {
		reqNum := i

		// Target Worker A
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			reqID := fmt.Sprintf("req-A-%d", id)
			tag := fmt.Sprintf("tag-A-%d", id)

			reqPayload, err := NewJSONRPCRequest(reqID, "tools/call", map[string]string{"tag": tag})
			if err != nil {
				errsChan <- fmt.Sprintf("WorkerA req %d build error: %v", id, err)
				return
			}
			reqBytes, _ := json.Marshal(reqPayload)

			resp, err := httpClient.Post(king.URL+"/"+nodeA+"/mcp", "application/json", bytes.NewReader(reqBytes))
			if err != nil {
				errsChan <- fmt.Sprintf("WorkerA req %d HTTP error: %v", id, err)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				errsChan <- fmt.Sprintf("WorkerA req %d status code %d", id, resp.StatusCode)
				return
			}

			var jsonResp JSONRPCResponse
			if err := json.NewDecoder(resp.Body).Decode(&jsonResp); err != nil {
				errsChan <- fmt.Sprintf("WorkerA req %d decode error: %v", id, err)
				return
			}

			if fmt.Sprintf("%v", jsonResp.ID) != reqID {
				errsChan <- fmt.Sprintf("WorkerA req %d ID mismatch: expected %s, got %v", id, reqID, jsonResp.ID)
				return
			}

			var res map[string]interface{}
			_ = json.Unmarshal(jsonResp.Result, &res)
			if res["worker"] != "WorkerA" || res["node_id"] != nodeA || res["req_tag"] != tag {
				errsChan <- fmt.Sprintf("WorkerA req %d received crosstalk payload: %+v", id, res)
				return
			}
		}(reqNum)

		// Target Worker B
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			reqID := fmt.Sprintf("req-B-%d", id)
			tag := fmt.Sprintf("tag-B-%d", id)

			reqPayload, err := NewJSONRPCRequest(reqID, "tools/call", map[string]string{"tag": tag})
			if err != nil {
				errsChan <- fmt.Sprintf("WorkerB req %d build error: %v", id, err)
				return
			}
			reqBytes, _ := json.Marshal(reqPayload)

			resp, err := httpClient.Post(king.URL+"/"+nodeB+"/mcp", "application/json", bytes.NewReader(reqBytes))
			if err != nil {
				errsChan <- fmt.Sprintf("WorkerB req %d HTTP error: %v", id, err)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				errsChan <- fmt.Sprintf("WorkerB req %d status code %d", id, resp.StatusCode)
				return
			}

			var jsonResp JSONRPCResponse
			if err := json.NewDecoder(resp.Body).Decode(&jsonResp); err != nil {
				errsChan <- fmt.Sprintf("WorkerB req %d decode error: %v", id, err)
				return
			}

			if fmt.Sprintf("%v", jsonResp.ID) != reqID {
				errsChan <- fmt.Sprintf("WorkerB req %d ID mismatch: expected %s, got %v", id, reqID, jsonResp.ID)
				return
			}

			var res map[string]interface{}
			_ = json.Unmarshal(jsonResp.Result, &res)
			if res["worker"] != "WorkerB" || res["node_id"] != nodeB || res["req_tag"] != tag {
				errsChan <- fmt.Sprintf("WorkerB req %d received crosstalk payload: %+v", id, res)
				return
			}
		}(reqNum)
	}

	wg.Wait()
	close(errsChan)

	var errList []string
	for errStr := range errsChan {
		errList = append(errList, errStr)
	}

	if len(errList) > 0 {
		t.Fatalf("encountered %d isolation errors:\n%s", len(errList), fmt.Sprintf("%v", errList[:min(5, len(errList))]))
	}

	if count := king.GetPendingCount(); count != 0 {
		t.Fatalf("expected 0 pending requests on King after multi-worker test, got %d", count)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
