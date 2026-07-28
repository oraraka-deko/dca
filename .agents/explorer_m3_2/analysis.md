# Detailed Analysis Report: CLI Subcommand Routing, Flag Parsing, and Config Integration

## Executive Summary
This analysis details the architectural design and implementation plan for integrating the new `king`, `worker`, and `pair` subcommands into `main.go` and `utils/server_config.go` for **Milestone 3 (Milestone R3)**. The primary objective is to enable King Control Plane mode, Worker Daemon mode, and device pairing CLI workflows while maintaining **100% backward compatibility** for all existing execution paths (interactive TUI, standalone foreground server, and OS background services).

---

## 1. Existing `main.go` Architecture & CLI Parsing Flow

### 1.1 Current Code Structure
In `main.go`, CLI argument evaluation follows a strict 5-stage sequential lifecycle:

```
               [ 1. flag.Parse() ]
                        │
                        ▼
         [ 2. Load Config (config.json) ]
                        │
                        ▼
         [ 3. Windows Service Check ] ── (Is Windows SCM Service?) ──> [ Execute mymcpService & return ]
                        │ (No)
                        ▼
           [ 4. Process Subcommand ]
         /           │            \
  (status/start/   (install)   (run / foreground)
  stop/uninstall)    │            │
         │         [Terminal?]  [Fallthrough to foreground]
         ▼          ├─ Yes ─> TUI
      Execute       └─ No  ─> Service Install
      Action & Exit     
                        │
                        ▼ (args == 0)
            [ 5. Default Fallback ]
            ├─ Terminal ────> Run TUI
            └─ Non-Terminal ─> Run Foreground Server
```

### 1.2 Step-by-Step Execution Analysis
1. **Global Flag Parsing (`flag.Parse()`)**:
   - `configPath := flag.String("config", "", "Path to server configuration JSON file")`
   - Parses flags matching `-config` / `--config` from `os.Args[1:]`.
   - In standard Go `flag` package, flag parsing stops at the first non-flag positional argument encountered (e.g., `king`, `status`, `worker`).

2. **Config File Loading (`LoadServerConfig`)**:
   - Resolves `cPath`. If `*configPath == ""`, resolves OS-specific default via `installer.GetDefaultConfigPath()` (`C:\ProgramData\mymcp\config.json` on Windows, `/etc/mymcp/config.json` on Linux).
   - Loads JSON configuration. If file does not exist or fails unmarshaling, falls back to `utils.DefaultServerConfig()`.

3. **Service Runtime Check (`runAsService`)**:
   - On Windows (`main_windows.go`), calls `svc.IsWindowsService()`. If running inside Service Control Manager (SCM), executes `svc.Run("mymcp", &mymcpService{cfg: cfg})` and returns immediately.
   - On Linux (`main_unix.go`), returns `(false, nil)` because systemd launches binaries as standard foreground processes.

4. **CLI Argument Switch (`flag.Args()`)**:
   - Reads positional arguments remaining after standard flags.
   - If `len(args) > 0`, checks `args[0]`:
     - `"status"`: calls `installer.GetServiceStatus()`, prints output, exits.
     - `"start"`: calls `installer.StartService()`, prints result, exits.
     - `"stop"`: calls `installer.StopService()`, prints result, exits.
     - `"restart"`: calls `installer.RestartService()`, prints result, exits.
     - `"uninstall"`: calls `installer.UninstallService()`, prints result, exits.
     - `"run"`, `"foreground"`: explicitly breaks out of switch statement to fall through to foreground server execution.
     - `"install"`: checks `isTerminal()`. If true, launches interactive Bubbletea TUI (`installer.RunTUI()`). If false (scripted/piped), executes `installer.InstallService(*cfg, *configPath)`.

5. **Default Fallback Behavior**:
   - If `len(args) == 0`:
     - If stdout is a TTY terminal (`isTerminal() == true`), launches interactive TUI (`installer.RunTUI()`).
     - If stdout is not a TTY (piped/redirected), falls through to standard foreground server execution.

---

