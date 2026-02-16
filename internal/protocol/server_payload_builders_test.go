package protocol

import (
	"encoding/binary"
	"testing"
)

func TestBasicControlBuilders(t *testing.T) {
	p1, err := (Cmd1RegistrationAckPayload{ExecutionCode: 0, ServerTime: 12345}).Build()
	if err != nil || len(p1) != 5 {
		t.Fatalf("cmd1 build failed: len=%d err=%v", len(p1), err)
	}
	p2, err := (Cmd2RebootPayload{TimeoutSec: 5}).Build()
	if err != nil || len(p2) != 1 {
		t.Fatalf("cmd2 build failed: len=%d err=%v", len(p2), err)
	}
	p3, err := (Cmd3FactoryResetPayload{TimeoutSec: 7}).Build()
	if err != nil || len(p3) != 1 {
		t.Fatalf("cmd3 build failed: len=%d err=%v", len(p3), err)
	}
	var flags [16]byte
	flags[0] = 1
	p5, err := (Cmd5TriggerPayload{Flags: flags}).Build()
	if err != nil || len(p5) != 16 {
		t.Fatalf("cmd5 build failed: len=%d err=%v", len(p5), err)
	}

	p5s, err := (Cmd5TriggerStructuredPayload{
		Flags: Cmd5TriggerFlags{
			ManualUnlock:        true,
			ScenarioUnlock:      true,
			AutoRegistration:    true,
			OpenForAll:          false,
			ForwardSMS:          true,
			NotifyUnknownIDSMS:  true,
			NotifyLowBalanceSMS: false,
			ScheduleForAllCalls: true,
		},
	}).Build()
	if err != nil || len(p5s) != 16 {
		t.Fatalf("cmd5 structured build failed: len=%d err=%v", len(p5s), err)
	}
}

func TestCmd11And25Builders(t *testing.T) {
	p11, err := (Cmd11SendSMSPayload{Phone10: "9991234567", Message: "test"}).Build()
	if err != nil {
		t.Fatalf("cmd11 build error: %v", err)
	}
	if len(p11) != 60 {
		t.Fatalf("cmd11 len=%d", len(p11))
	}

	p25, err := (Cmd25BindingResponsePayload{
		ErrorCode:      3,
		ObjectNumber:   0x0102030405060708,
		ObjectName:     "Object A",
		ControllerName: "Controller 1",
		AdminPhone:     "79991234567",
	}).Build()
	if err != nil {
		t.Fatalf("cmd25 build error: %v", err)
	}
	if len(p25) != 281 {
		t.Fatalf("cmd25 len=%d", len(p25))
	}
	if got := binary.LittleEndian.Uint64(p25[1:9]); got != 0x0102030405060708 {
		t.Fatalf("cmd25 object number encode mismatch: got=%x", got)
	}
}

func TestCmd12Builder(t *testing.T) {
	var rec Cmd12SettingsRecord
	rec.PINCode = 1234
	rec.Server1 = "server1:15010"
	rec.Server2 = "server2:15020"
	rec.NTPServer = "pool.ntp.org"
	rec.Operator = "operator-x"
	rec.NetworkType = Cmd12NetworkAuto3G4G
	rec.CountryCode = "+7"
	rec.SimPhone = "79991234567"
	rec.USSD = "*100#"
	rec.AutoRebootHour = 5
	rec.ExtendedDebug = true
	rec.UseVoLTE = true
	rec.Interface485Active = true
	rec.NTPEnabled = true
	rec.WiegandType = 2
	rec.Peripherals[0] = Cmd12Peripheral{Address: 1, Type: 6}
	rec.Relay1.Flags = [8]uint8{1, 1, 1, 1, 1, 1, 1, 1}
	rec.Relay2.Flags = [8]uint8{1, 0, 1, 0, 1, 0, 1, 0}
	rec.Input1Function = 1
	rec.Input2Function = 2

	p, err := (Cmd12SettingsPayload{Timestamp: 111, Record: rec}).Build()
	if err != nil {
		t.Fatalf("cmd12 build error: %v", err)
	}
	if len(p) != 5+Cmd12RecordSize {
		t.Fatalf("cmd12 len=%d", len(p))
	}
	if p[4] != 1 {
		t.Fatalf("cmd12 count=%d", p[4])
	}
}

