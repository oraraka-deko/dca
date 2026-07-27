package utils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// GetDefaultConfigPath returns the standard configuration file path based on OS.
func GetDefaultConfigPath() string {
	if runtime.GOOS == "windows" {
		progData := os.Getenv("ProgramData")
		if progData == "" {
			progData = `C:\ProgramData`
		}
		return filepath.Join(progData, "mymcp", "config.json")
	}
	return "/etc/mymcp/config.json"
}

// GenerateSystemdUnitFile generates a systemd service unit specification string for Linux.
func GenerateSystemdUnitFile(exePath string, configPath string) string {
	return fmt.Sprintf(`[Unit]
Description=MyMCP Server Service
After=network.target

[Service]
Type=simple
ExecStart=%s -config %s
Restart=always
RestartSec=5
User=root

[Install]
WantedBy=multi-user.target
`, exePath, configPath)
}

// InstallService registers and enables the mymcp background service on the host system.
func InstallService(cfg ServerConfig, configPath string) error {
	if configPath == "" {
		configPath = GetDefaultConfigPath()
	}

	// Save configuration first
	if err := cfg.SaveToFile(configPath); err != nil {
		return fmt.Errorf("failed saving service config: %w", err)
	}

	exePath, err := os.Executable()
	if err != nil {
		exePath = "mymcp"
	}

	switch runtime.GOOS {
	case "linux":
		return installLinuxService(exePath, configPath)
	case "windows":
		return installWindowsService(exePath, configPath)
	default:
		return fmt.Errorf("unsupported OS for service installation: %s", runtime.GOOS)
	}
}

func installLinuxService(exePath string, configPath string) error {
	unitContent := GenerateSystemdUnitFile(exePath, configPath)
	unitPath := "/etc/systemd/system/mymcp.service"

	if err := os.WriteFile(unitPath, []byte(unitContent), 0644); err != nil {
		return fmt.Errorf("failed writing systemd unit file: %w", err)
	}

	_ = exec.Command("systemctl", "daemon-reload").Run()
	_ = exec.Command("systemctl", "enable", "mymcp").Run()
	out, err := exec.Command("systemctl", "start", "mymcp").CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed starting mymcp systemd service: %v\nOutput: %s", err, string(out))
	}
	return nil
}

func installWindowsService(exePath string, configPath string) error {
	binPath := fmt.Sprintf(`"%s" -config "%s"`, exePath, configPath)
	cmd := exec.Command("sc.exe", "create", "mymcp", "binPath=", binPath, "start=", "auto", "DisplayName=", "MyMCP Server Service")
	out, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(out), "1073") { // Ignore if service already exists error
		return fmt.Errorf("failed creating windows service: %v\nOutput: %s", err, string(out))
	}

	startCmd := exec.Command("sc.exe", "start", "mymcp")
	_, _ = startCmd.CombinedOutput()
	return nil
}

// UninstallService stops and removes the mymcp background service.
func UninstallService() error {
	switch runtime.GOOS {
	case "linux":
		_ = exec.Command("systemctl", "stop", "mymcp").Run()
		_ = exec.Command("systemctl", "disable", "mymcp").Run()
		_ = os.Remove("/etc/systemd/system/mymcp.service")
		_ = exec.Command("systemctl", "daemon-reload").Run()
		return nil
	case "windows":
		_ = exec.Command("sc.exe", "stop", "mymcp").Run()
		out, err := exec.Command("sc.exe", "delete", "mymcp").CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed deleting windows service: %v\nOutput: %s", err, string(out))
		}
		return nil
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

// StartService starts the background mymcp service.
func StartService() error {
	switch runtime.GOOS {
	case "linux":
		out, err := exec.Command("systemctl", "start", "mymcp").CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed starting service: %v\nOutput: %s", err, string(out))
		}
		return nil
	case "windows":
		out, err := exec.Command("sc.exe", "start", "mymcp").CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed starting service: %v\nOutput: %s", err, string(out))
		}
		return nil
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

// StopService stops the background mymcp service.
func StopService() error {
	switch runtime.GOOS {
	case "linux":
		out, err := exec.Command("systemctl", "stop", "mymcp").CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed stopping service: %v\nOutput: %s", err, string(out))
		}
		return nil
	case "windows":
		out, err := exec.Command("sc.exe", "stop", "mymcp").CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed stopping service: %v\nOutput: %s", err, string(out))
		}
		return nil
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

// RestartService restarts the background mymcp service.
func RestartService() error {
	switch runtime.GOOS {
	case "linux":
		out, err := exec.Command("systemctl", "restart", "mymcp").CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed restarting service: %v\nOutput: %s", err, string(out))
		}
		return nil
	case "windows":
		_ = StopService()
		return StartService()
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

// GetServiceStatus returns current service status details.
func GetServiceStatus() (string, error) {
	switch runtime.GOOS {
	case "linux":
		out, _ := exec.Command("systemctl", "status", "mymcp").CombinedOutput()
		return string(out), nil
	case "windows":
		out, err := exec.Command("sc.exe", "query", "mymcp").CombinedOutput()
		if err != nil {
			return string(out), err
		}
		return string(out), nil
	default:
		return "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}