## 2. Integrating Subcommands `king`, `worker`, and `pair`

To introduce `king`, `worker`, and `pair` without breaking existing workflows or default behaviors, we must handle subcommand routing using isolated `flag.FlagSet` instances per subcommand.

### 2.1 Subcommand Routing Design
In `main.go`, we extend the `switch cmd` block within `if len(args) > 0`:

```go
switch cmd {
case "status":
    // Preserved existing logic...
case "start":
    // Preserved existing logic...
case "stop":
    // Preserved existing logic...
case "restart":
    // Preserved existing logic...
case "uninstall":
    // Preserved existing logic...
case "install":
    // Preserved existing logic...
case "run", "foreground":
    // Preserved existing logic...

case "king":
    return runKingCmd(args[1:], cfg, cPath)

case "worker":
    return runWorkerCmd(args[1:], cfg, cPath)

case "pair":
    return runPairCmd(args[1:], cfg, cPath)
}
```

### 2.2 Preserving Backward Compatibility Matrix
| Invocation Pattern | Standard Behavior | Preserved? | Rationale |
|---|---|---|---|
| `dca` (TTY interactive) | Launches Bubbletea TUI | ✅ Yes | `len(args) == 0 && isTerminal()` condition untouched |
| `dca` (Non-TTY / piped) | Runs standalone server foreground | ✅ Yes | Fallthrough after TUI check untouched |
| `dca run` / `dca foreground` | Explicit standalone server foreground | ✅ Yes | Switch break logic untouched |
| `dca start` / `stop` / `restart` / `status` / `uninstall` | Service lifecycle operations | ✅ Yes | Handled identically in `switch` |
| `dca install` (TTY vs Non-TTY) | Interactive TUI wizard or auto-install | ✅ Yes | TTY check in `case "install"` untouched |
| `sc.exe start mymcp` (Windows SCM) | SCM Service Dispatcher | ✅ Yes | `runAsService(*cfg)` check happens before `flag.Args()` |
| `dca king [flags]` | **NEW**: King Control Plane Gateway | ✅ Yes | New distinct `case "king"` |
| `dca worker [flags]` | **NEW**: Worker Reverse WSS Daemon | ✅ Yes | New distinct `case "worker"` |
| `dca pair [flags]` | **NEW**: Pair Code Exchange CLI | ✅ Yes | New distinct `case "pair"` |

---

## 3. Flag Specifications for New Subcommands

Each new subcommand uses `flag.NewFlagSet("<subcommand>", flag.ExitOnError)` to parse its own specific command-line arguments.

### 3.1 `dca king` Subcommand Flags
```go
func runKingCmd(subArgs []string, cfg *utils.ServerConfig, configPath string) {
    kingCmd := flag.NewFlagSet("king", flag.ExitOnError)
    
    port := kingCmd.Int("port", cfg.Port, "King HTTP/WSS listening port (default: 8080)")
    host := kingCmd.String("host", cfg.Host, "King host interface binding (default: 0.0.0.0)")
    ingressPort := kingCmd.Int("ingress-port", cfg.IngressPort, "King router ingress port")
    authToken := kingCmd.String("auth-token", cfg.AuthToken, "King authorization token for ingress API")
    subConfigPath := kingCmd.String("config", configPath, "Path to server configuration JSON file")
    
    _ = kingCmd.Parse(subArgs)

    // Override config with explicitly set CLI flags
    kingCmd.Visit(func(f *flag.Flag) {
        switch f.Name {
        case "port":
            cfg.Port = *port
        case "host":
            cfg.Host = *host
        case "ingress-port":
            cfg.IngressPort = *ingressPort
        case "auth-token":
            cfg.AuthToken = *authToken
        }
    })

    // Enable King Gateway Mode
    cfg.KingMode = true
    cfg.WorkerMode = false

    // Save updated config if config path specified/resolved
    if *subConfigPath != "" {
        _ = cfg.SaveToFile(*subConfigPath)
    }

    // Run King Server Loop
    runGatewayMode(cfg)
}
```

