package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"tcpadapter/internal/config"
	"tcpadapter/internal/kafka"
	"tcpadapter/internal/server"
	"tcpadapter/internal/session"
	"tcpadapter/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var sessionStore store.Store = store.NewInMemoryStore()
	sessions := session.NewManager(cfg.MaxConnections, cfg.MaxQueueDepth, cfg.MaxQueueBytes, cfg.QueueOverflow, sessionStore)
	bus := kafka.NewLoggingBus(logger)

	if cfg.StateFile != "" {
		sessionStore = store.NewFileStore(cfg.StateFile)
		sessions = session.NewManager(cfg.MaxConnections, cfg.MaxQueueDepth, cfg.MaxQueueBytes, cfg.QueueOverflow, sessionStore)
	}
	restored, err := sessions.Restore()
	if err != nil {
		logger.Error("failed to restore sessions", "error", err)
		os.Exit(1)
	}
	logger.Info("session state restored", "count", restored, "state_file", cfg.StateFile)

	tcpServer := server.New(cfg, logger, sessions, bus)
	if err := tcpServer.Run(ctx); err != nil {
		logger.Error("server exited with error", "error", err)
		os.Exit(1)
	}

	logger.Info("server stopped")
}
