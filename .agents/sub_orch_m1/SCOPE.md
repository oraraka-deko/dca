# Scope: Milestone 1 — Worker Daemon Mode & Reverse Tunnel with Outbox Pattern (R1)

## Architecture Overview
Milestone 1 implements the Worker Daemon capabilities in `dca`:
1. **Pairing Code Generator (`utils/pairing_code.go`)**:
   - Generates a short, secure 6-character alphanumeric pairing code (e.g. `[A-Z0-9]{6}`) when the worker is un-paired / missing pair credentials.
   - Provides functions to format, validate, and manage local pairing code state or display instructions to the user.

2. **Thread-Safe Outbox Queue (`utils/outbox.go`)**:
   - Implements `Outbox` for session resumption and async result buffering.
   - Buffers completed JSON-RPC tool call responses when execution completes.
   - Thread-safe (`sync.Mutex` or channel-backed), allowing concurrent tool execution goroutines to safely enqueue responses.
   - Offers `Enqueue(item)`, `Dequeue()`, `PeekAll()`, `Flush(sendFunc)` or equivalent iterator methods so queued results survive WebSocket disconnections and are automatically sent upon reconnection.

3. **Worker Daemon & Reverse Tunnel Client (`utils/worker_daemon.go`)**:
   - Manages worker daemon operational lifecycle.
   - Establishes persistent outbound WebSocket connection (`wss://<king>/register` or `ws://` fallback for local test/dev) with HTTP headers:
     - `X-Node-ID`: Unique ID of the worker node.
     - `Authorization`: Pair token or authorization string.
   - Handles incoming JSON-RPC requests from King over WebSocket.
   - Dispatches tool executions to isolated goroutines.
   - Pushes completed tool responses into `Outbox`.
   - Continuous background flusher goroutine: drains `Outbox` over the active WebSocket. If socket drops during or between executions, enqueued items remain in `Outbox` and automatically flush upon re-establishing the WebSocket connection.

## Sub-Milestones & Deliverables
| Sub-ID | Name | Target Files | Verification Criteria |
|--------|------|--------------|-----------------------|
| M1.1 | Outbox Queue | `utils/outbox.go`, `utils/outbox_test.go` | Unit tests verify enqueue, dequeue, flush, concurrent access, persistence across simulated connection drops |
| M1.2 | Pairing Code Generator | `utils/pairing_code.go`, `utils/pairing_code_test.go` | Unit tests verify 6-char alphanumeric format, randomness, uniqueness, validation |
| M1.3 | Worker Daemon & WSS Tunnel Client | `utils/worker_daemon.go`, `utils/worker_daemon_test.go` | Unit tests verify outbound WSS handshake with `X-Node-ID` / `Authorization`, async execution dispatch, session resumption on socket drop |

## Unit & Package Test Requirements
- `go test ./utils/...` must pass cleanly without race conditions (`-race` flag compatible).
- Package `utils` unit tests must cover:
  - 6-character code generation correctness.
  - Thread safety of `Outbox`.
  - Disconnect & reconnect session resumption logic using httptest / mock WebSocket servers.
