package utils

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// TaskStatus represents the current state of a task in the queue.
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "Pending"
	TaskStatusRunning   TaskStatus = "Running"
	TaskStatusCompleted TaskStatus = "Completed"
	TaskStatusFailed    TaskStatus = "Failed"
	TaskStatusCancelled TaskStatus = "Cancelled"
)

// TaskFunc is a closure executed by the task worker.
type TaskFunc func(ctx context.Context) (string, error)

// TaskSnapshot is a thread-safe snapshot view of a Task's status and details.
type TaskSnapshot struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Status      TaskStatus `json:"status"`
	Result      string     `json:"result"`
	Error       string     `json:"error"`
	Progress    float64    `json:"progress"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt time.Time  `json:"completed_at"`
}

// Task represents an executable background item in the TaskManager queue.
type Task struct {
	mu          sync.RWMutex
	ID          string
	Name        string
	Status      TaskStatus
	Result      string
	Error       string
	Progress    float64
	CreatedAt   time.Time
	StartedAt   time.Time
	CompletedAt time.Time

	fn         TaskFunc
	ctx        context.Context
	cancelFunc context.CancelFunc
}

// Snapshot returns a copy of the task state.
func (t *Task) Snapshot() TaskSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return TaskSnapshot{
		ID:          t.ID,
		Name:        t.Name,
		Status:      t.Status,
		Result:      t.Result,
		Error:       t.Error,
		Progress:    t.Progress,
		CreatedAt:   t.CreatedAt,
		StartedAt:   t.StartedAt,
		CompletedAt: t.CompletedAt,
	}
}

// SetProgress updates task execution progress (0.0 to 1.0).
func (t *Task) SetProgress(val float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if val < 0.0 {
		val = 0.0
	}
	if val > 1.0 {
		val = 1.0
	}
	t.Progress = val
}

// TaskManager manages queued background tasks with controlled concurrency.
type TaskManager struct {
	mu          sync.RWMutex
	tasks       map[string]*Task
	queue       chan *Task
	concurrency int
	hook        *MultiHook

	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	running bool
}

// NewTaskManager creates a new TaskManager instance with specified worker concurrency.
func NewTaskManager(concurrency int) *TaskManager {
	if concurrency <= 0 {
		concurrency = 4
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &TaskManager{
		tasks:       make(map[string]*Task),
		queue:       make(chan *Task, 1000),
		concurrency: concurrency,
		ctx:         ctx,
		cancel:      cancel,
	}
}

// SetHook attaches a MultiHook instance for event triggers.
func (tm *TaskManager) SetHook(h *MultiHook) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.hook = h
}

// Start launches worker goroutines to process the queue.
func (tm *TaskManager) Start() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if tm.running {
		return
	}
	tm.running = true

	for i := 0; i < tm.concurrency; i++ {
		tm.wg.Add(1)
		go tm.worker()
	}
}

// Stop gracefully shuts down workers and cancels pending/running tasks.
func (tm *TaskManager) Stop() {
	tm.mu.Lock()
	if !tm.running {
		tm.mu.Unlock()
		return
	}
	tm.running = false
	tm.cancel()
	close(tm.queue)
	tm.mu.Unlock()

	tm.wg.Wait()
}

func generateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// SubmitTask enqueues a closure task for execution.
func (tm *TaskManager) SubmitTask(name string, fn TaskFunc) (*Task, error) {
	tm.mu.Lock()
	if !tm.running {
		tm.mu.Unlock()
		return nil, errors.New("task manager is not running")
	}

	taskCtx, taskCancel := context.WithCancel(tm.ctx)
	task := &Task{
		ID:         generateID(),
		Name:       name,
		Status:     TaskStatusPending,
		CreatedAt:  time.Now(),
		fn:         fn,
		ctx:        taskCtx,
		cancelFunc: taskCancel,
	}

	tm.tasks[task.ID] = task
	tm.mu.Unlock()

	tm.notifyHook(task.Snapshot())

	select {
	case tm.queue <- task:
		return task, nil
	case <-tm.ctx.Done():
		return nil, errors.New("task manager stopped")
	}
}

// SubmitCommand enqueues an OS shell/binary command execution task.
func (tm *TaskManager) SubmitCommand(name string, command string, args ...string) (*Task, error) {
	fn := func(ctx context.Context) (string, error) {
		cmd := exec.CommandContext(ctx, command, args...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	return tm.SubmitTask(name, fn)
}

// CancelTask cancels a task by ID if pending or running.
func (tm *TaskManager) CancelTask(id string) error {
	tm.mu.RLock()
	task, exists := tm.tasks[id]
	tm.mu.RUnlock()

	if !exists {
		return fmt.Errorf("task with id %s not found", id)
	}

	task.mu.Lock()
	if task.Status == TaskStatusCompleted || task.Status == TaskStatusFailed || task.Status == TaskStatusCancelled {
		task.mu.Unlock()
		return fmt.Errorf("task %s already terminated with status %s", id, task.Status)
	}
	task.Status = TaskStatusCancelled
	task.CompletedAt = time.Now()
	if task.cancelFunc != nil {
		task.cancelFunc()
	}
	task.mu.Unlock()

	tm.notifyHook(task.Snapshot())
	return nil
}

// GetTask returns a snapshot of the specified task.
func (tm *TaskManager) GetTask(id string) (TaskSnapshot, bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	task, exists := tm.tasks[id]
	if !exists {
		return TaskSnapshot{}, false
	}
	return task.Snapshot(), true
}

// ListTasks returns snapshots of all tasks.
func (tm *TaskManager) ListTasks() []TaskSnapshot {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	var result []TaskSnapshot
	for _, task := range tm.tasks {
		result = append(result, task.Snapshot())
	}
	return result
}

// ClearCompleted removes completed, failed, or cancelled tasks from memory.
func (tm *TaskManager) ClearCompleted() int {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	cleared := 0
	for id, task := range tm.tasks {
		task.mu.RLock()
		st := task.Status
		task.mu.RUnlock()
		if st == TaskStatusCompleted || st == TaskStatusFailed || st == TaskStatusCancelled {
			delete(tm.tasks, id)
			cleared++
		}
	}
	return cleared
}

func (tm *TaskManager) worker() {
	defer tm.wg.Done()

	for task := range tm.queue {
		select {
		case <-tm.ctx.Done():
			return
		default:
		}

		task.mu.Lock()
		if task.Status == TaskStatusCancelled {
			task.mu.Unlock()
			continue
		}
		task.Status = TaskStatusRunning
		task.StartedAt = time.Now()
		task.mu.Unlock()

		tm.notifyHook(task.Snapshot())

		res, err := task.fn(task.ctx)

		task.mu.Lock()
		task.CompletedAt = time.Now()
		if task.ctx.Err() == context.Canceled || task.Status == TaskStatusCancelled {
			task.Status = TaskStatusCancelled
			if err != nil {
				task.Error = err.Error()
			}
		} else if err != nil {
			task.Status = TaskStatusFailed
			task.Error = err.Error()
			task.Result = res
		} else {
			task.Status = TaskStatusCompleted
			task.Result = res
			task.Progress = 1.0
		}
		taskSnapshot := TaskSnapshot{
			ID:          task.ID,
			Name:        task.Name,
			Status:      task.Status,
			Result:      task.Result,
			Error:       task.Error,
			Progress:    task.Progress,
			CreatedAt:   task.CreatedAt,
			StartedAt:   task.StartedAt,
			CompletedAt: task.CompletedAt,
		}
		task.mu.Unlock()

		tm.notifyHook(taskSnapshot)
	}
}

func (tm *TaskManager) notifyHook(snap TaskSnapshot) {
	tm.mu.RLock()
	hook := tm.hook
	tm.mu.RUnlock()

	if hook != nil {
		hook.TriggerString(fmt.Sprintf("Task [%s] '%s' status: %s", snap.ID, snap.Name, snap.Status))
		hook.TriggerInterface(snap)
	}
}
