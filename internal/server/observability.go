package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"tcpadapter/internal/kafka"
	"tcpadapter/internal/protocol"
	"tcpadapter/internal/queue"
)

var ackLatencyBoundsSeconds = []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60}

func (s *Server) startObservability() *http.Server {
	if s.cfg.MetricsAddr == "" {
		return nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.indexHandler)
	mux.HandleFunc("/healthz", s.healthzHandler)
	mux.HandleFunc("/readyz", s.readyzHandler)
	mux.HandleFunc("/metrics", s.metricsHandler)
	mux.HandleFunc("/debug/dashboard", s.debugDashboardHandler)
	mux.HandleFunc("/debug/connections", s.debugConnectionsHandler)
	mux.HandleFunc("/debug/events", s.debugEventsHandler)
	mux.HandleFunc("/debug/logs", s.debugLogsHandler)
	mux.HandleFunc("/debug/command-builder", s.debugCommandBuilderHandler)
	mux.HandleFunc("/debug/command-schema", s.debugCommandSchemaHandler)
	mux.HandleFunc("/debug/build-command", s.debugBuildCommandHandler)
	mux.HandleFunc("/debug/queues", s.debugQueuesHandler)
	mux.HandleFunc("/debug/enqueue", s.debugEnqueueHandler)

	httpSrv := &http.Server{Addr: s.cfg.MetricsAddr, Handler: mux}
	go func() {
		s.logger.Info("observability server started", "addr", s.cfg.MetricsAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("observability server failed", "error", err)
		}
	}()
	return httpSrv
}

func (s *Server) indexHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(renderDashboardHTML()))
}

func (s *Server) healthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) readyzHandler(w http.ResponseWriter, _ *http.Request) {
	if !s.ready.Load() || s.shuttingDown.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready\n"))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) publishAck(ctx context.Context, event kafka.AckEvent) {
	s.ackMu.Lock()
	s.ackStats[event.Status]++
	if isTerminalAckStatus(event.Status) {
		key := fmt.Sprintf("status=%s,reason=%s", event.Status, normalizeReasonLabel(event.Reason))
		s.ackTerminalReasonStats[key]++
	}
	s.ackMu.Unlock()
	s.recordDebugEvent(DebugEvent{
		Timestamp:    time.Now().UTC(),
		Kind:         "ack",
		ControllerID: event.ControllerID,
		CommandID:    event.CommandID,
		CommandSeq:   event.CommandSeq,
		MessageID:    event.MessageID,
		TraceID:      event.TraceID,
		Status:       event.Status,
		Reason:       event.Reason,
	})
	s.bus.PublishAck(ctx, event)
}

func (s *Server) publishTelemetry(ctx context.Context, event kafka.TelemetryEvent, traceSource string) {
	if traceSource == "" {
		traceSource = "rx"
	}
	key := fmt.Sprintf("command_id=%d,trace_source=%s", event.CommandID, traceSource)
	s.telemetryMu.Lock()
	s.telemetryStats[key]++
	s.telemetryMu.Unlock()
	s.bus.PublishTelemetry(ctx, event)
}

func (s *Server) publishAckWithCommand(ctx context.Context, event kafka.AckEvent, cmd queue.Command) {
	s.observeAckLatency(event.Status, cmd)
	s.publishAck(ctx, event)
}

func (s *Server) observeAckLatency(status string, cmd queue.Command) {
	if !isTerminalAckStatus(status) || cmd.CreatedAt.IsZero() {
		return
	}
	latencySeconds := time.Since(cmd.CreatedAt).Seconds()
	if latencySeconds < 0 {
		return
	}

	s.ackMu.Lock()
	defer s.ackMu.Unlock()

	s.ackLatencyCount++
	s.ackLatencySumSeconds += latencySeconds
	for i, bound := range ackLatencyBoundsSeconds {
		if latencySeconds <= bound {
			s.ackLatencyBucketCounts[i]++
			return
		}
	}
}

func isTerminalAckStatus(status string) bool {
	switch status {
	case "delivered", "failed", "expired", "unsupported", "duplicate":
		return true
	default:
		return false
	}
}

