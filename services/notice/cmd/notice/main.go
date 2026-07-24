// Command notice is the TokenMP v3 notice service entrypoint.
//
// It loads configuration from NOTICE_* environment variables, loads the
// Ed25519 JWT public key from disk (to verify Auth-issued access tokens),
// opens the PostgreSQL connection, builds the notice repository and HTTP
// server, and performs graceful shutdown on SIGINT/SIGTERM. Key paths are
// never echoed in logs or errors.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/tokenmp/v3/services/notice/internal/config"
	"github.com/tokenmp/v3/services/notice/internal/database"
	"github.com/tokenmp/v3/services/notice/internal/jwtverifier"
	"github.com/tokenmp/v3/services/notice/internal/repository"
	"github.com/tokenmp/v3/services/notice/internal/server"
)

func main() {
	if err := run(); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("notice service exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := newLogger(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(logger)

	logger.Info("loading jwt verifier")
	verifier, err := jwtverifier.New(cfg.JWTPublicKeyFile, cfg.JWTIssuer, cfg.JWTAudience)
	if err != nil {
		logger.Error("jwt verifier build failed", "error", err)
		return err
	}

	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	logger.Info("opening database connection")
	db, err := database.Open(rootCtx, database.Config{
		DatabaseURL:     cfg.DatabaseURL,
		MaxOpenConns:    cfg.DBMaxOpenConns,
		MaxIdleConns:    cfg.DBMaxIdleConns,
		ConnMaxLifetime: cfg.ConnMaxLifetime,
	})
	if err != nil {
		logger.Error("database connection failed", "error", err)
		return err
	}
	defer func() {
		if cerr := database.Close(db); cerr != nil {
			logger.Error("error closing database", "error", cerr)
		}
	}()

	repo := repository.NewRepository(db)
	pinger := database.PingerFromDB(db)

	srv := server.New(server.ServerConfig{
		Addr:     cfg.HTTPAddr,
		Pinger:   pinger,
		Verifier: verifier,
		Store:    repo,
		Logger:   logger,
	})

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
