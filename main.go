package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"dca/installer"
	"dca/utils"
	isatty "github.com/mattn/go-isatty"
)

func isTerminal() bool {
	return isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
}

// ParseCLIArgs parses global and subcommand flags, loads configuration, applies env overrides and validates.
func ParseCLIArgs(args []string, defaultConfigPath string) (*utils.ServerConfig, string, error) {
	topFlags := flag.NewFlagSet("mcp", flag.ContinueOnError)
	topConfig := topFlags.String("config", "", "Path to server configuration JSON file")

	if err := topFlags.Parse(args); err != nil {
		return nil, "", err
	}

	cPath := *topConfig
	if cPath == "" {
		cPath = defaultConfigPath
	}

	var cfg *utils.ServerConfig
	if cPath != "" {
		if loaded, err := utils.LoadServerConfig(cPath); err == nil {
			cfg = loaded
		}
	}
	if cfg == nil {
		defaultCfg := utils.DefaultServerConfig()
		cfg = &defaultCfg
	}

	remaining := topFlags.Args()
	if len(remaining) == 0 {
		cfg.ApplyEnvOverrides()
		if err := cfg.Validate(); err != nil {
			return nil, "", err
		}
		return cfg, "default", nil
	}

	subcmd := remaining[0]
	subArgs := remaining[1:]

	switch subcmd {
	case "status":
		return cfg, "status", nil
	case "start":
		return cfg, "start", nil
	case "stop":
		return cfg, "stop", nil
	case "restart":
		return cfg, "restart", nil
	case "uninstall":
		return cfg, "uninstall", nil
	case "run", "foreground":
		return cfg, "run", nil
	case "install":
		return cfg, "install", nil

	case "king":
		kingCmd := flag.NewFlagSet("king", flag.ContinueOnError)
		kingPort := kingCmd.Int("port", 0, "Server HTTP port")
		kingIngressPort := kingCmd.Int("ingress-port", 0, "King ingress port for workers")
		kingAuthToken := kingCmd.String("auth-token", "", "Authentication token")
		kingConfig := kingCmd.String("config", "", "Path to server configuration JSON file")

		if err := kingCmd.Parse(subArgs); err != nil {
			return nil, "", err
		}

		if *kingConfig != "" {
			if loaded, err := utils.LoadServerConfig(*kingConfig); err == nil {
				cfg = loaded
			}
		}

		cfg.KingMode = true
		if *kingPort != 0 {
			cfg.Port = *kingPort
		}
		if *kingIngressPort != 0 {
			cfg.IngressPort = *kingIngressPort
		}
		if *kingAuthToken != "" {
			cfg.AuthToken = *kingAuthToken
		}

		cfg.ApplyEnvOverrides()
		if err := cfg.Validate(); err != nil {
			return nil, "", err
		}
		return cfg, "king", nil

	case "worker":
		workerCmd := flag.NewFlagSet("worker", flag.ContinueOnError)
		workerKing := workerCmd.String("king", "", "King server address")
		workerPairCode := workerCmd.String("pair-code", "", "Pairing code")
		workerNodeID := workerCmd.String("node-id", "", "Worker Node ID")
		workerConfig := workerCmd.String("config", "", "Path to server configuration JSON file")

		if err := workerCmd.Parse(subArgs); err != nil {
			return nil, "", err
		}

		if *workerConfig != "" {
			if loaded, err := utils.LoadServerConfig(*workerConfig); err == nil {
				cfg = loaded
			}
		}

		cfg.WorkerMode = true
		if *workerKing != "" {
			cfg.KingAddress = *workerKing
		}
		if *workerPairCode != "" {
			cfg.PairCode = *workerPairCode
		}
		if *workerNodeID != "" {
			cfg.NodeID = *workerNodeID
		}

		cfg.ApplyEnvOverrides()
		if err := cfg.Validate(); err != nil {
			return nil, "", err
		}
		return cfg, "worker", nil

	case "pair":
		pairCmd := flag.NewFlagSet("pair", flag.ContinueOnError)
		pairCodeFlag := pairCmd.String("code", "", "Pairing code")
		pairKing := pairCmd.String("king", "", "King server address")
		pairNodeID := pairCmd.String("node-id", "", "Worker Node ID")
		pairConfig := pairCmd.String("config", "", "Path to server configuration JSON file")

		if err := pairCmd.Parse(subArgs); err != nil {
			return nil, "", err
		}

		code := *pairCodeFlag
		if code == "" && len(pairCmd.Args()) > 0 {
			code = pairCmd.Args()[0]
		}

		if code != "" && !utils.ValidatePairingCode(code) {
			return nil, "", fmt.Errorf("invalid pairing code format: %s", code)
		}

		if *pairConfig != "" {
			if loaded, err := utils.LoadServerConfig(*pairConfig); err == nil {
				cfg = loaded
			}
		}

		cfg.WorkerMode = true
		if code != "" {
			cfg.PairCode = code
		}
		if *pairKing != "" {
			cfg.KingAddress = *pairKing
		}
		if *pairNodeID != "" {
			cfg.NodeID = *pairNodeID
		}

		cfg.ApplyEnvOverrides()
		if err := cfg.Validate(); err != nil {
			return nil, "", err
		}
		return cfg, "pair", nil

	default:
		return nil, "", fmt.Errorf("unknown subcommand: %s", subcmd)
	}
}

