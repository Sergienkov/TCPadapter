package server

import (
	"fmt"
	"time"

	"tcpadapter/internal/protocol"
	"tcpadapter/internal/queue"
	"tcpadapter/internal/session"
)

var canonicalPriority = map[uint8]queue.Priority{
	1:  queue.PriorityHigh,
	2:  queue.PriorityHigh,
	3:  queue.PriorityHigh,
	5:  queue.PriorityHigh,
	6:  queue.PriorityQuery,
	7:  queue.PriorityQuery,
	8:  queue.PriorityHigh,
	9:  queue.PriorityQuery,
	10: queue.PriorityQuery,
	11: queue.PriorityQuery,
	12: queue.PrioritySync,
	13: queue.PrioritySync,
	14: queue.PrioritySync,
	15: queue.PrioritySync,
	16: queue.PrioritySync,
	17: queue.PriorityHigh,
	18: queue.PriorityHigh,
	19: queue.PriorityHigh,
	20: queue.PriorityFW,
	21: queue.PriorityQuery,
	22: queue.PriorityQuery,
	23: queue.PriorityHigh,
	24: queue.PriorityHigh,
	25: queue.PriorityQuery,
	27: queue.PriorityQuery,
}

func canonicalPriorityForCommand(commandID uint8) (queue.Priority, bool) {
	p, ok := canonicalPriority[commandID]
	return p, ok
}

func normalizeCommandPriority(cmd queue.Command) (queue.Command, error) {
	p, ok := canonicalPriorityForCommand(cmd.CommandID)
	if !ok {
		return cmd, fmt.Errorf("unsupported command id: %d", cmd.CommandID)
	}
	cmd.Priority = p
	return cmd, nil
}

func commandAllowedInModes(commandID uint8, fwMode, syncMode bool) bool {
	if fwMode {
		// During FW mode only high-priority commands and FW block command are allowed.
		if commandID == 20 {
			return true
		}
		p, ok := canonicalPriorityForCommand(commandID)
		return ok && p == queue.PriorityHigh
	}

	if syncMode {
		// During sync mode query commands are paused and some high-priority commands are blocked.
		if commandID >= 12 && commandID <= 16 {
			return true
		}
		switch commandID {
		case 2, 3, 17, 18, 19, 20, 23:
			return false
		}
		p, ok := canonicalPriorityForCommand(commandID)
		if !ok {
			return false
		}
		return p == queue.PriorityHigh
	}

	return true
}

func (s *Server) deliveryPolicyForCommand(_ string, cmd queue.Command) session.DeliveryPolicy {
	base := session.DeliveryPolicy{
		AckTimeout:   s.cfg.AckTimeout,
		RetryBackoff: s.cfg.RetryBackoff,
		MaxRetries:   s.cfg.MaxRetries,
	}
	if base.AckTimeout <= 0 {
		base.AckTimeout = 10 * time.Second
	}
	if base.MaxRetries < 0 {
		base.MaxRetries = 0
	}

	if cmd.Priority == queue.PriorityQuery {
		specific := session.DeliveryPolicy{
			AckTimeout:   s.cfg.AckTimeoutQuery,
			RetryBackoff: s.cfg.RetryBackoffQuery,
			MaxRetries:   s.cfg.MaxRetriesQuery,
		}
		return mergeDeliveryPolicy(base, specific)
	}

	if cmd.Priority == queue.PrioritySync {
		specific := session.DeliveryPolicy{
			AckTimeout:   s.cfg.AckTimeoutSync,
			RetryBackoff: s.cfg.RetryBackoffSync,
			MaxRetries:   s.cfg.MaxRetriesSync,
		}
		return mergeDeliveryPolicy(base, specific)
	}

	if cmd.CommandID == protocol.CmdFWStart || cmd.CommandID == protocol.CmdFWBlock || cmd.Priority == queue.PriorityFW {
		specific := session.DeliveryPolicy{
			AckTimeout:   s.cfg.AckTimeoutFW,
			RetryBackoff: s.cfg.RetryBackoffFW,
			MaxRetries:   s.cfg.MaxRetriesFW,
		}
		return mergeDeliveryPolicy(base, specific)
	}

	return base
}

func mergeDeliveryPolicy(base, specific session.DeliveryPolicy) session.DeliveryPolicy {
	// Treat all-zero profile as "unset" (typical in tests with manual Config literal).
	if specific.AckTimeout == 0 && specific.RetryBackoff == 0 && specific.MaxRetries == 0 {
		return base
	}
	merged := base
	if specific.AckTimeout > 0 {
		merged.AckTimeout = specific.AckTimeout
	}
	if specific.RetryBackoff >= 0 {
		merged.RetryBackoff = specific.RetryBackoff
	}
	if specific.MaxRetries >= 0 {
		merged.MaxRetries = specific.MaxRetries
	}
	return merged
}
