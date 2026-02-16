package protocol

const (
	CmdRegistrationAck uint8 = 1
	CmdReboot          uint8 = 2
	CmdFactoryReset    uint8 = 3
	CmdSetTriggers     uint8 = 5
	CmdCaptureID       uint8 = 6
	CmdRequestPhoto    uint8 = 7
	CmdControlOutputs  uint8 = 8
	CmdRequestEvents   uint8 = 9
	CmdRequestSMSIDs   uint8 = 10
	CmdSendSMS         uint8 = 11
	CmdSyncSettings    uint8 = 12
	CmdSyncIDs         uint8 = 13
	CmdSyncSchedules   uint8 = 14
	CmdSyncGroups      uint8 = 15
	CmdSyncHolidays    uint8 = 16
	CmdCRCAll          uint8 = 17
	CmdCRCBlock        uint8 = 18
	CmdFWStart         uint8 = 19
	CmdFWBlock         uint8 = 20
	CmdPhotoUploadReq  uint8 = 21
	CmdStatus2Req      uint8 = 22
	CmdSetTime         uint8 = 23
	CmdControllerAck   uint8 = 24
	CmdBindingResponse uint8 = 25
	CmdCustom          uint8 = 27
)

const (
	AckCodeOK             uint8 = 0
	AckCodeParamError     uint8 = 1
	AckCodeExecError      uint8 = 2
	AckCodeInvalidResult  uint8 = 3
	AckCodePasswordNeeded uint8 = 4
	AckCodeStarted        uint8 = 5
	AckCodeCancel         uint8 = 6
	AckCodeNextSet        uint8 = 7
	AckCodeCRCError       uint8 = 254
	AckCodeUnsupported    uint8 = 255
)

func IsAckExecutionCodeAllowed(code uint8) bool {
	switch code {
	case AckCodeOK, AckCodeParamError, AckCodeExecError, AckCodeInvalidResult, AckCodePasswordNeeded,
		AckCodeStarted, AckCodeCancel, AckCodeNextSet, AckCodeCRCError, AckCodeUnsupported:
		return true
	default:
		return false
	}
}
