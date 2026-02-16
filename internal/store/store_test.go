package store

import (
	"path/filepath"
	"testing"
	"time"

	"tcpadapter/internal/queue"
)

func TestFileStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := NewFileStore(filepath.Join(dir, "sessions.json"))

	snap := SessionSnapshot{
		ControllerID: "imei-1",
		LastSeen:     time.Unix(1700000000, 0).UTC(),
		BufferFree:   900,
		FWMode:       true,
		SyncMode:     false,
		Pending: []queue.Command{{
			MessageID: "m1",
			CommandID: 19,
			Priority:  queue.PriorityFW,
			CreatedAt: time.Unix(1700000000, 0).UTC(),
		}},
	}

	if err := st.SaveSession(snap); err != nil {
		t.Fatalf("SaveSession() error = %v", err)
	}

	all, err := st.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(all))
	}
	if all[0].ControllerID != "imei-1" || len(all[0].Pending) != 1 {
		t.Fatalf("unexpected snapshot: %+v", all[0])
	}

	if err := st.DeleteSession("imei-1"); err != nil {
		t.Fatalf("DeleteSession() error = %v", err)
	}
	all, err = st.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("expected empty snapshots, got %d", len(all))
	}
}
