package utils

import (
	"context"
	"testing"
	"time"
)

func TestSSHAuthOptionsValidation(t *testing.T) {
	mgr := NewSSHClientManager()

	// Case 1: No auth method provided
	_, err := mgr.BuildClientConfig("root", SSHAuthOptions{}, 5*time.Second)
	if err == nil {
		t.Fatalf("expected error when no auth method is provided, got nil")
	}

	// Case 2: Password auth provided
	cfg, err := mgr.BuildClientConfig("root", SSHAuthOptions{Password: "secret123"}, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error for password auth: %v", err)
	}
	if cfg.User != "root" {
		t.Errorf("expected user 'root', got '%s'", cfg.User)
	}
	if len(cfg.Auth) != 1 {
		t.Errorf("expected 1 auth method, got %d", len(cfg.Auth))
	}

	// Case 3: Invalid Key Content
	_, err = mgr.BuildClientConfig("root", SSHAuthOptions{KeyContent: "invalid-pem-data"}, 5*time.Second)
	if err == nil {
		t.Fatalf("expected error for invalid key content, got nil")
	}
}

func TestSSHManagerTestConnectionUnreachable(t *testing.T) {
	mgr := NewSSHClientManager()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Attempt connecting to unreachable address
	err := mgr.TestConnection(ctx, "127.0.0.1", 59999, "testuser", SSHAuthOptions{Password: "pass"}, 2*time.Second)
	if err == nil {
		t.Fatalf("expected connection error for closed port, got success")
	}
}
