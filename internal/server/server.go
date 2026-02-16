package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"tcpadapter/internal/config"
	"tcpadapter/internal/kafka"
	"tcpadapter/internal/protocol"
	"tcpadapter/internal/queue"
	"tcpadapter/internal/session"
)

type Server struct {
	cfg      config.Config
	logger   *slog.Logger
	sessions *session.Manager
	bus      kafka.Bus
	active   atomic.Int64
	flushMu  sync.Map
	ackMu    sync.Mutex
	ackStats map[string]uint64
	ovfMu    sync.Mutex
	ovfStats map[string]uint64
}

func New(cfg config.Config, logger *slog.Logger, sessions *session.Manager, bus kafka.Bus) *Server {
	return &Server{
		cfg:      cfg,
		logger:   logger,
		sessions: sessions,
		bus:      bus,
		ackStats: make(map[string]uint64),
		ovfStats: make(map[string]uint64),
	}
}

func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.cfg.ListenAddr, err)
	}
	s.logger.Info("tcp server started", "addr", s.cfg.ListenAddr)
	go s.sweepLoop(ctx)
	s.startObservability(ctx)

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			s.logger.Error("accept failed", "error", err)
			continue
		}
		if s.active.Load() >= int64(s.cfg.MaxConnections) {
			s.logger.Warn("rejecting connection: max connections reached", "remote", conn.RemoteAddr().String())
			_ = conn.Close()
			continue
		}
		s.active.Add(1)
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	registered := false
	controllerID := ""
	defer func() {
		if registered && controllerID != "" {
			s.sessions.MarkOffline(controllerID)
		}
		s.active.Add(-1)
		_ = conn.Close()
	}()

	remote := conn.RemoteAddr().String()
	reader := bufio.NewReader(conn)
	registrationDeadline := time.Now().Add(s.cfg.RegistrationWindow)

	for {
		if !registered && time.Now().After(registrationDeadline) {
			s.logger.Warn("registration timeout", "remote", remote)
			return
		}

		_ = conn.SetReadDeadline(time.Now().Add(s.cfg.ReadTimeout))
		frame, err := protocol.ReadFrame(reader)
		if err != nil {
			if isTimeout(err) {
				s.logger.Warn("connection read timeout", "remote", remote)
				return
			}
			s.logger.Warn("read frame error", "remote", remote, "error", err)
			return
		}

		cmdID, ok := frame.CommandID()
		if !ok {
			s.logger.Warn("empty payload", "remote", remote)
			return
		}

		if !registered {
			if cmdID != 1 {
				s.logger.Warn("first packet is not registration", "remote", remote, "cmd", cmdID)
				return
			}
			reg, err := protocol.ParseRegistrationPayload(frame.Payload)
			if err != nil {
				s.logger.Warn("registration parse failed", "remote", remote, "error", err)
				return
			}
			id := reg.IMEI
			if _, err := s.sessions.Create(id, conn); err != nil {
				s.logger.Warn("session create failed", "remote", remote, "controller_id", id, "error", err)
				return
			}
			registered = true
			controllerID = id
			s.logger.Info("controller registered", "controller_id", controllerID, "remote", remote)
		}

		s.sessions.Touch(controllerID)
		s.bus.PublishTelemetry(ctx, kafka.TelemetryEvent{ControllerID: controllerID, CommandID: cmdID, Payload: frame.Payload})

		switch cmdID {
		case 2:
			st, err := protocol.ParseStatus1Payload(frame.Payload)
			if err == nil {
				s.sessions.UpdateBufferFree(controllerID, int(st.BufferFree))
				// Controller advertises readiness for firmware update.
				if protocol.Status1ReadyForFW(st.Flags) {
					s.sessions.SetFWMode(controllerID, true)
				}
			}
		case 11:
			ack, err := protocol.ParseAck11Payload(frame.Payload)
			if err == nil {
				s.sessions.UpdateBufferFree(controllerID, int(ack.BufferFreeBytes))
				if ack.ExecutionCode == 0 {
					if cmd, attempts, ok := s.sessions.AckInFlight(controllerID, ack.CommandSeq); ok {
						s.publishAck(ctx, kafka.AckEvent{
							MessageID:    cmd.MessageID,
							ControllerID: controllerID,
							CommandID:    cmd.CommandID,
							CommandSeq:   ack.CommandSeq,
							Status:       "delivered",
							Reason:       fmt.Sprintf("attempts=%d", attempts),
						})
					}
				} else if ack.ExecutionCode != 5 {
					if cmd, _, ok := s.sessions.AckInFlight(controllerID, ack.CommandSeq); ok {
						s.publishAck(ctx, kafka.AckEvent{
							MessageID:    cmd.MessageID,
							ControllerID: controllerID,
							CommandID:    cmd.CommandID,
							CommandSeq:   ack.CommandSeq,
							Status:       "failed",
							Reason:       fmt.Sprintf("controller execution code=%d", ack.ExecutionCode),
						})
					}
				}
			}
		case 4:
			fw, err := protocol.ParseFWStatus4Payload(frame.Payload)
			if err == nil && (protocol.IsFWFinished(fw.ProgressOrErr) || protocol.IsFWFailed(fw.ProgressOrErr)) {
				s.sessions.SetFWMode(controllerID, false)
			}
		}

		s.processInFlight(ctx, controllerID)
		if err := s.withFlushLock(controllerID, func() error { return s.flushQueue(ctx, controllerID) }); err != nil {
			s.logger.Warn("flush queue failed", "controller_id", controllerID, "error", err)
			return
		}
	}
}

