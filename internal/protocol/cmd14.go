package protocol

const (
	Cmd14RecordSize = 32

	cmd14OffSlot         = 0
	cmd14OffStartHour    = 1
	cmd14OffStartMinute  = 2
	cmd14OffEndHour      = 3
	cmd14OffEndMinute    = 4
	cmd14OffFlags        = 5  // 8 bytes: lock + 7 weekdays
	cmd14OffScenes       = 13 // 8 bytes scene schedule numbers
	cmd14OffHolidayLogic = 21 // 0..3
	cmd14OffReserve      = 22 // 10 bytes
)

func isBoolByte(v byte) bool {
	return v == 0 || v == 1
}
