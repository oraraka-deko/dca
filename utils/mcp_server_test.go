package utils

import (
	"context"
	"testing"
)

func TestMCPServerWrapper_ToolRegistration(t *testing.T) {
	cfg := DefaultServerConfig()
	wrapper := NewMCPServerWrapper(cfg)
	defer wrapper.StopServer(context.Background())

	if wrapper.MCPServer == nil {
		t.Fatalf("MCPServer instance should not be nil")
	}
}

func TestMCPServerWrapper_Components(t *testing.T) {
	cfg := DefaultServerConfig()
	wrapper := NewMCPServerWrapper(cfg)
	defer wrapper.StopServer(context.Background())

	// 1. TaskManager integration
	task, err := wrapper.TaskManager.SubmitCommand("test_echo", "echo", "mcp_hello")
	if err != nil {
		t.Fatalf("SubmitCommand failed: %v", err)
	}
	if task.ID == "" {
		t.Fatalf("expected valid task ID")
	}

	// 2. SandboxManager integration
	sb, err := wrapper.SandboxManager.CreateMemSandbox("test_mcp_sb")
	if err != nil {
		t.Fatalf("CreateMemSandbox failed: %v", err)
	}

	err = sb.WriteFile("mcp_test.txt", []byte("mcp sandbox data"), 0644)
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	data, err := sb.ReadFile("mcp_test.txt")
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(data) != "mcp sandbox data" {
		t.Fatalf("unexpected sandbox data: %s", string(data))
	}

	// 3. System status integration
	status := GetStatusInfoJSON()
	if status.OS == "" && status.Hostname == "" {
		t.Fatalf("expected valid system status")
	}
}

func TestMCPServerWrapper_AuthServerConfig(t *testing.T) {
	cfg := DefaultServerConfig()
	cfg.AuthMode = AuthModeCustomPath
	cfg.CustomBasePath = "/secret-mcp-path"

	wrapper := NewMCPServerWrapper(cfg)
	defer wrapper.StopServer(context.Background())

	if wrapper.Cfg.CustomBasePath != "/secret-mcp-path" {
		t.Fatalf("unexpected custom base path: %s", wrapper.Cfg.CustomBasePath)
	}
}
