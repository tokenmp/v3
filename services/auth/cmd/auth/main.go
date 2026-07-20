// Command auth is the TokenMP v3 auth service entrypoint.
//
// It loads configuration from AUTH_* environment variables, loads the Ed25519
// JWT key pair from disk, opens the PostgreSQL connection, builds the auth
// identity service and HTTP server, and performs graceful shutdown on
// SIGINT/SIGTERM. Key paths and PEM contents are never echoed in logs or
// errors.
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

	"github.com/tokenmp/v3/services/auth/internal/auth"
	"github.com/tokenmp/v3/services/auth/internal/config"
	"github.com/tokenmp/v3/services/auth/internal/database"
	"github.com/tokenmp/v3/services/auth/internal/repository"
	"github.com/tokenmp/v3/services/auth/internal/security/jwt"
	"github.com/tokenmp/v3/services/auth/internal/transport/authv1api"
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

	// Load the Ed25519 JWT key pair. The file paths and PEM contents are
	// never echoed in errors or logs (the jwt package returns stable
	// classified sentinels). Fail fast at startup.
	logger.Info("loading jwt key pair")
	kp, err := jwt.LoadKeyPair(cfg.JWTPrivateKeyFile, cfg.JWTPublicKeyFile)
	if err != nil {
		logger.Error("jwt key pair load failed", "error", err)
		return err
	}
	issuer, err := jwt.NewIssuer(kp, cfg.JWTIssuer, cfg.JWTAudience, cfg.AccessTokenTTL)
	if err != nil {
		logger.Error("jwt issuer build failed", "error", err)
		return err
	}
	verifier, err := jwt.NewVerifier(kp, cfg.JWTIssuer, cfg.JWTAudience)
	if err != nil {
		logger.Error("jwt verifier build failed", "error", err)
		return err
	}

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

	userRepo := repository.NewUserRepository(db)
	sessionRepo := repository.NewSessionRepository(db)
	txRunner := repository.NewTxRunner(db)

	clock := realClock{}
	authService := auth.NewService(userRepo, sessionRepo, txRunner, issuer, clock, cfg.AccessTokenTTL, cfg.RefreshTokenTTL)
	userStore := authv1api.NewUserRepoAdapter(userRepo)

	pinger := database.PingerFromDB(db)
	srv := authv1api.NewServer(authv1api.ServerConfig{
		Addr:        cfg.HTTPAddr,
		Pinger:      pinger,
		JWTVerifier: verifier,
		UserStore:   userStore,
		AuthService: authService,
		AccessTTL:   cfg.AccessTokenTTL,
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

// realClock implements auth.Clock using time.Now.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

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
