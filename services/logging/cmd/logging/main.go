// Command logging is the TokenMP v3 logging service entrypoint.
//
// Responsibility: receive executor/edge log pushes, persist them, with no
// plaintext body stored, into daily partitions, asynchronously. It loads
// configuration from LOGGING_* environment variables, opens the PostgreSQL
// Log DB connection, builds the repository (writer + reader) and HTTP server,
// and performs graceful shutdown on SIGINT/SIGTERM. The connection string
// is never logged and never echoed in errors (config validates the URL form
// up-front; database.Open and the repository classify all driver failures
// into sentinels).
//
// Scope: serve atomic batch ingestion via POST /v1/logs/ingest and a single
// request read via GET /v1/logs/{request_id}, plus /healthz and /readyz. No
// plaintext request/response body is ever persisted (the V3 privacy design
// dropped those columns).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tokenmp/v3/services/logging/internal/config"
	"github.com/tokenmp/v3/services/logging/internal/database"
	"github.com/tokenmp/v3/services/logging/internal/repository"
	"github.com/tokenmp/v3/services/logging/internal/server"
)

func main() {
	if err := run(); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("logging service exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := newLogger(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(ctx, database.Config{
		DatabaseURL:     cfg.DatabaseURL,
		MaxOpenConns:    cfg.DBMaxOpenConns,
		MaxIdleConns:    cfg.DBMaxIdleConns,
		ConnMaxLifetime: cfg.DBConnMaxLifetime,
	})
	if err != nil {
		// database.Open returns classified sentinels that never carry the DSN.
		return err
	}
	defer func() { _ = database.Close(db) }()

	repo := repository.New(db)
	srv := server.New(repo, repo, database.PingerFromDB(db), logger)
	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// Ingest bodies are bounded to 2 MiB server-side; allow headroom.
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		logger.Info("logging service listening", "addr", cfg.HTTPAddr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server error", "error", err)
		}
	}()

	<-ctx.Done()
	logger.Info("logging service shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("logging service shutdown error", "error", err)
		return err
	}
	logger.Info("logging service stopped")
	return nil
}

func newLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)}
	var h slog.Handler
	if cfg.LogFormat == "text" {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(h)
}

func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
