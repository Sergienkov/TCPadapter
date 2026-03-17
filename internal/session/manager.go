package session

import (
	"errors"
	"net"
	"sort"
	"sync"
	"time"

	"tcpadapter/internal/protocol"
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
	ControllerID           string
	Conn                   net.Conn
	Queue                  *queue.ControllerQueue
	LastSeen               time.Time
	lastTouchedPersistedAt time.Time
	BufferFree             int
	FWMode                 bool
	SyncMode               bool
	Dedup                  map[string]time.Time
	InFlight               map[uint8]InFlightEntry
	NextSeq                uint8
	FrameMode              protocol.FrameMode
}

type DeliveryContext struct {
	ControllerID string
	Conn         net.Conn
	Queue        *queue.ControllerQueue
	BufferFree   int
	FWMode       bool
	SyncMode     bool
	FrameMode    protocol.FrameMode
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

type DeliveryPolicy struct {
	AckTimeout   time.Duration
	RetryBackoff time.Duration
	MaxRetries   int
}

type DeliveryPolicyResolver func(controllerID string, cmd queue.Command) DeliveryPolicy

type Manager struct {
	mu             sync.RWMutex
	sessions       map[string]*Session
	maxConns       int
	maxQueueDepth  int
	maxQueueBytes  int
	overflowPolicy string
	store          store.Store
}

const touchPersistInterval = 5 * time.Second

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

type ControllerSessionStat struct {
	ControllerID string    `json:"controller_id"`
	RemoteAddr   string    `json:"remote_addr"`
	QueueDepth   int       `json:"queue_depth"`
	QueueBytes   int       `json:"queue_bytes"`
	InFlight     int       `json:"in_flight"`
	Online       bool      `json:"online"`
	LastSeen     time.Time `json:"last_seen"`
	BufferFree   int       `json:"buffer_free"`
	FWMode       bool      `json:"fw_mode"`
	SyncMode     bool      `json:"sync_mode"`
	NextSeq      uint8     `json:"next_seq"`
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
		prevConn := existing.Conn
		prevLastSeen := existing.LastSeen
		existing.Conn = conn
		existing.LastSeen = time.Now().UTC()
		if existing.BufferFree <= 0 {
			existing.BufferFree = 1500
		}
		if err := m.persistLocked(existing); err != nil {
			existing.Conn = prevConn
			existing.LastSeen = prevLastSeen
			return nil, err
		}
		return existing, nil
	}
	if m.onlineConnectionsLocked() >= m.maxConns {
		return nil, errors.New("max connections reached")
	}
	now := time.Now().UTC()
	s := &Session{
		ControllerID:           controllerID,
		Conn:                   conn,
		Queue:                  queue.NewControllerQueue(),
		LastSeen:               now,
		lastTouchedPersistedAt: now,
		BufferFree:             1500,
		Dedup:                  make(map[string]time.Time),
		InFlight:               make(map[uint8]InFlightEntry),
		NextSeq:                0,
		FrameMode:              protocol.FrameModeSequenced,
	}
	m.sessions[controllerID] = s
	if err := m.persistLocked(s); err != nil {
		delete(m.sessions, controllerID)
		return nil, err
	}
	return s, nil
}

func (m *Manager) Delete(controllerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, controllerID)
	_ = m.store.DeleteSession(controllerID)
}

func (m *Manager) MarkOffline(controllerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[controllerID]; ok {
		s.Conn = nil
		s.LastSeen = time.Now().UTC()
		return m.persistLocked(s)
	}
	return nil
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
		if now.Sub(s.lastTouchedPersistedAt) < touchPersistInterval {
			return
		}
		s.lastTouchedPersistedAt = now
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
		var err error
		s, err = m.createDetachedLocked(controllerID)
		if err != nil {
			return err
		}
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
	return m.persistLocked(s)
}

func (m *Manager) createDetachedLocked(controllerID string) (*Session, error) {
	now := time.Now().UTC()
	s := &Session{
		ControllerID:           controllerID,
		Conn:                   nil,
		Queue:                  queue.NewControllerQueue(),
		LastSeen:               now,
		lastTouchedPersistedAt: now,
		BufferFree:             1500,
		Dedup:                  make(map[string]time.Time),
		InFlight:               make(map[uint8]InFlightEntry),
		NextSeq:                0,
		FrameMode:              protocol.FrameModeSequenced,
	}
	m.sessions[controllerID] = s
	if err := m.persistLocked(s); err != nil {
		delete(m.sessions, controllerID)
		return nil, err
	}
	return s, nil
}

func (m *Manager) UpdateBufferFree(controllerID string, free int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[controllerID]; ok {
		s.BufferFree = free
	}
}

func (m *Manager) SetFWMode(controllerID string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[controllerID]; ok {
		s.FWMode = enabled
		return m.persistLocked(s)
	}
	return nil
}

func (m *Manager) SetSyncMode(controllerID string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[controllerID]; ok {
		s.SyncMode = enabled
		return m.persistLocked(s)
	}
	return nil
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
		FrameMode:    s.FrameMode,
	}, true
}

func (m *Manager) SetFrameMode(controllerID string, mode protocol.FrameMode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[controllerID]; ok {
		s.FrameMode = mode
	}
}

