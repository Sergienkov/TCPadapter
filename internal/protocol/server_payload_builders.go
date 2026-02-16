package protocol

import (
	"encoding/binary"
	"fmt"
)

type Cmd1RegistrationAckPayload struct {
	ExecutionCode uint8
	ServerTime    uint32
}

func (p Cmd1RegistrationAckPayload) Build() ([]byte, error) {
	out := make([]byte, 5)
	out[0] = p.ExecutionCode
	binary.LittleEndian.PutUint32(out[1:5], p.ServerTime)
	if err := ValidateServerCommandPayload(1, out); err != nil {
		return nil, err
	}
	return out, nil
}

type Cmd2RebootPayload struct {
	TimeoutSec uint8
}

func (p Cmd2RebootPayload) Build() ([]byte, error) {
	out := []byte{p.TimeoutSec}
	if err := ValidateServerCommandPayload(2, out); err != nil {
		return nil, err
	}
	return out, nil
}

type Cmd3FactoryResetPayload struct {
	TimeoutSec uint8
}

func (p Cmd3FactoryResetPayload) Build() ([]byte, error) {
	out := []byte{p.TimeoutSec}
	if err := ValidateServerCommandPayload(3, out); err != nil {
		return nil, err
	}
	return out, nil
}

type Cmd5TriggerPayload struct {
	Flags [16]byte
}

func (p Cmd5TriggerPayload) Build() ([]byte, error) {
	out := make([]byte, 16)
	copy(out, p.Flags[:])
	if err := ValidateServerCommandPayload(5, out); err != nil {
		return nil, err
	}
	return out, nil
}

type Cmd5TriggerFlags struct {
	ManualUnlock        bool
	ScenarioUnlock      bool
	AutoRegistration    bool
	OpenForAll          bool
	ForwardSMS          bool
	NotifyUnknownIDSMS  bool
	NotifyLowBalanceSMS bool
	ScheduleForAllCalls bool
}

type Cmd5TriggerStructuredPayload struct {
	Flags   Cmd5TriggerFlags
	Reserve [8]byte // must be all zeros for strict mode
}

func (p Cmd5TriggerStructuredPayload) Build() ([]byte, error) {
	out := make([]byte, 16)
	out[0] = boolToByte(p.Flags.ManualUnlock)
	out[1] = boolToByte(p.Flags.ScenarioUnlock)
	out[2] = boolToByte(p.Flags.AutoRegistration)
	out[3] = boolToByte(p.Flags.OpenForAll)
	out[4] = boolToByte(p.Flags.ForwardSMS)
	out[5] = boolToByte(p.Flags.NotifyUnknownIDSMS)
	out[6] = boolToByte(p.Flags.NotifyLowBalanceSMS)
	out[7] = boolToByte(p.Flags.ScheduleForAllCalls)
	copy(out[8:16], p.Reserve[:])
	if err := ValidateServerCommandPayload(5, out); err != nil {
		return nil, err
	}
	return out, nil
}

type Cmd6CaptureIDPayload struct {
	CaptureTimeout uint8
	Interface      uint8
	NetworkAddress uint8
}

func (p Cmd6CaptureIDPayload) Build() ([]byte, error) {
	out := []byte{p.CaptureTimeout, p.Interface, p.NetworkAddress}
	if err := ValidateServerCommandPayload(6, out); err != nil {
		return nil, err
	}
	return out, nil
}

type Cmd7PhotoRequestPayload struct {
	CameraAddress uint8
}

func (p Cmd7PhotoRequestPayload) Build() ([]byte, error) {
	out := []byte{p.CameraAddress}
	if err := ValidateServerCommandPayload(7, out); err != nil {
		return nil, err
	}
	return out, nil
}

type Cmd8OutputControlPayload struct {
	SourceID uint8
	Output1  uint8
	Output2  uint8
}

func (p Cmd8OutputControlPayload) Build() ([]byte, error) {
	out := []byte{p.SourceID, p.Output1, p.Output2}
	if err := ValidateServerCommandPayload(8, out); err != nil {
		return nil, err
	}
	return out, nil
}

type Cmd11SendSMSPayload struct {
	Phone10 string
	Message string
}