func TestCmd13Builder(t *testing.T) {
	rec1 := make([]byte, 32)
	rec2 := make([]byte, 32)
	rec1[0] = 1
	rec2[0] = 2

	p, err := (Cmd13SyncIDPayload{Timestamp: 123, Records: [][]byte{rec1, rec2}}).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(p) != 5+64 {
		t.Fatalf("unexpected len=%d", len(p))
	}
	if binary.LittleEndian.Uint32(p[0:4]) != 123 || p[4] != 2 {
		t.Fatalf("bad header in payload")
	}
}

func TestCmd13StructuredBuilder(t *testing.T) {
	p, err := (Cmd13SyncIDStructuredPayload{
		Timestamp: 111,
		Records: []Cmd13IDRecord{
			{
				SlotIndex:       1,
				ID:              "1234567890",
				Group:           1,
				BanDate:         1000,
				LifetimeMinutes: 60,
				IDType:          Cmd13IDTypePhone,
			},
			{
				SlotIndex:       2,
				ID:              "ABCDEF",
				Group:           2,
				BanDate:         2000,
				LifetimeMinutes: 0,
				IDType:          Cmd13IDTypeRF433,
			},
		},
	}).Build()
	if err != nil {
		t.Fatalf("cmd13 structured build error: %v", err)
	}
	if len(p) != 5+2*Cmd13RecordSize {
		t.Fatalf("unexpected len=%d", len(p))
	}
	if p[4] != 2 {
		t.Fatalf("unexpected count=%d", p[4])
	}
}

func TestCmd14AndCmd15StructuredBuilders(t *testing.T) {
	p14, err := (Cmd14SyncScheduleStructuredPayload{
		Timestamp: 100,
		Records: []Cmd14ScheduleRecord{
			{
				Slot:         1,
				StartHour:    8,
				StartMinute:  0,
				EndHour:      18,
				EndMinute:    30,
				Flags:        [8]uint8{1, 1, 1, 1, 1, 1, 1, 1},
				Scenes:       [8]uint8{1, 2, 3, 4, 5, 6, 7, 8},
				HolidayLogic: 1,
			},
		},
	}).Build()
	if err != nil {
		t.Fatalf("cmd14 structured build error: %v", err)
	}
	if len(p14) != 5+Cmd14RecordSize {
		t.Fatalf("cmd14 len=%d", len(p14))
	}

	p15, err := (Cmd15SyncGroupStructuredPayload{
		Timestamp: 200,
		Records: []Cmd15GroupRecord{
			{
				Slot:        1,
				Schedule:    1,
				Permissions: [8]uint8{1, 1, 1, 1, 1, 1, 1, 1},
			},
		},
	}).Build()
	if err != nil {
		t.Fatalf("cmd15 structured build error: %v", err)
	}
	if len(p15) != 5+Cmd15RecordSize {
		t.Fatalf("cmd15 len=%d", len(p15))
	}
}

func TestCmd19Builder(t *testing.T) {
	p, err := (Cmd19StartFWPayload{
		FirmwareNumber: 10,
		BuildTimestamp: 1000,
		FileSize:       2048,
		FileCRC:        1234,
		BlockCount:     2,
	}).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(p) != 16 {
		t.Fatalf("unexpected len=%d", len(p))
	}
	if binary.LittleEndian.Uint16(p[0:2]) != 10 {
		t.Fatalf("bad fw number")
	}

	// Auto-calc block count when zero
	p2, err := (Cmd19StartFWPayload{
		FirmwareNumber: 11,
		FileSize:       2049,
		FileCRC:        1234,
		BlockCount:     0,
	}).Build()
	if err != nil {
		t.Fatalf("cmd19 auto block build error: %v", err)
	}
	if got := binary.LittleEndian.Uint16(p2[14:16]); got != 3 {
		t.Fatalf("expected 3 blocks, got %d", got)
	}
}

func TestCmd20Builder(t *testing.T) {
	data := make([]byte, 1024)
	data[0] = 0xAA
	p, err := (Cmd20FWBlockPayload{BlockNumber: 3, Data: data}).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(p) != 1026 {
		t.Fatalf("unexpected len=%d", len(p))
	}
	if binary.LittleEndian.Uint16(p[0:2]) != 3 || p[2] != 0xAA {
		t.Fatalf("bad block payload")
	}
}

