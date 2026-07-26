// +build windows

package utils

import (
	"fmt"
	"path/filepath"
	"golang.org/x/sys/windows/registry"
)

func RegisterPortableApp(cfg AppConfig) error {
	progID := cfg.AppKey + ".AssocFile"
	exeName := filepath.Base(cfg.ExePath)

	// 1. ProgID setup
	pKey, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\`+progID, registry.ALL_ACCESS)
	if err != nil {
		return fmt.Errorf("failed creating ProgID: %w", err)
	}
	defer pKey.Close()
	pKey.SetStringValue("", cfg.AppName+" File")

	iconKey, _, _ := registry.CreateKey(pKey, "DefaultIcon", registry.ALL_ACCESS)
	iconKey.SetStringValue("", fmt.Sprintf(`"%s",0`, cfg.ExePath))
	iconKey.Close()

	cmdKey, _, _ := registry.CreateKey(pKey, `shell\open\command`, registry.ALL_ACCESS)
	cmdKey.SetStringValue("", fmt.Sprintf(`"%s" "%%1"`, cfg.ExePath))
	cmdKey.Close()

	// 2. Capabilities
	capPath := fmt.Sprintf(`Software\%s\Capabilities`, cfg.AppKey)
	capKey, _, err := registry.CreateKey(registry.CURRENT_USER, capPath, registry.ALL_ACCESS)
	if err == nil {
		capKey.SetStringValue("ApplicationName", cfg.AppName)
		capKey.SetStringValue("ApplicationDescription", cfg.Description)
		assocKey, _, _ := registry.CreateKey(capKey, "FileAssociations", registry.ALL_ACCESS)
		for _, ext := range cfg.Extensions {
			assocKey.SetStringValue(ext, progID)
		}
		assocKey.Close()
		capKey.Close()
	}

	// 3. Registered Applications
	regAppsKey, _, _ := registry.CreateKey(registry.CURRENT_USER, `Software\RegisteredApplications`, registry.ALL_ACCESS)
	regAppsKey.SetStringValue(cfg.AppKey, capPath)
	regAppsKey.Close()

	// 4. App Paths (For "Run" command and Path resolution)
	appPathStr := fmt.Sprintf(`Software\Microsoft\Windows\CurrentVersion\App Paths\%s`, exeName)
	appPathKey, _, _ := registry.CreateKey(registry.CURRENT_USER, appPathStr, registry.ALL_ACCESS)
	appPathKey.SetStringValue("", cfg.ExePath)
	appPathKey.SetStringValue("Path", filepath.Dir(cfg.ExePath))
	appPathKey.Close()

	return nil
}

func RemovePortableApp(cfg AppConfig) error {
	progID := cfg.AppKey + ".AssocFile"
	exeName := filepath.Base(cfg.ExePath)

	// Remove Classes (deepest first for non-recursive DeleteKey)
	registry.DeleteKey(registry.CURRENT_USER, `Software\Classes\`+progID+`\shell\open\command`)
	registry.DeleteKey(registry.CURRENT_USER, `Software\Classes\`+progID+`\shell\open`)
	registry.DeleteKey(registry.CURRENT_USER, `Software\Classes\`+progID+`\shell`)
	registry.DeleteKey(registry.CURRENT_USER, `Software\Classes\`+progID+`\DefaultIcon`)
	registry.DeleteKey(registry.CURRENT_USER, `Software\Classes\`+progID)

	// Remove Registered App entry
	regAppsKey, err := registry.OpenKey(registry.CURRENT_USER, `Software\RegisteredApplications`, registry.ALL_ACCESS)
	if err == nil {
		regAppsKey.DeleteValue(cfg.AppKey)
		regAppsKey.Close()
	}

	// Remove App Capabilities (deepest first)
	registry.DeleteKey(registry.CURRENT_USER, `Software\`+cfg.AppKey+`\Capabilities\FileAssociations`)
	registry.DeleteKey(registry.CURRENT_USER, `Software\`+cfg.AppKey+`\Capabilities`)
	registry.DeleteKey(registry.CURRENT_USER, `Software\`+cfg.AppKey)

	// Remove App Path
	registry.DeleteKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\App Paths\`+exeName)

	return nil
}