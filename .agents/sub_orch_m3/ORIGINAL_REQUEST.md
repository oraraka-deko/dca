# Original User Request

## Initial Request — 2026-07-28T08:23:42-07:00

You are the Sub-Orchestrator for Milestone 3: CLI & Config Integration (R3).
Your working directory is `d:\Documents\dca\.agents\sub_orch_m3`.
Your parent is top-level Project Orchestrator (conversation ID: 340a979a-c757-4387-bb0e-d40454b1b8e8).

## Mission
Plan, coordinate, and supervise the complete implementation and verification of Milestone 3 (R3):
1. Extend `ServerConfig` struct in `utils/server_config.go` with King/Worker configuration fields (e.g. `KingAddress`, `PairCode`, `PairToken`, `NodeID`, `IngressPort`, `WorkerMode`, `KingMode`).
2. Add CLI subcommands (`dca king`, `dca worker`, `dca pair`) to `main.go`.
3. Ensure 100% backward compatibility: existing standalone (`dca run`), service handlers (`start`, `stop`, `status`, `install`, `uninstall`), installer, and TUI functionality must work without breaking changes or regressions.

## Workflow & Guidelines
1. Create directory `d:\Documents\dca\.agents\sub_orch_m3` if needed.
2. Create `SCOPE.md`, `BRIEFING.md`, `progress.md` in your working directory.
3. Apply Project Pattern iteration loop for M3:
   a. Spawn 3 Explorers (`explorer_m3_1`, `explorer_m3_2`, `explorer_m3_3` using `teamwork_preview_explorer`) to inspect `main.go`, `utils/server_config.go`, and existing subcommand routing.
   b. Spawn 1 Worker (`worker_m3` using `teamwork_preview_worker`) to implement `utils/server_config.go` extensions, CLI routing in `main.go`, and corresponding unit tests. Include mandatory integrity warning in prompt.
   c. Spawn 2 Reviewers (`teamwork_preview_reviewer`) to independently verify backward compatibility and CLI behavior.
   d. Spawn 2 Challengers (`teamwork_preview_challenger`) to stress-test CLI flag parsing, invalid config handling, and service subcommand preservation.
   e. Spawn 1 Forensic Auditor (`teamwork_preview_auditor`) for integrity verification.
   f. Gate Verification: Auditor must be CLEAN, zero reviewer vetoes, challenger confirmation, `go test ./...` passes.
4. Deliver `handoff.md` in `.agents/sub_orch_m3/` and notify parent orchestrator via `send_message`.
