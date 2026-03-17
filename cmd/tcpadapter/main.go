package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"tcpadapter/internal/buildinfo"
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
	logger.Info("build info", "version", buildinfo.Version, "commit", buildinfo.Commit, "build_time", buildinfo.BuildTime)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var (
		sessionStore store.Store
		storeLabel   string
	)
	bus := kafka.NewLoggingBus(logger)

	switch cfg.StoreBackend {
	case "memory":
		sessionStore = store.NewInMemoryStore()
		storeLabel = "memory"
	case "file":
		sessionStore = store.NewFileStore(cfg.StateFile)
		storeLabel = cfg.StateFile
	case "postgres":
		pgStore, err := store.NewPostgresStore(cfg.PostgresDSN)
		if err != nil {
			logger.Error("failed to initialize postgres store", "error", err)
			os.Exit(1)
		}
		defer func() {
			if err := pgStore.Close(); err != nil {
				logger.Warn("failed to close postgres store", "error", err)
			}
		}()
		sessionStore = pgStore
		storeLabel = "postgres"
	default:
		logger.Error("unsupported store backend", "backend", cfg.StoreBackend)
		os.Exit(1)
	}
	sessions := session.NewManager(cfg.MaxConnections, cfg.MaxQueueDepth, cfg.MaxQueueBytes, cfg.QueueOverflow, sessionStore)

	restored, err := sessions.Restore()
	if err != nil {
		logger.Error("failed to restore sessions", "error", err)
		os.Exit(1)
	}
	logger.Info("session state restored", "count", restored, "store_backend", cfg.StoreBackend, "store_target", storeLabel)

	tcpServer := server.New(cfg, logger, sessions, bus)
	if err := tcpServer.Run(ctx); err != nil {
		logger.Error("server exited with error", "error", err)
		os.Exit(1)
	}

	logger.Info("server stopped")
}
