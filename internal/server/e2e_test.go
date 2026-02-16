package server

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tcpadapter/internal/config"
	"tcpadapter/internal/kafka"
	"tcpadapter/internal/protocol"
	"tcpadapter/internal/queue"
	"tcpadapter/internal/session"
	"tcpadapter/internal/store"
)

type captureBus struct {
	mu   sync.Mutex
	acks []kafka.AckEvent
}

func (b *captureBus) PublishAck(_ context.Context, e kafka.AckEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.acks = append(b.acks, e)
}

func (b *captureBus) PublishTelemetry(_ context.Context, _ kafka.TelemetryEvent) {}

func (b *captureBus) snapshot() []kafka.AckEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]kafka.AckEvent, len(b.acks))
	copy(out, b.acks)
	return out
}

func TestE2ERetryThenDelivered(t *testing.T) {
	listenAddr := freeTCPAddr(t)
	metricsAddr := freeTCPAddr(t)

	cfg := config.Config{
		ListenAddr:         listenAddr,
		MetricsAddr:        metricsAddr,
		RegistrationWindow: 2 * time.Second,
		HeartbeatTimeout:   30 * time.Second,
		MaxConnections:     100,
		MaxQueueDepth:      100,
		MaxQueueBytes:      1 << 20,
		QueueOverflow:      "drop_oldest",
		AckTimeout:         150 * time.Millisecond,
		RetryBackoff:       50 * time.Millisecond,
		MaxRetries:         3,
		SweepInterval:      40 * time.Millisecond,
		WriteTimeout:       1 * time.Second,
		ReadTimeout:        5 * time.Second,
		DebugLogs:          false,
		StateFile:          "",
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sessions := session.NewManager(cfg.MaxConnections, cfg.MaxQueueDepth, cfg.MaxQueueBytes, cfg.QueueOverflow, store.NewInMemoryStore())
	bus := &captureBus{}
	srv := New(cfg, logger, sessions, bus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Run(ctx)
	}()
	waitTCPReady(t, listenAddr, 3*time.Second)

	controllerID := "860000000000001"
	go runControllerDropFirstAck(t, ctx, listenAddr, controllerID)

	cmd := queue.Command{
		MessageID: "e2e-msg-1",
		CommandID: 9,
		TTL:       5 * time.Second,
		CreatedAt: time.Now().UTC(),
	}
	if err := srv.EnqueueCommand(controllerID, cmd); err != nil {
		t.Fatalf("EnqueueCommand() error = %v", err)
	}

	waitForAckStatus(t, bus, "e2e-msg-1", "retrying", 5*time.Second)
	waitForAckStatus(t, bus, "e2e-msg-1", "delivered", 5*time.Second)

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("server run error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop in time")
	}
}

func TestE2EExpireWhenNoAck(t *testing.T) {
	listenAddr := freeTCPAddr(t)
	metricsAddr := freeTCPAddr(t)

	cfg := config.Config{
		ListenAddr:         listenAddr,
		MetricsAddr:        metricsAddr,
		RegistrationWindow: 2 * time.Second,
		HeartbeatTimeout:   30 * time.Second,
		MaxConnections:     100,
		MaxQueueDepth:      100,
		MaxQueueBytes:      1 << 20,
		QueueOverflow:      "drop_oldest",
		AckTimeout:         150 * time.Millisecond,
		RetryBackoff:       50 * time.Millisecond,
		MaxRetries:         10,
		SweepInterval:      40 * time.Millisecond,
		WriteTimeout:       1 * time.Second,
		ReadTimeout:        5 * time.Second,
		DebugLogs:          false,
		StateFile:          "",
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sessions := session.NewManager(cfg.MaxConnections, cfg.MaxQueueDepth, cfg.MaxQueueBytes, cfg.QueueOverflow, store.NewInMemoryStore())
	bus := &captureBus{}
	srv := New(cfg, logger, sessions, bus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Run(ctx)
	}()
	waitTCPReady(t, listenAddr, 3*time.Second)

	controllerID := "860000000000002"
	go runControllerNoAck(t, ctx, listenAddr, controllerID)

	cmd := queue.Command{
		MessageID: "e2e-msg-expire",
		CommandID: 9,
		TTL:       400 * time.Millisecond,
		CreatedAt: time.Now().UTC(),
	}
	if err := srv.EnqueueCommand(controllerID, cmd); err != nil {
		t.Fatalf("EnqueueCommand() error = %v", err)
	}

	waitForAckStatus(t, bus, "e2e-msg-expire", "expired", 5*time.Second)

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("server run error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop in time")
	}
}

