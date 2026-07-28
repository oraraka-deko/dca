## 2026-07-28T15:21:55Z
You are an E2E Test Implementation Engineer working on project `dca`.
Your working directory is `d:\Documents\dca\.agents\worker_tier1_tier2`.

MANDATORY INTEGRITY WARNING:
DO NOT CHEAT. All implementations must be genuine. DO NOT hardcode test results, create dummy/facade implementations, or circumvent the intended task. A Forensic Auditor will independently verify your work. Integrity violations WILL be detected and your work WILL be rejected.

Your objectives:
1. Read `d:\Documents\dca\TEST_INFRA.md` for full test specifications.
2. Implement `tests/e2e/tier1_feature_test.go` (in package `e2e` or `e2e_test`) containing at least 35 test cases covering all 7 features (≥5 test cases per feature):
   - Feature 1: Worker pairing code generation (`TestTier1_WorkerPairingCode_Format`, `TestTier1_WorkerPairingCode_Entropy`, `TestTier1_WorkerPairingCode_UnpairedState`, `TestTier1_WorkerPairingCode_Expiration`, `TestTier1_WorkerPairingCode_Persistence`).
   - Feature 2: Worker WSS registration headers (`TestTier1_WSSHeaders_ValidHandshake`, `TestTier1_WSSHeaders_MissingNodeID`, `TestTier1_WSSHeaders_MissingAuth`, `TestTier1_WSSHeaders_InvalidToken`, `TestTier1_WSSHeaders_CaseSanitization`).
   - Feature 3: Worker Outbox async buffering & session resumption queue (`TestTier1_Outbox_DisconnectedEnqueue`, `TestTier1_Outbox_AutoFlushOnReconnect`, `TestTier1_Outbox_Idempotency`, `TestTier1_Outbox_HighVolumeSaturation`, `TestTier1_Outbox_MultiDisconnectAccumulation`).
   - Feature 4: King device addition & single-use token issuance (`TestTier1_DeviceAddition_Success`, `TestTier1_DeviceAddition_SingleUseCodeConsumption`, `TestTier1_DeviceAddition_NonExistentCode`, `TestTier1_DeviceAddition_TokenIntegrity`, `TestTier1_DeviceAddition_ConcurrentInvocations`).
   - Feature 5: King HTTP Ingress routing (`TestTier1_HTTPIngress_ToolCallForwarding`, `TestTier1_HTTPIngress_ToolNamePreservation`, `TestTier1_HTTPIngress_NonExistentDevice`, `TestTier1_HTTPIngress_MalformedJSONRPC`, `TestTier1_HTTPIngress_ConcurrentRequests`).
   - Feature 6: King PendingRequests map, UUID ID rewriting & 30s recovery window (`TestTier1_PendingRequests_UUIDRewriting`, `TestTier1_PendingRequests_RecoveryWithinWindow`, `TestTier1_PendingRequests_RecoveryWindowTimeout`, `TestTier1_PendingRequests_MapCleanupOnTimeout`, `TestTier1_PendingRequests_OutOfOrderResponseMatching`).
   - Feature 7: CLI subcommands & config integration (`TestTier1_CLI_KingSubcommand`, `TestTier1_CLI_WorkerSubcommand`, `TestTier1_CLI_PairSubcommand`, `TestTier1_CLI_ServerConfigJSON`, `TestTier1_CLI_StandaloneBackwardCompatibility`).

3. Implement `tests/e2e/tier2_boundary_test.go` (in package `e2e` or `e2e_test`) containing Tier 2 boundary and corner case test cases:
   - `TestTier2_PairingCode_CaseAndWhitespace`
   - `TestTier2_WSS_DropAtRecoveryWindowBoundary` (29.9s vs 30.1s)
   - `TestTier2_WSS_DuplicateNodeIDRegistration` (preemption vs rejection)
   - `TestTier2_WSS_AbruptTCPDropWithoutCloseFrame`
   - `TestTier2_WSS_MicroChunkedJSONRPCDelivery`
   - `TestTier2_HTTPIngress_MaxPayloadSizeLimit`
   - `TestTier2_WSS_RapidReconnectLoop` (flapping connection)

4. Use the `harness.go` utilities (`MockKing`, `MockWorker`, `CLIRunner`, JSON-RPC helpers).

5. Verify compilation and test suite execution by running `go test -v ./tests/e2e/...` in `d:\Documents\dca`. Ensure ALL tests pass.

6. Document test results and handoff report in `d:\Documents\dca\.agents\worker_tier1_tier2\handoff.md`.
