package protocol

const (
	Cmd12RecordSize = 608

	cmd12OffPinCode      = 0
	cmd12OffServer1      = 4
	cmd12OffServer2      = 36
	cmd12OffNTPServer    = 68
	cmd12OffOperator     = 100
	cmd12OffNetworkType  = 132
	cmd12OffCountryCode  = 133
	cmd12OffSimPhone     = 141
	cmd12OffUSSD         = 157
	cmd12OffAutoReboot   = 165
	cmd12OffSettingsByte = 166 // 8 bytes: misc settings and reserved
	cmd12OffWiegandType  = 174
	cmd12OffPeripheral   = 175 // 16 bytes: 8 pairs address/type
	cmd12OffRelay1       = 191 // 11 bytes
	cmd12OffRelay2       = 202 // 11 bytes
	cmd12OffInput1       = 213
	cmd12OffInput2       = 214
	cmd12OffReserveA     = 215 // 292 bytes reserve block from spec
	cmd12OffReserveB     = 507 // trailing reserve till end of 608
)

type Cmd12NetworkType uint8

const (
	Cmd12NetworkAuto     Cmd12NetworkType = 0
	Cmd12NetworkGPRSOnly Cmd12NetworkType = 1
	Cmd12NetworkAuto3G4G Cmd12NetworkType = 2
	Cmd12Network3GOnly   Cmd12NetworkType = 3
	Cmd12Network4GOnly   Cmd12NetworkType = 4
)

func IsCmd12NetworkTypeValid(v uint8) bool {
	return v <= 4
}