func TestE2EQueueOverflowRejectMetric(t *testing.T) {
	cfg := config.Config{
		MaxConnections: 10,
		MaxQueueDepth:  1,
		MaxQueueBytes:  1 << 20,
		QueueOverflow:  "reject",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sessions := session.NewManager(cfg.MaxConnections, cfg.MaxQueueDepth, cfg.MaxQueueBytes, cfg.QueueOverflow, store.NewInMemoryStore())
	srv := New(cfg, logger, sessions, &captureBus{})

	controllerID := "overflow-imei-1"
	if err := srv.EnqueueCommand(controllerID, queue.Command{
		MessageID: "ovf-1",
		CommandID: 9,
		TTL:       10 * time.Second,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("first enqueue error = %v", err)
	}
	err := srv.EnqueueCommand(controllerID, queue.Command{
		MessageID: "ovf-2",
		CommandID: 10,
		TTL:       10 * time.Second,
		CreatedAt: time.Now().UTC(),
	})
	if err != session.ErrQueueFull {
		t.Fatalf("expected ErrQueueFull, got %v", err)
	}

	req := httptest.NewRequest("GET", "/metrics", nil)
	rr := httptest.NewRecorder()
	srv.metricsHandler(rr, req)
	body := rr.Body.String()
	if !strings.Contains(body, "tcpadapter_queue_overflow_total{limit=\"depth\",policy=\"reject\",action=\"rejected\"} 1") {
		t.Fatalf("expected overflow metric, got: %s", body)
	}
}

func TestE2EQueueOverflowDropOldestMetricAndReplacement(t *testing.T) {
	cfg := config.Config{
		MaxConnections: 10,
		MaxQueueDepth:  1,
		MaxQueueBytes:  1 << 20,
		QueueOverflow:  "drop_oldest",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sessions := session.NewManager(cfg.MaxConnections, cfg.MaxQueueDepth, cfg.MaxQueueBytes, cfg.QueueOverflow, store.NewInMemoryStore())
	srv := New(cfg, logger, sessions, &captureBus{})

	controllerID := "overflow-imei-2"
	if err := srv.EnqueueCommand(controllerID, queue.Command{
		MessageID: "old-msg",
		CommandID: 9,
		TTL:       10 * time.Second,
		CreatedAt: time.Now().UTC().Add(-1 * time.Minute),
	}); err != nil {
		t.Fatalf("first enqueue error = %v", err)
	}
	if err := srv.EnqueueCommand(controllerID, queue.Command{
		MessageID: "new-msg",
		CommandID: 10,
		TTL:       10 * time.Second,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("second enqueue error = %v", err)
	}

	ctx, ok := sessions.DeliveryContext(controllerID)
	if !ok {
		t.Fatal("missing session after enqueue")
	}
	if ctx.Queue.Len() != 1 {
		t.Fatalf("expected queue len 1, got %d", ctx.Queue.Len())
	}
	cmd, ok := ctx.Queue.PopNext(time.Now().UTC(), false, false)
	if !ok || cmd.MessageID != "new-msg" {
		t.Fatalf("expected new message to remain, got ok=%v msg=%s", ok, cmd.MessageID)
	}

	req := httptest.NewRequest("GET", "/metrics", nil)
	rr := httptest.NewRecorder()
	srv.metricsHandler(rr, req)
	body := rr.Body.String()
	if !strings.Contains(body, "tcpadapter_queue_overflow_total{limit=\"depth\",policy=\"drop_oldest\",action=\"dropped\"} 1") {
		t.Fatalf("expected drop_oldest metric, got: %s", body)
	}
}

func TestE2ERestartRecoveryFromFileStore(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "sessions.json")
	controllerID := "860000000000099"
	messageID := "restore-msg-1"

	// First process instance: enqueue command while controller is offline.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store1 := store.NewFileStore(statePath)
	mgr1 := session.NewManager(100, 100, 1<<20, "drop_oldest", store1)
	srv1 := New(config.Config{}, logger, mgr1, &captureBus{})
	if err := srv1.EnqueueCommand(controllerID, queue.Command{
		MessageID: messageID,
		CommandID: 9,
		TTL:       5 * time.Second,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("enqueue in first instance failed: %v", err)
	}

	// Second process instance: restore state and deliver command after controller reconnect.
	listenAddr := freeTCPAddr(t)
	metricsAddr := freeTCPAddr(t)
	cfg := config.Config{
		ListenAddr:         listenAddr,
		MetricsAddr:        metricsAddr,
		RegistrationWindow: 2 * time.Second,
		HeartbeatTimeout:   30 * time.Second,
		MaxConnections:     100,
		MaxQueueDepth:      100,
		MaxQueueBytes:      1 << 20,
		QueueOverflow:      "drop_oldest",
		AckTimeout:         300 * time.Millisecond,
		RetryBackoff:       100 * time.Millisecond,
		MaxRetries:         2,
		SweepInterval:      50 * time.Millisecond,
		WriteTimeout:       1 * time.Second,
		ReadTimeout:        5 * time.Second,
	}
	store2 := store.NewFileStore(statePath)
	mgr2 := session.NewManager(cfg.MaxConnections, cfg.MaxQueueDepth, cfg.MaxQueueBytes, cfg.QueueOverflow, store2)
	if n, err := mgr2.Restore(); err != nil {
		t.Fatalf("restore failed: %v", err)
	} else if n < 1 {
		t.Fatalf("expected restored sessions, got %d", n)
	}
	bus2 := &captureBus{}
	srv2 := New(cfg, logger, mgr2, bus2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv2.Run(ctx) }()
	waitTCPReady(t, listenAddr, 3*time.Second)

	go runControllerAckAll(t, ctx, listenAddr, controllerID)
	waitForAckStatus(t, bus2, messageID, "delivered", 5*time.Second)

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("server run error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop in time")
	}
}

func TestE2EAckInProgressThenDelivered(t *testing.T) {
	listenAddr := freeTCPAddr(t)
	metricsAddr := freeTCPAddr(t)

	cfg := config.Config{
		ListenAddr:         listenAddr,
		MetricsAddr:        metricsAddr,
		RegistrationWindow: 2 * time.Second,
		HeartbeatTimeout:   30 * time.Second,
		MaxConnections:     100,
		MaxQueueDepth:      100,
		MaxQueueBytes:      1 << 20,
		QueueOverflow:      "drop_oldest",
		AckTimeout:         500 * time.Millisecond,
		RetryBackoff:       100 * time.Millisecond,
		MaxRetries:         2,
		SweepInterval:      40 * time.Millisecond,
		WriteTimeout:       1 * time.Second,
		ReadTimeout:        5 * time.Second,
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sessions := session.NewManager(cfg.MaxConnections, cfg.MaxQueueDepth, cfg.MaxQueueBytes, cfg.QueueOverflow, store.NewInMemoryStore())
	bus := &captureBus{}
	srv := New(cfg, logger, sessions, bus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()
	waitTCPReady(t, listenAddr, 3*time.Second)

	controllerID := "860000000000123"
	go runControllerAckInProgressThenDone(t, ctx, listenAddr, controllerID)

	cmd := queue.Command{
		MessageID: "e2e-msg-progress",
		CommandID: 9,
		TTL:       5 * time.Second,
		CreatedAt: time.Now().UTC(),
	}
	if err := srv.EnqueueCommand(controllerID, cmd); err != nil {
		t.Fatalf("EnqueueCommand() error = %v", err)
	}

	waitForAckStatus(t, bus, "e2e-msg-progress", "in_progress", 5*time.Second)
	waitForAckStatus(t, bus, "e2e-msg-progress", "delivered", 5*time.Second)

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("server run error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop in time")
	}
}

func runControllerDropFirstAck(t *testing.T, ctx context.Context, addr, imei string) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Errorf("dial failed: %v", err)
		return
	}
	defer conn.Close()

	if err := writeFrame(conn, buildRegistrationPayload(imei)); err != nil {
		t.Errorf("registration write failed: %v", err)
		return
	}
	if err := writeFrame(conn, buildStatus1Payload(1500)); err != nil {
		t.Errorf("status1 write failed: %v", err)
		return
	}

	r := bufio.NewReader(conn)
	dropped := false
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		frame, err := protocol.ReadFrame(r)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}
		cmdID, ok := frame.CommandID()
		if !ok {
			continue
		}
		if cmdID == 24 {
			continue
		}
		if !dropped {
			dropped = true
			continue
		}
		ack := buildAck11Payload(frame.Seq, 0, 1500)
		_ = writeFrame(conn, ack)
	}
}

