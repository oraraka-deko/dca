# Outbox Pattern & Async Tool Execution Architecture Analysis (Milestone 1)

## Executive Summary

This document presents the detailed architectural analysis and design specification for **Explorer 2** under Milestone 1 (`Worker Daemon Mode & Reverse Tunnel with Outbox Pattern`).

The report addresses two primary operational mandates:
1. **Outbox Pattern Architecture**: Data structures, thread-safety models (`sync.Mutex` and notification signaling), atomic dequeue/flush mechanisms, and session resumption resilience across temporary or prolonged WebSocket disconnections between the Worker Daemon and King Control Plane.
2. **Async Tool Execution Engine**: How `WorkerDaemon` dispatches incoming JSON-RPC tool calls to isolated goroutines executing `MCPServerWrapper` / `MCPServer` tools from `utils/mcp_server.go`, handling panic recovery, context timeouts, and asynchronous Outbox enqueuing.

---

## 1. Outbox Pattern Architecture & Session Resumption

### 1.1 Problem Statement & Requirements
In a distributed King-Worker gateway model, child devices run `dca worker` behind NATs or corporate firewalls. Workers establish an outbound persistent WebSocket connection (`wss://<king>/register`). 

When an external AI agent invokes an MCP tool, the King routes the request down the target worker's WebSocket connection. Tool execution may take anywhere from milliseconds to several minutes (e.g. command execution, git operations, file indexing).

During this period:
- The WebSocket connection may drop due to network jitter, router resets, TLS timeouts, or server restarts.
- Completed responses MUST NOT be dropped or lost.
- Responses MUST NOT block execution goroutines when the socket is down.
- Upon reconnection, responses MUST be automatically flushed in strict FIFO order, matching the original request IDs stored in King's `PendingRequests` map.

### 1.2 Data Structure Design (`utils/outbox.go`)

We define two core structures for the Outbox module:

```go
package utils

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrOutboxClosed = errors.New("outbox queue is closed")
	ErrOutboxFull   = errors.New("outbox queue reached maximum capacity")
)

// OutboxItem represents a single buffered JSON-RPC response awaiting transmission.
type OutboxItem struct {
	ID         string    `json:"id"`          // Request ID matching King PendingRequests UUID
	Payload    []byte    `json:"payload"`     // Complete JSON-RPC 2.0 response payload bytes
	EnqueuedAt time.Time `json:"enqueued_at"` // Timestamp for queue diagnostics and metrics
	RetryCount int       `json:"retry_count"` // Number of transmission attempts
}

// Outbox is a thread-safe FIFO queue designed for session resumption and async buffering.
type Outbox struct {
	mu          sync.Mutex
	items       []OutboxItem
	notifyChan  chan struct{}
	maxCapacity int
	isClosed    bool
}
```

### 1.3 Thread Safety & Synchronization Analysis

#### Why `sync.Mutex` + Slice + Signal Channel over Pure Bounded Channels?

| Design Pattern | Pros | Cons | Decision |
|---|---|---|---|
| **Bounded Channel** (`chan OutboxItem`) | Simple standard Go primitive | Blocks tool execution goroutines when full; lacks non-destructive head inspection (pop occurs before send confirmation) | **Rejected** |
| **Unbounded Channel** | Never blocks enqueuers | Cannot inspect items without popping; memory leak under persistent disconnects; no queue management | **Rejected** |
| **`sync.Mutex` + Slice + Signal Channel** | Dynamic growth; atomic Peek-and-Ack (removes items ONLY after confirmed send); non-blocking enqueues; zero-latency notifications | Requires explicit lock discipline | **SELECTED** |

#### Locking Discipline & Non-Blocking Network I/O
To prevent lock contention between high-concurrency tool execution goroutines calling `Enqueue()` and the background flusher goroutine writing to the WebSocket, `Flush()` uses a **fine-grained unlock during network transmission**:

1. Flusher locks `mu`, inspects the head item (`items[0]`).
2. Flusher unlocks `mu` and invokes `sendFunc(head)` over the WebSocket.
3. If `sendFunc` succeeds: Flusher re-acquires `mu`, verifies head, and pops `items[0]` (`items = items[1:]`).
4. If `sendFunc` fails (socket error): Flusher re-acquires `mu`, leaves `items[0]` in place, halts flush iteration, and waits for socket reconnection.

