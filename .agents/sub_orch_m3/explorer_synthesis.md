# Explorer Synthesis: Milestone 3 (CLI & Config Integration)

## Executive Summary
Explorers 1, 2, and 3 have completed thorough read-only analysis of `utils/server_config.go`, `main.go`, installer/service handlers, and repository test suites. The implementation strategy guarantees 100% backward compatibility for existing standalone, TUI, and background service execution paths while introducing `king`, `worker`, and `pair` subcommands and extended `ServerConfig` fields.

## Key Findings & Consensus

### 1. `ServerConfig` Extension (`utils/server_config.go`)
- Extend `ServerConfig` struct with 7 new King/Worker fields:
  ```go
  KingAddress string `json:"king_address,omitempty" yaml:"king_address,omitempty"`
  PairCode    string `json:"pair_code,omitempty" yaml:"pair_code,omitempty"`
  PairToken   string `json:"pair_token,omitempty" yaml:"pair_token,omitempty"`
  NodeID      string `json:"node_id,omitempty" yaml:"node_id,omitempty"`
  IngressPort int    `json:"ingress_port,omitempty" yaml:"ingress_port,omitempty"`
  WorkerMode  bool   `json:"worker_mode,omitempty" yaml:"worker_mode,omitempty"`
  KingMode    bool   `json:"king_mode,omitempty" yaml:"king_mode,omitempty"`
  ```
- Implement `(cfg *ServerConfig) Validate() error` to validate configuration constraints (e.g., cannot be both King and Worker; King `IngressPort` cannot collide with standard `Port`).
- Implement `ApplyEnvOverrides()` for `DCA_` prefixed environment variables.
- Default `IngressPort` to `9090` when `KingMode == true` and `IngressPort == 0`.

### 2. Main Subcommand Routing & CLI Parsing (`main.go`)
- Extend positional subcommand evaluation in `main.go` after global `flag.Parse()`:
  - Existing subcommands preserved: `run` / `foreground`, `start`, `stop`, `status`, `restart`, `install`, `uninstall`, and interactive Bubbletea TUI default when terminal and no args.
  - New subcommands using `flag.NewFlagSet`:
    - `king`: `--port`, `--ingress-port`, `--auth-token`, `--config`
    - `worker`: `--king`, `--pair-code`, `--node-id`, `--config`
    - `pair`: `--code`, `--king`, `--node-id`, `--config` (also supporting positional code `dca pair 123456`)
- Ensure 3-tier precedence: **CLI Flag > Environment Variable > Config File > Default Fallback**.

### 3. Service Handlers & Backward Compatibility
- OS background service runners (Windows SCM via `main_windows.go` and systemd via `installer.go`) pass `-config <path>`:
  - `ServerConfig` JSON files without King/Worker fields unmarshal seamlessly with `KingMode: false` and `WorkerMode: false`.
  - Service handlers operate unchanged.

### 4. Codebase Fix & Verification Requirements
- `utils/pairing_code_test.go:4:2` has an unused `"os"` import preventing `go test ./...` compilation. The worker MUST remove this unused import so `go test ./...` builds and passes cleanly.
- Worker must write comprehensive unit tests in `utils/server_config_test.go` and `main_test.go` (or `cli_test.go`).

## Worker Action Plan
1. Fix unused `"os"` import in `utils/pairing_code_test.go`.
2. Extend `ServerConfig` struct in `utils/server_config.go` with 7 King/Worker fields, `Validate()`, `ApplyEnvOverrides()`, and updated `DefaultServerConfig()`.
3. Add `king`, `worker`, and `pair` subcommand handling to `main.go` using `flag.NewFlagSet` and sub-mode dispatchers.
4. Add unit tests for config extensions in `utils/server_config_test.go` and CLI subcommand parsing in `main_test.go`.
5. Run `go test ./...` and verify all package tests compile and pass.