func main() {
	defaultConfigPath := installer.GetDefaultConfigPath()
	cfg, action, err := ParseCLIArgs(os.Args[1:], defaultConfigPath)
	if err != nil {
		fmt.Printf("CLI Error: %v\n", err)
		os.Exit(1)
	}

	// 2. If running as Windows Service, run service handler and return
	if cfg != nil {
		isSvc, err := runAsService(*cfg)
		if err != nil {
			fmt.Printf("Error running service: %v\n", err)
			os.Exit(1)
		}
		if isSvc {
			return
		}
	}

	// 3. Process CLI subcommands and actions
	switch action {
	case "status":
		status, err := installer.GetServiceStatus()
		if err != nil {
			fmt.Printf("Error getting service status: %v\nOutput: %s\n", err, status)
			os.Exit(1)
		}
		fmt.Printf("Service Status:\n%s\n", status)
		return

	case "start":
		if err := installer.StartService(); err != nil {
			fmt.Printf("Failed to start service: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("MyMCP Service started successfully.")
		return

	case "stop":
		if err := installer.StopService(); err != nil {
			fmt.Printf("Failed to stop service: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("MyMCP Service stopped successfully.")
		return

	case "restart":
		if err := installer.RestartService(); err != nil {
			fmt.Printf("Failed to restart service: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("MyMCP Service restarted successfully.")
		return

	case "uninstall":
		if err := installer.UninstallService(); err != nil {
			fmt.Printf("Failed to uninstall service: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("MyMCP Service uninstalled successfully.")
		return

	case "install":
		if isTerminal() {
			if err := installer.RunTUI(); err != nil {
				fmt.Printf("TUI error: %v\n", err)
				os.Exit(1)
			}
			return
		}

		if err := installer.InstallService(*cfg, ""); err != nil {
			fmt.Printf("Installation failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("MyMCP Service installed and started successfully.")
		return

	case "default":
		if isTerminal() {
			if err := installer.RunTUI(); err != nil {
				fmt.Printf("TUI error: %v\n", err)
				os.Exit(1)
			}
			return
		}

	case "king":
		fmt.Printf("Starting MyMCP Server in King Mode (Port: %d, Ingress Port: %d)...\n",
			cfg.Port, cfg.IngressPort)

	case "worker":
		fmt.Printf("Starting MyMCP Server in Worker Mode (King: %s, Node ID: %s)...\n",
			cfg.KingAddress, cfg.NodeID)

	case "pair":
		fmt.Printf("Pairing worker (Node ID: %s) with King at %s using code %s...\n",
			cfg.NodeID, cfg.KingAddress, utils.FormatPairingCode(cfg.PairCode))

	case "run":
		// Fallthrough to server runtime
	}

	// 4. Default/Foreground execution: Run MCP server
	fmt.Printf("Starting MyMCP Server on %s://%s:%d (AuthMode: %s, BasePath: %s)...\n",
		cfg.Protocol, cfg.Host, cfg.Port, cfg.AuthMode, cfg.CustomBasePath)

	serverWrapper := utils.NewMCPServerWrapper(*cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\nShutting down MyMCP Server...")
		_ = serverWrapper.StopServer(ctx)
		cancel()
	}()

	if err := serverWrapper.StartServer(ctx); err != nil {
		fmt.Printf("Server runtime error: %v\n", err)
		os.Exit(1)
	}

	// Block main thread until context is cancelled (SIGINT/SIGTERM)
	<-ctx.Done()
}
