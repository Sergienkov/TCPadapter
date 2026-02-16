package kafka

import (
	"context"
	"log/slog"
)

type AckEvent struct {
	MessageID    string
	ControllerID string
	CommandID    uint8
	CommandSeq   uint8
	Status       string
	Reason       string
}

type TelemetryEvent struct {
	ControllerID string
	CommandID    uint8
	Payload      []byte
}

type Bus interface {
	PublishAck(ctx context.Context, event AckEvent)
	PublishTelemetry(ctx context.Context, event TelemetryEvent)
}

type LoggingBus struct {
	logger *slog.Logger
}

func NewLoggingBus(logger *slog.Logger) *LoggingBus {
	return &LoggingBus{logger: logger}
}

func (b *LoggingBus) PublishAck(_ context.Context, event AckEvent) {
	b.logger.Info("ack_event",
		"message_id", event.MessageID,
		"controller_id", event.ControllerID,
		"command_id", event.CommandID,
		"command_seq", event.CommandSeq,
		"status", event.Status,
		"reason", event.Reason,
	)
}

func (b *LoggingBus) PublishTelemetry(_ context.Context, event TelemetryEvent) {
	b.logger.Info("telemetry_event",
		"controller_id", event.ControllerID,
		"command_id", event.CommandID,
		"payload_len", len(event.Payload),
	)
}
