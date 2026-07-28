## 2026-07-28T15:22:48Z
You are Worker 1 for Milestone 1 in dca.
Working directory: d:\Documents\dca\.agents\worker_m1

MANDATORY INTEGRITY WARNING:
DO NOT CHEAT. All implementations must be genuine. DO NOT hardcode test results, create dummy/facade implementations, or circumvent the intended task. A Forensic Auditor will independently verify your work. Integrity violations WILL be detected and your work WILL be rejected.

Your mission:
Implement Sub-Milestones M1.1, M1.2, and M1.3 according to the Explorer designs:
1. `utils/pairing_code.go` and unit tests in `utils/pairing_code_test.go`:
   - 6-character uppercase alphanumeric code generation (`[A-Z0-9]{6}`) using `crypto/rand`.
   - `ValidatePairingCode(code string) bool`.
   - `PairingCodeManager` for loading, saving, and checking local worker pairing status / credentials (`WorkerCredentials`).
2. `utils/outbox.go` and unit tests in `utils/outbox_test.go`:
   - Bounded thread-safe `Outbox` queue guarded by `sync.Mutex` and `chan struct{}` notification channel.
   - `OutboxItem` representing JSON-RPC response payloads.
   - `Enqueue(item OutboxItem)` (thread-safe, non-blocking notification).
   - `Flush(sendFunc func(OutboxItem) error) error`: Drains queue by attempting sending items in FIFO order. If `sendFunc` succeeds, the item is removed from queue. If `sendFunc` fails, the item remains at the front of the queue and flushing stops (preserving queue ordering across WebSocket disconnections).
   - Helper methods: `Len()`, `Clear()`.
3. `utils/worker_daemon.go` and unit tests in `utils/worker_daemon_test.go`:
   - `WorkerDaemonConfig` (KingURL, NodeID, Authorization Token, ReconnectIntervals, etc.).
   - `WorkerDaemon` struct managing state, WebSocket client connection (`gorilla/websocket`), `Outbox`, `PairingCodeManager`, and tool execution wrapper.
   - Outbound WebSocket handshake (`wss://<king>/register` or `ws://`) setting HTTP headers `X-Node-ID` and `Authorization`.
   - Inbound reader loop receiving JSON-RPC requests, spawning asynchronous tool execution in isolated goroutines (`go executionWithTimeout()`), and enqueuing completed JSON-RPC responses to `Outbox`.
   - Background continuous `flushLoop()` that calls `Outbox.Flush(...)` over the active WebSocket when connected. Auto-reconnect with exponential backoff on connection drop.
   - Graceful shutdown (`Stop()` / `Close()`).

Read the Explorer reports in:
- `d:\Documents\dca\.agents\explorer_m1_1\analysis.md`
- `d:\Documents\dca\.agents\explorer_m1_2\analysis.md`
- `d:\Documents\dca\.agents\explorer_m1_3\analysis.md`

Verify all new code by running:
`go test -v -race ./utils/...`

Write your implementation report to `d:\Documents\dca\.agents\worker_m1\changes.md` and `d:\Documents\dca\.agents\worker_m1\handoff.md`.
