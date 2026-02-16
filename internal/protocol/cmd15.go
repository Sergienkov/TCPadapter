package protocol

const (
	Cmd15RecordSize = 16

	cmd15OffSlot      = 0
	cmd15OffSchedule  = 1
	cmd15OffPermFlags = 2  // 8 bytes: block + 7 permissions
	cmd15OffReserve   = 10 // 6 bytes
)
