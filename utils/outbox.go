package utils

import (
	"encoding/json"
	"errors"
	"runtime"
	"sync"
	"time"
)

var (
	// ErrOutboxClosed is returned when attempting operations on a closed Outbox.
	ErrOutboxClosed = errors.New("outbox queue is closed")
)

// OutboxItem represents a buffered JSON-RPC response payload awaiting network transmission.
type OutboxItem struct {
	ID        string          `json:"id"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
	Attempts  int             `json:"attempts"`
}

// Outbox is a bounded thread-safe FIFO queue with a non-blocking notification channel.
type Outbox struct {
	mu       sync.Mutex
	items    []OutboxItem
	maxSize  int
	notify   chan struct{}
	closed   bool
	flushing bool
}

// NewOutbox creates an Outbox instance bounded by maxSize. Default is 1000 if maxSize <= 0.
func NewOutbox(maxSize int) *Outbox {
	if maxSize <= 0 {
		maxSize = 1000
	}
	return &Outbox{
		items:   make([]OutboxItem, 0),
		maxSize: maxSize,
		notify:  make(chan struct{}, 1),
	}
}

// Enqueue appends an OutboxItem to the queue in a thread-safe manner and triggers notification.
func (o *Outbox) Enqueue(item OutboxItem) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.closed {
		return ErrOutboxClosed
	}

	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}

	if len(o.items) >= o.maxSize {
		// Drop oldest item if capacity is exceeded to keep bounded capacity
		o.items = o.items[1:]
	}

	o.items = append(o.items, item)

	// Non-blocking notification to flusher loop
	select {
	case o.notify <- struct{}{}:
	default:
	}

	return nil
}

// Flush drains items in FIFO order by attempting sendFunc for each item.
// If sendFunc succeeds, the item is removed from queue. If sendFunc fails,
// the item remains at the front of the queue and Flush stops immediately.
func (o *Outbox) Flush(sendFunc func(OutboxItem) error) error {
	o.mu.Lock()
	if o.closed || o.flushing || len(o.items) == 0 {
		o.mu.Unlock()
		return nil
	}
	o.flushing = true
	o.mu.Unlock()

	defer func() {
		o.mu.Lock()
		o.flushing = false
		o.mu.Unlock()
	}()

	for {
		runtime.Gosched()
		o.mu.Lock()
		if o.closed || len(o.items) == 0 {
			o.mu.Unlock()
			return nil
		}
		o.items[0].Attempts++
		head := o.items[0]
		o.mu.Unlock()

		if err := sendFunc(head); err != nil {
			// Delivery failed: head item remains at queue head with incremented Attempts, flushing halts
			return err
		}

		// Delivery succeeded: remove head item atomically if head matches
		o.mu.Lock()
		if len(o.items) > 0 && o.items[0].ID == head.ID {
			o.items = o.items[1:]
		}
		o.mu.Unlock()
	}
}

// Dequeue removes and returns the front item from the outbox queue if non-empty.
func (o *Outbox) Dequeue() (OutboxItem, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if len(o.items) == 0 {
		return OutboxItem{}, false
	}
	item := o.items[0]
	o.items = o.items[1:]
	return item, true
}

// PeekAll returns a slice copy of current items in the outbox queue.
func (o *Outbox) PeekAll() []OutboxItem {
	o.mu.Lock()
	defer o.mu.Unlock()

	cp := make([]OutboxItem, len(o.items))
	copy(cp, o.items)
	return cp
}

// Len returns current number of items in outbox queue.
func (o *Outbox) Len() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.items)
}

// Clear removes all items from the outbox queue.
func (o *Outbox) Clear() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.items = make([]OutboxItem, 0)
}

// Notify returns the notification channel signaling when new items are enqueued.
func (o *Outbox) Notify() <-chan struct{} {
	return o.notify
}

// NotifyChan is an alias for Notify.
func (o *Outbox) NotifyChan() <-chan struct{} {
	return o.notify
}

// Close marks the outbox queue as closed.
func (o *Outbox) Close() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closed = true
}

// Prepend inserts a slice of OutboxItems to the front of the queue.
func (o *Outbox) Prepend(items []OutboxItem) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || len(items) == 0 {
		return
	}
	// Filter out any duplicates that might already be in o.items
	uniqueNew := make([]OutboxItem, 0, len(items))
	for _, it := range items {
		found := false
		for _, existing := range o.items {
			if existing.ID == it.ID {
				found = true
				break
			}
		}
		if !found {
			uniqueNew = append(uniqueNew, it)
		}
	}
	
	o.items = append(uniqueNew, o.items...)
	if len(o.items) > o.maxSize {
		o.items = o.items[len(o.items)-o.maxSize:]
	}
}

