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

	policy := func(_ string, _ queue.Command) DeliveryPolicy {
		return DeliveryPolicy{AckTimeout: 5 * time.Second, RetryBackoff: 0, MaxRetries: 3}
	}
	outcomes, err := m.ProcessInFlight("imei-if", time.Now().UTC(), policy)
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

	policy := func(_ string, _ queue.Command) DeliveryPolicy {
		return DeliveryPolicy{AckTimeout: 3 * time.Second, RetryBackoff: 0, MaxRetries: 3}
	}
	outcomes, err := m.ProcessInFlight("imei-exp", time.Now().UTC(), policy)
	if err != nil {
		t.Fatalf("ProcessInFlight() error = %v", err)
	}
	if len(outcomes) != 1 || outcomes[0].Type != "expired" {
		t.Fatalf("expected one expired outcome, got %+v", outcomes)
	}
}

func TestAckSingleInFlightFallback(t *testing.T) {
	st := store.NewInMemoryStore()
	m := NewManager(100, 1000, 1048576, "drop_oldest", st)

	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	_, err := m.Create("imei-fallback", c1)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	cmd := queue.Command{
		MessageID: "m-fallback",
		CommandID: 9,
		Priority:  queue.PriorityQuery,
		CreatedAt: time.Now().UTC(),
		TTL:       10 * time.Second,
	}
	seq, err := m.NextCommandSeq("imei-fallback")
	if err != nil {
		t.Fatalf("NextCommandSeq() error = %v", err)
	}
	if err := m.RegisterInFlight("imei-fallback", seq, cmd, time.Now().UTC()); err != nil {
		t.Fatalf("RegisterInFlight() error = %v", err)
	}

	gotSeq, gotCmd, gotAttempts, ok := m.AckSingleInFlightFallback("imei-fallback")
	if !ok {
		t.Fatal("expected fallback ack to match single in-flight entry")
	}
	if gotSeq != seq || gotCmd.MessageID != cmd.MessageID || gotAttempts != 1 {
		t.Fatalf("unexpected fallback result seq=%d cmd=%+v attempts=%d", gotSeq, gotCmd, gotAttempts)
	}

	if _, _, _, ok := m.AckSingleInFlightFallback("imei-fallback"); ok {
		t.Fatal("expected no in-flight entries after fallback ack")
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

func TestRetryDelay_ExponentialWithCapAndJitter(t *testing.T) {
	base := 100 * time.Millisecond
	seed := uint64(0x1234abcd)

	if got := retryDelay(1, base, seed); got != 0 {
		t.Fatalf("attempt1 expected 0, got %v", got)
	}

	d2 := retryDelay(2, base, seed)
	if d2 < 80*time.Millisecond || d2 > 120*time.Millisecond {
		t.Fatalf("attempt2 expected in [80ms,120ms], got %v", d2)
	}

	d3 := retryDelay(3, base, seed)
	if d3 < 160*time.Millisecond || d3 > 240*time.Millisecond {
		t.Fatalf("attempt3 expected in [160ms,240ms], got %v", d3)
	}
	if d3 <= d2 {
		t.Fatalf("attempt3 should be greater than attempt2, got d2=%v d3=%v", d2, d3)
	}

	d20 := retryDelay(20, base, seed) // capped at 64x base with jitter
	if d20 < 5120*time.Millisecond || d20 > 7680*time.Millisecond {
		t.Fatalf("attempt20 expected in [5.12s,7.68s], got %v", d20)
	}
}

func TestProcessInFlight_UsesRetryDelay(t *testing.T) {
	st := store.NewInMemoryStore()
	m := NewManager(100, 1000, 1048576, "drop_oldest", st)

	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	_, err := m.Create("imei-backoff", c1)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	cmd := queue.Command{
		MessageID: "m-backoff",
		CommandID: 8,
		Priority:  queue.PriorityHigh,
		CreatedAt: time.Now().UTC(),
		TTL:       1 * time.Minute,
		Attempts:  1, // next RegisterInFlight will store Attempts=2
	}
	seq, err := m.NextCommandSeq("imei-backoff")
	if err != nil {
		t.Fatalf("NextCommandSeq() error = %v", err)
	}
	sentAt := time.Now().UTC()
	if err := m.RegisterInFlight("imei-backoff", seq, cmd, sentAt); err != nil {
		t.Fatalf("RegisterInFlight() error = %v", err)
	}

	policy := func(_ string, _ queue.Command) DeliveryPolicy {
		return DeliveryPolicy{
			AckTimeout:   100 * time.Millisecond,
			RetryBackoff: 100 * time.Millisecond,
			MaxRetries:   3,
		}
	}

	entrySeed := jitterSeedForEntry(InFlightEntry{
		Seq:      seq,
		Command:  cmd,
		Attempts: 2,
	})
	wait := 100*time.Millisecond + retryDelay(2, 100*time.Millisecond, entrySeed)

	// Slightly before deadline: must not retry yet.
	outcomes, err := m.ProcessInFlight("imei-backoff", sentAt.Add(wait-10*time.Millisecond), policy)
	if err != nil {
		t.Fatalf("ProcessInFlight() error = %v", err)
	}
	if len(outcomes) != 0 {
		t.Fatalf("expected no outcomes before wait deadline, got %+v", outcomes)
	}

	// Slightly after deadline: should retry.
	outcomes, err = m.ProcessInFlight("imei-backoff", sentAt.Add(wait+10*time.Millisecond), policy)
	if err != nil {
		t.Fatalf("ProcessInFlight() error = %v", err)
	}
	if len(outcomes) != 1 || outcomes[0].Type != "retrying" {
		t.Fatalf("expected one retrying outcome after wait deadline, got %+v", outcomes)
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
