package protocol

import (
	"encoding/binary"
	"testing"
)

func TestValidateServerCommandPayload_SyncCommands(t *testing.T) {
	mk := func(ts uint32, count uint8, itemSize int) []byte {
		p := make([]byte, 5+int(count)*itemSize)
		binary.LittleEndian.PutUint32(p[0:4], ts)
		p[4] = count
		return p
	}

	cmd13 := mk(1, 2, Cmd13RecordSize)
	binary.LittleEndian.PutUint16(cmd13[5+cmd13OffSlot:5+cmd13OffSlot+2], 1)
	copy(cmd13[5+cmd13OffID:5+cmd13OffID+10], []byte("1234567890"))
	cmd13[5+cmd13OffGroup] = 1
	cmd13[5+cmd13OffIDType] = uint8(Cmd13IDTypePhone)
	off2 := 5 + Cmd13RecordSize
	binary.LittleEndian.PutUint16(cmd13[off2+cmd13OffSlot:off2+cmd13OffSlot+2], 2)
	copy(cmd13[off2+cmd13OffID:off2+cmd13OffID+10], []byte("ABC"))
	cmd13[off2+cmd13OffGroup] = 2
	cmd13[off2+cmd13OffIDType] = uint8(Cmd13IDTypeRF433)
	if err := ValidateServerCommandPayload(13, cmd13); err != nil {
		t.Fatalf("cmd13 valid payload rejected: %v", err)
	}
	cmd14 := mk(1, 1, Cmd14RecordSize)
	cmd14[5+cmd14OffSlot] = 1
	cmd14[5+cmd14OffStartHour] = 8
	cmd14[5+cmd14OffStartMinute] = 30
	cmd14[5+cmd14OffEndHour] = 20
	cmd14[5+cmd14OffEndMinute] = 45
	cmd14[5+cmd14OffFlags] = 1
	cmd14[5+cmd14OffHolidayLogic] = 1
	if err := ValidateServerCommandPayload(14, cmd14); err != nil {
		t.Fatalf("cmd14 valid payload rejected: %v", err)
	}
	cmd15 := mk(1, 1, Cmd15RecordSize)
	cmd15[5+cmd15OffSlot] = 1
	cmd15[5+cmd15OffSchedule] = 1
	cmd15[5+cmd15OffPermFlags] = 1
	if err := ValidateServerCommandPayload(15, cmd15); err != nil {
		t.Fatalf("cmd15 valid payload rejected: %v", err)
	}
	if err := ValidateServerCommandPayload(16, mk(1, 1, 4)); err != nil {
		t.Fatalf("cmd16 valid payload rejected: %v", err)
	}

	// cmd12 with empty record defaults
	cmd12 := make([]byte, 5+Cmd12RecordSize)
	binary.LittleEndian.PutUint32(cmd12[0:4], 1)
	cmd12[4] = 1
	if err := ValidateServerCommandPayload(12, cmd12); err != nil {
		t.Fatalf("cmd12 valid payload rejected: %v", err)
	}
}

func TestValidateServerCommandPayload_FWCommands(t *testing.T) {
	c19 := make([]byte, 16)
	binary.LittleEndian.PutUint16(c19[0:2], 42)
	binary.LittleEndian.PutUint32(c19[6:10], 2048)
	binary.LittleEndian.PutUint32(c19[10:14], 555)
	binary.LittleEndian.PutUint16(c19[14:16], 5)
	if err := ValidateServerCommandPayload(19, c19); err == nil {
		t.Fatalf("cmd19 invalid blocks should be rejected")
	}
	binary.LittleEndian.PutUint16(c19[14:16], 2)
	if err := ValidateServerCommandPayload(19, c19); err != nil {
		t.Fatalf("cmd19 valid payload rejected: %v", err)
	}

	c20 := make([]byte, 1026)
	if err := ValidateServerCommandPayload(20, c20); err != nil {
		t.Fatalf("cmd20 valid payload rejected: %v", err)
	}
}

