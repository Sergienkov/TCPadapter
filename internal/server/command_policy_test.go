package server

import (
	"testing"

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
