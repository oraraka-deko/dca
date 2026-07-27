package utils

import (
	"fmt"
	"strings"
)

// TruncateOptions configures smart content truncation and tail filtering.
type TruncateOptions struct {
	MaxLines   int  `json:"max_lines"`
	MaxBytes   int  `json:"max_bytes"`
	TailOnly   bool `json:"tail_only"`
	TailLines  int  `json:"tail_lines"`
	Bypass     bool `json:"bypass"`
}

// DefaultTruncateOptions returns sensible defaults to prevent massive context windows.
func DefaultTruncateOptions() TruncateOptions {
	return TruncateOptions{
		MaxLines:  200,
		MaxBytes:  64 * 1024, // 64 KB
		TailOnly:  false,
		TailLines: 50,
		Bypass:    false,
	}
}

// SmartTruncate processes text content according to options, returning truncated content and a boolean flag.
func SmartTruncate(content string, opts TruncateOptions) (string, bool) {
	if opts.Bypass {
		return content, false
	}

	maxLines := opts.MaxLines
	maxBytes := opts.MaxBytes
	if maxLines <= 0 {
		maxLines = 200
	}
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}

	lines := strings.Split(content, "\n")
	totalLines := len(lines)
	wasTruncated := false

	var selectedLines []string

	// 1. Tail filtering if requested
	if opts.TailOnly {
		tailN := opts.TailLines
		if tailN <= 0 {
			tailN = 50
		}
		if totalLines > tailN {
			selectedLines = lines[totalLines-tailN:]
			wasTruncated = true
		} else {
			selectedLines = lines
		}
	} else {
		// Head filtering up to MaxLines
		if totalLines > maxLines {
			selectedLines = lines[:maxLines]
			wasTruncated = true
		} else {
			selectedLines = lines
		}
	}

	result := strings.Join(selectedLines, "\n")

	// 2. Byte limit enforcement
	if len(result) > maxBytes {
		result = result[:maxBytes]
		wasTruncated = true
	}

	if wasTruncated {
		result += fmt.Sprintf("\n\n[... Truncated output: showing subset of %d total lines (%d bytes). Set bypass=true or increase limits to view full content ...]", totalLines, len(content))
	}

	return result, wasTruncated
}

// TruncateTail returns only the last N lines of a response.
func TruncateTail(content string, lineCount int) string {
	if lineCount <= 0 {
		lineCount = 20
	}
	lines := strings.Split(content, "\n")
	if len(lines) <= lineCount {
		return content
	}
	tail := lines[len(lines)-lineCount:]
	return strings.Join(tail, "\n")
}
