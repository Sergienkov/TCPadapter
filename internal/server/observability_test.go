package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tcpadapter/internal/config"
	"tcpadapter/internal/kafka"
	"tcpadapter/internal/queue"
	"tcpadapter/internal/session"
	"tcpadapter/internal/store"
)

type noopBus struct{}

func (noopBus) PublishAck(context.Context, kafka.AckEvent)             {}
func (noopBus) PublishTelemetry(context.Context, kafka.TelemetryEvent) {}

func TestMetricsHandler(t *testing.T) {
	cfg := config.Config{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sessions := session.NewManager(10, 100, 1048576, "drop_oldest", store.NewInMemoryStore())
	srv := New(cfg, logger, sessions, noopBus{})

	srv.publishAck(context.Background(), kafka.AckEvent{Status: "accepted"})
	srv.publishAck(context.Background(), kafka.AckEvent{Status: "accepted"})
	srv.publishAck(context.Background(), kafka.AckEvent{Status: "expired"})
	srv.publishAck(context.Background(), kafka.AckEvent{Status: "failed", Reason: "parameter_error; code=1; attempts=2"})
	srv.publishAck(context.Background(), kafka.AckEvent{Status: "failed", Reason: "invalid payload: bad len"})
	srv.publishAck(context.Background(), kafka.AckEvent{Status: "unsupported", Reason: "command_not_supported; code=255"})
	srv.publishAckWithCommand(context.Background(), kafka.AckEvent{Status: "failed"}, queue.Command{
		MessageID: "m-latency",
		CommandID: 8,
		Priority:  queue.PriorityHigh,
		CreatedAt: time.Now().UTC().Add(-2 * time.Second),
	})
	srv.publishTelemetry(context.Background(), kafka.TelemetryEvent{ControllerID: "imei-1", CommandID: 2, TraceID: "rx-imei-1"}, "rx")
	srv.publishTelemetry(context.Background(), kafka.TelemetryEvent{ControllerID: "imei-1", CommandID: 11, TraceID: "trace-123"}, "command")
	srv.recordOverflowMetric(session.OverflowEvent{LimitType: "bytes", Policy: "reject", Action: "rejected"})
	srv.recordOverflowMetric(session.OverflowEvent{LimitType: "depth", Policy: "drop_oldest", Action: "dropped"})
	srv.ipViolationTotal.Add(3)
	srv.ipPerIPRejectTotal.Add(2)
	srv.ipBlockedRejectTotal.Add(1)
	srv.ipBlockAppliedTotal.Add(1)
	srv.recordShutdownDrain(1200*time.Millisecond, false)
	srv.recordShutdownDrain(500*time.Millisecond, true)
	_ = sessions.Enqueue("imei-1", queueCommand(10))
	_ = sessions.Enqueue("imei-1", queueCommand(20))
	_ = sessions.Enqueue("imei-2", queueCommand(5))
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	_, _ = sessions.Create("imei-3", c1)
	seq, _ := sessions.NextCommandSeq("imei-3")
	_ = sessions.RegisterInFlight("imei-3", seq, queueCommand(7), time.Now().UTC())

	req := httptest.NewRequest("GET", "/metrics", nil)
	rr := httptest.NewRecorder()
	srv.metricsHandler(rr, req)

	body := rr.Body.String()
	if rr.Code != 200 {
		t.Fatalf("unexpected status: %d", rr.Code)
	}
	if !strings.Contains(body, "tcpadapter_ack_total{status=\"accepted\"} 2") {
		t.Fatalf("expected accepted counter in metrics, got: %s", body)
	}
	if !strings.Contains(body, "tcpadapter_ack_total{status=\"expired\"} 1") {
		t.Fatalf("expected expired counter in metrics, got: %s", body)
	}
	if !strings.Contains(body, "tcpadapter_ack_terminal_total{status=\"failed\",reason=\"parameter_error\"} 1") {
		t.Fatalf("expected normalized terminal reason metric (parameter_error), got: %s", body)
	}
	if !strings.Contains(body, "tcpadapter_ack_terminal_total{status=\"failed\",reason=\"invalid_payload\"} 1") {
		t.Fatalf("expected normalized terminal reason metric (invalid_payload), got: %s", body)
	}
	if !strings.Contains(body, "tcpadapter_ack_terminal_total{status=\"unsupported\",reason=\"command_not_supported\"} 1") {
		t.Fatalf("expected normalized terminal reason metric (command_not_supported), got: %s", body)
	}
	if !strings.Contains(body, "tcpadapter_queue_overflow_total{limit=\"bytes\",policy=\"reject\",action=\"rejected\"} 1") {
		t.Fatalf("expected bytes/reject overflow metric, got: %s", body)
	}
	if !strings.Contains(body, "tcpadapter_queue_overflow_total{limit=\"depth\",policy=\"drop_oldest\",action=\"dropped\"} 1") {
		t.Fatalf("expected depth/drop overflow metric, got: %s", body)
	}
	if !strings.Contains(body, "tcpadapter_queue_depth_sum 3") {
		t.Fatalf("expected queue depth sum metric, got: %s", body)
	}
	if !strings.Contains(body, "tcpadapter_inflight_sum 1") {
		t.Fatalf("expected inflight sum metric, got: %s", body)
	}
	if !strings.Contains(body, "tcpadapter_online_sessions_total 1") {
		t.Fatalf("expected online sessions metric, got: %s", body)
	}
	if !strings.Contains(body, "tcpadapter_ack_latency_seconds_count 1") {
		t.Fatalf("expected ack latency count metric, got: %s", body)
	}
	if !strings.Contains(body, "tcpadapter_ack_latency_seconds_bucket{le=\"5\"} 1") {
		t.Fatalf("expected ack latency bucket metric, got: %s", body)
	}
	if !strings.Contains(body, "tcpadapter_telemetry_total{command_id=\"2\",trace_source=\"rx\"} 1") {
		t.Fatalf("expected telemetry rx metric, got: %s", body)
	}
	if !strings.Contains(body, "tcpadapter_telemetry_total{command_id=\"11\",trace_source=\"command\"} 1") {
		t.Fatalf("expected telemetry command metric, got: %s", body)
	}
	if !strings.Contains(body, "tcpadapter_ip_violations_total 3") {
		t.Fatalf("expected ip violations metric, got: %s", body)
	}
	if !strings.Contains(body, "tcpadapter_ip_rejected_total{reason=\"per_ip_limit\"} 2") {
		t.Fatalf("expected per_ip_limit rejects metric, got: %s", body)
	}
	if !strings.Contains(body, "tcpadapter_ip_rejected_total{reason=\"blocked\"} 1") {
		t.Fatalf("expected blocked rejects metric, got: %s", body)
	}
	if !strings.Contains(body, "tcpadapter_ip_blocks_applied_total 1") {
		t.Fatalf("expected ip blocks applied metric, got: %s", body)
	}
	if !strings.Contains(body, "tcpadapter_shutdown_drain_seconds_count 2") {
		t.Fatalf("expected shutdown drain count metric, got: %s", body)
	}
	if !strings.Contains(body, "tcpadapter_shutdown_drain_last_seconds 0.5") {
		t.Fatalf("expected shutdown drain last metric, got: %s", body)
	}
	if !strings.Contains(body, "tcpadapter_shutdown_drain_timeouts_total 1") {
		t.Fatalf("expected shutdown drain timeout metric, got: %s", body)
	}
}

func TestHealthzHandler(t *testing.T) {
	cfg := config.Config{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sessions := session.NewManager(10, 100, 1048576, "drop_oldest", store.NewInMemoryStore())
	srv := New(cfg, logger, sessions, noopBus{})

	req := httptest.NewRequest("GET", "/healthz", nil)
	rr := httptest.NewRecorder()
	srv.healthzHandler(rr, req)
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "ok") {
		t.Fatalf("expected ok body, got: %s", rr.Body.String())
	}
}

func TestReadyzHandler(t *testing.T) {
	cfg := config.Config{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sessions := session.NewManager(10, 100, 1048576, "drop_oldest", store.NewInMemoryStore())
	srv := New(cfg, logger, sessions, noopBus{})

	// Default before Run(): not ready
	req := httptest.NewRequest("GET", "/readyz", nil)
	rr := httptest.NewRecorder()
	srv.readyzHandler(rr, req)
	if rr.Code != 503 {
		t.Fatalf("expected 503 when not ready, got %d", rr.Code)
	}

	// Ready state
	srv.ready.Store(true)
	srv.shuttingDown.Store(false)
	rr = httptest.NewRecorder()
	srv.readyzHandler(rr, req)
	if rr.Code != 200 {
		t.Fatalf("expected 200 when ready, got %d", rr.Code)
	}

	// During shutdown/drain: not ready
	srv.shuttingDown.Store(true)
	rr = httptest.NewRecorder()
	srv.readyzHandler(rr, req)
	if rr.Code != 503 {
		t.Fatalf("expected 503 when shutting down, got %d", rr.Code)
	}
}

func queueCommand(payloadLen int) queue.Command {
	return queue.Command{
		MessageID: "m",
		CommandID: 8,
		Priority:  queue.PriorityHigh,
		CreatedAt: time.Now().UTC(),
		Payload:   make([]byte, payloadLen),
	}
}

func TestDebugQueuesHandler(t *testing.T) {
	cfg := config.Config{DebugLogs: true}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sessions := session.NewManager(10, 100, 1048576, "drop_oldest", store.NewInMemoryStore())
	srv := New(cfg, logger, sessions, noopBus{})

	_ = sessions.Enqueue("imei-a", queueCommand(10))
	_ = sessions.Enqueue("imei-a", queueCommand(20))
	_ = sessions.Enqueue("imei-b", queueCommand(10))

	req := httptest.NewRequest("GET", "/debug/queues?limit=1", nil)
	rr := httptest.NewRecorder()
	srv.debugQueuesHandler(rr, req)

	if rr.Code != 200 {
		t.Fatalf("unexpected status: %d", rr.Code)
	}

	var payload struct {
		Count int                           `json:"count"`
		Items []session.ControllerQueueStat `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json decode error: %v", err)
	}
	if payload.Count != 1 {
		t.Fatalf("expected count=1, got %d", payload.Count)
	}
	if len(payload.Items) != 1 || payload.Items[0].ControllerID != "imei-a" {
		t.Fatalf("unexpected items: %+v", payload.Items)
	}
}

func TestDebugQueuesHandler_Disabled(t *testing.T) {
	cfg := config.Config{DebugLogs: false}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sessions := session.NewManager(10, 100, 1048576, "drop_oldest", store.NewInMemoryStore())
	srv := New(cfg, logger, sessions, noopBus{})

	req := httptest.NewRequest("GET", "/debug/queues", nil)
	rr := httptest.NewRecorder()
	srv.debugQueuesHandler(rr, req)
	if rr.Code != 404 {
		t.Fatalf("expected 404 when debug disabled, got %d", rr.Code)
	}
}

func TestDebugEnqueueHandler(t *testing.T) {
	cfg := config.Config{DebugLogs: true}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sessions := session.NewManager(10, 100, 1048576, "drop_oldest", store.NewInMemoryStore())
	srv := New(cfg, logger, sessions, noopBus{})

	body := `{"controller_id":"imei-debug","command_id":9,"ttl_seconds":5,"trace_id":"trace-test-1"}`
	req := httptest.NewRequest("POST", "/debug/enqueue", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.debugEnqueueHandler(rr, req)
	if rr.Code != 202 {
		t.Fatalf("expected 202, got %d body=%s", rr.Code, rr.Body.String())
	}

	ctx, ok := sessions.DeliveryContext("imei-debug")
	if !ok || ctx.Queue.Len() != 1 {
		t.Fatalf("expected command in queue, ok=%v len=%d", ok, ctx.Queue.Len())
	}
	cmd, ok := ctx.Queue.PopNext(time.Now().UTC(), false, false)
	if !ok {
		t.Fatal("expected queued command to be readable")
	}
	if cmd.TraceID != "trace-test-1" {
		t.Fatalf("expected trace_id propagated into command, got %q", cmd.TraceID)
	}

	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response json decode error: %v", err)
	}
	if got, _ := payload["trace_id"].(string); got != "trace-test-1" {
		t.Fatalf("expected response trace_id=trace-test-1, got %q", got)
	}
}

func TestDebugEnqueueHandler_GeneratesTraceID(t *testing.T) {
	cfg := config.Config{DebugLogs: true}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sessions := session.NewManager(10, 100, 1048576, "drop_oldest", store.NewInMemoryStore())
	srv := New(cfg, logger, sessions, noopBus{})

	body := `{"controller_id":"imei-debug-2","command_id":9,"ttl_seconds":5}`
	req := httptest.NewRequest("POST", "/debug/enqueue", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.debugEnqueueHandler(rr, req)
	if rr.Code != 202 {
		t.Fatalf("expected 202, got %d body=%s", rr.Code, rr.Body.String())
	}

	ctx, ok := sessions.DeliveryContext("imei-debug-2")
	if !ok || ctx.Queue.Len() != 1 {
		t.Fatalf("expected command in queue, ok=%v len=%d", ok, ctx.Queue.Len())
	}
	cmd, ok := ctx.Queue.PopNext(time.Now().UTC(), false, false)
	if !ok {
		t.Fatal("expected queued command to be readable")
	}
	if strings.TrimSpace(cmd.TraceID) == "" {
		t.Fatal("expected generated trace_id in command")
	}
}

func TestDebugEnqueueHandler_BadPayload(t *testing.T) {
	cfg := config.Config{DebugLogs: true}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sessions := session.NewManager(10, 100, 1048576, "drop_oldest", store.NewInMemoryStore())
	srv := New(cfg, logger, sessions, noopBus{})

	// cmd20 requires 1026 bytes payload.
	body := `{"controller_id":"imei-debug","command_id":20,"payload_hex":"AA"}`
	req := httptest.NewRequest("POST", "/debug/enqueue", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.debugEnqueueHandler(rr, req)
	if rr.Code != 400 {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}
