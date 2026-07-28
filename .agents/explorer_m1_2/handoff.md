# Handoff Report — Explorer 2 (Milestone 1)

## 1. Observation

Direct code and file observations:

1. **`PROJECT.md` (lines 6, 14, 40)**:
   - Line 6: "Tool executions occur in isolated goroutines; completed JSON-RPC responses are buffered in an `Outbox` queue and flushed over WebSocket. If connection drops mid-execution, items remain queued and flush upon reconnection."
   - Line 14: "Milestone 1: Worker Daemon & Outbox Queue (R1) - `dca worker` mode, 6-char pairing code generator, WSS reverse tunnel client, async Outbox session resumption queue".
   - Line 40: Designated location for `Outbox` is `utils/outbox.go`.

2. **`utils/mcp_server.go` (lines 36-46, 87-800, 1137-1141)**:
   - Line 36-46: `MCPServerWrapper` encapsulates `MCPServer *server.MCPServer` (`github.com/mark3labs/mcp-go/server`) and utility managers (`TaskManager`, `SandboxManager`, `TimerChain`, `Hook`, `FileManager`, `Store`).
   - Line 87-800: `RegisterAllTools()` registers 20+ tool handlers taking `(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error)`.
   - Line 1137-1141: `w.MCPServer.AddTool(tool, handler)` registers handlers directly with `mcp-go` server.

3. **`utils/server_config.go` (lines 33-49)**:
   - Line 33-49: `ServerConfig` struct holds network host/port/protocol/auth settings.

4. **`go.mod` (lines 15, 59)**:
   - Line 15: `github.com/mark3labs/mcp-go v0.57.0`
   - Line 59: `github.com/gorilla/websocket v1.5.3`

5. **`sub_orch_m1/SCOPE.md` (lines 9-24)**:
   - Outbox requirements: `Enqueue(item)`, `Dequeue()`, `PeekAll()`, `Flush(sendFunc)` iterator methods, thread-safety under `sync.Mutex`, session resumption across WebSocket drops.
   - Worker daemon requirements: isolated goroutines for tool calls, continuous background flusher, persistent outbound WebSocket connection with `X-Node-ID` and `Authorization` headers.

---

## 2. Logic Chain

1. **Premise**: In the King-Worker architecture, the Worker Daemon receives incoming tool execution calls from King via WebSocket.
2. **Observation**: Tool execution calls can take extended time (e.g. commands, git tasks, syslog parsing). WebSocket reads must remain unblocked to handle control frames and concurrent calls.
3. **Deduction 1**: WorkerDaemon must process incoming WebSocket frames by unmarshaling the JSON-RPC request and spawning an isolated goroutine (`go w.executeRequestAsync(req)`).
4. **Deduction 2**: To prevent goroutine panics from crashing the daemon process, each isolated execution goroutine must employ panic recovery (`defer recover()`) and context timeouts (`context.WithTimeout`).
5. **Observation**: Network connections between Worker and King can drop unexpectedly at any point before, during, or after tool execution.
6. **Deduction 3**: Completed JSON-RPC responses cannot be written directly to the WebSocket synchronously within the tool execution goroutine. Instead, they must be enqueued into a thread-safe `Outbox` queue (`utils/outbox.go`).
7. **Deduction 4**: Using a `sync.Mutex` guarding a slice (`[]OutboxItem`) alongside a signal channel (`notifyChan chan struct{}`) allows non-blocking enqueues from tool goroutines while giving the background flusher an atomic "Peek-and-Ack" mechanism (removing items from the queue *only* after confirmed WebSocket send).
8. **Conclusion**: This decoupled architecture guarantees at-least-once delivery, strict FIFO response ordering, and complete session resumption resilience across WebSocket connection drops.

---

## 3. Caveats

1. **Uninvestigated Areas**:
   - King Gateway HTTP ingress (`/<device_id>/mcp`) and `PendingRequests` map routing are part of Milestone 2 (M2) and were not analyzed in detail here except to ensure request ID contracts match.
   - Single-use pair token generation/validation in `utils/pairing.go` is part of Sub-Milestone M1.2 and M2.
2. **Assumptions**:
   - `mcp-go` v0.57.0 handles internal tool call matching cleanly via `w.MCPServer.HandleCallTool(ctx, mcpReq)`.
   - Tool executions default to a 60-second context timeout unless overridden by request parameters.
3. **Alternative Interpretations**:
   - Using a Go channel (`chan OutboxItem`) for the Outbox was considered but rejected because channel pops occur before send confirmation, risking data loss on broken sockets.

---

## 4. Conclusion

1. **`Outbox` Structure**: Implement in `utils/outbox.go` using a mutex-protected `[]OutboxItem` slice with a non-blocking `notifyChan` signal channel. Provide `Enqueue()`, `Flush(sendFunc)`, `PeekAll()`, `Len()`, and `Close()`.
2. **Atomic Dequeue/Flush**: Implement `Flush` such that items are removed from `Outbox` only when `sendFunc` returns `nil`. If `sendFunc` returns a socket error, flush halts and remaining items stay queued for session resumption upon reconnection.
3. **Async Execution Engine**: In `utils/worker_daemon.go`, read incoming WebSocket frames, unmarshal JSON-RPC requests, and launch isolated goroutines (`go w.executeRequestAsync(req)`). Each goroutine wraps execution in `defer recover()` and a 60-second context timeout, invoking `MCPServerWrapper.HandleJSONRPCRequest(ctx, req)` and enqueuing results to `Outbox`.

---

## 5. Verification Method

To independently verify this design once implemented:

1. **Unit Test Verification**:
   - Run `go test -v -race ./utils/...`
   - Verify `utils/outbox_test.go` checks:
     - Concurrent `Enqueue` operations across 100+ goroutines without race warnings (`-race`).
     - `Flush()` with simulated `sendFunc` failure retains items in order.
     - `Flush()` after simulated reconnection successfully drains all enqueued items.
2. **Worker Daemon Integration Test**:
   - Run `go test -v -run TestWorkerDaemon_SessionResumption ./utils/...`
   - Inspect test behavior:
     - Establish WebSocket server with `httptest.NewServer`.
     - Send tool call to WorkerDaemon.
     - Close WebSocket connection while tool is running.
     - Verify tool result lands in `Outbox`.
     - Restart WebSocket server and reconnect WorkerDaemon.
     - Confirm result is automatically flushed to WebSocket server with original request ID intact.
