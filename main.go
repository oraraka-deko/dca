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

func main() {
	configPath := flag.String("config", "", "Path to server configuration JSON file")
	flag.Parse()

	// 1. Resolve configuration path and load config
	cPath := *configPath
	if cPath == "" {
		cPath = installer.GetDefaultConfigPath()
	}

	cfg, err := utils.LoadServerConfig(cPath)
	if err != nil {
		defaultCfg := utils.DefaultServerConfig()
		cfg = &defaultCfg
	}

	// 2. If running as Windows Service, run service handler and return
	isSvc, err := runAsService(*cfg)
	if err != nil {
		fmt.Printf("Error running service: %v\n", err)
		os.Exit(1)
	}
	if isSvc {
		return
	}

	// 3. Process CLI arguments
	args := flag.Args()
	if len(args) > 0 {
		cmd := args[0]
		switch cmd {
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

			// Non-interactive fallback
			if err := installer.InstallService(*cfg, *configPath); err != nil {
				fmt.Printf("Installation failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("MyMCP Service installed and started successfully.")
			return
		}
	}

	// 4. Default mode: If no arguments and stdout is a terminal, run the interactive TUI
	if len(args) == 0 && isTerminal() {
		if err := installer.RunTUI(); err != nil {
			fmt.Printf("TUI error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// 5. Otherwise: Run in foreground (e.g. systemd service or piped run)
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
