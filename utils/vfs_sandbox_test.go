package utils

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVFSSandbox_MemFs(t *testing.T) {
	mgr := NewVFSSandboxManager()
	sb, err := mgr.CreateMemSandbox("test_mem")
	if err != nil {
		t.Fatalf("failed to create mem sandbox: %v", err)
	}

	err = sb.WriteFile("hello.txt", []byte("world"), 0644)
	if err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	data, err := sb.ReadFile("hello.txt")
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(data) != "world" {
		t.Fatalf("expected 'world', got '%s'", string(data))
	}

	exists, _ := sb.Exists("hello.txt")
	if !exists {
		t.Fatalf("expected file to exist")
	}

	_ = sb.RemoveFile("hello.txt")
	exists, _ = sb.Exists("hello.txt")
	if exists {
		t.Fatalf("expected file to be removed")
	}
}

func TestVFSSandbox_BasePathFs(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewVFSSandboxManager()

	sb, err := mgr.CreateBasePathSandbox("test_base", tempDir)
	if err != nil {
		t.Fatalf("failed to create base path sandbox: %v", err)
	}

	err = sb.WriteFile("sub/doc.txt", []byte("sandbox content"), 0644)
	if err != nil {
		t.Fatalf("failed to write file in sandbox: %v", err)
	}

	// Verify on host disk inside tempDir
	hostFile := filepath.Join(tempDir, "sub", "doc.txt")
	hostData, err := os.ReadFile(hostFile)
	if err != nil {
		t.Fatalf("expected file to exist on host: %v", err)
	}
	if string(hostData) != "sandbox content" {
		t.Fatalf("expected 'sandbox content', got '%s'", string(hostData))
	}
}

func TestVFSSandbox_CopyOnWriteFs(t *testing.T) {
	tempDir := t.TempDir()
	origFile := filepath.Join(tempDir, "original.txt")
	_ = os.WriteFile(origFile, []byte("original text"), 0644)

	mgr := NewVFSSandboxManager()
	sb, err := mgr.CreateCopyOnWriteSandbox("test_cow", tempDir)
	if err != nil {
		t.Fatalf("failed to create COW sandbox: %v", err)
	}

	// Read original via sandbox
	data, err := sb.ReadFile("original.txt")
	if err != nil {
		t.Fatalf("failed reading original via COW sandbox: %v", err)
	}
	if string(data) != "original text" {
		t.Fatalf("expected 'original text', got '%s'", string(data))
	}

	// Mutate inside sandbox
	err = sb.WriteFile("original.txt", []byte("mutated inside sandbox"), 0644)
	if err != nil {
		t.Fatalf("failed writing to COW sandbox: %v", err)
	}

	// Read inside sandbox (should see mutated text)
	dataSb, _ := sb.ReadFile("original.txt")
	if string(dataSb) != "mutated inside sandbox" {
		t.Fatalf("sandbox should see mutated text, got '%s'", string(dataSb))
	}

	// Read on host disk (original file MUST be untouched!)
	dataHost, _ := os.ReadFile(origFile)
	if string(dataHost) != "original text" {
		t.Fatalf("host file was modified! Expected 'original text', got '%s'", string(dataHost))
	}
}

func TestVFSSandbox_FileManagerOps(t *testing.T) {
	mgr := NewVFSSandboxManager()
	sb, _ := mgr.CreateMemSandbox("test_ops")

	// 1. BatchCreate & MkdirAll
	files := map[string][]byte{
		"src/main.go":  []byte("package main\nfunc main() {}\n// TODO: fix bug"),
		"src/utils.go": []byte("package main\n// TODO: fix bug"),
		"README.md":    []byte("# DCA Sandbox Test"),
	}
	err := sb.BatchCreate(files)
	if err != nil {
		t.Fatalf("BatchCreate failed: %v", err)
	}

	// 2. SearchFiles (Grep)
	results, err := sb.SearchFiles("TODO", false)
	if err != nil {
		t.Fatalf("SearchFiles failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 search results for 'TODO', got %d", len(results))
	}

	// 3. SearchAndReplace
	replaced, err := sb.SearchAndReplace("TODO: fix bug", "DONE: fixed", false)
	if err != nil {
		t.Fatalf("SearchAndReplace failed: %v", err)
	}
	if replaced != 2 {
		t.Fatalf("expected 2 replacements, got %d", replaced)
	}

	// Verify replacement
	content, _ := sb.ReadFile("src/main.go")
	if !filepath.HasPrefix(string(content), "package main") || len(string(content)) == 0 {
		t.Fatalf("unexpected content: %s", string(content))
	}

	// 4. Copy & Move
	_ = sb.Copy("src/main.go", "backup/main.go.bak")
	existsCopy, _ := sb.Exists("backup/main.go.bak")
	if !existsCopy {
		t.Fatalf("expected copied file to exist")
	}

	_ = sb.Move("backup/main.go.bak", "moved/main.go.moved")
	existsOld, _ := sb.Exists("backup/main.go.bak")
	existsMoved, _ := sb.Exists("moved/main.go.moved")
	if existsOld || !existsMoved {
		t.Fatalf("Move operation failed")
	}

	// 5. BatchRemove
	_ = sb.BatchRemove([]string{"src/utils.go", "moved"})
	existsUtils, _ := sb.Exists("src/utils.go")
	if existsUtils {
		t.Fatalf("expected batch removed file to be deleted")
	}
}

func TestVFSSandboxManager_Lifecycle(t *testing.T) {
	mgr := NewVFSSandboxManager()

	sb1, _ := mgr.CreateMemSandbox("s1")
	sb2, _ := mgr.CreateMemSandbox("s2")
	if sb1 == nil || sb2 == nil {
		t.Fatalf("expected non-nil sandboxes")
	}

	if len(mgr.ListSandboxes()) != 2 {
		t.Fatalf("expected 2 sandboxes, got %d", len(mgr.ListSandboxes()))
	}

	_, found := mgr.GetSandbox("s1")
	if !found || sb1.ID != "s1" {
		t.Fatalf("failed to retrieve s1")
	}

	_ = mgr.RemoveSandbox("s2")
	if len(mgr.ListSandboxes()) != 1 {
		t.Fatalf("expected 1 sandbox after deletion")
	}

	_ = mgr.RemoveAll()
	if len(mgr.ListSandboxes()) != 0 {
		t.Fatalf("expected 0 sandboxes after RemoveAll")
	}
}
