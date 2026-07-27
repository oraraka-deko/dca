package utils

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestTaskManager_Lifecycle(t *testing.T) {
	tm := NewTaskManager(2)
	tm.Start()
	defer tm.Stop()

	task, err := tm.SubmitTask("simple_task", func(ctx context.Context) (string, error) {
		return "hello world", nil
	})
	if err != nil {
		t.Fatalf("failed to submit task: %v", err)
	}

	// Wait for task completion
	var snap TaskSnapshot
	for i := 0; i < 50; i++ {
		s, ok := tm.GetTask(task.ID)
		if ok && s.Status == TaskStatusCompleted {
			snap = s
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if snap.Status != TaskStatusCompleted {
		t.Fatalf("expected status Completed, got %s", snap.Status)
	}
	if snap.Result != "hello world" {
		t.Fatalf("expected result 'hello world', got '%s'", snap.Result)
	}
}

func TestTaskManager_CommandExecution(t *testing.T) {
	tm := NewTaskManager(2)
	tm.Start()
	defer tm.Stop()

	cmdName := "echo"
	args := []string{"hello", "task"}
	if runtime.GOOS == "windows" {
		cmdName = "cmd"
		args = []string{"/c", "echo", "hello", "task"}
	}

	task, err := tm.SubmitCommand("echo_cmd", false, cmdName, args...)
	if err != nil {
		t.Fatalf("failed to submit command: %v", err)
	}

	var snap TaskSnapshot
	for i := 0; i < 50; i++ {
		s, ok := tm.GetTask(task.ID)
		if ok && s.Status == TaskStatusCompleted {
			snap = s
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if snap.Status != TaskStatusCompleted {
		t.Fatalf("expected status Completed, got %s (err: %s)", snap.Status, snap.Error)
	}
}

func TestTaskManager_TaskCancellation(t *testing.T) {
	tm := NewTaskManager(2)
	tm.Start()
	defer tm.Stop()

	task, err := tm.SubmitTask("slow_task", func(ctx context.Context) (string, error) {
		select {
		case <-time.After(2 * time.Second):
			return "done", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	if err != nil {
		t.Fatalf("failed to submit task: %v", err)
	}

	// Wait until running
	time.Sleep(50 * time.Millisecond)

	if err := tm.CancelTask(task.ID); err != nil {
		t.Fatalf("failed to cancel task: %v", err)
	}

	s, ok := tm.GetTask(task.ID)
	if !ok {
		t.Fatalf("task not found")
	}
	if s.Status != TaskStatusCancelled {
		t.Fatalf("expected status Cancelled, got %s", s.Status)
	}
}

func TestTaskManager_Concurrency(t *testing.T) {
	concurrency := 2
	tm := NewTaskManager(concurrency)
	tm.Start()
	defer tm.Stop()

	var activeCount int32
	var maxActive int32

	taskCount := 6
	for i := 0; i < taskCount; i++ {
		_, err := tm.SubmitTask("concurrent_task", func(ctx context.Context) (string, error) {
			curr := atomic.AddInt32(&activeCount, 1)
			for {
				m := atomic.LoadInt32(&maxActive)
				if curr <= m || atomic.CompareAndSwapInt32(&maxActive, m, curr) {
					break
				}
			}
			time.Sleep(100 * time.Millisecond)
			atomic.AddInt32(&activeCount, -1)
			return "ok", nil
		})
		if err != nil {
			t.Fatalf("failed submitting task: %v", err)
		}
	}

	// Wait for all to complete
	for i := 0; i < 100; i++ {
		tasks := tm.ListTasks()
		completed := 0
		for _, task := range tasks {
			if task.Status == TaskStatusCompleted {
				completed++
			}
		}
		if completed == taskCount {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	if atomic.LoadInt32(&maxActive) > int32(concurrency) {
		t.Fatalf("max active tasks %d exceeded concurrency limit %d", maxActive, concurrency)
	}
}

func TestTaskManager_MultiHook(t *testing.T) {
	tm := NewTaskManager(1)
	hook := NewMultiHook()
	tm.SetHook(hook)

	var hookTriggered int32
	hook.OnString(func(msg string) {
		atomic.AddInt32(&hookTriggered, 1)
	})

	tm.Start()
	defer tm.Stop()

	task, err := tm.SubmitTask("hook_task", func(ctx context.Context) (string, error) {
		return "done", nil
	})
	if err != nil {
		t.Fatalf("failed submitting task: %v", err)
	}

	for i := 0; i < 50; i++ {
		s, _ := tm.GetTask(task.ID)
		if s.Status == TaskStatusCompleted {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	time.Sleep(50 * time.Millisecond)

	if atomic.LoadInt32(&hookTriggered) == 0 {
		t.Fatalf("expected hook to be triggered at least once")
	}
}

func TestTaskManager_ClearCompleted(t *testing.T) {
	tm := NewTaskManager(2)
	tm.Start()
	defer tm.Stop()

	t1, _ := tm.SubmitTask("t1", func(ctx context.Context) (string, error) { return "ok", nil })
	t2, _ := tm.SubmitTask("t2", func(ctx context.Context) (string, error) { return "", errors.New("fail") })

	for i := 0; i < 50; i++ {
		s1, _ := tm.GetTask(t1.ID)
		s2, _ := tm.GetTask(t2.ID)
		if s1.Status == TaskStatusCompleted && s2.Status == TaskStatusFailed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	cleared := tm.ClearCompleted()
	if cleared != 2 {
		t.Fatalf("expected 2 tasks cleared, got %d", cleared)
	}

	if len(tm.ListTasks()) != 0 {
		t.Fatalf("expected 0 tasks remaining, got %d", len(tm.ListTasks()))
	}
}
