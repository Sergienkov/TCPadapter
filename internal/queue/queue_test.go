package queue

import (
	"testing"
	"time"
)

func TestPriorityOrder(t *testing.T) {
	q := NewControllerQueue()
	now := time.Now().UTC()

	q.Push(Command{CommandID: 9, Priority: PriorityQuery, CreatedAt: now})
	q.Push(Command{CommandID: 14, Priority: PrioritySync, CreatedAt: now})
	q.Push(Command{CommandID: 20, Priority: PriorityFW, CreatedAt: now})
	q.Push(Command{CommandID: 8, Priority: PriorityHigh, CreatedAt: now})

	expect := []uint8{8, 20, 14, 9}
	for i, want := range expect {
		cmd, ok := q.PopNext(now, false, false)
		if !ok {
			t.Fatalf("pop %d: expected command", i)
		}
		if cmd.CommandID != want {
			t.Fatalf("pop %d: got %d want %d", i, cmd.CommandID, want)
		}
	}
}

func TestModeGates(t *testing.T) {
	q := NewControllerQueue()
	now := time.Now().UTC()
	q.Push(Command{CommandID: 14, Priority: PrioritySync, CreatedAt: now})
	q.Push(Command{CommandID: 9, Priority: PriorityQuery, CreatedAt: now})

	if _, ok := q.PopNext(now, true, false); ok {
		t.Fatal("fw mode should not pop sync/query without high/fw")
	}

	cmd, ok := q.PopNext(now, false, true)
	if !ok || cmd.CommandID != 14 {
		t.Fatalf("sync mode expected sync command, got=%v ok=%v", cmd.CommandID, ok)
	}
}

func TestTTLExpiry(t *testing.T) {
	q := NewControllerQueue()
	now := time.Now().UTC()
	q.Push(Command{CommandID: 2, Priority: PriorityHigh, CreatedAt: now.Add(-11 * time.Second), TTL: 10 * time.Second})
	q.Push(Command{CommandID: 3, Priority: PriorityHigh, CreatedAt: now, TTL: 10 * time.Second})

	expired := q.DrainExpired(now)
	if len(expired) != 1 || expired[0].CommandID != 2 {
		t.Fatalf("expected drained expired cmd 2, got %#v", expired)
	}

	cmd, ok := q.PopNext(now, false, false)
	if !ok {
		t.Fatal("expected non-expired command")
	}
	if cmd.CommandID != 3 {
		t.Fatalf("expected cmd 3, got %d", cmd.CommandID)
	}
}
