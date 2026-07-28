package main

import (
	"path/filepath"
	"testing"

	"dca/utils"
)

func TestParseCLIArgs_King(t *testing.T) {
	args := []string{"king", "--port", "8888", "--ingress-port", "9999", "--auth-token", "kingtok"}
	cfg, action, err := ParseCLIArgs(args, "")
	if err != nil {
		t.Fatalf("ParseCLIArgs failed: %v", err)
	}

	if action != "king" {
		t.Errorf("action = %s; want king", action)
	}
	if !cfg.KingMode {
		t.Errorf("KingMode = false; want true")
	}
	if cfg.Port != 8888 {
		t.Errorf("Port = %d; want 8888", cfg.Port)
	}
	if cfg.IngressPort != 9999 {
		t.Errorf("IngressPort = %d; want 9999", cfg.IngressPort)
	}
	if cfg.AuthToken != "kingtok" {
		t.Errorf("AuthToken = %s; want kingtok", cfg.AuthToken)
	}
}

func TestParseCLIArgs_Worker(t *testing.T) {
	args := []string{"worker", "--king", "http://10.0.0.1:8080", "--pair-code", "DEF456", "--node-id", "node-w1"}
	cfg, action, err := ParseCLIArgs(args, "")
	if err != nil {
		t.Fatalf("ParseCLIArgs failed: %v", err)
	}

	if action != "worker" {
		t.Errorf("action = %s; want worker", action)
	}
	if !cfg.WorkerMode {
		t.Errorf("WorkerMode = false; want true")
	}
	if cfg.KingAddress != "http://10.0.0.1:8080" {
		t.Errorf("KingAddress = %s; want http://10.0.0.1:8080", cfg.KingAddress)
	}
	if cfg.PairCode != "DEF456" {
		t.Errorf("PairCode = %s; want DEF456", cfg.PairCode)
	}
	if cfg.NodeID != "node-w1" {
		t.Errorf("NodeID = %s; want node-w1", cfg.NodeID)
	}
}

func TestParseCLIArgs_Pair_Positional(t *testing.T) {
	args := []string{"pair", "123456", "--king", "http://10.0.0.1:8080", "--node-id", "node-w2"}
	cfg, action, err := ParseCLIArgs(args, "")
	if err != nil {
		t.Fatalf("ParseCLIArgs failed: %v", err)
	}

	if action != "pair" {
		t.Errorf("action = %s; want pair", action)
	}
	if !cfg.WorkerMode {
		t.Errorf("WorkerMode = false; want true")
	}
	if cfg.PairCode != "123456" {
		t.Errorf("PairCode = %s; want 123456", cfg.PairCode)
	}
	if cfg.KingAddress != "http://10.0.0.1:8080" {
		t.Errorf("KingAddress = %s; want http://10.0.0.1:8080", cfg.KingAddress)
	}
	if cfg.NodeID != "node-w2" {
		t.Errorf("NodeID = %s; want node-w2", cfg.NodeID)
	}
}

func TestParseCLIArgs_Pair_Flag(t *testing.T) {
	args := []string{"pair", "--code", "ABCDEF", "--king", "http://10.0.0.1:8080"}
	cfg, action, err := ParseCLIArgs(args, "")
	if err != nil {
		t.Fatalf("ParseCLIArgs failed: %v", err)
	}

	if action != "pair" {
		t.Errorf("action = %s; want pair", action)
	}
	if !cfg.WorkerMode {
		t.Errorf("WorkerMode = false; want true")
	}
	if cfg.PairCode != "ABCDEF" {
		t.Errorf("PairCode = %s; want ABCDEF", cfg.PairCode)
	}
}

func TestParseCLIArgs_Pair_InvalidCode(t *testing.T) {
	args := []string{"pair", "INVALID_PAIR_CODE!!!"}
	_, _, err := ParseCLIArgs(args, "")
	if err == nil {
		t.Errorf("expected error for invalid pair code")
	}
}

func TestParseCLIArgs_ExistingCommands(t *testing.T) {
	testCases := []struct {
		cmd    string
		action string
	}{
		{"status", "status"},
		{"start", "start"},
		{"stop", "stop"},
		{"restart", "restart"},
		{"uninstall", "uninstall"},
		{"run", "run"},
		{"foreground", "run"},
		{"install", "install"},
	}

	for _, tc := range testCases {
		_, action, err := ParseCLIArgs([]string{tc.cmd}, "")
		if err != nil {
			t.Errorf("ParseCLIArgs(%s) error: %v", tc.cmd, err)
		}
		if action != tc.action {
			t.Errorf("ParseCLIArgs(%s) action = %s; want %s", tc.cmd, action, tc.action)
		}
	}
}

func TestParseCLIArgs_ConfigFilePrecedence(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "test_config.json")

	fileCfg := utils.DefaultServerConfig()
	fileCfg.Port = 7777
	fileCfg.KingAddress = "http://file-king:8080"
	if err := fileCfg.SaveToFile(cfgPath); err != nil {
		t.Fatalf("SaveToFile failed: %v", err)
	}

	// Environment variable
	t.Setenv("DCA_KING_ADDRESS", "http://env-king:8080")

	// CLI flag overrides both config file and env var
	args := []string{"worker", "--config", cfgPath, "--king", "http://cli-king:8080"}
	cfg, action, err := ParseCLIArgs(args, "")
	if err != nil {
		t.Fatalf("ParseCLIArgs failed: %v", err)
	}

	if action != "worker" {
		t.Errorf("action = %s; want worker", action)
	}
	if cfg.Port != 7777 {
		t.Errorf("Port = %d; want 7777 (from config file)", cfg.Port)
	}
	if cfg.KingAddress != "http://cli-king:8080" {
		t.Errorf("KingAddress = %s; want http://cli-king:8080 (CLI flag precedence)", cfg.KingAddress)
	}
}
