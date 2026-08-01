// Command api is the entry point for the TokenMP v3 API Service (Edge/BFF).
//
// The Edge/BFF is the public-facing entry layer: it verifies client identity
// (JWT), reserves/settles quota via the Billing Service, and forwards requests
// to the Executor service. It does not execute model calls itself.
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	ratelimitpkg "github.com/tokenmp/v3/packages/go/ratelimit"
	"github.com/tokenmp/v3/packages/go/ratelimit/trustedip"
	"github.com/tokenmp/v3/services/api/internal/admin"
	"github.com/tokenmp/v3/services/api/internal/app"
	"github.com/tokenmp/v3/services/api/internal/billing"
	"github.com/tokenmp/v3/services/api/internal/config"
	"github.com/tokenmp/v3/services/api/internal/identity"
	"github.com/tokenmp/v3/services/api/internal/keys"
	"github.com/tokenmp/v3/services/api/internal/logging"
	"github.com/tokenmp/v3/services/api/internal/proxy"
	"github.com/tokenmp/v3/services/api/internal/quota"
	"github.com/tokenmp/v3/services/api/internal/ratelimit"
	"github.com/tokenmp/v3/services/api/internal/settings"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("api: %v", err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := slog.Default()

	var verifier identity.Verifier
	if cfg.JWTPublicKeyFile == "" {
		logger.Warn("identity: using noop verifier because API_ALLOW_NOOP_AUTH=true")
		verifier = identity.NewNoopVerifier()
	} else {
		verifier, err = identity.NewVerifier(cfg.JWTPublicKeyFile, cfg.JWTIssuer, cfg.JWTAudience, logger)
		if err != nil {
			return fmt.Errorf("identity verifier: %w", err)
		}
	}

	// When Auth URL is configured, the edge also accepts API keys (sk- prefix)
	// by verifying them against Auth's /api/v1/auth/verify-key endpoint.
	var apiKeyVerifier *identity.APIKeyVerifier
	if cfg.AuthURL != "" {
		apiKeyVerifier = identity.NewAPIKeyVerifier(cfg.AuthURL, logger)
	}
	compositeVerifier := identity.NewCompositeVerifier(verifier, apiKeyVerifier)

	userSettings := settings.NewStore()
	prx, err := proxy.NewWithSettings(cfg.ExecutorURL, cfg.ExecutorToken, userSettings, logger)
	if err != nil {
		return fmt.Errorf("proxy: %w", err)
	}

	// Auth URL 配置时启用密钥管理端点（代理到 Auth Service）；未配置时跳过。
	var keysHandler *keys.Handler
	if cfg.AuthURL != "" {
		keysHandler = keys.NewHandler(keys.New(cfg.AuthURL), logger)
	}

	// Config URL 配置时启用 admin 配置 CRUD 代理。
	var configClient *config.Client
	if cfg.ConfigServiceURL != "" {
		configClient = config.NewClient(cfg.ConfigServiceURL, cfg.ConfigServiceToken)
	}

	deps := app.Deps{
		Verifier:                compositeVerifier,
		Proxy:                   prx,
		Quota:                   quota.NewManager(cfg.BillingURL),
		Logging:                 logging.NewClient(cfg.LoggingURL),
		Billing:                 billing.NewClient(cfg.BillingURL),
		AdminAuth:               admin.NewAuthClient(cfg.AuthURL),
		ConfigCfg:               configClient,
		ConfigAdminProxyEnabled: cfg.ConfigAdminProxyEnabled,
		Settings:                userSettings,
		KeysHandler:             keysHandler,
		Logger:                  logger,
	}

	if cfg.RateLimitEnabled {
		resolver, err := trustedip.NewResolver(cfg.RateLimitTrustedProxies)
		if err != nil {
			return fmt.Errorf("trusted proxy config: %w", err)
		}
		opts, err := redis.ParseURL(cfg.RateLimitRedisAddr)
		if err != nil {
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
			return fmt.Errorf("rate limit deriver: %w", err)
		}
		// Zero the short-lived secret copy; the deriver holds its own copy.
		for i := range cfg.RateLimitHMACSecret {
			cfg.RateLimitHMACSecret[i] = 0
		}
		limiter := ratelimitpkg.NewRedisLimiter(rdb, 2*time.Second)
		deps.TrustedIPResolver = resolver
		deps.RateLimitDeps = ratelimit.Deps{
			Limiter: limiter,
			Deriver: deriver,
			Policies: ratelimit.Policies{
				IPCapacity:   cfg.RateLimitIPCapacity,
				IPRefill:     cfg.RateLimitIPRefill,
				SubjCapacity: cfg.RateLimitSubjCapacity,
				SubjRefill:   cfg.RateLimitSubjRefill,
				TTL:          cfg.RateLimitBucketTTL,
			},
		}
	}

	ln, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.HTTPAddr, err)
	}
	defer ln.Close()

	srv := app.NewServer(deps, cfg.ReadHeaderTimeout, cfg.IdleTimeout)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.Info("api: listening", "addr", ln.Addr())
	if err := app.Run(ctx, ln, srv, cfg.ShutdownTimeout); err != nil {
		return err
	}
	logger.Info("api: shutdown complete")
	return nil
}
