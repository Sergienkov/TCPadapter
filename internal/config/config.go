package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	ListenAddr          string
	MetricsAddr         string
	RegistrationWindow  time.Duration
	HeartbeatTimeout    time.Duration
	MaxConnections      int
	MaxConnectionsPerIP int
	IPViolationWindow   time.Duration
	IPViolationLimit    int
	IPBlockDuration     time.Duration
	MaxQueueDepth       int
	MaxQueueBytes       int
	QueueOverflow       string
	AckTimeout          time.Duration
	RetryBackoff        time.Duration
	MaxRetries          int
	AckTimeoutQuery     time.Duration
	RetryBackoffQuery   time.Duration
	MaxRetriesQuery     int
	AckTimeoutSync      time.Duration
	RetryBackoffSync    time.Duration
	MaxRetriesSync      int
	AckTimeoutFW        time.Duration
	RetryBackoffFW      time.Duration
	MaxRetriesFW        int
	SweepInterval       time.Duration
	WriteTimeout        time.Duration
	ReadTimeout         time.Duration
	DebugLogs           bool
	StoreBackend        string
	StateFile           string
	PostgresDSN         string
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddr:          envOrDefault("TCPADAPTER_LISTEN_ADDR", ":15010"),
		MetricsAddr:         envOrDefault("TCPADAPTER_METRICS_ADDR", ":18080"),
		RegistrationWindow:  envDurationOrDefault("TCPADAPTER_REG_WINDOW", 5*time.Second),
		HeartbeatTimeout:    envDurationOrDefault("TCPADAPTER_HEARTBEAT_TIMEOUT", 30*time.Second),
		MaxConnections:      envIntOrDefault("TCPADAPTER_MAX_CONNECTIONS", 10000),
		MaxConnectionsPerIP: envIntOrDefault("TCPADAPTER_MAX_CONNECTIONS_PER_IP", 0),
		IPViolationWindow:   envDurationOrDefault("TCPADAPTER_IP_VIOLATION_WINDOW", 30*time.Second),
		IPViolationLimit:    envIntOrDefault("TCPADAPTER_IP_VIOLATION_LIMIT", 10),
		IPBlockDuration:     envDurationOrDefault("TCPADAPTER_IP_BLOCK_DURATION", 1*time.Minute),
		MaxQueueDepth:       envIntOrDefault("TCPADAPTER_QUEUE_MAX_DEPTH", 1000),
		MaxQueueBytes:       envIntOrDefault("TCPADAPTER_QUEUE_MAX_BYTES", 1048576),
		QueueOverflow:       envOrDefault("TCPADAPTER_QUEUE_OVERFLOW", "drop_oldest"),
		AckTimeout:          envDurationOrDefault("TCPADAPTER_ACK_TIMEOUT", 10*time.Second),
		RetryBackoff:        envDurationOrDefault("TCPADAPTER_RETRY_BACKOFF", 3*time.Second),
		MaxRetries:          envIntOrDefault("TCPADAPTER_MAX_RETRIES", 3),
		AckTimeoutQuery:     envDurationOrDefault("TCPADAPTER_ACK_TIMEOUT_QUERY", 2*time.Second),
		RetryBackoffQuery:   envDurationOrDefault("TCPADAPTER_RETRY_BACKOFF_QUERY", 1*time.Second),
		MaxRetriesQuery:     envIntOrDefault("TCPADAPTER_MAX_RETRIES_QUERY", 2),
		AckTimeoutSync:      envDurationOrDefault("TCPADAPTER_ACK_TIMEOUT_SYNC", 15*time.Second),
		RetryBackoffSync:    envDurationOrDefault("TCPADAPTER_RETRY_BACKOFF_SYNC", 5*time.Second),
		MaxRetriesSync:      envIntOrDefault("TCPADAPTER_MAX_RETRIES_SYNC", 1),
		AckTimeoutFW:        envDurationOrDefault("TCPADAPTER_ACK_TIMEOUT_FW", 20*time.Second),
		RetryBackoffFW:      envDurationOrDefault("TCPADAPTER_RETRY_BACKOFF_FW", 3*time.Second),
		MaxRetriesFW:        envIntOrDefault("TCPADAPTER_MAX_RETRIES_FW", 3),
		SweepInterval:       envDurationOrDefault("TCPADAPTER_SWEEP_INTERVAL", 2*time.Second),
		WriteTimeout:        envDurationOrDefault("TCPADAPTER_WRITE_TIMEOUT", 10*time.Second),
		ReadTimeout:         envDurationOrDefault("TCPADAPTER_READ_TIMEOUT", 35*time.Second),
		DebugLogs:           envBoolOrDefault("TCPADAPTER_DEBUG", false),
		StoreBackend:        envOrDefault("TCPADAPTER_STORE_BACKEND", "file"),
		StateFile:           envOrDefault("TCPADAPTER_STATE_FILE", ".data/sessions.json"),
		PostgresDSN:         envOrDefault("TCPADAPTER_POSTGRES_DSN", ""),
	}

	if cfg.MaxConnections <= 0 {
		return Config{}, fmt.Errorf("TCPADAPTER_MAX_CONNECTIONS must be > 0")
	}
	if cfg.MaxConnectionsPerIP < 0 {
		return Config{}, fmt.Errorf("TCPADAPTER_MAX_CONNECTIONS_PER_IP must be >= 0")
	}
	if cfg.IPViolationLimit < 0 {
		return Config{}, fmt.Errorf("TCPADAPTER_IP_VIOLATION_LIMIT must be >= 0")
	}
	if cfg.IPViolationLimit > 0 && cfg.IPViolationWindow <= 0 {
		return Config{}, fmt.Errorf("TCPADAPTER_IP_VIOLATION_WINDOW must be > 0 when TCPADAPTER_IP_VIOLATION_LIMIT > 0")
	}
	if cfg.IPViolationLimit > 0 && cfg.IPBlockDuration <= 0 {
		return Config{}, fmt.Errorf("TCPADAPTER_IP_BLOCK_DURATION must be > 0 when TCPADAPTER_IP_VIOLATION_LIMIT > 0")
	}
	if cfg.RegistrationWindow <= 0 || cfg.HeartbeatTimeout <= 0 {
		return Config{}, fmt.Errorf("timeouts must be > 0")
	}
	if cfg.MaxQueueDepth <= 0 {
		return Config{}, fmt.Errorf("TCPADAPTER_QUEUE_MAX_DEPTH must be > 0")
	}
	if cfg.MaxQueueBytes <= 0 {
		return Config{}, fmt.Errorf("TCPADAPTER_QUEUE_MAX_BYTES must be > 0")
	}
	if cfg.QueueOverflow != "drop_oldest" && cfg.QueueOverflow != "reject" {
		return Config{}, fmt.Errorf("TCPADAPTER_QUEUE_OVERFLOW must be one of: drop_oldest, reject")
	}
	if cfg.AckTimeout <= 0 || cfg.RetryBackoff < 0 {
		return Config{}, fmt.Errorf("TCPADAPTER_ACK_TIMEOUT must be > 0 and TCPADAPTER_RETRY_BACKOFF must be >= 0")
	}
	if cfg.MaxRetries < 0 {
		return Config{}, fmt.Errorf("TCPADAPTER_MAX_RETRIES must be >= 0")
	}
	if cfg.AckTimeoutQuery <= 0 || cfg.RetryBackoffQuery < 0 || cfg.MaxRetriesQuery < 0 {
		return Config{}, fmt.Errorf("invalid query retry policy config")
	}
	if cfg.AckTimeoutSync <= 0 || cfg.RetryBackoffSync < 0 || cfg.MaxRetriesSync < 0 {
		return Config{}, fmt.Errorf("invalid sync retry policy config")
	}
	if cfg.AckTimeoutFW <= 0 || cfg.RetryBackoffFW < 0 || cfg.MaxRetriesFW < 0 {
		return Config{}, fmt.Errorf("invalid fw retry policy config")
	}
	if cfg.SweepInterval <= 0 {
		return Config{}, fmt.Errorf("TCPADAPTER_SWEEP_INTERVAL must be > 0")
	}
	if cfg.StoreBackend != "memory" && cfg.StoreBackend != "file" && cfg.StoreBackend != "postgres" {
		return Config{}, fmt.Errorf("TCPADAPTER_STORE_BACKEND must be one of: memory, file, postgres")
	}
	if cfg.StoreBackend == "file" && cfg.StateFile == "" {
		return Config{}, fmt.Errorf("TCPADAPTER_STATE_FILE is required when TCPADAPTER_STORE_BACKEND=file")
	}
	if cfg.StoreBackend == "postgres" && cfg.PostgresDSN == "" {
		return Config{}, fmt.Errorf("TCPADAPTER_POSTGRES_DSN is required when TCPADAPTER_STORE_BACKEND=postgres")
	}
	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOrDefault(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envDurationOrDefault(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func envBoolOrDefault(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return parsed
}