func (s *Server) flushQueue(ctx context.Context, controllerID string) error {
	dctx, ok := s.sessions.DeliveryContext(controllerID)
	if !ok {
		return session.ErrSessionNotFound
	}
	if dctx.Conn == nil {
		return nil
	}

	maxBudget := dctx.BufferFree
	if maxBudget <= 0 {
		maxBudget = 1500
	}
	remaining := maxBudget

	for remaining > 0 {
		scan := dctx.Queue.Len()
		if scan == 0 {
			return nil
		}
		skipped := make([]queue.Command, 0)
		sent := false

		for i := 0; i < scan; i++ {
			cmd, ok := dctx.Queue.PopNext(time.Now().UTC(), dctx.FWMode, dctx.SyncMode)
			if !ok {
				break
			}
			if cmd.Expired(time.Now().UTC()) {
				s.publishAck(ctx, kafka.AckEvent{
					MessageID:    cmd.MessageID,
					ControllerID: controllerID,
					CommandID:    cmd.CommandID,
					Status:       "expired",
					Reason:       "ttl expired before send",
				})
				continue
			}
			if !commandAllowedInModes(cmd.CommandID, dctx.FWMode, dctx.SyncMode) {
				skipped = append(skipped, cmd)
				continue
			}
			if cmd.CommandID >= 12 && cmd.CommandID <= 16 {
				s.sessions.SetSyncMode(controllerID, true)
				dctx.SyncMode = true
			}
			if cmd.CommandID == 19 {
				s.sessions.SetFWMode(controllerID, true)
				dctx.FWMode = true
			}

			payload := append([]byte{cmd.CommandID}, cmd.Payload...)
			if err := protocol.ValidateServerCommandPayload(cmd.CommandID, cmd.Payload); err != nil {
				s.publishAck(ctx, kafka.AckEvent{
					MessageID:    cmd.MessageID,
					ControllerID: controllerID,
					CommandID:    cmd.CommandID,
					Status:       "failed",
					Reason:       fmt.Sprintf("invalid payload: %v", err),
				})
				continue
			}
			seq, err := s.sessions.NextCommandSeq(controllerID)
			if err != nil {
				for _, sCmd := range skipped {
					dctx.Queue.Push(sCmd)
				}
				return err
			}
			frameBytes, err := protocol.EncodeFrame(protocol.Frame{TTL: ttlToWire(cmd.TTL), Seq: seq, Payload: payload})
			if err != nil {
				s.publishAck(ctx, kafka.AckEvent{MessageID: cmd.MessageID, ControllerID: controllerID, CommandID: cmd.CommandID, Status: "failed", Reason: err.Error()})
				continue
			}
			if len(frameBytes) > remaining {
				// Budget exhausted for this cycle. Put back and stop.
				dctx.Queue.Push(cmd)
				for _, sCmd := range skipped {
					dctx.Queue.Push(sCmd)
				}
				return nil
			}

			_ = dctx.Conn.SetWriteDeadline(time.Now().Add(s.cfg.WriteTimeout))
			if _, err := dctx.Conn.Write(frameBytes); err != nil {
				s.publishAck(ctx, kafka.AckEvent{MessageID: cmd.MessageID, ControllerID: controllerID, CommandID: cmd.CommandID, Status: "failed", Reason: err.Error()})
				for _, sCmd := range skipped {
					dctx.Queue.Push(sCmd)
				}
				return err
			}
			if err := s.sessions.RegisterInFlight(controllerID, seq, cmd, time.Now().UTC()); err != nil {
				s.publishAck(ctx, kafka.AckEvent{MessageID: cmd.MessageID, ControllerID: controllerID, CommandID: cmd.CommandID, Status: "failed", Reason: err.Error()})
				for _, sCmd := range skipped {
					dctx.Queue.Push(sCmd)
				}
				return err
			}
			remaining -= len(frameBytes)
			s.publishAck(ctx, kafka.AckEvent{MessageID: cmd.MessageID, ControllerID: controllerID, CommandID: cmd.CommandID, Status: "accepted", CommandSeq: seq})
			s.sessions.AckSent(controllerID)
			sent = true
			break
		}

		for _, sCmd := range skipped {
			dctx.Queue.Push(sCmd)
		}
		if !sent {
			return nil
		}

		if dctx.SyncMode && !dctx.Queue.HasSync() {
			s.sessions.SetSyncMode(controllerID, false)
			dctx.SyncMode = false
		}
		if dctx.FWMode && !dctx.Queue.HasFW() {
			// Keep FW mode enabled until status 4 completion, unless queue is drained and no progress state yet.
			// This fallback prevents deadlock on empty FW queue.
			s.sessions.SetFWMode(controllerID, false)
			dctx.FWMode = false
		}
	}
	return nil
}

