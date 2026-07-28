## 2026-07-28T08:18:37Z
You are an E2E Test Infrastructure Engineer working on project `dca`.
Your working directory is `d:\Documents\dca\.agents\worker_infra`.

MANDATORY INTEGRITY WARNING:
DO NOT CHEAT. All implementations must be genuine. DO NOT hardcode test results, create dummy/facade implementations, or circumvent the intended task. A Forensic Auditor will independently verify your work. Integrity violations WILL be detected and your work WILL be rejected.

Your objective:
1. Write `d:\Documents\dca\TEST_INFRA.md` at project root establishing the E2E test philosophy, feature inventory, test architecture, and coverage goals across 4 Tiers:
   - Philosophy: Opaque-box, requirement-driven testing using Go `testing` package.
   - 7 Core Features:
     1. Worker pairing code generation (6-char code)
     2. Worker WSS registration headers (`X-Node-ID`, `Authorization`)
     3. Worker Outbox async buffering & session resumption queue
     4. King device addition (`dca king add-device <code>`) and single-use token issuance
     5. King HTTP ingress (`/<device_id>/mcp`) routing without tool renaming
     6. King PendingRequests map, UUID request ID rewriting, and 30-second session recovery window
     7. CLI subcommands (`dca king`, `dca worker`, `dca pair`) and ServerConfig integration while preserving standalone/service compatibility.
   - Tier 1 Coverage: ≥5 test cases per feature (≥35 total).
   - Tier 2 Coverage: Boundary & corner cases.
   - Tier 3 Coverage: Cross-feature combinations.
   - Tier 4 Coverage: Real-world application scenarios.

2. Create `d:\Documents\dca\tests\e2e` directory and write `tests/e2e/harness.go` (in package `e2e` or `e2e_test`).
   The harness must provide test structures and utilities for E2E tests:
   - `MockKing`: Programmatic mock King gateway server with `/register` WebSocket endpoint, `/<device_id>/mcp` HTTP POST endpoint, pairing code validation, pending request UUID rewriting, and outbox response matching.
   - `MockWorker`: Programmatic mock Worker client capable of connecting over WSS with custom HTTP headers (`X-Node-ID`, `Authorization`), local outbox queue, handling tool execution calls, simulating network disconnects, and auto-flushing queued responses upon reconnect.
   - `CLIRunner`: Utility for testing CLI binary/commands (`dca king`, `dca worker`, `dca pair`, `dca run`).
   - JSON-RPC 2.0 helper functions for request/response serialization, ID mapping, and assertions.

3. Verify that `tests/e2e/harness.go` compiles cleanly by running `go test -c ./tests/e2e/...` or `go test ./tests/e2e/...`.

4. Document all changes and verification outputs in `d:\Documents\dca\.agents\worker_infra\handoff.md`.
