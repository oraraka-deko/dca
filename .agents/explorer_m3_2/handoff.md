# Handoff Report: Milestone 3 (CLI & Config Integration - Subcommand Routing & Flag Parsing)

## 1. Observation
- **File Paths Examined**:
  - `main.go`: Main binary entry point (lines 1-151). Standard `flag.Parse()` consumes global flags like `-config`, followed by `utils.LoadServerConfig(...)`, `runAsService(*cfg)` check, and positional subcommand routing `switch cmd` (`status`, `start`, `stop`, `restart`, `uninstall`, `run`/`foreground`, `install`). Default fallback launches interactive Bubbletea TUI if terminal, or runs standalone server in foreground if non-terminal.
  - `main_windows.go`: Windows Service dispatcher (lines 1-77). Checks `svc.IsWindowsService()`, instantiates `utils.NewMCPServerWrapper(m.cfg)`, and runs `StartServer(ctx)` inside SCM service loop.
  - `main_unix.go`: Unix/Linux service helper (lines 1-12). Returns `(false, nil)` as systemd executes binaries as standard foreground processes.
  - `installer/installer.go`: Service management and installation (lines 1-260). `InstallService(cfg, configPath)` serializes `cfg` to `configPath` JSON file and generates systemd unit file (`ExecStart=%s -config %s`) on Linux or Windows service (`sc.exe create mymcp binPath= "%s -config %s"`).
  - `installer/tui.go`: Bubbletea interactive manager (lines 1-791). Allows starting/stopping service, running foreground server, interactive setup wizard, viewing logs/status.
  - `utils/server_config.go`: Server configuration struct and methods (lines 1-182). Supports JSON serialization, default values, save/load, and HTTP authentication validation.
  - `.agents/sub_orch_m3/SCOPE.md`: Milestone 3 specification detailing CLI interface contracts and `ServerConfig` extensions (`KingAddress`, `PairCode`, `PairToken`, `NodeID`, `IngressPort`, `WorkerMode`, `KingMode`).

## 2. Logic Chain
1. **Goal**: Add new subcommands `king`, `worker`, and `pair` while retaining 100% backward compatibility for all existing execution paths (`dca` without args, `dca run`, `dca install`, service management subcommands, and OS background services).
2. **Current Routing Mechanism**: `main.go` parses global flags via standard `flag.Parse()`, checks Windows service runner via `runAsService(*cfg)`, and then evaluates positional args via `flag.Args()`.
3. **Subcommand Routing Extension**:
   - By inserting `case "king":`, `case "worker":`, and `case "pair":` into the existing `switch cmd` block in `main.go`, we route the new commands without affecting any existing cases (`status`, `start`, `stop`, `restart`, `uninstall`, `install`, `run`, `foreground`) or default fallbacks (`isTerminal() -> RunTUI()` or standard foreground server).
4. **Flag Parsing for Subcommands**:
   - Standard Go `flag.Parse()` stops parsing flags at the first non-flag argument (`king`, `worker`, `pair`).
   - Using subcommand-specific `flag.NewFlagSet("<cmd>", flag.ExitOnError)` allows parsing flags specific to each subcommand (`dca king --port 8080 --ingress-port 8081`, `dca worker --king wss://... --pair-code ABCDEF`, `dca pair --code ABCDEF` or `dca pair ABCDEF`).
5. **Config & Service Integration**:
   - Subcommand flag parsing updates fields on the loaded `*utils.ServerConfig` struct (setting `cfg.KingMode = true` for `king` or `cfg.WorkerMode = true` for `worker`).
   - Calling `cfg.SaveToFile(configPath)` persists these settings to JSON (`config.json`).
   - Updating `main_windows.go` and foreground server execution to dispatch based on `cfg.KingMode` vs `cfg.WorkerMode` vs default standalone server ensures background services (Windows SCM or systemd) automatically run the correct mode configured in `config.json`.

## 3. Caveats
- **Global Flag Placement**: Standard Go `flag.Parse()` processes global flags (like `-config`) ONLY if placed before the subcommand (e.g. `dca -config custom.json king`). To support `-config` placed after subcommands (e.g. `dca king -config custom.json`), each subcommand `FlagSet` must also register the `config` flag.
- **Service Configuration File Paths**: Ensure `dca install` saves `ServerConfig` containing `KingMode` / `WorkerMode` to the OS default path (`/etc/mymcp/config.json` or `C:\ProgramData\mymcp\config.json`) so service managers invoke the binary with `-config` pointing to valid settings.
- **Dependencies on M1 & M2**: `king` subcommand execution requires `utils.KingGateway` (from Milestone 2), `worker` subcommand execution requires `utils.WorkerDaemon` (from Milestone 1), and `pair` subcommand execution requires `utils.ExecutePairingExchange` / `utils.PairingCodeManager`.

## 4. Conclusion
Integrating subcommands `king`, `worker`, and `pair` into `main.go` using `flag.NewFlagSet` and a unified mode dispatcher `runMode(ctx, cfg)` provides a robust, clean implementation. It preserves all existing standalone, TUI, and service management behaviors with **zero regression risks**.

## 5. Verification Method
1. **Compilation Check**:
   - Run `go build -o dca.exe .` to verify successful compilation with subcommand routing extensions.
2. **Unit Test Suite**:
   - Run `go test ./...` to verify zero test regressions across existing packages.
3. **CLI Subcommand Verification**:
   - Run `dca king --help` to verify King subcommand flags (`--port`, `--host`, `--ingress-port`, `--auth-token`).
   - Run `dca worker --help` to verify Worker subcommand flags (`--king`, `--pair-code`, `--node-id`).
   - Run `dca pair --help` to verify Pair subcommand flags (`--code`, `--king`, `--node-id`).
   - Run `dca` without args in TTY terminal to verify Bubbletea TUI launches.
   - Run `dca status` / `dca run` to verify existing subcommands function identically.
