package protocol

import (
	"encoding/binary"
	"fmt"
)

type Cmd13SyncIDPayload struct {
	Timestamp uint32
	Records   [][]byte // each record is 32 bytes
}

func (p Cmd13SyncIDPayload) Build() ([]byte, error) {
	if len(p.Records) < 1 || len(p.Records) > 250 {
		return nil, fmt.Errorf("cmd 13: records count out of range: %d", len(p.Records))
	}
	out := make([]byte, 5+len(p.Records)*32)
	binary.LittleEndian.PutUint32(out[0:4], p.Timestamp)
	out[4] = uint8(len(p.Records))
	off := 5
	for i, rec := range p.Records {
		if len(rec) != 32 {
			return nil, fmt.Errorf("cmd 13: record %d size must be 32", i)
		}
		copy(out[off:off+32], rec)
		off += 32
	}
	if err := ValidateServerCommandPayload(13, out); err != nil {
		return nil, err
	}
	return out, nil
}

type Cmd14SyncSchedulePayload struct {
	Timestamp uint32
	Records   [][]byte // each record is 32 bytes
}

func (p Cmd14SyncSchedulePayload) Build() ([]byte, error) {
	if len(p.Records) < 1 || len(p.Records) > 64 {
		return nil, fmt.Errorf("cmd 14: records count out of range: %d", len(p.Records))
	}
	out := make([]byte, 5+len(p.Records)*32)
	binary.LittleEndian.PutUint32(out[0:4], p.Timestamp)
	out[4] = uint8(len(p.Records))
	off := 5
	for i, rec := range p.Records {
		if len(rec) != 32 {
			return nil, fmt.Errorf("cmd 14: record %d size must be 32", i)
		}
		copy(out[off:off+32], rec)
		off += 32
	}
	if err := ValidateServerCommandPayload(14, out); err != nil {
		return nil, err
	}
	return out, nil
}

type Cmd15SyncGroupPayload struct {
	Timestamp uint32
	Records   [][]byte // each record is 16 bytes
}

func (p Cmd15SyncGroupPayload) Build() ([]byte, error) {
	if len(p.Records) < 1 || len(p.Records) > 64 {
		return nil, fmt.Errorf("cmd 15: records count out of range: %d", len(p.Records))
	}
	out := make([]byte, 5+len(p.Records)*16)
	binary.LittleEndian.PutUint32(out[0:4], p.Timestamp)
	out[4] = uint8(len(p.Records))
	off := 5
	for i, rec := range p.Records {
		if len(rec) != 16 {
			return nil, fmt.Errorf("cmd 15: record %d size must be 16", i)
		}
		copy(out[off:off+16], rec)
		off += 16
	}
	if err := ValidateServerCommandPayload(15, out); err != nil {
		return nil, err
	}
	return out, nil
}

type Cmd16SyncHolidayPayload struct {
	Timestamp uint32
	Days      []uint32
}

func (p Cmd16SyncHolidayPayload) Build() ([]byte, error) {
	if len(p.Days) < 1 || len(p.Days) > 255 {
		return nil, fmt.Errorf("cmd 16: days count out of range: %d", len(p.Days))
	}
	out := make([]byte, 5+len(p.Days)*4)
	binary.LittleEndian.PutUint32(out[0:4], p.Timestamp)
	out[4] = uint8(len(p.Days))
	off := 5
	for _, d := range p.Days {
		binary.LittleEndian.PutUint32(out[off:off+4], d)
		off += 4
	}
	if err := ValidateServerCommandPayload(16, out); err != nil {
		return nil, err
	}
	return out, nil
}

type Cmd18CRCBlockPayload struct {
	Block uint8 // 0..100
}

func (p Cmd18CRCBlockPayload) Build() ([]byte, error) {
	out := []byte{p.Block}
	if err := ValidateServerCommandPayload(18, out); err != nil {
		return nil, err
	}
	return out, nil
}

type Cmd19StartFWPayload struct {
	FirmwareNumber uint16
	BuildTimestamp uint32
	FileSize       uint32
	FileCRC        uint32
	BlockCount     uint16
}

func (p Cmd19StartFWPayload) Build() ([]byte, error) {
	out := make([]byte, 16)
	binary.LittleEndian.PutUint16(out[0:2], p.FirmwareNumber)
	binary.LittleEndian.PutUint32(out[2:6], p.BuildTimestamp)
	binary.LittleEndian.PutUint32(out[6:10], p.FileSize)
	binary.LittleEndian.PutUint32(out[10:14], p.FileCRC)
	binary.LittleEndian.PutUint16(out[14:16], p.BlockCount)
	if err := ValidateServerCommandPayload(19, out); err != nil {
		return nil, err
	}
	return out, nil
}

type Cmd20FWBlockPayload struct {
	BlockNumber uint16
	Data        []byte // must be 1024 bytes
}

func (p Cmd20FWBlockPayload) Build() ([]byte, error) {
	if len(p.Data) != 1024 {
		return nil, fmt.Errorf("cmd 20: data must be 1024 bytes")
	}
	out := make([]byte, 1026)
	binary.LittleEndian.PutUint16(out[0:2], p.BlockNumber)
	copy(out[2:], p.Data)
	if err := ValidateServerCommandPayload(20, out); err != nil {
		return nil, err
	}
	return out, nil
}

type Cmd23SetTimePayload struct {
	ServerTime uint32
}

func (p Cmd23SetTimePayload) Build() ([]byte, error) {
	out := make([]byte, 4)
	binary.LittleEndian.PutUint32(out, p.ServerTime)
	if err := ValidateServerCommandPayload(23, out); err != nil {
		return nil, err
	}
	return out, nil
}

type Cmd24AckPayload struct {
	CommandSeq uint8
	Code       uint8
}

func (p Cmd24AckPayload) Build() ([]byte, error) {
	out := []byte{p.CommandSeq, p.Code}
	if err := ValidateServerCommandPayload(24, out); err != nil {
		return nil, err
	}
	return out, nil
}

type Cmd27CustomPayload struct {
	CustomCommand uint8
	Argument      []byte
}

func (p Cmd27CustomPayload) Build() ([]byte, error) {
	if len(p.Argument) > 0xFFFF {
		return nil, fmt.Errorf("cmd 27: argument too large")
	}
	out := make([]byte, 3+len(p.Argument))
	out[0] = p.CustomCommand
	binary.LittleEndian.PutUint16(out[1:3], uint16(len(p.Argument)))
	copy(out[3:], p.Argument)
	if err := ValidateServerCommandPayload(27, out); err != nil {
		return nil, err
	}
	return out, nil
}

func BuildEmptyPayload(commandID uint8) ([]byte, error) {
	switch commandID {
	case 17, 21, 22:
		if err := ValidateServerCommandPayload(commandID, nil); err != nil {
			return nil, err
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("command %d does not use empty payload builder", commandID)
	}
}
