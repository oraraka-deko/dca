# Technical Analysis & Design Report: Milestone 2 (R2 - Requirements 3 & 4)
## King Control Plane Gateway Mode & Decoupled Router (`utils/king_ingress.go`)

**Author:** Explorer 3 (Milestone 2)  
**Date:** 2026-07-28  
**Working Directory:** `d:\Documents\dca\.agents\explorer_m2_3`  
**Target File / Package:** `utils/king_ingress.go`, `utils/king_ingress_test.go` (`package utils`)

---

## 1. Executive Summary & Scope Overview

Milestone 2 (R2) establishes the **King Control Plane Gateway & Decoupled Router** for the `dca` distributed MCP architecture. Explorer 3's specific focus covers **Requirements 3 & 4**:
1. **Requirement 3: URL Route-Based MCP Ingress (`/<device_id>/mcp`)**
   - HTTP Ingress endpoint at `/<device_id>/mcp` accepting incoming JSON-RPC 2.0 calls from agents.
   - Fast lookup in `ActiveConns` (`sync.Map`) for `<device_id>`.
   - Transparent forwarding of JSON-RPC tool calls down the worker WebSocket tunnel **without tool renaming or prefixing** (e.g. keeping tool name `submit_command` intact).
2. **Requirement 4: Transport-Agnostic Pending Map & ID Rewriting**
   - **ID Rewriting**: Agent JSON-RPC request `id`s (int, string, or float) are rewritten to unique UUIDs before tunneling to the target worker daemon.
   - **`PendingRequests` `sync.Map`**: Thread-safe map tracking unique UUID -> response channel (`chan *JSONRPCResponse`), original request `id`, device ID, and timestamp.
   - **Transport Decoupling & 30-Second Recovery Timeout Window**: Decouples logical HTTP/JSON-RPC requests from physical TCP/WebSocket socket drops and reconnects. If a socket drops while a tool is executing on the worker, the HTTP response channel waits up to 30 seconds for the worker to reconnect and flush its Outbox queue. Responses are demuxed back to waiting channels and returned to the HTTP client with the original request `id` restored intact.

---

## 2. Codebase Context & Integration Assessment

### Existing & Planned Infrastructure in `dca`
- **`utils/mcp_server.go`**: Provides `MCPServerWrapper` encapsulating `mark3labs/mcp-go/server` and registering system tools (`submit_command`, `file_manager_*`, `git_*`, `sys_monitor`, etc.).
- **`utils/king_gateway.go` (Milestone 2 Req 2)**: Listens for incoming worker reverse WebSocket connections on `/register`, validates `X-Node-ID` and `Authorization` bearer tokens, and maintains `ActiveConns` (`sync.Map` mapping `device_id` -> `*WorkerConn`).
- **`utils/king_ingress.go` (Milestone 2 Req 3 & 4 - THIS REPORT)**: Exposes `/<device_id>/mcp` HTTP handler, manages `PendingRequests` (`sync.Map`), rewrites JSON-RPC request IDs to UUIDs, forwards calls to `ActiveConns`, handles demuxing, and manages the 30-second session recovery window.
- **Dependencies**:
  - `github.com/google/uuid` (UUID generation for request ID rewriting)
  - `github.com/gorilla/websocket` (WebSocket frame writing and demuxing)
  - `net/http` (Standard Go HTTP routing and handler implementation)

---

## 3. System Architecture & Data Flow

