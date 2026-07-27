package utils

import (
	"fmt"
	"strings"
	"testing"
)

func TestSmartTruncate_MaxLines(t *testing.T) {
	var lines []string
	for i := 1; i <= 300; i++ {
		lines = append(lines, fmt.Sprintf("Line %d", i))
	}
	content := strings.Join(lines, "\n")

	opts := DefaultTruncateOptions()
	opts.MaxLines = 50

	res, wasTruncated := SmartTruncate(content, opts)
	if !wasTruncated {
		t.Fatalf("expected content to be truncated")
	}

	if !strings.Contains(res, "Line 1") || strings.Contains(res, "Line 200") {
		t.Fatalf("unexpected truncated content: %s", res)
	}
}

func TestSmartTruncate_TailOnly(t *testing.T) {
	var lines []string
	for i := 1; i <= 100; i++ {
		lines = append(lines, fmt.Sprintf("Log entry %d", i))
	}
	content := strings.Join(lines, "\n")

	opts := DefaultTruncateOptions()
	opts.TailOnly = true
	opts.TailLines = 10

	res, wasTruncated := SmartTruncate(content, opts)
	if !wasTruncated {
		t.Fatalf("expected content to be truncated")
	}

	if !strings.Contains(res, "Log entry 100") || strings.Contains(res, "Log entry 1\n") {
		t.Fatalf("expected tail lines including 'Log entry 100' without head line 1, got: %s", res)
	}
}

func TestSmartTruncate_Bypass(t *testing.T) {
	var lines []string
	for i := 1; i <= 500; i++ {
		lines = append(lines, fmt.Sprintf("Line %d", i))
	}
	content := strings.Join(lines, "\n")

	opts := DefaultTruncateOptions()
	opts.Bypass = true

	res, wasTruncated := SmartTruncate(content, opts)
	if wasTruncated {
		t.Fatalf("expected bypass to prevent truncation")
	}
	if res != content {
		t.Fatalf("content was altered under bypass")
	}
}

func TestTruncateTail(t *testing.T) {
	content := "line1\nline2\nline3\nline4\nline5"
	tail := TruncateTail(content, 2)
	if tail != "line4\nline5" {
		t.Fatalf("expected 'line4\\nline5', got '%s'", tail)
	}
}
