# Scope: Milestone 3 - CLI & Config Integration (R3)

## Architecture
- `utils/server_config.go`: Extend `ServerConfig` struct with King/Worker fields (`KingAddress`, `PairCode`, `PairToken`, `NodeID`, `IngressPort`, `WorkerMode`, `KingMode`, etc.), supporting serialization, saving/loading, and defaults.
- `main.go`: Subcommand parsing and routing. Retain existing subcommands (`run`, `start`, `stop`, `status`, `install`, `uninstall`, TUI default) and add new King/Worker subcommands (`dca king`, `dca worker`, `dca pair`).

## Milestones & Work Items
| # | Name | Scope | Dependencies | Status |
|---|------|-------|-------------|--------|
| 1 | M3: CLI & Config Integration | Extend ServerConfig & Add `king`, `worker`, `pair` subcommands | M1, M2 | IN_PROGRESS |

## Interface Contracts
### CLI Interface
- `dca run` [flags]: Run standalone / default node daemon.
- `dca king` [flags]: Run King node daemon.
- `dca worker` [flags]: Run Worker node daemon.
- `dca pair` [flags]: Execute pair registration / code exchange command.
- Service subcommands: `start`, `stop`, `status`, `install`, `uninstall` preserved with unchanged semantics.

### ServerConfig Extensions
```go
type ServerConfig struct {
    // Existing fields preserved
    ...
    // King/Worker Extensions
    KingAddress string `json:"king_address" yaml:"king_address"`
    PairCode    string `json:"pair_code" yaml:"pair_code"`
    PairToken   string `json:"pair_token" yaml:"pair_token"`
    NodeID      string `json:"node_id" yaml:"node_id"`
    IngressPort int    `json:"ingress_port" yaml:"ingress_port"`
    WorkerMode  bool   `json:"worker_mode" yaml:"worker_mode"`
    KingMode    bool   `json:"king_mode" yaml:"king_mode"`
}
```
