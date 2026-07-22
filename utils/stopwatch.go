// Package utils provides utility components.
package utils

import (
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
)

var lastStopwatchID int64

func nextStopwatchID() int {
	return int(atomic.AddInt64(&lastStopwatchID, 1))
}

// StopwatchOption is a configuration option in [NewStopwatch].
type StopwatchOption func(*StopwatchModel)

// WithStopwatchInterval is an option for setting the interval between ticks.
func WithStopwatchInterval(interval time.Duration) StopwatchOption {
	return func(m *StopwatchModel) {
		m.Interval = interval
	}
}

// StopwatchTickMsg is a message that is sent on every stopwatch tick.
type StopwatchTickMsg struct {
	// ID is the identifier of the stopwatch that sends the message.
	ID  int
	tag int
}

// StopwatchStartStopMsg is sent when the stopwatch should start or stop.
type StopwatchStartStopMsg struct {
	ID      int
	running bool
}

// StopwatchResetMsg is sent when the stopwatch should reset.
type StopwatchResetMsg struct {
	ID int
}

// StopwatchModel for the stopwatch component.
type StopwatchModel struct {
	d       time.Duration
	id      int
	tag     int
	running bool

	// How long to wait before every tick. Defaults to 1 second.
	Interval time.Duration
}

// NewStopwatch creates a new stopwatch with 1s interval.
func NewStopwatch(opts ...StopwatchOption) StopwatchModel {
	m := StopwatchModel{
		id:       nextStopwatchID(),
		Interval: time.Second,
	}

	for _, opt := range opts {
		opt(&m)
	}
	return m
}

// ID returns the unique ID of the model.
func (m StopwatchModel) ID() int {
	return m.id
}

// Init starts the stopwatch.
func (m StopwatchModel) Init() tea.Cmd {
	return m.Start()
}

// Start starts the stopwatch.
func (m StopwatchModel) Start() tea.Cmd {
	return tea.Sequence(func() tea.Msg {
		return StopwatchStartStopMsg{ID: m.id, running: true}
	}, stopwatchTick(m.id, m.tag, m.Interval))
}

// Stop stops the stopwatch.
func (m StopwatchModel) Stop() tea.Cmd {
	return func() tea.Msg {
		return StopwatchStartStopMsg{ID: m.id, running: false}
	}
}

// Toggle stops the stopwatch if it is running and starts it if it is stopped.
func (m StopwatchModel) Toggle() tea.Cmd {
	if m.Running() {
		return m.Stop()
	}
	return m.Start()
}

// Reset resets the stopwatch to 0.
func (m StopwatchModel) Reset() tea.Cmd {
	return func() tea.Msg {
		return StopwatchResetMsg{ID: m.id}
	}
}

// Running returns true if the stopwatch is running or false if it is stopped.
func (m StopwatchModel) Running() bool {
	return m.running
}

// Update handles the stopwatch tick.
func (m StopwatchModel) Update(msg tea.Msg) (StopwatchModel, tea.Cmd) {
	switch msg := msg.(type) {
	case StopwatchStartStopMsg:
		if msg.ID != m.id {
			return m, nil
		}
		m.running = msg.running
	case StopwatchResetMsg:
		if msg.ID != m.id {
			return m, nil
		}
		m.d = 0
	case StopwatchTickMsg:
		if !m.running || msg.ID != m.id {
			break
		}

		// If a tag is set, and it's not the one we expect, reject the message.
		if msg.tag > 0 && msg.tag != m.tag {
			return m, nil
		}

		m.d += m.Interval
		m.tag++
		return m, stopwatchTick(m.id, m.tag, m.Interval)
	}

	return m, nil
}

// Elapsed returns the time elapsed.
func (m StopwatchModel) Elapsed() time.Duration {
	return m.d
}

// View of the stopwatch component.
func (m StopwatchModel) View() string {
	return m.d.String()
}

func stopwatchTick(id int, tag int, d time.Duration) tea.Cmd {
	return tea.Tick(d, func(_ time.Time) tea.Msg {
		return StopwatchTickMsg{ID: id, tag: tag}
	})
}