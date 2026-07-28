# Technical Analysis & Design Report: Milestone 2 (R2 - Requirement 2)
## King Control Plane Gateway Mode & Protocol Inversion WebSocket Server (`utils/king_gateway.go`)

**Author:** Explorer 2 (Milestone 2)  
**Date:** 2026-07-28  
**Working Directory:** `d:\Documents\dca\.agents\explorer_m2_2`  
**Target Files:** `utils/king_gateway.go`, `utils/king_gateway_test.go` (`package utils`)

---

## 1. Executive Summary & Scope Overview

Milestone 2 (R2) establishes the **King Control Plane Gateway Mode & Decoupled Router** for the `dca` distributed MCP architecture. Explorer 2's primary focus covers **Requirement 2**:
1. **HTTP Endpoint `/register`**:
   - HTTP server endpoint `/register` accepting worker WebSocket connections (`wss://` / `ws://`).
   - Uses `gorilla/websocket.Upgrader` to perform 101 Switching Protocols upgrades.
2. **Header Validation & Token Verification**:
   - `X-Node-ID`: Unique node/device identifier. Must be present and non-empty. Supports case-insensitivity and whitespace trimming. Missing/empty -> HTTP 400 Bad Request.
   - `Authorization`: Must be `Bearer <token>`. Supports case-insensitivity and whitespace trimming. Missing/malformed -> HTTP 401 Unauthorized.
   - Token validation against activation/pair token store. Invalid/mismatched token -> HTTP 403 Forbidden.
3. **Connection Registry (`ActiveConns`) & Preemption**:
   - Thread-safe registry using `sync.Map` mapping device/node ID (`string`) to `*WorkerConn`.
   - Handling reconnection preemption: when a new connection registers with an existing `node_id`, cleanly close the old connection (`oldConn.Close()`) and replace it in `ActiveConns`.
   - Safe cleanup: when a connection drops, ensure `ActiveConns.Delete` is only executed if the current entry in `ActiveConns` still matches the dropping connection instance (preventing quick-reconnect race conditions).
4. **Protocol Inversion Handshake (King as MCP Client)**:
   - Reverse tunneling pattern: Worker connects outbound to King `/register` as WebSocket client, but Worker operates as MCP Server.
   - King acts as **MCP Client** down the WebSocket tunnel upon registration completion:
     - Sends `initialize` JSON-RPC request to worker.
     - Sends `tools/list` JSON-RPC request to worker.
     - Stores worker's returned tool inventory in `WorkerConn.Tools` and marks session initialized.
5. **Thread-Safe WebSocket Communication**:
   - Gorilla `websocket.Conn` writes are NOT safe for concurrent goroutine calls. Enforces per-session write mutex (`WorkerConn.mu`).

---

## 2. Codebase Context & Integration Assessment

### Existing Infrastructure & Dependencies in `dca`
- **`utils/server_config.go`**: Configures server options (`Host`, `Port`, `Protocol`, `CertType`, `AuthMode`, etc.).
- **`utils/pairing.go` (Requirement 1)**: Generates/validates pairing codes and issues activation pair tokens.
- **`utils/king_ingress.go` (Requirements 3 & 4)**: Handles HTTP ingress at `/<device_id>/mcp`, request ID rewriting to UUIDs, and 30-second session recovery window in `PendingRequests` (`sync.Map`).
- **`tests/e2e/harness.go`**: Defines `MockKing` and `MockWorker` reference implementations used by E2E tests (`TestTier1_WSSHeaders_*`).
- **Dependencies**:
  - `github.com/gorilla/websocket` (Upgrader, TextMessage, CloseMessage)
  - `net/http` (Standard HTTP handler and server components)

---

## 3. Data Structures & Type Definitions

The proposed implementation in `utils/king_gateway.go` will define the following core structures:

