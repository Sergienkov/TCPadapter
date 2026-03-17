package server

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"tcpadapter/internal/protocol"
)

type commandBuilderOption struct {
	Value any    `json:"value"`
	Label string `json:"label"`
}

type commandBuilderField struct {
	Name         string                 `json:"name"`
	Label        string                 `json:"label"`
	Kind         string                 `json:"kind"`
	Required     bool                   `json:"required,omitempty"`
	Placeholder  string                 `json:"placeholder,omitempty"`
	Help         string                 `json:"help,omitempty"`
	DefaultValue any                    `json:"default_value,omitempty"`
	Options      []commandBuilderOption `json:"options,omitempty"`
}

type commandBuilderSpec struct {
	CommandID uint8                 `json:"command_id"`
	Name      string                `json:"name"`
	Summary   string                `json:"summary"`
	Fields    []commandBuilderField `json:"fields"`
}

type debugBuildCommandRequest struct {
	CommandID  uint8           `json:"command_id"`
	Parameters json.RawMessage `json:"parameters"`
}

type commandBuildResponse struct {
	CommandID   uint8  `json:"command_id"`
	CommandName string `json:"command_name"`
	PayloadHex  string `json:"payload_hex"`
	PayloadLen  int    `json:"payload_len"`
	FrameHex    string `json:"frame_hex"`
	FrameLen    int    `json:"frame_len"`
}

func (s *Server) debugCommandBuilderHandler(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.DebugLogs {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(renderCommandBuilderHTML()))
}

