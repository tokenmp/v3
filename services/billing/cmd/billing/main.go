// Command billing is the TokenMP v3 billing service entrypoint.
//
// It owns plan, quota, and ledger operations. Executor does not connect to it
// directly: Edge/BFF calls this service. Requests reserve quota first, then
// finalize on success or release on failure.
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

	"github.com/tokenmp/v3/services/billing/internal/config"
	"github.com/tokenmp/v3/services/billing/internal/database"
	"github.com/tokenmp/v3/services/billing/internal/evidence"
	"github.com/tokenmp/v3/services/billing/internal/repository"
	"github.com/tokenmp/v3/services/billing/internal/server"
	"github.com/tokenmp/v3/services/billing/internal/sweeper"
)

func main() {
	if err := run(); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("billing service exited with error", "error", err)
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
		DatabaseURL: cfg.DatabaseURL, MaxOpenConns: cfg.DBMaxOpenConns,
		MaxIdleConns: cfg.DBMaxIdleConns, ConnMaxLifetime: cfg.DBConnMaxLifetime,
	})
	if err != nil {
		return err
	}
	defer func() { _ = database.Close(db) }()

	repo := repository.New(db)
	srv := server.New(repo, repo, repo, repo, repo, repo, repo, repo, database.PingerFromDB(db), logger)
	httpSrv := &http.Server{
		Addr: cfg.HTTPAddr, Handler: srv.Router(), ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second,
	}
	go func() {
		logger.Info("billing service listening", "addr", cfg.HTTPAddr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server error", "error", err)
		}
	}()

	// Background settlement sweeper/reconciler. It uses a detached context
	// (not bound to any request) and is bounded by the process shutdown ctx.
	// The reconciler resolves pending reservations from confirmed terminal
	// usage evidence (Billing → Logging HTTP, never the DB); an unconfigured
	// Logging endpoint degrades to the default-safe "keep pending" policy.
	if cfg.SweeperEnabled {
		ev := evidence.NewClient(cfg.LoggingURL, cfg.LoggingServiceToken, cfg.LoggingTimeout)
		sweep := sweeper.New(repo, sweeper.Config{
			Interval:          cfg.SweeperInterval,
			PendingGrace:      cfg.SweeperPendingGrace,
			ExpiryBatch:       cfg.SweeperExpiryBatch,
			PendingBatch:      cfg.SweeperPendingBatch,
			RetentionDeadline: cfg.RetentionDeadline,
			UnknownPolicy:     sweeper.UnknownPolicy(cfg.UnknownPolicy),
		}, ev, logger)
		go func() {
			logger.Info("billing sweeper started", "interval", cfg.SweeperInterval, "retention", cfg.RetentionDeadline, "unknown_policy", cfg.UnknownPolicy, "logging_configured", cfg.LoggingURL != "")
			if err := sweep.Run(ctx); err != nil {
				logger.Warn("billing sweeper exited", "error", err)
			}
		}()
	}

	<-ctx.Done()
	logger.Info("billing service shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("billing service shutdown error", "error", err)
		return err
	}
	logger.Info("billing service stopped")
	return nil
}

func newLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)}
	if cfg.LogFormat == "text" {
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
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