func TestValidateServerCommandPayload_BaseCommands(t *testing.T) {
	if err := ValidateServerCommandPayload(1, make([]byte, 5)); err != nil {
		t.Fatalf("cmd1 valid payload rejected: %v", err)
	}
	if err := ValidateServerCommandPayload(2, []byte{1}); err != nil {
		t.Fatalf("cmd2 valid payload rejected: %v", err)
	}
	if err := ValidateServerCommandPayload(3, []byte{1}); err != nil {
		t.Fatalf("cmd3 valid payload rejected: %v", err)
	}
	if err := ValidateServerCommandPayload(5, make([]byte, 16)); err != nil {
		t.Fatalf("cmd5 valid payload rejected: %v", err)
	}
	if err := ValidateServerCommandPayload(6, []byte{10, 3, 1}); err != nil {
		t.Fatalf("cmd6 valid payload rejected: %v", err)
	}
	if err := ValidateServerCommandPayload(6, []byte{10, 1, 0}); err != nil {
		t.Fatalf("cmd6 non-485 payload rejected: %v", err)
	}
	if err := ValidateServerCommandPayload(7, []byte{1}); err != nil {
		t.Fatalf("cmd7 valid payload rejected: %v", err)
	}
	if err := ValidateServerCommandPayload(8, []byte{1, 1, 0}); err != nil {
		t.Fatalf("cmd8 valid payload rejected: %v", err)
	}
	validSMS := make([]byte, 60)
	copy(validSMS[:10], []byte("9991234567"))
	if err := ValidateServerCommandPayload(11, validSMS); err != nil {
		t.Fatalf("cmd11 valid payload rejected: %v", err)
	}
	if err := ValidateServerCommandPayload(25, make([]byte, 281)); err != nil {
		t.Fatalf("cmd25 valid payload rejected: %v", err)
	}
}

