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

	"github.com/tokenmp/v3/services/api/internal/app"
	"github.com/tokenmp/v3/services/api/internal/billing"
	"github.com/tokenmp/v3/services/api/internal/config"
	"github.com/tokenmp/v3/services/api/internal/identity"
	"github.com/tokenmp/v3/services/api/internal/keys"
	"github.com/tokenmp/v3/services/api/internal/logging"
	"github.com/tokenmp/v3/services/api/internal/proxy"
	"github.com/tokenmp/v3/services/api/internal/quota"
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

	verifier, err := identity.NewVerifier(cfg.JWTPublicKeyFile, cfg.JWTIssuer, cfg.JWTAudience, logger)
	if err != nil {
		return fmt.Errorf("identity verifier: %w", err)
	}

	prx, err := proxy.New(cfg.ExecutorURL, cfg.ExecutorToken, logger)
	if err != nil {
		return fmt.Errorf("proxy: %w", err)
	}

	// Auth URL 配置时启用密钥管理端点（代理到 Auth Service）；未配置时跳过。
	var keysHandler *keys.Handler
	if cfg.AuthURL != "" {
		keysHandler = keys.NewHandler(keys.New(cfg.AuthURL), logger)
	}

	deps := app.Deps{
		Verifier:    verifier,
		Proxy:       prx,
		Quota:       quota.NewManager(cfg.BillingURL),
		Logging:     logging.NewClient(cfg.LoggingURL),
		Billing:     billing.NewClient(cfg.BillingURL),
		Settings:    settings.NewStore(),
		KeysHandler: keysHandler,
		Logger:      logger,
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
