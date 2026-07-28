# Milestone 2 (R2) Requirement 1: Single-Use Pairing Exchange & Activation Key Token Issuance — Analysis Report

**Target Location**: `d:\Documents\dca\utils\pairing.go`  
**Explorer**: Explorer 1 (`teamwork_preview_explorer`)  
**Date**: 2026-07-28  

---

## Executive Summary

This report presents a thorough investigation of the architectural requirements, codebase conventions, data structures, edge cases, and interface signatures required for **Requirement 1** of Milestone 2 (King Control Plane Gateway Mode).

Requirement 1 mandates a single-use pairing code exchange mechanism (`dca king add-device <code>`), pairing code validation, expiration management, and single-use activation key token (pair token) issuance. All design proposals align with existing E2E harness expectations (`tests/e2e/harness.go`) and Tier 1–3 E2E test suites (`tests/e2e/tier1_feature_test.go`, `tier2_boundary_test.go`, `tier3_cross_feature_test.go`).

---

## 1. Codebase Audit & Existing Conventions

### 1.1 Existing Files Inspected

1. **`utils/pairing_code.go`** (Worker side, lines 1–165):
   - `GeneratePairingCode()` (Line 34): Generates 6-character uppercase alphanumeric strings from `ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789` using `crypto/rand`.
   - `ValidatePairingCode(code string) bool` (Line 49): Validates code format against `^[A-Z0-9]{6}$` after `strings.TrimSpace`, `strings.ToUpper`, and `strings.ReplaceAll(clean, "-", "")`.
   - `FormatPairingCode(code string) string` (Line 56): Converts 6-char code to `ABC-DEF` format.
   - `WorkerCredentials` (Line 26): Worker persistent state (`NodeID`, `PairToken`, `KingURL`, `IsPaired`).
   - `PairingCodeManager` (Line 66): Thread-safe manager for worker local pairing state and JSON persistence.

2. **`database/database.go`** (Lines 1–589):
   - Defines `Store` interface and `SQLStore` implementing SQLite and PostgreSQL persistence.
   - Tables: `server_configs`, `server_logs`, `task_history`.

3. **`utils/server_config.go`** (Lines 1–182):
   - Defines `ServerConfig` struct (`Host`, `Port`, `Protocol`, `AuthMode`, `DBType`, `DBConnString`).

4. **`tests/e2e/harness.go`** (Lines 255–375):
   - `MockKing` structure implements the target interface contract expected for King control plane:
     - `pairingCodes`: `map[string]string` (code -> deviceID)
     - `codeExpirations`: `map[string]time.Time` (code -> expiration time)
     - `pairTokens`: `map[string]string` (pairToken -> deviceID)
     - `consumedCodes`: `map[string]bool` (code -> true)
     - `AddPairingCode(code, deviceID string)`
     - `AddPairingCodeWithTTL(code, deviceID string, ttl time.Duration)`
     - `ValidateAndPair(code string) (token string, deviceID string, err error)`

---

## 2. Proposed Data Structures for `utils/pairing.go`

`utils/pairing.go` must define the King-side `PairingManager` and associated record types:

```go
package utils

import (
	"crypto/rand"
	"fmt"
	"strings"
	"sync"
	"time"
)

// PairingCodeRecord holds metadata for a registered temporary pairing code on the King server.
type PairingCodeRecord struct {
	Code       string    `json:"code"`        // Normalized 6-character uppercase code (e.g., "ABC123")
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
```

---

## 3. Core Interface & Function Signatures

### 3.1 Constructor & Helpers
```go
// NewPairingManager creates a thread-safe PairingManager instance.
func NewPairingManager() *PairingManager

// NormalizePairingCode cleans, upper-cases, and strips hyphens/whitespace from input code.
func NormalizePairingCode(code string) string {
	clean := strings.ToUpper(strings.TrimSpace(code))
	return strings.ReplaceAll(clean, "-", "")
}

// GeneratePairToken creates a secure pair token with "token-" prefix.
func GeneratePairToken() string {
	return "token-" + GenerateUUID()
}
```

### 3.2 Code Management Methods
```go
// AddPairingCode registers a pairing code for a target device ID.
func (pm *PairingManager) AddPairingCode(code, deviceID string) error

// AddPairingCodeWithTTL registers a pairing code with a time-to-live expiration duration.
func (pm *PairingManager) AddPairingCodeWithTTL(code, deviceID string, ttl time.Duration) error
```

