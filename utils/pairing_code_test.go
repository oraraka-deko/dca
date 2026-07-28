package utils

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestGeneratePairingCode(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		code, err := GeneratePairingCode()
		if err != nil {
			t.Fatalf("GeneratePairingCode failed: %v", err)
		}
		if len(code) != 6 {
			t.Errorf("Expected length 6, got %d for code %s", len(code), code)
		}
		if !ValidatePairingCode(code) {
			t.Errorf("Generated code %s failed validation", code)
		}
		seen[code] = true
	}
	if len(seen) < 90 {
		t.Errorf("Low entropy detected in code generation: %d unique out of 100", len(seen))
	}
}

func TestValidatePairingCode(t *testing.T) {
	testCases := []struct {
		code  string
		valid bool
	}{
		{"ABCDEF", true},
		{"123456", true},
		{"A1B2C3", true},
		{"a1b2c3", true}, // normalized to uppercase
		{"ABC-DEF", true}, // hyphenated format
		{" A1B2C3 ", true}, // trimmed
		{"ABCDE", false}, // short
		{"ABCDEFG", false}, // long
		{"ABC!EF", false}, // invalid character
		{"", false},
	}

	for _, tc := range testCases {
		got := ValidatePairingCode(tc.code)
		if got != tc.valid {
			t.Errorf("ValidatePairingCode(%q) = %v; want %v", tc.code, got, tc.valid)
		}
	}
}

func TestFormatPairingCode(t *testing.T) {
	if got := FormatPairingCode("ABCDEF"); got != "ABC-DEF" {
		t.Errorf("FormatPairingCode(ABCDEF) = %q; want ABC-DEF", got)
	}
	if got := FormatPairingCode("abc-def"); got != "ABC-DEF" {
		t.Errorf("FormatPairingCode(abc-def) = %q; want ABC-DEF", got)
	}
	if got := FormatPairingCode("SHORT"); got != "SHORT" {
		t.Errorf("FormatPairingCode(SHORT) = %q; want SHORT", got)
	}
}

func TestPairingCodeManager_CredentialsFile(t *testing.T) {
	tmpDir := t.TempDir()
	credPath := filepath.Join(tmpDir, "subDir", "credentials.json")

	mgr := NewPairingCodeManager(credPath)

	// Initially un-paired
	if mgr.IsPaired() {
		t.Error("Expected worker to be un-paired initially")
	}

	code1, err := mgr.GetOrGenerateCode()
	if err != nil {
		t.Fatalf("GetOrGenerateCode failed: %v", err)
	}
	code2, err := mgr.GetOrGenerateCode()
	if err != nil {
		t.Fatalf("GetOrGenerateCode failed: %v", err)
	}
	if code1 != code2 {
		t.Errorf("Expected sticky code, got %s and %s", code1, code2)
	}

	testCreds := WorkerCredentials{
		NodeID:    "node-12345",
		PairToken: "token-secret-999",
		KingURL:   "ws://localhost:8080/register",
		IsPaired:  true,
	}

	if err := mgr.SaveCredentials(testCreds); err != nil {
		t.Fatalf("SaveCredentials failed: %v", err)
	}

	// Read back credentials using new manager instance
	mgr2 := NewPairingCodeManager(credPath)
	loaded, err := mgr2.LoadCredentials()
	if err != nil {
		t.Fatalf("LoadCredentials failed: %v", err)
	}

	if loaded.NodeID != testCreds.NodeID {
		t.Errorf("Loaded NodeID = %s; want %s", loaded.NodeID, testCreds.NodeID)
	}
	if loaded.PairToken != testCreds.PairToken {
		t.Errorf("Loaded PairToken = %s; want %s", loaded.PairToken, testCreds.PairToken)
	}
	if loaded.KingURL != testCreds.KingURL {
		t.Errorf("Loaded KingURL = %s; want %s", loaded.KingURL, testCreds.KingURL)
	}
	if !mgr2.IsPaired() {
		t.Error("Expected IsPaired() to be true after loading saved credentials")
	}
}

func TestPairingCodeManager_Concurrency(t *testing.T) {
	tmpDir := t.TempDir()
	credPath := filepath.Join(tmpDir, "creds.json")
	mgr := NewPairingCodeManager(credPath)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = mgr.GetOrGenerateCode()
			_ = mgr.IsPaired()
			_ = mgr.SaveCredentials(WorkerCredentials{NodeID: "test", IsPaired: true})
			_, _ = mgr.LoadCredentials()
		}()
	}
	wg.Wait()
}
