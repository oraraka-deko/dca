# E2E Test Infrastructure Specification & Test Plan

**Project**: `dca` (Distributed MCP Gateway Architecture)  
**Location**: `d:\Documents\dca\TEST_INFRA.md`  
**Owner**: E2E Test Infrastructure Engineer  

---

## 1. Testing Philosophy

The testing framework for the `dca` King-Worker architecture follows an **opaque-box (black-box), requirement-driven** methodology built entirely upon standard Go idioms (`testing` package).

### Core Principles

1. **Opaque-Box Verification**: Tests interact with King control planes and Worker daemons strictly through external interfaces—WebSocket endpoints (`wss://`), HTTP ingress endpoints (`/<device_id>/mcp`), CLI binaries, and JSON-RPC 2.0 wire formats. Internal state is observed through public protocol behaviors and wire outputs rather than private field inspection.
2. **Standard Go Tooling**: Tests are executed via standard `go test ./tests/e2e/...` commands without requiring external test runners, custom binaries, or third-party test framework abstractions.
3. **Genuine Implementations**: Mock components (`MockKing`, `MockWorker`) implement real networking stacks (`net/http`, `gorilla/websocket`, standard JSON-RPC 2.0 marshaling). No test results, expected outputs, or protocol frames are hardcoded or circumvented.
4. **Transport-Agnostic Wire Reliability**: The test suite validates that JSON-RPC 2.0 payloads flow transparently over WebSocket tunnels regardless of connection state, network drops, or server restarts.
5. **Deterministic Isolation**: Every test case runs against an isolated, programmatically managed mock server or worker instance bound to ephemeral local ports (`127.0.0.1:0`), preventing port collision and state leak across test runs.

---

## 2. Core Feature Inventory & Requirements

The E2E test infrastructure targets 7 fundamental features of the `dca` distributed architecture:

| # | Feature Name | Key Requirements & Protocol Contracts |
|---|--------------|--------------------------------------|
| **1** | **Worker Pairing Code Generation** | Generates 6-character uppercase alphanumeric codes (`[A-Z0-9]{6}`). Temporary state; expires after single use or timeout. Un-paired workers present this code on launch. |
| **2** | **Worker WSS Registration Headers** | Worker connects outbound to King via `wss://<king>/register`. Handshake MUST include HTTP headers `X-Node-ID` (unique node identity) and `Authorization: Bearer <pair_token>`. Missing/invalid headers yield HTTP 401/403. |
| **3** | **Worker Outbox Async Buffering & Resumption** | Completed JSON-RPC responses generated during disconnects are buffered in a thread-safe local `Outbox` queue. Queued messages flush automatically in original sequence upon WSS reconnection. |
| **4** | **King Device Addition & Token Issuance** | King executes `dca king add-device <code>`. Validates 6-char pairing code, marks code as consumed (single-use), and issues encrypted single-use JWT / pair token. Replaying code yields error. |
| **5** | **King HTTP Ingress Routing** | Endpoint `POST /<device_id>/mcp` accepts standard MCP JSON-RPC 2.0 requests, routes payload down the matching worker's active WSS tunnel without altering tool names, and returns response to HTTP caller. |
| **6** | **King PendingRequests & Session Recovery** | Inbound request ID rewritten to fresh random UUID before tunneling. King tracks request in `PendingRequests` map. Disconnections trigger a 30-second recovery window; responses arriving before timeout resolve the waiting HTTP request. |
| **7** | **CLI Subcommands & Config Integration** | Subcommands (`dca king`, `dca worker`, `dca pair`) integrate with `ServerConfig` and `main.go` while preserving standalone, Windows Service, and interactive TUI compatibility. |

---

## 3. Test Architecture & Harness Design (`tests/e2e/harness.go`)

The E2E test harness resides in package `e2e` (`tests/e2e/harness.go`) and provides programmatic building blocks for simulating full network topologies.

```
                    +---------------------------------------+
                    |             E2E Test Case             |
                    +-------------------+-------------------+
                                        |
                 +----------------------+----------------------+
                 |                                             |
                 v                                             v
       +-------------------+                         +-------------------+
       |     MockKing      |                         |    MockWorker     |
       |  (Control Plane)  |                         |  (Child Daemon)   |
       +---------+---------+                         +---------+---------+
                 |                                             |
  HTTP Ingress   |  JSON-RPC over WSS Tunnel (/register)       |  Async Outbox
  /<device_id>/mcp| <=========================================> |  Buffer Queue
                 |    X-Node-ID / Authorization Headers        |
                 +---------------------------------------------+
```

### Core Harness Components

1. **`MockKing`**:
   - HTTP/WSS server running on `httptest.Server`.
   - `/register` WebSocket handler validating `X-Node-ID` and `Authorization` headers.
   - `/<device_id>/mcp` HTTP ingress handler with UUID request ID rewriting.
   - Thread-safe `PendingRequests` map (`map[string]chan *JSONRPCResponse`) with configurable timeout (default 30s).
   - Single-use pairing code registry and token issuer.

