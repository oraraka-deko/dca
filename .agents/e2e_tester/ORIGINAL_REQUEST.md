# Original Request — E2E Testing Track

## Initial Request — 2026-07-28T08:18:13-07:00

You are the E2E Testing Orchestrator for project `dca`.
Your working directory is `d:\Documents\dca\.agents\e2e_tester`.
Your parent orchestrator conversation ID is `a043b2d1-98c3-4961-b34a-54efa2ea4f8f`.

Your mission is to design, implement, and verify a comprehensive, opaque-box, requirement-driven E2E test suite in Go for the Distributed MCP Gateway Architecture (King-Worker Pattern).

Key Instructions:
1. Create your working directory `d:\Documents\dca\.agents\e2e_tester`. Initialize `BRIEFING.md` and `progress.md`.
2. Read `d:\Documents\dca\PROJECT.md` and `d:\Documents\dca\.agents\ORIGINAL_REQUEST.md` for requirements and specification details.
3. Write `TEST_INFRA.md` at project root (`d:\Documents\dca\TEST_INFRA.md`) establishing the E2E test philosophy, feature inventory, test architecture, and coverage goals across 4 Tiers:
   - Tier 1: Feature Coverage (≥5 tests per feature: pairing code generation, WSS registration headers, Outbox buffering, King device addition, `/<device_id>/mcp` forwarding, pending request ID rewriting, CLI subcommands).
   - Tier 2: Boundary & Corner cases (unpaired worker behavior, key reuse attempts, socket disconnect mid-tool-execution, timeout recovery window, invalid device IDs).
   - Tier 3: Cross-Feature Combinations (full lifecycle pairing -> WSS connection -> agent MCP call -> outbox flush on reconnect).
   - Tier 4: Real-World Application Scenarios (multiple workers behind NATs executing tools concurrently, transient connection loss recovery).
4. Implement the test files in `tests/e2e/` using standard `go test` (`testing` package in Go, e.g. `tests/e2e/...` or package `e2e_test`).
5. Ensure the test harness can launch mock/real King and Worker instances programmatically or via binary execution.
6. Verify all test files compile cleanly.
7. Once the test suite infrastructure and all Tier 1-4 test cases are written and ready to be run, publish `TEST_READY.md` at `d:\Documents\dca\TEST_READY.md`.
8. Spawn worker subagents (using `teamwork_preview_worker`) as needed to implement the test files and test harness. Do NOT modify source code directly.
9. Deliver `handoff.md` in your working directory and notify the parent orchestrator via `send_message`.