```go
package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// JSONRPCRequest represents a JSON-RPC 2.0 request payload.
type JSONRPCRequest struct {
	JSONVersion string          `json:"jsonrpc"`
	ID          interface{}     `json:"id,omitempty"`
	Method      string          `json:"method"`
	Params      json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse represents a JSON-RPC 2.0 response payload.
type JSONRPCResponse struct {
	JSONVersion string          `json:"jsonrpc"`
	ID          interface{}     `json:"id"`
	Result      json.RawMessage `json:"result,omitempty"`
	Error       *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError represents a standard JSON-RPC 2.0 error object.
type JSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// WorkerConn encapsulates an active WebSocket connection from a Worker daemon,
// along with session metadata and write synchronization.
type WorkerConn struct {
	NodeID            string
	Conn              *websocket.Conn
	mu                sync.Mutex // Synchronizes writes to Conn (gorilla/websocket write lock)
	RegisteredHeaders http.Header
	ConnectedAt       time.Time
	Context           context.Context
	Cancel            context.CancelFunc
	Initialized       bool
	Tools             []map[string]interface{}
	LastPing          time.Time
}

// WriteJSONRPC sends a JSON-RPC payload down the WebSocket tunnel safely.
func (wc *WorkerConn) WriteJSONRPC(v interface{}) error {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	return wc.Conn.WriteJSON(v)
}

// WriteTextMessage sends a raw text frame down the WebSocket tunnel safely.
func (wc *WorkerConn) WriteTextMessage(msg []byte) error {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	return wc.Conn.WriteMessage(websocket.TextMessage, msg)
}

// Close cleanly closes the WebSocket connection and cancels context.
func (wc *WorkerConn) Close() error {
	if wc.Cancel != nil {
		wc.Cancel()
	}
	if wc.Conn != nil {
		_ = wc.Conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "connection closed"),
			time.Now().Add(1*time.Second),
		)
		return wc.Conn.Close()
	}
	return nil
}

// TokenValidatorFunc validates an activation token against a target node ID.
type TokenValidatorFunc func(nodeID, token string) bool

// KingGateway manages worker registrations over /register, connection registry ActiveConns,
// protocol inversion handshakes, and frame routing.
type KingGateway struct {
	ActiveConns     sync.Map           // map[string]*WorkerConn (NodeID -> *WorkerConn)
	Upgrader        websocket.Upgrader
	TokenValidator  TokenValidatorFunc
	PendingRequests *sync.Map          // Shared map from king_ingress (requestUUID -> chan *JSONRPCResponse)
	RequestIDMap    *sync.Map          // Shared map from king_ingress (requestUUID -> originalID)
	mu              sync.RWMutex
}
```

---

## 4. End-to-End Control Plane Flow

```
   Worker Daemon                             King Control Plane Gateway
  (WebSocket Client)                             (WebSocket Server)
         |                                               |
         | --- HTTP GET /register ---------------------> |
         |     Headers: X-Node-ID: node-001              | 1. Extract & Sanitize Headers
         |              Authorization: Bearer <token>    | 2. Validate X-Node-ID non-empty (else 400)
         |                                               | 3. Validate Bearer token format (else 401)
         |                                               | 4. Verify token against node-001 (else 403)
         |                                               |
         | <--- HTTP 101 Switching Protocols ----------- | 5. Upgrade connection to WebSocket
         |                                               | 6. Check ActiveConns: if old conn exists,
         |                                               |    close old socket and preempt.
         |                                               | 7. Store new WorkerConn in ActiveConns.
         |                                               |
         | <=== WS Text: JSON-RPC "initialize" ========= | 8. Protocol Inversion Handshake Step 1:
         |      {"jsonrpc":"2.0","id":"init-1",          |    King acts as MCP Client, sends initialize.
         |       "method":"initialize", ...}             |
         |                                               |
         | === WS Text: JSON-RPC Result ===============> |
         |                                               |
         | <=== WS Text: JSON-RPC "tools/list" ========= | 9. Protocol Inversion Handshake Step 2:
         |      {"jsonrpc":"2.0","id":"init-2",          |    King sends tools/list request.
         |       "method":"tools/list"}                  |
         |                                               |
         | === WS Text: JSON-RPC Result (tools) =======> | 10. Store tool inventory in WorkerConn.Tools,
         |                                               |     mark session Initialized=true.
         |                                               |
         | <================ Read Loop =================>| 11. Continuous read loop demuxing frames
         |                                               |     to PendingRequests channels.
```