func (s *Server) metricsHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")

	var b strings.Builder
	b.WriteString("# HELP tcpadapter_active_connections Active TCP connections\n")
	b.WriteString("# TYPE tcpadapter_active_connections gauge\n")
	b.WriteString(fmt.Sprintf("tcpadapter_active_connections %d\n", s.active.Load()))

	b.WriteString("# HELP tcpadapter_sessions_total Known controller sessions\n")
	b.WriteString("# TYPE tcpadapter_sessions_total gauge\n")
	stats := s.sessions.SnapshotStats()
	b.WriteString(fmt.Sprintf("tcpadapter_sessions_total %d\n", stats.SessionCount))

	b.WriteString("# HELP tcpadapter_online_sessions_total Connected controller sessions\n")
	b.WriteString("# TYPE tcpadapter_online_sessions_total gauge\n")
	b.WriteString(fmt.Sprintf("tcpadapter_online_sessions_total %d\n", stats.OnlineSessionCount))

	b.WriteString("# HELP tcpadapter_queue_depth_sum Total commands queued across all controllers\n")
	b.WriteString("# TYPE tcpadapter_queue_depth_sum gauge\n")
	b.WriteString(fmt.Sprintf("tcpadapter_queue_depth_sum %d\n", stats.QueueDepthSum))
	b.WriteString("# HELP tcpadapter_queue_depth_max Max queue depth among controllers\n")
	b.WriteString("# TYPE tcpadapter_queue_depth_max gauge\n")
	b.WriteString(fmt.Sprintf("tcpadapter_queue_depth_max %d\n", stats.QueueDepthMax))

	b.WriteString("# HELP tcpadapter_queue_bytes_sum Total estimated queue bytes across all controllers\n")
	b.WriteString("# TYPE tcpadapter_queue_bytes_sum gauge\n")
	b.WriteString(fmt.Sprintf("tcpadapter_queue_bytes_sum %d\n", stats.QueueBytesSum))
	b.WriteString("# HELP tcpadapter_queue_bytes_max Max estimated queue bytes among controllers\n")
	b.WriteString("# TYPE tcpadapter_queue_bytes_max gauge\n")
	b.WriteString(fmt.Sprintf("tcpadapter_queue_bytes_max %d\n", stats.QueueBytesMax))

	b.WriteString("# HELP tcpadapter_inflight_sum Total in-flight commands across all controllers\n")
	b.WriteString("# TYPE tcpadapter_inflight_sum gauge\n")
	b.WriteString(fmt.Sprintf("tcpadapter_inflight_sum %d\n", stats.InFlightCountSum))
	b.WriteString("# HELP tcpadapter_inflight_max Max in-flight commands among controllers\n")
	b.WriteString("# TYPE tcpadapter_inflight_max gauge\n")
	b.WriteString(fmt.Sprintf("tcpadapter_inflight_max %d\n", stats.InFlightCountMax))

	b.WriteString("# HELP tcpadapter_ack_total ACK outcomes by status\n")
	b.WriteString("# TYPE tcpadapter_ack_total counter\n")

	s.ackMu.Lock()
	keys := make([]string, 0, len(s.ackStats))
	for k := range s.ackStats {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString(fmt.Sprintf("tcpadapter_ack_total{status=%q} %d\n", k, s.ackStats[k]))
	}
	b.WriteString("# HELP tcpadapter_ack_terminal_total Terminal ACK outcomes by status and normalized reason\n")
	b.WriteString("# TYPE tcpadapter_ack_terminal_total counter\n")
	rkeys := make([]string, 0, len(s.ackTerminalReasonStats))
	for k := range s.ackTerminalReasonStats {
		rkeys = append(rkeys, k)
	}
	sort.Strings(rkeys)
	for _, k := range rkeys {
		labels := parseAckTerminalReasonKey(k)
		b.WriteString(fmt.Sprintf(
			"tcpadapter_ack_terminal_total{status=%q,reason=%q} %d\n",
			labels["status"], labels["reason"], s.ackTerminalReasonStats[k],
		))
	}
	b.WriteString("# HELP tcpadapter_ack_latency_seconds End-to-end latency from command enqueue to terminal ack\n")
	b.WriteString("# TYPE tcpadapter_ack_latency_seconds histogram\n")
	cumulative := uint64(0)
	for i, le := range ackLatencyBoundsSeconds {
		cumulative += s.ackLatencyBucketCounts[i]
		b.WriteString(fmt.Sprintf("tcpadapter_ack_latency_seconds_bucket{le=%q} %d\n", strconv.FormatFloat(le, 'f', -1, 64), cumulative))
	}
	b.WriteString(fmt.Sprintf("tcpadapter_ack_latency_seconds_bucket{le=\"+Inf\"} %d\n", s.ackLatencyCount))
	b.WriteString(fmt.Sprintf("tcpadapter_ack_latency_seconds_sum %g\n", s.ackLatencySumSeconds))
	b.WriteString(fmt.Sprintf("tcpadapter_ack_latency_seconds_count %d\n", s.ackLatencyCount))
	s.ackMu.Unlock()

	b.WriteString("# HELP tcpadapter_telemetry_total Incoming telemetry frames by command and trace source\n")
	b.WriteString("# TYPE tcpadapter_telemetry_total counter\n")
	s.telemetryMu.Lock()
	tkeys := make([]string, 0, len(s.telemetryStats))
	for k := range s.telemetryStats {
		tkeys = append(tkeys, k)
	}
	sort.Strings(tkeys)
	for _, k := range tkeys {
		labels := parseTelemetryKey(k)
		b.WriteString(fmt.Sprintf(
			"tcpadapter_telemetry_total{command_id=%q,trace_source=%q} %d\n",
			labels["command_id"], labels["trace_source"], s.telemetryStats[k],
		))
	}
	s.telemetryMu.Unlock()

	b.WriteString("# HELP tcpadapter_queue_overflow_total Queue overflow events by limit and policy\n")
	b.WriteString("# TYPE tcpadapter_queue_overflow_total counter\n")
	s.ovfMu.Lock()
	ovfKeys := make([]string, 0, len(s.ovfStats))
	for k := range s.ovfStats {
		ovfKeys = append(ovfKeys, k)
	}
	sort.Strings(ovfKeys)
	for _, k := range ovfKeys {
		labels := parseOverflowKey(k)
		b.WriteString(fmt.Sprintf(
			"tcpadapter_queue_overflow_total{limit=%q,policy=%q,action=%q} %d\n",
			labels["limit"], labels["policy"], labels["action"], s.ovfStats[k],
		))
	}
	s.ovfMu.Unlock()

	b.WriteString("# HELP tcpadapter_ip_violations_total Protocol violations tracked per source IP\n")
	b.WriteString("# TYPE tcpadapter_ip_violations_total counter\n")
	b.WriteString(fmt.Sprintf("tcpadapter_ip_violations_total %d\n", s.ipViolationTotal.Load()))

	b.WriteString("# HELP tcpadapter_ip_rejected_total Rejected connections by IP protection reason\n")
	b.WriteString("# TYPE tcpadapter_ip_rejected_total counter\n")
	b.WriteString(fmt.Sprintf("tcpadapter_ip_rejected_total{reason=%q} %d\n", "per_ip_limit", s.ipPerIPRejectTotal.Load()))
	b.WriteString(fmt.Sprintf("tcpadapter_ip_rejected_total{reason=%q} %d\n", "blocked", s.ipBlockedRejectTotal.Load()))

	b.WriteString("# HELP tcpadapter_ip_blocks_applied_total Temporary IP blocks applied after violations\n")
	b.WriteString("# TYPE tcpadapter_ip_blocks_applied_total counter\n")
	b.WriteString(fmt.Sprintf("tcpadapter_ip_blocks_applied_total %d\n", s.ipBlockAppliedTotal.Load()))

	b.WriteString("# HELP tcpadapter_shutdown_drain_seconds Time spent draining connections during shutdown\n")
	b.WriteString("# TYPE tcpadapter_shutdown_drain_seconds summary\n")
	s.shutdownMu.Lock()
	b.WriteString(fmt.Sprintf("tcpadapter_shutdown_drain_seconds_sum %g\n", s.shutdownDrainSumSecs))
	b.WriteString(fmt.Sprintf("tcpadapter_shutdown_drain_seconds_count %d\n", s.shutdownDrainCount))
	s.shutdownMu.Unlock()

	b.WriteString("# HELP tcpadapter_shutdown_drain_last_seconds Last observed shutdown drain duration\n")
	b.WriteString("# TYPE tcpadapter_shutdown_drain_last_seconds gauge\n")
	s.shutdownMu.Lock()
	b.WriteString(fmt.Sprintf("tcpadapter_shutdown_drain_last_seconds %g\n", s.shutdownDrainLastSecs))
	b.WriteString("# HELP tcpadapter_shutdown_drain_timeouts_total Number of shutdown drains that hit timeout\n")
	b.WriteString("# TYPE tcpadapter_shutdown_drain_timeouts_total counter\n")
	b.WriteString(fmt.Sprintf("tcpadapter_shutdown_drain_timeouts_total %d\n", s.shutdownDrainTimeouts))
	s.shutdownMu.Unlock()

	_, _ = w.Write([]byte(b.String()))
}

