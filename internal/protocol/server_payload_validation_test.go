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

	if err := ValidateServerCommandPayload(13, mk(1, 2, 32)); err != nil {
		t.Fatalf("cmd13 valid payload rejected: %v", err)
	}
	if err := ValidateServerCommandPayload(14, mk(1, 1, 32)); err != nil {
		t.Fatalf("cmd14 valid payload rejected: %v", err)
	}
	if err := ValidateServerCommandPayload(15, mk(1, 1, 16)); err != nil {
		t.Fatalf("cmd15 valid payload rejected: %v", err)
	}
	if err := ValidateServerCommandPayload(16, mk(1, 1, 4)); err != nil {
		t.Fatalf("cmd16 valid payload rejected: %v", err)
	}
}

func TestValidateServerCommandPayload_FWCommands(t *testing.T) {
	c19 := make([]byte, 16)
	binary.LittleEndian.PutUint16(c19[0:2], 42)
	binary.LittleEndian.PutUint16(c19[14:16], 5)
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
	if err := ValidateServerCommandPayload(8, []byte{1, 1, 0}); err != nil {
		t.Fatalf("cmd8 valid payload rejected: %v", err)
	}
	if err := ValidateServerCommandPayload(11, make([]byte, 60)); err != nil {
		t.Fatalf("cmd11 valid payload rejected: %v", err)
	}
	if err := ValidateServerCommandPayload(25, make([]byte, 281)); err != nil {
		t.Fatalf("cmd25 valid payload rejected: %v", err)
	}
}

func TestValidateServerCommandPayload_Errors(t *testing.T) {
	if err := ValidateServerCommandPayload(6, []byte{1, 9, 1}); err == nil {
		t.Fatal("expected cmd6 interface range error")
	}
	if err := ValidateServerCommandPayload(8, []byte{1, 2, 0}); err == nil {
		t.Fatal("expected cmd8 output value error")
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
	if err := ValidateServerCommandPayload(27, []byte{1, 5, 0, 0, 0}); err == nil {
		t.Fatal("expected cmd27 arg size mismatch")
	}
}
