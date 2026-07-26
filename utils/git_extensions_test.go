package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetCompactStatus_Clean(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	writeRepoFile(t, gm, "f.txt", "data")
	if err := gm.Add("f.txt"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if _, err := gm.Commit("init", "U", "u@t.com"); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	cs, err := gm.GetCompactStatus()
	if err != nil {
		t.Fatalf("GetCompactStatus failed: %v", err)
	}
	if !cs.IsClean {
		t.Error("expected clean status")
	}
	if len(cs.Modified) != 0 {
		t.Error("expected no modified files")
	}
	if len(cs.Untracked) != 0 {
		t.Error("expected no untracked files")
	}
	if len(cs.Staged) != 0 {
		t.Error("expected no staged files")
	}
}

func TestGetCompactStatus_Modified(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	writeRepoFile(t, gm, "f.txt", "data")
	if err := gm.Add("f.txt"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if _, err := gm.Commit("init", "U", "u@t.com"); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	os.WriteFile(filepath.Join(gm.repoPath, "f.txt"), []byte("modified"), 0644)

	cs, err := gm.GetCompactStatus()
	if err != nil {
		t.Fatalf("GetCompactStatus failed: %v", err)
	}
	if cs.IsClean {
		t.Error("expected dirty status with modified file")
	}
	if len(cs.Modified) == 0 {
		t.Error("expected modified files")
	}
}

func TestGetCompactStatus_Untracked(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	writeRepoFile(t, gm, "f.txt", "data")
	if err := gm.Add("f.txt"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if _, err := gm.Commit("init", "U", "u@t.com"); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	writeRepoFile(t, gm, "untracked.txt", "new file")

	cs, err := gm.GetCompactStatus()
	if err != nil {
		t.Fatalf("GetCompactStatus failed: %v", err)
	}
	if cs.IsClean {
		t.Error("expected dirty status with untracked file")
	}
	if len(cs.Untracked) != 1 {
		t.Fatalf("expected 1 untracked file, got %d", len(cs.Untracked))
	}
	if cs.Untracked[0] != "untracked.txt" {
		t.Errorf("expected 'untracked.txt', got %q", cs.Untracked[0])
	}
}

func TestGetCompactStatus_Staged(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	writeRepoFile(t, gm, "f.txt", "data")
	if err := gm.Add("f.txt"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if _, err := gm.Commit("init", "U", "u@t.com"); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	os.WriteFile(filepath.Join(gm.repoPath, "f.txt"), []byte("staged change"), 0644)
	if err := gm.Add("f.txt"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	cs, err := gm.GetCompactStatus()
	if err != nil {
		t.Fatalf("GetCompactStatus failed: %v", err)
	}
	if cs.IsClean {
		t.Error("expected dirty status with staged file")
	}
	if len(cs.Staged) == 0 {
		t.Error("expected staged files")
	}
}

func TestGetCompactLog(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("f%d.txt", i)
		writeRepoFile(t, gm, name, "x")
		if err := gm.Add(name); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
		if _, err := gm.Commit(fmt.Sprintf("commit %d", i), "U", "u@t.com"); err != nil {
			t.Fatalf("Commit failed: %v", err)
		}
	}

	logs, err := gm.GetCompactLog(0)
	if err != nil {
		t.Fatalf("GetCompactLog failed: %v", err)
	}
	if len(logs) != 3 {
		t.Fatalf("expected 3 log entries, got %d", len(logs))
	}

	for i, entry := range logs {
		parts := strings.SplitN(entry, " | ", 2)
		if len(parts) != 2 {
			t.Errorf("entry %d: expected 'hash | message' format, got %q", i, entry)
		}
		if len(parts[0]) != 7 {
			t.Errorf("entry %d: expected 7-char hash, got %q", i, parts[0])
		}
	}
}

func TestGetCompactLog_Limit(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("f%d.txt", i)
		writeRepoFile(t, gm, name, "x")
		if err := gm.Add(name); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
		if _, err := gm.Commit(fmt.Sprintf("c%d", i), "U", "u@t.com"); err != nil {
			t.Fatalf("Commit failed: %v", err)
		}
	}

	logs, err := gm.GetCompactLog(2)
	if err != nil {
		t.Fatalf("GetCompactLog failed: %v", err)
	}
	if len(logs) != 2 {
		t.Errorf("expected 2 log entries with limit, got %d", len(logs))
	}
}

func TestCreateGitCheckpoint(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	writeRepoFile(t, gm, "f.txt", "data")
	if err := gm.Add("f.txt"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if _, err := gm.Commit("init", "U", "u@t.com"); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	writeRepoFile(t, gm, "untracked.txt", "checkpoint content")
	hash, err := gm.CreateGitCheckpoint("my-label")
	if err != nil {
		t.Fatalf("CreateGitCheckpoint failed: %v", err)
	}
	if hash == "" {
		t.Error("expected non-empty hash from checkpoint")
	}

	cs, err := gm.GetCompactStatus()
	if err != nil {
		t.Fatalf("GetCompactStatus failed: %v", err)
	}
	if !cs.IsClean {
		t.Error("expected clean status after checkpoint commit")
	}

	logs, _ := gm.GetCompactLog(1)
	if len(logs) > 0 && !strings.Contains(logs[0], "[CHECKPOINT]") {
		t.Errorf("expected checkpoint marker in log message, got %q", logs[0])
	}
}

func TestCreateGitCheckpoint_DefaultLabel(t *testing.T) {
	gm, cleanup := createTestRepo(t)
	defer cleanup()

	writeRepoFile(t, gm, "f.txt", "data")
	if err := gm.Add("f.txt"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if _, err := gm.Commit("init", "U", "u@t.com"); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	writeRepoFile(t, gm, "new.txt", "checkpoint")
	hash, err := gm.CreateGitCheckpoint("")
	if err != nil {
		t.Fatalf("CreateGitCheckpoint failed: %v", err)
	}
	if hash == "" {
		t.Error("expected non-empty hash from checkpoint")
	}

	logs, _ := gm.GetCompactLog(1)
	if len(logs) > 0 && !strings.Contains(logs[0], "[CHECKPOINT] checkpoint") {
		t.Errorf("expected default 'checkpoint-' label, got %q", logs[0])
	}
}