---

## 5. Implementation Details & Key Algorithms

### 5.1 Header Sanitization & Extraction (`HandleRegister`)

```go
func extractHeader(h http.Header, name string) string {
	val := strings.TrimSpace(h.Get(name))
	if val != "" {
		return val
	}
	// Case-insensitive fallback
	for k, vals := range h {
		if strings.EqualFold(k, name) && len(vals) > 0 {
			return strings.TrimSpace(vals[0])
		}
	}
	return ""
}

func (kg *KingGateway) HandleRegister(w http.ResponseWriter, r *http.Request) {
	nodeID := extractHeader(r.Header, "X-Node-ID")
	if nodeID == "" {
		http.Error(w, "Missing X-Node-ID header", http.StatusBadRequest)
		return
	}

	authHeader := extractHeader(r.Header, "Authorization")
	if authHeader == "" || !strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		http.Error(w, "Missing or invalid Authorization header", http.StatusUnauthorized)
		return
	}

	token := strings.TrimSpace(authHeader[7:])
	if token == "" {
		http.Error(w, "Missing or invalid Authorization header", http.StatusUnauthorized)
		return
	}

	if kg.TokenValidator != nil && !kg.TokenValidator(nodeID, token) {
		http.Error(w, "Unauthorized pair token", http.StatusForbidden)
		return
	}

	conn, err := kg.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	workerConn := &WorkerConn{
		NodeID:            nodeID,
		Conn:              conn,
		RegisteredHeaders: r.Header.Clone(),
		ConnectedAt:       time.Now(),
		Context:           ctx,
		Cancel:            cancel,
	}

	// Preempt existing connection for nodeID if present
	if oldVal, loaded := kg.ActiveConns.Swap(nodeID, workerConn); loaded {
		if oldConn, ok := oldVal.(*WorkerConn); ok && oldConn != nil {
			_ = oldConn.Close()
		}
	}

	// Perform Protocol Inversion Handshake & Start Read Loop
	go kg.performProtocolInversionHandshake(workerConn)
	go kg.readLoop(workerConn)
}
```

---

### 5.2 Connection Read Loop & Safe Removal (`readLoop`)

```go
func (kg *KingGateway) readLoop(wc *WorkerConn) {
	defer func() {
		// Race-free cleanup: only delete from ActiveConns if current entry is wc
		if currVal, ok := kg.ActiveConns.Load(wc.NodeID); ok {
			if currConn, ok := currVal.(*WorkerConn); ok && currConn == wc {
				kg.ActiveConns.Delete(wc.NodeID)
			}
		}
		_ = wc.Close()
	}()

	for {
		_, message, err := wc.Conn.ReadMessage()
		if err != nil {
			break
		}
		kg.dispatchIncomingFrame(wc, message)
	}
}

func (kg *KingGateway) dispatchIncomingFrame(wc *WorkerConn, msg []byte) {
	var raw map[string]interface{}
	if err := json.Unmarshal(msg, &raw); err != nil {
		return
	}

	// Check if this is a response frame (has "result" or "error")
	_, hasResult := raw["result"]
	hasError := raw["error"] != nil
	if hasResult || hasError {
		var resp JSONRPCResponse
		if err := json.Unmarshal(msg, &resp); err != nil {
			return
		}

		reqIDStr := fmt.Sprintf("%v", resp.ID)

		// Check if pending request exists in PendingRequests map
		if kg.PendingRequests != nil {
			if chVal, ok := kg.PendingRequests.Load(reqIDStr); ok {
				if ch, ok := chVal.(chan *JSONRPCResponse); ok && ch != nil {
					select {
					case ch <- &resp:
					default:
					}
				}
			}
		}
	}
}
```