### 3.3 Core Validation & Exchange (`ValidateAndPair`)
```go
// ValidateAndPair validates a pairing code, enforces single-use consumption & TTL expiration,
// and returns a newly issued pair token along with the paired device ID.
func (pm *PairingManager) ValidateAndPair(code string) (token string, deviceID string, err error)
```

**Step-by-step logic inside `ValidateAndPair`**:
1. `pm.mu.Lock()` (deferred `pm.mu.Unlock()`) to ensure race-free execution.
2. Trim and normalize code: `normalized := NormalizePairingCode(code)`.
3. Check lookup in `pm.pairingCodes`. If missing:
   `return "", "", fmt.Errorf("invalid pairing code %s", code)`
4. Check if consumed (`pm.consumed[normalized] == true` or `rec.IsConsumed == true`):
   `return "", "", fmt.Errorf("pairing code %s already consumed", code)`
5. Check expiration (`!rec.ExpiresAt.IsZero() && time.Now().After(rec.ExpiresAt)`):
   `return "", "", fmt.Errorf("pairing code %s expired", code)`
6. Mark consumed:
   `pm.consumed[normalized] = true`
   `rec.IsConsumed = true`
   `rec.ConsumedAt = time.Now().UTC()`
7. Issue token: `token = GeneratePairToken()`
8. Store token: `pm.pairTokens[token] = &PairTokenRecord{Token: token, DeviceID: rec.DeviceID, IssuedAt: time.Now().UTC()}`
9. Return `(token, rec.DeviceID, nil)`

### 3.4 Token Inspection & Revocation
```go
// ValidatePairToken checks if a pair token is valid and active, returning the associated device ID.
func (pm *PairingManager) ValidatePairToken(token string) (deviceID string, valid bool)

// RevokePairToken revokes an active pair token.
func (pm *PairingManager) RevokePairToken(token string) bool
```

---

## 4. Edge Cases & Safety Analysis

| Edge Case | Exact Condition / Scenario | Required Error / Behavior | E2E Test Reference |
|---|---|---|---|
| **Code Double-Use** | Second call to `ValidateAndPair` with same code | Error containing `"already consumed"` | `TestTier1_DeviceAddition_SingleUseCodeConsumption` (`tier1_feature_test.go:420`) |
| **Expired Code** | Call after `ExpiresAt` timestamp | Error containing `"expired"` | `TestTier1_WorkerPairingCode_Expiration` (`tier1_feature_test.go:98`) |
| **Non-existent Code** | Code not registered on King | Error containing `"invalid pairing code"` | `TestTier1_DeviceAddition_NonExistentCode` (`tier1_feature_test.go:433`) |
| **Formatting / Case Variations** | `  abc-123  `, `abc123`, `ABC-123` | Resolves to `ABC123` and succeeds | `TestTier2_PairingCode_CaseAndWhitespace` (`tier2_boundary_test.go:28-46`) |
| **Concurrent Pair Requests** | 50+ goroutines call `ValidateAndPair` concurrently | Mutex lock guarantees zero race conditions; exactly 1 caller succeeds per code | `TestTier1_DeviceAddition_ConcurrentInvocations` (`tier1_feature_test.go:460-492`) |
| **Empty Code Input** | Code input is `""` or whitespace | Returns `"invalid pairing code"` error | Unit test boundary |

---

## 5. CLI Command Integration (`dca king add-device <code>`)

In `main.go` / subcommand handler:
- Command syntax: `dca king add-device <code>` (or `--code <code> --device-id <id>`).
- Execution flow:
  1. Parse subcommand `king add-device`.
  2. Validate command argument length.
  3. Call `pairingMgr.ValidateAndPair(code)`.
  4. Print success output:
     ```text
     Device paired successfully!
     Device ID: node-001
     Issued Pair Token: token-550e8400-e29b-41d4-a716-446655440000
     ```
  5. If validation fails, print error to `os.Stderr` and exit with status 1.

---

## 6. Implementation Action Plan

1. Create `utils/pairing.go` implementing `PairingManager`, `ValidateAndPair`, `AddPairingCode`, `AddPairingCodeWithTTL`, `ValidatePairToken`, and `GeneratePairToken`.
2. Create `utils/pairing_test.go` covering all unit test cases and boundary conditions.
3. Integrate CLI subcommand handling for `dca king add-device <code>` in `main.go`.