func TestValidateServerCommandPayload_Errors(t *testing.T) {
	bad5Flag := make([]byte, 16)
	bad5Flag[0] = 2
	if err := ValidateServerCommandPayload(5, bad5Flag); err == nil {
		t.Fatal("expected cmd5 bool flag validation error")
	}
	bad5Reserved := make([]byte, 16)
	bad5Reserved[8] = 1
	if err := ValidateServerCommandPayload(5, bad5Reserved); err == nil {
		t.Fatal("expected cmd5 reserved validation error")
	}

	cmd13BadSlot := make([]byte, 5+Cmd13RecordSize)
	cmd13BadSlot[4] = 1
	if err := ValidateServerCommandPayload(13, cmd13BadSlot); err == nil {
		t.Fatal("expected cmd13 slot range error")
	}
	cmd13BadGroup := make([]byte, 5+Cmd13RecordSize)
	cmd13BadGroup[4] = 1
	binary.LittleEndian.PutUint16(cmd13BadGroup[5+cmd13OffSlot:5+cmd13OffSlot+2], 1)
	cmd13BadGroup[5+cmd13OffGroup] = 65
	if err := ValidateServerCommandPayload(13, cmd13BadGroup); err == nil {
		t.Fatal("expected cmd13 group range error")
	}
	cmd13BadType := make([]byte, 5+Cmd13RecordSize)
	cmd13BadType[4] = 1
	binary.LittleEndian.PutUint16(cmd13BadType[5+cmd13OffSlot:5+cmd13OffSlot+2], 1)
	cmd13BadType[5+cmd13OffIDType] = 9
	if err := ValidateServerCommandPayload(13, cmd13BadType); err == nil {
		t.Fatal("expected cmd13 id type range error")
	}
	cmd13BadIDPad := make([]byte, 5+Cmd13RecordSize)
	cmd13BadIDPad[4] = 1
	binary.LittleEndian.PutUint16(cmd13BadIDPad[5+cmd13OffSlot:5+cmd13OffSlot+2], 1)
	cmd13BadIDPad[5+cmd13OffID] = 'A'
	cmd13BadIDPad[5+cmd13OffID+1] = 0
	cmd13BadIDPad[5+cmd13OffID+2] = 'B'
	if err := ValidateServerCommandPayload(13, cmd13BadIDPad); err == nil {
		t.Fatal("expected cmd13 padded ascii error")
	}
	cmd14BadMinute := make([]byte, 5+Cmd14RecordSize)
	cmd14BadMinute[4] = 1
	cmd14BadMinute[5+cmd14OffSlot] = 1
	cmd14BadMinute[5+cmd14OffStartMinute] = 60
	if err := ValidateServerCommandPayload(14, cmd14BadMinute); err == nil {
		t.Fatal("expected cmd14 minute range error")
	}
	cmd14BadFlag := make([]byte, 5+Cmd14RecordSize)
	cmd14BadFlag[4] = 1
	cmd14BadFlag[5+cmd14OffSlot] = 1
	cmd14BadFlag[5+cmd14OffFlags] = 2
	if err := ValidateServerCommandPayload(14, cmd14BadFlag); err == nil {
		t.Fatal("expected cmd14 flag bool error")
	}
	cmd15BadSchedule := make([]byte, 5+Cmd15RecordSize)
	cmd15BadSchedule[4] = 1
	cmd15BadSchedule[5+cmd15OffSlot] = 1
	cmd15BadSchedule[5+cmd15OffSchedule] = 65
	if err := ValidateServerCommandPayload(15, cmd15BadSchedule); err == nil {
		t.Fatal("expected cmd15 schedule range error")
	}
	cmd15BadPerm := make([]byte, 5+Cmd15RecordSize)
	cmd15BadPerm[4] = 1
	cmd15BadPerm[5+cmd15OffSlot] = 1
	cmd15BadPerm[5+cmd15OffPermFlags] = 2
	if err := ValidateServerCommandPayload(15, cmd15BadPerm); err == nil {
		t.Fatal("expected cmd15 permission bool error")
	}

	cmd12BadNetwork := make([]byte, 5+Cmd12RecordSize)
	cmd12BadNetwork[4] = 1
	cmd12BadNetwork[5+cmd12OffNetworkType] = 9
	if err := ValidateServerCommandPayload(12, cmd12BadNetwork); err == nil {
		t.Fatal("expected cmd12 network type range error")
	}
	cmd12BadFlag := make([]byte, 5+Cmd12RecordSize)
	cmd12BadFlag[4] = 1
	cmd12BadFlag[5+cmd12OffRelay1] = 2
	if err := ValidateServerCommandPayload(12, cmd12BadFlag); err == nil {
		t.Fatal("expected cmd12 relay flag error")
	}
	if err := ValidateServerCommandPayload(1, []byte{9, 0, 0, 0, 0}); err == nil {
		t.Fatal("expected cmd1 execution code range error")
	}
	if err := ValidateServerCommandPayload(6, []byte{1, 9, 1}); err == nil {
		t.Fatal("expected cmd6 interface range error")
	}
	if err := ValidateServerCommandPayload(6, []byte{1, 3, 0}); err == nil {
		t.Fatal("expected cmd6 485 address error")
	}
	if err := ValidateServerCommandPayload(6, []byte{1, 2, 5}); err == nil {
		t.Fatal("expected cmd6 non-485 address error")
	}
	if err := ValidateServerCommandPayload(7, []byte{0}); err == nil {
		t.Fatal("expected cmd7 reserved address error")
	}
	if err := ValidateServerCommandPayload(8, []byte{0, 1, 1}); err == nil {
		t.Fatal("expected cmd8 source reserved error")
	}
	if err := ValidateServerCommandPayload(8, []byte{1, 2, 0}); err == nil {
		t.Fatal("expected cmd8 output value error")
	}
	if err := ValidateServerCommandPayload(11, append([]byte("12345abcde"), make([]byte, 50)...)); err == nil {
		t.Fatal("expected cmd11 non-digit phone error")
	}
	if err := ValidateServerCommandPayload(24, []byte{1, 9}); err == nil {
		t.Fatal("expected cmd24 invalid execution code")
	}
	if err := ValidateServerCommandPayload(25, append([]byte{9}, make([]byte, 280)...)); err == nil {
		t.Fatal("expected cmd25 error code range error")
	}
	if err := ValidateServerCommandPayload(17, []byte{1}); err == nil {
		t.Fatal("expected cmd17 error")
	}
	if err := ValidateServerCommandPayload(18, []byte{101}); err == nil {
		t.Fatal("expected cmd18 block range error")
	}
	bad19 := make([]byte, 16)
	binary.LittleEndian.PutUint16(bad19[0:2], 0)
	binary.LittleEndian.PutUint16(bad19[14:16], 0)
	if err := ValidateServerCommandPayload(19, bad19); err == nil {
		t.Fatal("expected cmd19 validation error")
	}
	bad19 = make([]byte, 16)
	binary.LittleEndian.PutUint16(bad19[0:2], 1)
	binary.LittleEndian.PutUint32(bad19[6:10], 1025)
	binary.LittleEndian.PutUint32(bad19[10:14], 111)
	binary.LittleEndian.PutUint16(bad19[14:16], 1)
	if err := ValidateServerCommandPayload(19, bad19); err == nil {
		t.Fatal("expected cmd19 blocks mismatch error")
	}
	if err := ValidateServerCommandPayload(27, []byte{1, 5, 0, 0, 0}); err == nil {
		t.Fatal("expected cmd27 arg size mismatch")
	}
}
