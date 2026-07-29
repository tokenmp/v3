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
	"github.com/tokenmp/v3/packages/go/httpresp/envelope"
	"github.com/tokenmp/v3/services/api/internal/admin"
	"github.com/tokenmp/v3/services/api/internal/billing"
	"github.com/tokenmp/v3/services/api/internal/config"
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
	Verifier  identity.Verifier
	Proxy     *proxy.Proxy
	Quota     quota.Manager
	Logging   *logging.Client
	Billing   *billing.Client
	AdminAuth *admin.AuthClient
	ConfigCfg *config.Client
	Settings  *settings.Store
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
	if deps.Settings == nil {
		deps.Settings = settings.NewStore()
	}
	panelHandlers := panel.New(deps.Logging, deps.Billing, deps.Settings, deps.Logger)
	adminHandlers := admin.New(deps.Logging, deps.Billing, deps.AdminAuth, deps.Logger)
	configHandlers := admin.NewConfigHandlers(deps.ConfigCfg)

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
			r.Group(func(r chi.Router) {
				r.Use(envelope.Wrap)
				deps.KeysHandler.Routes(r)
			})
		}
		r.Get("/api/v1/user/balance", panelHandlers.GetUserBalance)
		r.Get("/api/v1/user/plans", panelHandlers.ListUserPlans)
		r.Get("/api/v1/user/settings", panelHandlers.GetUserSettings)
		r.Patch("/api/v1/user/settings", panelHandlers.UpdateUserSettings)
		r.Get("/api/v1/user/auto-models", panelHandlers.GetAutoModels)
		r.Patch("/api/v1/user/auto-models", panelHandlers.UpdateAutoModels)
		r.Get("/api/v1/request-logs", panelHandlers.ListRequestLogs)
		r.Get("/api/v1/request-logs/stats", panelHandlers.GetRequestLogStats)
		r.Get("/api/v1/request-logs/{requestId}", panelHandlers.GetRequestLog)
	})

	// Admin routes (identity → RequireAdmin). Aggregates Logging/Billing for
	// the admin dashboard. No quota.
	r.Group(func(r chi.Router) {
		r.Use(identity.Middleware(deps.Verifier, deps.Logger))
		r.Use(identity.RequireAdmin(deps.Logger))
		adminHandlers.Routes(r)
		configHandlers.Routes(r)
	})

	// Authenticated executor proxy routes (identity → quota → proxy).
	r.Group(func(r chi.Router) {
		r.Use(identity.Middleware(deps.Verifier, deps.Logger))
		r.Use(quotaMiddleware(deps.Quota, deps.Logging, deps.Settings, deps.Logger))
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
//
// When logClient is non-nil, the middleware posts an early "received" event
// to the Logging Service so the request log row appears immediately with
// final_status="processing". The executor's subsequent events update the
// same row (keyed by request_id) and supply the terminal status/completion
// time. The Edge receipt post is fire-and-forget (background context, errors
// swallowed) and never blocks the request path.
func quotaMiddleware(mgr quota.Manager, logClient *logging.Client, settingsStore *settings.Store, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !meteredExecutorRequest(r) {
				next.ServeHTTP(w, r)
				return
			}
			startedAt := time.Now().UTC()
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

			// Set the request ID on the forwarded request so the executor
			// logs under the same ID, enabling the progressive log row to be
			// updated by executor events.
			r.Header.Set("X-Request-ID", requestID)

			// Post an early "received" event so the request appears in the
			// log immediately with "processing" status. Fire-and-forget.
			if logClient != nil && logClient.Available() {
				log := logging.IngestLog{
					RequestID:   requestID,
					UserID:      claims.Subject,
					UserAgent:   sanitizeUserAgent(r.UserAgent()),
					FinalStatus: "processing",
					CreatedAt:   startedAt,
				}
				events := []logging.IngestEvent{{
					RequestID: requestID,
					Source:    "edge",
					Stage:     "received",
					Status:    "info",
					Message:   "request received",
					CreatedAt: startedAt,
				}}
				go func() {
					bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if err := logClient.Ingest(bgCtx, log, events); err != nil {
						logger.Debug("edge log ingest (received) failed", "error", err, "request_id", requestID)
					}
				}()
			}

			billingPlan, reservedReqs, reservedTokens, finalReqs, finalTokens := billingUsageForUser(settingsStore, claims.Subject)

			// Reserve (best-effort; noop manager skips).
			_, err := mgr.Reserve(r.Context(), reservationID, claims.Subject, requestID, billingPlan, reservedReqs, reservedTokens)
			if err != nil {
				if r.Context().Err() != nil {
					completedAt := time.Now().UTC()
					logEdgeClientCancelled(logClient, logger, requestID, claims.Subject, startedAt, completedAt)
					return
				}
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

			completedAt := time.Now().UTC()
			clientCancelled := r.Context().Err() != nil

			// Finalize or release based on status. Use a background context
			// with a timeout because the request context may be cancelled after
			// the response is sent (e.g. reverse proxy streaming). If the client
			// disconnected before the proxy completed, release the reservation and
			// durably close the early processing log row as client_cancelled.
			finCtx, finCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer finCancel()
			if clientCancelled {
				if err := mgr.Release(finCtx, reservationID); err != nil {
					logger.Warn("quota release after client cancel failed", "error", err, "request_id", requestID)
				}
				logEdgeClientCancelled(logClient, logger, requestID, claims.Subject, startedAt, completedAt)
			} else if ww.status >= 200 && ww.status < 400 {
				if err := mgr.Finalize(finCtx, reservationID, finalReqs, finalTokens); err != nil {
					logger.Warn("quota finalize failed", "error", err, "request_id", requestID)
				}
			} else {
				if err := mgr.Release(finCtx, reservationID); err != nil {
					logger.Warn("quota release failed", "error", err, "request_id", requestID)
				}
			}
		})
	}
}

