package utils

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPersistentSession(t *testing.T) {
	var shell string
	if runtime.GOOS == "windows" {
		shell = "cmd.exe"
	} else {
		shell = "/bin/bash"
	}

	session, err := StartPersistentSession("test-sess-1", shell)
	if err != nil {
		t.Fatalf("Failed starting persistent session with shell %s: %v", shell, err)
	}
	defer session.Close()

	// 1. Run basic command
	var cmdStr string
	if runtime.GOOS == "windows" {
		cmdStr = "echo Hello Persistent Shell"
	} else {
		cmdStr = "echo 'Hello Persistent Shell'"
	}

	fut1 := session.ExecuteAsync(CommandOptions{
		Command: cmdStr,
		Timeout: 5 * time.Second,
	})
	res1 := <-fut1

	if res1.ExitCode != 0 {
		t.Errorf("Expected exit code 0, got %d. Output: %s", res1.ExitCode, res1.Output)
	}
	if !strings.Contains(res1.Output, "Hello Persistent Shell") {
		t.Errorf("Output missing expected text: %s", res1.Output)
	}

	// 2. Test state preservation across commands (Directory change)
	tempDir := t.TempDir()
	evalDir, _ := filepath.EvalSymlinks(tempDir)

	var cdCmd, pwdCmd string
	if runtime.GOOS == "windows" {
		cdCmd = "cd /d \"" + evalDir + "\""
		pwdCmd = "cd"
	} else {
		cdCmd = "cd \"" + evalDir + "\""
		pwdCmd = "pwd"
	}

	_ = <-session.ExecuteAsync(CommandOptions{Command: cdCmd})
	res2 := <-session.ExecuteAsync(CommandOptions{Command: pwdCmd})

	if res2.ExitCode != 0 {
		t.Errorf("Expected exit code 0 for pwdCmd, got %d", res2.ExitCode)
	}

	// Normalize directory paths for comparison
	normOutput := strings.ToLower(filepath.Clean(strings.TrimSpace(res2.Output)))
	normTarget := strings.ToLower(filepath.Clean(evalDir))

	if !strings.Contains(normOutput, normTarget) {
		t.Errorf("Expected persistent working directory containing '%s', got: '%s'", normTarget, normOutput)
	}
}

func TestRunElevated(t *testing.T) {
	// Skip actual elevation prompt in automated unit test environment
	if os.Getenv("CI") != "" {
		t.Skip("Skipping elevation prompt in CI environment")
	}
}
