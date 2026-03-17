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
	cfg                    config.Config
	logger                 *slog.Logger
	sessions               *session.Manager
	bus                    kafka.Bus
	active                 atomic.Int64
	ready                  atomic.Bool
	shuttingDown           atomic.Bool
	flushMu                sync.Map
	ackMu                  sync.Mutex
	ackStats               map[string]uint64
	ackTerminalReasonStats map[string]uint64
	ackLatencyBucketCounts []uint64
	ackLatencyCount        uint64
	ackLatencySumSeconds   float64
	telemetryMu            sync.Mutex
	telemetryStats         map[string]uint64
	ovfMu                  sync.Mutex
	ovfStats               map[string]uint64
	connMu                 sync.Mutex
	conns                  map[net.Conn]struct{}
	ipConnCount            map[string]int
	ipSec                  map[string]*ipSecurityState
	ipViolationTotal       atomic.Uint64
	ipBlockedRejectTotal   atomic.Uint64
	ipPerIPRejectTotal     atomic.Uint64
	ipBlockAppliedTotal    atomic.Uint64
	shutdownMu             sync.Mutex
	shutdownDrainCount     uint64
	shutdownDrainSumSecs   float64
	shutdownDrainLastSecs  float64
	shutdownDrainTimeouts  uint64
	connWG                 sync.WaitGroup
	debugEventsMu          sync.Mutex
	debugEvents            []DebugEvent
}

type DebugEvent struct {
	Timestamp    time.Time `json:"timestamp"`
	Kind         string    `json:"kind"`
	ControllerID string    `json:"controller_id"`
	RemoteAddr   string    `json:"remote_addr,omitempty"`
	CommandID    uint8     `json:"command_id"`
	CommandSeq   uint8     `json:"command_seq"`
	MessageID    string    `json:"message_id,omitempty"`
	TraceID      string    `json:"trace_id,omitempty"`
	Status       string    `json:"status,omitempty"`
	Reason       string    `json:"reason,omitempty"`
	PayloadLen   int       `json:"payload_len"`
}

type ipSecurityState struct {
	violations []time.Time
	blockedTil time.Time
}

func New(cfg config.Config, logger *slog.Logger, sessions *session.Manager, bus kafka.Bus) *Server {
	return &Server{
		cfg:                    cfg,
		logger:                 logger,
		sessions:               sessions,
		bus:                    bus,
		ackStats:               make(map[string]uint64),
		ackTerminalReasonStats: make(map[string]uint64),
		ackLatencyBucketCounts: make([]uint64, len(ackLatencyBoundsSeconds)),
		telemetryStats:         make(map[string]uint64),
		ovfStats:               make(map[string]uint64),
		conns:                  make(map[net.Conn]struct{}),
		ipConnCount:            make(map[string]int),
		ipSec:                  make(map[string]*ipSecurityState),
		debugEvents:            make([]DebugEvent, 0, 256),
	}
}

const shutdownDrainTimeout = 5 * time.Second
const shutdownReadinessGrace = 200 * time.Millisecond