func (s *Server) debugCommandSchemaHandler(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.DebugLogs {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": commandBuilderSpecs()})
}

func (s *Server) debugBuildCommandHandler(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.DebugLogs {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req debugBuildCommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	if req.CommandID == 0 {
		http.Error(w, "command_id is required", http.StatusBadRequest)
		return
	}

	payload, err := buildCommandPayload(req.CommandID, req.Parameters)
	if err != nil {
		http.Error(w, "build failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	frameBytes, err := protocol.EncodeFrame(protocol.Frame{
		TTL:     255,
		Seq:     1,
		Payload: append([]byte{req.CommandID}, payload...),
	})
	if err != nil {
		http.Error(w, "frame preview failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, http.StatusOK, commandBuildResponse{
		CommandID:   req.CommandID,
		CommandName: commandName(req.CommandID),
		PayloadHex:  strings.ToUpper(hex.EncodeToString(payload)),
		PayloadLen:  len(payload),
		FrameHex:    strings.ToUpper(hex.EncodeToString(frameBytes)),
		FrameLen:    len(frameBytes),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func commandName(commandID uint8) string {
	switch commandID {
	case protocol.CmdReboot:
		return "Reboot"
	case protocol.CmdFactoryReset:
		return "Factory Reset"
	case protocol.CmdSetTriggers:
		return "Set Triggers"
	case protocol.CmdCaptureID:
		return "Capture ID"
	case protocol.CmdRequestPhoto:
		return "Request Photo"
	case protocol.CmdControlOutputs:
		return "Control Outputs"
	case protocol.CmdRequestEvents:
		return "Request Events"
	case protocol.CmdRequestSMSIDs:
		return "Request SMS IDs"
	case protocol.CmdSendSMS:
		return "Send SMS"
	case protocol.CmdSyncSettings:
		return "Sync Settings"
	case protocol.CmdSyncIDs:
		return "Sync IDs"
	case protocol.CmdSyncSchedules:
		return "Sync Schedules"
	case protocol.CmdSyncGroups:
		return "Sync Groups"
	case protocol.CmdSyncHolidays:
		return "Sync Holidays"
	case protocol.CmdCRCAll:
		return "CRC All"
	case protocol.CmdCRCBlock:
		return "CRC Block"
	case protocol.CmdFWStart:
		return "FW Start"
	case protocol.CmdFWBlock:
		return "FW Block"
	case protocol.CmdPhotoUploadReq:
		return "Photo Upload Request"
	case protocol.CmdStatus2Req:
		return "Status2 Request"
	case protocol.CmdSetTime:
		return "Set Time"
	case protocol.CmdBindingResponse:
		return "Binding Response"
	case protocol.CmdCustom:
		return "Custom"
	default:
		return fmt.Sprintf("Command %d", commandID)
	}
}

func commandBuilderSpecs() []commandBuilderSpec {
	now := uint32(time.Now().Unix())
	return []commandBuilderSpec{
		{
			CommandID: protocol.CmdReboot,
			Name:      commandName(protocol.CmdReboot),
			Summary:   "Перезагрузка контроллера с задержкой в секундах.",
			Fields: []commandBuilderField{
				{Name: "timeout_sec", Label: "Timeout, sec", Kind: "number", Required: true, DefaultValue: 3},
			},
		},
		{
			CommandID: protocol.CmdFactoryReset,
			Name:      commandName(protocol.CmdFactoryReset),
			Summary:   "Сброс к заводским настройкам.",
			Fields: []commandBuilderField{
				{Name: "timeout_sec", Label: "Timeout, sec", Kind: "number", Required: true, DefaultValue: 5},
			},
		},
		{
			CommandID: protocol.CmdSetTriggers,
			Name:      commandName(protocol.CmdSetTriggers),
			Summary:   "Визуальные флаги для `cmd=5` без ручного hex.",
			Fields: []commandBuilderField{
				{Name: "manual_unlock", Label: "Manual Unlock", Kind: "checkbox", DefaultValue: false},
				{Name: "scenario_unlock", Label: "Scenario Unlock", Kind: "checkbox", DefaultValue: false},
				{Name: "auto_registration", Label: "Auto Registration", Kind: "checkbox", DefaultValue: true},
				{Name: "open_for_all", Label: "Open For All", Kind: "checkbox", DefaultValue: false},
				{Name: "forward_sms", Label: "Forward SMS", Kind: "checkbox", DefaultValue: false},
				{Name: "notify_unknown_id_sms", Label: "Notify Unknown ID SMS", Kind: "checkbox", DefaultValue: false},
				{Name: "notify_low_balance_sms", Label: "Notify Low Balance SMS", Kind: "checkbox", DefaultValue: false},
				{Name: "schedule_for_all_calls", Label: "Schedule For All Calls", Kind: "checkbox", DefaultValue: false},
			},
		},
		{
			CommandID: protocol.CmdCaptureID,
			Name:      commandName(protocol.CmdCaptureID),
			Summary:   "Захват идентификатора на интерфейсе.",
			Fields: []commandBuilderField{
				{Name: "capture_timeout", Label: "Capture Timeout", Kind: "number", Required: true, DefaultValue: 15},
				{Name: "interface", Label: "Interface", Kind: "number", Required: true, DefaultValue: 1},
				{Name: "network_address", Label: "Network Address", Kind: "number", Required: true, DefaultValue: 1},
			},
		},
		{
			CommandID: protocol.CmdRequestPhoto,
			Name:      commandName(protocol.CmdRequestPhoto),
			Summary:   "Запрос фото с камеры.",
			Fields: []commandBuilderField{
				{Name: "camera_address", Label: "Camera Address", Kind: "number", Required: true, DefaultValue: 1},
			},
		},
		{
			CommandID: protocol.CmdControlOutputs,
			Name:      commandName(protocol.CmdControlOutputs),
			Summary:   "Управление выходами.",
			Fields: []commandBuilderField{
				{Name: "source_id", Label: "Source ID", Kind: "number", Required: true, DefaultValue: 1},
				{Name: "output1", Label: "Output 1", Kind: "number", Required: true, DefaultValue: 0},
				{Name: "output2", Label: "Output 2", Kind: "number", Required: true, DefaultValue: 0},
			},
		},
		{CommandID: protocol.CmdRequestEvents, Name: commandName(protocol.CmdRequestEvents), Summary: "Команда без payload."},
		{CommandID: protocol.CmdRequestSMSIDs, Name: commandName(protocol.CmdRequestSMSIDs), Summary: "Команда без payload."},
		{
			CommandID: protocol.CmdSendSMS,
			Name:      commandName(protocol.CmdSendSMS),
			Summary:   "Отправка SMS.",
			Fields: []commandBuilderField{
				{Name: "phone10", Label: "Phone 10 digits", Kind: "text", Required: true, Placeholder: "9991234567"},
				{Name: "message", Label: "Message", Kind: "textarea", Required: true, Placeholder: "hello"},
			},
		},
		{
			CommandID: protocol.CmdSyncSettings,
			Name:      commandName(protocol.CmdSyncSettings),
			Summary:   "Большая команда: timestamp плюс JSON объекта `protocol.Cmd12SettingsRecord`.",
			Fields: []commandBuilderField{
				{Name: "timestamp", Label: "Timestamp", Kind: "number", Required: true, DefaultValue: now},
				{Name: "record_json", Label: "Record JSON", Kind: "json", Required: true, DefaultValue: map[string]any{
					"PINCode":            1234,
					"Server1":            "srv1.local",
					"Server2":            "srv2.local",
					"NTPServer":          "pool.ntp.org",
					"Operator":           "carrier",
					"NetworkType":        2,
					"CountryCode":        "250",
					"SimPhone":           "79990000000",
					"USSD":               "*100#",
					"AutoRebootHour":     0,
					"ExtendedDebug":      false,
					"UseVoLTE":           false,
					"Interface485Active": false,
					"NTPEnabled":         true,
					"WiegandType":        0,
				}},
			},
		},
		{
			CommandID: protocol.CmdSyncIDs,
			Name:      commandName(protocol.CmdSyncIDs),
			Summary:   "Timestamp плюс JSON-массив `protocol.Cmd13IDRecord`.",
			Fields: []commandBuilderField{
				{Name: "timestamp", Label: "Timestamp", Kind: "number", Required: true, DefaultValue: now},
				{Name: "records_json", Label: "Records JSON", Kind: "json", Required: true, DefaultValue: []map[string]any{
					{
						"SlotIndex":       1,
						"ID":              "123456",
						"Group":           1,
						"BanDate":         0,
						"LifetimeMinutes": 0,
						"IDType":          0,
					},
				}},
			},
		},
		{
			CommandID: protocol.CmdSyncSchedules,
			Name:      commandName(protocol.CmdSyncSchedules),
			Summary:   "Timestamp плюс JSON-массив `protocol.Cmd14ScheduleRecord`.",
			Fields: []commandBuilderField{
				{Name: "timestamp", Label: "Timestamp", Kind: "number", Required: true, DefaultValue: now},
				{Name: "records_json", Label: "Records JSON", Kind: "json", Required: true, DefaultValue: []map[string]any{
					{
						"Slot":         1,
						"StartHour":    9,
						"StartMinute":  0,
						"EndHour":      18,
						"EndMinute":    0,
						"HolidayLogic": 0,
					},
				}},
			},
		},
		{
			CommandID: protocol.CmdSyncGroups,
			Name:      commandName(protocol.CmdSyncGroups),
			Summary:   "Timestamp плюс JSON-массив `protocol.Cmd15GroupRecord`.",
			Fields: []commandBuilderField{
				{Name: "timestamp", Label: "Timestamp", Kind: "number", Required: true, DefaultValue: now},
				{Name: "records_json", Label: "Records JSON", Kind: "json", Required: true, DefaultValue: []map[string]any{
					{
						"Slot":     1,
						"Schedule": 1,
					},
				}},
			},
		},
		{
			CommandID: protocol.CmdSyncHolidays,
			Name:      commandName(protocol.CmdSyncHolidays),
			Summary:   "Timestamp плюс CSV unix days list.",
			Fields: []commandBuilderField{
				{Name: "timestamp", Label: "Timestamp", Kind: "number", Required: true, DefaultValue: now},
				{Name: "days_csv", Label: "Days CSV", Kind: "text", Required: true, Placeholder: "1711929600,1712016000"},
			},
		},
		{CommandID: protocol.CmdCRCAll, Name: commandName(protocol.CmdCRCAll), Summary: "Команда без payload."},
		{
			CommandID: protocol.CmdCRCBlock,
			Name:      commandName(protocol.CmdCRCBlock),
			Summary:   "CRC для блока.",
			Fields: []commandBuilderField{
				{Name: "block", Label: "Block", Kind: "number", Required: true, DefaultValue: 1},
			},
		},
		{
			CommandID: protocol.CmdFWStart,
			Name:      commandName(protocol.CmdFWStart),
			Summary:   "Старт обновления firmware.",
			Fields: []commandBuilderField{
				{Name: "firmware_number", Label: "Firmware Number", Kind: "number", Required: true, DefaultValue: 1},
				{Name: "build_timestamp", Label: "Build Timestamp", Kind: "number", Required: true, DefaultValue: now},
				{Name: "file_size", Label: "File Size", Kind: "number", Required: true, DefaultValue: 2048},
				{Name: "file_crc", Label: "File CRC", Kind: "number", Required: true, DefaultValue: 305419896},
				{Name: "block_count", Label: "Block Count (0=auto)", Kind: "number", DefaultValue: 0},
			},
		},
		{
			CommandID: protocol.CmdFWBlock,
			Name:      commandName(protocol.CmdFWBlock),
			Summary:   "Номер блока и 1024 байта data_hex.",
			Fields: []commandBuilderField{
				{Name: "block_number", Label: "Block Number", Kind: "number", Required: true, DefaultValue: 1},
				{Name: "data_hex", Label: "Data Hex", Kind: "textarea", Required: true, Placeholder: strings.Repeat("AA", 16) + "..."},
			},
		},
		{CommandID: protocol.CmdPhotoUploadReq, Name: commandName(protocol.CmdPhotoUploadReq), Summary: "Команда без payload."},
		{CommandID: protocol.CmdStatus2Req, Name: commandName(protocol.CmdStatus2Req), Summary: "Команда без payload."},
		{
			CommandID: protocol.CmdSetTime,
			Name:      commandName(protocol.CmdSetTime),
			Summary:   "Установка времени контроллера.",
			Fields: []commandBuilderField{
				{Name: "server_time", Label: "Server Time", Kind: "number", Required: true, DefaultValue: now},
			},
		},
		{
			CommandID: protocol.CmdBindingResponse,
			Name:      commandName(protocol.CmdBindingResponse),
			Summary:   "Ответ на привязку контроллера.",
			Fields: []commandBuilderField{
				{Name: "error_code", Label: "Error Code", Kind: "number", Required: true, DefaultValue: 0},
				{Name: "object_number", Label: "Object Number", Kind: "number", DefaultValue: 1},
				{Name: "object_number_raw_hex", Label: "Object Number Raw Hex", Kind: "text", Placeholder: "01020304"},
				{Name: "object_name", Label: "Object Name", Kind: "text", DefaultValue: "Gate A"},
				{Name: "controller_name", Label: "Controller Name", Kind: "text", DefaultValue: "Controller A"},
				{Name: "admin_phone", Label: "Admin Phone", Kind: "text", DefaultValue: "79990000000"},
			},
		},
		{
			CommandID: protocol.CmdCustom,
			Name:      commandName(protocol.CmdCustom),
			Summary:   "Произвольная custom-команда без ручной упаковки размера.",
			Fields: []commandBuilderField{
				{Name: "custom_command", Label: "Custom Command", Kind: "number", Required: true, DefaultValue: 1},
				{Name: "argument_hex", Label: "Argument Hex", Kind: "textarea", Placeholder: "01020304"},
			},
		},
	}
}

func buildCommandPayload(commandID uint8, raw json.RawMessage) ([]byte, error) {
	switch commandID {
	case protocol.CmdReboot:
		var req struct {
			TimeoutSec uint8 `json:"timeout_sec"`
		}
		if err := decodeBuilderParams(raw, &req); err != nil {
			return nil, err
		}
		return (protocol.Cmd2RebootPayload{TimeoutSec: req.TimeoutSec}).Build()
	case protocol.CmdFactoryReset:
		var req struct {
			TimeoutSec uint8 `json:"timeout_sec"`
		}
		if err := decodeBuilderParams(raw, &req); err != nil {
			return nil, err
		}
		return (protocol.Cmd3FactoryResetPayload{TimeoutSec: req.TimeoutSec}).Build()
	case protocol.CmdSetTriggers:
		var req struct {
			ManualUnlock        bool `json:"manual_unlock"`
			ScenarioUnlock      bool `json:"scenario_unlock"`
			AutoRegistration    bool `json:"auto_registration"`
			OpenForAll          bool `json:"open_for_all"`
			ForwardSMS          bool `json:"forward_sms"`
			NotifyUnknownIDSMS  bool `json:"notify_unknown_id_sms"`
			NotifyLowBalanceSMS bool `json:"notify_low_balance_sms"`
			ScheduleForAllCalls bool `json:"schedule_for_all_calls"`
		}
		if err := decodeBuilderParams(raw, &req); err != nil {
			return nil, err
		}
		return (protocol.Cmd5TriggerStructuredPayload{
			Flags: protocol.Cmd5TriggerFlags{
				ManualUnlock:        req.ManualUnlock,
				ScenarioUnlock:      req.ScenarioUnlock,
				AutoRegistration:    req.AutoRegistration,
				OpenForAll:          req.OpenForAll,
				ForwardSMS:          req.ForwardSMS,
				NotifyUnknownIDSMS:  req.NotifyUnknownIDSMS,
				NotifyLowBalanceSMS: req.NotifyLowBalanceSMS,
				ScheduleForAllCalls: req.ScheduleForAllCalls,
			},
		}).Build()
	case protocol.CmdCaptureID:
		var req struct {
			CaptureTimeout uint8 `json:"capture_timeout"`
			Interface      uint8 `json:"interface"`
			NetworkAddress uint8 `json:"network_address"`
		}
		if err := decodeBuilderParams(raw, &req); err != nil {
			return nil, err
		}
		return (protocol.Cmd6CaptureIDPayload{
			CaptureTimeout: req.CaptureTimeout,
			Interface:      req.Interface,
			NetworkAddress: req.NetworkAddress,
		}).Build()
	case protocol.CmdRequestPhoto:
		var req struct {
			CameraAddress uint8 `json:"camera_address"`
		}
		if err := decodeBuilderParams(raw, &req); err != nil {
			return nil, err
		}
		return (protocol.Cmd7PhotoRequestPayload{CameraAddress: req.CameraAddress}).Build()
	case protocol.CmdControlOutputs:
		var req struct {
			SourceID uint8 `json:"source_id"`
			Output1  uint8 `json:"output1"`
			Output2  uint8 `json:"output2"`
		}
		if err := decodeBuilderParams(raw, &req); err != nil {
			return nil, err
		}
		return (protocol.Cmd8OutputControlPayload{
			SourceID: req.SourceID,
			Output1:  req.Output1,
			Output2:  req.Output2,
		}).Build()
	case protocol.CmdRequestEvents, protocol.CmdRequestSMSIDs, protocol.CmdCRCAll, protocol.CmdPhotoUploadReq, protocol.CmdStatus2Req:
		return protocol.BuildEmptyPayload(commandID)
	case protocol.CmdSendSMS:
		var req struct {
			Phone10 string `json:"phone10"`
			Message string `json:"message"`
		}
		if err := decodeBuilderParams(raw, &req); err != nil {
			return nil, err
		}
		return (protocol.Cmd11SendSMSPayload{
			Phone10: strings.TrimSpace(req.Phone10),
			Message: req.Message,
		}).Build()
	case protocol.CmdSyncSettings:
		var req struct {
			Timestamp  uint32          `json:"timestamp"`
			RecordJSON json.RawMessage `json:"record_json"`
		}
		var record protocol.Cmd12SettingsRecord
		if err := decodeBuilderParams(raw, &req); err != nil {
			return nil, err
		}
		if len(req.RecordJSON) == 0 {
			return nil, fmt.Errorf("record_json is required")
		}
		if err := json.Unmarshal(req.RecordJSON, &record); err != nil {
			return nil, fmt.Errorf("record_json: %w", err)
		}
		return (protocol.Cmd12SettingsPayload{Timestamp: req.Timestamp, Record: record}).Build()
	case protocol.CmdSyncIDs:
		var req struct {
			Timestamp   uint32          `json:"timestamp"`
			RecordsJSON json.RawMessage `json:"records_json"`
		}
		var records []protocol.Cmd13IDRecord
		if err := decodeBuilderParams(raw, &req); err != nil {
			return nil, err
		}
		if len(req.RecordsJSON) == 0 {
			return nil, fmt.Errorf("records_json is required")
		}
		if err := json.Unmarshal(req.RecordsJSON, &records); err != nil {
			return nil, fmt.Errorf("records_json: %w", err)
		}
		return (protocol.Cmd13SyncIDStructuredPayload{Timestamp: req.Timestamp, Records: records}).Build()
	case protocol.CmdSyncSchedules:
		var req struct {
			Timestamp   uint32          `json:"timestamp"`
			RecordsJSON json.RawMessage `json:"records_json"`
		}
		var records []protocol.Cmd14ScheduleRecord
		if err := decodeBuilderParams(raw, &req); err != nil {
			return nil, err
		}
		if len(req.RecordsJSON) == 0 {
			return nil, fmt.Errorf("records_json is required")
		}
		if err := json.Unmarshal(req.RecordsJSON, &records); err != nil {
			return nil, fmt.Errorf("records_json: %w", err)
		}
		return (protocol.Cmd14SyncScheduleStructuredPayload{Timestamp: req.Timestamp, Records: records}).Build()
	case protocol.CmdSyncGroups:
		var req struct {
			Timestamp   uint32          `json:"timestamp"`
			RecordsJSON json.RawMessage `json:"records_json"`
		}
		var records []protocol.Cmd15GroupRecord
		if err := decodeBuilderParams(raw, &req); err != nil {
			return nil, err
		}
		if len(req.RecordsJSON) == 0 {
			return nil, fmt.Errorf("records_json is required")
		}
		if err := json.Unmarshal(req.RecordsJSON, &records); err != nil {
			return nil, fmt.Errorf("records_json: %w", err)
		}
		return (protocol.Cmd15SyncGroupStructuredPayload{Timestamp: req.Timestamp, Records: records}).Build()
	case protocol.CmdSyncHolidays:
		var req struct {
			Timestamp uint32 `json:"timestamp"`
			DaysCSV   string `json:"days_csv"`
		}
		if err := decodeBuilderParams(raw, &req); err != nil {
			return nil, err
		}
		days, err := parseUint32CSV(req.DaysCSV)
		if err != nil {
			return nil, err
		}
		return (protocol.Cmd16SyncHolidayPayload{Timestamp: req.Timestamp, Days: days}).Build()
	case protocol.CmdCRCBlock:
		var req struct {
			Block uint8 `json:"block"`
		}
		if err := decodeBuilderParams(raw, &req); err != nil {
			return nil, err
		}
		return (protocol.Cmd18CRCBlockPayload{Block: req.Block}).Build()
	case protocol.CmdFWStart:
		var req struct {
			FirmwareNumber uint16 `json:"firmware_number"`
			BuildTimestamp uint32 `json:"build_timestamp"`
			FileSize       uint32 `json:"file_size"`
			FileCRC        uint32 `json:"file_crc"`
			BlockCount     uint16 `json:"block_count"`
		}
		if err := decodeBuilderParams(raw, &req); err != nil {
			return nil, err
		}
		return (protocol.Cmd19StartFWPayload{
			FirmwareNumber: req.FirmwareNumber,
			BuildTimestamp: req.BuildTimestamp,
			FileSize:       req.FileSize,
			FileCRC:        req.FileCRC,
			BlockCount:     req.BlockCount,
		}).Build()
	case protocol.CmdFWBlock:
		var req struct {
			BlockNumber uint16 `json:"block_number"`
			DataHex     string `json:"data_hex"`
		}
		if err := decodeBuilderParams(raw, &req); err != nil {
			return nil, err
		}
		data, err := decodeHexString(req.DataHex)
		if err != nil {
			return nil, fmt.Errorf("data_hex: %w", err)
		}
		return (protocol.Cmd20FWBlockPayload{BlockNumber: req.BlockNumber, Data: data}).Build()
	case protocol.CmdSetTime:
		var req struct {
			ServerTime uint32 `json:"server_time"`
		}
		if err := decodeBuilderParams(raw, &req); err != nil {
			return nil, err
		}
		return (protocol.Cmd23SetTimePayload{ServerTime: req.ServerTime}).Build()
	case protocol.CmdBindingResponse:
		var req struct {
			ErrorCode          uint8  `json:"error_code"`
			ObjectNumber       uint64 `json:"object_number"`
			ObjectNumberRawHex string `json:"object_number_raw_hex"`
			ObjectName         string `json:"object_name"`
			ControllerName     string `json:"controller_name"`
			AdminPhone         string `json:"admin_phone"`
		}
		if err := decodeBuilderParams(raw, &req); err != nil {
			return nil, err
		}
		rawNumber, err := decodeHexString(req.ObjectNumberRawHex)
		if err != nil {
			return nil, fmt.Errorf("object_number_raw_hex: %w", err)
		}
		return (protocol.Cmd25BindingResponsePayload{
			ErrorCode:       req.ErrorCode,
			ObjectNumber:    req.ObjectNumber,
			ObjectNumberRaw: rawNumber,
			ObjectName:      req.ObjectName,
			ControllerName:  req.ControllerName,
			AdminPhone:      req.AdminPhone,
		}).Build()
	case protocol.CmdCustom:
		var req struct {
			CustomCommand uint8  `json:"custom_command"`
			ArgumentHex   string `json:"argument_hex"`
		}
		if err := decodeBuilderParams(raw, &req); err != nil {
			return nil, err
		}
		arg, err := decodeHexString(req.ArgumentHex)
		if err != nil {
			return nil, fmt.Errorf("argument_hex: %w", err)
		}
		return (protocol.Cmd27CustomPayload{CustomCommand: req.CustomCommand, Argument: arg}).Build()
	default:
		return nil, fmt.Errorf("command %d is not supported by builder", commandID)
	}
}

func decodeBuilderParams(raw json.RawMessage, out any) error {
	body := strings.TrimSpace(string(raw))
	if body == "" || body == "null" {
		body = "{}"
	}
	if err := json.Unmarshal([]byte(body), out); err != nil {
		return fmt.Errorf("parameters: %w", err)
	}
	return nil
}

func decodeHexString(s string) ([]byte, error) {
	trimmed := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\n', '\r', '\t':
			return -1
		default:
			return r
		}
	}, strings.TrimSpace(s))
	if trimmed == "" {
		return nil, nil
	}
	out, err := hex.DecodeString(trimmed)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func parseUint32CSV(s string) ([]uint32, error) {
	parts := strings.Split(strings.TrimSpace(s), ",")
	out := make([]uint32, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var value uint32
		if _, err := fmt.Sscanf(part, "%d", &value); err != nil {
			return nil, fmt.Errorf("invalid uint32 value %q", part)
		}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("days_csv must contain at least one unix day")
	}
	return out, nil
}

func renderCommandBuilderHTML() string {
	return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>TCPadapter Command Builder</title>
  <style>
    :root {
      --bg: #f4f1ea;
      --panel: #fffaf2;
      --ink: #1f1d1a;
      --muted: #6f6559;
      --line: #d8cebf;
      --accent: #b6542a;
      --accent-soft: #f3d5c6;
      --shadow: 0 12px 30px rgba(54, 38, 16, 0.08);
      --radius: 18px;
      font-family: "IBM Plex Sans", "Segoe UI", sans-serif;
    }
    * { box-sizing: border-box; }
    body { margin: 0; background: linear-gradient(180deg, #efe5d8 0, #f4f1ea 20%); color: var(--ink); }
    a { color: var(--accent); }
    .wrap { max-width: 1320px; margin: 0 auto; padding: 28px 20px 40px; }
    .hero, .layout { display: grid; gap: 18px; }
    .hero { grid-template-columns: 1.15fr 0.85fr; margin-bottom: 18px; }
    .layout { grid-template-columns: 0.95fr 1.05fr; }
    .panel {
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: var(--radius);
      box-shadow: var(--shadow);
      padding: 22px;
    }
    h1, h2, h3 { margin: 0 0 12px; }
    .sub { color: var(--muted); max-width: 80ch; margin-bottom: 14px; }
    .badge-row { display: flex; flex-wrap: wrap; gap: 10px; }
    .badge { border: 1px solid var(--line); border-radius: 999px; padding: 8px 12px; background: #fff; }
    .grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
    .full { grid-column: 1 / -1; }
    label { display: block; font-size: 13px; color: var(--muted); margin-bottom: 6px; }
    input, textarea, select, button {
      width: 100%;
      font: inherit;
      border-radius: 12px;
      border: 1px solid var(--line);
      padding: 11px 12px;
      background: #fff;
      color: var(--ink);
    }
    textarea { min-height: 120px; resize: vertical; }
    button { background: var(--accent); color: #fff; border: none; cursor: pointer; font-weight: 600; }
    button.secondary { background: #fff; color: var(--accent); border: 1px solid var(--accent); }
    .field { margin-bottom: 12px; }
    .checkbox {
      display: flex;
      align-items: center;
      gap: 10px;
      padding: 12px;
      border: 1px solid var(--line);
      border-radius: 12px;
      background: #fff;
    }
    .checkbox input { width: auto; margin: 0; }
    .hint, .muted { color: var(--muted); font-size: 13px; }
    .mono { font-family: "IBM Plex Mono", "SFMono-Regular", monospace; }
    .result {
      display: grid;
      gap: 12px;
    }
    .result pre {
      margin: 0;
      padding: 14px;
      border-radius: 14px;
      background: #181512;
      color: #f7eadb;
      overflow: auto;
    }
    @media (max-width: 960px) {
      .hero, .layout, .grid { grid-template-columns: 1fr; }
    }
  </style>
</head>
<body>
  <div class="wrap">
    <section class="panel hero">
      <div>
        <h1>Controller Command Builder</h1>
        <div class="sub">Полноценный конструктор payload и wire-frame без ручного пересчёта hex. Собираешь поля визуально, сервер строит payload через строгие builders из протокола, после этого можно сразу поставить команду в очередь.</div>
        <div class="badge-row">
          <span class="badge"><a href="/">Dashboard</a></span>
          <span class="badge"><a href="/debug/queues?limit=20">/debug/queues</a></span>
          <span class="badge"><a href="/debug/enqueue">/debug/enqueue</a></span>
          <span class="badge"><a href="/debug/command-schema">/debug/command-schema</a></span>
        </div>
      </div>
      <div>
        <div class="field">
          <label for="command-select">Command</label>
          <select id="command-select"></select>
        </div>
        <div id="command-summary" class="hint"></div>
      </div>
    </section>

    <div class="layout">
      <section class="panel">
        <h2>Payload Form</h2>
        <div id="form-fields"></div>
      </section>

      <section class="panel">
        <h2>Build And Queue</h2>
        <div class="grid">
          <div class="field">
            <label>controller_id / IMEI</label>
            <input id="queue-controller" placeholder="860000000000001">
          </div>
          <div class="field">
            <label>ttl_seconds</label>
            <input id="queue-ttl" type="number" min="0" value="10">
          </div>
          <div class="field">
            <label>message_id</label>
            <input id="queue-message" placeholder="builder-1">
          </div>
          <div class="field">
            <label>trace_id</label>
            <input id="queue-trace" placeholder="trace-builder-1">
          </div>
          <div class="field full">
            <button id="build-btn" type="button">Build Payload</button>
          </div>
          <div class="field full">
            <button id="queue-btn" class="secondary" type="button">Queue Built Command</button>
          </div>
        </div>
        <div class="result">
          <div class="hint" id="build-status">Select a command and press Build Payload.</div>
          <div>
            <label>payload_hex</label>
            <pre id="payload-hex" class="mono"></pre>
          </div>
          <div>
            <label>frame_hex preview (TTL=255, Seq=1)</label>
            <pre id="frame-hex" class="mono"></pre>
          </div>
        </div>
      </section>
    </div>
  </div>

  <script>
    const state = { specs: [], built: null };

    function commandSpec() {
      return state.specs.find((item) => String(item.command_id) === document.getElementById('command-select').value);
    }

    function prettyJSON(value) {
      return JSON.stringify(value, null, 2);
    }

    function escapeHTML(value) {
      return String(value ?? '').replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;');
    }

    function renderFields() {
      const spec = commandSpec();
      const host = document.getElementById('form-fields');
      document.getElementById('command-summary').textContent = spec ? spec.summary : '';
      if (!spec || !spec.fields || spec.fields.length === 0) {
        host.innerHTML = '<div class="muted">This command uses an empty payload.</div>';
        return;
      }
      host.innerHTML = spec.fields.map((field) => {
        const base = 'data-field="' + escapeHTML(field.name) + '"';
        if (field.kind === 'checkbox') {
          return '<div class="field"><label>' + escapeHTML(field.label) + '</label><div class="checkbox"><input type="checkbox" ' + base + (field.default_value ? ' checked' : '') + '><span>' + escapeHTML(field.help || field.label) + '</span></div></div>';
        }
        if (field.kind === 'json') {
          return '<div class="field"><label>' + escapeHTML(field.label) + '</label><textarea class="mono" ' + base + ' placeholder="' + escapeHTML(field.placeholder || '') + '">' + escapeHTML(prettyJSON(field.default_value || {})) + '</textarea><div class="hint">' + escapeHTML(field.help || 'JSON object/array accepted') + '</div></div>';
        }
        if (field.kind === 'textarea') {
          return '<div class="field"><label>' + escapeHTML(field.label) + '</label><textarea class="mono" ' + base + ' placeholder="' + escapeHTML(field.placeholder || '') + '">' + escapeHTML(field.default_value || '') + '</textarea></div>';
        }
        return '<div class="field"><label>' + escapeHTML(field.label) + '</label><input ' + (field.kind === 'number' ? 'type="number"' : 'type="text"') + ' ' + base + ' value="' + escapeHTML(field.default_value ?? '') + '" placeholder="' + escapeHTML(field.placeholder || '') + '"><div class="hint">' + escapeHTML(field.help || '') + '</div></div>';
      }).join('');
    }

    function collectParameters() {
      const spec = commandSpec();
      const result = {};
      if (!spec || !spec.fields) return result;
      for (const field of spec.fields) {
        const el = document.querySelector('[data-field="' + field.name + '"]');
        if (!el) continue;
        if (field.kind === 'checkbox') {
          result[field.name] = el.checked;
          continue;
        }
        const raw = el.value;
        if (field.kind === 'number') {
          if (raw !== '') result[field.name] = Number(raw);
          continue;
        }
        if (field.kind === 'json') {
          result[field.name] = raw.trim() === '' ? null : JSON.parse(raw);
          continue;
        }
        result[field.name] = raw;
      }
      return result;
    }

    async function buildPayload() {
      const spec = commandSpec();
      const payload = {
        command_id: Number(spec.command_id),
        parameters: collectParameters(),
      };
      const status = document.getElementById('build-status');
      status.textContent = 'Building...';
      const res = await fetch('/debug/build-command', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      const text = await res.text();
      if (!res.ok) {
        state.built = null;
        document.getElementById('payload-hex').textContent = '';
        document.getElementById('frame-hex').textContent = '';
        throw new Error(text);
      }
      const built = JSON.parse(text);
      state.built = built;
      document.getElementById('payload-hex').textContent = built.payload_hex || '(empty payload)';
      document.getElementById('frame-hex').textContent = built.frame_hex || '';
      status.textContent = built.command_name + ': payload ' + built.payload_len + ' bytes, frame ' + built.frame_len + ' bytes';
    }

    async function queueBuiltCommand() {
      if (!state.built) {
        await buildPayload();
      }
      const controllerID = document.getElementById('queue-controller').value.trim();
      if (!controllerID) {
        throw new Error('controller_id is required for queueing');
      }
      const body = {
        controller_id: controllerID,
        command_id: Number(commandSpec().command_id),
        ttl_seconds: Number(document.getElementById('queue-ttl').value || 0),
        payload_hex: state.built.payload_hex || '',
      };
      const messageID = document.getElementById('queue-message').value.trim();
      const traceID = document.getElementById('queue-trace').value.trim();
      if (messageID) body.message_id = messageID;
      if (traceID) body.trace_id = traceID;
      const res = await fetch('/debug/enqueue', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      const text = await res.text();
      if (!res.ok) {
        throw new Error(text);
      }
      document.getElementById('build-status').textContent = text;
    }

    async function init() {
      const res = await fetch('/debug/command-schema', { cache: 'no-store' });
      const data = await res.json();
      state.specs = data.items || [];
      const select = document.getElementById('command-select');
      select.innerHTML = state.specs.map((item) => (
        '<option value="' + item.command_id + '">' + item.command_id + ' - ' + escapeHTML(item.name) + '</option>'
      )).join('');
      select.value = '9';
      renderFields();
      select.addEventListener('change', () => {
        state.built = null;
        document.getElementById('payload-hex').textContent = '';
        document.getElementById('frame-hex').textContent = '';
        document.getElementById('build-status').textContent = 'Fill the fields and press Build Payload.';
        renderFields();
      });
      document.getElementById('build-btn').addEventListener('click', () => {
        buildPayload().catch((err) => {
          document.getElementById('build-status').textContent = err.message;
        });
      });
      document.getElementById('queue-btn').addEventListener('click', () => {
        queueBuiltCommand().catch((err) => {
          document.getElementById('build-status').textContent = err.message;
        });
      });
    }

    init().catch((err) => {
      document.getElementById('build-status').textContent = err.message;
    });
  </script>
</body>
</html>`
}
