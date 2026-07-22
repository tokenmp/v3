// Command executor runs the Executor service.
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/tokenmp/v3/services/executor/internal/app"
	"github.com/tokenmp/v3/services/executor/internal/composition"
	"github.com/tokenmp/v3/services/executor/internal/config"
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
	handler, err := composition.Build(ctx, cfg, os.LookupEnv)
	if err != nil {
		return fmt.Errorf("build composition: %w", err)
	}

	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.HTTPAddr, err)
	}
	defer listener.Close()

	server, err := app.NewServer(handler, cfg.ReadHeaderTimeout, cfg.IdleTimeout)
	if err != nil {
		return fmt.Errorf("create server: %w", err)
	}

	return app.Run(ctx, listener, server, cfg.ShutdownTimeout)
}
