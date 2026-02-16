package session

import (
	"net"
	"testing"
	"time"

	"tcpadapter/internal/queue"
	"tcpadapter/internal/store"
)

func TestRestoreSessionWithQueue(t *testing.T) {
	st := store.NewInMemoryStore()
	err := st.SaveSession(store.SessionSnapshot{
		ControllerID: "imei-1",
		LastSeen:     time.Unix(1700000000, 0).UTC(),
		BufferFree:   1111,
		SyncMode:     true,
		Pending: []queue.Command{{
			MessageID: "m1",
			CommandID: 14,
			Priority:  queue.PrioritySync,
			CreatedAt: time.Now().UTC(),
		}},
	})
	if err != nil {
		t.Fatalf("SaveSession() error = %v", err)
	}

	m := NewManager(100, 1000, 1048576, "drop_oldest", st)
	n, err := m.Restore()
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if n != 1 {
		t.Fatalf("restored count = %d", n)
	}

	ctx, ok := m.DeliveryContext("imei-1")
	if !ok {
		t.Fatal("expected restored session")
	}
	if ctx.BufferFree != 1111 || !ctx.SyncMode {
		t.Fatalf("unexpected restored context: %+v", ctx)
	}
	if !ctx.Queue.HasSync() {
		t.Fatal("expected restored sync queue")
	}
}

