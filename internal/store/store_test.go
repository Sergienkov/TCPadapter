package store

import (
	"os"
	"path/filepath"
	"strings"
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

func TestFileStoreAtomicWriteLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	st := NewFileStore(filepath.Join(dir, "sessions.json"))

	if err := st.SaveSession(SessionSnapshot{
		ControllerID: "imei-atomic",
		LastSeen:     time.Now().UTC(),
		BufferFree:   1000,
	}); err != nil {
		t.Fatalf("SaveSession() error = %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".sessions-") && strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("unexpected temp file left after atomic write: %s", e.Name())
		}
	}
}

func TestFileStoreCorruptStateAutoBackupAndRecover(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	if err := os.WriteFile(path, []byte("{not-json"), 0o644); err != nil {
		t.Fatalf("WriteFile() corrupt seed error = %v", err)
	}

	st := NewFileStore(path)
	all, err := st.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions() on corrupt file should not fail, got %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("expected empty snapshot set after corrupt recovery, got %d", len(all))
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected original corrupt file to be moved away, stat err=%v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	corruptFound := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "sessions.json.corrupt-") {
			corruptFound = true
			break
		}
	}
	if !corruptFound {
		t.Fatalf("expected backup file with prefix sessions.json.corrupt- in %s", dir)
	}

	if err := st.SaveSession(SessionSnapshot{
		ControllerID: "imei-restore",
		LastSeen:     time.Now().UTC(),
		BufferFree:   1234,
	}); err != nil {
		t.Fatalf("SaveSession() after corrupt recovery error = %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected recreated sessions file after save, stat err=%v", err)
	}
}

func TestFileStoreCorruptBackupRetention(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	st := NewFileStore(path)

	for i := 0; i < maxCorruptBackups+3; i++ {
		if err := os.WriteFile(path, []byte("{broken-json"), 0o644); err != nil {
			t.Fatalf("WriteFile() corrupt seed error = %v", err)
		}
		_, err := st.ListSessions()
		if err != nil {
			t.Fatalf("ListSessions() on corrupt file should not fail, got %v", err)
		}
		time.Sleep(1 * time.Millisecond)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	count := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "sessions.json.corrupt-") {
			count++
		}
	}
	if count > maxCorruptBackups {
		t.Fatalf("expected at most %d corrupt backups, got %d", maxCorruptBackups, count)
	}
}
