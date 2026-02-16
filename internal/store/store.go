package store

import (
	"encoding/json"
	"os"
	"path/filepath"
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
		return nil, err
	}
	if data == nil {
		data = make(map[string]SessionSnapshot)
	}
	return data, nil
}

func (s *FileStore) writeAllLocked(data map[string]SessionSnapshot) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, raw, 0o644)
}