func (s *Server) EnqueueCommand(controllerID string, cmd queue.Command) error {
	if cmd.CreatedAt.IsZero() {
		cmd.CreatedAt = time.Now().UTC()
	}
	normalized, err := normalizeCommandPriority(cmd)
	if err != nil {
		return err
	}
	events, err := s.sessions.EnqueueWithEvents(controllerID, normalized)
	for _, e := range events {
		s.recordOverflowMetric(e)
	}
	return err
}

func ttlToWire(ttl time.Duration) uint8 {
	if ttl <= 0 {
		return 255
	}
	sec := int(ttl / time.Second)
	if sec <= 0 {
		sec = 1
	}
	if sec > 250 {
		return 255
	}
	return uint8(sec)
}

func (s *Server) processInFlight(ctx context.Context, controllerID string) {
	outcomes, err := s.sessions.ProcessInFlight(
		controllerID,
		time.Now().UTC(),
		s.cfg.AckTimeout,
		s.cfg.RetryBackoff,
		s.cfg.MaxRetries,
	)
	if err != nil {
		return
	}
	for _, o := range outcomes {
		switch o.Type {
		case "retrying":
			s.publishAck(ctx, kafka.AckEvent{
				MessageID:    o.Command.MessageID,
				ControllerID: controllerID,
				CommandID:    o.Command.CommandID,
				CommandSeq:   o.Seq,
				Status:       "retrying",
				Reason:       o.Reason,
			})
		case "expired":
			s.publishAck(ctx, kafka.AckEvent{
				MessageID:    o.Command.MessageID,
				ControllerID: controllerID,
				CommandID:    o.Command.CommandID,
				CommandSeq:   o.Seq,
				Status:       "expired",
				Reason:       o.Reason,
			})
		case "failed":
			s.publishAck(ctx, kafka.AckEvent{
				MessageID:    o.Command.MessageID,
				ControllerID: controllerID,
				CommandID:    o.Command.CommandID,
				CommandSeq:   o.Seq,
				Status:       "failed",
				Reason:       o.Reason,
			})
		}
	}
}

func (s *Server) sweepLoop(ctx context.Context) {
	t := time.NewTicker(s.cfg.SweepInterval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			ids := s.sessions.ControllerIDs()
			for _, controllerID := range ids {
				s.processInFlight(ctx, controllerID)
				if err := s.withFlushLock(controllerID, func() error { return s.flushQueue(ctx, controllerID) }); err != nil {
					if err != session.ErrSessionNotFound {
						s.logger.Debug("sweep flush skipped", "controller_id", controllerID, "error", err)
					}
				}
			}
		}
	}
}

func (s *Server) withFlushLock(controllerID string, fn func() error) error {
	v, _ := s.flushMu.LoadOrStore(controllerID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	return fn()
}

func (s *Server) recordOverflowMetric(e session.OverflowEvent) {
	key := fmt.Sprintf("limit=%s,policy=%s,action=%s", e.LimitType, e.Policy, e.Action)
	s.ovfMu.Lock()
	s.ovfStats[key]++
	s.ovfMu.Unlock()
}

func isTimeout(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}
