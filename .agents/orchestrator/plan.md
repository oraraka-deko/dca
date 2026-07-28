# Project Orchestration Plan: Distributed MCP Gateway Architecture (King-Worker Pattern in Go)

## Executive Summary
This plan details the architecture, decomposition, subagent delegation, dual-track verification, and quality gates for implementing the Distributed MCP Gateway in `dca`.

## Requirements Summary
1. **R1: Worker Daemon Mode & Reverse Tunnel with Outbox Pattern**
   - `dca worker` command & flag parsing.
   - Generates 6-character alphanumeric pairing code when un-paired.
   - Persistent WebSocket connection (`wss://<king>/register`) with HTTP headers (`X-Node-ID`, `Authorization`).
   - Isolated goroutines for tool executions + thread-safe `Outbox` queue for session resumption across socket drops.

2. **R2: King Control Plane Gateway Mode & Decoupled Session Waiter**
   - `dca king` command & `dca king add-device <code>`.
   - Single-use pairing code validation and activation key token issuance. Re-use attempts fail.
   - Protocol Inversion: King listens on `/register` for WebSocket connections, holds `ActiveConns` in a `sync.Map`, acts as MCP Client (`initialize`, `tools/list`).
   - URL Route-Based MCP Ingress: `/<device_id>/mcp` forwards agent calls down target device tunnel without tool renaming/prefixing.
   - Transport-Agnostic Pending Map & ID Rewriting: Agent request IDs rewritten to UUIDs; pending channels kept in `PendingRequests` `sync.Map` with 30s recovery window for socket drops.

3. **R3: CLI & Config Integration**
   - Extend `ServerConfig` struct in `utils/server_config.go`.
   - Add CLI subcommands (`dca king`, `dca worker`, `dca pair`).
   - Preserve existing standalone (`dca run`), installer, Windows/Linux service handlers, and TUI functionality without breaking changes.

4. **Acceptance Criteria & Verification**
   - `go test ./...` passes without errors.
   - Zero regression on existing standalone, service, and TUI modes.
   - Forensic Auditor verification CLEAN (zero hardcoded responses or facade implementations).

## Architecture & Data Flow

```
+-----------------------------------------------------------------------------------+
|                                 KING CONTROL PLANE                                |
|                                                                                   |
|  Agent Request --HTTP POST--> [ /<device_id>/mcp Ingress Handler ]                |
|                                         |                                         |
|                                (ID Rewriter -> UUID)                              |
|                                (PendingRequests sync.Map)                         |
|                                         |                                         |
|                                  [ ActiveConns Map ]                              |
|                                         |                                         |
|                                   WebSocket Tunnel                                |
+-----------------------------------------|-----------------------------------------+
                                          |
                                    Reverse WSS Tunnel (wss://<king>/register)
                                    (Headers: X-Node-ID, Authorization)
                                          |
+-----------------------------------------v-----------------------------------------+
|                                   WORKER DAEMON                                   |
|                                                                                   |
|  [ WSS Tunnel Client ] <--- (Outbox Queue) <--- [ Tool Execution Goroutines ]     |
|         |                                                  |                      |
|  (Session Resumption)                              (MCP Server / Local Tools)     |
+-----------------------------------------------------------------------------------+
```

## Milestones & Decomposition

| # | Milestone Name | Scope & Responsibilities | Key Dependencies | Target Directory |
|---|----------------|--------------------------|------------------|------------------|
| M0 | E2E Testing Track | Requirement-driven test suite (Tiers 1-4: pairing, WSS tunnel, outbox, King router, CLI) | None | `.agents/e2e_tester` |
| M1 | R1: Worker Daemon & Outbox | `dca worker` mode, 6-char pairing code, WSS tunnel client, async Outbox session resumption queue | None | `.agents/sub_orch_m1` |
| M2 | R2: King Control Plane & Router | `dca king`, single-use pairing exchange, WSS `/register` protocol inversion, `/<device_id>/mcp` ingress, `PendingRequests` ID rewriting | M1 interface specs | `.agents/sub_orch_m2` |
| M3 | R3: CLI & Config Integration | Extend `ServerConfig`, integrate `dca king`/`worker`/`pair` commands into `main.go` and CLI parser, backward compatibility | M1, M2 | `.agents/sub_orch_m3` |
| M4 | Final Integration & Adversarial Hardening | E2E Test Suite verification (Tiers 1-4) + Tier 5 Adversarial Coverage Hardening | M0, M1, M2, M3 | `.agents/sub_orch_m4` |

## Code Layout Plan
- `utils/worker_daemon.go`, `utils/worker_daemon_test.go` — Worker daemon loop, code generator, WSS client.
- `utils/outbox.go`, `utils/outbox_test.go` — Thread-safe session resumption outbox queue.
- `utils/king_gateway.go`, `utils/king_gateway_test.go` — King gateway, WSS `/register` handler, pairing manager, `ActiveConns` map.
- `utils/king_ingress.go`, `utils/king_ingress_test.go` — `/<device_id>/mcp` handler, `PendingRequests` UUID rewriter and session waiter.
- `utils/pairing.go`, `utils/pairing_test.go` — Single-use pairing exchange & token store.
- `utils/server_config.go` — Extended `ServerConfig` struct with King/Worker settings.
- `main.go` — CLI routing for `king`, `worker`, `pair`, preserving `run`, `start`, `stop`, `status`, `uninstall`, `install`.

## Quality Gates & Verification Protocol
1. Forensic Audit (CLEAN verdict required).
2. Unit tests for each package.
3. `go test ./...` passes with zero failures.
4. E2E Test Suite pass 100%.
