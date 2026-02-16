package protocol

import "fmt"

const (
	Cmd13RecordSize = 32

	cmd13OffSlot     = 0  // uint16
	cmd13OffID       = 2  // [10]byte ASCII
	cmd13OffGroup    = 12 // uint8
	cmd13OffBanDate  = 13 // uint32
	cmd13OffLifetime = 17 // uint16
	cmd13OffIDType   = 19 // uint8
	cmd13OffReserve  = 20 // [12]byte
)

type Cmd13IDType uint8

const (
	Cmd13IDTypePhone   Cmd13IDType = 0
	Cmd13IDTypeRFID485 Cmd13IDType = 1
	Cmd13IDTypePlate   Cmd13IDType = 2
	Cmd13IDTypeRF433   Cmd13IDType = 3
	Cmd13IDTypeWiegand Cmd13IDType = 4
	Cmd13IDTypeRes1    Cmd13IDType = 5
	Cmd13IDTypeRes2    Cmd13IDType = 6
	Cmd13IDTypeRes3    Cmd13IDType = 7
)

func IsCmd13IDTypeValid(v uint8) bool {
	return v <= 7
}

func validatePaddedASCII(field []byte) error {
	seenZero := false
	for i := 0; i < len(field); i++ {
		b := field[i]
		if b == 0 {
			seenZero = true
			continue
		}
		if seenZero {
			return fmt.Errorf("non-zero byte after zero padding at index %d", i)
		}
		if b > 127 {
			return fmt.Errorf("non-ascii byte at index %d", i)
		}
	}
	return nil
}
