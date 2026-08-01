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
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	ratelimitpkg "github.com/tokenmp/v3/packages/go/ratelimit"
	"github.com/tokenmp/v3/packages/go/ratelimit/trustedip"
	"github.com/tokenmp/v3/services/auth/internal/auth"
	"github.com/tokenmp/v3/services/auth/internal/config"
	"github.com/tokenmp/v3/services/auth/internal/contract/authv1"
	"github.com/tokenmp/v3/services/auth/internal/database"
	internalratelimit "github.com/tokenmp/v3/services/auth/internal/ratelimit"
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
	apiKeyRepo := repository.NewAPIKeyRepository(db)
	txRunner := repository.NewTxRunner(db)

	clock := realClock{}
	authService := auth.NewService(userRepo, sessionRepo, txRunner, issuer, clock, cfg.AccessTokenTTL, cfg.RefreshTokenTTL)
	userStore := authv1api.NewUserRepoAdapter(userRepo)
	apiKeyStore := authv1api.NewAPIKeyRepoAdapter(apiKeyRepo)
	adminUserStore := authv1api.NewAdminUserRepoAdapter(userRepo)
	adminKeyStore := authv1api.NewAdminKeyRepoAdapter(apiKeyRepo)

	pinger := database.PingerFromDB(db)

	var rlMW authv1.StrictMiddlewareFunc
	var resolver *trustedip.Resolver
	if cfg.RateLimitEnabled {
		resolver, err = trustedip.NewResolver(cfg.RateLimitTrustedProxies)
		if err != nil {
			logger.Error("trusted proxy config invalid", "error", err)
			return err
		}
		opts, err := redis.ParseURL(cfg.RateLimitRedisAddr)
		if err != nil {
			logger.Error("rate limit redis url invalid")
			return fmt.Errorf("rate limit redis url invalid")
		}
		opts.DB = cfg.RateLimitRedisDB
		rdb := redis.NewClient(opts)
		defer func() {
			if cerr := rdb.Close(); cerr != nil {
				logger.Error("error closing redis", "error", cerr)
			}
		}()
		deriver, err := ratelimitpkg.NewKeyDeriver(cfg.RateLimitHMACSecret)
		if err != nil {
			logger.Error("rate limit deriver build failed", "error", err)
			return err
		}
		// Zero the short-lived secret copy in config now that the deriver has
		// its own internal copy; minimize the lifetime of the secret material.
		for i := range cfg.RateLimitHMACSecret {
			cfg.RateLimitHMACSecret[i] = 0
		}
		limiter := ratelimitpkg.NewRedisLimiter(rdb, 2*time.Second)
		rlMW = internalratelimit.NewStrictMiddleware(internalratelimit.Deps{
			Limiter: limiter,
			Deriver: deriver,
			Policies: internalratelimit.Policies{
				LoginIPCapacity:         cfg.RateLimitLoginIPCapacity,
				LoginIPRefill:           cfg.RateLimitLoginIPRefill,
				LoginAccountCapacity:    cfg.RateLimitLoginAcctCapacity,
				LoginAccountRefill:      cfg.RateLimitLoginAcctRefill,
				RegisterIPCapacity:      cfg.RateLimitRegisterIPCapacity,
				RegisterIPRefill:        cfg.RateLimitRegisterIPRefill,
				RegisterAccountCapacity: cfg.RateLimitRegisterAcctCapacity,
				RegisterAccountRefill:   cfg.RateLimitRegisterAcctRefill,
				RefreshIPCapacity:       cfg.RateLimitRefreshIPCapacity,
				RefreshIPRefill:         cfg.RateLimitRefreshIPRefill,
				RefreshAccountCapacity:  cfg.RateLimitRefreshAcctCapacity,
				RefreshAccountRefill:    cfg.RateLimitRefreshAcctRefill,
				TTL:                     cfg.RateLimitBucketTTL,
			},
		})
	}

	srv := authv1api.NewServer(authv1api.ServerConfig{
		Addr:                cfg.HTTPAddr,
		Pinger:              pinger,
		JWTVerifier:         verifier,
		UserStore:           userStore,
		AuthService:         authService,
		AccessTTL:           cfg.AccessTokenTTL,
		APIKeyStore:         apiKeyStore,
		AdminUserStore:      adminUserStore,
		AdminKeyStore:       adminKeyStore,
		KeyVerifier:         authv1api.NewKeyVerifierAdapter(apiKeyRepo, userStore),
		RateLimitMiddleware: rlMW,
		TrustedIPResolver:   resolver,
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
