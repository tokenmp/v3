// Package app wires Executor's HTTP application and lifecycle.
package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"time"
)

// ErrNilHandler is returned when NewServer is given a nil or typed-nil
// handler. A nil or typed-nil handler would serve an empty default mux or
// panic on the first request, and is never admissible for the Executor
// runtime, which must serve its composed routes or fail closed.
var ErrNilHandler = errors.New("app: handler must not be nil")

// NewServer creates the Executor HTTP server from an injected handler. It
// deliberately leaves ReadTimeout and WriteTimeout unset: ReadTimeout can
// truncate streaming reads, and WriteTimeout would terminate future SSE
// responses. A nil or typed-nil handler fails closed with ErrNilHandler.
func NewServer(handler http.Handler, readHeaderTimeout, idleTimeout time.Duration) (*http.Server, error) {
	if isNilHandler(handler) {
		return nil, ErrNilHandler
	}
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}, nil
}

// isNilHandler reports whether handler is an untyped nil interface or a
// typed-nil value wrapped in the interface (for example a *Handler that is
// nil, or a nil http.HandlerFunc). A typed-nil handler passes the plain nil
// comparison yet panics on a nil-pointer dereference the moment the server
// dispatches its ServeHTTP method; rejecting it at construction keeps the
// runtime from binding a port it cannot serve. This mirrors the nil/typed-nil
// detection used by the transport executor and request-id source.
func isNilHandler(handler http.Handler) bool {
	if handler == nil {
		return true
	}
	value := reflect.ValueOf(handler)
	switch value.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return value.IsNil()
	}
	return false
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
