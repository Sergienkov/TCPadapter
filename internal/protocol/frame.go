package protocol

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

var (
	ErrInvalidMarker = errors.New("invalid start marker")
	ErrFrameTooShort = errors.New("frame too short")
	ErrCRC           = errors.New("crc mismatch")
)

var StartMarker = [4]byte{'S', 'L', 'D', 'N'}

type Frame struct {
	TTL     uint8
	Seq     uint8
	Payload []byte
}

func (f Frame) CommandID() (uint8, bool) {
	if len(f.Payload) == 0 {
		return 0, false
	}
	return f.Payload[0], true
}

func EncodeFrame(f Frame) ([]byte, error) {
	if len(f.Payload) == 0 {
		return nil, fmt.Errorf("payload must include command id")
	}
	dataLen := 2 + len(f.Payload) // ttl + seq + payload
	if dataLen > 0xFFFF {
		return nil, fmt.Errorf("frame too large: %d", dataLen)
	}

	buf := make([]byte, 0, 4+2+dataLen+2)
	buf = append(buf, StartMarker[:]...)

	lenField := make([]byte, 2)
	binary.LittleEndian.PutUint16(lenField, uint16(dataLen))
	buf = append(buf, lenField...)

	buf = append(buf, f.TTL)
	buf = append(buf, f.Seq)
	buf = append(buf, f.Payload...)

	crc := CRC16Modbus(buf[4:])
	crcField := make([]byte, 2)
	binary.LittleEndian.PutUint16(crcField, crc)
	buf = append(buf, crcField...)

	return buf, nil
}

func ReadFrame(r *bufio.Reader) (Frame, error) {
	header := make([]byte, 6)
	if _, err := io.ReadFull(r, header); err != nil {
		return Frame{}, err
	}

	if header[0] != StartMarker[0] || header[1] != StartMarker[1] || header[2] != StartMarker[2] || header[3] != StartMarker[3] {
		return Frame{}, ErrInvalidMarker
	}

	dataLen := int(binary.LittleEndian.Uint16(header[4:6]))
	if dataLen < 2 {
		return Frame{}, ErrFrameTooShort
	}

	dataAndCRC := make([]byte, dataLen+2)
	if _, err := io.ReadFull(r, dataAndCRC); err != nil {
		return Frame{}, err
	}

	wireCRC := binary.LittleEndian.Uint16(dataAndCRC[dataLen:])
	calcCRC := CRC16Modbus(append(header[4:6], dataAndCRC[:dataLen]...))
	if wireCRC != calcCRC {
		return Frame{}, ErrCRC
	}

	return Frame{
		TTL:     dataAndCRC[0],
		Seq:     dataAndCRC[1],
		Payload: append([]byte(nil), dataAndCRC[2:dataLen]...),
	}, nil
}
