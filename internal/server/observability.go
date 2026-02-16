package server

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"tcpadapter/internal/kafka"
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
