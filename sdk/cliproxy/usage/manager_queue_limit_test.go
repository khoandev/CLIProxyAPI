package usage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestManagerBoundsQueue proves the usage queue is bounded.
//
// Every pending record holds its response headers, so an unbounded queue
// turns a slow plugin into unbounded memory growth.
func TestManagerBoundsQueue(t *testing.T) {
	const capacity = 8
	mgr := NewManager(capacity)
	t.Cleanup(mgr.Stop)

	// No plugin is registered, so the dispatcher drops the records instead of
	// delivering them, and the queue grows to its bound.
	for i := range capacity * 4 {
		mgr.Publish(context.Background(), Record{RequestID: fmt.Sprintf("req-%d", i)})
	}

	mgr.mu.Lock()
	queued := len(mgr.queue)
	mgr.mu.Unlock()
	if queued > capacity {
		t.Fatalf("queued records = %d, want at most %d", queued, capacity)
	}
}

// TestManagerDefaultQueueCapacity proves a manager built without a capacity
// still bounds its queue.
func TestManagerDefaultQueueCapacity(t *testing.T) {
	mgr := NewManager(0)
	t.Cleanup(mgr.Stop)

	if mgr.maxQueue != DefaultQueueCapacity {
		t.Fatalf("maxQueue = %d, want %d", mgr.maxQueue, DefaultQueueCapacity)
	}

	for i := range DefaultQueueCapacity + 4 {
		mgr.Publish(context.Background(), Record{RequestID: fmt.Sprintf("req-%d", i)})
	}
	mgr.mu.Lock()
	queued := len(mgr.queue)
	mgr.mu.Unlock()
	if queued > DefaultQueueCapacity {
		t.Fatalf("queued records = %d, want at most %d", queued, DefaultQueueCapacity)
	}
}

// TestManagerDeliversQueuedRecords proves the bound does not stop delivery of
// the records that are still queued.
func TestManagerDeliversQueuedRecords(t *testing.T) {
	mgr := NewManager(4)
	t.Cleanup(mgr.Stop)
	plugin := &captureUsagePlugin{records: make(chan Record, 8)}
	mgr.Register(plugin)

	for i := range 2 {
		mgr.Publish(context.Background(), Record{RequestID: fmt.Sprintf("req-%d", i)})
	}

	for i := range 2 {
		select {
		case <-plugin.records:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for usage record %d", i)
		}
	}
}
