package protocol

import (
	"encoding/binary"
	"testing"
)

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
}