func parseOverflowKey(key string) map[string]string {
	out := map[string]string{"limit": "", "policy": "", "action": ""}
	parts := strings.Split(key, ",")
	for _, p := range parts {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) != 2 {
			continue
		}
		out[kv[0]] = kv[1]
	}
	return out
}

func parseAckTerminalReasonKey(key string) map[string]string {
	out := map[string]string{"status": "", "reason": ""}
	parts := strings.Split(key, ",")
	for _, p := range parts {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) != 2 {
			continue
		}
		out[kv[0]] = kv[1]
	}
	return out
}

func parseTelemetryKey(key string) map[string]string {
	out := map[string]string{"command_id": "", "trace_source": ""}
	parts := strings.Split(key, ",")
	for _, p := range parts {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) != 2 {
			continue
		}
		out[kv[0]] = kv[1]
	}
	return out
}

func normalizeReasonLabel(reason string) string {
	r := strings.TrimSpace(reason)
	if r == "" {
		return "unspecified"
	}
	if i := strings.Index(r, ";"); i > 0 {
		r = strings.TrimSpace(r[:i])
	}
	if i := strings.Index(r, ":"); i > 0 {
		prefix := strings.TrimSpace(r[:i])
		if prefix != "" {
			r = prefix
		}
	}
	r = strings.ToLower(r)
	r = strings.ReplaceAll(r, " ", "_")
	if len(r) > 48 {
		r = r[:48]
	}
	if r == "" {
		return "unspecified"
	}
	return r
}

type dashboardSummary struct {
	ActiveConnections       int64   `json:"active_connections"`
	KnownSessions           int     `json:"known_sessions"`
	OnlineSessions          int     `json:"online_sessions"`
	QueueDepthSum           int     `json:"queue_depth_sum"`
	QueueDepthMax           int     `json:"queue_depth_max"`
	InFlightSum             int     `json:"inflight_sum"`
	InFlightMax             int     `json:"inflight_max"`
	IPViolations            uint64  `json:"ip_violations"`
	IPBlockedRejects        uint64  `json:"ip_blocked_rejects"`
	IPPerIPRejects          uint64  `json:"ip_per_ip_rejects"`
	ShutdownDrainLastSecs   float64 `json:"shutdown_drain_last_seconds"`
	ShutdownDrainTimeouts   uint64  `json:"shutdown_drain_timeouts"`
	AckAccepted             uint64  `json:"ack_accepted"`
	AckRetrying             uint64  `json:"ack_retrying"`
	AckFailed               uint64  `json:"ack_failed"`
	AckExpired              uint64  `json:"ack_expired"`
	AckDelivered            uint64  `json:"ack_delivered"`
	TelemetryFramesObserved uint64  `json:"telemetry_frames_observed"`
}

