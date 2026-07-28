# Handoff Report: Milestone 2 (R2) — Protocol Inversion WebSocket Server (`utils/king_gateway.go`)

**Agent:** Explorer 2 (`explorer_m2_2`)  
**Archetype:** `teamwork_preview_explorer`  
**Date:** 2026-07-28  
**Working Directory:** `d:\Documents\dca\.agents\explorer_m2_2`  

---

## 1. Observation

1. **Repository Layout & Existing Files**:
   - `PROJECT.md` lines 15 & 43: Specifies Milestone 2 (R2) scope and location of King Gateway control plane at `utils/king_gateway.go` and tests at `utils/king_gateway_test.go`.
   - `utils/server_config.go` lines 33-66: Defines `ServerConfig` struct with host, port, protocol, and auth modes.
   - `go.mod` line 59: Includes dependency `github.com/gorilla/websocket v1.5.3`.
   - `tests/e2e/harness.go` lines 378-450: Implements reference `MockKing.HandleRegister` and `readLoop` for validating `X-Node-ID` and `Authorization: Bearer <token>` headers, upgrading WebSockets, maintaining active connection maps, and demuxing responses.
   - `tests/e2e/tier1_feature_test.go` lines 123-239: Tests `TestTier1_WSSHeaders_*` asserting HTTP 400 for missing `X-Node-ID`, HTTP 401 for missing/malformed `Authorization`, HTTP 403 for invalid token, and success for valid headers.

2. **Existing Implementation Gaps**:
   - `utils/king_gateway.go` and `utils/king_gateway_test.go` do not currently exist in `d:\Documents\dca\utils/` and need to be implemented by implementer agents in package `utils`.

3. **Protocol Contracts**:
   - Endpoint: `/register` accepting WebSocket upgrades (`wss://` / `ws://`).
   - Headers: `X-Node-ID` (node identity string) and `Authorization: Bearer <token>` (pair token).
   - Connection map: Thread-safe `ActiveConns` (`sync.Map`) mapping `node_id` -> `*WorkerConn`.
   - Protocol Inversion: King acts as MCP Client down the WebSocket tunnel, issuing `initialize` and `tools/list` JSON-RPC requests upon connection establishment.

---

## 2. Logic Chain

1. **Observation 1 & 3** establish that `utils/king_gateway.go` must implement the `/register` HTTP endpoint, validate headers, manage worker connections in `ActiveConns` (`sync.Map`), and perform protocol inversion handshakes (`initialize`, `tools/list`).
2. **Observation 1 (`harness.go` & `tier1_feature_test.go`)** demonstrates the exact HTTP status codes and header validation behavior expected by the E2E harness:
   - Missing `X-Node-ID` -> HTTP 400 Bad Request.
   - Missing or non-Bearer `Authorization` -> HTTP 401 Unauthorized.
   - Token validation failure or node ID mismatch -> HTTP 403 Forbidden.
   - Header sanitization must handle case insensitivity (e.g. `x-node-id`, `AUTHORIZATION`) and leading/trailing whitespace.
3. **Gorilla WebSocket Concurrency Constraint**:
   - In Gorilla WebSocket (`github.com/gorilla/websocket`), concurrent writes to a `*websocket.Conn` from multiple goroutines are not thread-safe and can corrupt frames.
   - Therefore, `WorkerConn` must encapsulate `Conn *websocket.Conn` along with a write mutex `mu sync.Mutex` and helper methods `WriteJSONRPC` and `WriteTextMessage`.
4. **Reconnection Preemption & Teardown Safety**:
   - When a worker reconnects with an existing `node_id`, `ActiveConns.Swap` replaces the active session and closes the old socket cleanly (`oldConn.Close()`).
   - When a read loop exits, deferred cleanup must check `ActiveConns.Load(node_id) == wc` before calling `ActiveConns.Delete(node_id)` to prevent a dropped old connection from accidentally deleting a newly reconnected session.
5. **Conclusion**:
   - `utils/king_gateway.go` can be implemented cleanly in package `utils` with structs `WorkerConn`, `KingGateway`, and helper types/methods, meeting all functional and safety requirements.

---

## 3. Caveats

- **Token Validation Dependency**: `KingGateway` relies on a `TokenValidator` function or integration with `pairing.go` (Requirement 1) to verify tokens. During unit tests, a mock or closure token validator function can be injected.
- **Ingress Integration Dependency**: Frame demuxing in `readLoop` interacts with `PendingRequests` (`sync.Map`), which is managed in `king_ingress.go` (Requirements 3 & 4). `KingGateway` holds a pointer to `PendingRequests` so responses are delivered smoothly.

---

## 4. Conclusion

The design for Requirement 2 (`utils/king_gateway.go`) is fully specified, architected, and validated against existing project test harnesses and codebase structures. It provides:
1. Endpoint `/register` with robust HTTP header extraction (`X-Node-ID`, `Authorization`), whitespace trimming, and case sanitization.
2. HTTP error status returns (400 Bad Request, 401 Unauthorized, 403 Forbidden) matching E2E test specifications.
3. `ActiveConns` (`sync.Map`) with atomic preemption of old connections and race-free teardown logic.
4. Protocol Inversion Handshake sending `initialize` and `tools/list` JSON-RPC 2.0 requests over the WebSocket tunnel upon worker registration.
5. Thread-safe WebSocket write synchronization using `WorkerConn.mu`.

---

## 5. Verification Method

### How to Independently Verify:

1. **Inspect Artifacts**:
   - Technical analysis: `d:\Documents\dca\.agents\explorer_m2_2\analysis.md`
   - Handoff report: `d:\Documents\dca\.agents\explorer_m2_2\handoff.md`

2. **Unit Test Verification Command**:
   Once implemented in `utils/king_gateway.go` and `utils/king_gateway_test.go`:
   ```bash
   # Run package unit tests with data race detector
   go test -v -race ./utils/...
   ```

3. **E2E Integration Test Command**:
   ```bash
   # Run E2E feature tests
   go test -v ./tests/e2e/...
   ```

4. **Invalidation Conditions**:
   - Any test failure in `TestTier1_WSSHeaders_*` or race condition detected during `go test -race ./utils/...`.