func TestCustomAndEmptyBuilders(t *testing.T) {
	cp, err := (Cmd27CustomPayload{CustomCommand: 7, Argument: []byte{1, 2, 3}}).Build()
	if err != nil {
		t.Fatalf("cmd27 build error = %v", err)
	}
	if cp[0] != 7 || binary.LittleEndian.Uint16(cp[1:3]) != 3 {
		t.Fatalf("bad cmd27 payload")
	}

	e, err := BuildEmptyPayload(17)
	if err != nil {
		t.Fatalf("empty build error = %v", err)
	}
	if len(e) != 0 {
		t.Fatalf("expected empty payload")
	}

	e9, err := BuildEmptyPayload(9)
	if err != nil || len(e9) != 0 {
		t.Fatalf("cmd9 empty payload builder failed: len=%d err=%v", len(e9), err)
	}
}

func TestBuilderValidationErrors(t *testing.T) {
	if _, err := (Cmd18CRCBlockPayload{Block: 101}).Build(); err == nil {
		t.Fatal("expected cmd18 validation error")
	}
	if _, err := (Cmd20FWBlockPayload{BlockNumber: 1, Data: make([]byte, 10)}).Build(); err == nil {
		t.Fatal("expected cmd20 data size error")
	}
	if _, err := (Cmd19StartFWPayload{
		FirmwareNumber: 1,
		FileSize:       1025,
		FileCRC:        123,
		BlockCount:     1,
	}).Build(); err == nil {
		t.Fatal("expected cmd19 block count mismatch error")
	}
	if _, err := BuildEmptyPayload(23); err == nil {
		t.Fatal("expected empty builder error for non-empty command")
	}
	if _, err := (Cmd11SendSMSPayload{Phone10: "123", Message: "x"}).Build(); err == nil {
		t.Fatal("expected cmd11 phone length validation error")
	}
	if _, err := (Cmd11SendSMSPayload{Phone10: "12345abcde", Message: "x"}).Build(); err == nil {
		t.Fatal("expected cmd11 non-digit validation error")
	}
	if _, err := (Cmd25BindingResponsePayload{ObjectNumberRaw: make([]byte, 9)}).Build(); err == nil {
		t.Fatal("expected cmd25 object number size error")
	}
	if _, err := (Cmd25BindingResponsePayload{
		ObjectNumber:    1,
		ObjectNumberRaw: []byte{1},
	}).Build(); err == nil {
		t.Fatal("expected cmd25 mutual exclusivity error")
	}
	var reserved [8]byte
	reserved[0] = 1
	if _, err := (Cmd5TriggerStructuredPayload{
		Flags:   Cmd5TriggerFlags{ManualUnlock: true},
		Reserve: reserved,
	}).Build(); err == nil {
		t.Fatal("expected cmd5 reserved validation error")
	}
	if _, err := (Cmd13SyncIDStructuredPayload{
		Timestamp: 1,
		Records: []Cmd13IDRecord{{
			SlotIndex: 0,
			ID:        "123",
		}},
	}).Build(); err == nil {
		t.Fatal("expected cmd13 slot validation error")
	}
	if _, err := (Cmd13SyncIDStructuredPayload{
		Timestamp: 1,
		Records: []Cmd13IDRecord{{
			SlotIndex: 1,
			ID:        "123",
			Group:     99,
		}},
	}).Build(); err == nil {
		t.Fatal("expected cmd13 group validation error")
	}
	if _, err := (Cmd14SyncScheduleStructuredPayload{
		Timestamp: 1,
		Records: []Cmd14ScheduleRecord{{
			Slot:        1,
			StartHour:   8,
			StartMinute: 99,
		}},
	}).Build(); err == nil {
		t.Fatal("expected cmd14 minute validation error")
	}
	if _, err := (Cmd15SyncGroupStructuredPayload{
		Timestamp: 1,
		Records: []Cmd15GroupRecord{{
			Slot:        1,
			Schedule:    1,
			Permissions: [8]uint8{2, 0, 0, 0, 0, 0, 0, 0},
		}},
	}).Build(); err == nil {
		t.Fatal("expected cmd15 permissions validation error")
	}
	var rec Cmd12SettingsRecord
	rec.NetworkType = Cmd12NetworkType(9)
	if _, err := (Cmd12SettingsPayload{Timestamp: 1, Record: rec}).Build(); err == nil {
		t.Fatal("expected cmd12 network type validation error")
	}
}