func (s *Server) dashboardSummary() dashboardSummary {
	stats := s.sessions.SnapshotStats()
	var telemetryTotal uint64
	s.telemetryMu.Lock()
	for _, v := range s.telemetryStats {
		telemetryTotal += v
	}
	s.telemetryMu.Unlock()

	s.ackMu.Lock()
	accepted := s.ackStats["accepted"]
	retrying := s.ackStats["retrying"]
	failed := s.ackStats["failed"]
	expired := s.ackStats["expired"]
	delivered := s.ackStats["delivered"]
	s.ackMu.Unlock()

	s.shutdownMu.Lock()
	drainLast := s.shutdownDrainLastSecs
	drainTimeouts := s.shutdownDrainTimeouts
	s.shutdownMu.Unlock()

	return dashboardSummary{
		ActiveConnections:       s.active.Load(),
		KnownSessions:           stats.SessionCount,
		OnlineSessions:          stats.OnlineSessionCount,
		QueueDepthSum:           stats.QueueDepthSum,
		QueueDepthMax:           stats.QueueDepthMax,
		InFlightSum:             stats.InFlightCountSum,
		InFlightMax:             stats.InFlightCountMax,
		IPViolations:            s.ipViolationTotal.Load(),
		IPBlockedRejects:        s.ipBlockedRejectTotal.Load(),
		IPPerIPRejects:          s.ipPerIPRejectTotal.Load(),
		ShutdownDrainLastSecs:   drainLast,
		ShutdownDrainTimeouts:   drainTimeouts,
		AckAccepted:             accepted,
		AckRetrying:             retrying,
		AckFailed:               failed,
		AckExpired:              expired,
		AckDelivered:            delivered,
		TelemetryFramesObserved: telemetryTotal,
	}
}

func parseDebugLimit(r *http.Request, fallback, max int) int {
	limit := fallback
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			limit = v
		}
	}
	if max > 0 && limit > max {
		limit = max
	}
	return limit
}

func (s *Server) debugDashboardHandler(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.DebugLogs {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"summary":     s.dashboardSummary(),
		"connections": s.sessions.SessionStats(parseDebugLimit(r, 50, 200)),
		"events":      s.debugEventsSnapshot(parseDebugLimit(r, 60, 200)),
	})
}

func (s *Server) debugConnectionsHandler(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.DebugLogs {
		http.NotFound(w, r)
		return
	}

	items := s.sessions.SessionStats(parseDebugLimit(r, 100, 500))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"count": len(items),
		"items": items,
	})
}

func (s *Server) debugEventsHandler(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.DebugLogs {
		http.NotFound(w, r)
		return
	}

	items := s.debugEventsSnapshot(parseDebugLimit(r, 100, 250))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"count": len(items),
		"items": items,
	})
}

