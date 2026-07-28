package utils

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPairing_Normalization(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"  ABC123  ", "ABC123"},
		{"abc-123", "ABC123"},
		{"a-b-c-1-2-3", "ABC123"},
		{"XYZ789", "XYZ789"},
		{" xyz-789 ", "XYZ789"},
	}

	for _, tc := range cases {
		norm := NormalizePairingCode(tc.input)
		if norm != tc.expected {
			t.Errorf("NormalizePairingCode(%q) = %q; expected %q", tc.input, norm, tc.expected)
		}
	}
}

func TestPairing_SuccessAndTokenFormat(t *testing.T) {
	pm := NewPairingManager()
	code := "ABC123"
	deviceID := "node-001"

	if err := pm.AddPairingCode(code, deviceID); err != nil {
		t.Fatalf("Failed to add pairing code: %v", err)
	}

	token, dev, err := pm.ValidateAndPair("  abc-123  ")
	if err != nil {
		t.Fatalf("ValidateAndPair failed unexpectedly: %v", err)
	}

	if dev != deviceID {
		t.Errorf("Expected device ID %q, got %q", deviceID, dev)
	}

	if !strings.HasPrefix(token, "token-") {
		t.Errorf("Issued token %q must start with 'token-' prefix", token)
	}

	// Validate token lookup
	valDev, valid := pm.ValidatePairToken(token)
	if !valid || valDev != deviceID {
		t.Errorf("ValidatePairToken(%q) = (%q, %v); expected (%q, true)", token, valDev, valid, deviceID)
	}
}

func TestPairing_SingleUseEnforcement(t *testing.T) {
	pm := NewPairingManager()
	code := "SINGLE1"
	deviceID := "node-single"

	if err := pm.AddPairingCode(code, deviceID); err != nil {
		t.Fatalf("Failed to add code: %v", err)
	}

	_, _, err := pm.ValidateAndPair(code)
	if err != nil {
		t.Fatalf("First validation failed: %v", err)
	}

	// Second attempt must fail with "already consumed"
	_, _, err = pm.ValidateAndPair(code)
	if err == nil {
		t.Fatal("Expected error on second validation attempt, got nil")
	}
	if !strings.Contains(err.Error(), "already consumed") {
		t.Errorf("Expected error containing 'already consumed', got %q", err.Error())
	}
}

func TestPairing_TTLExpiration(t *testing.T) {
	pm := NewPairingManager()
	code := "EXP123"
	deviceID := "node-exp"

	if err := pm.AddPairingCodeWithTTL(code, deviceID, 50*time.Millisecond); err != nil {
		t.Fatalf("Failed to add code with TTL: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	_, _, err := pm.ValidateAndPair(code)
	if err == nil {
		t.Fatal("Expected error on expired code validation, got nil")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("Expected error containing 'expired', got %q", err.Error())
	}
}

func TestPairing_InvalidCodeContract(t *testing.T) {
	pm := NewPairingManager()

	_, _, err := pm.ValidateAndPair("NONEXISTENT")
	if err == nil {
		t.Fatal("Expected error on invalid code validation, got nil")
	}
	if !strings.Contains(err.Error(), "invalid pairing code") {
		t.Errorf("Expected error containing 'invalid pairing code', got %q", err.Error())
	}
}

func TestPairing_TokenRevocation(t *testing.T) {
	pm := NewPairingManager()
	code := "REV123"
	deviceID := "node-rev"

	_ = pm.AddPairingCode(code, deviceID)
	token, _, _ := pm.ValidateAndPair(code)

	if !pm.RevokePairToken(token) {
		t.Errorf("Expected RevokePairToken(%q) to return true", token)
	}

	_, valid := pm.ValidatePairToken(token)
	if valid {
		t.Errorf("Expected token %q to be invalid after revocation", token)
	}
}

func TestPairing_ConcurrentInvocations(t *testing.T) {
	pm := NewPairingManager()
	code := "CONC01"
	deviceID := "node-conc"

	_ = pm.AddPairingCode(code, deviceID)

	var wg sync.WaitGroup
	numGoroutines := 50
	successCount := 0
	alreadyConsumedCount := 0
	var mu sync.Mutex

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := pm.ValidateAndPair("  conc-01  ")
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successCount++
			} else if strings.Contains(err.Error(), "already consumed") {
				alreadyConsumedCount++
			}
		}()
	}

	wg.Wait()

	if successCount != 1 {
		t.Errorf("Expected exactly 1 successful validation, got %d", successCount)
	}
	if alreadyConsumedCount != numGoroutines-1 {
		t.Errorf("Expected %d 'already consumed' errors, got %d", numGoroutines-1, alreadyConsumedCount)
	}
}
