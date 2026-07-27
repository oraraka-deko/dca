package utils

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestTimerChain_ScheduleTimerHook(t *testing.T) {
	hook := NewMultiHook()
	tm := NewTaskManager(1)
	tm.Start()
	defer tm.Stop()

	tcm := NewTimerChainManager(tm, hook)
	defer tcm.Close()

	var triggered int32
	hook.OnString(func(msg string) {
		if msg == "hook_payload" {
			atomic.StoreInt32(&triggered, 1)
		}
	})

	err := tcm.ScheduleTimerHook("timer1", 50*time.Millisecond, "hook_payload")
	if err != nil {
		t.Fatalf("failed to schedule timer hook: %v", err)
	}

	time.Sleep(120 * time.Millisecond)

	if atomic.LoadInt32(&triggered) != 1 {
		t.Fatalf("expected timer hook to trigger")
	}
}

func TestTimerChain_ScheduleTaskTimer(t *testing.T) {
	hook := NewMultiHook()
	tm := NewTaskManager(2)
	tm.Start()
	defer tm.Stop()

	tcm := NewTimerChainManager(tm, hook)
	defer tcm.Close()

	var taskExecuted int32
	err := tcm.ScheduleTaskTimer("task_timer1", 50*time.Millisecond, "delayed_task", func(ctx context.Context) (string, error) {
		atomic.StoreInt32(&taskExecuted, 1)
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("failed to schedule task timer: %v", err)
	}

	time.Sleep(150 * time.Millisecond)

	if atomic.LoadInt32(&taskExecuted) != 1 {
		t.Fatalf("expected delayed task to be executed")
	}
}

func TestTimerChain_ScheduleRecurringTask(t *testing.T) {
	hook := NewMultiHook()
	tm := NewTaskManager(2)
	tm.Start()
	defer tm.Stop()

	tcm := NewTimerChainManager(tm, hook)
	defer tcm.Close()

	var counter int32
	err := tcm.ScheduleRecurringTask("rec1", 30*time.Millisecond, "rec_task", func(ctx context.Context) (string, error) {
		atomic.AddInt32(&counter, 1)
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("failed to schedule recurring task: %v", err)
	}

	time.Sleep(110 * time.Millisecond)
	tcm.CancelScheduledItem("rec1")
	countBefore := atomic.LoadInt32(&counter)

	time.Sleep(80 * time.Millisecond)
	countAfter := atomic.LoadInt32(&counter)

	if countBefore == 0 {
		t.Fatalf("expected recurring task to run multiple times, got 0")
	}
	if countAfter > countBefore+1 {
		t.Fatalf("recurring task kept running after cancellation! before=%d, after=%d", countBefore, countAfter)
	}
}

func TestTimerChain_Watchdog(t *testing.T) {
	hook := NewMultiHook()
	tm := NewTaskManager(2)
	tm.Start()
	defer tm.Stop()

	tcm := NewTimerChainManager(tm, hook)
	defer tcm.Close()

	var watchdogTriggered int32
	hook.OnString(func(msg string) {
		if len(msg) > 8 && msg[:8] == "WATCHDOG" {
			atomic.StoreInt32(&watchdogTriggered, 1)
		}
	})

	// Start watchdog checking every 20ms for tasks running longer than 80ms
	tcm.StartWatchdog(20*time.Millisecond, 80*time.Millisecond)

	// Submit a task that sleeps 500ms
	task, err := tm.SubmitTask("runaway_task", func(ctx context.Context) (string, error) {
		select {
		case <-time.After(500 * time.Millisecond):
			return "done", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	if err != nil {
		t.Fatalf("failed to submit task: %v", err)
	}

	time.Sleep(250 * time.Millisecond)

	snap, ok := tm.GetTask(task.ID)
	if !ok {
		t.Fatalf("task not found")
	}

	if snap.Status != TaskStatusCancelled {
		t.Fatalf("expected runaway task to be cancelled by Watchdog, status is %s", snap.Status)
	}
	if atomic.LoadInt32(&watchdogTriggered) != 1 {
		t.Fatalf("expected Watchdog alert hook to trigger")
	}
}