func billingUsageForUser(settingsStore *settings.Store, userID string) (billingPlan string, reservedReqs int, reservedTokens int64, finalReqs int, finalTokens int64) {
	if settingsStore != nil && settingsStore.Get(userID).PreferredBilling == "token" {
		// The current public request path has no authoritative token estimate yet.
		// Reserve zero to avoid double-counting token balance (Billing token balance
		// sums all token ledger deltas, including reserves), then charge the minimal
		// one-token unit at finalize so selecting Token billing hits token ledger
		// instead of the default coding request ledger. A later phase should wire
		// provider confirmed usage into finalTokens.
		return "token", 0, 0, 0, 1
	}
	return "coding", 1, 0, 1, 0
}

func logEdgeClientCancelled(logClient *logging.Client, logger *slog.Logger, requestID, userID string, startedAt, completedAt time.Time) {
	if logClient == nil || !logClient.Available() {
		return
	}
	latencyMS := int(completedAt.Sub(startedAt).Milliseconds())
	if latencyMS < 0 {
		latencyMS = 0
	}
	log := logging.IngestLog{
		RequestID:   requestID,
		UserID:      userID,
		FinalStatus: "client_cancelled",
		HTTPStatus:  499,
		LatencyMS:   latencyMS,
		ErrorCode:   "client_cancelled",
		ErrorType:   "client_cancelled",
		CreatedAt:   startedAt,
		CompletedAt: &completedAt,
	}
	events := []logging.IngestEvent{{
		RequestID:  requestID,
		Source:     "edge",
		Stage:      "terminal",
		Status:     "failed",
		DurationMS: latencyMS,
		Message:    "client cancelled",
		CreatedAt:  completedAt,
	}}
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := logClient.Ingest(bgCtx, log, events); err != nil {
			logger.Debug("edge log ingest (client cancelled) failed", "error", err, "request_id", requestID)
		}
	}()
}

func meteredExecutorRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.Method != http.MethodPost {
		return false
	}
	switch r.URL.Path {
	case "/v1/chat/completions", "/v1/messages", "/v1/responses", "/v1/images/generations":
		return true
	default:
		return false
	}
}

// statusWriter wraps http.ResponseWriter to capture the status code.
// It implements Unwrap so http.ResponseController (used by httputil.ReverseProxy)
// can reach the underlying writer's Flush/Hijack methods. Without Unwrap,
// SSE streaming through the reverse proxy is silently buffered.
type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

// Unwrap exposes the underlying http.ResponseWriter so http.ResponseController
// can call Flush, Hijack, etc. on the real writer.
func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
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