func (m *Manager) AckSent(controllerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[controllerID]; ok {
		return m.persistLocked(s)
	}
	return nil
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
			ControllerID:           snap.ControllerID,
			Conn:                   nil,
			Queue:                  q,
			LastSeen:               snap.LastSeen,
			lastTouchedPersistedAt: snap.LastSeen,
			BufferFree:             snap.BufferFree,
			FWMode:                 snap.FWMode,
			SyncMode:               snap.SyncMode,
			Dedup:                  make(map[string]time.Time),
			InFlight:               inFlight,
			NextSeq:                snap.NextSeq,
			FrameMode:              protocol.FrameModeSequenced,
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

func (m *Manager) InFlightSeqs(controllerID string) []uint8 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[controllerID]
	if !ok || len(s.InFlight) == 0 {
		return nil
	}
	out := make([]uint8, 0, len(s.InFlight))
	for seq := range s.InFlight {
		out = append(out, seq)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (m *Manager) InFlightCount(controllerID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[controllerID]
	if !ok {
		return 0
	}
	return len(s.InFlight)
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

func (m *Manager) SessionStats(limit int) []ControllerSessionStat {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]ControllerSessionStat, 0, len(m.sessions))
	for _, s := range m.sessions {
		remote := ""
		if s.Conn != nil && s.Conn.RemoteAddr() != nil {
			remote = s.Conn.RemoteAddr().String()
		}
		out = append(out, ControllerSessionStat{
			ControllerID: s.ControllerID,
			RemoteAddr:   remote,
			QueueDepth:   s.Queue.Len(),
			QueueBytes:   s.Queue.TotalEstimatedBytes(),
			InFlight:     len(s.InFlight),
			Online:       s.Conn != nil,
			LastSeen:     s.LastSeen,
			BufferFree:   s.BufferFree,
			FWMode:       s.FWMode,
			SyncMode:     s.SyncMode,
			NextSeq:      s.NextSeq,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online
		}
		if out[i].QueueDepth != out[j].QueueDepth {
			return out[i].QueueDepth > out[j].QueueDepth
		}
		if !out[i].LastSeen.Equal(out[j].LastSeen) {
			return out[i].LastSeen.After(out[j].LastSeen)
		}
		return out[i].ControllerID < out[j].ControllerID
	})

	if limit > 0 && len(out) > limit {
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
	return m.persistLocked(s)
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
	if err := m.persistLocked(s); err != nil {
		s.InFlight[seq] = entry
		return queue.Command{}, 0, false
	}
	return entry.Command, entry.Attempts, true
}

func (m *Manager) PeekInFlight(controllerID string, seq uint8) (queue.Command, int, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[controllerID]
	if !ok {
		return queue.Command{}, 0, false
	}
	entry, ok := s.InFlight[seq]
	if !ok {
		return queue.Command{}, 0, false
	}
	return entry.Command, entry.Attempts, true
}

func (m *Manager) ProcessInFlight(controllerID string, now time.Time, resolver DeliveryPolicyResolver) ([]InFlightOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[controllerID]
	if !ok {
		return nil, ErrSessionNotFound
	}

	out := make([]InFlightOutcome, 0)
	for seq, entry := range s.InFlight {
		policy := DeliveryPolicy{
			AckTimeout:   10 * time.Second,
			RetryBackoff: 0,
			MaxRetries:   3,
		}
		if resolver != nil {
			policy = resolver(controllerID, entry.Command)
		}
		if policy.AckTimeout <= 0 {
			policy.AckTimeout = 10 * time.Second
		}
		if policy.MaxRetries < 0 {
			policy.MaxRetries = 0
		}

		ackTimeout := policy.AckTimeout
		retryBackoff := policy.RetryBackoff
		maxRetries := policy.MaxRetries
		wait := ackTimeout + retryDelay(entry.Attempts, retryBackoff, jitterSeedForEntry(entry))
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
			// Allow a short grace window for late terminal ACKs before final fail.
			if now.Before(entry.SentAt.Add(wait + lateAckGrace)) {
				s.InFlight[seq] = entry
				continue
			}
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
		cmd.RetrySeq = seq
		s.Queue.Push(cmd)
		out = append(out, InFlightOutcome{
			Type:     "retrying",
			Seq:      seq,
			Command:  cmd,
			Attempts: entry.Attempts,
			Reason:   "ack timeout",
		})
	}

	return out, m.persistLocked(s)
}

const (
	maxRetryBackoffShift = 6 // cap at 64x of base
	jitterPercent        = 20
	lateAckGrace         = 2 * time.Second
)

func retryDelay(attempts int, base time.Duration, seed uint64) time.Duration {
	if attempts <= 1 || base <= 0 {
		return 0
	}

	shift := attempts - 2
	if shift < 0 {
		shift = 0
	}
	if shift > maxRetryBackoffShift {
		shift = maxRetryBackoffShift
	}

	// Exponential backoff from attempt #2 and onward.
	delay := base * time.Duration(1<<shift)
	if delay < 0 {
		return base
	}

	// Deterministic jitter (+/-20%) keeps tests stable and reduces thundering herd.
	jitterRange := delay * jitterPercent / 100
	if jitterRange <= 0 {
		return delay
	}

	span := int64(jitterRange)*2 + 1
	if span <= 0 {
		return delay
	}

	v := int64(xorshift64(seed)%uint64(span)) - int64(jitterRange)
	out := delay + time.Duration(v)
	if out < 0 {
		return 0
	}
	return out
}

func jitterSeedForEntry(entry InFlightEntry) uint64 {
	// Stable per in-flight command attempt.
	return (uint64(entry.Seq) << 32) | (uint64(entry.Attempts&0xffff) << 16) | uint64(entry.Command.CommandID)
}

func xorshift64(x uint64) uint64 {
	if x == 0 {
		x = 0x9e3779b97f4a7c15
	}
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	return x
}