```
+---------------------------------------------------------------------------------------------------+
|                                      KING CONTROL PLANE                                           |
|                                                                                                   |
|  Agent Request: POST /node-12345/mcp                                                              |
|  Payload: {"jsonrpc":"2.0", "id": 42, "method": "tools/call", "params": {"name":"submit_command"}}|
|                                         |                                                         |
|                                         v                                                         |
|                       +-----------------------------------+                                       |
|                       |   KingIngress HTTP Handler        |                                       |
|                       |   Path: /<device_id>/mcp          |                                       |
|                       +-----------------------------------+                                       |
|                                         |                                                         |
|                                         | 1. Validate JSON-RPC & Method                           |
|                                         | 2. Check ActiveConns.Load("node-12345")                 |
|                                         v                                                         |
|                       +-----------------------------------+                                       |
|                       |        ID Rewriter Module         |                                       |
|                       | Original ID: 42                   |                                       |
|                       | Generated UUID: uuid-aaaa-bbbb... |                                       |
|                       +-----------------------------------+                                       |
|                                         |                                                         |
|                                         | Store PendingRequest{UUID, OriginalID, RespChan}        |
|                                         v                                                         |
|                       +-----------------------------------+                                       |
|                       |    PendingRequests (sync.Map)     |                                       |
|                       +-----------------------------------+                                       |
|                                         |                                                         |
|                                         | Write Text Frame down WSS Tunnel                        |
|                                         v                                                         |
|                       +-----------------------------------+                                       |
|                       |    Active Worker WSS Connection   |                                       |
|                       +-----------------------------------+                                       |
|                                         |                                                         |
+-----------------------------------------|---------------------------------------------------------+
                                          |
                                    WebSocket Tunnel (wss://<king>/register)
                                    Rewritten Request: id="uuid-aaaa-bbbb..."
                                          |
+-----------------------------------------v---------------------------------------------------------+
|                                      WORKER DAEMON                                                |
|                                                                                                   |
|  1. Executes tool call (e.g. submit_command) in isolated goroutine                                |
|  2. Enqueues result in thread-safe Outbox Queue                                                   |
|  3. Flushes result frame over WebSocket: {"jsonrpc":"2.0", "id":"uuid-aaaa-bbbb...", "result":...} |
+-----------------------------------------|---------------------------------------------------------+
                                          |
                                    Worker Response over WSS
                                          |
+-----------------------------------------v---------------------------------------------------------+
|                                      KING CONTROL PLANE                                           |
|                                                                                                   |
|                       +-----------------------------------+                                       |
|                       |    WebSocket Reader & Demuxer     |                                       |
|                       +-----------------------------------+                                       |
|                                         |                                                         |
|                                         | Demux: PendingRequests.LoadAndDelete("uuid-aaaa-bbbb..")|
|                                         v                                                         |
|                       +-----------------------------------+                                       |
|                       |    Restore Original ID (42)       |                                       |
|                       |    Send to pending.RespChan       |                                       |
|                       +-----------------------------------+                                       |
|                                         |                                                         |
|                                         v                                                         |
|                       +-----------------------------------+                                       |
|                       |   Return HTTP 200 OK to Agent     |                                       |
|                       |   Payload: {"jsonrpc":"2.0",      |                                       |
|                       |             "id": 42,             |                                       |
|                       |             "result": ...}        |                                       |
|                       +-----------------------------------+                                       |
+---------------------------------------------------------------------------------------------------+
```

---

## 4. Deep-Dive Design of Requirements 3 & 4

### 4.1 Requirement 3: URL Route-Based MCP Ingress (`/<device_id>/mcp`)

1. **Path Pattern & Verification**:
   - Path format: `/<device_id>/mcp` (e.g., `/node-abc1234/mcp`).
   - Standard Go `http.Handler` pattern:
     ```go
     path := strings.Trim(r.URL.Path, "/")
     parts := strings.Split(path, "/")
     if len(parts) != 2 || parts[1] != "mcp" {
         http.Error(w, "Not Found", http.StatusNotFound)
         return
     }
     deviceID := parts[0]
     ```
2. **HTTP Method Verification**:
   - Only `POST` requests are processed for MCP JSON-RPC calls. Non-POST requests return HTTP `405 Method Not Allowed`.
3. **No Tool Renaming / Tool Prefixing Policy**:
   - The King Ingress handler acts as a transparent router.
   - When an agent calls tool `"submit_command"` on device `node-1`, the payload sent to `node-1` maintains method `"tools/call"` and tool name `"submit_command"` unchanged. No modification or prefixing (such as `"node-1_submit_command"`) occurs.
4. **Device Active Connection Verification**:
   - Lookup `deviceID` in `ActiveConns` (`sync.Map`).
   - If not found or connection is nil: return `503 Service Unavailable` with JSON-RPC error response:
     ```json
     {
       "jsonrpc": "2.0",
       "id": <original_id>,
       "error": {
         "code": -32001,
         "message": "Device 'node-12345' is not connected or offline"
       }
     }
     ```

---

### 4.2 Requirement 4: Transport-Agnostic Pending Map & ID Rewriting

1. **ID Rewriting**:
   - Agents may use simple integer IDs (`id: 1`), strings (`id: "req-1"`), or floats. Concurrent requests across different agents or sessions can use identical IDs.
   - To uniquely demux worker responses over shared WebSocket tunnels, `KingIngress` generates a unique UUID (`uuid.New().String()`).
   - The original ID (`OriginalID interface{}`) is saved in `PendingRequest`.
   - The request payload `id` is replaced with the generated UUID string before sending over the worker WebSocket.
