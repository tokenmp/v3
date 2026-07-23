// Command executor runs the Executor service.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/app"
	"github.com/tokenmp/v3/services/executor/internal/composition"
	"github.com/tokenmp/v3/services/executor/internal/config"
	"github.com/tokenmp/v3/services/executor/internal/configreload"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	// Compose the HTTP handler before opening the listener so a missing
	// configuration file, invalid credential/identity mapping, or an
	// unsupported enabled route fails closed without ever binding a port.
	executorApp, err := composition.Build(ctx, cfg, os.LookupEnv)
	if err != nil {
		return fmt.Errorf("build composition: %w", err)
	}

	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.HTTPAddr, err)
	}
	defer listener.Close()

	server, err := app.NewServer(executorApp.Handler, cfg.ReadHeaderTimeout, cfg.IdleTimeout)
	if err != nil {
		return fmt.Errorf("create server: %w", err)
	}

	// Wire the Reloader's logger to the standard library logger so reload
	// events appear in the process output without leaking paths or content.
	logger := stdlibLogger{}
	executorApp.Reloader = executorApp.Reloader.WithLogger(logger)

	// SIGHUP channel for on-demand reload.
	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)
	defer signal.Stop(sighup)

	// Start config reload goroutine (SIGHUP + optional stat polling).
	go reloadLoop(ctx, executorApp.Reloader, cfg, sighup)

	return app.Run(ctx, listener, server, cfg.ShutdownTimeout)
}

// reloadLoop handles SIGHUP-triggered and optional stat-polling config reloads.
// It runs for the lifetime of ctx and exits when ctx is canceled.
func reloadLoop(ctx context.Context, reloader *configreload.Reloader, cfg config.Config, sighup <-chan os.Signal) {
	if reloader == nil {
		return
	}

	// Stat-based polling state.
	var (
		pollTicker <-chan time.Time
		lastMtime  time.Time
		lastSize   int64
	)
	if cfg.ConfigReloadInterval > 0 {
		ticker := time.NewTicker(cfg.ConfigReloadInterval)
		defer ticker.Stop()
		pollTicker = ticker.C
		// Initialize the baseline file state.
		if info, err := os.Stat(cfg.ConfigFile); err == nil {
			lastMtime = info.ModTime()
			lastSize = info.Size()
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-sighup:
			_ = reloader.Reload(ctx)
		case <-pollTicker:
			if cfg.ConfigReloadInterval <= 0 {
				continue
			}
			info, err := os.Stat(cfg.ConfigFile)
			if err != nil {
				continue
			}
			if !info.ModTime().Equal(lastMtime) || info.Size() != lastSize {
				lastMtime = info.ModTime()
				lastSize = info.Size()
				_ = reloader.Reload(ctx)
			}
		}
	}
}

// stdlibLogger adapts the standard library log package to the configreload
// Logger interface. It does not leak paths, content, or secrets.
type stdlibLogger struct{}

func (stdlibLogger) Infof(template string, args ...any)  { log.Printf(template, args...) }
func (stdlibLogger) Errorf(template string, args ...any) { log.Printf(template, args...) }