2. **`MockWorker`**:
   - Outbound WebSocket client connecting with custom HTTP headers.
   - Thread-safe local `OutboxQueue` for response buffering.
   - Simulated network dropouts (`Disconnect()`) and reconnection (`Reconnect()`).
   - Auto-flushing mechanism sending queued responses upon WSS handshake completion.
   - Configurable tool call handlers for executing MCP commands (`tools/call`, `tools/list`).

3. **`CLIRunner`**:
   - Utility for executing compiled `dca` binaries or CLI entrypoints in isolated subprocesses.
   - Captures `stdout`, `stderr`, and process exit codes.
   - Context-aware cancellation for timeout management.

4. **JSON-RPC 2.0 Helpers**:
   - Serializers and deserializers for standard JSON-RPC 2.0 request/response/error payloads.
   - UUID mapping functions (`RewriteID`, `RestoreID`).
   - Assertion functions (`AssertJSONRPCResponse`, `AssertJSONRPCError`).

---

## 4. 4-Tier Test Matrix & Coverage Goals

### Tier 1: Functional Feature Baseline (≥5 Test Cases per Feature = 35+ Total)

#### Feature 1: Worker Pairing Code Generation
- `TC-F1-01`: Code Format Validation — Verify generated pairing code is exactly 6 characters matching `^[A-Z0-9]{6}$`.
- `TC-F1-02`: Entropy & Uniqueness — Generate 1,000 pairing codes and verify zero collisions across iterations.
- `TC-F1-03`: Un-paired Worker Startup State — Verify un-paired worker initializes in pending pairing state and outputs valid 6-char code.
- `TC-F1-04`: Code Expiration Enforcement — Verify pairing code becomes invalid after configured TTL window.
- `TC-F1-05`: State Persistence Across Restarts — Verify worker preserves pairing code across un-paired daemon restarts until paired.

#### Feature 2: Worker WSS Registration Headers
- `TC-F2-01`: Valid Handshake Registration — Connect with valid `X-Node-ID` and `Authorization: Bearer <token>` headers; verify 101 Switching Protocols.
- `TC-F2-02`: Missing `X-Node-ID` Header — Connect with valid token but missing `X-Node-ID`; verify King rejects connection with HTTP 400/401.
- `TC-F2-03`: Missing `Authorization` Header — Connect with valid `X-Node-ID` but missing `Authorization`; verify King rejects with HTTP 401.
- `TC-F2-04`: Invalid Bearer Token — Connect with corrupted or non-existent token; verify King rejects connection with HTTP 403.
- `TC-F2-05`: Header Case Insensitivity & Sanitization — Verify King correctly parses normalized `X-Node-ID` and Bearer token regardless of whitespace or casing anomalies.

#### Feature 3: Worker Outbox Async Buffering & Session Resumption
- `TC-F3-01`: Disconnected Outbox Enqueue — Trigger tool execution when WSS is disconnected; verify response is buffered in Outbox queue without loss.
- `TC-F3-02`: Auto-Flush on Reconnection — Reconnect worker WSS; verify queued Outbox messages auto-flush in original FIFO order.
- `TC-F3-03`: Outbox Idempotency & De-duplication — Verify flushed outbox items are cleared from local queue and not re-transmitted twice.
- `TC-F3-04`: Outbox Capacity & Saturation Handling — Buffer high volume of messages in Outbox during extended outage; verify queue integrity and non-blocking operation.
- `TC-F3-05`: Multi-Disconnect Accumulation — Simulate multiple disconnect/reconnect cycles; verify outbox correctly accumulates and drains across cycles.

#### Feature 4: King Device Addition & Single-Use Token Issuance
- `TC-F4-01`: Successful Device Addition — Execute device addition with valid pairing code; verify single-use pair token is issued and device registered.
- `TC-F4-02`: Single-Use Code Consumption — Re-use an already consumed pairing code; verify King rejects request with code consumption error.
- `TC-F4-03`: Non-existent Pairing Code — Supply invalid/random 6-char code; verify King returns device pairing error.
- `TC-F4-04`: Token Cryptographic Integrity — Verify issued pair token contains encrypted device identity and signature verifiable by King.
- `TC-F4-05`: Concurrent Pairing Invocations — Issue parallel device addition requests for different codes; verify atomic pair state updates without race conditions.

#### Feature 5: King HTTP Ingress Routing
- `TC-F5-01`: End-to-End Ingress Tool Call — Post JSON-RPC tool call to `POST /<device_id>/mcp`; verify payload routes to target worker and returns result.
- `TC-F5-02`: Tool Name Preservation — Verify tool names (e.g. `read_file`, `exec_cmd`) pass through King unchanged without prefixing or renaming.
- `TC-F5-03`: Non-Existent Device Ingress — Post request to `POST /invalid-device/mcp`; verify King returns HTTP 404 / JSON-RPC error.
- `TC-F5-04`: Malformed JSON-RPC Body — Send invalid JSON payload to ingress endpoint; verify King returns HTTP 400 with JSON-RPC error code `-32700`.
- `TC-F5-05`: Concurrent Ingress Handling — Dispatch 50 simultaneous HTTP ingress requests to same worker; verify all complete with correct response mapping.

