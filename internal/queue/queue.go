package queue

import (
	"sync"
	"time"
)

type Priority string

const (
	PriorityHigh  Priority = "high"
	PriorityFW    Priority = "fw"
	PrioritySync  Priority = "sync"
	PriorityQuery Priority = "query"
)

type Command struct {
	MessageID string
	TraceID   string
	DedupKey  string
	CommandID uint8
	TTL       time.Duration
	CreatedAt time.Time
	Payload   []byte
	Priority  Priority
	Attempts  int
	RetrySeq  uint8
}

func EstimatedCommandBytes(cmd Command) int {
	// Rough wire budget per command frame:
	// marker(4) + len(2) + ttl(1) + seq(1) + cmd_id(1) + payload + crc(2)
	const frameOverhead = 11
	return frameOverhead + len(cmd.Payload)
}

func (c Command) Expired(now time.Time) bool {
	if c.TTL <= 0 {
		return false
	}
	return now.After(c.CreatedAt.Add(c.TTL))
}

type ControllerQueue struct {
	mu    sync.Mutex
	high  []Command
	fw    []Command
	sync  []Command
	query []Command
}

func NewControllerQueue() *ControllerQueue {
	return &ControllerQueue{}
}

func (q *ControllerQueue) Push(cmd Command) {
	q.mu.Lock()
	defer q.mu.Unlock()

	switch cmd.Priority {
	case PriorityHigh:
		q.high = append(q.high, cmd)
	case PriorityFW:
		q.fw = append(q.fw, cmd)
	case PrioritySync:
		q.sync = append(q.sync, cmd)
	default:
		q.query = append(q.query, cmd)
	}
}

func (q *ControllerQueue) PopNext(now time.Time, fwMode, syncMode bool) (Command, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	pruneExpired := func(in []Command) []Command {
		if len(in) == 0 {
			return in
		}
		out := in[:0]
		for _, c := range in {
			if !c.Expired(now) {
				out = append(out, c)
			}
		}
		return out
	}

	q.high = pruneExpired(q.high)
	q.fw = pruneExpired(q.fw)
	q.sync = pruneExpired(q.sync)
	q.query = pruneExpired(q.query)

	pop := func(buf *[]Command) (Command, bool) {
		if len(*buf) == 0 {
			return Command{}, false
		}
		cmd := (*buf)[0]
		*buf = (*buf)[1:]
		return cmd, true
	}

	if cmd, ok := pop(&q.high); ok {
		return cmd, true
	}

	if fwMode {
		if cmd, ok := pop(&q.fw); ok {
			return cmd, true
		}
		return Command{}, false
	}

	if syncMode {
		if cmd, ok := pop(&q.sync); ok {
			return cmd, true
		}
		return Command{}, false
	}

	if cmd, ok := pop(&q.fw); ok {
		return cmd, true
	}
	if cmd, ok := pop(&q.sync); ok {
		return cmd, true
	}
	if cmd, ok := pop(&q.query); ok {
		return cmd, true
	}
	return Command{}, false
}

func (q *ControllerQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.high) + len(q.fw) + len(q.sync) + len(q.query)
}

func (q *ControllerQueue) Snapshot() []Command {
	q.mu.Lock()
	defer q.mu.Unlock()

	out := make([]Command, 0, len(q.high)+len(q.fw)+len(q.sync)+len(q.query))
	out = append(out, q.high...)
	out = append(out, q.fw...)
	out = append(out, q.sync...)
	out = append(out, q.query...)
	return out
}

func (q *ControllerQueue) Restore(cmds []Command) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.high = nil
	q.fw = nil
	q.sync = nil
	q.query = nil
	for _, cmd := range cmds {
		switch cmd.Priority {
		case PriorityHigh:
			q.high = append(q.high, cmd)
		case PriorityFW:
			q.fw = append(q.fw, cmd)
		case PrioritySync:
			q.sync = append(q.sync, cmd)
		default:
			q.query = append(q.query, cmd)
		}
	}
}

func (q *ControllerQueue) HasFW() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.fw) > 0
}

func (q *ControllerQueue) HasSync() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.sync) > 0
}

func (q *ControllerQueue) DropOldest() (Command, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	type candidate struct {
		buf string
		idx int
		cmd Command
		ok  bool
	}
	pick := candidate{idx: -1}
	consider := func(bufName string, arr []Command) {
		for i, c := range arr {
			if !pick.ok || c.CreatedAt.Before(pick.cmd.CreatedAt) {
				pick = candidate{buf: bufName, idx: i, cmd: c, ok: true}
			}
		}
	}

	consider("high", q.high)
	consider("fw", q.fw)
	consider("sync", q.sync)
	consider("query", q.query)

	if !pick.ok {
		return Command{}, false
	}

	switch pick.buf {
	case "high":
		q.high = removeAt(q.high, pick.idx)
	case "fw":
		q.fw = removeAt(q.fw, pick.idx)
	case "sync":
		q.sync = removeAt(q.sync, pick.idx)
	default:
		q.query = removeAt(q.query, pick.idx)
	}
	return pick.cmd, true
}

func removeAt(in []Command, idx int) []Command {
	if idx < 0 || idx >= len(in) {
		return in
	}
	copy(in[idx:], in[idx+1:])
	return in[:len(in)-1]
}

func (q *ControllerQueue) TotalEstimatedBytes() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	total := 0
	for _, c := range q.high {
		total += EstimatedCommandBytes(c)
	}
	for _, c := range q.fw {
		total += EstimatedCommandBytes(c)
	}
	for _, c := range q.sync {
		total += EstimatedCommandBytes(c)
	}
	for _, c := range q.query {
		total += EstimatedCommandBytes(c)
	}
	return total
}
