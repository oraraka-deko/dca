package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestOutbox_BasicFIFO(t *testing.T) {
	outbox := NewOutbox(10)

	if outbox.Len() != 0 {
		t.Errorf("Expected empty outbox, got Len %d", outbox.Len())
	}

	item1 := OutboxItem{ID: "req-1", Payload: json.RawMessage(`{"result":"ok1"}`)}
	item2 := OutboxItem{ID: "req-2", Payload: json.RawMessage(`{"result":"ok2"}`)}

	if err := outbox.Enqueue(item1); err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}
	if err := outbox.Enqueue(item2); err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	if outbox.Len() != 2 {
		t.Errorf("Expected Len 2, got %d", outbox.Len())
	}

	// Dequeue item 1
	d1, ok := outbox.Dequeue()
	if !ok || d1.ID != "req-1" {
		t.Errorf("Expected req-1, got %v (ok=%v)", d1, ok)
	}

	// Dequeue item 2
	d2, ok := outbox.Dequeue()
	if !ok || d2.ID != "req-2" {
		t.Errorf("Expected req-2, got %v (ok=%v)", d2, ok)
	}

	// Dequeue empty
	_, ok = outbox.Dequeue()
	if ok {
		t.Error("Expected Dequeue on empty outbox to return false")
	}
}

func TestOutbox_BoundedCapacity(t *testing.T) {
	outbox := NewOutbox(3)

	for i := 1; i <= 5; i++ {
		_ = outbox.Enqueue(OutboxItem{ID: fmt.Sprintf("req-%d", i)})
	}

	if outbox.Len() != 3 {
		t.Fatalf("Expected bounded capacity of 3, got Len %d", outbox.Len())
	}

	// Oldest items (req-1, req-2) should have been dropped
	items := outbox.PeekAll()
	expectedIDs := []string{"req-3", "req-4", "req-5"}
	for i, exp := range expectedIDs {
		if items[i].ID != exp {
			t.Errorf("Index %d ID = %s; want %s", i, items[i].ID, exp)
		}
	}
}

func TestOutbox_FlushSuccessAndFailure(t *testing.T) {
	outbox := NewOutbox(10)

	_ = outbox.Enqueue(OutboxItem{ID: "item-1", Payload: json.RawMessage(`"p1"`)})
	_ = outbox.Enqueue(OutboxItem{ID: "item-2", Payload: json.RawMessage(`"p2"`)})
	_ = outbox.Enqueue(OutboxItem{ID: "item-3", Payload: json.RawMessage(`"p3"`)})

	sent := make([]string, 0)
	failItem2 := true

	mockSend := func(item OutboxItem) error {
		if item.ID == "item-2" && failItem2 {
			return errors.New("network drop on item-2")
		}
		sent = append(sent, item.ID)
		return nil
	}

	// Flush attempt 1: should send item-1, then fail on item-2
	err := outbox.Flush(mockSend)
	if err == nil || err.Error() != "network drop on item-2" {
		t.Fatalf("Expected network drop error, got: %v", err)
	}

	if len(sent) != 1 || sent[0] != "item-1" {
		t.Errorf("Expected only item-1 sent, got: %v", sent)
	}

	if outbox.Len() != 2 {
		t.Errorf("Expected 2 items remaining in outbox after failure, got %d", outbox.Len())
	}

	peek := outbox.PeekAll()
	if peek[0].ID != "item-2" {
		t.Errorf("Expected item-2 to remain at head of queue, got %s", peek[0].ID)
	}
	if peek[0].Attempts != 1 {
		t.Errorf("Expected attempt count 1 on item-2, got %d", peek[0].Attempts)
	}

	// Re-connection occurs: network problem resolved
	failItem2 = false
	err = outbox.Flush(mockSend)
	if err != nil {
		t.Fatalf("Expected second flush to succeed, got %v", err)
	}

	if outbox.Len() != 0 {
		t.Errorf("Expected outbox to be empty after successful flush, got %d", outbox.Len())
	}

	if len(sent) != 3 || sent[1] != "item-2" || sent[2] != "item-3" {
		t.Errorf("Expected all items delivered in order, got: %v", sent)
	}
}

func TestOutbox_ClearAndNotify(t *testing.T) {
	outbox := NewOutbox(5)

	select {
	case <-outbox.Notify():
		t.Error("Notify channel should be empty initially")
	default:
	}

	_ = outbox.Enqueue(OutboxItem{ID: "1"})

	select {
	case <-outbox.Notify():
		// Success
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected notification on Enqueue")
	}

	outbox.Clear()
	if outbox.Len() != 0 {
		t.Errorf("Expected Len 0 after Clear, got %d", outbox.Len())
	}
}

func TestOutbox_Concurrency(t *testing.T) {
	outbox := NewOutbox(1000)

	var wg sync.WaitGroup

	// 10 concurrent enqueuers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = outbox.Enqueue(OutboxItem{
					ID:        fmt.Sprintf("w%d-%d", workerID, j),
					Payload:   json.RawMessage(`{"test":true}`),
					CreatedAt: time.Now(),
				})
			}
		}(i)
	}

	// 2 concurrent flushers
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 20; k++ {
				_ = outbox.Flush(func(item OutboxItem) error {
					return nil
				})
				time.Sleep(1 * time.Millisecond)
			}
		}()
	}

	wg.Wait()

	// Drain remaining
	_ = outbox.Flush(func(item OutboxItem) error {
		return nil
	})
	if outbox.Len() != 0 {
		t.Errorf("Expected 0 items after full drain, got %d", outbox.Len())
	}
}