This guarantees:
- Tool execution goroutines calling `Enqueue()` never wait for WebSocket TCP write ACKs.
- Strict FIFO order is preserved.
- No response is lost if `sendFunc` fails mid-transmission.

### 1.4 Outbox Method Specifications

```go
// NewOutbox creates an Outbox instance with an optional capacity limit (0 for unlimited).
func NewOutbox(capacity int) *Outbox {
	return &Outbox{
		items:       make([]OutboxItem, 0, 64),
		notifyChan:  make(chan struct{}, 1),
		maxCapacity: capacity,
	}
}

// Enqueue appends an OutboxItem to the queue in a thread-safe manner.
func (o *Outbox) Enqueue(item OutboxItem) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.isClosed {
		return ErrOutboxClosed
	}

	if o.maxCapacity > 0 && len(o.items) >= o.maxCapacity {
		return ErrOutboxFull
	}

	if item.EnqueuedAt.IsZero() {
		item.EnqueuedAt = time.Now()
	}

	o.items = append(o.items, item)

	// Non-blocking signal to flusher goroutine
	select {
	case o.notifyChan <- struct{}{}:
	default:
	}

	return nil
}

// Flush sends enqueued items via sendFunc. Items are dequeued ONLY on success.
func (o *Outbox) Flush(sendFunc func(item OutboxItem) error) (int, error) {
	sentCount := 0

	for {
		o.mu.Lock()
		if o.isClosed || len(o.items) == 0 {
			o.mu.Unlock()
			break
		}

		head := o.items[0]
		o.mu.Unlock()

		// Attempt transmission outside lock
		err := sendFunc(head)

		o.mu.Lock()
		if err != nil {
			o.mu.Unlock()
			return sentCount, err
		}

		// Confirmed send: remove head item
		if len(o.items) > 0 && o.items[0].ID == head.ID {
			o.items = o.items[1:]
		}
		sentCount++
		o.mu.Unlock()
	}

	return sentCount, nil
}

// PeekAll returns a copy snapshot of all enqueued items (for testing/diagnostics).
func (o *Outbox) PeekAll() []OutboxItem {
	o.mu.Lock()
	defer o.mu.Unlock()
	cp := make([]OutboxItem, len(o.items))
	copy(cp, o.items)
	return cp
}

// Len returns current item count.
func (o *Outbox) Len() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.items)
}

// NotifyChan returns the notification channel for the flusher loop.
func (o *Outbox) NotifyChan() <-chan struct{} {
	return o.notifyChan
}

// Close closes the Outbox queue.
func (o *Outbox) Close() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.isClosed = true
}
```

### 1.5 Session Resumption State Diagram Across Socket Drops

```
   +-----------------------+
   |   Worker Connected    |
   | (WSS Active to King)  |
   +-----------+-----------+
               |
     [Tool Call Finishes]
               |
               v
     +-------------------+       Flusher Active
     | Enqueue(Outbox)   | ------------------------> [ws.WriteMessage()]
     +-------------------+                                  |
               |                                            v
       [Socket Drops!]                              (Sent to King)
               |
               v
   +-----------------------+
   |  Worker Disconnected  |
   | (Reconnecting Loop)   |
   +-----------+-----------+
               |
     [Tools Finish Async]
               |
               v
     +-------------------+       Flusher Paused
     | Enqueue(Outbox)   | ------------------------> (Items remain in slice)
     +-------------------+
               |
       [WSS Reconnected!]
               |
               v
   +-----------------------+
   | Resume Outbox Flush   | ------------------------> Drain slice in FIFO order
   +-----------------------+                           to King PendingRequests
```

---

## 2. Async Tool Execution Engine (`MCPServer` / `WorkerDaemon`)

### 2.1 Overview & Integration Architecture

`utils/mcp_server.go` implements `MCPServerWrapper`, wrapping `github.com/mark3labs/mcp-go/server`. It registers 20+ system administration, task management, VFS sandbox, and git tools.

In `WorkerDaemon`, tool execution must be decoupled from the WebSocket receiver loop.

