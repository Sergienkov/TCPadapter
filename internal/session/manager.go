package session

import (
	"errors"
	"net"
	"sort"
	"sync"
	"time"

	"tcpadapter/internal/queue"
	"tcpadapter/internal/store"
)

var (
	ErrSessionExists   = errors.New("controller session already exists")
	ErrSessionNotFound = errors.New("controller session not found")
	ErrDuplicate       = errors.New("duplicate command")
	ErrQueueFull       = errors.New("controller queue is full")
	ErrQueueBytesFull  = errors.New("controller queue bytes limit exceeded")
)

type Session struct {
	ControllerID string
	Conn         net.Conn
	Queue        *queue.ControllerQueue
	LastSeen     time.Time
	BufferFree   int
	FWMode       bool
	SyncMode     bool
	Dedup        map[string]time.Time
	InFlight     map[uint8]InFlightEntry
	NextSeq      uint8
}

type DeliveryContext struct {
	ControllerID string
	Conn         net.Conn
	Queue        *queue.ControllerQueue
	BufferFree   int
	FWMode       bool
	SyncMode     bool
}

type InFlightEntry struct {
	Seq      uint8
	Command  queue.Command
	SentAt   time.Time
	Attempts int
}

type InFlightOutcome struct {
	Type       string
	Seq        uint8
	Command    queue.Command
	Attempts   int
	Controller uint8
	Reason     string
}

type Manager struct {
	mu             sync.RWMutex
	sessions       map[string]*Session
	maxConns       int
	maxQueueDepth  int
	maxQueueBytes  int
	overflowPolicy string
	store          store.Store
}

type OverflowEvent struct {
	ControllerID string
	LimitType    string // depth|bytes
	Policy       string // reject|drop_oldest
	Action       string // rejected|dropped
}

type Stats struct {
	SessionCount       int
	QueueDepthSum      int
	QueueDepthMax      int
	QueueBytesSum      int
	QueueBytesMax      int
	InFlightCountSum   int
	InFlightCountMax   int
	OnlineSessionCount int
}

type ControllerQueueStat struct {
	ControllerID string    `json:"controller_id"`
	QueueDepth   int       `json:"queue_depth"`
	QueueBytes   int       `json:"queue_bytes"`
	InFlight     int       `json:"in_flight"`
	Online       bool      `json:"online"`
	LastSeen     time.Time `json:"last_seen"`
}

func NewManager(maxConns, maxQueueDepth, maxQueueBytes int, overflowPolicy string, st store.Store) *Manager {
	if overflowPolicy == "" {
		overflowPolicy = "drop_oldest"
	}
	return &Manager{
		sessions:       make(map[string]*Session),
		maxConns:       maxConns,
		maxQueueDepth:  maxQueueDepth,
		maxQueueBytes:  maxQueueBytes,
		overflowPolicy: overflowPolicy,
		store:          st,
	}
}

func (m *Manager) Create(controllerID string, conn net.Conn) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, exists := m.sessions[controllerID]; exists {
		if existing.Conn != nil {
			return nil, ErrSessionExists
		}
		existing.Conn = conn
		existing.LastSeen = time.Now().UTC()
		if existing.BufferFree <= 0 {
			existing.BufferFree = 1500
		}
		_ = m.persistLocked(existing)
		return existing, nil
	}
	if m.onlineConnectionsLocked() >= m.maxConns {
		return nil, errors.New("max connections reached")
	}
	now := time.Now().UTC()
	s := &Session{
		ControllerID: controllerID,
		Conn:         conn,
		Queue:        queue.NewControllerQueue(),
		LastSeen:     now,
		BufferFree:   1500,
		Dedup:        make(map[string]time.Time),
		InFlight:     make(map[uint8]InFlightEntry),
		NextSeq:      0,
	}
	m.sessions[controllerID] = s
	_ = m.persistLocked(s)
	return s, nil
}

func (m *Manager) Delete(controllerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, controllerID)
	_ = m.store.DeleteSession(controllerID)
}