func (p Cmd11SendSMSPayload) Build() ([]byte, error) {
	if len(p.Phone10) != 10 {
		return nil, fmt.Errorf("cmd 11: phone must be exactly 10 digits")
	}
	phone, err := fixedDigits(p.Phone10, 10)
	if err != nil {
		return nil, fmt.Errorf("cmd 11: phone: %w", err)
	}
	msg, err := fixedASCII(p.Message, 50)
	if err != nil {
		return nil, fmt.Errorf("cmd 11: message: %w", err)
	}
	out := make([]byte, 60)
	copy(out[0:10], phone)
	copy(out[10:60], msg)
	if err := ValidateServerCommandPayload(11, out); err != nil {
		return nil, err
	}
	return out, nil
}

type Cmd12Peripheral struct {
	Address uint8
	Type    uint8
}

type Cmd12RelaySettings struct {
	Flags      [8]uint8 // each value must be 0|1
	ImpulseSec uint8
	PauseSec   uint8
	Repeats    uint8
}

type Cmd12SettingsRecord struct {
	PINCode            uint32
	Server1            string
	Server2            string
	NTPServer          string
	Operator           string
	NetworkType        Cmd12NetworkType
	CountryCode        string
	SimPhone           string
	USSD               string
	AutoRebootHour     uint8 // 0 or 1..23
	ExtendedDebug      bool
	UseVoLTE           bool
	Interface485Active bool // false=passive, true=active address mode
	NTPEnabled         bool
	WiegandType        uint8
	Peripherals        [8]Cmd12Peripheral
	Relay1             Cmd12RelaySettings
	Relay2             Cmd12RelaySettings
	Input1Function     uint8
	Input2Function     uint8
	ReserveA           [292]byte
	ReserveB           [101]byte
}

func (r Cmd12SettingsRecord) Build() ([Cmd12RecordSize]byte, error) {
	var out [Cmd12RecordSize]byte

	if !IsCmd12NetworkTypeValid(uint8(r.NetworkType)) {
		return out, fmt.Errorf("cmd 12: network type out of range: %d", r.NetworkType)
	}
	if r.AutoRebootHour > 23 {
		return out, fmt.Errorf("cmd 12: auto reboot hour out of range: %d", r.AutoRebootHour)
	}
	if err := validateRelayFlags(r.Relay1.Flags); err != nil {
		return out, fmt.Errorf("cmd 12 relay1: %w", err)
	}
	if err := validateRelayFlags(r.Relay2.Flags); err != nil {
		return out, fmt.Errorf("cmd 12 relay2: %w", err)
	}

	s1, err := fixedASCII(r.Server1, 32)
	if err != nil {
		return out, fmt.Errorf("cmd 12 server1: %w", err)
	}
	s2, err := fixedASCII(r.Server2, 32)
	if err != nil {
		return out, fmt.Errorf("cmd 12 server2: %w", err)
	}
	ntp, err := fixedASCII(r.NTPServer, 32)
	if err != nil {
		return out, fmt.Errorf("cmd 12 ntp server: %w", err)
	}
	op, err := fixedASCII(r.Operator, 32)
	if err != nil {
		return out, fmt.Errorf("cmd 12 operator: %w", err)
	}
	cc, err := fixedASCII(r.CountryCode, 8)
	if err != nil {
		return out, fmt.Errorf("cmd 12 country code: %w", err)
	}
	sim, err := fixedASCII(r.SimPhone, 16)
	if err != nil {
		return out, fmt.Errorf("cmd 12 sim phone: %w", err)
	}
	ussd, err := fixedASCII(r.USSD, 8)
	if err != nil {
		return out, fmt.Errorf("cmd 12 ussd: %w", err)
	}

	binary.LittleEndian.PutUint32(out[cmd12OffPinCode:cmd12OffPinCode+4], r.PINCode)
	copy(out[cmd12OffServer1:cmd12OffServer1+32], s1)
	copy(out[cmd12OffServer2:cmd12OffServer2+32], s2)
	copy(out[cmd12OffNTPServer:cmd12OffNTPServer+32], ntp)
	copy(out[cmd12OffOperator:cmd12OffOperator+32], op)
	out[cmd12OffNetworkType] = uint8(r.NetworkType)
	copy(out[cmd12OffCountryCode:cmd12OffCountryCode+8], cc)
	copy(out[cmd12OffSimPhone:cmd12OffSimPhone+16], sim)
	copy(out[cmd12OffUSSD:cmd12OffUSSD+8], ussd)
	out[cmd12OffAutoReboot] = r.AutoRebootHour
	out[cmd12OffSettingsByte+0] = boolToByte(r.ExtendedDebug)
	out[cmd12OffSettingsByte+1] = boolToByte(r.UseVoLTE)
	out[cmd12OffSettingsByte+2] = boolToByte(r.Interface485Active)
	out[cmd12OffSettingsByte+3] = boolToByte(r.NTPEnabled)
	out[cmd12OffWiegandType] = r.WiegandType

	off := cmd12OffPeripheral
	for i := 0; i < 8; i++ {
		out[off] = r.Peripherals[i].Address
		out[off+1] = r.Peripherals[i].Type
		off += 2
	}

	writeRelay := func(base int, relay Cmd12RelaySettings) {
		for i := 0; i < 8; i++ {
			out[base+i] = relay.Flags[i]
		}
		out[base+8] = relay.ImpulseSec
		out[base+9] = relay.PauseSec
		out[base+10] = relay.Repeats
	}
	writeRelay(cmd12OffRelay1, r.Relay1)
	writeRelay(cmd12OffRelay2, r.Relay2)
	out[cmd12OffInput1] = r.Input1Function
	out[cmd12OffInput2] = r.Input2Function
	copy(out[cmd12OffReserveA:cmd12OffReserveA+292], r.ReserveA[:])
	copy(out[cmd12OffReserveB:cmd12OffReserveB+101], r.ReserveB[:])

	return out, nil
}

