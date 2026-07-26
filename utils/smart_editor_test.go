package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupEditFile(t *testing.T) (string, func()) {
	t.Helper()
	tempDir, err := os.MkdirTemp("", "smart_editor_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	cleanup := func() { os.RemoveAll(tempDir) }
	return tempDir, cleanup
}

func writeEditTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
	return path
}

func readEditTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read test file: %v", err)
	}
	return string(data)
}

func TestApplySmartEdit_ExactSearchReplace(t *testing.T) {
	dir, cleanup := setupEditFile(t)
	defer cleanup()

	path := writeEditTestFile(t, dir, "test.go", "line0\nline1\nline2\nline3\n")

	resp, err := ApplySmartEdit(SmartEditRequest{
		FilePath:    path,
		Mode:        ModeExactSearchReplace,
		SearchBlock: "line1\nline2",
		ReplaceWith: "new1\nnew2\nnew3",
	})

	if err != nil {
		t.Fatalf("ApplySmartEdit failed: %v", err)
	}
	if !resp.Success {
		t.Error("expected success")
	}
	if resp.LinesChanged != 5 {
		t.Errorf("expected 5 lines changed (2 old + 3 new), got %d", resp.LinesChanged)
	}

	content := readEditTestFile(t, path)
	expected := "line0\nnew1\nnew2\nnew3\nline3\n"
	if content != expected {
		t.Errorf("content mismatch:\nexpected: %q\ngot:      %q", expected, content)
	}
}

func TestApplySmartEdit_ExactSearchReplace_NotFound(t *testing.T) {
	dir, cleanup := setupEditFile(t)
	defer cleanup()

	path := writeEditTestFile(t, dir, "test.go", "line0\nline1\n")

	_, err := ApplySmartEdit(SmartEditRequest{
		FilePath:    path,
		Mode:        ModeExactSearchReplace,
		SearchBlock: "nonexistent",
		ReplaceWith: "replacement",
	})

	if err == nil {
		t.Error("expected error for missing search_block")
	}
}

func TestApplySmartEdit_ExactSearchReplace_Duplicate(t *testing.T) {
	dir, cleanup := setupEditFile(t)
	defer cleanup()

	path := writeEditTestFile(t, dir, "test.go", "dup\nother\ndup\n")

	_, err := ApplySmartEdit(SmartEditRequest{
		FilePath:    path,
		Mode:        ModeExactSearchReplace,
		SearchBlock: "dup",
		ReplaceWith: "replacement",
	})

	if err == nil {
		t.Error("expected error for duplicate search_block")
	}
	if !strings.Contains(err.Error(), "found 2 times") {
		t.Errorf("expected duplicate error, got: %v", err)
	}
}

func TestApplySmartEdit_ScopedReplace(t *testing.T) {
	dir, cleanup := setupEditFile(t)
	defer cleanup()

	path := writeEditTestFile(t, dir, "test.go",
		"line0\nline1\nline2\nline3\nline4\nline5\n")

	resp, err := ApplySmartEdit(SmartEditRequest{
		FilePath:    path,
		Mode:        ModeScopedReplace,
		StartLine:   2,
		EndLine:     4,
		SearchBlock: "line1\nline2",
		ReplaceWith: "aaa\nbbb",
	})

	if err != nil {
		t.Fatalf("ApplySmartEdit failed: %v", err)
	}
	if !resp.Success {
		t.Error("expected success")
	}

	content := readEditTestFile(t, path)
	expected := "line0\naaa\nbbb\nline3\nline4\nline5\n"
	if content != expected {
		t.Errorf("content mismatch:\nexpected: %q\ngot:      %q", expected, content)
	}
}

func TestApplySmartEdit_ScopedReplace_SearchNotFoundInScope(t *testing.T) {
	dir, cleanup := setupEditFile(t)
	defer cleanup()

	path := writeEditTestFile(t, dir, "test.go",
		"line0\nline1\nline2\nline3\n")

	_, err := ApplySmartEdit(SmartEditRequest{
		FilePath:    path,
		Mode:        ModeScopedReplace,
		StartLine:   2,
		EndLine:     2,
		SearchBlock: "line3",
		ReplaceWith: "new",
	})

	if err == nil {
		t.Error("expected error for search_block outside scope")
	}
}