func (s *Server) Run(ctx context.Context) error {
	s.ready.Store(false)
	s.shuttingDown.Store(false)
	ln, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.cfg.ListenAddr, err)
	}
	s.logger.Info("tcp server started", "addr", s.cfg.ListenAddr)
	go s.sweepLoop(ctx)
	obsSrv := s.startObservability()
	defer func() {
		if obsSrv == nil {
			return
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = obsSrv.Shutdown(shutdownCtx)
	}()
	s.ready.Store(true)

	go func() {
		<-ctx.Done()
		s.shuttingDown.Store(true)
		s.ready.Store(false)
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				time.Sleep(shutdownReadinessGrace)
				s.closeAllConns()
				drainDur, timedOut := s.waitForConnDrain(shutdownDrainTimeout)
				s.recordShutdownDrain(drainDur, timedOut)
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
		host := connRemoteHost(conn)
		if s.isIPBlocked(host, time.Now().UTC()) {
			s.ipBlockedRejectTotal.Add(1)
			s.logger.Warn("rejecting connection: ip temporarily blocked", "remote", conn.RemoteAddr().String())
			_ = conn.Close()
			continue
		}
		if !s.tryTrackConn(conn) {
			s.ipPerIPRejectTotal.Add(1)
			s.logger.Warn("rejecting connection: per-ip limit reached", "remote", conn.RemoteAddr().String(), "max_per_ip", s.cfg.MaxConnectionsPerIP)
			_ = conn.Close()
			continue
		}
		s.active.Add(1)
		s.connWG.Add(1)
		go func() {
			defer s.connWG.Done()
			s.handleConn(ctx, conn)
		}()
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	registered := false
	controllerID := ""
	lastHeartbeat := time.Now().UTC()
	defer func() {
		if registered && controllerID != "" {
			if err := s.sessions.MarkOffline(controllerID); err != nil {
				s.logger.Error("failed to persist offline session state", "controller_id", s.logControllerID(controllerID), "error", err)
			}
		}
		s.active.Add(-1)
		s.untrackConn(conn)
		_ = conn.Close()
	}()

	remote := conn.RemoteAddr().String()
	remoteHost := connRemoteHost(conn)
	reader := bufio.NewReader(conn)
	registrationDeadline := time.Now().Add(s.cfg.RegistrationWindow)

	for {
		if !registered && time.Now().After(registrationDeadline) {
			s.registerIPViolation(remoteHost, time.Now().UTC())
			s.logger.Warn("registration timeout", "remote", remote)
			return
		}
		if registered && s.cfg.HeartbeatTimeout > 0 && time.Since(lastHeartbeat) > s.cfg.HeartbeatTimeout {
			s.logger.Warn("heartbeat timeout", "remote", remote, "controller_id", s.logControllerID(controllerID), "timeout", s.cfg.HeartbeatTimeout.String())
			return
		}

		readWait := s.cfg.ReadTimeout
		if readWait <= 0 {
			readWait = 5 * time.Second
		}
		if registered && s.cfg.HeartbeatTimeout > 0 {
			untilHeartbeatTimeout := time.Until(lastHeartbeat.Add(s.cfg.HeartbeatTimeout))
			if untilHeartbeatTimeout <= 0 {
				s.logger.Warn("heartbeat timeout", "remote", remote, "controller_id", s.logControllerID(controllerID), "timeout", s.cfg.HeartbeatTimeout.String())
				return
			}
			if untilHeartbeatTimeout < readWait {
				readWait = untilHeartbeatTimeout
			}
		}
		_ = conn.SetReadDeadline(time.Now().Add(readWait))
		frame, err := protocol.ReadFrame(reader)
		if err != nil {
			if isTimeout(err) {
				s.logger.Warn("connection read timeout", "remote", remote)
				return
			}
			if !registered {
				s.registerIPViolation(remoteHost, time.Now().UTC())
			}
			s.logger.Warn("read frame error", "remote", remote, "error", err)
			return
		}

		cmdID, ok := frame.CommandID()
		if !ok {
			if !registered {
				s.registerIPViolation(remoteHost, time.Now().UTC())
			}
			s.logger.Warn("empty payload", "remote", remote)
			return
		}

		if !registered {
			if cmdID != 1 {
				s.registerIPViolation(remoteHost, time.Now().UTC())
				s.logger.Warn("first packet is not registration", "remote", remote, "cmd", cmdID)
				return
			}
			reg, err := protocol.ParseRegistrationPayload(frame.Payload)
			if err != nil {
				s.registerIPViolation(remoteHost, time.Now().UTC())
				s.logger.Warn("registration parse failed", "remote", remote, "error", err)
				return
			}
			id := reg.IMEI
			if _, err := s.sessions.Create(id, conn); err != nil {
				s.logger.Warn("session create failed", "remote", remote, "controller_id", s.logControllerID(id), "error", err)
				return
			}
			if err := s.sendRegistrationAck(conn, frame.Seq); err != nil {
				if offErr := s.sessions.MarkOffline(id); offErr != nil {
					s.logger.Error("failed to persist offline session state", "controller_id", s.logControllerID(id), "error", offErr)
				}
				s.logger.Warn("registration ack send failed", "remote", remote, "controller_id", s.logControllerID(id), "error", err)
				return
			}
			registered = true
			controllerID = id
			lastHeartbeat = time.Now().UTC()
			s.logger.Info("controller registered", "controller_id", s.logControllerID(controllerID), "remote", remote)
		}

		s.sessions.Touch(controllerID)
		traceID := newIncomingTraceID(controllerID, cmdID, frame.Seq)
		traceSource := "rx"

		switch cmdID {
		case 2:
			st, err := protocol.ParseStatus1Payload(frame.Payload)
			if err == nil {
				lastHeartbeat = time.Now().UTC()
				s.sessions.UpdateBufferFree(controllerID, int(st.BufferFree))
				// Controller advertises readiness for firmware update.
				if protocol.Status1ReadyForFW(st.Flags) {
					if err := s.sessions.SetFWMode(controllerID, true); err != nil {
						s.logger.Error("failed to persist fw mode", "controller_id", s.logControllerID(controllerID), "error", err)
						return
					}
				}
			}
		case 11:
			ack, err := protocol.ParseAck11Payload(frame.Payload)
			if err == nil {
				s.sessions.UpdateBufferFree(controllerID, int(ack.BufferFreeBytes))
				interp := protocol.InterpretAck11ExecutionCode(ack.ExecutionCode)
				if interp.Terminal {
					ackedSeq := ack.CommandSeq
					cmd, attempts, ok := s.sessions.AckInFlight(controllerID, ack.CommandSeq)
					if ok {
						if cmd.TraceID != "" {
							traceID = cmd.TraceID
							traceSource = "command"
						}
						s.publishAckWithCommand(ctx, kafka.AckEvent{
							MessageID:    cmd.MessageID,
							TraceID:      cmd.TraceID,
							ControllerID: controllerID,
							CommandID:    cmd.CommandID,
							CommandSeq:   ackedSeq,
							Status:       interp.Status,
							Reason:       fmt.Sprintf("%s; code=%d; attempts=%d", interp.Reason, ack.ExecutionCode, attempts),
						}, cmd)
					} else {
						inFlightSeqs := s.sessions.InFlightSeqs(controllerID)
						s.logger.Warn("terminal ack for unknown in-flight command",
							"controller_id", s.logControllerID(controllerID),
							"ack_seq", ack.CommandSeq,
							"code", ack.ExecutionCode,
							"in_flight_seqs", inFlightSeqs,
						)
					}
				} else {
					// Non-terminal update (e.g. command started). Keep in-flight entry.
					msgID := fmt.Sprintf("inflight-%s-%d", controllerID, ack.CommandSeq)
					ackTraceID := ""
					cmdID := uint8(0)
					if cmd, attempts, ok := s.sessions.PeekInFlight(controllerID, ack.CommandSeq); ok {
						msgID = cmd.MessageID
						ackTraceID = cmd.TraceID
						cmdID = cmd.CommandID
						_ = attempts
					}
					if ackTraceID != "" {
						// Reuse originating command trace for correlated telemetry.
						traceID = ackTraceID
						traceSource = "command"
					}
					s.publishAck(ctx, kafka.AckEvent{
						MessageID:    msgID,
						TraceID:      ackTraceID,
						ControllerID: controllerID,
						CommandID:    cmdID,
						CommandSeq:   ack.CommandSeq,
						Status:       interp.Status,
						Reason:       fmt.Sprintf("%s; code=%d", interp.Reason, ack.ExecutionCode),
					})
				}
			}
		case 4:
			fw, err := protocol.ParseFWStatus4Payload(frame.Payload)
			if err == nil && (protocol.IsFWFinished(fw.ProgressOrErr) || protocol.IsFWFailed(fw.ProgressOrErr)) {
				if err := s.sessions.SetFWMode(controllerID, false); err != nil {
					s.logger.Error("failed to persist fw mode", "controller_id", s.logControllerID(controllerID), "error", err)
					return
				}
			}
		}
		if s.cfg.DebugLogs {
			s.logger.Info("rx_frame", "controller_id", s.logControllerID(controllerID), "command_id", cmdID, "seq", frame.Seq, "trace_id", traceID, "payload_len", len(frame.Payload))
		}
		s.recordDebugEvent(DebugEvent{
			Timestamp:    time.Now().UTC(),
			Kind:         "rx",
			ControllerID: controllerID,
			RemoteAddr:   remote,
			CommandID:    cmdID,
			CommandSeq:   frame.Seq,
			TraceID:      traceID,
			PayloadLen:   len(frame.Payload),
		})
		s.publishTelemetry(ctx, kafka.TelemetryEvent{ControllerID: controllerID, CommandID: cmdID, TraceID: traceID, Payload: frame.Payload}, traceSource)

		s.processInFlight(ctx, controllerID)
		if err := s.withFlushLock(controllerID, func() error { return s.flushQueue(ctx, controllerID) }); err != nil {
			s.logger.Warn("flush queue failed", "controller_id", s.logControllerID(controllerID), "error", err)
			return
		}
	}
}

func (s *Server) sendRegistrationAck(conn net.Conn, seq uint8) error {
	payload, err := (protocol.Cmd1RegistrationAckPayload{
		ExecutionCode: protocol.AckCodeOK,
		ServerTime:    uint32(time.Now().Unix()),
	}).Build()
	if err != nil {
		return err
	}

	frame, err := protocol.EncodeFrame(protocol.Frame{
		TTL:     255,
		Seq:     seq,
		Payload: append([]byte{protocol.CmdRegistrationAck}, payload...),
	})
	if err != nil {
		return err
	}

	_ = conn.SetWriteDeadline(time.Now().Add(s.cfg.WriteTimeout))
	_, err = conn.Write(frame)
	return err
}

func (s *Server) flushQueue(ctx context.Context, controllerID string) error {
	dctx, ok := s.sessions.DeliveryContext(controllerID)
	if !ok {
		return session.ErrSessionNotFound
	}
	if dctx.Conn == nil {
		return nil
	}
	// Keep at most one outstanding command per controller.
	// This stabilizes ACK11 correlation by sequence under retries/timeouts.
	if s.sessions.InFlightCount(controllerID) > 0 {
		return nil
	}

	maxBudget := dctx.BufferFree
	if maxBudget <= 0 {
		maxBudget = 1500
	}
	remaining := maxBudget

	for remaining > 0 {
		for _, expired := range dctx.Queue.DrainExpired(time.Now().UTC()) {
			s.publishAckWithCommand(ctx, kafka.AckEvent{
				MessageID:    expired.MessageID,
				TraceID:      expired.TraceID,
				ControllerID: controllerID,
				CommandID:    expired.CommandID,
				Status:       "expired",
				Reason:       "ttl expired before send",
			}, expired)
		}
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
				s.publishAckWithCommand(ctx, kafka.AckEvent{
					MessageID:    cmd.MessageID,
					TraceID:      cmd.TraceID,
					ControllerID: controllerID,
					CommandID:    cmd.CommandID,
					Status:       "expired",
					Reason:       "ttl expired before send",
				}, cmd)
				continue
			}
			if !commandAllowedInModes(cmd.CommandID, dctx.FWMode, dctx.SyncMode) {
				skipped = append(skipped, cmd)
				continue
			}

			payload := append([]byte{cmd.CommandID}, cmd.Payload...)
			if err := protocol.ValidateServerCommandPayload(cmd.CommandID, cmd.Payload); err != nil {
				s.publishAckWithCommand(ctx, kafka.AckEvent{
					MessageID:    cmd.MessageID,
					TraceID:      cmd.TraceID,
					ControllerID: controllerID,
					CommandID:    cmd.CommandID,
					Status:       "failed",
					Reason:       fmt.Sprintf("invalid payload: %v", err),
				}, cmd)
				continue
			}
			seq := cmd.RetrySeq
			if seq == 0 {
				var err error
				seq, err = s.sessions.NextCommandSeq(controllerID)
				if err != nil {
					for _, sCmd := range skipped {
						dctx.Queue.Push(sCmd)
					}
					return err
				}
			}
			cmd.RetrySeq = 0
			frameBytes, err := protocol.EncodeFrame(protocol.Frame{TTL: ttlToWire(cmd.TTL), Seq: seq, Payload: payload})
			if err != nil {
				s.publishAckWithCommand(ctx, kafka.AckEvent{MessageID: cmd.MessageID, TraceID: cmd.TraceID, ControllerID: controllerID, CommandID: cmd.CommandID, Status: "failed", Reason: err.Error()}, cmd)
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
				s.publishAckWithCommand(ctx, kafka.AckEvent{MessageID: cmd.MessageID, TraceID: cmd.TraceID, ControllerID: controllerID, CommandID: cmd.CommandID, Status: "failed", Reason: err.Error()}, cmd)
				for _, sCmd := range skipped {
					dctx.Queue.Push(sCmd)
				}
				return err
			}
			if err := s.sessions.RegisterInFlight(controllerID, seq, cmd, time.Now().UTC()); err != nil {
				s.publishAckWithCommand(ctx, kafka.AckEvent{MessageID: cmd.MessageID, TraceID: cmd.TraceID, ControllerID: controllerID, CommandID: cmd.CommandID, Status: "failed", Reason: err.Error()}, cmd)
				for _, sCmd := range skipped {
					dctx.Queue.Push(sCmd)
				}
				return err
			}
			if cmd.CommandID >= 12 && cmd.CommandID <= 16 {
				if err := s.sessions.SetSyncMode(controllerID, true); err != nil {
					s.publishAckWithCommand(ctx, kafka.AckEvent{MessageID: cmd.MessageID, TraceID: cmd.TraceID, ControllerID: controllerID, CommandID: cmd.CommandID, Status: "failed", Reason: err.Error()}, cmd)
					return err
				}
				dctx.SyncMode = true
			}
			if cmd.CommandID == 19 {
				if err := s.sessions.SetFWMode(controllerID, true); err != nil {
					s.publishAckWithCommand(ctx, kafka.AckEvent{MessageID: cmd.MessageID, TraceID: cmd.TraceID, ControllerID: controllerID, CommandID: cmd.CommandID, Status: "failed", Reason: err.Error()}, cmd)
					return err
				}
				dctx.FWMode = true
			}
			s.recordDebugEvent(DebugEvent{
				Timestamp:    time.Now().UTC(),
				Kind:         "tx",
				ControllerID: controllerID,
				RemoteAddr:   dctx.Conn.RemoteAddr().String(),
				CommandID:    cmd.CommandID,
				CommandSeq:   seq,
				MessageID:    cmd.MessageID,
				TraceID:      cmd.TraceID,
				PayloadLen:   len(cmd.Payload) + 1,
			})
			remaining -= len(frameBytes)
			s.publishAck(ctx, kafka.AckEvent{MessageID: cmd.MessageID, TraceID: cmd.TraceID, ControllerID: controllerID, CommandID: cmd.CommandID, Status: "accepted", CommandSeq: seq})
			if err := s.sessions.AckSent(controllerID); err != nil {
				s.publishAckWithCommand(ctx, kafka.AckEvent{MessageID: cmd.MessageID, TraceID: cmd.TraceID, ControllerID: controllerID, CommandID: cmd.CommandID, CommandSeq: seq, Status: "failed", Reason: err.Error()}, cmd)
				return err
			}
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
			if err := s.sessions.SetSyncMode(controllerID, false); err != nil {
				return err
			}
			dctx.SyncMode = false
		}
		if dctx.FWMode && !dctx.Queue.HasFW() {
			// Keep FW mode enabled until status 4 completion, unless queue is drained and no progress state yet.
			// This fallback prevents deadlock on empty FW queue.
			if err := s.sessions.SetFWMode(controllerID, false); err != nil {
				return err
			}
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
		s.deliveryPolicyForCommand,
	)
	if err != nil {
		return
	}
	for _, o := range outcomes {
		switch o.Type {
		case "retrying":
			s.publishAck(ctx, kafka.AckEvent{
				MessageID:    o.Command.MessageID,
				TraceID:      o.Command.TraceID,
				ControllerID: controllerID,
				CommandID:    o.Command.CommandID,
				CommandSeq:   o.Seq,
				Status:       "retrying",
				Reason:       o.Reason,
			})
		case "expired":
			s.publishAckWithCommand(ctx, kafka.AckEvent{
				MessageID:    o.Command.MessageID,
				TraceID:      o.Command.TraceID,
				ControllerID: controllerID,
				CommandID:    o.Command.CommandID,
				CommandSeq:   o.Seq,
				Status:       "expired",
				Reason:       o.Reason,
			}, o.Command)
		case "failed":
			s.publishAckWithCommand(ctx, kafka.AckEvent{
				MessageID:    o.Command.MessageID,
				TraceID:      o.Command.TraceID,
				ControllerID: controllerID,
				CommandID:    o.Command.CommandID,
				CommandSeq:   o.Seq,
				Status:       "failed",
				Reason:       o.Reason,
			}, o.Command)
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
						s.logger.Debug("sweep flush skipped", "controller_id", s.logControllerID(controllerID), "error", err)
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

func (s *Server) untrackConn(conn net.Conn) {
	s.connMu.Lock()
	delete(s.conns, conn)
	host := connRemoteHost(conn)
	if n, ok := s.ipConnCount[host]; ok {
		if n <= 1 {
			delete(s.ipConnCount, host)
		} else {
			s.ipConnCount[host] = n - 1
		}
	}
	s.connMu.Unlock()
}

func (s *Server) tryTrackConn(conn net.Conn) bool {
	s.connMu.Lock()
	defer s.connMu.Unlock()

	host := connRemoteHost(conn)
	if s.cfg.MaxConnectionsPerIP > 0 && s.ipConnCount[host] >= s.cfg.MaxConnectionsPerIP {
		return false
	}
	s.conns[conn] = struct{}{}
	s.ipConnCount[host]++
	return true
}

func (s *Server) isIPBlocked(host string, now time.Time) bool {
	if s.cfg.IPViolationLimit <= 0 {
		return false
	}
	s.connMu.Lock()
	defer s.connMu.Unlock()
	st, ok := s.ipSec[host]
	if !ok {
		return false
	}
	if st.blockedTil.IsZero() {
		return false
	}
	if now.Before(st.blockedTil) {
		return true
	}
	st.blockedTil = time.Time{}
	st.violations = nil
	return false
}

func (s *Server) registerIPViolation(host string, now time.Time) {
	if s.cfg.IPViolationLimit <= 0 || host == "" {
		return
	}
	s.ipViolationTotal.Add(1)
	s.connMu.Lock()
	defer s.connMu.Unlock()

	st, ok := s.ipSec[host]
	if !ok {
		st = &ipSecurityState{}
		s.ipSec[host] = st
	}

	cutoff := now.Add(-s.cfg.IPViolationWindow)
	keep := st.violations[:0]
	for _, ts := range st.violations {
		if ts.After(cutoff) {
			keep = append(keep, ts)
		}
	}
	st.violations = append(keep, now)
	if len(st.violations) >= s.cfg.IPViolationLimit {
		st.blockedTil = now.Add(s.cfg.IPBlockDuration)
		st.violations = nil
		s.ipBlockAppliedTotal.Add(1)
	}
}

func (s *Server) closeAllConns() {
	s.connMu.Lock()
	conns := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.connMu.Unlock()

	for _, c := range conns {
		_ = c.Close()
	}
}

func (s *Server) waitForConnDrain(timeout time.Duration) (time.Duration, bool) {
	start := time.Now()
	done := make(chan struct{})
	go func() {
		s.connWG.Wait()
		close(done)
	}()

	select {
	case <-done:
		return time.Since(start), false
	case <-time.After(timeout):
		s.logger.Warn("shutdown drain timeout exceeded", "timeout", timeout.String(), "active", s.active.Load())
		return time.Since(start), true
	}
}

func connRemoteHost(conn net.Conn) string {
	remote := conn.RemoteAddr()
	if remote == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(remote.String())
	if err != nil {
		return remote.String()
	}
	return host
}

const maxDebugEvents = 256

func (s *Server) recordDebugEvent(event DebugEvent) {
	if !s.cfg.DebugLogs {
		return
	}
	s.debugEventsMu.Lock()
	defer s.debugEventsMu.Unlock()
	s.debugEvents = append(s.debugEvents, event)
	if len(s.debugEvents) > maxDebugEvents {
		s.debugEvents = append([]DebugEvent(nil), s.debugEvents[len(s.debugEvents)-maxDebugEvents:]...)
	}
}

func (s *Server) debugEventsSnapshot(limit int) []DebugEvent {
	s.debugEventsMu.Lock()
	defer s.debugEventsMu.Unlock()
	if limit <= 0 || limit > len(s.debugEvents) {
		limit = len(s.debugEvents)
	}
	out := make([]DebugEvent, 0, limit)
	for i := len(s.debugEvents) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, s.debugEvents[i])
	}
	return out
}

func newIncomingTraceID(controllerID string, cmdID, seq uint8) string {
	if controllerID == "" {
		controllerID = "unknown"
	}
	return fmt.Sprintf("rx-%s-%d-%d-%d", controllerID, cmdID, seq, time.Now().UnixNano())
}

func (s *Server) recordShutdownDrain(d time.Duration, timedOut bool) {
	s.shutdownMu.Lock()
	s.shutdownDrainCount++
	s.shutdownDrainSumSecs += d.Seconds()
	s.shutdownDrainLastSecs = d.Seconds()
	if timedOut {
		s.shutdownDrainTimeouts++
	}
	s.shutdownMu.Unlock()
}

func (s *Server) logControllerID(id string) string {
	if s.cfg.DebugLogs {
		return id
	}
	return maskControllerID(id)
}

func maskControllerID(id string) string {
	if id == "" {
		return ""
	}
	if len(id) <= 4 {
		return "****"
	}
	n := len(id)
	return "****" + id[n-4:]
}
