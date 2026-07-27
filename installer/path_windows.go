//go:build windows

package installer

import (
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

// AddFolderToUserPath appends the specified folder path to the current user's PATH environment variable.
func AddFolderToUserPath(folderPath string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.ALL_ACCESS)
	if err != nil {
		return fmt.Errorf("failed to open Environment registry key: %w", err)
	}
	defer k.Close()

	pathVal, _, err := k.GetStringValue("Path")
	if err != nil {
		pathVal = ""
	}

	folderUpper := strings.ToUpper(filepath.Clean(folderPath))
	parts := filepath.SplitList(pathVal)
	alreadyExists := false
	for _, p := range parts {
		if strings.ToUpper(filepath.Clean(p)) == folderUpper {
			alreadyExists = true
			break
		}
	}

	if !alreadyExists {
		var newPath string
		if pathVal == "" {
			newPath = folderPath
		} else {
			existing := strings.TrimRight(pathVal, ";")
			newPath = existing + ";" + folderPath
		}
		err = k.SetStringValue("Path", newPath)
		if err != nil {
			return fmt.Errorf("failed to write Path to registry: %w", err)
		}

		broadcastSettingChange()
	}
	return nil
}

func broadcastSettingChange() {
	user32 := syscall.NewLazyDLL("user32.dll")
	sendMsg := user32.NewProc("SendMessageTimeoutW")
	if sendMsg.Find() == nil {
		envStr, _ := syscall.UTF16PtrFromString("Environment")
		_, _, _ = sendMsg.Call(
			0xffff, // HWND_BROADCAST
			0x001a, // WM_SETTINGCHANGE
			0,
			uintptr(unsafe.Pointer(envStr)),
			0x0002, // SMTO_ABORTIFHUNG
			2000,   // timeout
			0,
		)
	}
}