func TestApplySmartEdit_ScopedReplace_OutOfBounds(t *testing.T) {
	dir, cleanup := setupEditFile(t)
	defer cleanup()

	path := writeEditTestFile(t, dir, "test.go", "line0\nline1\n")

	_, err := ApplySmartEdit(SmartEditRequest{
		FilePath:    path,
		Mode:        ModeScopedReplace,
		StartLine:   1,
		EndLine:     10,
		SearchBlock: "line0",
		ReplaceWith: "new",
	})

	if err == nil {
		t.Error("expected error for out-of-bounds line range")
	}
}

func TestApplySmartEdit_LineReplace(t *testing.T) {
	dir, cleanup := setupEditFile(t)
	defer cleanup()

	path := writeEditTestFile(t, dir, "test.go", "line0\nline1\nline2\nline3\nline4\n")

	resp, err := ApplySmartEdit(SmartEditRequest{
		FilePath:    path,
		Mode:        ModeLineReplace,
		StartLine:   2,
		EndLine:     4,
		ReplaceWith: "aaa\nbbb",
	})

	if err != nil {
		t.Fatalf("ApplySmartEdit failed: %v", err)
	}
	if !resp.Success {
		t.Error("expected success")
	}
	if resp.LinesChanged != 3 {
		t.Errorf("expected 3 lines changed, got %d", resp.LinesChanged)
	}

	content := readEditTestFile(t, path)
	expected := "line0\naaa\nbbb\nline4\n"
	if content != expected {
		t.Errorf("content mismatch:\nexpected: %q\ngot:      %q", expected, content)
	}
}

func TestApplySmartEdit_LineReplace_Append(t *testing.T) {
	dir, cleanup := setupEditFile(t)
	defer cleanup()

	path := writeEditTestFile(t, dir, "test.go", "line0\nline1\n")

	resp, err := ApplySmartEdit(SmartEditRequest{
		FilePath:    path,
		Mode:        ModeLineReplace,
		StartLine:   3,
		EndLine:     2,
		ReplaceWith: "line2",
	})

	if err != nil {
		t.Fatalf("ApplySmartEdit failed: %v", err)
	}
	if !resp.Success {
		t.Error("expected success")
	}

	content := readEditTestFile(t, path)
	expected := "line0\nline1\nline2\n"
	if content != expected {
		t.Errorf("content mismatch:\nexpected: %q\ngot:      %q", expected, content)
	}
}

func TestApplySmartEdit_LineReplace_Delete(t *testing.T) {
	dir, cleanup := setupEditFile(t)
	defer cleanup()

	path := writeEditTestFile(t, dir, "test.go", "line0\nline1\nline2\n")

	resp, err := ApplySmartEdit(SmartEditRequest{
		FilePath:    path,
		Mode:        ModeLineReplace,
		StartLine:   2,
		EndLine:     2,
		ReplaceWith: "",
	})

	if err != nil {
		t.Fatalf("ApplySmartEdit failed: %v", err)
	}
	if !resp.Success {
		t.Error("expected success")
	}

	content := readEditTestFile(t, path)
	expected := "line0\nline2\n"
	if content != expected {
		t.Errorf("content mismatch:\nexpected: %q\ngot:      %q", expected, content)
	}
}

func TestApplySmartEdit_LineReplace_Prepend(t *testing.T) {
	dir, cleanup := setupEditFile(t)
	defer cleanup()

	path := writeEditTestFile(t, dir, "test.go", "line0\nline1\n")

	resp, err := ApplySmartEdit(SmartEditRequest{
		FilePath:    path,
		Mode:        ModeLineReplace,
		StartLine:   1,
		EndLine:     0,
		ReplaceWith: "header",
	})

	if err != nil {
		t.Fatalf("ApplySmartEdit failed: %v", err)
	}
	if !resp.Success {
		t.Error("expected success")
	}

	content := readEditTestFile(t, path)
	expected := "header\nline0\nline1\n"
	if content != expected {
		t.Errorf("content mismatch:\nexpected: %q\ngot:      %q", expected, content)
	}
}

func TestApplySmartEdit_InvalidMode(t *testing.T) {
	dir, cleanup := setupEditFile(t)
	defer cleanup()

	path := writeEditTestFile(t, dir, "test.go", "content\n")

	_, err := ApplySmartEdit(SmartEditRequest{
		FilePath: path,
		Mode:     "invalid_mode",
	})

	if err == nil {
		t.Error("expected error for unsupported mode")
	}
}

func TestApplySmartEdit_NonexistentFile(t *testing.T) {
	_, err := ApplySmartEdit(SmartEditRequest{
		FilePath: "nonexistent.txt",
		Mode:     ModeExactSearchReplace,
	})

	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}