func runControllerNoAck(t *testing.T, ctx context.Context, addr, imei string) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Errorf("dial failed: %v", err)
		return
	}
	defer conn.Close()

	if err := writeFrame(conn, buildRegistrationPayload(imei)); err != nil {
		t.Errorf("registration write failed: %v", err)
		return
	}
	if err := writeFrame(conn, buildStatus1Payload(1500)); err != nil {
		t.Errorf("status1 write failed: %v", err)
		return
	}

	r := bufio.NewReader(conn)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = writeFrame(conn, buildStatus1Payload(1500))
		default:
			_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			_, err := protocol.ReadFrame(r)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue
				}
				return
			}
			// Intentionally drop all acks.
		}
	}
}

func runControllerAckAll(t *testing.T, ctx context.Context, addr, imei string) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Errorf("dial failed: %v", err)
		return
	}
	defer conn.Close()

	if err := writeFrame(conn, buildRegistrationPayload(imei)); err != nil {
		t.Errorf("registration write failed: %v", err)
		return
	}
	if err := writeFrame(conn, buildStatus1Payload(1500)); err != nil {
		t.Errorf("status1 write failed: %v", err)
		return
	}

	r := bufio.NewReader(conn)
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = writeFrame(conn, buildStatus1Payload(1500))
		default:
			_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			frame, err := protocol.ReadFrame(r)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue
				}
				return
			}
			cmdID, ok := frame.CommandID()
			if !ok || cmdID == 24 {
				continue
			}
			_ = writeFrame(conn, buildAck11Payload(frame.Seq, 0, 1500))
		}
	}
}

