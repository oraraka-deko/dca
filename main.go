package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"dca/utils"
)

func main() {
	configPath := flag.String("config", "", "Path to server configuration JSON file")
	flag.Parse()

	args := flag.Args()

	if len(args) > 0 {
		cmd := args[0]
		switch cmd {
		case "status":
			status, err := utils.GetServiceStatus()
			if err != nil {
				fmt.Printf("Error getting service status: %v\nOutput: %s\n", err, status)
				os.Exit(1)
			}
			fmt.Printf("Service Status:\n%s\n", status)
			return

		case "start":
			if err := utils.StartService(); err != nil {
				fmt.Printf("Failed to start service: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("MyMCP Service started successfully.")
			return

		case "stop":
			if err := utils.StopService(); err != nil {
				fmt.Printf("Failed to stop service: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("MyMCP Service stopped successfully.")
			return

		case "restart":
			if err := utils.RestartService(); err != nil {
				fmt.Printf("Failed to restart service: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("MyMCP Service restarted successfully.")
			return

		case "uninstall":
			if err := utils.UninstallService(); err != nil {
				fmt.Printf("Failed to uninstall service: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("MyMCP Service uninstalled successfully.")
			return

		case "install":
			cfg := utils.DefaultServerConfig()
			if *configPath != "" {
				loaded, err := utils.LoadServerConfig(*configPath)
				if err == nil {
					cfg = *loaded
				}
			}
			if err := utils.InstallService(cfg, *configPath); err != nil {
				fmt.Printf("Installation failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("MyMCP Service installed and started successfully.")
			return
		}
	}

	// Default run mode: Load config and launch MCP Server in foreground
	cPath := *configPath
	if cPath == "" {
		cPath = utils.GetDefaultConfigPath()
	}

	cfg, err := utils.LoadServerConfig(cPath)
	if err != nil {
		fmt.Printf("Config file not found at %s. Using default server configuration.\n", cPath)
		defaultCfg := utils.DefaultServerConfig()
		cfg = &defaultCfg
		_ = cfg.SaveToFile(cPath)
	}

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
	}
}