type Cmd12SettingsPayload struct {
	Timestamp uint32
	Record    Cmd12SettingsRecord
}

func (p Cmd12SettingsPayload) Build() ([]byte, error) {
	rec, err := p.Record.Build()
	if err != nil {
		return nil, err
	}
	out := make([]byte, 5+Cmd12RecordSize)
	binary.LittleEndian.PutUint32(out[0:4], p.Timestamp)
	out[4] = 1 // protocol currently allows one settings record
	copy(out[5:], rec[:])
	if err := ValidateServerCommandPayload(12, out); err != nil {
		return nil, err
	}
	return out, nil
}

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

type Cmd13IDRecord struct {
	SlotIndex       uint16
	ID              string // max 10 ASCII bytes, zero padded
	Group           uint8  // 0..64
	BanDate         uint32
	LifetimeMinutes uint16
	IDType          Cmd13IDType
	Reserve         [12]byte
}

func (r Cmd13IDRecord) Build() ([Cmd13RecordSize]byte, error) {
	var out [Cmd13RecordSize]byte
	if r.SlotIndex == 0 || r.SlotIndex > 50000 {
		return out, fmt.Errorf("cmd 13: slot out of range: %d", r.SlotIndex)
	}
	if r.Group > 64 {
		return out, fmt.Errorf("cmd 13: group out of range: %d", r.Group)
	}
	if !IsCmd13IDTypeValid(uint8(r.IDType)) {
		return out, fmt.Errorf("cmd 13: id type out of range: %d", r.IDType)
	}
	idBytes, err := fixedASCII(r.ID, 10)
	if err != nil {
		return out, fmt.Errorf("cmd 13: id: %w", err)
	}

	binary.LittleEndian.PutUint16(out[cmd13OffSlot:cmd13OffSlot+2], r.SlotIndex)
	copy(out[cmd13OffID:cmd13OffID+10], idBytes)
	out[cmd13OffGroup] = r.Group
	binary.LittleEndian.PutUint32(out[cmd13OffBanDate:cmd13OffBanDate+4], r.BanDate)
	binary.LittleEndian.PutUint16(out[cmd13OffLifetime:cmd13OffLifetime+2], r.LifetimeMinutes)
	out[cmd13OffIDType] = uint8(r.IDType)
	copy(out[cmd13OffReserve:cmd13OffReserve+12], r.Reserve[:])
	return out, nil
}

type Cmd13SyncIDStructuredPayload struct {
	Timestamp uint32
	Records   []Cmd13IDRecord
}