func runControllerAckInProgressThenDone(t *testing.T, ctx context.Context, addr, imei string) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Errorf("dial failed: %v", err)
		return
	}
	defer conn.Close()

	if err := writeFrame(conn, buildRegistrationPayload(imei)); err != nil {
		t.Errorf("registration write failed: %v", err)
		return
	}
	if err := writeFrame(conn, buildStatus1Payload(1500)); err != nil {
		t.Errorf("status1 write failed: %v", err)
		return
	}

	r := bufio.NewReader(conn)
	seen := make(map[uint8]bool)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		frame, err := protocol.ReadFrame(r)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}
		cmdID, ok := frame.CommandID()
		if !ok || cmdID == 24 {
			continue
		}

		if !seen[frame.Seq] {
			seen[frame.Seq] = true
			_ = writeFrame(conn, buildAck11Payload(frame.Seq, 5, 1500)) // in_progress
			time.Sleep(120 * time.Millisecond)
			_ = writeFrame(conn, buildAck11Payload(frame.Seq, 0, 1500)) // done
		}
	}
}

func waitForAckStatus(t *testing.T, bus *captureBus, messageID, status string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		acks := bus.snapshot()
		for _, a := range acks {
			if a.MessageID == messageID && a.Status == status {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting ack status=%s for message=%s; got=%+v", status, messageID, bus.snapshot())
}

func freeTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free addr listen failed: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func waitTCPReady(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("tcp not ready: %s", addr)
}

func writeFrame(conn net.Conn, payload []byte) error {
	wire, err := protocol.EncodeFrame(protocol.Frame{TTL: 255, Seq: 1, Payload: payload})
	if err != nil {
		return err
	}
	_, err = conn.Write(wire)
	return err
}

func buildRegistrationPayload(imei string) []byte {
	b := make([]byte, 0, 18)
	b = append(b, 1)
	imeiBytes := []byte(imei)
	if len(imeiBytes) > 15 {
		imeiBytes = imeiBytes[:15]
	}
	pad := make([]byte, 15)
	copy(pad, imeiBytes)
	b = append(b, pad...)
	b = append(b, 1)
	b = append(b, 1)
	return b
}

func buildStatus1Payload(bufferFree uint16) []byte {
	b := make([]byte, 9)
	b[0] = 2
	binary.LittleEndian.PutUint32(b[1:5], uint32(time.Now().Unix()))
	binary.LittleEndian.PutUint16(b[5:7], 0)
	binary.LittleEndian.PutUint16(b[7:9], bufferFree)
	return b
}

func buildAck11Payload(seq, code uint8, bufferFree uint16) []byte {
	b := make([]byte, 9)
	b[0] = 11
	b[1] = seq
	b[2] = code
	binary.LittleEndian.PutUint32(b[3:7], uint32(time.Now().Unix()))
	binary.LittleEndian.PutUint16(b[7:9], bufferFree)
	return b
}