2. **`PendingRequests` `sync.Map` Structure**:
   ```go
   type PendingRequest struct {
       UUID       string
       OriginalID interface{}
       DeviceID   string
       RespChan   chan *JSONRPCResponse
       CreatedAt  time.Time
   }
   ```
   - Key in `PendingRequests`: `UUID` string.
   - Value in `PendingRequests`: `*PendingRequest`.
   - `RespChan` is created with buffer capacity 1 (`make(chan *JSONRPCResponse, 1)`), eliminating sender goroutine blocking during response demuxing.
3. **Transport Decoupling & 30-Second Recovery Timeout Window**:
   - When request is forwarded, HTTP handler executes a blocking `select` waiting on `pending.RespChan` or context timeout (`30 * time.Second`).
   - **Socket Drop Resiliency**: If the WebSocket drops during tool execution, worker's local Outbox queue holds completed responses. Worker reconnects within 30 seconds and flushes the Outbox.
   - Demuxer receives flushed response, matches rewritten UUID in `PendingRequests`, restores `OriginalID`, and delivers to `pending.RespChan`.
   - Logical HTTP JSON-RPC request completes successfully (HTTP 200 OK) without client noticing the underlying socket drop.
   - **Timeout Cleanup**: If 30 seconds expire before response arrives, `PendingRequests.Delete(uuid)` removes map entry, preventing memory leaks, and returns HTTP `504 Gateway Timeout`.

---

## 5. Detailed Concrete Code Implementation Drafts

### 5.1 Concrete Implementation: `utils/king_ingress.go`

```go
package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// PendingRequest represents an in-flight JSON-RPC request waiting for a worker response.
type PendingRequest struct {
	UUID       string
	OriginalID interface{}
	DeviceID   string
	RespChan   chan *JSONRPCResponse
	CreatedAt  time.Time
}

// WorkerConn wraps a worker WebSocket connection with thread-safe write locking.
type WorkerConn struct {
	DeviceID string
	Conn     *websocket.Conn
	Mu       sync.Mutex
}

// KingIngress provides HTTP route-based ingress at /<device_id>/mcp and handles transport-agnostic pending request demuxing.
type KingIngress struct {
	ActiveConns     *sync.Map     // map[string]*WorkerConn
	PendingRequests sync.Map      // map[string]*PendingRequest
	Timeout         time.Duration // Session recovery window timeout (default 30s)
}

// NewKingIngress initializes a KingIngress router instance.
func NewKingIngress(activeConns *sync.Map) *KingIngress {
	if activeConns == nil {
		activeConns = &sync.Map{}
	}
	return &KingIngress{
		ActiveConns: activeConns,
		Timeout:     30 * time.Second,
	}
}

// ServeHTTP handles incoming HTTP requests at /<device_id>/mcp.
func (ing *KingIngress) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. Verify HTTP Method
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// 2. Parse Path Pattern: /<device_id>/mcp
	path := strings.Trim(r.URL.Path, "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[1] != "mcp" || parts[0] == "" {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	deviceID := parts[0]

	// 3. Read and Parse JSON-RPC Payload
	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		writeJSONRPCError(w, http.StatusBadRequest, nil, -32700, "Parse error reading request body")
		return
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		writeJSONRPCError(w, http.StatusBadRequest, nil, -32700, "Invalid JSON payload: "+err.Error())
		return
	}

	if req.JSONRPC != "2.0" || req.Method == "" {
		writeJSONRPCError(w, http.StatusBadRequest, req.ID, -32600, "Invalid Request: missing jsonrpc='2.0' or method")
		return
	}

	// 4. Verify Active Worker Connection
	val, ok := ing.ActiveConns.Load(deviceID)
	if !ok || val == nil {
		writeJSONRPCError(w, http.StatusServiceUnavailable, req.ID, -32001, fmt.Sprintf("Device '%s' not connected or offline", deviceID))
		return
	}
	workerConn := val.(*WorkerConn)

	// 5. Rewrite ID to UUID and Register PendingRequest
	originalID := req.ID
	rewrittenUUID := uuid.New().String()
	req.ID = rewrittenUUID

	pending := &PendingRequest{
		UUID:       rewrittenUUID,
		OriginalID: originalID,
		DeviceID:   deviceID,
		RespChan:   make(chan *JSONRPCResponse, 1),
		CreatedAt:  time.Now(),
	}

	ing.PendingRequests.Store(rewrittenUUID, pending)

	// 6. Forward Request down Worker WebSocket Tunnel
	reqBytes, err := json.Marshal(req)
	if err != nil {
		ing.PendingRequests.Delete(rewrittenUUID)
		writeJSONRPCError(w, http.StatusInternalServerError, originalID, -32603, "Internal error marshaling request")
		return
	}

	workerConn.Mu.Lock()
	err = workerConn.Conn.WriteMessage(websocket.TextMessage, reqBytes)
	workerConn.Mu.Unlock()

	if err != nil {
		ing.PendingRequests.Delete(rewrittenUUID)
		writeJSONRPCError(w, http.StatusBadGateway, originalID, -32002, "Failed transmitting request to worker WebSocket tunnel")
		return
	}

	// 7. Session Waiter with 30-Second Recovery Window
	ctx, cancel := context.WithTimeout(r.Context(), ing.Timeout)
	defer cancel()

	select {
	case resp := <-pending.RespChan:
		// Worker response received and demuxed cleanly
		resp.ID = pending.OriginalID // Restore original agent request ID
		respBytes, err := json.Marshal(resp)
		if err != nil {
			writeJSONRPCError(w, http.StatusInternalServerError, originalID, -32603, "Failed marshaling worker response")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(respBytes)

	case <-ctx.Done():
		// 30s session recovery timeout expired or client cancelled
		ing.PendingRequests.Delete(rewrittenUUID)
		writeJSONRPCError(w, http.StatusGatewayTimeout, originalID, -32000, "Gateway Timeout: worker response timed out after 30 seconds")
	}
}

// DemuxResponse receives raw worker WebSocket response bytes, matches rewritten UUID, and forwards to waiting channel.
func (ing *KingIngress) DemuxResponse(data []byte) bool {
	var resp JSONRPCResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return false
	}
	return ing.DemuxResponseStruct(&resp)
}

// DemuxResponseStruct processes an unmarshaled JSONRPCResponse struct.
func (ing *KingIngress) DemuxResponseStruct(resp *JSONRPCResponse) bool {
	if resp.ID == nil {
		return false
	}
	uuidStr := fmt.Sprintf("%v", resp.ID)

	val, ok := ing.PendingRequests.LoadAndDelete(uuidStr)
	if !ok || val == nil {
		// No pending request found (already timed out or invalid ID)
		return false
	}

	pending := val.(*PendingRequest)
	select {
	case pending.RespChan <- resp:
		return true
	default:
		return false
	}
}

// writeJSONRPCError formats and writes a JSON-RPC 2.0 error HTTP response.
func writeJSONRPCError(w http.ResponseWriter, httpStatus int, id interface{}, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	errResp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
		},
	}
	_ = json.NewEncoder(w).Encode(errResp)
}
```

