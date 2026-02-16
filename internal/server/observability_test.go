package server

import (
	"context"
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
