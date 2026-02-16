package protocol

import (
	"encoding/binary"
	"fmt"
)

// ValidateServerCommandPayload validates payload bytes for known server->controller commands.
// The payload here excludes command id byte (it is prepended by transport layer).
func ValidateServerCommandPayload(commandID uint8, payload []byte) error {
	switch commandID {
	case 1:
		// [execution_code=1][server_time=4]
		if len(payload) != 5 {
			return fmt.Errorf("cmd 1: payload must be 5 bytes, got=%d", len(payload))
		}
		if payload[0] > 5 {
			return fmt.Errorf("cmd 1: execution code out of range: %d", payload[0])
		}
		return nil
	case 2:
		if len(payload) != 1 {
			return fmt.Errorf("cmd 2: payload must be 1 byte, got=%d", len(payload))
		}
		return nil
	case 3:
		if len(payload) != 1 {
			return fmt.Errorf("cmd 3: payload must be 1 byte, got=%d", len(payload))
		}
		return nil
	case 5:
		if len(payload) != 16 {
			return fmt.Errorf("cmd 5: payload must be 16 bytes, got=%d", len(payload))
		}
		for i := 0; i < 8; i++ {
			if !isBoolByte(payload[i]) {
				return fmt.Errorf("cmd 5: flag %d must be 0 or 1", i)
			}
		}
		for i := 8; i < 16; i++ {
			if payload[i] != 0 {
				return fmt.Errorf("cmd 5: reserved byte %d must be 0", i)
			}
		}
		return nil
	case 6:
		// [capture_timeout=1][interface=1][network_address=1]
		if len(payload) != 3 {
			return fmt.Errorf("cmd 6: payload must be 3 bytes, got=%d", len(payload))
		}
		if payload[1] > 3 {
			return fmt.Errorf("cmd 6: interface out of range: %d", payload[1])
		}
		if payload[1] == 3 {
			if payload[2] == 0 {
				return fmt.Errorf("cmd 6: network address must be 1..255 for interface 485")
			}
		} else if payload[2] != 0 {
			return fmt.Errorf("cmd 6: network address must be 0 for non-485 interface")
		}
		return nil
	case 7:
		if len(payload) != 1 {
			return fmt.Errorf("cmd 7: payload must be 1 byte, got=%d", len(payload))
		}
		if payload[0] == 0 {
			return fmt.Errorf("cmd 7: camera address 0 is reserved")
		}
		return nil
	case 8:
		// [source=1][output1=1][output2=1]
		if len(payload) != 3 {
			return fmt.Errorf("cmd 8: payload must be 3 bytes, got=%d", len(payload))
		}
		if payload[0] == 0 {
			return fmt.Errorf("cmd 8: source id 0 is reserved")
		}
		if payload[1] > 1 || payload[2] > 1 {
			return fmt.Errorf("cmd 8: output states must be 0 or 1")
		}
		return nil
	case 9:
		if len(payload) != 0 {
			return fmt.Errorf("cmd 9: payload must be empty")
		}
		return nil
	case 10:
		if len(payload) != 0 {
			return fmt.Errorf("cmd 10: payload must be empty")
		}
		return nil
	case 11:
		// [phone=10][message=50]
		if len(payload) != 60 {
			return fmt.Errorf("cmd 11: payload must be 60 bytes, got=%d", len(payload))
		}
		phone := payload[:10]
		for i := range phone {
			if phone[i] < '0' || phone[i] > '9' {
				return fmt.Errorf("cmd 11: phone must contain only digits")
			}
		}
		return nil
	case 12:
		// [timestamp=4][count=1][count*608 bytes] (strict fixed block for settings)
		if len(payload) < 5 {
			return fmt.Errorf("cmd 12: payload too short: %d", len(payload))
		}
		count := int(payload[4])
		if count < 1 || count > 1 {
			return fmt.Errorf("cmd 12: count must be 1")
		}
		const itemSize = Cmd12RecordSize
		expected := 5 + count*itemSize
		if len(payload) != expected {
			return fmt.Errorf("cmd 12: invalid payload size: got=%d want=%d", len(payload), expected)
		}
		rec := payload[5 : 5+Cmd12RecordSize]
		if !IsCmd12NetworkTypeValid(rec[cmd12OffNetworkType]) {
			return fmt.Errorf("cmd 12: network type out of range: %d", rec[cmd12OffNetworkType])
		}
		if rec[cmd12OffAutoReboot] > 23 {
			return fmt.Errorf("cmd 12: auto reboot hour out of range: %d", rec[cmd12OffAutoReboot])
		}
		if rec[cmd12OffSettingsByte] > 1 || rec[cmd12OffSettingsByte+1] > 1 || rec[cmd12OffSettingsByte+2] > 1 || rec[cmd12OffSettingsByte+3] > 1 {
			return fmt.Errorf("cmd 12: boolean settings flags must be 0 or 1")
		}
		for i := 0; i < 8; i++ {
			if rec[cmd12OffRelay1+i] > 1 {
				return fmt.Errorf("cmd 12: relay1 flag %d must be 0 or 1", i)
			}
			if rec[cmd12OffRelay2+i] > 1 {
				return fmt.Errorf("cmd 12: relay2 flag %d must be 0 or 1", i)
			}
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
		expected := 5 + count*Cmd13RecordSize
		if len(payload) != expected {
			return fmt.Errorf("cmd 13: invalid payload size: got=%d want=%d", len(payload), expected)
		}
		off := 5
		for i := 0; i < count; i++ {
			rec := payload[off : off+Cmd13RecordSize]
			slot := binary.LittleEndian.Uint16(rec[cmd13OffSlot : cmd13OffSlot+2])
			if slot == 0 || slot > 50000 {
				return fmt.Errorf("cmd 13: record %d slot out of range: %d", i, slot)
			}
			if err := validatePaddedASCII(rec[cmd13OffID : cmd13OffID+10]); err != nil {
				return fmt.Errorf("cmd 13: record %d id: %v", i, err)
			}
			group := rec[cmd13OffGroup]
			if group > 64 {
				return fmt.Errorf("cmd 13: record %d group out of range: %d", i, group)
			}
			if !IsCmd13IDTypeValid(rec[cmd13OffIDType]) {
				return fmt.Errorf("cmd 13: record %d id type out of range: %d", i, rec[cmd13OffIDType])
			}
			off += Cmd13RecordSize
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
		expected := 5 + count*Cmd14RecordSize
		if len(payload) != expected {
			return fmt.Errorf("cmd 14: invalid payload size: got=%d want=%d", len(payload), expected)
		}
		off := 5
		for i := 0; i < count; i++ {
			rec := payload[off : off+Cmd14RecordSize]
			slot := rec[cmd14OffSlot]
			if slot < 1 || slot > 64 {
				return fmt.Errorf("cmd 14: record %d slot out of range: %d", i, slot)
			}
			if rec[cmd14OffStartHour] > 23 || rec[cmd14OffEndHour] > 23 {
				return fmt.Errorf("cmd 14: record %d hour out of range", i)
			}
			if rec[cmd14OffStartMinute] > 59 || rec[cmd14OffEndMinute] > 59 {
				return fmt.Errorf("cmd 14: record %d minute out of range", i)
			}
			for f := 0; f < 8; f++ {
				if !isBoolByte(rec[cmd14OffFlags+f]) {
					return fmt.Errorf("cmd 14: record %d flag %d must be 0 or 1", i, f)
				}
			}
			for s := 0; s < 8; s++ {
				scene := rec[cmd14OffScenes+s]
				if scene > 64 {
					return fmt.Errorf("cmd 14: record %d scene %d out of range: %d", i, s, scene)
				}
			}
			if rec[cmd14OffHolidayLogic] > 3 {
				return fmt.Errorf("cmd 14: record %d holiday logic out of range: %d", i, rec[cmd14OffHolidayLogic])
			}
			off += Cmd14RecordSize
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
		expected := 5 + count*Cmd15RecordSize
		if len(payload) != expected {
			return fmt.Errorf("cmd 15: invalid payload size: got=%d want=%d", len(payload), expected)
		}
		off := 5
		for i := 0; i < count; i++ {
			rec := payload[off : off+Cmd15RecordSize]
			slot := rec[cmd15OffSlot]
			if slot < 1 || slot > 64 {
				return fmt.Errorf("cmd 15: record %d slot out of range: %d", i, slot)
			}
			sched := rec[cmd15OffSchedule]
			if sched > 64 {
				return fmt.Errorf("cmd 15: record %d schedule out of range: %d", i, sched)
			}
			for f := 0; f < 8; f++ {
				if !isBoolByte(rec[cmd15OffPermFlags+f]) {
					return fmt.Errorf("cmd 15: record %d permission flag %d must be 0 or 1", i, f)
				}
			}
			off += Cmd15RecordSize
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
		fileSize := binary.LittleEndian.Uint32(payload[6:10])
		if fileSize == 0 {
			return fmt.Errorf("cmd 19: file size cannot be 0")
		}
		fileCRC := binary.LittleEndian.Uint32(payload[10:14])
		if fileCRC == 0 {
			return fmt.Errorf("cmd 19: file crc cannot be 0")
		}
		blocks := binary.LittleEndian.Uint16(payload[14:16])
		if blocks == 0 {
			return fmt.Errorf("cmd 19: blocks cannot be 0")
		}
		if blocks != FWBlockCountForSize(fileSize) {
			return fmt.Errorf("cmd 19: blocks mismatch: got=%d want=%d", blocks, FWBlockCountForSize(fileSize))
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
		if len(payload) != 4 {
			return fmt.Errorf("cmd 23: payload must be 4 bytes, got=%d", len(payload))
		}
		return nil
	case 24:
		if len(payload) != 2 {
			return fmt.Errorf("cmd 24: payload must be 2 bytes")
		}
		if !IsAckExecutionCodeAllowed(payload[1]) {
			return fmt.Errorf("cmd 24: invalid execution code: %d", payload[1])
		}
		return nil
	case 25:
		// [error_code=1][object_number=8][object_name=128][controller_name=128][admin_phone=16]
		if len(payload) != 281 {
			return fmt.Errorf("cmd 25: payload must be 281 bytes, got=%d", len(payload))
		}
		if payload[0] > 3 {
			return fmt.Errorf("cmd 25: error code out of range: %d", payload[0])
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
