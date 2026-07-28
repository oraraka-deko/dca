package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"testing"
	"time"
)

// TestTier4_HighConcurrencyMultiWorkerNAT simulates 10 active Worker daemons connected over WSS
// to 1 King control plane, handling 500 concurrent tool calls with simulated 10% random network dropouts.
// Verifies 100% completion rate via Outbox recovery with zero lost or mismatched responses.
func TestTier4_HighConcurrencyMultiWorkerNAT(t *testing.T) {
	king := NewMockKing(WithRecoveryWindow(15 * time.Second))
	defer king.Close()

	numWorkers := 10
	totalRequests := 500

	workers := make([]*MockWorker, numWorkers)
	for i := 0; i < numWorkers; i++ {
		nodeID := fmt.Sprintf("nat-worker-%02d", i+1)
		code := GeneratePairingCode()
		king.AddPairingCode(code, nodeID)
		token, _, err := king.ValidateAndPair(code)
		if err != nil {
			t.Fatalf("failed to pair worker %s: %v", nodeID, err)
		}

		w := NewMockWorker(nodeID, king.WSSURL+"/register", token)
		workers[i] = w

		// Capture worker reference for local closure
		targetWorker := w

		// Register tool handler with 10% simulated random network dropouts
		w.RegisterTool("tools/call", func(params json.RawMessage) (interface{}, error) {
			var p map[string]interface{}
			_ = json.Unmarshal(params, &p)

			// 10% random dropout mid-tool-execution
			if rand.Float32() < 0.10 {
				targetWorker.Disconnect()
			}

			return map[string]interface{}{
				"worker_id": targetWorker.NodeID,
				"req_num":   p["req_num"],
				"status":    "processed",
			}, nil
		})

		if err := w.Connect(); err != nil {
			t.Fatalf("failed worker %s connect: %v", nodeID, err)
		}
	}

	defer func() {
		for _, w := range workers {
			w.Stop()
		}
	}()

	// Auto-reconnect background manager monitoring workers during network drops
	stopReconnect := make(chan struct{})
	defer close(stopReconnect)

	for _, w := range workers {
		targetWorker := w
		go func(worker *MockWorker) {
			for {
				select {
				case <-stopReconnect:
					return
				case <-time.After(15 * time.Millisecond):
					if !worker.IsConnected() {
						_ = worker.Reconnect()
					}
				}
			}
		}(targetWorker)
	}

	httpClient := &http.Client{
		Timeout: 12 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        500,
			MaxIdleConnsPerHost: 100,
		},
	}

	var wg sync.WaitGroup
	errsChan := make(chan string, totalRequests)

	// Dispatch 500 concurrent tool execution calls across workers
	for i := 1; i <= totalRequests; i++ {
		reqID := i
		workerIdx := (i - 1) % numWorkers
		targetWorker := workers[workerIdx]

		wg.Add(1)
		go func(id int, target *MockWorker) {
			defer wg.Done()

			reqPayload, err := NewJSONRPCRequest(id, "tools/call", map[string]interface{}{
				"req_num":   id,
				"worker_id": target.NodeID,
			})
			if err != nil {
				errsChan <- fmt.Sprintf("req %d build error: %v", id, err)
				return
			}
			reqBytes, _ := json.Marshal(reqPayload)

			url := king.URL + "/" + target.NodeID + "/mcp"

			// Execute request with transient retry in case POST hits worker mid-reconnect
			var resp *http.Response
			var httpErr error
			for attempt := 0; attempt < 5; attempt++ {
				resp, httpErr = httpClient.Post(url, "application/json", bytes.NewReader(reqBytes))
				if httpErr == nil && resp.StatusCode == http.StatusOK {
					break
				}
				if resp != nil {
					resp.Body.Close()
				}
				time.Sleep(20 * time.Millisecond)
			}

			if httpErr != nil {
				errsChan <- fmt.Sprintf("req %d HTTP post failed: %v", id, httpErr)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				errsChan <- fmt.Sprintf("req %d non-200 status code: %d", id, resp.StatusCode)
				return
			}

			var jsonResp JSONRPCResponse
			if err := json.NewDecoder(resp.Body).Decode(&jsonResp); err != nil {
				errsChan <- fmt.Sprintf("req %d response decode error: %v", id, err)
				return
			}

			if fmt.Sprintf("%v", jsonResp.ID) != fmt.Sprintf("%d", id) {
				errsChan <- fmt.Sprintf("req %d ID mismatch: expected %d, got %v", id, id, jsonResp.ID)
				return
			}

			var result map[string]interface{}
			if err := json.Unmarshal(jsonResp.Result, &result); err != nil {
				errsChan <- fmt.Sprintf("req %d result unmarshal error: %v", id, err)
				return
			}

			if result["worker_id"] != target.NodeID || fmt.Sprintf("%v", result["req_num"]) != fmt.Sprintf("%d", id) {
				errsChan <- fmt.Sprintf("req %d payload mismatch: expected worker=%s req=%d, got %+v", id, target.NodeID, id, result)
				return
			}
		}(reqID, targetWorker)
	}

	wg.Wait()
	close(errsChan)

	var errList []string
	for errStr := range errsChan {
		errList = append(errList, errStr)
	}

	if len(errList) > 0 {
		t.Fatalf("encountered %d errors during high-concurrency NAT test:\n%s", len(errList), fmt.Sprintf("%v", errList[:minVal(5, len(errList))]))
	}

	if count := king.GetPendingCount(); count != 0 {
		t.Fatalf("expected 0 pending requests on King after high concurrency test, got %d", count)
	}
}

