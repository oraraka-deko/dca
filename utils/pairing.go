package utils

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// PairingCodeRecord holds metadata for a registered temporary pairing code on the King server.
type PairingCodeRecord struct {
	Code       string    `json:"code"`        // Normalized pairing code
	DeviceID   string    `json:"device_id"`   // Target device/node ID bound to this code
	CreatedAt  time.Time `json:"created_at"`  // Registration timestamp
	ExpiresAt  time.Time `json:"expires_at"`  // Optional expiration timestamp (zero value if no expiry)
	IsConsumed bool      `json:"is_consumed"` // Single-use invalidation flag
	ConsumedAt time.Time `json:"consumed_at"` // Timestamp when code was consumed
}

// PairTokenRecord holds metadata for an issued pair token.
type PairTokenRecord struct {
	Token    string    `json:"token"`     // Issued bearer pair token (prefix: "token-")
	DeviceID string    `json:"device_id"` // Associated target device/node ID
	IssuedAt time.Time `json:"issued_at"` // Issuance timestamp
	Revoked  bool      `json:"revoked"`   // Revocation status
}

// PairingManager handles King-side pairing code storage, single-use validation, and pair token issuance.
type PairingManager struct {
	mu           sync.RWMutex
	pairingCodes map[string]*PairingCodeRecord // normalized code -> PairingCodeRecord
	pairTokens   map[string]*PairTokenRecord   // pairToken -> PairTokenRecord
	consumed     map[string]bool              // normalized code -> consumed flag
}

// NewPairingManager creates a thread-safe PairingManager instance.
func NewPairingManager() *PairingManager {
	return &PairingManager{
		pairingCodes: make(map[string]*PairingCodeRecord),
		pairTokens:   make(map[string]*PairTokenRecord),
		consumed:     make(map[string]bool),
	}
}

// NormalizePairingCode cleans, upper-cases, and strips hyphens/whitespace from input code.
func NormalizePairingCode(code string) string {
	clean := strings.ToUpper(strings.TrimSpace(code))
	return strings.ReplaceAll(clean, "-", "")
}

// GeneratePairToken creates a secure pair token with "token-" prefix.
func GeneratePairToken() string {
	return "token-" + uuid.New().String()
}

// AddPairingCode registers a pairing code for a target device ID.
func (pm *PairingManager) AddPairingCode(code, deviceID string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	norm := NormalizePairingCode(code)
	if norm == "" {
		return fmt.Errorf("invalid pairing code %s", code)
	}

	pm.pairingCodes[norm] = &PairingCodeRecord{
		Code:       norm,
		DeviceID:   deviceID,
		CreatedAt:  time.Now().UTC(),
		IsConsumed: false,
	}
	delete(pm.consumed, norm)
	return nil
}

// AddPairingCodeWithTTL registers a pairing code with a time-to-live expiration duration.
func (pm *PairingManager) AddPairingCodeWithTTL(code, deviceID string, ttl time.Duration) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	norm := NormalizePairingCode(code)
	if norm == "" {
		return fmt.Errorf("invalid pairing code %s", code)
	}

	pm.pairingCodes[norm] = &PairingCodeRecord{
		Code:       norm,
		DeviceID:   deviceID,
		CreatedAt:  time.Now().UTC(),
		ExpiresAt:  time.Now().UTC().Add(ttl),
		IsConsumed: false,
	}
	delete(pm.consumed, norm)
	return nil
}

// ValidateAndPair validates a pairing code, enforces single-use consumption & TTL expiration,
// and returns a newly issued pair token along with the paired device ID.
func (pm *PairingManager) ValidateAndPair(code string) (token string, deviceID string, err error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	norm := NormalizePairingCode(code)
	if norm == "" {
		return "", "", fmt.Errorf("invalid pairing code %s", code)
	}

	rec, exists := pm.pairingCodes[norm]
	if !exists {
		return "", "", fmt.Errorf("invalid pairing code %s", code)
	}

	if pm.consumed[norm] || rec.IsConsumed {
		return "", "", fmt.Errorf("pairing code %s already consumed", code)
	}

	if !rec.ExpiresAt.IsZero() && time.Now().After(rec.ExpiresAt) {
		return "", "", fmt.Errorf("pairing code %s expired", code)
	}

	pm.consumed[norm] = true
	rec.IsConsumed = true
	rec.ConsumedAt = time.Now().UTC()

	token = GeneratePairToken()
	pm.pairTokens[token] = &PairTokenRecord{
		Token:    token,
		DeviceID: rec.DeviceID,
		IssuedAt: time.Now().UTC(),
		Revoked:  false,
	}

	return token, rec.DeviceID, nil
}

// ValidatePairToken checks if a pair token is valid and active, returning the associated device ID.
func (pm *PairingManager) ValidatePairToken(token string) (deviceID string, valid bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	rec, exists := pm.pairTokens[token]
	if !exists || rec.Revoked {
		return "", false
	}
	return rec.DeviceID, true
}

// RevokePairToken revokes an active pair token.
func (pm *PairingManager) RevokePairToken(token string) bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	rec, exists := pm.pairTokens[token]
	if !exists {
		return false
	}
	rec.Revoked = true
	return true
}

// RegisterDeviceToken directly assigns a pair token to a device ID.
func (pm *PairingManager) RegisterDeviceToken(deviceID, token string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	pm.pairTokens[token] = &PairTokenRecord{
		Token:    token,
		DeviceID: deviceID,
		IssuedAt: time.Now().UTC(),
		Revoked:  false,
	}
}

// AddDeviceCommand generates or accepts a code/deviceID, registers it on pm, and returns the record.
func (pm *PairingManager) AddDeviceCommand(code, deviceID string) (*PairingCodeRecord, error) {
	if code == "" {
		generated, err := GeneratePairingCode()
		if err != nil {
			return nil, fmt.Errorf("failed generating pairing code: %w", err)
		}
		code = generated
	}
	if deviceID == "" {
		deviceID = "node-" + uuid.New().String()[:8]
	}

	if err := pm.AddPairingCode(code, deviceID); err != nil {
		return nil, err
	}

	norm := NormalizePairingCode(code)
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	rec := pm.pairingCodes[norm]
	return rec, nil
}

