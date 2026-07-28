## 2026-07-28T15:21:56Z
You are an E2E Test Implementation Engineer working on project `dca`.
Your working directory is `d:\Documents\dca\.agents\worker_tier3_tier4`.

MANDATORY INTEGRITY WARNING:
DO NOT CHEAT. All implementations must be genuine. DO NOT hardcode test results, create dummy/facade implementations, or circumvent the intended task. A Forensic Auditor will independently verify your work. Integrity violations WILL be detected and your work WILL be rejected.

Your objectives:
1. Read `d:\Documents\dca\TEST_INFRA.md` for full test specifications.
2. Implement `tests/e2e/tier3_cross_feature_test.go` (in package `e2e` or `e2e_test`) containing Tier 3 cross-feature integration test cases:
   - `TestTier3_FullLifecycleResilience`: Full lifecycle test sequence:
     1. Un-paired worker generates 6-char pairing code.
     2. King `add-device` consumes code and issues single-use token.
     3. Worker registers over WSS at `/register` with `X-Node-ID` and token.
     4. Client POSTs MCP tool request to King `/<device_id>/mcp`.
     5. King rewrites request ID to random UUID and tunnels to worker over WSS.
     6. WSS drops mid-tool-execution; worker places result in Outbox queue.
     7. Worker reconnects WSS within recovery window.
     8. Outbox flushes queued response over WSS.
     9. King matches UUID in `PendingRequests` map, restores original request ID, and returns HTTP response to client.
   - `TestTier3_MultiWorkerRouteIsolation`: Multiple Workers (A and B) registered simultaneously on single King instance; concurrent interleaved MCP HTTP ingress calls to `/<device_id_A>/mcp` and `/<device_id_B>/mcp`; verify complete route isolation, correct response routing, and zero tool crosstalk.

3. Implement `tests/e2e/tier4_real_world_test.go` (in package `e2e` or `e2e_test`) containing Tier 4 real-world application scenario test cases:
   - `TestTier4_HighConcurrencyMultiWorkerNAT`: 10 active Worker daemons connected over WSS to 1 King control plane; 500 concurrent tool calls distributed across workers with simulated 10% random network dropouts; verify 100% completion rate via Outbox recovery with zero lost/mismatched responses.
   - `TestTier4_KingRestartSessionRecovery`: King control plane restarts while workers are executing; workers detect drop, hold responses in local outbox, retry backoff, re-establish WSS sessions upon King restart, and complete all pending work.

4. Use the `harness.go` utilities (`MockKing`, `MockWorker`, `CLIRunner`, JSON-RPC helpers).

5. Verify clean compilation and test execution by running `go test -v ./tests/e2e/...` in `d:\Documents\dca`. Ensure ALL tests pass.

6. Document test results and handoff report in `d:\Documents\dca\.agents\worker_tier3_tier4\handoff.md`.
