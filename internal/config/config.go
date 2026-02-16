package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	ListenAddr         string
	MetricsAddr        string
	RegistrationWindow time.Duration
	HeartbeatTimeout   time.Duration
	MaxConnections     int
	MaxQueueDepth      int
	MaxQueueBytes      int
	QueueOverflow      string
	AckTimeout         time.Duration
	RetryBackoff       time.Duration
	MaxRetries         int
	SweepInterval      time.Duration
	WriteTimeout       time.Duration
	ReadTimeout        time.Duration
	DebugLogs          bool
	StateFile          string
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddr:         envOrDefault("TCPADAPTER_LISTEN_ADDR", ":15010"),
		MetricsAddr:        envOrDefault("TCPADAPTER_METRICS_ADDR", ":18080"),
		RegistrationWindow: envDurationOrDefault("TCPADAPTER_REG_WINDOW", 5*time.Second),
		HeartbeatTimeout:   envDurationOrDefault("TCPADAPTER_HEARTBEAT_TIMEOUT", 30*time.Second),
		MaxConnections:     envIntOrDefault("TCPADAPTER_MAX_CONNECTIONS", 10000),
		MaxQueueDepth:      envIntOrDefault("TCPADAPTER_QUEUE_MAX_DEPTH", 1000),
		MaxQueueBytes:      envIntOrDefault("TCPADAPTER_QUEUE_MAX_BYTES", 1048576),
		QueueOverflow:      envOrDefault("TCPADAPTER_QUEUE_OVERFLOW", "drop_oldest"),
		AckTimeout:         envDurationOrDefault("TCPADAPTER_ACK_TIMEOUT", 10*time.Second),
		RetryBackoff:       envDurationOrDefault("TCPADAPTER_RETRY_BACKOFF", 3*time.Second),
		MaxRetries:         envIntOrDefault("TCPADAPTER_MAX_RETRIES", 3),
		SweepInterval:      envDurationOrDefault("TCPADAPTER_SWEEP_INTERVAL", 2*time.Second),
		WriteTimeout:       envDurationOrDefault("TCPADAPTER_WRITE_TIMEOUT", 10*time.Second),
		ReadTimeout:        envDurationOrDefault("TCPADAPTER_READ_TIMEOUT", 35*time.Second),
		DebugLogs:          envBoolOrDefault("TCPADAPTER_DEBUG", false),
		StateFile:          envOrDefault("TCPADAPTER_STATE_FILE", ".data/sessions.json"),
	}

	if cfg.MaxConnections <= 0 {
		return Config{}, fmt.Errorf("TCPADAPTER_MAX_CONNECTIONS must be > 0")
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
	if cfg.SweepInterval <= 0 {
		return Config{}, fmt.Errorf("TCPADAPTER_SWEEP_INTERVAL must be > 0")
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
