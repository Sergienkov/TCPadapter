package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"tcpadapter/internal/config"
	"tcpadapter/internal/session"
	"tcpadapter/internal/store"
)

func TestRunGracefulShutdownDrainsConnections(t *testing.T) {
	listenAddr := freeTCPAddr(t)
	cfg := config.Config{
		ListenAddr:         listenAddr,
		MetricsAddr:        "",
		RegistrationWindow: 30 * time.Second,
		HeartbeatTimeout:   30 * time.Second,
		MaxConnections:     10,
		MaxQueueDepth:      10,
		MaxQueueBytes:      1 << 20,
		QueueOverflow:      "drop_oldest",
		AckTimeout:         1 * time.Second,
		RetryBackoff:       100 * time.Millisecond,
		MaxRetries:         1,
		SweepInterval:      100 * time.Millisecond,
		WriteTimeout:       500 * time.Millisecond,
		ReadTimeout:        30 * time.Second,
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sessions := session.NewManager(cfg.MaxConnections, cfg.MaxQueueDepth, cfg.MaxQueueBytes, cfg.QueueOverflow, store.NewInMemoryStore())
	srv := New(cfg, logger, sessions, noopBus{})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Run(ctx)
	}()
	waitTCPReady(t, listenAddr, 3*time.Second)
	waitUntil(t, 2*time.Second, func() bool { return srv.active.Load() == 0 }, "expected no active connections after readiness probe")

	conn, err := net.DialTimeout("tcp", listenAddr, 500*time.Millisecond)
	if err != nil {
		cancel()
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srv.active.Load() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if srv.active.Load() < 1 {
		cancel()
		t.Fatalf("expected at least 1 active connection, got %d", srv.active.Load())
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop in time")
	}

	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 1)
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("expected client connection to be closed by server on shutdown")
	}

	if srv.active.Load() != 0 {
		t.Fatalf("expected no active connections after shutdown, got %d", srv.active.Load())
	}
}