func TestEnqueueForOfflineControllerCreatesQueue(t *testing.T) {
	st := store.NewInMemoryStore()
	m := NewManager(100, 1000, 1048576, "drop_oldest", st)

	err := m.Enqueue("imei-offline", queue.Command{
		MessageID: "m1",
		CommandID: 8,
		Priority:  queue.PriorityHigh,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	ctx, ok := m.DeliveryContext("imei-offline")
	if !ok {
		t.Fatal("expected session to be created for offline enqueue")
	}
	if ctx.Conn != nil {
		t.Fatal("expected detached session without connection")
	}
	if ctx.Queue.Len() != 1 {
		t.Fatalf("expected queued command, got len=%d", ctx.Queue.Len())
	}

	snaps, err := st.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(snaps) != 1 || len(snaps[0].Pending) != 1 {
		t.Fatalf("unexpected persisted snapshot: %+v", snaps)
	}
}

func TestCreateAttachesToDetachedSession(t *testing.T) {
	st := store.NewInMemoryStore()
	m := NewManager(100, 1000, 1048576, "drop_oldest", st)

	if err := m.Enqueue("imei-attach", queue.Command{
		MessageID: "m1",
		CommandID: 14,
		Priority:  queue.PrioritySync,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	s, err := m.Create("imei-attach", c1)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if s.Conn == nil {
		t.Fatal("expected connection to be attached")
	}
	if s.Queue.Len() != 1 {
		t.Fatalf("expected existing queued command to remain, got len=%d", s.Queue.Len())
	}
}

func TestQueueOverflowReject(t *testing.T) {
	st := store.NewInMemoryStore()
	m := NewManager(100, 1, 1048576, "reject", st)

	if err := m.Enqueue("imei-1", queue.Command{
		MessageID: "m1",
		CommandID: 8,
		Priority:  queue.PriorityHigh,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("first enqueue error = %v", err)
	}
	err := m.Enqueue("imei-1", queue.Command{
		MessageID: "m2",
		CommandID: 9,
		Priority:  queue.PriorityQuery,
		CreatedAt: time.Now().UTC(),
	})
	if err != ErrQueueFull {
		t.Fatalf("expected ErrQueueFull, got %v", err)
	}

	ctx, ok := m.DeliveryContext("imei-1")
	if !ok || ctx.Queue.Len() != 1 {
		t.Fatalf("expected queue len 1, ok=%v len=%d", ok, ctx.Queue.Len())
	}
}

func TestQueueOverflowDropOldest(t *testing.T) {
	st := store.NewInMemoryStore()
	m := NewManager(100, 1, 1048576, "drop_oldest", st)

	old := time.Now().UTC().Add(-1 * time.Minute)
	if err := m.Enqueue("imei-1", queue.Command{
		MessageID: "m1",
		CommandID: 8,
		Priority:  queue.PriorityHigh,
		CreatedAt: old,
	}); err != nil {
		t.Fatalf("first enqueue error = %v", err)
	}
	if err := m.Enqueue("imei-1", queue.Command{
		MessageID: "m2",
		CommandID: 9,
		Priority:  queue.PriorityQuery,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("second enqueue error = %v", err)
	}

	ctx, ok := m.DeliveryContext("imei-1")
	if !ok {
		t.Fatal("missing session")
	}
	if ctx.Queue.Len() != 1 {
		t.Fatalf("expected queue len 1, got %d", ctx.Queue.Len())
	}
	cmd, ok := ctx.Queue.PopNext(time.Now().UTC(), false, false)
	if !ok || cmd.MessageID != "m2" {
		t.Fatalf("expected newest command to remain, got ok=%v msg=%s", ok, cmd.MessageID)
	}
}

func TestInFlightRetryAndAck(t *testing.T) {
	st := store.NewInMemoryStore()
	m := NewManager(100, 1000, 1048576, "drop_oldest", st)

	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	_, err := m.Create("imei-if", c1)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	cmd := queue.Command{
		MessageID: "m1",
		CommandID: 8,
		Priority:  queue.PriorityHigh,
		CreatedAt: time.Now().UTC().Add(-2 * time.Second),
		TTL:       30 * time.Second,
	}
	seq, err := m.NextCommandSeq("imei-if")
	if err != nil {
		t.Fatalf("NextCommandSeq() error = %v", err)
	}
	if err := m.RegisterInFlight("imei-if", seq, cmd, time.Now().UTC().Add(-20*time.Second)); err != nil {
		t.Fatalf("RegisterInFlight() error = %v", err)
	}

	outcomes, err := m.ProcessInFlight("imei-if", time.Now().UTC(), 5*time.Second, 0, 3)
	if err != nil {
		t.Fatalf("ProcessInFlight() error = %v", err)
	}
	if len(outcomes) != 1 || outcomes[0].Type != "retrying" {
		t.Fatalf("expected one retrying outcome, got %+v", outcomes)
	}

	ctx, ok := m.DeliveryContext("imei-if")
	if !ok {
		t.Fatal("missing session")
	}
	requeued, ok := ctx.Queue.PopNext(time.Now().UTC(), false, false)
	if !ok {
		t.Fatal("expected command requeued")
	}
	if requeued.Attempts != 1 {
		t.Fatalf("expected attempts=1, got %d", requeued.Attempts)
	}
	if err := m.RegisterInFlight("imei-if", seq, requeued, time.Now().UTC()); err != nil {
		t.Fatalf("RegisterInFlight() second error = %v", err)
	}

	ackedCmd, attempts, ok := m.AckInFlight("imei-if", seq)
	if !ok {
		t.Fatal("expected acked in-flight command")
	}
	if ackedCmd.MessageID != "m1" || attempts != 2 {
		t.Fatalf("unexpected acked command=%+v attempts=%d", ackedCmd, attempts)
	}
}

func TestInFlightExpiresByTTL(t *testing.T) {
	st := store.NewInMemoryStore()
	m := NewManager(100, 1000, 1048576, "drop_oldest", st)

	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	_, err := m.Create("imei-exp", c1)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	cmd := queue.Command{
		MessageID: "m-exp",
		CommandID: 8,
		Priority:  queue.PriorityHigh,
		CreatedAt: time.Now().UTC().Add(-20 * time.Second),
		TTL:       5 * time.Second,
	}
	seq, err := m.NextCommandSeq("imei-exp")
	if err != nil {
		t.Fatalf("NextCommandSeq() error = %v", err)
	}
	if err := m.RegisterInFlight("imei-exp", seq, cmd, time.Now().UTC().Add(-10*time.Second)); err != nil {
		t.Fatalf("RegisterInFlight() error = %v", err)
	}

	outcomes, err := m.ProcessInFlight("imei-exp", time.Now().UTC(), 3*time.Second, 0, 3)
	if err != nil {
		t.Fatalf("ProcessInFlight() error = %v", err)
	}
	if len(outcomes) != 1 || outcomes[0].Type != "expired" {
		t.Fatalf("expected one expired outcome, got %+v", outcomes)
	}
}

func TestQueueBytesOverflowReject(t *testing.T) {
	st := store.NewInMemoryStore()
	m := NewManager(100, 1000, 30, "reject", st)

	// Estimated size = 11 + payload_len. payload_len=10 -> 21 bytes.
	if err := m.Enqueue("imei-b", queue.Command{
		MessageID: "m1",
		CommandID: 8,
		Priority:  queue.PriorityHigh,
		CreatedAt: time.Now().UTC(),
		Payload:   make([]byte, 10),
	}); err != nil {
		t.Fatalf("first enqueue error = %v", err)
	}

	err := m.Enqueue("imei-b", queue.Command{
		MessageID: "m2",
		CommandID: 9,
		Priority:  queue.PriorityQuery,
		CreatedAt: time.Now().UTC(),
		Payload:   make([]byte, 10),
	})
	if err != ErrQueueBytesFull {
		t.Fatalf("expected ErrQueueBytesFull, got %v", err)
	}
}

func TestQueueBytesOverflowDropOldest(t *testing.T) {
	st := store.NewInMemoryStore()
	m := NewManager(100, 1000, 30, "drop_oldest", st)

	if err := m.Enqueue("imei-b2", queue.Command{
		MessageID: "old",
		CommandID: 8,
		Priority:  queue.PriorityHigh,
		CreatedAt: time.Now().UTC().Add(-1 * time.Minute),
		Payload:   make([]byte, 10),
	}); err != nil {
		t.Fatalf("first enqueue error = %v", err)
	}
	if err := m.Enqueue("imei-b2", queue.Command{
		MessageID: "new",
		CommandID: 9,
		Priority:  queue.PriorityQuery,
		CreatedAt: time.Now().UTC(),
		Payload:   make([]byte, 10),
	}); err != nil {
		t.Fatalf("second enqueue error = %v", err)
	}

	ctx, ok := m.DeliveryContext("imei-b2")
	if !ok {
		t.Fatal("missing session")
	}
	if ctx.Queue.Len() != 1 {
		t.Fatalf("expected queue len 1, got %d", ctx.Queue.Len())
	}
	cmd, ok := ctx.Queue.PopNext(time.Now().UTC(), false, false)
	if !ok || cmd.MessageID != "new" {
		t.Fatalf("expected new command to remain, got ok=%v msg=%s", ok, cmd.MessageID)
	}
}
