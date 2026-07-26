package utils

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetCodeOutline_GoFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "code_outline_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	src := filepath.Join(tempDir, "sample.go")
	content := `package main

import "fmt"

type Config struct {
	Name string
}

func NewConfig() *Config {
	return &Config{}
}

func (c *Config) Validate() error {
	return nil
}
`
	if err := os.WriteFile(src, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	symbols, err := GetCodeOutline(src)
	if err != nil {
		t.Fatalf("GetCodeOutline failed: %v", err)
	}

	if len(symbols) != 3 {
		t.Fatalf("expected 3 symbols, got %d", len(symbols))
	}

	sym0 := symbols[0]
	if sym0.Type != "type" {
		t.Errorf("symbol 0: expected type 'type', got '%s'", sym0.Type)
	}
	if sym0.Line != 5 {
		t.Errorf("symbol 0: expected line 5, got %d", sym0.Line)
	}

	sym1 := symbols[1]
	if sym1.Type != "func" {
		t.Errorf("symbol 1: expected type 'func', got '%s'", sym1.Type)
	}
	if sym1.Line != 9 {
		t.Errorf("symbol 1: expected line 9, got %d", sym1.Line)
	}

	sym2 := symbols[2]
	if sym2.Type != "func" {
		t.Errorf("symbol 2: expected type 'func', got '%s'", sym2.Type)
	}
}

func TestGetCodeOutline_EmptyFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "code_outline_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	src := filepath.Join(tempDir, "empty.go")
	if err := os.WriteFile(src, []byte(""), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	symbols, err := GetCodeOutline(src)
	if err != nil {
		t.Fatalf("GetCodeOutline failed: %v", err)
	}

	if len(symbols) != 0 {
		t.Errorf("expected 0 symbols, got %d", len(symbols))
	}
}

func TestGetCodeOutline_SkipsComments(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "code_outline_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	src := filepath.Join(tempDir, "commented.go")
	content := `package main

// func Skipped() {}
func RealFunction() {}

// type SkippedType struct{}
type RealType struct {
	X int
}
`
	if err := os.WriteFile(src, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	symbols, err := GetCodeOutline(src)
	if err != nil {
		t.Fatalf("GetCodeOutline failed: %v", err)
	}

	if len(symbols) != 2 {
		t.Fatalf("expected 2 symbols, got %d", len(symbols))
	}
}

func TestGetCodeOutline_NonexistentFile(t *testing.T) {
	_, err := GetCodeOutline("nonexistent_file.go")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}
