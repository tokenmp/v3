// Package app wires the API Service (Edge/BFF) HTTP application and lifecycle.
//
// Request flow:
//
//	client → identity middleware (JWT verify) → quota middleware (reserve)
//	→ proxy (forward to executor) → quota middleware (finalize/release)
//
// The quota middleware wraps the proxy: it reserves before forwarding and
// finalizes (on success) or releases (on error) after the response.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/tokenmp/v3/services/api/internal/billing"
	"github.com/tokenmp/v3/services/api/internal/identity"
	"github.com/tokenmp/v3/services/api/internal/keys"
	"github.com/tokenmp/v3/services/api/internal/logging"
	"github.com/tokenmp/v3/services/api/internal/panel"
	"github.com/tokenmp/v3/services/api/internal/proxy"
	"github.com/tokenmp/v3/services/api/internal/quota"
	"github.com/tokenmp/v3/services/api/internal/settings"
	"github.com/tokenmp/v3/services/api/internal/transport/healthz"
)

// Deps holds the runtime dependencies for the API Service.
type Deps struct {
	Verifier identity.Verifier
	Proxy    *proxy.Proxy
	Quota    quota.Manager
	Logging  *logging.Client
	Billing  *billing.Client
	Settings *settings.Store
	// KeysHandler 注册 /api/v1/keys* 路由（鉴权但不走配额）；nil 时不注册。
	KeysHandler *keys.Handler
	Logger      *slog.Logger
}

// NewServer creates the API Service HTTP server with the full middleware
// chain: healthz (anonymous), /api/v1/* panel business routes (identity),
// and /v1/* executor proxy routes (identity → quota → proxy).
func NewServer(deps Deps, readHeaderTimeout, idleTimeout time.Duration) *http.Server {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	panelHandlers := panel.New(deps.Logging, deps.Billing, deps.Settings, deps.Logger)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(logMiddleware(deps.Logger))

	// Anonymous health endpoint.
	r.Handle("/healthz", healthz.NewHandler())

	// Public plan listing (contract security: []).
	r.Get("/api/v1/plans", panelHandlers.ListPlans)

	// Authenticated panel business routes (no quota — these are reads/settings).
	r.Group(func(r chi.Router) {
		r.Use(identity.Middleware(deps.Verifier, deps.Logger))
		if deps.KeysHandler != nil {
			deps.KeysHandler.Routes(r)
		}
		r.Get("/api/v1/user/balance", panelHandlers.GetUserBalance)
		r.Get("/api/v1/user/plans", panelHandlers.ListUserPlans)
		r.Get("/api/v1/user/settings", panelHandlers.GetUserSettings)
		r.Patch("/api/v1/user/settings", panelHandlers.UpdateUserSettings)
		r.Get("/api/v1/request-logs", panelHandlers.ListRequestLogs)
		r.Get("/api/v1/request-logs/stats", panelHandlers.GetRequestLogStats)
		r.Get("/api/v1/request-logs/{requestId}", panelHandlers.GetRequestLog)
	})

	// Authenticated executor proxy routes (identity → quota → proxy).
	r.Group(func(r chi.Router) {
		r.Use(identity.Middleware(deps.Verifier, deps.Logger))
		r.Use(quotaMiddleware(deps.Quota, deps.Logger))
		// Catch-all forward to executor.
		r.HandleFunc("/v1/*", deps.Proxy.ServeHTTP)
	})

	return &http.Server{
		Handler:           r,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
}

// Run starts the HTTP server and blocks until ctx is cancelled or an error
// occurs. It performs a graceful shutdown with the given timeout.
func Run(ctx context.Context, ln net.Listener, srv *http.Server, shutdownTimeout time.Duration) error {
	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("serve HTTP server: %w", err)
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}
	return nil
}

// quotaMiddleware reserves quota before the request and finalizes or releases
// after. Reserve failures return 503. Finalize/release failures are logged
// but do not affect the already-sent response.
func quotaMiddleware(mgr quota.Manager, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := identity.FromContext(r.Context())
			if !ok {
				// No identity (should not happen after auth middleware); proceed
				// without quota to avoid blocking.
				next.ServeHTTP(w, r)
				return
			}

			reservationID := newReservationID()
			requestID := r.Header.Get("X-Request-ID")
			if requestID == "" {
				requestID = reservationID
			}

			// Reserve (best-effort; noop manager skips).
			_, err := mgr.Reserve(r.Context(), reservationID, claims.Subject, requestID, "coding", 1, 0)
			if err != nil {
				logger.Error("quota reserve failed", "error", err, "request_id", requestID)
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":{"code":"quota_unavailable","message":"Quota service unavailable"}}`))
				return
			}

			// Wrap the response writer to capture the status code.
			ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(ww, r)

			// Finalize or release based on status.
			if ww.status >= 200 && ww.status < 400 {
				if err := mgr.Finalize(r.Context(), reservationID, 1, 0); err != nil {
					logger.Warn("quota finalize failed", "error", err, "request_id", requestID)
				}
			} else {
				if err := mgr.Release(r.Context(), reservationID); err != nil {
					logger.Warn("quota release failed", "error", err, "request_id", requestID)
				}
			}
		})
	}
}

// statusWriter wraps http.ResponseWriter to capture the status code.
type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.status = code
	w.wrote = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.wrote = true
	}
	return w.ResponseWriter.Write(b)
}

// newReservationID generates a crypto-random reservation ID.
func newReservationID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "rsv_" + hex.EncodeToString(b)
}

// logMiddleware logs each HTTP request.
func logMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			logger.Info("http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration", time.Since(start).String(),
			)
		})
	}
}
