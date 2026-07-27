package utils

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// TimerChainManager links timers, stopwatches, MultiHook, and TaskManager together.
type TimerChainManager struct {
	mu             sync.RWMutex
	timers         map[string]*time.Timer
	stopChans      map[string]chan struct{}
	taskMgr        *TaskManager
	hook           *MultiHook
	watchdogCtx    context.Context
	watchdogCancel context.CancelFunc
	watchdogWg     sync.WaitGroup
}

// NewTimerChainManager creates a new TimerChainManager instance.
func NewTimerChainManager(tm *TaskManager, hook *MultiHook) *TimerChainManager {
	return &TimerChainManager{
		timers:    make(map[string]*time.Timer),
		stopChans: make(map[string]chan struct{}),
		taskMgr:   tm,
		hook:      hook,
	}
}

// ScheduleTimerHook triggers a MultiHook string event after the specified duration.
func (tc *TimerChainManager) ScheduleTimerHook(id string, duration time.Duration, payload string) error {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	tc.cancelTimerLocked(id)

	t := time.AfterFunc(duration, func() {
		tc.mu.RLock()
		hk := tc.hook
		tc.mu.RUnlock()

		if hk != nil {
			hk.TriggerString(payload)
		}

		tc.mu.Lock()
		delete(tc.timers, id)
		tc.mu.Unlock()
	})

	tc.timers[id] = t
	return nil
}

// ScheduleTaskTimer enqueues a TaskManager task after a delay.
func (tc *TimerChainManager) ScheduleTaskTimer(id string, duration time.Duration, taskName string, fn TaskFunc) error {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	tc.cancelTimerLocked(id)

	t := time.AfterFunc(duration, func() {
		tc.mu.RLock()
		tm := tc.taskMgr
		tc.mu.RUnlock()

		if tm != nil {
			_, _ = tm.SubmitTask(taskName, fn)
		}

		tc.mu.Lock()
		delete(tc.timers, id)
		tc.mu.Unlock()
	})

	tc.timers[id] = t
	return nil
}

// ScheduleRecurringTask repeatedly enqueues a task at specified intervals until cancelled.
func (tc *TimerChainManager) ScheduleRecurringTask(id string, interval time.Duration, taskName string, fn TaskFunc) error {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	tc.cancelTimerLocked(id)

	stopChan := make(chan struct{})
	tc.stopChans[id] = stopChan

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-stopChan:
				return
			case <-ticker.C:
				tc.mu.RLock()
				tm := tc.taskMgr
				tc.mu.RUnlock()

				if tm != nil {
					_, _ = tm.SubmitTask(taskName, fn)
				}
			}
		}
	}()

	return nil
}

// StartWatchdog starts a background monitor that checks running tasks.
// If any task runs longer than maxTaskDuration, it is cancelled automatically to protect resources.
func (tc *TimerChainManager) StartWatchdog(checkInterval time.Duration, maxTaskDuration time.Duration) {
	tc.mu.Lock()
	if tc.watchdogCancel != nil {
		tc.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	tc.watchdogCtx = ctx
	tc.watchdogCancel = cancel
	tc.watchdogWg.Add(1)
	tc.mu.Unlock()

	go func() {
		defer tc.watchdogWg.Done()
		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				tc.checkRunningTasks(maxTaskDuration)
			}
		}
	}()
}

// StopWatchdog stops the task monitor.
func (tc *TimerChainManager) StopWatchdog() {
	tc.mu.Lock()
	if tc.watchdogCancel == nil {
		tc.mu.Unlock()
		return
	}
	tc.watchdogCancel()
	tc.watchdogCancel = nil
	tc.mu.Unlock()

	tc.watchdogWg.Wait()
}

func (tc *TimerChainManager) checkRunningTasks(maxTaskDuration time.Duration) {
	tc.mu.RLock()
	tm := tc.taskMgr
	hk := tc.hook
	tc.mu.RUnlock()

	if tm == nil {
		return
	}

	tasks := tm.ListTasks()
	now := time.Now()

	for _, task := range tasks {
		if task.Status == TaskStatusRunning && !task.StartedAt.IsZero() {
			if now.Sub(task.StartedAt) > maxTaskDuration {
				_ = tm.CancelTask(task.ID)
				alertMsg := fmt.Sprintf("WATCHDOG: Task [%s] '%s' exceeded max duration %v and was cancelled.", task.ID, task.Name, maxTaskDuration)
				if hk != nil {
					hk.TriggerString(alertMsg)
				}
			}
		}
	}
}

// CancelScheduledItem cancels a timer or recurring schedule by ID.
func (tc *TimerChainManager) CancelScheduledItem(id string) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.cancelTimerLocked(id)
}

func (tc *TimerChainManager) cancelTimerLocked(id string) {
	if t, exists := tc.timers[id]; exists {
		t.Stop()
		delete(tc.timers, id)
	}
	if ch, exists := tc.stopChans[id]; exists {
		close(ch)
		delete(tc.stopChans, id)
	}
}

// Close stops all timers, recurring schedules, and the watchdog.
func (tc *TimerChainManager) Close() {
	tc.StopWatchdog()

	tc.mu.Lock()
	defer tc.mu.Unlock()

	for id, t := range tc.timers {
		t.Stop()
		delete(tc.timers, id)
	}
	for id, ch := range tc.stopChans {
		close(ch)
		delete(tc.stopChans, id)
	}
}