---

### 5.2 Concrete Implementation: `utils/king_ingress_test.go`

```go
package utils

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestKingIngress_RouteAndIDRewriting(t *testing.T) {
	activeConns := &sync.Map{}
	ingress := NewKingIngress(activeConns)

	// Mock WebSocket server simulating worker
	var receivedMessage []byte
	var mu sync.Mutex

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		_, p, err := c.ReadMessage()
		if err != nil {
			return
		}

		mu.Lock()
		receivedMessage = p
		mu.Unlock()

		// Worker echoes back response using received UUID
		var req JSONRPCRequest
		_ = json.Unmarshal(p, &req)

		resBytes, _ := json.Marshal(map[string]string{"status": "ok"})
		resp := JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  resBytes,
		}
		respBytes, _ := json.Marshal(resp)

		// Wait briefly then send response back
		time.Sleep(50 * time.Millisecond)
		ingress.DemuxResponse(respBytes)
	}))
	defer s.Close()

	// Connect mock worker client
	wsURL := "ws" + strings.TrimPrefix(s.URL, "http")
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed connecting mock WS worker: %v", err)
	}
	defer wsConn.Close()

	activeConns.Store("device-123", &WorkerConn{
		DeviceID: "device-123",
		Conn:     wsConn,
	})

	// Perform HTTP POST to /device-123/mcp with integer ID
	reqBody := []byte(`{"jsonrpc":"2.0","id":42,"method":"tools/call","params":{"name":"submit_command"}}`)
	req := httptest.NewRequest("POST", "/device-123/mcp", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()

	ingress.ServeHTTP(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Expected HTTP 200 OK, got %d", res.StatusCode)
	}

	var resp JSONRPCResponse
	_ = json.NewDecoder(res.Body).Decode(&resp)

	// Verify original integer ID 42 is restored in final response
	idNum, ok := resp.ID.(float64)
	if !ok || int(idNum) != 42 {
		t.Errorf("Expected restored integer ID 42, got %v", resp.ID)
	}

	// Verify request payload sent to worker had rewritten UUID ID and unchanged method/tool
	mu.Lock()
	var forwardedReq JSONRPCRequest
	_ = json.Unmarshal(receivedMessage, &forwardedReq)
	mu.Unlock()

	if forwardedReq.Method != "tools/call" {
		t.Errorf("Expected method 'tools/call', got '%s'", forwardedReq.Method)
	}
	if forwardedReq.ID == 42 || forwardedReq.ID == "42" {
		t.Errorf("Forwarded ID should have been rewritten to UUID, but got original ID: %v", forwardedReq.ID)
	}
}

func TestKingIngress_DeviceNotConnected(t *testing.T) {
	activeConns := &sync.Map{}
	ingress := NewKingIngress(activeConns)

	reqBody := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	req := httptest.NewRequest("POST", "/offline-device/mcp", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()

	ingress.ServeHTTP(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("Expected HTTP 503 Service Unavailable, got %d", res.StatusCode)
	}
}

func TestKingIngress_Timeout(t *testing.T) {
	activeConns := &sync.Map{}
	ingress := NewKingIngress(activeConns)
	ingress.Timeout = 100 * time.Millisecond // Short timeout for testing

	// Mock WS server that swallows requests without answering
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer s.Close()

	wsURL := "ws" + strings.TrimPrefix(s.URL, "http")
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed connecting mock WS worker: %v", err)
	}
	defer wsConn.Close()

	activeConns.Store("device-slow", &WorkerConn{
		DeviceID: "device-slow",
		Conn:     wsConn,
	})

	reqBody := []byte(`{"jsonrpc":"2.0","id":99,"method":"tools/call"}`)
	req := httptest.NewRequest("POST", "/device-slow/mcp", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()

	ingress.ServeHTTP(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusGatewayTimeout {
		t.Errorf("Expected HTTP 504 Gateway Timeout, got %d", res.StatusCode)
	}
}
```

