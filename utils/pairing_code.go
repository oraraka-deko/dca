package utils

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const (
	// PairingCodeLength is the exact length of generated pairing codes.
	PairingCodeLength = 6
	// PairingCodeCharset contains valid uppercase alphanumeric characters for pairing codes.
	PairingCodeCharset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

var pairingCodeRegex = regexp.MustCompile(`^[A-Z0-9]{6}$`)

// WorkerCredentials stores worker identity and pairing state for persistent authentication.
type WorkerCredentials struct {
	NodeID    string `json:"node_id"`
	PairToken string `json:"pair_token"`
	KingURL   string `json:"king_url"`
	IsPaired  bool   `json:"is_paired"`
}

// GeneratePairingCode creates a 6-character uppercase alphanumeric string using crypto/rand.
func GeneratePairingCode() (string, error) {
	code := make([]byte, PairingCodeLength)
	charsetLen := big.NewInt(int64(len(PairingCodeCharset)))
	for i := 0; i < PairingCodeLength; i++ {
		num, err := rand.Int(rand.Reader, charsetLen)
		if err != nil {
			return "", fmt.Errorf("crypto/rand failed: %w", err)
		}
		code[i] = PairingCodeCharset[num.Int64()]
	}
	return string(code), nil
}

// ValidatePairingCode checks if a string is a valid 6-character uppercase alphanumeric pairing code.
// It trims whitespace and strips hyphens before validation.
func ValidatePairingCode(code string) bool {
	clean := strings.ToUpper(strings.TrimSpace(code))
	clean = strings.ReplaceAll(clean, "-", "")
	return pairingCodeRegex.MatchString(clean)
}

// FormatPairingCode converts a 6-character code into hyphenated ABC-DEF format.
func FormatPairingCode(code string) string {
	clean := strings.ToUpper(strings.TrimSpace(code))
	clean = strings.ReplaceAll(clean, "-", "")
	if len(clean) != 6 {
		return code
	}
	return clean[:3] + "-" + clean[3:]
}

// PairingCodeManager manages local pairing status, code generation, and credential persistence.
type PairingCodeManager struct {
	mu          sync.Mutex
	currentCode string
	filePath    string
	creds       WorkerCredentials
}

// NewPairingCodeManager creates a new PairingCodeManager bound to a credential storage file path.
func NewPairingCodeManager(filePath string) *PairingCodeManager {
	return &PairingCodeManager{
		filePath: filePath,
	}
}

// GetOrGenerateCode returns the current pairing code or generates a new 6-character code if none exists.
func (m *PairingCodeManager) GetOrGenerateCode() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.currentCode != "" {
		return m.currentCode, nil
	}

	code, err := GeneratePairingCode()
	if err != nil {
		return "", err
	}
	m.currentCode = code
	return code, nil
}

// LoadCredentials reads and unmarshals WorkerCredentials from disk.
func (m *PairingCodeManager) LoadCredentials() (*WorkerCredentials, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.filePath == "" {
		return &m.creds, nil
	}

	data, err := os.ReadFile(m.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &m.creds, nil
		}
		return nil, fmt.Errorf("failed reading credentials file: %w", err)
	}

	var creds WorkerCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("failed unmarshaling credentials: %w", err)
	}

	m.creds = creds
	return &m.creds, nil
}

// SaveCredentials writes WorkerCredentials to disk in JSON format.
func (m *PairingCodeManager) SaveCredentials(creds WorkerCredentials) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.creds = creds
	if m.filePath == "" {
		return nil
	}

	dir := filepath.Dir(m.filePath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed creating directory for credentials: %w", err)
		}
	}

	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("failed marshaling credentials: %w", err)
	}

	if err := os.WriteFile(m.filePath, data, 0644); err != nil {
		return fmt.Errorf("failed writing credentials file: %w", err)
	}

	return nil
}

// IsPaired checks if the worker is currently paired based on loaded credentials.
func (m *PairingCodeManager) IsPaired() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.creds.IsPaired || m.creds.PairToken != ""
}

// GetCredentials returns a copy of current loaded credentials.
func (m *PairingCodeManager) GetCredentials() WorkerCredentials {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.creds
}
