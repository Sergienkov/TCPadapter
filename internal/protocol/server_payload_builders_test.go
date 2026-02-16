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
		ErrorCode:       3,
		ObjectNumberRaw: []byte{1, 2, 3},
		ObjectName:      "Object A",
		ControllerName:  "Controller 1",
		AdminPhone:      "79991234567",
	}).Build()
	if err != nil {
		t.Fatalf("cmd25 build error: %v", err)
	}
	if len(p25) != 281 {
		t.Fatalf("cmd25 len=%d", len(p25))
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
	if _, err := BuildEmptyPayload(23); err == nil {
		t.Fatal("expected empty builder error for non-empty command")
	}
	if _, err := (Cmd11SendSMSPayload{Phone10: "привет", Message: "x"}).Build(); err == nil {
		t.Fatal("expected cmd11 non-ascii validation error")
	}
	if _, err := (Cmd25BindingResponsePayload{ObjectNumberRaw: make([]byte, 9)}).Build(); err == nil {
		t.Fatal("expected cmd25 object number size error")
	}
}
