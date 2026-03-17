package protocol

import (
	"encoding/binary"
	"testing"
)

func TestParseRegistrationPayload(t *testing.T) {
	p := make([]byte, 0, 18)
	p = append(p, 1)
	p = append(p, []byte("123456789012345")...)
	p = append(p, 3, 2)

	reg, err := ParseRegistrationPayload(p)
	if err != nil {
		t.Fatalf("ParseRegistrationPayload() error = %v", err)
	}
	if reg.IMEI != "123456789012345" || reg.ProtocolVersion != 3 || reg.HWVersion != 2 {
		t.Fatalf("unexpected parsed registration: %+v", reg)
	}
}

func TestParseRegistrationPayload_InvalidIMEI(t *testing.T) {
	p := make([]byte, 0, 18)
	p = append(p, 1)
	p = append(p, []byte("A23456789012345")...)
	p = append(p, 3, 2)

	if _, err := ParseRegistrationPayload(p); err == nil {
		t.Fatal("expected invalid imei error")
	}
}

func TestParseStatus1Payload(t *testing.T) {
	p := make([]byte, 9)
	p[0] = 2
	binary.LittleEndian.PutUint32(p[1:5], 100)
	binary.LittleEndian.PutUint16(p[5:7], 1<<11)
	binary.LittleEndian.PutUint16(p[7:9], 777)

	st, err := ParseStatus1Payload(p)
	if err != nil {
		t.Fatalf("ParseStatus1Payload() error = %v", err)
	}
	if st.BufferFree != 777 || !Status1ReadyForFW(st.Flags) {
		t.Fatalf("unexpected status1 parse: %+v", st)
	}
}

func TestParseAck11Payload(t *testing.T) {
	p := make([]byte, 9)
	p[0] = 11
	p[1] = 55
	p[2] = 0
	binary.LittleEndian.PutUint32(p[3:7], 123456)
	binary.LittleEndian.PutUint16(p[7:9], 1024)

	ack, err := ParseAck11Payload(p)
	if err != nil {
		t.Fatalf("ParseAck11Payload() error = %v", err)
	}
	if ack.CommandSeq != 55 || ack.BufferFreeBytes != 1024 {
		t.Fatalf("unexpected ack parse: %+v", ack)
	}
}

func TestInterpretAck11ExecutionCode(t *testing.T) {
	cases := []struct {
		code     uint8
		status   string
		terminal bool
	}{
		{0, "delivered", true},
		{5, "in_progress", false},
		{6, "expired", true},
		{255, "unsupported", true},
		{99, "failed", true},
	}

	for _, tc := range cases {
		got := InterpretAck11ExecutionCode(tc.code)
		if got.Status != tc.status || got.Terminal != tc.terminal {
			t.Fatalf("code=%d got=%+v", tc.code, got)
		}
	}
}
