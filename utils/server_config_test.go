package utils

import (
	"crypto/tls"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestServerConfig_SaveAndLoad(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")

	orig := DefaultServerConfig()
	orig.Port = 9090
	orig.Protocol = "https"
	orig.CertType = CertTypeSelfSigned
	orig.AuthMode = AuthModeCustomPathIP
	orig.CustomBasePath = "/secret-mcp-path"
	orig.AllowedIPs = []string{"192.168.1.100", "10.0.0.0/24"}

	err := orig.SaveToFile(cfgPath)
	if err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	loaded, err := LoadServerConfig(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if loaded.Port != 9090 || loaded.Protocol != "https" || loaded.AuthMode != AuthModeCustomPathIP {
		t.Fatalf("loaded config mismatch: %+v", loaded)
	}
	if loaded.CustomBasePath != "/secret-mcp-path" || len(loaded.AllowedIPs) != 2 {
		t.Fatalf("loaded config fields mismatch: %+v", loaded)
	}
}

func TestServerConfig_AuthMiddleware(t *testing.T) {
	// 1. Open Mode
	cfgOpen := DefaultServerConfig()
	cfgOpen.AuthMode = AuthModeOpen
	req1 := httptest.NewRequest("GET", "http://example.com/mcp", nil)
	req1.RemoteAddr = "203.0.113.5:12345"
	if !cfgOpen.ValidateAuthRequest(req1) {
		t.Fatalf("expected Open mode request to pass")
	}

	// 2. Custom Path Mode
	cfgPath := DefaultServerConfig()
	cfgPath.AuthMode = AuthModeCustomPath
	cfgPath.CustomBasePath = "/my-secret-token-123/mcp"

	reqValidPath := httptest.NewRequest("POST", "http://example.com/my-secret-token-123/mcp", nil)
	if !cfgPath.ValidateAuthRequest(reqValidPath) {
		t.Fatalf("expected valid custom path request to pass")
	}

	reqInvalidPath := httptest.NewRequest("POST", "http://example.com/mcp", nil)
	if cfgPath.ValidateAuthRequest(reqInvalidPath) {
		t.Fatalf("expected invalid custom path request to fail")
	}

	// 3. Custom Path + IP Mode
	cfgPathIP := DefaultServerConfig()
	cfgPathIP.AuthMode = AuthModeCustomPathIP
	cfgPathIP.CustomBasePath = "/secret-mcp"
	cfgPathIP.AllowedIPs = []string{"198.51.100.42"}

	reqValidBoth := httptest.NewRequest("POST", "http://example.com/secret-mcp", nil)
	reqValidBoth.RemoteAddr = "198.51.100.42:54321"
	if !cfgPathIP.ValidateAuthRequest(reqValidBoth) {
		t.Fatalf("expected valid path + IP to pass")
	}

	reqBadIP := httptest.NewRequest("POST", "http://example.com/secret-mcp", nil)
	reqBadIP.RemoteAddr = "203.0.113.10:54321"
	if cfgPathIP.ValidateAuthRequest(reqBadIP) {
		t.Fatalf("expected bad IP to fail")
	}

	// 4. IP Only Mode
	cfgIP := DefaultServerConfig()
	cfgIP.AuthMode = AuthModeIPOnly
	cfgIP.AllowedIPs = []string{"10.0.0.0/16"}

	reqSubnet := httptest.NewRequest("GET", "http://example.com/mcp", nil)
	reqSubnet.RemoteAddr = "10.0.5.12:1122"
	if !cfgIP.ValidateAuthRequest(reqSubnet) {
		t.Fatalf("expected CIDR matched IP to pass")
	}
}

func TestCertManager_GenerateSelfSigned(t *testing.T) {
	tempDir := t.TempDir()
	certPath := filepath.Join(tempDir, "cert.pem")
	keyPath := filepath.Join(tempDir, "key.pem")

	err := GenerateSelfSignedCert("mydomain.local", certPath, keyPath)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	// Load TLS KeyPair to verify validity
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("failed loading generated TLS KeyPair: %v", err)
	}

	if len(cert.Certificate) == 0 {
		t.Fatalf("expected non-empty certificate slice")
	}
}

func TestServerConfig_KingWorkerFields_SaveAndLoad(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "king_config.json")

	orig := DefaultServerConfig()
	orig.KingMode = true
	orig.KingAddress = "http://127.0.0.1:8080"
	orig.PairCode = "ABC123"
	orig.PairToken = "tok-999"
	orig.NodeID = "node-alpha"
	orig.IngressPort = 9090

	if err := orig.Validate(); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}

	err := orig.SaveToFile(cfgPath)
	if err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	loaded, err := LoadServerConfig(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if !loaded.KingMode || loaded.KingAddress != "http://127.0.0.1:8080" || loaded.PairCode != "ABC123" {
		t.Fatalf("loaded King config mismatch: %+v", loaded)
	}
	if loaded.PairToken != "tok-999" || loaded.NodeID != "node-alpha" || loaded.IngressPort != 9090 {
		t.Fatalf("loaded King config subfields mismatch: %+v", loaded)
	}
}

