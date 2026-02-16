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
	srv.recordOverflowMetric(session.OverflowEvent{LimitType: "bytes", Policy: "reject", Action: "rejected"})
	srv.recordOverflowMetric(session.OverflowEvent{LimitType: "depth", Policy: "drop_oldest", Action: "dropped"})
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

	body := `{"controller_id":"imei-debug","command_id":9,"ttl_seconds":5}`
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
