package installer

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"dca/utils"
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

func copyFile(src, dst string) error {
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	_ = os.Remove(dst)

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// InstallService registers and enables the mymcp background service on the host system.
func InstallService(cfg utils.ServerConfig, configPath string) error {
	if configPath == "" {
		configPath = GetDefaultConfigPath()
	}

	// 1. Stop existing service to unlock files and prevent issues
	_ = StopService()

	// 2. Locate current running binary
	currExe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to locate current executable: %w", err)
	}

	var finalExePath string
	switch runtime.GOOS {
	case "linux":
		finalExePath = "/usr/local/bin/mymcp"
		if err := copyFile(currExe, finalExePath); err != nil {
			return fmt.Errorf("failed to copy binary to %s: %w", finalExePath, err)
		}
	case "windows":
		progData := os.Getenv("ProgramData")
		if progData == "" {
			progData = `C:\ProgramData`
		}
		installDir := filepath.Join(progData, "mymcp")
		finalExePath = filepath.Join(installDir, "mymcp.exe")
		if err := copyFile(currExe, finalExePath); err != nil {
			return fmt.Errorf("failed to copy binary to %s: %w", finalExePath, err)
		}
		// Update PATH to include installDir
		if err := AddFolderToUserPath(installDir); err != nil {
			fmt.Printf("Warning: failed to add %s to user PATH: %v\n", installDir, err)
		}
	default:
		return fmt.Errorf("unsupported OS for service installation: %s", runtime.GOOS)
	}

	// 3. Ensure SSL certificates are provisioned if HTTPS enabled
	if err := utils.EnsureCertificates(&cfg, filepath.Dir(configPath)); err != nil {
		fmt.Printf("Warning: SSL certificate provisioning: %v\n", err)
	}

	// 4. Save configuration
	if err := cfg.SaveToFile(configPath); err != nil {
		return fmt.Errorf("failed saving service config: %w", err)
	}

	// 4. Register and start service pointing to the copied binary
	switch runtime.GOOS {
	case "linux":
		return installLinuxService(finalExePath, configPath)
	case "windows":
		return installWindowsService(finalExePath, configPath)
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
		_ = os.Remove("/usr/local/bin/mymcp")
		return nil
	case "windows":
		_ = exec.Command("sc.exe", "stop", "mymcp").Run()
		out, err := exec.Command("sc.exe", "delete", "mymcp").CombinedOutput()
		if err != nil && !strings.Contains(string(out), "1060") { // Ignore if service does not exist
			return fmt.Errorf("failed deleting windows service: %v\nOutput: %s", err, string(out))
		}
		progData := os.Getenv("ProgramData")
		if progData == "" {
			progData = `C:\ProgramData`
		}
		_ = os.Remove(filepath.Join(progData, "mymcp", "mymcp.exe"))
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
		if err != nil && !strings.Contains(string(out), "1062") { // Ignore if service has not been started
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