func TestServerConfig_Validate(t *testing.T) {
	// Dual mode invalid
	cfgDual := DefaultServerConfig()
	cfgDual.KingMode = true
	cfgDual.WorkerMode = true
	if err := cfgDual.Validate(); err == nil {
		t.Errorf("expected error for simultaneous KingMode and WorkerMode")
	}

	// KingMode IngressPort default
	cfgKing := DefaultServerConfig()
	cfgKing.KingMode = true
	cfgKing.IngressPort = 0
	if err := cfgKing.Validate(); err != nil {
		t.Fatalf("unexpected error validating KingMode: %v", err)
	}
	if cfgKing.IngressPort != 9090 {
		t.Errorf("expected IngressPort default 9090, got %d", cfgKing.IngressPort)
	}

	// KingMode IngressPort collision with Port
	cfgCollide := DefaultServerConfig()
	cfgCollide.Port = 9090
	cfgCollide.KingMode = true
	cfgCollide.IngressPort = 9090
	if err := cfgCollide.Validate(); err == nil {
		t.Errorf("expected error when IngressPort equals Port")
	}

	// Invalid port
	cfgBadPort := DefaultServerConfig()
	cfgBadPort.Port = 70000
	if err := cfgBadPort.Validate(); err == nil {
		t.Errorf("expected error for invalid port range")
	}
}

func TestServerConfig_ApplyEnvOverrides(t *testing.T) {
	t.Setenv("DCA_KING_ADDRESS", "http://king.internal:8080")
	t.Setenv("DCA_PAIR_CODE", "XYZ789")
	t.Setenv("DCA_PAIR_TOKEN", "tok-env")
	t.Setenv("DCA_NODE_ID", "node-env")
	t.Setenv("DCA_INGRESS_PORT", "9191")
	t.Setenv("DCA_WORKER_MODE", "true")
	t.Setenv("DCA_KING_MODE", "false")
	t.Setenv("DCA_PORT", "8888")

	cfg := DefaultServerConfig()
	cfg.ApplyEnvOverrides()

	if cfg.KingAddress != "http://king.internal:8080" {
		t.Errorf("KingAddress = %s; want http://king.internal:8080", cfg.KingAddress)
	}
	if cfg.PairCode != "XYZ789" {
		t.Errorf("PairCode = %s; want XYZ789", cfg.PairCode)
	}
	if cfg.PairToken != "tok-env" {
		t.Errorf("PairToken = %s; want tok-env", cfg.PairToken)
	}
	if cfg.NodeID != "node-env" {
		t.Errorf("NodeID = %s; want node-env", cfg.NodeID)
	}
	if cfg.IngressPort != 9191 {
		t.Errorf("IngressPort = %d; want 9191", cfg.IngressPort)
	}
	if !cfg.WorkerMode {
		t.Errorf("WorkerMode = false; want true")
	}
	if cfg.KingMode {
		t.Errorf("KingMode = true; want false")
	}
	if cfg.Port != 8888 {
		t.Errorf("Port = %d; want 8888", cfg.Port)
	}
}
