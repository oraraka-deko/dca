package utils

import (
	"fmt"
	"os"
	"strings"
)

type EditMode string

const (
	ModeExactSearchReplace EditMode = "search_replace"
	ModeScopedReplace      EditMode = "scoped_replace"
	ModeLineReplace        EditMode = "line_replace"
)

type SmartEditRequest struct {
	FilePath    string   `json:"file_path"`
	Mode        EditMode `json:"mode"`
	SearchBlock string   `json:"search_block,omitempty"`
	ReplaceWith string   `json:"replace_with"`
	StartLine   int      `json:"start_line,omitempty"`
	EndLine     int      `json:"end_line,omitempty"`
}

type EditResponse struct {
	Success      bool   `json:"success"`
	FilePath     string `json:"file_path"`
	LinesChanged int    `json:"lines_changed"`
	Message      string `json:"message"`
}

func ApplySmartEdit(req SmartEditRequest) (*EditResponse, error) {
	data, err := os.ReadFile(req.FilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", req.FilePath, err)
	}

	content := string(data)
	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	switch req.Mode {

	case ModeExactSearchReplace:
		if req.SearchBlock == "" {
			return nil, fmt.Errorf("search_block cannot be empty in search_replace mode")
		}

		count := strings.Count(content, req.SearchBlock)
		if count == 0 {
			return nil, fmt.Errorf("search_block not found in %s", req.FilePath)
		}
		if count > 1 {
			return nil, fmt.Errorf("search_block found %d times; use scoped_replace with line numbers", count)
		}

		newContent := strings.Replace(content, req.SearchBlock, req.ReplaceWith, 1)
		if err := os.WriteFile(req.FilePath, []byte(newContent), 0644); err != nil {
			return nil, err
		}

		oldLinesCount := len(strings.Split(req.SearchBlock, "\n"))
		newLinesCount := len(strings.Split(req.ReplaceWith, "\n"))
		return &EditResponse{
			Success:      true,
			FilePath:     req.FilePath,
			LinesChanged: oldLinesCount + newLinesCount,
			Message:      fmt.Sprintf("OK: replaced unique block (-%d +%d lines)", oldLinesCount, newLinesCount),
		}, nil

	case ModeScopedReplace:
		if req.StartLine < 1 || req.EndLine > totalLines || req.StartLine > req.EndLine {
			return nil, fmt.Errorf("line range [%d-%d] out of bounds (1-%d)", req.StartLine, req.EndLine, totalLines)
		}

		scopeSlice := lines[req.StartLine-1 : req.EndLine]
		scopeText := strings.Join(scopeSlice, "\n")

		if !strings.Contains(scopeText, req.SearchBlock) {
			return nil, fmt.Errorf("search_block not found within lines %d-%d", req.StartLine, req.EndLine)
		}

		updatedScope := strings.Replace(scopeText, req.SearchBlock, req.ReplaceWith, 1)

		var resultLines []string
		resultLines = append(resultLines, lines[:req.StartLine-1]...)
		resultLines = append(resultLines, strings.Split(updatedScope, "\n")...)
		resultLines = append(resultLines, lines[req.EndLine:]...)

		newContent := strings.Join(resultLines, "\n")
		if err := os.WriteFile(req.FilePath, []byte(newContent), 0644); err != nil {
			return nil, err
		}

		return &EditResponse{
			Success:      true,
			FilePath:     req.FilePath,
			LinesChanged: len(resultLines) - totalLines,
			Message:      fmt.Sprintf("OK: scoped edit applied within lines %d-%d", req.StartLine, req.EndLine),
		}, nil

	case ModeLineReplace:
		if req.StartLine < 1 || req.StartLine > totalLines+1 {
			return nil, fmt.Errorf("start_line %d out of bounds (1-%d)", req.StartLine, totalLines)
		}
		if req.EndLine < req.StartLine-1 || req.EndLine > totalLines {
			return nil, fmt.Errorf("end_line %d out of bounds", req.EndLine)
		}

		var newLines []string
		if req.StartLine > 1 {
			newLines = append(newLines, lines[:req.StartLine-1]...)
		}
		if req.ReplaceWith != "" {
			newLines = append(newLines, strings.Split(req.ReplaceWith, "\n")...)
		}
		if req.EndLine < totalLines {
			newLines = append(newLines, lines[req.EndLine:]...)
		}

		newContent := strings.Join(newLines, "\n")
		if err := os.WriteFile(req.FilePath, []byte(newContent), 0644); err != nil {
			return nil, err
		}

		return &EditResponse{
			Success:      true,
			FilePath:     req.FilePath,
			LinesChanged: req.EndLine - req.StartLine + 1,
			Message:      fmt.Sprintf("OK: line range %d-%d replaced", req.StartLine, req.EndLine),
		}, nil

	default:
		return nil, fmt.Errorf("unsupported mode: %s", req.Mode)
	}
}
