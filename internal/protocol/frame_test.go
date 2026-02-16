package protocol

import (
	"bufio"
	"bytes"
	"testing"
)

func TestCRC16Modbus(t *testing.T) {
	got := CRC16Modbus([]byte("123456789"))
	const want uint16 = 0x4B37
	if got != want {
		t.Fatalf("crc mismatch: got=0x%X want=0x%X", got, want)
	}
}

func TestEncodeDecodeFrame(t *testing.T) {
	in := Frame{
		TTL:     10,
		Seq:     7,
		Payload: []byte{2, 0xAA, 0xBB, 0xCC},
	}

	wire, err := EncodeFrame(in)
	if err != nil {
		t.Fatalf("EncodeFrame() error = %v", err)
	}

	out, err := ReadFrame(bufio.NewReader(bytes.NewReader(wire)))
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}

	if out.TTL != in.TTL || out.Seq != in.Seq {
		t.Fatalf("header mismatch: got ttl=%d seq=%d", out.TTL, out.Seq)
	}
	if !bytes.Equal(out.Payload, in.Payload) {
		t.Fatalf("payload mismatch: got=%v want=%v", out.Payload, in.Payload)
	}
}

func TestReadFrame_BadCRC(t *testing.T) {
	in := Frame{TTL: 1, Seq: 1, Payload: []byte{1, 2, 3}}
	wire, err := EncodeFrame(in)
	if err != nil {
		t.Fatalf("EncodeFrame() error = %v", err)
	}
	wire[len(wire)-1] ^= 0xFF

	_, err = ReadFrame(bufio.NewReader(bytes.NewReader(wire)))
	if err == nil {
		t.Fatal("expected error for bad crc")
	}
	if err != ErrCRC {
		t.Fatalf("expected ErrCRC, got %v", err)
	}
}