### 3.2 `dca worker` Subcommand Flags
```go
func runWorkerCmd(subArgs []string, cfg *utils.ServerConfig, configPath string) {
    workerCmd := flag.NewFlagSet("worker", flag.ExitOnError)
    
    kingURL := workerCmd.String("king", cfg.KingAddress, "King control plane WebSocket URL (e.g. wss://king:8080)")
    pairCode := workerCmd.String("pair-code", cfg.PairCode, "6-character pairing code for registration")
    nodeID := workerCmd.String("node-id", cfg.NodeID, "Unique node identifier for this worker")
    subConfigPath := workerCmd.String("config", configPath, "Path to server configuration JSON file")
    
    _ = workerCmd.Parse(subArgs)

    workerCmd.Visit(func(f *flag.Flag) {
        switch f.Name {
        case "king":
            cfg.KingAddress = *kingURL
        case "pair-code":
            cfg.PairCode = *pairCode
        case "node-id":
            cfg.NodeID = *nodeID
        }
    })

    // Enable Worker Daemon Mode
    cfg.WorkerMode = true
    cfg.KingMode = false

    if *subConfigPath != "" {
        _ = cfg.SaveToFile(*subConfigPath)
    }

    // Run Worker Daemon Loop
    runWorkerMode(cfg)
}
```

### 3.3 `dca pair` Subcommand Flags & Positional Argument Support
`dca pair` supports both flag syntax (`dca pair --code ABC-DEF --king wss://...`) and positional syntax (`dca pair ABC-DEF`):

```go
func runPairCmd(subArgs []string, cfg *utils.ServerConfig, configPath string) {
    pairCmd := flag.NewFlagSet("pair", flag.ExitOnError)
    
    codeFlag := pairCmd.String("code", "", "6-character pairing code")
    kingFlag := pairCmd.String("king", cfg.KingAddress, "King control plane URL")
    nodeIDFlag := pairCmd.String("node-id", cfg.NodeID, "Node identifier for registration")
    
    _ = pairCmd.Parse(subArgs)

    // Handle positional pairing code fallback: dca pair ABCDEF
    pairCode := *codeFlag
    if pairCode == "" && len(pairCmd.Args()) > 0 {
        pairCode = pairCmd.Args()[0]
    }

    if pairCode == "" {
        fmt.Println("Error: pairing code is required (e.g., dca pair ABCDEF or dca pair --code ABCDEF)")
        os.Exit(1)
    }

    kingURL := *kingFlag
    if kingURL == "" {
        kingURL = cfg.KingAddress
    }
    if kingURL == "" {
        fmt.Println("Error: king URL is required (e.g., dca pair --king wss://king.domain.com:8080)")
        os.Exit(1)
    }

    nodeID := *nodeIDFlag
    if nodeID == "" {
        nodeID = cfg.NodeID
    }

    fmt.Printf("Pairing worker node '%s' with King at %s using code '%s'...\n", nodeID, kingURL, utils.FormatPairingCode(pairCode))

    // Perform Pair Registration Protocol Exchange
    pairToken, err := utils.ExecutePairingExchange(kingURL, nodeID, pairCode)
    if err != nil {
        fmt.Printf("Pairing failed: %v\n", err)
        os.Exit(1)
    }

    // Update config with persistent credentials
    cfg.KingAddress = kingURL
    cfg.PairCode = pairCode
    cfg.PairToken = pairToken
    cfg.NodeID = nodeID
    cfg.WorkerMode = true

    if configPath != "" {
        if err := cfg.SaveToFile(configPath); err != nil {
            fmt.Printf("Warning: failed to update config file: %v\n", err)
        }
    }

    fmt.Println("Pairing successful! Credentials stored in configuration.")
}
```

---

## 4. Connecting Subcommand Execution to `ServerConfig` and Background Services

### 4.1 Config Override & Serialization Model
1. **Config Hierarchy**:
   `DefaultServerConfig()` -> loaded from JSON (`config.json`) -> overridden by CLI Subcommand Flags -> written back to `config.json` via `cfg.SaveToFile()`.
