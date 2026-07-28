## 2026-07-28T15:28:06Z
<USER_REQUEST>
You are the Worker for Milestone 2: King Control Plane Gateway Mode & Decoupled Router (R2).
Your working directory is `d:\Documents\dca\.agents\worker_m2`.
Identity: archetype `teamwork_preview_worker`.

DO NOT CHEAT. All implementations must be genuine. DO NOT hardcode test results, create dummy/facade implementations, or circumvent the intended task. A Forensic Auditor will independently verify your work. Integrity violations WILL be detected and your work WILL be rejected.

Objective:
Implement and verify all core features of Milestone 2 (R2) in `utils/` and unit test suites:

1. `utils/pairing.go` & `utils/pairing_test.go`:
   - Single-use pairing code generation/exchange (`dca king add-device <code>`), code validation, and activation token issuance (`token-<uuid>`).
   - Thread-safe storage with single-use enforcement: return `"already consumed"` error if code is reused.
   - TTL expiration support: return `"expired"` error if code has expired.
   - Code validation error contract: return `"invalid pairing code"` error for unknown/malformed codes.
   - Code normalization: whitespace trimming, upper-casing, hyphen stripping.
   - Subcommand integration for `dca king add-device <code>` in `cmd/` or `utils/pairing.go`.

2. `utils/king_gateway.go` & `utils/king_gateway_test.go`:
   - Protocol Inversion WebSocket server listening on `/register`.
   - Validate `X-Node-ID` and `Authorization: Bearer <token>` headers (whitespace trimming, case-insensitive header check). Return HTTP 400 for missing headers, 401 for invalid format, 403 for unauthorized/unpaired node or invalid token.
   - Thread-safe `ActiveConns` (`sync.Map`) mapping `deviceID` -> worker WebSocket connection session metadata. Reconnection preemption (close existing connection if same node reconnects).
   - Thread-safe Gorilla WebSocket writing using a mutex wrapper (`WorkerConn`).
   - Protocol Inversion MCP Client handshake: upon worker registration, King initiates `initialize` and `tools/list` JSON-RPC requests down the WebSocket tunnel.

3. `utils/king_ingress.go` & `utils/king_ingress_test.go`:
   - URL Route-Based MCP Ingress at `/<device_id>/mcp`: HTTP POST handler accepting JSON-RPC calls from agents. Checks `ActiveConns` for `<device_id>` (returns HTTP 503 error -32001 if offline).
   - Forwards JSON-RPC tool calls down worker WebSocket tunnel WITHOUT tool renaming or prefixing.
   - Transport-Agnostic Pending Map & ID Rewriting: rewrites incoming agent request `id` (int/string/float) to unique UUID string before sending down WebSocket tunnel, while preserving `OriginalID`.
   - `PendingRequests` (`sync.Map`) tracking rewritten UUID -> `*PendingRequest{UUID, OriginalID, DeviceID, RespChan, CreatedAt}` (buffered `chan *JSONRPCResponse`).
   - 30-second recovery timeout window decoupling logical HTTP/JSON-RPC requests from physical socket drops. Demuxing responses back from worker via UUID, restoring `resp.ID = pending.OriginalID`, and sending down `RespChan`. Return HTTP 504 on timeout.

4. Testing & Verification:
   - Write comprehensive unit tests for pairing, gateway server, ingress router, ID rewriting, and recovery timeout in `utils/pairing_test.go`, `utils/king_gateway_test.go`, and `utils/king_ingress_test.go`.
   - Run `go test -v ./utils/...` (or `go test ./...`) using run_command to verify all tests pass cleanly.

Reference Documents:
- Explorer 1 analysis/handoff: `d:\Documents\dca\.agents\explorer_m2_1\handoff.md` and `analysis.md`
- Explorer 2 analysis/handoff: `d:\Documents\dca\.agents\explorer_m2_2\handoff.md` and `analysis.md`
- Explorer 3 analysis/handoff: `d:\Documents\dca\.agents\explorer_m2_3\handoff.md` and `analysis.md`

Output:
Write `d:\Documents\dca\.agents\worker_m2\handoff.md` with build & test output logs, implemented files list, and verification status. Notify the parent orchestrator when complete.
</USER_REQUEST>
