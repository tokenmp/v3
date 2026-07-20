// Package app wires Executor's HTTP application and lifecycle.
package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/transport/healthz"
)

// NewServer creates the Executor HTTP server. It deliberately leaves
// ReadTimeout and WriteTimeout unset: ReadTimeout can truncate streaming reads,
// and WriteTimeout would terminate future SSE responses.
func NewServer(readHeaderTimeout, idleTimeout time.Duration) *http.Server {
	return &http.Server{
		Handler:           healthz.NewHandler(),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
}

// Run serves server on listener until ctx is canceled. It then gracefully
// shuts down the server using shutdownTimeout. A normal http.ErrServerClosed
// result is not reported as an error.
func Run(ctx context.Context, listener net.Listener, server *http.Server, shutdownTimeout time.Duration) error {
	if listener == nil {
		return errors.New("listener must not be nil")
	}
	if server == nil {
		return errors.New("server must not be nil")
	}
	if shutdownTimeout <= 0 {
		return errors.New("shutdown timeout must be positive")
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP server: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("gracefully shut down HTTP server: %w", err)
		}
		if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP server: %w", err)
		}
		return nil
	}
}
