// Command auth is the TokenMP v3 auth service entrypoint.
//
// It loads configuration from AUTH_* environment variables, opens the
// PostgreSQL connection, starts the HTTP server, and performs graceful
// shutdown on SIGINT/SIGTERM.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/tokenmp/v3/services/auth/internal/config"
	"github.com/tokenmp/v3/services/auth/internal/database"
	"github.com/tokenmp/v3/services/auth/internal/server"
)

func main() {
	if err := run(); err != nil {
		// Use a fresh logger here because the structured logger may not have
		// been built yet when run() returns early.
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("auth service exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		// Fail fast on configuration errors before logging starts.
		return err
	}
	logger := newLogger(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(logger)

	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	// Log a safe, fixed message before attempting the connection. The
	// underlying error from database.Open is a stable classified sentinel and
	// never carries the DSN; we still log only the classification (not the
	// cause) to guarantee no host/user/db fragment is ever written out.
	logger.Info("opening database connection")
	db, err := database.Open(rootCtx, database.Config{
		DatabaseURL:     cfg.DatabaseURL,
		MaxOpenConns:    cfg.DBMaxOpenConns,
		MaxIdleConns:    cfg.DBMaxIdleConns,
		ConnMaxLifetime: cfg.DBConnMaxLifetime,
	})
	if err != nil {
		// err.Error() is a fixed safe classification; never log err's cause.
		logger.Error("database connection failed", "error", err)
		return err
	}
	defer func() {
		if cerr := database.Close(db); cerr != nil {
			logger.Error("error closing database", "error", cerr)
		}
	}()

	pinger := database.PingerFromDB(db)
	srv := server.New(cfg.HTTPAddr, pinger)

	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case err := <-errCh:
		logger.Error("http server error", "error", err)
		return err
	case sig := <-sigCh:
		logger.Info("shutdown signal received", "signal", sig.String())
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		return err
	}
	logger.Info("shutdown complete")
	return nil
}

func newLogger(level, format string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}
	var handler slog.Handler
	switch format {
	case "text":
		handler = slog.NewTextHandler(os.Stdout, opts)
	default:
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(handler)
}
