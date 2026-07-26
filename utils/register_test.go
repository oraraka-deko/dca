//go:build windows || linux

package utils

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRegisterAndRemoveApp(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("not supported on darwin")
	}

	cfg := AppConfig{
		AppName:     "DCA UnitTest App",
		AppKey:      "dcaunittest_" + t.Name(),
		ExePath:     "/tmp/dca_unittest/testapp.sh",
		IconPath:    "/tmp/dca_unittest/testapp.png",
		Extensions:  []string{".dcauni1", ".dcauni2"},
		Description: "DCA unit test app - safe to delete",
	}

	if runtime.GOOS == "windows" {
		cfg.ExePath = `C:\Temp\dca_unittest\testapp.exe`
		cfg.IconPath = `C:\Temp\dca_unittest\testapp.ico`
	}

	if runtime.GOOS == "linux" {
		tempHome := t.TempDir()
		origHome := os.Getenv("HOME")
		os.Setenv("HOME", tempHome)
		t.Cleanup(func() { os.Setenv("HOME", origHome) })
	}

	if err := RegisterPortableApp(cfg); err != nil {
		t.Fatalf("RegisterPortableApp failed: %v", err)
	}

	verifyRegistered(t, cfg)

	if err := RemovePortableApp(cfg); err != nil {
		t.Fatalf("RemovePortableApp failed: %v", err)
	}

	verifyRemoved(t, cfg)
}

func TestRegisterApp_WithEmptyExtensions(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("not supported on darwin")
	}

	cfg := AppConfig{
		AppName:     "DCA NoExt App",
		AppKey:      "dcaunittest_noext_" + t.Name(),
		ExePath:     "/tmp/dca_unittest/noext.sh",
		IconPath:    "/tmp/dca_unittest/noext.png",
		Extensions:  []string{},
		Description: "DCA test app with no extensions",
	}

	if runtime.GOOS == "windows" {
		cfg.ExePath = `C:\Temp\dca_unittest\noext.exe`
		cfg.IconPath = `C:\Temp\dca_unittest\noext.ico`
	}

	if runtime.GOOS == "linux" {
		tempHome := t.TempDir()
		origHome := os.Getenv("HOME")
		os.Setenv("HOME", tempHome)
		t.Cleanup(func() { os.Setenv("HOME", origHome) })
	}

	if err := RegisterPortableApp(cfg); err != nil {
		t.Fatalf("RegisterPortableApp failed with empty extensions: %v", err)
	}

	verifyRegistered(t, cfg)

	if err := RemovePortableApp(cfg); err != nil {
		t.Fatalf("RemovePortableApp failed: %v", err)
	}

	verifyRemoved(t, cfg)
}

func TestRemoveApp_NotExist(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("not supported on darwin")
	}

	cfg := AppConfig{
		AppName:     "DCA NeverRegistered App",
		AppKey:      "dcaunittest_never_" + t.Name(),
		ExePath:     "/tmp/dca_unittest/never.sh",
		IconPath:    "/tmp/dca_unittest/never.png",
		Extensions:  []string{".dcanever"},
		Description: "DCA test app that was never registered",
	}

	if runtime.GOOS == "windows" {
		cfg.ExePath = `C:\Temp\dca_unittest\never.exe`
		cfg.IconPath = `C:\Temp\dca_unittest\never.ico`
	}

	err := RemovePortableApp(cfg)
	if err != nil {
		t.Errorf("RemovePortableApp on unregistered app should not error, got: %v", err)
	}
}

func verifyRegistered(t *testing.T, cfg AppConfig) {
	t.Helper()
	switch runtime.GOOS {
	case "linux":
		verifyLinuxRegistered(t, cfg)
	case "windows":
		verifyWindowsRegistered(t, cfg)
	}
}

func verifyRemoved(t *testing.T, cfg AppConfig) {
	t.Helper()
	switch runtime.GOOS {
	case "linux":
		verifyLinuxRemoved(t, cfg)
	case "windows":
		verifyWindowsRemoved(t, cfg)
	}
}

// Linux verification helpers

func verifyLinuxRegistered(t *testing.T, cfg AppConfig) {
	t.Helper()
	home := os.Getenv("HOME")

	symlinkPath := filepath.Join(home, ".local", "bin", cfg.AppKey)
	if _, err := os.Lstat(symlinkPath); err != nil {
		t.Errorf("expected symlink at %s: %v", symlinkPath, err)
	}

	desktopFile := filepath.Join(home, ".local", "share", "applications", cfg.AppKey+".desktop")
	if _, err := os.Stat(desktopFile); err != nil {
		t.Errorf("expected desktop entry at %s: %v", desktopFile, err)
	}

	desktopShortcut := filepath.Join(home, "Desktop", cfg.AppKey+".desktop")
	if _, err := os.Stat(desktopShortcut); err != nil {
		t.Errorf("expected desktop shortcut at %s: %v", desktopShortcut, err)
	}
}

func verifyLinuxRemoved(t *testing.T, cfg AppConfig) {
	t.Helper()
	home := os.Getenv("HOME")

	paths := []string{
		filepath.Join(home, ".local", "bin", cfg.AppKey),
		filepath.Join(home, ".local", "share", "applications", cfg.AppKey+".desktop"),
		filepath.Join(home, "Desktop", cfg.AppKey+".desktop"),
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("expected %s to be removed after cleanup", p)
		}
	}
}

// Windows verification helpers (uses reg.exe to avoid platform-specific imports)

func verifyWindowsRegistered(t *testing.T, cfg AppConfig) {
	t.Helper()

	progID := `HKCU\Software\Classes\` + cfg.AppKey + ".AssocFile"
	out, err := exec.Command("reg", "query", progID).CombinedOutput()
	if err != nil {
		t.Errorf("ProgID key not found after register:\n%s\n%v", out, err)
	}

	capKey := `HKCU\Software\` + cfg.AppKey + `\Capabilities`
	out, err = exec.Command("reg", "query", capKey).CombinedOutput()
	if err != nil {
		t.Errorf("Capabilities key not found after register:\n%s\n%v", out, err)
	}

	out, err = exec.Command("reg", "query", `HKCU\Software\RegisteredApplications`, "/v", cfg.AppKey).CombinedOutput()
	if err != nil {
		t.Errorf("RegisteredApplications value not found after register:\n%s\n%v", out, err)
	}
}

func verifyWindowsRemoved(t *testing.T, cfg AppConfig) {
	t.Helper()

	progID := `HKCU\Software\Classes\` + cfg.AppKey + ".AssocFile"
	out, err := exec.Command("reg", "query", progID).CombinedOutput()
	if err == nil {
		t.Errorf("ProgID key should have been removed:\n%s", out)
	}

	capKey := `HKCU\Software\` + cfg.AppKey
	out, err = exec.Command("reg", "query", capKey).CombinedOutput()
	if err == nil {
		t.Errorf("Capabilities tree should have been removed:\n%s", out)
	}
}