func (p Cmd13SyncIDStructuredPayload) Build() ([]byte, error) {
	if len(p.Records) < 1 || len(p.Records) > 250 {
		return nil, fmt.Errorf("cmd 13: records count out of range: %d", len(p.Records))
	}
	out := make([]byte, 5+len(p.Records)*Cmd13RecordSize)
	binary.LittleEndian.PutUint32(out[0:4], p.Timestamp)
	out[4] = uint8(len(p.Records))
	off := 5
	for i, rec := range p.Records {
		b, err := rec.Build()
		if err != nil {
			return nil, fmt.Errorf("cmd 13: record %d: %w", i, err)
		}
		copy(out[off:off+Cmd13RecordSize], b[:])
		off += Cmd13RecordSize
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

type Cmd14ScheduleRecord struct {
	Slot         uint8
	StartHour    uint8
	StartMinute  uint8
	EndHour      uint8
	EndMinute    uint8
	Flags        [8]uint8 // 0/1: blocked + weekdays
	Scenes       [8]uint8 // 0 or 1..64
	HolidayLogic uint8    // 0..3
	Reserve      [10]byte
}

func (r Cmd14ScheduleRecord) Build() ([Cmd14RecordSize]byte, error) {
	var out [Cmd14RecordSize]byte
	out[cmd14OffSlot] = r.Slot
	out[cmd14OffStartHour] = r.StartHour
	out[cmd14OffStartMinute] = r.StartMinute
	out[cmd14OffEndHour] = r.EndHour
	out[cmd14OffEndMinute] = r.EndMinute
	copy(out[cmd14OffFlags:cmd14OffFlags+8], r.Flags[:])
	copy(out[cmd14OffScenes:cmd14OffScenes+8], r.Scenes[:])
	out[cmd14OffHolidayLogic] = r.HolidayLogic
	copy(out[cmd14OffReserve:cmd14OffReserve+10], r.Reserve[:])

	raw := make([]byte, Cmd14RecordSize)
	copy(raw, out[:])
	// Reuse command-level validator by wrapping one record frame.
	p := make([]byte, 5+Cmd14RecordSize)
	p[4] = 1
	copy(p[5:], raw)
	if err := ValidateServerCommandPayload(14, p); err != nil {
		return out, err
	}
	return out, nil
}

type Cmd14SyncScheduleStructuredPayload struct {
	Timestamp uint32
	Records   []Cmd14ScheduleRecord
}

func (p Cmd14SyncScheduleStructuredPayload) Build() ([]byte, error) {
	if len(p.Records) < 1 || len(p.Records) > 64 {
		return nil, fmt.Errorf("cmd 14: records count out of range: %d", len(p.Records))
	}
	out := make([]byte, 5+len(p.Records)*Cmd14RecordSize)
	binary.LittleEndian.PutUint32(out[0:4], p.Timestamp)
	out[4] = uint8(len(p.Records))
	off := 5
	for i, rec := range p.Records {
		b, err := rec.Build()
		if err != nil {
			return nil, fmt.Errorf("cmd 14: record %d: %w", i, err)
		}
		copy(out[off:off+Cmd14RecordSize], b[:])
		off += Cmd14RecordSize
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

type Cmd15GroupRecord struct {
	Slot        uint8
	Schedule    uint8
	Permissions [8]uint8 // 0/1
	Reserve     [6]byte
}

func (r Cmd15GroupRecord) Build() ([Cmd15RecordSize]byte, error) {
	var out [Cmd15RecordSize]byte
	out[cmd15OffSlot] = r.Slot
	out[cmd15OffSchedule] = r.Schedule
	copy(out[cmd15OffPermFlags:cmd15OffPermFlags+8], r.Permissions[:])
	copy(out[cmd15OffReserve:cmd15OffReserve+6], r.Reserve[:])

	p := make([]byte, 5+Cmd15RecordSize)
	p[4] = 1
	copy(p[5:], out[:])
	if err := ValidateServerCommandPayload(15, p); err != nil {
		return out, err
	}
	return out, nil
}

type Cmd15SyncGroupStructuredPayload struct {
	Timestamp uint32
	Records   []Cmd15GroupRecord
}

func (p Cmd15SyncGroupStructuredPayload) Build() ([]byte, error) {
	if len(p.Records) < 1 || len(p.Records) > 64 {
		return nil, fmt.Errorf("cmd 15: records count out of range: %d", len(p.Records))
	}
	out := make([]byte, 5+len(p.Records)*Cmd15RecordSize)
	binary.LittleEndian.PutUint32(out[0:4], p.Timestamp)
	out[4] = uint8(len(p.Records))
	off := 5
	for i, rec := range p.Records {
		b, err := rec.Build()
		if err != nil {
			return nil, fmt.Errorf("cmd 15: record %d: %w", i, err)
		}
		copy(out[off:off+Cmd15RecordSize], b[:])
		off += Cmd15RecordSize
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
	if p.FileSize == 0 {
		return nil, fmt.Errorf("cmd 19: file size cannot be 0")
	}
	if p.FileCRC == 0 {
		return nil, fmt.Errorf("cmd 19: file crc cannot be 0")
	}
	if p.BlockCount == 0 {
		p.BlockCount = FWBlockCountForSize(p.FileSize)
	}
	if p.BlockCount != FWBlockCountForSize(p.FileSize) {
		return nil, fmt.Errorf("cmd 19: block count does not match file size")
	}
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

func FWBlockCountForSize(fileSize uint32) uint16 {
	if fileSize == 0 {
		return 0
	}
	const block = uint32(1024)
	blocks := (fileSize + block - 1) / block
	if blocks > 0xFFFF {
		return 0xFFFF
	}
	return uint16(blocks)
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

type Cmd25BindingResponsePayload struct {
	ErrorCode       uint8
	ObjectNumber    uint64 // preferred typed representation, encoded little-endian
	ObjectNumberRaw []byte // optional legacy representation, must be <= 8 bytes
	ObjectName      string // <= 128 ASCII bytes
	ControllerName  string // <= 128 ASCII bytes
	AdminPhone      string // <= 16 ASCII bytes
}

func (p Cmd25BindingResponsePayload) Build() ([]byte, error) {
	if p.ObjectNumber != 0 && len(p.ObjectNumberRaw) > 0 {
		return nil, fmt.Errorf("cmd 25: use either ObjectNumber or ObjectNumberRaw, not both")
	}
	if len(p.ObjectNumberRaw) > 8 {
		return nil, fmt.Errorf("cmd 25: object number too long: %d", len(p.ObjectNumberRaw))
	}
	name, err := fixedASCII(p.ObjectName, 128)
	if err != nil {
		return nil, fmt.Errorf("cmd 25: object name: %w", err)
	}
	controller, err := fixedASCII(p.ControllerName, 128)
	if err != nil {
		return nil, fmt.Errorf("cmd 25: controller name: %w", err)
	}
	phone, err := fixedASCII(p.AdminPhone, 16)
	if err != nil {
		return nil, fmt.Errorf("cmd 25: admin phone: %w", err)
	}

	out := make([]byte, 281)
	out[0] = p.ErrorCode
	if len(p.ObjectNumberRaw) > 0 {
		copy(out[1:9], p.ObjectNumberRaw)
	} else {
		binary.LittleEndian.PutUint64(out[1:9], p.ObjectNumber)
	}
	copy(out[9:137], name)
	copy(out[137:265], controller)
	copy(out[265:281], phone)
	if err := ValidateServerCommandPayload(25, out); err != nil {
		return nil, err
	}
	return out, nil
}

func BuildEmptyPayload(commandID uint8) ([]byte, error) {
	switch commandID {
	case 9, 10, 17, 21, 22:
		if err := ValidateServerCommandPayload(commandID, nil); err != nil {
			return nil, err
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("command %d does not use empty payload builder", commandID)
	}
}

func fixedASCII(s string, size int) ([]byte, error) {
	if len(s) > size {
		return nil, fmt.Errorf("too long: len=%d max=%d", len(s), size)
	}
	out := make([]byte, size)
	for i := 0; i < len(s); i++ {
		if s[i] > 127 {
			return nil, fmt.Errorf("non-ascii byte at index %d", i)
		}
		out[i] = s[i]
	}
	return out, nil
}

func fixedDigits(s string, size int) ([]byte, error) {
	if len(s) != size {
		return nil, fmt.Errorf("invalid length: len=%d want=%d", len(s), size)
	}
	out := make([]byte, size)
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return nil, fmt.Errorf("non-digit byte at index %d", i)
		}
		out[i] = s[i]
	}
	return out, nil
}

func validateRelayFlags(flags [8]uint8) error {
	for i := 0; i < len(flags); i++ {
		if flags[i] > 1 {
			return fmt.Errorf("flag %d must be 0 or 1", i)
		}
	}
	return nil
}

func boolToByte(v bool) uint8 {
	if v {
		return 1
	}
	return 0
}
