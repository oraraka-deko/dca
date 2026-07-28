# Original User Request

## 2026-07-28T15:23:33Z

You are the Sub-Orchestrator for Milestone 2: King Control Plane Gateway Mode & Decoupled Router (R2).
Your working directory is `d:\Documents\dca\.agents\sub_orch_m2`.
Your parent is top-level Project Orchestrator (conversation ID: 340a979a-c757-4387-bb0e-d40454b1b8e8).

## Mission
Plan, coordinate, and supervise the complete implementation and verification of Milestone 2 (R2):
1. Single-use pairing exchange (`dca king add-device <code>`), code validation, single-use activation key token issuance in `utils/pairing.go`. Verify code reuse attempts fail.
2. Protocol Inversion WebSocket server in `utils/king_gateway.go`: King listens on `/register` for Worker WSS connections, validates `X-Node-ID` and `Authorization` headers, stores active connections in thread-safe `ActiveConns` (`sync.Map`), and acts as MCP Client (`initialize`, `tools/list`).
3. URL Route-Based MCP Ingress in `utils/king_ingress.go`: Ingress at `/<device_id>/mcp` forwards incoming JSON-RPC calls down the worker WebSocket tunnel without tool renaming/prefixing.
4. Transport-Agnostic Pending Map & ID Rewriting in `utils/king_ingress.go`: Rewrites agent request `id`s to unique UUIDs, tracks response channels in `PendingRequests` (`sync.Map`) with a 30-second recovery timeout window decoupling logical requests from physical socket drops.

## Workflow & Guidelines
1. Create directory `d:\Documents\dca\.agents\sub_orch_m2` if needed.
2. Create `SCOPE.md`, `BRIEFING.md`, `progress.md` in your working directory.
3. Apply Project Pattern iteration loop for M2:
   a. Spawn 3 Explorers (`explorer_m2_1`, `explorer_m2_2`, `explorer_m2_3` using `teamwork_preview_explorer`) to analyze requirements, data structures, and edge cases.
   b. Spawn 1 Worker (`worker_m2` using `teamwork_preview_worker`) to implement `utils/pairing.go`, `utils/king_gateway.go`, `utils/king_ingress.go`, and their unit tests. Include mandatory integrity warning in prompt.
   c. Spawn 2 Reviewers (`teamwork_preview_reviewer`) to independently review implementation for correctness, thread safety, and specification compliance.
   d. Spawn 2 Challengers (`teamwork_preview_challenger`) to stress-test single-use code reuse prevention, request ID rewriting, and socket disconnect resumption during pending HTTP requests.
   e. Spawn 1 Forensic Auditor (`teamwork_preview_auditor`) for integrity verification.
   f. Gate Verification: Auditor must be CLEAN, zero reviewer vetoes, challenger confirmation, unit tests (`go test ./utils/...`) pass.
4. Deliver `handoff.md` in `.agents/sub_orch_m2/` and notify parent orchestrator via `send_message`.

DO NOT write source code directly. Delegate all implementation and testing work to subagents.
