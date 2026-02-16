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

func (s *Server) startObservability(ctx context.Context) {
	if s.cfg.MetricsAddr == "" {
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/metrics", s.metricsHandler)
	mux.HandleFunc("/debug/queues", s.debugQueuesHandler)
	mux.HandleFunc("/debug/enqueue", s.debugEnqueueHandler)

	httpSrv := &http.Server{Addr: s.cfg.MetricsAddr, Handler: mux}
	go func() {
		<-ctx.Done()
		_ = httpSrv.Shutdown(context.Background())
	}()
	go func() {
		s.logger.Info("observability server started", "addr", s.cfg.MetricsAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("observability server failed", "error", err)
		}
	}()
}

func (s *Server) publishAck(ctx context.Context, event kafka.AckEvent) {
	s.ackMu.Lock()
	s.ackStats[event.Status]++
	s.ackMu.Unlock()
	s.bus.PublishAck(ctx, event)
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
	s.ackMu.Unlock()

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

	cmd := queue.Command{
		MessageID: messageID,
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
	})
}
