package protocol

import (
	"encoding/binary"
	"fmt"
)

type Registration struct {
	IMEI            string
	ProtocolVersion uint8
	HWVersion       uint8
}

type Status1 struct {
	ControllerTime uint32
	Flags          uint16
	BufferFree     uint16
}

type Ack11 struct {
	CommandSeq      uint8
	ExecutionCode   uint8
	ControllerTime  uint32
	BufferFreeBytes uint16
}

type FWStatus4 struct {
	CommandSeq    uint8
	ProgressOrErr uint8
	ExecutionCode uint8
}

func ParseRegistrationPayload(payload []byte) (Registration, error) {
	// [cmd_id=1][imei=15][proto=1][hw=1]...
	if len(payload) < 18 {
		return Registration{}, fmt.Errorf("registration payload too short: %d", len(payload))
	}
	if payload[0] != 1 {
		return Registration{}, fmt.Errorf("unexpected cmd id: %d", payload[0])
	}

	imeiRaw := payload[1:16]
	end := len(imeiRaw)
	for end > 0 && imeiRaw[end-1] == 0 {
		end--
	}
	imei := string(imeiRaw[:end])
	if imei == "" {
		return Registration{}, fmt.Errorf("empty imei")
	}

	return Registration{
		IMEI:            imei,
		ProtocolVersion: payload[16],
		HWVersion:       payload[17],
	}, nil
}

func ParseStatus1Payload(payload []byte) (Status1, error) {
	// [cmd_id=2][time=4][flags=2][buffer_free=2]...
	if len(payload) < 9 {
		return Status1{}, fmt.Errorf("status1 payload too short: %d", len(payload))
	}
	if payload[0] != 2 {
		return Status1{}, fmt.Errorf("unexpected cmd id: %d", payload[0])
	}
	return Status1{
		ControllerTime: binary.LittleEndian.Uint32(payload[1:5]),
		Flags:          binary.LittleEndian.Uint16(payload[5:7]),
		BufferFree:     binary.LittleEndian.Uint16(payload[7:9]),
	}, nil
}

func ParseAck11Payload(payload []byte) (Ack11, error) {
	// [cmd_id=11][seq=1][code=1][time=4][buffer_free=2]
	if len(payload) < 9 {
		return Ack11{}, fmt.Errorf("ack11 payload too short: %d", len(payload))
	}
	if payload[0] != 11 {
		return Ack11{}, fmt.Errorf("unexpected cmd id: %d", payload[0])
	}
	return Ack11{
		CommandSeq:      payload[1],
		ExecutionCode:   payload[2],
		ControllerTime:  binary.LittleEndian.Uint32(payload[3:7]),
		BufferFreeBytes: binary.LittleEndian.Uint16(payload[7:9]),
	}, nil
}

func ParseFWStatus4Payload(payload []byte) (FWStatus4, error) {
	// [cmd_id=4][seq=1][progress_or_error=1][execution_code=1]
	if len(payload) < 4 {
		return FWStatus4{}, fmt.Errorf("fw status payload too short: %d", len(payload))
	}
	if payload[0] != 4 {
		return FWStatus4{}, fmt.Errorf("unexpected cmd id: %d", payload[0])
	}
	return FWStatus4{CommandSeq: payload[1], ProgressOrErr: payload[2], ExecutionCode: payload[3]}, nil
}

func Status1ReadyForFW(flags uint16) bool {
	// bit 11 according to protocol table
	return flags&(1<<11) != 0
}

func IsFWFinished(progress uint8) bool {
	return progress == 248
}

func IsFWFailed(progress uint8) bool {
	return progress >= 249 && progress <= 255
}