```
                  +--------------------------------+
                  |  WorkerDaemon WSS Read Loop    |
                  +---------------+----------------+
                                  |
                       (Receives JSON-RPC Frame)
                                  |
                                  v
                  +---------------+----------------+
                  |   Spawn Isolated Goroutine     |
                  |     go executeAsync(req)       |
                  +---------------+----------------+
                                  |
          +-----------------------+-----------------------+
          |                                               |
          v                                               v
  [Panic Recovery Guard]                        [Context Timeout (60s)]
          |                                               |
          +-----------------------+-----------------------+
                                  |
                                  v
                  +---------------+----------------+
                  |   MCPServerWrapper Handler     |
                  |   (Tool Execution Logic)       |
                  +---------------+----------------+
                                  |
                                  v
                  +---------------+----------------+
                  |   JSON-RPC Response Formatted  |
                  +---------------+----------------+
                                  |
                                  v
                  +---------------+----------------+
                  |      Outbox.Enqueue(item)      |
                  +--------------------------------+
```

### 2.2 Dispatcher Implementation & Helper Interface

To allow `WorkerDaemon` to cleanly invoke tools registered on `MCPServerWrapper`, we introduce a helper method in `utils/mcp_server.go` or `utils/worker_daemon.go`:

```go
// HandleJSONRPCRequest executes a tool call request synchronously within the caller's goroutine context.
func (w *MCPServerWrapper) HandleJSONRPCRequest(ctx context.Context, req JSONRPCRequest) JSONRPCResponse {
	if req.Method != "tools/call" {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &JSONRPCError{
				Code:    -32601,
				Message: fmt.Sprintf("Method not supported on worker: %s", req.Method),
			},
		}
	}

	var callParams struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}

	if err := json.Unmarshal(req.Params, &callParams); err != nil {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &JSONRPCError{
				Code:    -32602,
				Message: fmt.Sprintf("Invalid params JSON: %v", err),
			},
		}
	}

	// Invoke mcp-go server handle call tool
	mcpReq := mcp.CallToolRequest{}
	mcpReq.Params.Name = callParams.Name
	mcpReq.Params.Arguments = callParams.Arguments

	res, err := w.MCPServer.HandleCallTool(ctx, mcpReq)
	if err != nil {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &JSONRPCError{
				Code:    -32603,
				Message: err.Error(),
			},
		}
	}

	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  res,
	}
}
```

### 2.3 Goroutine Lifecycle, Panic Recovery, and Resource Safety

1. **Panic Protection**: If a tool handler causes a runtime panic (e.g., nil pointer dereference or OS signal), the `defer recover()` block captures the error and returns a standard JSON-RPC internal error (`-32603`). This ensures individual tool failures never crash the `WorkerDaemon`.
2. **Context Timeout**: Each isolated goroutine receives a child `context.WithTimeout(parentCtx, 60*time.Second)`. If execution exceeds 60 seconds, context cancellation propagates down to underlying process runners (`TaskManager`, persistent shell, etc.).
3. **Thread Safety of Underlying Tool Subsystems**:
   - `TaskManager`: Guarded by internal `sync.Mutex` and safe for concurrent calls.
   - `FileManager`: Operating on filesystem paths concurrently; uses thread-safe operations.
   - `database.Store`: SQLite/PostgreSQL connections managed via Go `sql.DB` thread-safe pool.
   - `TimerChainManager`: Guarded by internal locks.

---

## 3. Verification & Test Architecture

To verify the implementation of `Outbox` and `Async Tool Execution`:

1. **Unit Test Suite (`utils/outbox_test.go`)**:
   - Test FIFO ordering under concurrent `Enqueue` calls.
   - Test atomic `Flush()` retention across simulated network send failures.
   - Test capacity limits and `PeekAll()` snapshot integrity.
   - Test race conditions with `go test -race ./utils/...`.
2. **Worker Daemon & Disconnect Simulation Test (`utils/worker_daemon_test.go`)**:
   - Launch mock WebSocket King server using `httptest.NewServer`.
   - Dispatch multiple tool execution requests over WebSocket.
   - Force abrupt WebSocket connection termination mid-execution.
   - Verify tool responses buffer cleanly in `Outbox`.
   - Re-establish WebSocket connection and verify automatic `Flush()` delivers all pending responses to King with intact request IDs.

---

## 4. Architectural Conclusions & Recommendations

1. **Implement `Outbox` in `utils/outbox.go`**: Use the `sync.Mutex` + `[]OutboxItem` + `notifyChan` architecture specified in Section 1.
2. **Implement Async Dispatcher in `WorkerDaemon`**: Spawning an isolated goroutine per frame with `defer recover()` and context timeout ensures maximum resiliency and concurrency.
3. **Integrate with `MCPServerWrapper`**: Add `HandleJSONRPCRequest` to expose `MCPServer` tool execution cleanly.