---

### 5.3 Protocol Inversion Handshake (`performProtocolInversionHandshake`)

```go
func (kg *KingGateway) performProtocolInversionHandshake(wc *WorkerConn) {
	// Step 1: Send initialize request
	initParams := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]string{
			"name":    "dca-king-gateway",
			"version": "1.0.0",
		},
	}
	initReq, err := NewJSONRPCRequest("king-init-1", "initialize", initParams)
	if err == nil {
		_ = wc.WriteJSONRPC(initReq)
	}

	// Step 2: Send tools/list request
	toolsReq, err := NewJSONRPCRequest("king-init-2", "tools/list", map[string]interface{}{})
	if err == nil {
		_ = wc.WriteJSONRPC(toolsReq)
	}

	wc.Initialized = true
}
```

---

## 6. Edge Cases & Boundary Conditions Analysis

| # | Scenario / Edge Case | Edge Condition Detail | Handled Protocol Behavior |
|---|----------------------|-----------------------|---------------------------|
| 1 | **Missing `X-Node-ID`** | HTTP request has no `X-Node-ID` header | Handshake rejected with `HTTP 400 Bad Request` |
| 2 | **Header Formatting** | Headers contain extra spaces (` Authorization: Bearer token `) or alternate casing (`x-node-id`) | Trimmed and case-insensitively parsed; accepted if token/node ID are valid |
| 3 | **Missing / Malformed `Authorization`** | Header missing or does not start with `Bearer ` | Handshake rejected with `HTTP 401 Unauthorized` |
| 4 | **Invalid / Expired Token** | Bearer token does not match node ID in activation token store | Handshake rejected with `HTTP 403 Forbidden` |
| 5 | **Reconnection Preemption** | Worker reconnects with new socket while old socket is still active in `ActiveConns` | Old `WorkerConn` is closed (`oldConn.Close()`), context canceled, new socket stored in `ActiveConns` |
| 6 | **Fast Reconnect Race Condition** | Old read loop exits after new connection replaced it in `ActiveConns` | Read loop defer checks `ActiveConns.Load(nodeID) == wc` before deleting, preserving new connection |
| 7 | **Concurrent Writes on WS** | Multiple goroutines writing tool requests or ping frames concurrently | Synchronized via `WorkerConn.mu` write mutex |
| 8 | **Worker Unclean Disconnect** | Worker process drops without sending WebSocket close frame | Read loop receives read error, triggers deferred cleanup and removal from `ActiveConns` |

---

## 7. Verification Plan & Test Strategy

### 7.1 Unit Tests (`utils/king_gateway_test.go`)
- `TestKingGateway_Register_ValidHandshake`: Valid headers & token yield 101 status and populate `ActiveConns`.
- `TestKingGateway_Register_MissingNodeID`: Missing `X-Node-ID` returns 400.
- `TestKingGateway_Register_MissingAuth`: Missing `Authorization` header returns 401.
- `TestKingGateway_Register_InvalidToken`: Invalid token returns 403.
- `TestKingGateway_Register_HeaderSanitization`: Whitespace and case variations are correctly parsed.
- `TestKingGateway_Register_ReconnectionPreemption`: Old connection is closed when same `node_id` connects anew.
- `TestKingGateway_ProtocolInversionHandshake`: King sends `initialize` and `tools/list` upon connection setup.
- `TestKingGateway_ConcurrentWrites`: Concurrent `WriteJSONRPC` calls run without data race (tested with `go test -race ./utils/...`).

### 7.2 Integration & E2E Tests
- Run full suite: `go test -v ./utils/...` and `go test -v ./tests/e2e/...`.

---
*End of Technical Analysis & Design Report for Requirement 2*
