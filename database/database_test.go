package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteStore(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	store, err := NewStore("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed creating sqlite store: %v", err)
	}
	defer store.Close()

	if store.GetDriverName() != "sqlite" {
		t.Errorf("Expected driver sqlite, got %s", store.GetDriverName())
	}

	ctx := context.Background()

	// 1. Config Test
	if err := store.SaveConfig(ctx, "test_key", "test_val"); err != nil {
		t.Fatalf("Failed saving config: %v", err)
	}
	val, err := store.GetConfig(ctx, "test_key")
	if err != nil || val != "test_val" {
		t.Errorf("GetConfig expected 'test_val', got '%s', err: %v", val, err)
	}

	// 2. Log Test
	if err := store.InsertLog(ctx, "INFO", "test_comp", "hello log", `{"foo":"bar"}`); err != nil {
		t.Fatalf("Failed inserting log: %v", err)
	}
	logs, err := store.QueryLogs(ctx, LogFilter{Component: "test_comp"})
	if err != nil || len(logs) != 1 {
		t.Fatalf("QueryLogs expected 1 log, got %d, err: %v", len(logs), err)
	}
	if logs[0].Message != "hello log" || logs[0].Level != "INFO" {
		t.Errorf("Unexpected log content: %+v", logs[0])
	}

	// 3. Task Test
	taskRec := TaskRecord{
		ID:          "task-100",
		Name:        "Build Test",
		Status:      "Completed",
		Command:     "go test ./...",
		Result:      "PASS",
		Progress:    1.0,
		CreatedAt:   time.Now(),
		CompletedAt: time.Now(),
	}
	if err := store.SaveTask(ctx, taskRec); err != nil {
		t.Fatalf("Failed saving task: %v", err)
	}

	gotTask, exists, err := store.GetTask(ctx, "task-100")
	if err != nil || !exists {
		t.Fatalf("GetTask failed or not found: %v", err)
	}
	if gotTask.Name != "Build Test" || gotTask.Status != "Completed" {
		t.Errorf("GetTask mismatch: %+v", gotTask)
	}

	tasks, err := store.QueryTasks(ctx, TaskFilter{Status: "Completed"})
	if err != nil || len(tasks) != 1 {
		t.Errorf("QueryTasks failed or unexpected count: %d, err: %v", len(tasks), err)
	}
}
