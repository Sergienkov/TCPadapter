package server

import (
	"fmt"

	"tcpadapter/internal/queue"
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
