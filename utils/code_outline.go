package utils

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

type SymbolInfo struct {
	Line int    `json:"line"`
	Type string `json:"type"`
	Text string `json:"text"`
}

func GetCodeOutline(filePath string) ([]SymbolInfo, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	var symbols []SymbolInfo
	scanner := bufio.NewScanner(file)
	lineNumber := 0

	reGoFunc := regexp.MustCompile(`^func\s+(\([^\)]+\)\s+)?([A-Za-z0-9_]+)`)
	reGoType := regexp.MustCompile(`^type\s+([A-Za-z0-9_]+)\s+(struct|interface)`)
	rePyDef := regexp.MustCompile(`^\s*(def|class)\s+([A-Za-z0-9_]+)`)
	reGenericFunc := regexp.MustCompile(`^\s*(async\s+)?function\s+([A-Za-z0-9_]+)`)

	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if matches := reGoFunc.FindStringSubmatch(line); len(matches) > 2 {
			symbols = append(symbols, SymbolInfo{Line: lineNumber, Type: "func", Text: line})
			continue
		}

		if matches := reGoType.FindStringSubmatch(line); len(matches) > 2 {
			symbols = append(symbols, SymbolInfo{Line: lineNumber, Type: "type", Text: line})
			continue
		}

		if matches := rePyDef.FindStringSubmatch(line); len(matches) > 2 {
			symbols = append(symbols, SymbolInfo{Line: lineNumber, Type: matches[1], Text: strings.TrimSpace(line)})
			continue
		}

		if matches := reGenericFunc.FindStringSubmatch(line); len(matches) > 2 {
			symbols = append(symbols, SymbolInfo{Line: lineNumber, Type: "function", Text: strings.TrimSpace(line)})
			continue
		}
	}

	return symbols, scanner.Err()
}