func TestRunRejectsConnectionsOverPerIPLimit(t *testing.T) {
	listenAddr := freeTCPAddr(t)
	cfg := config.Config{
		ListenAddr:          listenAddr,
		MetricsAddr:         "",
		RegistrationWindow:  30 * time.Second,
		HeartbeatTimeout:    30 * time.Second,
		MaxConnections:      10,
		MaxConnectionsPerIP: 1,
		MaxQueueDepth:       10,
		MaxQueueBytes:       1 << 20,
		QueueOverflow:       "drop_oldest",
		AckTimeout:          1 * time.Second,
		RetryBackoff:        100 * time.Millisecond,
		MaxRetries:          1,
		SweepInterval:       100 * time.Millisecond,
		WriteTimeout:        500 * time.Millisecond,
		ReadTimeout:         30 * time.Second,
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sessions := session.NewManager(cfg.MaxConnections, cfg.MaxQueueDepth, cfg.MaxQueueBytes, cfg.QueueOverflow, store.NewInMemoryStore())
	srv := New(cfg, logger, sessions, noopBus{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Run(ctx)
	}()
	waitTCPReady(t, listenAddr, 3*time.Second)

	conn1, err := net.DialTimeout("tcp", listenAddr, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("first dial failed: %v", err)
	}
	defer conn1.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srv.active.Load() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if srv.active.Load() < 1 {
		t.Fatalf("expected first connection to be active, got %d", srv.active.Load())
	}

	conn2, err := net.DialTimeout("tcp", listenAddr, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("second dial failed: %v", err)
	}
	defer conn2.Close()

	if !waitConnClosed(conn2, 2*time.Second) {
		t.Fatal("expected second connection to be closed due to per-ip limit")
	}

	if got := srv.active.Load(); got != 1 {
		t.Fatalf("expected exactly 1 active connection, got %d", got)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop in time")
	}
}

func TestRunBlocksIPAfterRegistrationViolation(t *testing.T) {
	listenAddr := freeTCPAddr(t)
	cfg := config.Config{
		ListenAddr:          listenAddr,
		MetricsAddr:         "",
		RegistrationWindow:  30 * time.Second,
		HeartbeatTimeout:    30 * time.Second,
		MaxConnections:      10,
		MaxConnectionsPerIP: 0,
		IPViolationWindow:   30 * time.Second,
		IPViolationLimit:    2,
		IPBlockDuration:     2 * time.Second,
		MaxQueueDepth:       10,
		MaxQueueBytes:       1 << 20,
		QueueOverflow:       "drop_oldest",
		AckTimeout:          1 * time.Second,
		RetryBackoff:        100 * time.Millisecond,
		MaxRetries:          1,
		SweepInterval:       100 * time.Millisecond,
		WriteTimeout:        500 * time.Millisecond,
		ReadTimeout:         30 * time.Second,
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sessions := session.NewManager(cfg.MaxConnections, cfg.MaxQueueDepth, cfg.MaxQueueBytes, cfg.QueueOverflow, store.NewInMemoryStore())
	srv := New(cfg, logger, sessions, noopBus{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Run(ctx)
	}()
	waitTCPReady(t, listenAddr, 3*time.Second)

	conn1, err := net.DialTimeout("tcp", listenAddr, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("first dial failed: %v", err)
	}
	_ = writeFrame(conn1, buildStatus1Payload(1500)) // invalid first packet (not registration)
	if !waitConnClosed(conn1, 2*time.Second) {
		_ = conn1.Close()
		t.Fatal("expected first violating connection to be closed")
	}
	_ = conn1.Close()

	conn2, err := net.DialTimeout("tcp", listenAddr, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("second dial failed: %v", err)
	}
	defer conn2.Close()
	if !waitConnClosed(conn2, 2*time.Second) {
		t.Fatal("expected second connection to be blocked and closed")
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop in time")
	}
}

func TestReadyzSwitchesTo503DuringShutdown(t *testing.T) {
	listenAddr := freeTCPAddr(t)
	metricsAddr := freeTCPAddr(t)
	cfg := config.Config{
		ListenAddr:         listenAddr,
		MetricsAddr:        metricsAddr,
		RegistrationWindow: 30 * time.Second,
		HeartbeatTimeout:   30 * time.Second,
		MaxConnections:     10,
		MaxQueueDepth:      10,
		MaxQueueBytes:      1 << 20,
		QueueOverflow:      "drop_oldest",
		AckTimeout:         1 * time.Second,
		RetryBackoff:       100 * time.Millisecond,
		MaxRetries:         1,
		SweepInterval:      100 * time.Millisecond,
		WriteTimeout:       500 * time.Millisecond,
		ReadTimeout:        30 * time.Second,
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sessions := session.NewManager(cfg.MaxConnections, cfg.MaxQueueDepth, cfg.MaxQueueBytes, cfg.QueueOverflow, store.NewInMemoryStore())
	srv := New(cfg, logger, sessions, noopBus{})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Run(ctx)
	}()
	waitTCPReady(t, listenAddr, 3*time.Second)

	readyURL := "http://" + metricsAddr + "/readyz"
	waitHTTPStatus(t, readyURL, http.StatusOK, 3*time.Second)

	// Keep one TCP session active so shutdown enters drain path.
	conn, err := net.DialTimeout("tcp", listenAddr, 500*time.Millisecond)
	if err != nil {
		cancel()
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	cancel()

	waitHTTPStatus(t, readyURL, http.StatusServiceUnavailable, 2*time.Second)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop in time")
	}
}

func waitConnClosed(conn net.Conn, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	buf := make([]byte, 1)
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
		_, err := conn.Read(buf)
		if err == nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		errText := strings.ToLower(err.Error())
		if err == io.EOF || strings.Contains(errText, "closed") || strings.Contains(errText, "reset") || strings.Contains(errText, "broken pipe") {
			return true
		}
		return true
	}
	return false
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(msg)
}

func waitHTTPStatus(t *testing.T, url string, expected int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 150 * time.Millisecond}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == expected {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting %s to return %d", url, expected)
}
