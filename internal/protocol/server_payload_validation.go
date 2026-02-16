package protocol

import (
	"encoding/binary"
	"fmt"
)

// ValidateServerCommandPayload validates payload bytes for known server->controller commands.
// The payload here excludes command id byte (it is prepended by transport layer).
func ValidateServerCommandPayload(commandID uint8, payload []byte) error {
	switch commandID {
	case 12:
		// [timestamp=4][count=1][count*608 bytes] (strict fixed block for settings)
		if len(payload) < 5 {
			return fmt.Errorf("cmd 12: payload too short: %d", len(payload))
		}
		count := int(payload[4])
		if count < 1 || count > 1 {
			return fmt.Errorf("cmd 12: count must be 1")
		}
		const itemSize = 608
		expected := 5 + count*itemSize
		if len(payload) != expected {
			return fmt.Errorf("cmd 12: invalid payload size: got=%d want=%d", len(payload), expected)
		}
		return nil
	case 13:
		// [timestamp=4][count=1][count*32]
		if len(payload) < 5 {
			return fmt.Errorf("cmd 13: payload too short: %d", len(payload))
		}
		count := int(payload[4])
		if count < 1 || count > 250 {
			return fmt.Errorf("cmd 13: count out of range: %d", count)
		}
		expected := 5 + count*32
		if len(payload) != expected {
			return fmt.Errorf("cmd 13: invalid payload size: got=%d want=%d", len(payload), expected)
		}
		return nil
	case 14:
		// [timestamp=4][count=1][count*32]
		if len(payload) < 5 {
			return fmt.Errorf("cmd 14: payload too short: %d", len(payload))
		}
		count := int(payload[4])
		if count < 1 || count > 64 {
			return fmt.Errorf("cmd 14: count out of range: %d", count)
		}
		expected := 5 + count*32
		if len(payload) != expected {
			return fmt.Errorf("cmd 14: invalid payload size: got=%d want=%d", len(payload), expected)
		}
		return nil
	case 15:
		// [timestamp=4][count=1][count*16]
		if len(payload) < 5 {
			return fmt.Errorf("cmd 15: payload too short: %d", len(payload))
		}
		count := int(payload[4])
		if count < 1 || count > 64 {
			return fmt.Errorf("cmd 15: count out of range: %d", count)
		}
		expected := 5 + count*16
		if len(payload) != expected {
			return fmt.Errorf("cmd 15: invalid payload size: got=%d want=%d", len(payload), expected)
		}
		return nil
	case 16:
		// [timestamp=4][count=1][count*4]
		if len(payload) < 5 {
			return fmt.Errorf("cmd 16: payload too short: %d", len(payload))
		}
		count := int(payload[4])
		if count < 1 || count > 255 {
			return fmt.Errorf("cmd 16: count out of range: %d", count)
		}
		expected := 5 + count*4
		if len(payload) != expected {
			return fmt.Errorf("cmd 16: invalid payload size: got=%d want=%d", len(payload), expected)
		}
		return nil
	case 17:
		if len(payload) != 0 {
			return fmt.Errorf("cmd 17: payload must be empty")
		}
		return nil
	case 18:
		if len(payload) != 1 {
			return fmt.Errorf("cmd 18: payload must be 1 byte")
		}
		block := payload[0]
		if block > 100 {
			return fmt.Errorf("cmd 18: block out of range: %d", block)
		}
		return nil
	case 19:
		// [fw=2][build=4][size=4][crc=4][blocks=2]
		if len(payload) != 16 {
			return fmt.Errorf("cmd 19: payload must be 16 bytes, got=%d", len(payload))
		}
		fwNum := binary.LittleEndian.Uint16(payload[0:2])
		if fwNum == 0 {
			return fmt.Errorf("cmd 19: fw number cannot be 0")
		}
		blocks := binary.LittleEndian.Uint16(payload[14:16])
		if blocks == 0 {
			return fmt.Errorf("cmd 19: blocks cannot be 0")
		}
		return nil
	case 20:
		// [block=2][data=1024]
		if len(payload) != 1026 {
			return fmt.Errorf("cmd 20: payload must be 1026 bytes, got=%d", len(payload))
		}
		return nil
	case 21:
		if len(payload) != 0 {
			return fmt.Errorf("cmd 21: payload must be empty")
		}
		return nil
	case 22:
		if len(payload) != 0 {
			return fmt.Errorf("cmd 22: payload must be empty")
		}
		return nil
	case 23:
		// server time as uint32
		if len(payload) != 4 {
			return fmt.Errorf("cmd 23: payload must be 4 bytes, got=%d", len(payload))
		}
		return nil
	case 24:
		// [command_seq=1][execution_code=1]
		if len(payload) != 2 {
			return fmt.Errorf("cmd 24: payload must be 2 bytes")
		}
		return nil
	case 27:
		// [custom_cmd=1][arg_size=2][arg=N]
		if len(payload) < 3 {
			return fmt.Errorf("cmd 27: payload too short: %d", len(payload))
		}
		size := int(binary.LittleEndian.Uint16(payload[1:3]))
		if len(payload[3:]) != size {
			return fmt.Errorf("cmd 27: arg size mismatch: got=%d want=%d", len(payload[3:]), size)
		}
		return nil
	default:
		// Unknown command id: do not block transport layer yet.
		return nil
	}
}
