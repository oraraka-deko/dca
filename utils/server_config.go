package utils

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type AuthMode string

const (
	AuthModeOpen               AuthMode = "open"
	AuthModeCustomPath         AuthMode = "custom_path"
	AuthModeCustomPathIP       AuthMode = "custom_path_ip"
	AuthModeIPOnly             AuthMode = "ip_only"
	AuthModeToken              AuthMode = "token"
	AuthModeCustomPathTokenIP AuthMode = "custom_path_token_ip"
)

type CertType string

const (
	CertTypeNone       CertType = "none"
	CertTypeSelfSigned CertType = "selfsigned"
	CertTypeAcme       CertType = "acme"
	CertTypeCustom     CertType = "custom"
)

// ServerConfig holds the network, TLS, authorization, and database settings for the MCP server.
type ServerConfig struct {
	Host           string   `json:"host"`
	Port           int      `json:"port"`
	Protocol       string   `json:"protocol"` // "http" or "https"
	CertType       CertType `json:"cert_type"`
	Domain         string   `json:"domain"`
	CertFile       string   `json:"cert_file"`
	KeyFile        string   `json:"key_file"`
	AuthMode       AuthMode `json:"auth_mode"`
	CustomBasePath string   `json:"custom_base_path"`
	AllowedIPs     []string `json:"allowed_ips"`
	AuthToken      string   `json:"auth_token,omitempty"`

	// Database fields
	DBType       string `json:"db_type"`        // "sqlite" or "postgres"
	DBConnString string `json:"db_conn_string"` // sqlite file path or postgres connection string
}

// DefaultServerConfig returns safe default server options.
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		Host:           "0.0.0.0",
		Port:           8080,
		Protocol:       "http",
		CertType:       CertTypeNone,
		Domain:         "localhost",
		AuthMode:       AuthModeOpen,
		CustomBasePath: "/mcp",
		AllowedIPs:     []string{},
		AuthToken:      "",
		DBType:         "sqlite",
		DBConnString:   "mymcp.db",
	}
}

// SaveToFile serializes ServerConfig to JSON.
func (cfg *ServerConfig) SaveToFile(filePath string) error {
	dir := filepath.Dir(filePath)
	if dir != "." && dir != "/" {
		_ = os.MkdirAll(dir, 0755)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, data, 0644)
}

// LoadServerConfig loads ServerConfig from JSON file.
func LoadServerConfig(filePath string) (*ServerConfig, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	var cfg ServerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.DBType == "" {
		cfg.DBType = "sqlite"
	}
	if cfg.DBConnString == "" {
		cfg.DBConnString = "mymcp.db"
	}
	return &cfg, nil
}

// ValidateAuthRequest checks whether an incoming HTTP request satisfies the AuthMode rules.
func (cfg *ServerConfig) ValidateAuthRequest(r *http.Request) bool {
	// 1. Token Check if Token mode active
	needTokenCheck := cfg.AuthMode == AuthModeToken || cfg.AuthMode == AuthModeCustomPathTokenIP
	if needTokenCheck {
		tokenHeader := r.Header.Get("X-MCP-Auth-Token")
		tokenQuery := r.URL.Query().Get("token")
		if cfg.AuthToken != "" && tokenHeader != cfg.AuthToken && tokenQuery != cfg.AuthToken {
			return false
		}
	}

	// 2. IP Validation if IP mode active
	needIPCheck := cfg.AuthMode == AuthModeIPOnly || cfg.AuthMode == AuthModeCustomPathIP || cfg.AuthMode == AuthModeCustomPathTokenIP
	if needIPCheck {
		clientIP := getClientIP(r)
		if !cfg.isIPAllowed(clientIP) {
			return false
		}
	}

	// 3. Path Validation if Custom Path mode active
	needPathCheck := cfg.AuthMode == AuthModeCustomPath || cfg.AuthMode == AuthModeCustomPathIP || cfg.AuthMode == AuthModeCustomPathTokenIP
	if needPathCheck {
		basePath := cfg.CustomBasePath
		if !strings.HasPrefix(basePath, "/") {
			basePath = "/" + basePath
		}
		reqPath := r.URL.Path
		if !strings.HasPrefix(reqPath, basePath) {
			return false
		}
	}

	return true
}

func (cfg *ServerConfig) isIPAllowed(clientIP string) bool {
	if len(cfg.AllowedIPs) == 0 {
		return true
	}
	ip := net.ParseIP(clientIP)
	if ip == nil {
		return false
	}

	for _, allowed := range cfg.AllowedIPs {
		allowed = strings.TrimSpace(allowed)
		if allowed == clientIP {
			return true
		}
		// Check CIDR block
		if strings.Contains(allowed, "/") {
			_, cidr, err := net.ParseCIDR(allowed)
			if err == nil && cidr.Contains(ip) {
				return true
			}
		}
	}
	return false
}

func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header
	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded != "" {
		parts := strings.Split(forwarded, ",")
		return strings.TrimSpace(parts[0])
	}
	// Check X-Real-IP
	realIP := r.Header.Get("X-Real-IP")
	if realIP != "" {
		return strings.TrimSpace(realIP)
	}

	// Fallback to RemoteAddr
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