func (m *Manager) MarkOffline(controllerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[controllerID]; ok {
		s.Conn = nil
		s.LastSeen = time.Now().UTC()
		_ = m.persistLocked(s)
	}
}

func (m *Manager) Get(controllerID string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[controllerID]
	return s, ok
}

func (m *Manager) Touch(controllerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[controllerID]; ok {
		now := time.Now().UTC()
		s.LastSeen = now
		_ = m.persistLocked(s)
	}
}

func (m *Manager) Enqueue(controllerID string, cmd queue.Command) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.enqueueLocked(controllerID, cmd, nil)
}

func (m *Manager) EnqueueWithEvents(controllerID string, cmd queue.Command) ([]OverflowEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	events := make([]OverflowEvent, 0)
	err := m.enqueueLocked(controllerID, cmd, &events)
	return events, err
}

func (m *Manager) enqueueLocked(controllerID string, cmd queue.Command, events *[]OverflowEvent) error {
	s, ok := m.sessions[controllerID]
	if !ok {
		s = m.createDetachedLocked(controllerID)
	}

	if cmd.DedupKey != "" {
		if t, exists := s.Dedup[cmd.DedupKey]; exists {
			if time.Since(t) < 10*time.Minute {
				return ErrDuplicate
			}
		}
		s.Dedup[cmd.DedupKey] = time.Now().UTC()
	}

	fits := func() bool {
		if m.maxQueueDepth > 0 && s.Queue.Len()+1 > m.maxQueueDepth {
			return false
		}
		if m.maxQueueBytes > 0 && s.Queue.TotalEstimatedBytes()+queue.EstimatedCommandBytes(cmd) > m.maxQueueBytes {
			return false
		}
		return true
	}

	if !fits() {
		bytesExceeded := m.maxQueueBytes > 0 && s.Queue.TotalEstimatedBytes()+queue.EstimatedCommandBytes(cmd) > m.maxQueueBytes
		depthExceeded := m.maxQueueDepth > 0 && s.Queue.Len()+1 > m.maxQueueDepth
		switch m.overflowPolicy {
		case "reject":
			if bytesExceeded {
				if events != nil {
					*events = append(*events, OverflowEvent{
						ControllerID: controllerID,
						LimitType:    "bytes",
						Policy:       "reject",
						Action:       "rejected",
					})
				}
				return ErrQueueBytesFull
			}
			if depthExceeded && events != nil {
				*events = append(*events, OverflowEvent{
					ControllerID: controllerID,
					LimitType:    "depth",
					Policy:       "reject",
					Action:       "rejected",
				})
			}
			return ErrQueueFull
		case "drop_oldest":
			for !fits() {
				limitType := "depth"
				if m.maxQueueBytes > 0 && s.Queue.TotalEstimatedBytes()+queue.EstimatedCommandBytes(cmd) > m.maxQueueBytes {
					limitType = "bytes"
				}
				if _, ok := s.Queue.DropOldest(); !ok {
					if m.maxQueueBytes > 0 && queue.EstimatedCommandBytes(cmd) > m.maxQueueBytes {
						if events != nil {
							*events = append(*events, OverflowEvent{
								ControllerID: controllerID,
								LimitType:    "bytes",
								Policy:       "drop_oldest",
								Action:       "rejected",
							})
						}
						return ErrQueueBytesFull
					}
					if events != nil {
						*events = append(*events, OverflowEvent{
							ControllerID: controllerID,
							LimitType:    limitType,
							Policy:       "drop_oldest",
							Action:       "rejected",
						})
					}
					return ErrQueueFull
				}
				if events != nil {
					*events = append(*events, OverflowEvent{
						ControllerID: controllerID,
						LimitType:    limitType,
						Policy:       "drop_oldest",
						Action:       "dropped",
					})
				}
			}
		default:
			return ErrQueueFull
		}
	}

	s.Queue.Push(cmd)
	_ = m.persistLocked(s)
	return nil
}