#### Feature 6: King PendingRequests Map, UUID Rewriting & 30-Second Recovery Window
- `TC-F6-01`: Request ID UUID Rewriting — Send request with integer ID (`101`); verify King rewrites request ID to random UUID over WSS tunnel and restores original ID in HTTP response.
- `TC-F6-02`: Session Recovery Within 30s Window — Disconnect worker while request is pending; reconnect worker and flush response at t=5s; verify HTTP ingress request resolves successfully.
- `TC-F6-03`: Recovery Window Timeout at 30s — Disconnect worker while request is pending; keep worker offline past 30s; verify King times out request and returns Gateway Timeout (504 / JSON-RPC timeout error).
- `TC-F6-04`: Pending Map Cleanup on Timeout — Verify timed-out requests are purged from King `PendingRequests` map to prevent memory leaks.
- `TC-F6-05`: Out-of-Order Response Matching — Dispatch multiple requests (A, B, C); worker returns responses in order (C, A, B); verify King routes each response to its respective pending HTTP handler.

#### Feature 7: CLI Subcommands & Config Integration
- `TC-F7-01`: `dca king` Command Dispatch — Execute `dca king` CLI subcommand; verify King control plane initializes with configured port and endpoints.
- `TC-F7-02`: `dca worker` Command Dispatch — Execute `dca worker` CLI subcommand; verify Worker daemon initializes and attempts WSS registration.
- `TC-F7-03`: `dca pair` CLI Utility — Execute `dca pair <code>`; verify pairing exchange executes cleanly against King control plane.
- `TC-F7-04`: `ServerConfig` JSON File Integration — Load custom `ServerConfig` JSON file via `--config`; verify all King/Worker parameters are parsed and respected.
- `TC-F7-05`: Standalone & Service Backward Compatibility — Execute `dca run` and service flag variants; verify existing standalone MCP functionality remains intact.

---

### Tier 2: Boundary & Corner Cases

- `TC-T2-01`: Pairing code case sensitivity & leading/trailing whitespace handling.
- `TC-T2-02`: WSS connection drop exactly at 29.99s vs 30.01s relative to recovery window.
- `TC-T2-03`: Duplicate `X-Node-ID` registration while previous connection is still active (connection preemption vs rejection).
- `TC-T2-04`: Abrupt TCP FIN/RST packet without clean WebSocket closure frame.
- `TC-T2-05`: Micro-chunked JSON-RPC frame delivery over TCP socket.
- `TC-T2-06`: Max payload size limit enforcement on HTTP ingress (`POST /<device_id>/mcp`).
- `TC-T2-07`: Rapid reconnect loop (flapping worker connection 100 times in 1 second).

---

### Tier 3: Cross-Feature Integration Scenarios

- `TC-T3-01`: **Full Lifecycle Resilience**:
  1. Worker generates 6-char pairing code.
  2. CLI executes `dca king add-device <code>`, obtaining single-use token.
  3. Worker connects WSS to `/register` with `X-Node-ID` and token.
  4. HTTP ingress receives `tools/call` for `/<device_id>/mcp`.
  5. King rewrites ID to UUID and forwards over WSS.
  6. Network drops mid-execution; worker completes tool call locally and enqueues to Outbox.
  7. Worker reconnects WSS within 10 seconds.
  8. Outbox flushes queued response over WSS.
  9. King matches UUID in `PendingRequests` map, restores original request ID, and returns HTTP 200 to ingress client.

- `TC-T3-02`: **Multi-Worker Route Isolation**:
  - Register Worker A and Worker B simultaneously on same King instance.
  - Dispatch interleaved HTTP ingress requests to `/<device_id_A>/mcp` and `/<device_id_B>/mcp`.
  - Verify strict route isolation, correct response routing, and zero tool crosstalk.

---

### Tier 4: Real-World Application Scenarios

- `TC-T4-01`: **High-Concurrency Multi-Worker Network**:
  - 10 active Worker daemons connected over WSS to 1 King control plane.
  - 500 concurrent tool execution calls distributed randomly across all 10 workers.
  - 10% simulated random disconnect rate during execution.
  - Verification: 100% request completion rate via outbox recovery with zero lost or mismatched responses.

- `TC-T4-02`: **King Restart & Session Recovery**:
  - King control plane restarts while 5 workers are actively queued.
  - Worker daemons detect drop, hold outbox items, retry backoff, and re-establish WSS session upon King restoration.

---

## 5. Execution Guide & Verification

### Running E2E Harness Tests

```bash
# Compile and run E2E test harness
go test -v ./tests/e2e/...

# Verify test binary compilation without execution
go test -c ./tests/e2e/...
```

---
*End of E2E Test Infrastructure Specification & Test Plan*