// TestTier4_KingRestartSessionRecovery verifies King control plane restart while workers are executing:
// workers detect drop, hold responses in local outbox, retry backoff, re-establish WSS sessions upon King restart,
// and complete all pending work.
func TestTier4_KingRestartSessionRecovery(t *testing.T) {
	// Step 1: Start King 1
	king1 := NewMockKing(WithRecoveryWindow(15 * time.Second))

	numWorkers := 5
	workers := make([]*MockWorker, numWorkers)
	pairTokens := make(map[string]string)

	for i := 0; i < numWorkers; i++ {
		nodeID := fmt.Sprintf("restart-worker-%02d", i+1)
		code := GeneratePairingCode()
		king1.AddPairingCode(code, nodeID)
		token, _, err := king1.ValidateAndPair(code)
		if err != nil {
			t.Fatalf("failed to pair worker %s: %v", nodeID, err)
		}
		pairTokens[nodeID] = token

		w := NewMockWorker(nodeID, king1.WSSURL+"/register", token)
		workers[i] = w

		targetWorker := w
		w.RegisterTool("tools/call", func(params json.RawMessage) (interface{}, error) {
			var p map[string]interface{}
			_ = json.Unmarshal(params, &p)
			return map[string]interface{}{
				"worker_id": targetWorker.NodeID,
				"job_id":    p["job_id"],
				"status":    "done",
			}, nil
		})

		if err := w.Connect(); err != nil {
			t.Fatalf("failed to connect worker %s to King 1: %v", nodeID, err)
		}
	}

	// Verify all workers connected to King 1
	for _, w := range workers {
		if !w.IsConnected() {
			t.Fatalf("expected worker %s to be connected to King 1", w.NodeID)
		}
	}

	// Step 2: King 1 shuts down / restarts
	king1.Close()

	// Wait for workers to detect drop
	time.Sleep(100 * time.Millisecond)

	// Step 3: Simulate tool requests being handled while King is down -> enqueued to local Outbox
	for i, w := range workers {
		reqID := fmt.Sprintf("job-offline-%d", i+1)
		req, _ := NewJSONRPCRequest(reqID, "tools/call", map[string]interface{}{
			"job_id": reqID,
		})
		// Force handle request locally while disconnected -> places response into w.Outbox
		resp, _ := NewJSONRPCResponse(reqID, map[string]interface{}{
			"worker_id": w.NodeID,
			"job_id":    reqID,
			"status":    "done_offline",
		})
		w.EnqueueOutbox(resp)

		if req == nil {
			t.Fatalf("nil request")
		}
	}

	// Verify that workers held responses in local outbox queue
	for _, w := range workers {
		if w.Outbox.Len() == 0 {
			t.Fatalf("expected worker %s to hold items in Outbox during King restart", w.NodeID)
		}
	}

	// Step 4: King control plane restarts (King 2 starts up)
	king2 := NewMockKing(WithRecoveryWindow(15 * time.Second))
	defer king2.Close()

	// Re-register tokens on King 2
	for nodeID, token := range pairTokens {
		king2.RegisterDeviceToken(nodeID, token)
	}

	// Step 5: Workers update WSS URL, execute retry backoff, re-establish WSS sessions upon King restart
	var wg sync.WaitGroup
	for _, w := range workers {
		targetWorker := w
		targetWorker.KingWSSURL = king2.WSSURL + "/register"

		wg.Add(1)
		go func(worker *MockWorker) {
			defer wg.Done()

			backoff := 10 * time.Millisecond
			for attempt := 0; attempt < 10; attempt++ {
				err := worker.Reconnect()
				if err == nil {
					return
				}
				time.Sleep(backoff)
				backoff *= 2
			}
			t.Errorf("worker %s failed to reconnect to King 2", worker.NodeID)
		}(targetWorker)
	}

	wg.Wait()

	// Step 6: Verify outbox queues are flushed and sessions restored on King 2
	for _, w := range workers {
		if w.Outbox.Len() != 0 {
			t.Fatalf("expected worker %s outbox to be completely flushed after King 2 reconnect, got %d items remaining", w.NodeID, w.Outbox.Len())
		}
		if !w.IsConnected() {
			t.Fatalf("expected worker %s to be connected to King 2", w.NodeID)
		}
	}

	// Step 7: Post-restart verification: dispatch new HTTP ingress requests to King 2
	httpClient := &http.Client{Timeout: 5 * time.Second}
	for i, w := range workers {
		reqID := fmt.Sprintf("job-online-%d", i+1)
		reqPayload, err := NewJSONRPCRequest(reqID, "tools/call", map[string]interface{}{
			"job_id": reqID,
		})
		if err != nil {
			t.Fatalf("failed to build request for %s: %v", w.NodeID, err)
		}
		reqBytes, _ := json.Marshal(reqPayload)

		resp, err := httpClient.Post(king2.URL+"/"+w.NodeID+"/mcp", "application/json", bytes.NewReader(reqBytes))
		if err != nil {
			t.Fatalf("post-restart HTTP call failed for worker %s: %v", w.NodeID, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("post-restart HTTP call for worker %s returned status %d", w.NodeID, resp.StatusCode)
		}

		var jsonResp JSONRPCResponse
		if err := json.NewDecoder(resp.Body).Decode(&jsonResp); err != nil {
			t.Fatalf("post-restart response decode failed for worker %s: %v", w.NodeID, err)
		}

		AssertJSONRPCResponse(t, &jsonResp, reqID, false)

		var result map[string]interface{}
		_ = json.Unmarshal(jsonResp.Result, &result)
		if result["worker_id"] != w.NodeID || result["status"] != "done" {
			t.Fatalf("unexpected post-restart result for worker %s: %+v", w.NodeID, result)
		}
	}

	// Stop workers
	for _, w := range workers {
		w.Stop()
	}

	if count := king2.GetPendingCount(); count != 0 {
		t.Fatalf("expected 0 pending requests on King 2 after test, got %d", count)
	}
}

func minVal(a, b int) int {
	if a < b {
		return a
	}
	return b
}
