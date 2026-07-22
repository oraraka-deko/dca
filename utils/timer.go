// Package utils provides utility components.
package utils

import (
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
)

var lastTimerID int64

func nextTimerID() int {
	return int(atomic.AddInt64(&lastTimerID, 1))
}

// TimerOption is a configuration option in [NewTimer].
type TimerOption func(*TimerModel)

// WithTimerInterval is an option for setting the interval between ticks.
func WithTimerInterval(interval time.Duration) TimerOption {
	return func(m *TimerModel) {
		m.Interval = interval
	}
}

// TimerStartStopMsg is used to start and stop the timer.
type TimerStartStopMsg struct {
	ID      int
	running bool
}

// TimerTickMsg is a message that is sent on every timer tick.
type TimerTickMsg struct {
	// ID is the identifier of the timer that sends the message.
	ID int

	// Timeout returns whether or not this tick is a timeout tick.
	Timeout bool

	tag int
}

// TimerTimeoutMsg is a message that is sent once when the timer times out.
type TimerTimeoutMsg struct {
	ID int
}

// TimerModel of the timer component.
type TimerModel struct {
	// How long until the timer expires.
	Timeout time.Duration

	// How long to wait before every tick. Defaults to 1 second.
	Interval time.Duration

	id      int
	tag     int
	running bool
}

// NewTimer creates a new timer with the given timeout and default 1s interval.
func NewTimer(timeout time.Duration, opts ...TimerOption) TimerModel {
	m := TimerModel{
		Timeout:  timeout,
		Interval: time.Second,
		running:  true,
		id:       nextTimerID(),
	}
	for _, opt := range opts {
		opt(&m)
	}
	return m
}

// ID returns the model's identifier.
func (m TimerModel) ID() int {
	return m.id
}

// Running returns whether or not the timer is running.
func (m TimerModel) Running() bool {
	if m.Timedout() || !m.running {
		return false
	}
	return true
}

// Timedout returns whether or not the timer has timed out.
func (m TimerModel) Timedout() bool {
	return m.Timeout <= 0
}

// Init starts the timer.
func (m TimerModel) Init() tea.Cmd {
	return m.tick()
}

// Update handles the timer tick.
func (m TimerModel) Update(msg tea.Msg) (TimerModel, tea.Cmd) {
	switch msg := msg.(type) {
	case TimerStartStopMsg:
		if msg.ID != 0 && msg.ID != m.id {
			return m, nil
		}
		m.running = msg.running
		return m, m.tick()
	case TimerTickMsg:
		if !m.Running() || (msg.ID != 0 && msg.ID != m.id) {
			break
		}

		// If a tag is set, and it's not the one we expect, reject the message.
		if msg.tag > 0 && msg.tag != m.tag {
			return m, nil
		}

		m.Timeout -= m.Interval
		return m, tea.Batch(m.tick(), m.timedout())
	}

	return m, nil
}

// View of the timer component.
func (m TimerModel) View() string {
	return m.Timeout.String()
}

// Start resumes the timer. Has no effect if the timer has timed out.
func (m TimerModel) Start() tea.Cmd {
	return m.startStop(true)
}

// Stop pauses the timer. Has no effect if the timer has timed out.
func (m TimerModel) Stop() tea.Cmd {
	return m.startStop(false)
}

// Toggle stops the timer if it's running and starts it if it's stopped.
func (m TimerModel) Toggle() tea.Cmd {
	return m.startStop(!m.Running())
}

func (m TimerModel) tick() tea.Cmd {
	return tea.Tick(m.Interval, func(_ time.Time) tea.Msg {
		return TimerTickMsg{ID: m.id, tag: m.tag, Timeout: m.Timedout()}
	})
}

func (m TimerModel) timedout() tea.Cmd {
	if !m.Timedout() {
		return nil
	}
	return func() tea.Msg {
		return TimerTimeoutMsg{ID: m.id}
	}
}

func (m TimerModel) startStop(v bool) tea.Cmd {
	return func() tea.Msg {
		return TimerStartStopMsg{ID: m.id, running: v}
	}
}