2. **Persistence for Background Services**:
   When `dca king` or `dca worker` is executed or installed as a service via `dca install`, setting `cfg.KingMode = true` or `cfg.WorkerMode = true` and saving to `config.json` ensures that when OS services launch:
   - On Windows: `sc.exe start mymcp` executes `mymcp.exe -config C:\ProgramData\mymcp\config.json`. `runAsService(*cfg)` reads `cfg.KingMode` / `cfg.WorkerMode` from disk and starts the corresponding mode automatically.
   - On Linux: `systemctl start mymcp` executes `/usr/local/bin/mymcp -config /etc/mymcp/config.json`. `main.go` reads `cfg.KingMode` / `cfg.WorkerMode` from disk and enters the corresponding mode.

### 4.2 Unified Runtime Mode Dispatcher in `main.go`
We implement a clean, unified mode dispatcher function `runMode(ctx context.Context, cfg utils.ServerConfig)` used by both foreground CLI execution and Windows Service execution:

```go
func runMode(ctx context.Context, cfg utils.ServerConfig) error {
    if cfg.KingMode {
        fmt.Printf("Starting MyMCP King Gateway on %s://%s:%d (IngressPort: %d)...\n",
            cfg.Protocol, cfg.Host, cfg.Port, cfg.IngressPort)
        gateway := utils.NewKingGateway(cfg)
        return gateway.Start(ctx)
    } else if cfg.WorkerMode {
        fmt.Printf("Starting MyMCP Worker Daemon connecting to %s (NodeID: %s)...\n",
            cfg.KingAddress, cfg.NodeID)
        wrapper := utils.NewMCPServerWrapper(cfg)
        daemon := utils.NewWorkerDaemon(cfg, wrapper)
        return daemon.Start(ctx)
    } else {
        fmt.Printf("Starting MyMCP Server on %s://%s:%d (AuthMode: %s, BasePath: %s)...\n",
            cfg.Protocol, cfg.Host, cfg.Port, cfg.AuthMode, cfg.CustomBasePath)
        wrapper := utils.NewMCPServerWrapper(cfg)
        return wrapper.StartServer(ctx)
    }
}
```

### 4.3 Updating `main_windows.go` Service Execution
In `main_windows.go`, `mymcpService.Execute` calls `runMode(ctx, m.cfg)` instead of directly calling `MCPServerWrapper.StartServer(ctx)`:

```go
func (m *mymcpService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
    const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
    changes <- svc.Status{State: svc.StartPending}

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    errChan := make(chan error, 1)
    go func() {
        err := runMode(ctx, m.cfg)
        if err != nil {
            errChan <- err
            return
        }
        <-ctx.Done()
        errChan <- nil
    }()

    changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}
    ...
```

---

## 5. Verification & Test Strategy

### 5.1 Unit Tests for CLI Routing & Flag Parsing
Create `main_test.go` or `utils/server_config_test.go` CLI tests:
1. `TestCLI_KingSubcommandParsing`: Verify `dca king --port 9000 --ingress-port 9001` sets `cfg.Port = 9000`, `cfg.IngressPort = 9001`, and `cfg.KingMode = true`.
2. `TestCLI_WorkerSubcommandParsing`: Verify `dca worker --king wss://king.example.com --pair-code 123456` sets `cfg.KingAddress`, `cfg.PairCode`, and `cfg.WorkerMode = true`.
3. `TestCLI_PairSubcommandParsing`: Verify `dca pair ABC-DEF` correctly parses positional code and formats it as `ABC-DEF` / `ABCDEF`.
4. `TestCLI_BackwardCompatibility`: Verify running `dca` without args in non-interactive mode defaults to standard standalone MCP server mode without setting `KingMode` or `WorkerMode`.

### 5.2 Verification Commands
- `go build -o dca.exe .`
- `go test ./...`
- Manual execution check:
  - `./dca king --port 8085` -> prints "Starting MyMCP King Gateway on http://0.0.0.0:8085..."
  - `./dca worker --king ws://localhost:8085` -> prints "Starting MyMCP Worker Daemon..."