func (m *Manager) createDetachedLocked(controllerID string) *Session {
	now := time.Now().UTC()
	s := &Session{
		ControllerID: controllerID,
		Conn:         nil,
		Queue:        queue.NewControllerQueue(),
		LastSeen:     now,
		BufferFree:   1500,
		Dedup:        make(map[string]time.Time),
		InFlight:     make(map[uint8]InFlightEntry),
		NextSeq:      0,
	}
	m.sessions[controllerID] = s
	_ = m.persistLocked(s)
	return s
}

func (m *Manager) UpdateBufferFree(controllerID string, free int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[controllerID]; ok {
		s.BufferFree = free
		_ = m.persistLocked(s)
	}
}

func (m *Manager) SetFWMode(controllerID string, enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[controllerID]; ok {
		s.FWMode = enabled
		_ = m.persistLocked(s)
	}
}

func (m *Manager) SetSyncMode(controllerID string, enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[controllerID]; ok {
		s.SyncMode = enabled
		_ = m.persistLocked(s)
	}
}

func (m *Manager) DeliveryContext(controllerID string) (DeliveryContext, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[controllerID]
	if !ok {
		return DeliveryContext{}, false
	}
	return DeliveryContext{
		ControllerID: s.ControllerID,
		Conn:         s.Conn,
		Queue:        s.Queue,
		BufferFree:   s.BufferFree,
		FWMode:       s.FWMode,
		SyncMode:     s.SyncMode,
	}, true
}

func (m *Manager) AckSent(controllerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[controllerID]; ok {
		_ = m.persistLocked(s)
	}
}

func (m *Manager) Restore() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	snapshots, err := m.store.ListSessions()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, snap := range snapshots {
		if snap.ControllerID == "" {
			continue
		}
		q := queue.NewControllerQueue()
		q.Restore(snap.Pending)
		inFlight := make(map[uint8]InFlightEntry, len(snap.InFlight))
		for _, v := range snap.InFlight {
			inFlight[v.Seq] = InFlightEntry{
				Seq:      v.Seq,
				Command:  v.Command,
				SentAt:   v.SentAt,
				Attempts: v.Attempts,
			}
		}
		m.sessions[snap.ControllerID] = &Session{
			ControllerID: snap.ControllerID,
			Conn:         nil,
			Queue:        q,
			LastSeen:     snap.LastSeen,
			BufferFree:   snap.BufferFree,
			FWMode:       snap.FWMode,
			SyncMode:     snap.SyncMode,
			Dedup:        make(map[string]time.Time),
			InFlight:     inFlight,
			NextSeq:      snap.NextSeq,
		}
		if m.sessions[snap.ControllerID].BufferFree <= 0 {
			m.sessions[snap.ControllerID].BufferFree = 1500
		}
		count++
	}
	return count, nil
}

func (m *Manager) onlineConnectionsLocked() int {
	n := 0
	for _, s := range m.sessions {
		if s.Conn != nil {
			n++
		}
	}
	return n
}

func (m *Manager) persistLocked(s *Session) error {
	inFlight := make([]store.InFlightSnapshot, 0, len(s.InFlight))
	for _, v := range s.InFlight {
		inFlight = append(inFlight, store.InFlightSnapshot{
			Seq:      v.Seq,
			Command:  v.Command,
			SentAt:   v.SentAt,
			Attempts: v.Attempts,
		})
	}
	return m.store.SaveSession(store.SessionSnapshot{
		ControllerID: s.ControllerID,
		LastSeen:     s.LastSeen,
		BufferFree:   s.BufferFree,
		FWMode:       s.FWMode,
		SyncMode:     s.SyncMode,
		Pending:      s.Queue.Snapshot(),
		InFlight:     inFlight,
		NextSeq:      s.NextSeq,
	})
}

func (m *Manager) NextCommandSeq(controllerID string) (uint8, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[controllerID]
	if !ok {
		return 0, ErrSessionNotFound
	}
	s.NextSeq++
	seq := s.NextSeq
	_ = m.persistLocked(s)
	return seq, nil
}

func (m *Manager) ControllerIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		out = append(out, id)
	}
	return out
}

