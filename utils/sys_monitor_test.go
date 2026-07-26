package utils

import (
	"fmt"
	"os"
	"testing"
)

func TestListProcesses(t *testing.T) {
	list, err := ListProcesses("", "pid", 5)
	if err != nil {
		t.Fatalf("failed to list processes: %v", err)
	}

	if len(list) == 0 {
		t.Log("no processes found (could be expected in some constrained test environments)")
		return
	}

	if len(list) > 5 {
		t.Errorf("expected at most 5 processes, got %d", len(list))
	}

	for i := 1; i < len(list); i++ {
		if list[i].PID < list[i-1].PID {
			t.Errorf("expected sorted by PID ascending, but got PID %d after %d", list[i].PID, list[i-1].PID)
		}
	}
}

func TestListProcesses_Filtering(t *testing.T) {
	pid := os.Getpid()
	list, err := ListProcesses(fmt.Sprintf("%d", pid), "pid", 1)
	if err != nil {
		t.Fatalf("failed to list processes: %v", err)
	}

	if len(list) == 0 {
		t.Logf("no matching process found for PID %d", pid)
	}
}

func TestMatchService(t *testing.T) {
	s := ServiceInfo{
		Name:        "Spooler",
		DisplayName: "Print Spooler",
		Status:      "RUNNING",
	}

	if !matchService(s, "") {
		t.Error("empty query should match all services")
	}
	if !matchService(s, "spooler") {
		t.Error("query 'spooler' should match Name 'Spooler'")
	}
	if !matchService(s, "print") {
		t.Error("query 'print' should match DisplayName 'Print Spooler'")
	}
	if !matchService(s, "running") {
		t.Error("query 'running' should match Status 'RUNNING'")
	}
	if matchService(s, "nonexistent") {
		t.Error("query 'nonexistent' should not match")
	}
}

func TestMatchTask(t *testing.T) {
	task := ScheduledTaskInfo{
		TaskName:  `\Microsoft\Windows\WindowsUpdate\Scheduled Start`,
		TaskToRun: "C:\\Windows\\system32\\usoclient.exe StartScan",
		Status:    "Ready",
	}

	if !matchTask(task, "") {
		t.Error("empty query should match all tasks")
	}
	if !matchTask(task, "update") {
		t.Error("query 'update' should match TaskName")
	}
	if !matchTask(task, "usoclient") {
		t.Error("query 'usoclient' should match TaskToRun")
	}
	if !matchTask(task, "ready") {
		t.Error("query 'ready' should match Status")
	}
	if matchTask(task, "nonexistent") {
		t.Error("query 'nonexistent' should not match")
	}
}

func TestSignalProcess_Invalid(t *testing.T) {
	err := SignalProcess(999999, "invalid-signal")
	if err == nil {
		t.Error("expected error for invalid signal action, got nil")
	}
}
