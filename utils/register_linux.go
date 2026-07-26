// +build linux

package utils

import (
	"fmt"
	"os"
	"path/filepath"
)

func RegisterPortableApp(cfg AppConfig) error {
	home, _ := os.UserHomeDir()

	// 1. Add to PATH (via symlink in ~/.local/bin)
	binDir := filepath.Join(home, ".local", "bin")
	os.MkdirAll(binDir, 0755)
	symlinkPath := filepath.Join(binDir, cfg.AppKey)
	
	// Remove existing symlink if it exists
	os.Remove(symlinkPath)
	if err := os.Symlink(cfg.ExePath, symlinkPath); err != nil {
		return fmt.Errorf("failed to create symlink: %w", err)
	}

	// 2. Create Desktop Entry Content
	desktopContent := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=%s
Comment=%s
Exec=%s %%f
Icon=%s
Terminal=false
Categories=Utility;Application;
MimeType=%s
`, cfg.AppName, cfg.Description, cfg.ExePath, cfg.IconPath, "application/octet-stream;")

	// 3. Install to Start Menu (~/.local/share/applications)
	appsDir := filepath.Join(home, ".local", "share", "applications")
	os.MkdirAll(appsDir, 0755)
	desktopFileName := cfg.AppKey + ".desktop"
	
	err := os.WriteFile(filepath.Join(appsDir, desktopFileName), []byte(desktopContent), 0644)
	if err != nil {
		return fmt.Errorf("failed to create start menu entry: %w", err)
	}

	// 4. Install to Desktop (~/Desktop)
	desktopDir := filepath.Join(home, "Desktop")
	if _, err := os.Stat(desktopDir); err == nil {
		os.WriteFile(filepath.Join(desktopDir, desktopFileName), []byte(desktopContent), 0755)
	}

	return nil
}

func RemovePortableApp(cfg AppConfig) error {
	home, _ := os.UserHomeDir()
	desktopFileName := cfg.AppKey + ".desktop"

	// Remove Symlink
	os.Remove(filepath.Join(home, ".local", "bin", cfg.AppKey))
	
	// Remove Menu Entry
	os.Remove(filepath.Join(home, ".local", "share", "applications", desktopFileName))
	
	// Remove Desktop Shortcut
	os.Remove(filepath.Join(home, "Desktop", desktopFileName))

	return nil
}