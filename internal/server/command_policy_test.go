package server

import (
	"testing"
	"time"

	"tcpadapter/internal/config"
	"tcpadapter/internal/queue"
)

func TestNormalizeCommandPriority(t *testing.T) {
	cmd := queue.Command{CommandID: 13, Priority: queue.PriorityHigh}
	n, err := normalizeCommandPriority(cmd)
	if err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	if n.Priority != queue.PrioritySync {
		t.Fatalf("expected sync priority, got %s", n.Priority)
	}
}

func TestNormalizeCommandPriority_Unsupported(t *testing.T) {
	_, err := normalizeCommandPriority(queue.Command{CommandID: 99, Priority: queue.PriorityHigh})
	if err == nil {
		t.Fatal("expected unsupported command error")
	}
}

func TestCommandAllowedInModes(t *testing.T) {
	if !commandAllowedInModes(20, true, false) {
		t.Fatal("cmd20 must be allowed in fw mode")
	}
	if commandAllowedInModes(14, true, false) {
		t.Fatal("sync cmd must be blocked in fw mode")
	}
	if commandAllowedInModes(2, false, true) {
		t.Fatal("cmd2 must be blocked in sync mode")
	}
	if !commandAllowedInModes(12, false, true) {
		t.Fatal("sync cmd must be allowed in sync mode")
	}
	if !commandAllowedInModes(24, false, true) {
		t.Fatal("cmd24 should be allowed in sync mode")
	}
}

func TestDeliveryPolicyByPriority(t *testing.T) {
	s := &Server{
		cfg: config.Config{
			AckTimeout:        10 * time.Second,
			RetryBackoff:      3 * time.Second,
			MaxRetries:        3,
			AckTimeoutQuery:   2 * time.Second,
			RetryBackoffQuery: 1 * time.Second,
			MaxRetriesQuery:   2,
			AckTimeoutSync:    15 * time.Second,
			RetryBackoffSync:  5 * time.Second,
			MaxRetriesSync:    1,
			AckTimeoutFW:      20 * time.Second,
			RetryBackoffFW:    4 * time.Second,
			MaxRetriesFW:      4,
		},
	}

	q := s.deliveryPolicyForCommand("c1", queue.Command{CommandID: 9, Priority: queue.PriorityQuery})
	if q.AckTimeout != 2*time.Second || q.MaxRetries != 2 {
		t.Fatalf("unexpected query policy: %+v", q)
	}

	sync := s.deliveryPolicyForCommand("c1", queue.Command{CommandID: 13, Priority: queue.PrioritySync})
	if sync.AckTimeout != 15*time.Second || sync.MaxRetries != 1 {
		t.Fatalf("unexpected sync policy: %+v", sync)
	}

	fw := s.deliveryPolicyForCommand("c1", queue.Command{CommandID: 20, Priority: queue.PriorityFW})
	if fw.AckTimeout != 20*time.Second || fw.MaxRetries != 4 {
		t.Fatalf("unexpected fw policy: %+v", fw)
	}

	high := s.deliveryPolicyForCommand("c1", queue.Command{CommandID: 8, Priority: queue.PriorityHigh})
	if high.AckTimeout != 10*time.Second || high.MaxRetries != 3 {
		t.Fatalf("unexpected high policy: %+v", high)
	}
}