func (s *Server) debugLogsHandler(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.DebugLogs {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>TCPadapter Logs</title></head>
<body style="font-family: sans-serif; max-width: 900px; margin: 40px auto; line-height: 1.5">
<h1>Adapter Logs</h1>
<p>Логи адаптера читаются на сервере, а не через HTTP endpoint приложения.</p>
<pre style="background:#f6f6f6; padding:16px; border-radius:12px">ssh root@93.189.229.197
cd /opt/tcpadapter
docker compose logs -f --tail=200 adapter</pre>
<p>Точечный поиск:</p>
<pre style="background:#f6f6f6; padding:16px; border-radius:12px">docker logs tcpadapter 2&gt;&amp;1 | grep 'registration timeout'</pre>
<p><a href="/">Back to dashboard</a></p>
</body></html>`))
}

func (s *Server) debugQueuesHandler(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.DebugLogs {
		http.NotFound(w, r)
		return
	}

	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			if v > 200 {
				v = 200
			}
			limit = v
		}
	}

	stats := s.sessions.TopQueueStats(limit)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"count": len(stats),
		"items": stats,
	})
}

type debugEnqueueRequest struct {
	ControllerID string `json:"controller_id"`
	CommandID    uint8  `json:"command_id"`
	TTLSeconds   int    `json:"ttl_seconds"`
	PayloadHex   string `json:"payload_hex"`
	MessageID    string `json:"message_id"`
	TraceID      string `json:"trace_id"`
	DedupKey     string `json:"dedup_key"`
}

func (s *Server) debugEnqueueHandler(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.DebugLogs {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req debugEnqueueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	req.ControllerID = strings.TrimSpace(req.ControllerID)
	if req.ControllerID == "" {
		http.Error(w, "controller_id is required", http.StatusBadRequest)
		return
	}
	if req.CommandID == 0 {
		http.Error(w, "command_id is required", http.StatusBadRequest)
		return
	}

	payload := []byte(nil)
	if strings.TrimSpace(req.PayloadHex) != "" {
		decoded, err := hex.DecodeString(strings.TrimSpace(req.PayloadHex))
		if err != nil {
			http.Error(w, "payload_hex must be valid hex string", http.StatusBadRequest)
			return
		}
		payload = decoded
	}
	if err := protocol.ValidateServerCommandPayload(req.CommandID, payload); err != nil {
		http.Error(w, "invalid payload for command: "+err.Error(), http.StatusBadRequest)
		return
	}

	ttl := time.Duration(req.TTLSeconds) * time.Second
	if req.TTLSeconds < 0 {
		http.Error(w, "ttl_seconds must be >= 0", http.StatusBadRequest)
		return
	}
	messageID := strings.TrimSpace(req.MessageID)
	if messageID == "" {
		messageID = fmt.Sprintf("debug-%d", time.Now().UnixNano())
	}
	traceID := strings.TrimSpace(req.TraceID)
	if traceID == "" {
		traceID = strings.TrimSpace(r.Header.Get("X-Request-ID"))
	}
	if traceID == "" {
		traceID = fmt.Sprintf("trace-%d", time.Now().UnixNano())
	}

	cmd := queue.Command{
		MessageID: messageID,
		TraceID:   traceID,
		DedupKey:  strings.TrimSpace(req.DedupKey),
		CommandID: req.CommandID,
		TTL:       ttl,
		Payload:   payload,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.EnqueueCommand(req.ControllerID, cmd); err != nil {
		http.Error(w, "enqueue failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":        "queued",
		"controller_id": req.ControllerID,
		"command_id":    req.CommandID,
		"message_id":    messageID,
		"trace_id":      traceID,
	})
}

func renderDashboardHTML() string {
	return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>TCPadapter Dashboard</title>
  <style>
    :root {
      --bg: #f4f1ea;
      --panel: #fffaf2;
      --ink: #1f1d1a;
      --muted: #6f6559;
      --line: #d8cebf;
      --accent: #b6542a;
      --accent-soft: #f3d5c6;
      --good: #2f7d4b;
      --warn: #a66b00;
      --bad: #a63c2f;
      --rx: #0d6b8a;
      --tx: #8a4f0d;
      --ack: #5c3d99;
      --shadow: 0 12px 30px rgba(54, 38, 16, 0.08);
      --radius: 18px;
      font-family: "IBM Plex Sans", "Segoe UI", sans-serif;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      color: var(--ink);
      background:
        radial-gradient(circle at top left, #fff8ef 0, #f4f1ea 38%),
        linear-gradient(180deg, #efe5d8 0, #f4f1ea 20%);
    }
    a { color: var(--accent); }
    .wrap {
      max-width: 1440px;
      margin: 0 auto;
      padding: 28px 20px 40px;
    }
    .hero {
      display: grid;
      grid-template-columns: 1.3fr 0.7fr;
      gap: 18px;
      margin-bottom: 18px;
    }
    .panel {
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: var(--radius);
      box-shadow: var(--shadow);
    }
    .hero-main {
      padding: 24px;
      background:
        linear-gradient(135deg, rgba(182, 84, 42, 0.10), rgba(13, 107, 138, 0.08)),
        var(--panel);
    }
    .hero-side {
      padding: 20px;
    }
    h1, h2, h3 { margin: 0; }
    h1 {
      font-size: 34px;
      line-height: 1.1;
      margin-bottom: 10px;
    }
    .sub {
      color: var(--muted);
      max-width: 70ch;
      margin-bottom: 18px;
    }
    .badges {
      display: flex;
      flex-wrap: wrap;
      gap: 10px;
    }
    .badge {
      border: 1px solid var(--line);
      border-radius: 999px;
      padding: 8px 12px;
      background: rgba(255,255,255,0.75);
      font-size: 13px;
    }
    .grid {
      display: grid;
      grid-template-columns: repeat(4, minmax(0, 1fr));
      gap: 14px;
      margin-bottom: 18px;
    }
    .card {
      padding: 16px;
      min-height: 120px;
    }
    .card .label {
      color: var(--muted);
      font-size: 13px;
      margin-bottom: 8px;
      text-transform: uppercase;
      letter-spacing: 0.05em;
    }
    .card .value {
      font-size: 32px;
      font-weight: 700;
      margin-bottom: 10px;
    }
    .card .meta {
      color: var(--muted);
      font-size: 13px;
      line-height: 1.45;
    }
    .layout {
      display: grid;
      grid-template-columns: 1.1fr 0.9fr;
      gap: 18px;
    }
    .section-head {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 10px;
      padding: 18px 18px 0;
    }
    .section-body { padding: 18px; }
    table {
      width: 100%;
      border-collapse: collapse;
      font-size: 14px;
    }
    th, td {
      text-align: left;
      padding: 10px 8px;
      border-bottom: 1px solid var(--line);
      vertical-align: top;
    }
    th {
      color: var(--muted);
      font-size: 12px;
      text-transform: uppercase;
      letter-spacing: 0.05em;
    }
    tr.session-row { cursor: pointer; }
    tr.session-row:hover { background: rgba(182, 84, 42, 0.06); }
    .status-dot {
      display: inline-block;
      width: 9px;
      height: 9px;
      border-radius: 50%;
      margin-right: 8px;
      background: var(--muted);
    }
    .online .status-dot { background: var(--good); }
    .offline .status-dot { background: var(--bad); }
    .stack {
      display: grid;
      gap: 14px;
    }
    .detail-grid {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 10px;
    }
    .detail {
      padding: 12px;
      border-radius: 14px;
      background: #fff;
      border: 1px solid var(--line);
    }
    .detail .k {
      color: var(--muted);
      font-size: 12px;
      text-transform: uppercase;
      letter-spacing: 0.05em;
      margin-bottom: 6px;
    }
    .detail .v {
      font-size: 15px;
      word-break: break-word;
    }
    .waterfall {
      display: grid;
      gap: 8px;
      max-height: 760px;
      overflow: auto;
      padding-right: 4px;
    }
    .toolbar {
      display: grid;
      grid-template-columns: 180px minmax(0, 1fr) 140px;
      gap: 10px;
      margin-bottom: 12px;
      align-items: center;
    }
    .toolbar .status {
      text-align: right;
      color: var(--muted);
      font-size: 13px;
    }
    .event {
      display: grid;
      grid-template-columns: 72px 108px 1fr;
      gap: 12px;
      align-items: start;
      padding: 12px;
      border-radius: 14px;
      background: #fff;
      border: 1px solid var(--line);
      border-left-width: 5px;
    }
    .event.rx { border-left-color: var(--rx); }
    .event.tx { border-left-color: var(--tx); }
    .event.ack { border-left-color: var(--ack); }
    .event-kind {
      font-weight: 700;
      text-transform: uppercase;
      font-size: 12px;
      letter-spacing: 0.08em;
    }
    .event-time {
      font-size: 12px;
      color: var(--muted);
      white-space: nowrap;
    }
    .event-main {
      display: grid;
      gap: 4px;
      min-width: 0;
    }
    .event-title {
      font-weight: 600;
      overflow-wrap: anywhere;
    }
    .event-meta {
      color: var(--muted);
      font-size: 13px;
      overflow-wrap: anywhere;
    }
    .enqueue-form {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 10px;
    }
    .enqueue-form .full { grid-column: 1 / -1; }
    input, textarea, button, select {
      width: 100%;
      font: inherit;
      border-radius: 12px;
      border: 1px solid var(--line);
      padding: 11px 12px;
      background: #fff;
      color: var(--ink);
    }
    textarea { min-height: 90px; resize: vertical; }
    button {
      background: var(--accent);
      color: #fff;
      border: none;
      cursor: pointer;
      font-weight: 600;
    }
    .hint, .muted {
      color: var(--muted);
      font-size: 13px;
    }
    .mono { font-family: "IBM Plex Mono", "SFMono-Regular", monospace; }
    @media (max-width: 1100px) {
      .hero, .layout, .grid { grid-template-columns: 1fr; }
    }
    @media (max-width: 720px) {
      .enqueue-form, .detail-grid { grid-template-columns: 1fr; }
      .toolbar { grid-template-columns: 1fr; }
      .toolbar .status { text-align: left; }
      .event { grid-template-columns: 1fr; }
    }
  </style>
</head>
<body>
  <div class="wrap">
    <div class="hero">
      <section class="panel hero-main">
        <h1>TCPadapter Control Room</h1>
        <div class="sub">Живая debug-панель адаптера: summary по очередям и подключениям, список контроллеров и водопад последних входящих, исходящих и ACK-событий.</div>
        <div class="badges">
          <span class="badge"><a href="/healthz">/healthz</a></span>
          <span class="badge"><a href="/readyz">/readyz</a></span>
          <span class="badge"><a href="/metrics">/metrics</a></span>
          <span class="badge"><a href="/debug/queues?limit=20">/debug/queues</a></span>
          <span class="badge"><a href="/debug/connections?limit=100">/debug/connections</a></span>
          <span class="badge"><a href="/debug/events?limit=100">/debug/events</a></span>
          <span class="badge"><a href="/debug/logs">/debug/logs</a></span>
          <span class="badge"><a href="/debug/command-builder">/debug/command-builder</a></span>
        </div>
      </section>
      <section class="panel hero-side">
        <h3 style="margin-bottom:12px">Quick Enqueue</h3>
        <form id="enqueue-form" class="enqueue-form">
          <input name="controller_id" placeholder="controller_id / IMEI" required>
          <select name="command_id" required>
            <option value="">Select command</option>
            <option value="2">2 - Reboot</option>
            <option value="3">3 - Factory Reset</option>
            <option value="5">5 - Set Triggers</option>
            <option value="6">6 - Capture ID</option>
            <option value="7">7 - Request Photo</option>
            <option value="8">8 - Control Outputs</option>
            <option value="9" selected>9 - Request Events</option>
            <option value="10">10 - Request SMS IDs</option>
            <option value="11">11 - Send SMS</option>
            <option value="12">12 - Sync Settings</option>
            <option value="13">13 - Sync IDs</option>
            <option value="14">14 - Sync Schedules</option>
            <option value="15">15 - Sync Groups</option>
            <option value="16">16 - Sync Holidays</option>
            <option value="17">17 - CRC All</option>
            <option value="18">18 - CRC Block</option>
            <option value="19">19 - FW Start</option>
            <option value="20">20 - FW Block</option>
            <option value="21">21 - Photo Upload Request</option>
            <option value="22">22 - Status2 Request</option>
            <option value="23">23 - Set Time</option>
            <option value="25">25 - Binding Response</option>
            <option value="27">27 - Custom</option>
          </select>
          <input name="ttl_seconds" type="number" min="0" value="10" placeholder="ttl_seconds">
          <input name="message_id" placeholder="message_id (optional)">
          <input class="full" name="trace_id" placeholder="trace_id (optional)">
          <textarea class="full mono" name="payload_hex" placeholder="payload_hex without spaces (optional)"></textarea>
          <button class="full" type="submit">Queue Command</button>
        </form>
        <div id="enqueue-result" class="hint" style="margin-top:10px">POST /debug/enqueue</div>
      </section>
    </div>

    <section id="summary-grid" class="grid"></section>

    <div class="layout">
      <section class="panel">
        <div class="section-head">
          <h2>Controllers</h2>
          <div class="muted">Click a row for details</div>
        </div>
        <div class="section-body">
          <table>
            <thead>
              <tr>
                <th>Controller</th>
                <th>State</th>
                <th>Queue</th>
                <th>In-flight</th>
                <th>Buffer</th>
                <th>Last Seen</th>
              </tr>
            </thead>
            <tbody id="connections-body"></tbody>
          </table>
        </div>
      </section>

      <section class="stack">
        <section class="panel">
          <div class="section-head">
            <h2>Selected Controller</h2>
            <div id="selected-title" class="muted">No controller selected</div>
          </div>
          <div class="section-body">
            <div id="selected-details" class="detail-grid"></div>
          </div>
        </section>

        <section class="panel">
          <div class="section-head">
            <h2>Recent Waterfall</h2>
            <div class="muted">RX / TX / ACK</div>
          </div>
          <div class="section-body">
            <div class="toolbar">
              <select id="event-kind-filter">
                <option value="all">All Events</option>
                <option value="tx">Only TX</option>
                <option value="rx">Only RX</option>
                <option value="ack">Only ACK</option>
              </select>
              <input id="event-imei-filter" placeholder="Filter by IMEI / controller_id">
              <button id="pause-refresh" type="button">Pause Live</button>
              <div id="waterfall-status" class="status">Live updates enabled</div>
            </div>
            <div id="events-body" class="waterfall"></div>
          </div>
        </section>
      </section>
    </div>
  </div>

  <script>
    const state = {
      selectedControllerID: null,
      eventKindFilter: 'all',
      eventIMEIFilter: '',
      paused: false,
      events: [],
      refreshTimer: null
    };

    function fmtTime(value) {
      if (!value) return "n/a";
      const d = new Date(value);
      if (Number.isNaN(d.getTime())) return value;
      return d.toLocaleString();
    }

    function text(v) {
      return v === null || v === undefined || v === "" ? "n/a" : String(v);
    }

    function metricCards(summary) {
      return [
        ["Active TCP", summary.active_connections, "online sessions: " + summary.online_sessions + " / known: " + summary.known_sessions],
        ["Queues", summary.queue_depth_sum, "max queue depth: " + summary.queue_depth_max],
        ["In-flight", summary.inflight_sum, "max per controller: " + summary.inflight_max],
        ["Telemetry", summary.telemetry_frames_observed, "frames observed by adapter"],
        ["ACK Delivered", summary.ack_delivered, "accepted: " + summary.ack_accepted + ", retrying: " + summary.ack_retrying],
        ["ACK Failures", summary.ack_failed, "expired: " + summary.ack_expired],
        ["IP Protection", summary.ip_violations, "blocked rejects: " + summary.ip_blocked_rejects + ", per-ip rejects: " + summary.ip_per_ip_rejects],
        ["Shutdown", summary.shutdown_drain_last_seconds, "drain timeouts: " + summary.shutdown_drain_timeouts]
      ];
    }

    function renderSummary(summary) {
      const el = document.getElementById("summary-grid");
      el.innerHTML = metricCards(summary).map(([label, value, meta]) => (
        '<section class="panel card">' +
          '<div class="label">' + label + '</div>' +
          '<div class="value">' + text(value) + '</div>' +
          '<div class="meta">' + meta + '</div>' +
        '</section>'
      )).join("");
    }

    function renderConnections(items) {
      const body = document.getElementById("connections-body");
      body.innerHTML = items.map((item) => {
        const mode = [item.fw_mode ? "fw" : "", item.sync_mode ? "sync" : ""].filter(Boolean).join(", ") || "normal";
        const rowClass = item.online ? "session-row online" : "session-row offline";
        return '<tr class="' + rowClass + '" data-controller="' + item.controller_id + '">' +
          '<td><span class="status-dot"></span><span class="mono">' + item.controller_id + '</span><br><span class="muted">' + text(item.remote_addr) + '</span></td>' +
          '<td>' + (item.online ? "online" : "offline") + '<br><span class="muted">' + mode + '</span></td>' +
          '<td>' + item.queue_depth + '<br><span class="muted">' + item.queue_bytes + ' bytes</span></td>' +
          '<td>' + item.in_flight + '<br><span class="muted">next seq: ' + item.next_seq + '</span></td>' +
          '<td>' + item.buffer_free + '</td>' +
          '<td>' + fmtTime(item.last_seen) + '</td>' +
        '</tr>';
      }).join("");
      for (const row of body.querySelectorAll("tr.session-row")) {
        row.addEventListener("click", () => {
          state.selectedControllerID = row.dataset.controller;
          renderSelected(items);
          renderEvents(window.__dashboardEvents || []);
        });
      }
      if (!state.selectedControllerID && items.length > 0) {
        state.selectedControllerID = items[0].controller_id;
      }
      renderSelected(items);
    }

    function renderSelected(items) {
      const selected = items.find((item) => item.controller_id === state.selectedControllerID);
      const title = document.getElementById("selected-title");
      const body = document.getElementById("selected-details");
      if (!selected) {
        title.textContent = "No controller selected";
        body.innerHTML = '<div class="muted">Select a controller from the table.</div>';
        return;
      }
      title.textContent = selected.controller_id;
      const entries = [
        ["Controller", selected.controller_id],
        ["Remote", selected.remote_addr || "n/a"],
        ["Online", selected.online ? "yes" : "no"],
        ["Queue depth", selected.queue_depth],
        ["Queue bytes", selected.queue_bytes],
        ["In-flight", selected.in_flight],
        ["Buffer free", selected.buffer_free],
        ["FW mode", selected.fw_mode ? "enabled" : "off"],
        ["Sync mode", selected.sync_mode ? "enabled" : "off"],
        ["Next seq", selected.next_seq],
        ["Last seen", fmtTime(selected.last_seen)]
      ];
      body.innerHTML = entries.map(([k, v]) => (
        '<div class="detail"><div class="k">' + k + '</div><div class="v mono">' + text(v) + '</div></div>'
      )).join("");
    }

    function renderEvents(items) {
      state.events = items || [];
      window.__dashboardEvents = state.events;
      const selectedID = state.selectedControllerID;
      const imeiFilter = state.eventIMEIFilter.trim().toLowerCase();
      let filtered = state.events.filter((item) => {
        if (state.eventKindFilter !== 'all' && item.kind !== state.eventKindFilter) return false;
        if (imeiFilter && !(item.controller_id || '').toLowerCase().includes(imeiFilter)) return false;
        return true;
      });
      if (selectedID && !imeiFilter) {
        filtered = filtered
          .filter((item) => item.controller_id === selectedID)
          .concat(filtered.filter((item) => item.controller_id !== selectedID));
      }
      filtered = filtered.slice(0, 120);
      const body = document.getElementById("events-body");
      body.innerHTML = filtered.map((item) => {
        const title = (item.controller_id || "unknown") + ' · cmd ' + item.command_id + ' · seq ' + item.command_seq;
        const meta = [
          item.message_id ? 'msg=' + item.message_id : '',
          item.trace_id ? 'trace=' + item.trace_id : '',
          item.status ? 'status=' + item.status : '',
          item.reason ? item.reason : '',
          item.payload_len ? 'payload=' + item.payload_len + 'B' : '',
          item.remote_addr ? item.remote_addr : ''
        ].filter(Boolean).join(' · ');
        return '<div class="event ' + item.kind + '">' +
          '<div><div class="event-kind">' + item.kind + '</div><div class="event-time">' + fmtTime(item.timestamp) + '</div></div>' +
          '<div class="event-main" style="grid-column: span 2">' +
            '<div class="event-title mono">' + title + '</div>' +
            '<div class="event-meta">' + meta + '</div>' +
          '</div>' +
        '</div>';
      }).join("");
      document.getElementById('waterfall-status').textContent =
        (state.paused ? 'Paused' : 'Live updates enabled') + ' · showing ' + filtered.length + ' events';
    }

    async function refresh() {
      if (state.paused) return;
      const res = await fetch('/debug/dashboard?limit=120', { cache: 'no-store' });
      if (!res.ok) throw new Error('dashboard fetch failed: ' + res.status);
      const data = await res.json();
      renderSummary(data.summary);
      renderConnections(data.connections || []);
      renderEvents(data.events || []);
    }

    async function submitEnqueue(ev) {
      ev.preventDefault();
      const form = ev.currentTarget;
      const data = Object.fromEntries(new FormData(form).entries());
      data.command_id = Number(data.command_id || 0);
      data.ttl_seconds = Number(data.ttl_seconds || 0);
      if (!data.message_id) delete data.message_id;
      if (!data.trace_id) delete data.trace_id;
      if (!data.payload_hex) delete data.payload_hex;
      const result = document.getElementById('enqueue-result');
      result.textContent = 'Queueing...';
      const res = await fetch('/debug/enqueue', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(data)
      });
      const body = await res.text();
      result.textContent = body;
      await refresh();
    }

    document.getElementById('enqueue-form').addEventListener('submit', (ev) => {
      submitEnqueue(ev).catch((err) => {
        document.getElementById('enqueue-result').textContent = err.message;
      });
    });

    document.getElementById('event-kind-filter').addEventListener('change', (ev) => {
      state.eventKindFilter = ev.currentTarget.value;
      renderEvents(state.events);
    });

    document.getElementById('event-imei-filter').addEventListener('input', (ev) => {
      state.eventIMEIFilter = ev.currentTarget.value;
      renderEvents(state.events);
    });

    document.getElementById('pause-refresh').addEventListener('click', (ev) => {
      state.paused = !state.paused;
      ev.currentTarget.textContent = state.paused ? 'Resume Live' : 'Pause Live';
      renderEvents(state.events);
      if (!state.paused) {
        refresh().catch(() => {});
      }
    });

    refresh().catch((err) => {
      document.getElementById('enqueue-result').textContent = err.message;
    });
    state.refreshTimer = setInterval(() => refresh().catch(() => {}), 3000);
  </script>
</body>
</html>`
}