func (m *Manager) SessionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

func (m *Manager) SnapshotStats() Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var st Stats
	st.SessionCount = len(m.sessions)
	for _, s := range m.sessions {
		depth := s.Queue.Len()
		bytes := s.Queue.TotalEstimatedBytes()
		inFlight := len(s.InFlight)

		st.QueueDepthSum += depth
		if depth > st.QueueDepthMax {
			st.QueueDepthMax = depth
		}
		st.QueueBytesSum += bytes
		if bytes > st.QueueBytesMax {
			st.QueueBytesMax = bytes
		}
		st.InFlightCountSum += inFlight
		if inFlight > st.InFlightCountMax {
			st.InFlightCountMax = inFlight
		}
		if s.Conn != nil {
			st.OnlineSessionCount++
		}
	}
	return st
}

func (m *Manager) TopQueueStats(limit int) []ControllerQueueStat {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if limit <= 0 {
		limit = 20
	}

	out := make([]ControllerQueueStat, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, ControllerQueueStat{
			ControllerID: s.ControllerID,
			QueueDepth:   s.Queue.Len(),
			QueueBytes:   s.Queue.TotalEstimatedBytes(),
			InFlight:     len(s.InFlight),
			Online:       s.Conn != nil,
			LastSeen:     s.LastSeen,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].QueueDepth != out[j].QueueDepth {
			return out[i].QueueDepth > out[j].QueueDepth
		}
		if out[i].InFlight != out[j].InFlight {
			return out[i].InFlight > out[j].InFlight
		}
		return out[i].ControllerID < out[j].ControllerID
	})

	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (m *Manager) RegisterInFlight(controllerID string, seq uint8, cmd queue.Command, sentAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[controllerID]
	if !ok {
		return ErrSessionNotFound
	}
	attempts := cmd.Attempts + 1
	s.InFlight[seq] = InFlightEntry{
		Seq:      seq,
		Command:  cmd,
		SentAt:   sentAt,
		Attempts: attempts,
	}
	_ = m.persistLocked(s)
	return nil
}

func (m *Manager) AckInFlight(controllerID string, seq uint8) (queue.Command, int, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[controllerID]
	if !ok {
		return queue.Command{}, 0, false
	}
	entry, ok := s.InFlight[seq]
	if !ok {
		return queue.Command{}, 0, false
	}
	delete(s.InFlight, seq)
	_ = m.persistLocked(s)
	return entry.Command, entry.Attempts, true
}

func (m *Manager) ProcessInFlight(controllerID string, now time.Time, ackTimeout, retryBackoff time.Duration, maxRetries int) ([]InFlightOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[controllerID]
	if !ok {
		return nil, ErrSessionNotFound
	}

	out := make([]InFlightOutcome, 0)
	for seq, entry := range s.InFlight {
		wait := ackTimeout + time.Duration(entry.Attempts-1)*retryBackoff
		if wait < 0 {
			wait = ackTimeout
		}
		if now.Before(entry.SentAt.Add(wait)) {
			continue
		}

		delete(s.InFlight, seq)
		cmd := entry.Command
		if cmd.Expired(now) {
			out = append(out, InFlightOutcome{
				Type:     "expired",
				Seq:      seq,
				Command:  cmd,
				Attempts: entry.Attempts,
				Reason:   "ttl expired while waiting ack",
			})
			continue
		}

		if entry.Attempts >= maxRetries+1 {
			out = append(out, InFlightOutcome{
				Type:     "failed",
				Seq:      seq,
				Command:  cmd,
				Attempts: entry.Attempts,
				Reason:   "ack timeout retries exceeded",
			})
			continue
		}

		cmd.Attempts = entry.Attempts
		s.Queue.Push(cmd)
		out = append(out, InFlightOutcome{
			Type:     "retrying",
			Seq:      seq,
			Command:  cmd,
			Attempts: entry.Attempts,
			Reason:   "ack timeout",
		})
	}

	_ = m.persistLocked(s)
	return out, nil
}
