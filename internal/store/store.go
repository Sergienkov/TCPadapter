package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"tcpadapter/internal/queue"
)

type SessionSnapshot struct {
	ControllerID string
	LastSeen     time.Time
	BufferFree   int
	FWMode       bool
	SyncMode     bool
	Pending      []queue.Command
	InFlight     []InFlightSnapshot
	NextSeq      uint8
}

type InFlightSnapshot struct {
	Seq      uint8
	Command  queue.Command
	SentAt   time.Time
	Attempts int
}

type Store interface {
	SaveSession(snapshot SessionSnapshot) error
	DeleteSession(controllerID string) error
	ListSessions() ([]SessionSnapshot, error)
}

type InMemoryStore struct {
	mu       sync.Mutex
	sessions map[string]SessionSnapshot
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{sessions: make(map[string]SessionSnapshot)}
}

func (s *InMemoryStore) SaveSession(snapshot SessionSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[snapshot.ControllerID] = snapshot
	return nil
}

func (s *InMemoryStore) DeleteSession(controllerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, controllerID)
	return nil
}

func (s *InMemoryStore) ListSessions() ([]SessionSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SessionSnapshot, 0, len(s.sessions))
	for _, v := range s.sessions {
		out = append(out, v)
	}
	return out, nil
}

type FileStore struct {
	mu   sync.Mutex
	path string
}

const maxCorruptBackups = 5

func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

func (s *FileStore) SaveSession(snapshot SessionSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.readAllLocked()
	if err != nil {
		return err
	}
	data[snapshot.ControllerID] = snapshot
	return s.writeAllLocked(data)
}

func (s *FileStore) DeleteSession(controllerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.readAllLocked()
	if err != nil {
		return err
	}
	delete(data, controllerID)
	return s.writeAllLocked(data)
}

func (s *FileStore) ListSessions() ([]SessionSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.readAllLocked()
	if err != nil {
		return nil, err
	}
	out := make([]SessionSnapshot, 0, len(data))
	for _, v := range data {
		out = append(out, v)
	}
	return out, nil
}

func (s *FileStore) readAllLocked() (map[string]SessionSnapshot, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]SessionSnapshot), nil
		}
		return nil, err
	}
	if len(raw) == 0 {
		return make(map[string]SessionSnapshot), nil
	}
	var data map[string]SessionSnapshot
	if err := json.Unmarshal(raw, &data); err != nil {
		backup := fmt.Sprintf("%s.corrupt-%d", s.path, time.Now().UnixNano())
		_ = os.Rename(s.path, backup)
		_ = cleanupCorruptBackups(s.path, maxCorruptBackups)
		return make(map[string]SessionSnapshot), nil
	}
	if data == nil {
		data = make(map[string]SessionSnapshot)
	}
	return data, nil
}

func (s *FileStore) writeAllLocked(data map[string]SessionSnapshot) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".sessions-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("rename temp store file: %w", err)
	}
	cleanup = false

	// Best effort: fsync directory entry to harden against power loss.
	_ = syncDir(dir)
	return nil
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func cleanupCorruptBackups(basePath string, maxKeep int) error {
	if maxKeep <= 0 {
		return nil
	}
	dir := filepath.Dir(basePath)
	base := filepath.Base(basePath) + ".corrupt-"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	type fileEntry struct {
		name string
		mod  time.Time
	}
	files := make([]fileEntry, 0)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasPrefix(e.Name(), base) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileEntry{name: e.Name(), mod: info.ModTime()})
	}
	if len(files) <= maxKeep {
		return nil
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].mod.Equal(files[j].mod) {
			return files[i].name > files[j].name
		}
		return files[i].mod.After(files[j].mod)
	})

	for _, f := range files[maxKeep:] {
		_ = os.Remove(filepath.Join(dir, f.name))
	}
	return nil
}