---

## 6. Edge Case & Concurrency Verification Matrix

| Edge Case / Scenario | Risk / Problem | Technical Mechanism & Solution |
|----------------------|----------------|--------------------------------|
| **Worker Disconnected Initially** | Request sent to nonexistent or offline device ID | Fast lookup in `ActiveConns` `sync.Map`. Immediately returns HTTP `503 Service Unavailable` with JSON-RPC error `-32001`. |
| **Malformed / Non-JSON-RPC Payload** | Unmarshaling failure or invalid protocol format | Validates `jsonrpc=="2.0"` and `method!=""`. Returns HTTP `400 Bad Request` with JSON-RPC error `-32700 Parse error` or `-32600 Invalid Request`. |
| **Worker Disconnection Mid-Execution** | Socket drops while worker executes long tool call | Worker Outbox buffers response. Logical HTTP handler waits up to 30s. Worker reconnects and flushes response; demuxer matches UUID and returns HTTP 200 OK. |
| **30-Second Timeout Expiration** | Worker crashes or fails to reconnect within 30s | Context timeout fires in `select`. Handler calls `PendingRequests.Delete(uuid)`, preventing memory leaks, and returns HTTP `504 Gateway Timeout`. |
| **Concurrent Requests with Duplicate IDs** | Multiple agents submit `id: 1` concurrently | ID rewriter replaces every agent ID with a globally unique UUID string before forwarding over WebSocket tunnel. Responses demux cleanly. |
| **Concurrent WebSocket Writes** | Data corruption / panic in `gorilla/websocket` | `WorkerConn` encapsulates `*websocket.Conn` with `sync.Mutex`. All `WriteMessage` calls acquire `Mu.Lock()` first. |
| **Late Worker Responses** | Worker response arrives after 30s timeout | `PendingRequests.LoadAndDelete(uuid)` returns `false`. Late response dropped safely without memory leaks or channel panics. |

---

## 7. Next Steps & Handoff Guidance

1. Implement `utils/king_ingress.go` and `utils/king_ingress_test.go` as specified above.
2. Integrate `KingIngress` into `KingGateway` (`utils/king_gateway.go`) and bind the HTTP handler at `/<device_id>/mcp` in the King server setup.
3. Verify test coverage with `go test -v ./utils -run TestKingIngress`.
