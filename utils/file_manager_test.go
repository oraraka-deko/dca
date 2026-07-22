package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileManager_BasicOperations(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "filemanager-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	fm, err := NewFileManager(tempDir)
	if err != nil {
		t.Fatalf("failed to create FileManager: %v", err)
	}

	// 1. Test Mkdir & CreateFile
	if err := fm.Mkdir("dir1"); err != nil {
		t.Errorf("failed to Mkdir: %v", err)
	}
	if err := fm.CreateFile("file1.txt"); err != nil {
		t.Errorf("failed to CreateFile: %v", err)
	}
	if err := fm.CreateFile("file2.log"); err != nil {
		t.Errorf("failed to CreateFile: %v", err)
	}

	// Write some text to file1.txt for preview testing
	err = os.WriteFile(filepath.Join(tempDir, "file1.txt"), []byte("line1\nline2\nline3"), 0644)
	if err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Write binary data to file2.log for binary preview testing
	err = os.WriteFile(filepath.Join(tempDir, "file2.log"), []byte{0x00, 0x01, 0x02, 0x03}, 0644)
	if err != nil {
		t.Fatalf("failed to write binary file: %v", err)
	}

	// 2. Test List & Sorting (Directory first, then Name)
	files, err := fm.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(files) != 3 {
		t.Errorf("expected 3 files, got %d", len(files))
	}
	// Verify directory comes first
	if !files[0].IsDir || files[0].Name != "dir1" {
		t.Errorf("expected directory 'dir1' first, got %s (dir=%v)", files[0].Name, files[0].IsDir)
	}

	// 3. Test Navigation and History
	err = fm.GoTo(filepath.Join(tempDir, "dir1"))
	if err != nil {
		t.Errorf("GoTo failed: %v", err)
	}
	if fm.CurrentPath != filepath.Join(tempDir, "dir1") {
		t.Errorf("expected path dir1, got %s", fm.CurrentPath)
	}
	if len(fm.BackHistory) != 1 || fm.BackHistory[0] != tempDir {
		t.Errorf("expected back history [tempDir], got %v", fm.BackHistory)
	}

	err = fm.GoBack()
	if err != nil {
		t.Errorf("GoBack failed: %v", err)
	}
	if fm.CurrentPath != tempDir {
		t.Errorf("expected current path to be tempDir after GoBack, got %s", fm.CurrentPath)
	}
	if len(fm.ForwardHistory) != 1 || fm.ForwardHistory[0] != filepath.Join(tempDir, "dir1") {
		t.Errorf("expected forward history [dir1], got %v", fm.ForwardHistory)
	}

	err = fm.GoForward()
	if err != nil {
		t.Errorf("GoForward failed: %v", err)
	}
	if fm.CurrentPath != filepath.Join(tempDir, "dir1") {
		t.Errorf("expected path dir1 after GoForward, got %s", fm.CurrentPath)
	}

	err = fm.GoUp()
	if err != nil {
		t.Errorf("GoUp failed: %v", err)
	}
	if fm.CurrentPath != tempDir {
		t.Errorf("expected path tempDir after GoUp, got %s", fm.CurrentPath)
	}
}

func TestFileManager_ClipboardAndFileOps(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "filemanager-clip-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	fm, err := NewFileManager(tempDir)
	if err != nil {
		t.Fatalf("failed to create FileManager: %v", err)
	}

	srcFile := filepath.Join(tempDir, "source.txt")
	err = os.WriteFile(srcFile, []byte("clip content"), 0644)
	if err != nil {
		t.Fatalf("failed to create source file: %v", err)
	}

	destSubDir := filepath.Join(tempDir, "dest_dir")
	err = os.Mkdir(destSubDir, 0755)
	if err != nil {
		t.Fatalf("failed to create dest dir: %v", err)
	}

	// Test Copy & Paste
	fm.Copy([]string{srcFile})
	err = fm.GoTo(destSubDir)
	if err != nil {
		t.Fatalf("GoTo failed: %v", err)
	}

	err = fm.Paste()
	if err != nil {
		t.Fatalf("Paste failed: %v", err)
	}

	pastedFile := filepath.Join(destSubDir, "source.txt")
	if _, err := os.Stat(pastedFile); os.IsNotExist(err) {
		t.Errorf("expected pasted file to exist at %s", pastedFile)
	}

	// Test Rename
	err = fm.Rename(pastedFile, "renamed.txt")
	if err != nil {
		t.Fatalf("Rename failed: %v", err)
	}
	renamedFile := filepath.Join(destSubDir, "renamed.txt")
	if _, err := os.Stat(renamedFile); os.IsNotExist(err) {
		t.Errorf("expected renamed file to exist at %s", renamedFile)
	}

	// Test Delete
	err = fm.Delete([]string{renamedFile})
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if _, err := os.Stat(renamedFile); !os.IsNotExist(err) {
		t.Errorf("expected deleted file to be removed")
	}
}

func TestFileManager_BookmarksSearchPreview(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "filemanager-bsp-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	fm, err := NewFileManager(tempDir)
	if err != nil {
		t.Fatalf("failed to create FileManager: %v", err)
	}

	// 1. Test Bookmarks
	fm.AddBookmark(tempDir)
	bms := fm.GetBookmarks()
	if len(bms) != 1 || bms[0] != tempDir {
		t.Errorf("expected bookmark tempDir, got %v", bms)
	}
	fm.RemoveBookmark(tempDir)
	if len(fm.GetBookmarks()) != 0 {
		t.Errorf("expected bookmarks to be empty")
	}

	// Create a nested file hierarchy
	subDir := filepath.Join(tempDir, "sub")
	os.Mkdir(subDir, 0755)
	targetFile := filepath.Join(subDir, "target_match.txt")
	os.WriteFile(targetFile, []byte("text content"), 0644)

	// 2. Test Search
	results, err := fm.Search("match")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 || results[0].Name != "target_match.txt" {
		t.Errorf("expected to find target_match.txt, got %v", results)
	}

	// 3. Test Previews
	// Folder preview
	pType, pText, err := fm.GetPreview(subDir, 5)
	if err != nil {
		t.Fatalf("GetPreview on folder failed: %v", err)
	}
	if pType != "directory" || !strings.Contains(pText, "target_match.txt") {
		t.Errorf("expected directory preview type containing target_match.txt, got %s: %q", pType, pText)
	}

	// Text file preview
	pType, pText, err = fm.GetPreview(targetFile, 5)
	if err != nil {
		t.Fatalf("GetPreview on file failed: %v", err)
	}
	if pType != "text" || pText != "text content" {
		t.Errorf("expected text preview 'text content', got %s: %q", pType, pText)
	}
}
