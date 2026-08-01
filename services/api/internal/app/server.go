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
	// ConfigAdminProxyEnabled gates registration of the admin config CRUD
	// proxy routes. When false, only the read-only models catalog is registered.
	ConfigAdminProxyEnabled bool
	Settings                *settings.Store
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
	configHandlers := admin.NewConfigHandlers(deps.ConfigCfg, deps.ConfigAdminProxyEnabled)

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

			billingPlan, reservedReqs, reservedTokens, finalReqs := billingUsageForUser(settingsStore, claims.Subject)

			// Reserve (best-effort; noop manager skips).
			_, err := mgr.Reserve(r.Context(), reservationID, claims.Subject, requestID, billingPlan, reservedReqs, reservedTokens)
			if err != nil {
				if r.Context().Err() != nil {
					completedAt := time.Now().UTC()
					logEdgeClientCancelled(logClient, logger, requestID, claims.Subject, startedAt, completedAt)
					return
				}
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				if errors.Is(err, quota.ErrQuotaExceeded) {
					logger.Info("quota exceeded", "request_id", requestID)
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = w.Write([]byte(`{"error":{"code":"quota_exceeded","message":"Quota exceeded"}}`))
					return
				}
				logger.Error("quota reserve failed", "error", err, "request_id", requestID)
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":{"code":"quota_unavailable","message":"Quota service unavailable"}}`))
				return
			}

			// Wrap the response writer to capture the status code and whether any
			// bytes/headers were committed (used to distinguish pre-commit failures
			// from stream-committed responses).
			ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(ww, r)

			completedAt := time.Now().UTC()
			clientCancelled := r.Context().Err() != nil
			committed := ww.wrote

			// Settlement must NOT be lost on client cancel: use a bounded detached
			// context (independent of the request context, which may already be
			// cancelled after the response is flushed). A missing Billing here is a
			// real error — it is logged, never silently double-charged. The
			// background reconciler resolves any pending rows the loop cannot settle.
			finCtx, finCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer finCancel()
			settleReservation(finCtx, mgr, logClient, logger, reservationID, requestID, billingPlan, finalReqs, startedAt, completedAt, clientCancelled, committed, ww.status, claims.Subject)
		})
	}
}

// settleReservation decides the durable terminal action for a reservation after
// the proxy returned. It is stream-aware and never guesses a token count:
//
//   - pre-commit failure (no bytes written, status >= 400 or client cancel
//     before commit) → Release the held amount.
//   - stream-committed success → fetch confirmed usage evidence once (bounded,
//     not a polling loop). usage known → Finalize actual counts; unknown →
//     MarkPending (reconciler resolves, never a fabricated 1-token guess).
//   - non-stream success (2xx/3xx) → for coding plans Finalize the request
//     count (1) with zero tokens; for token plans fetch usage once like streams.
//   - Billing temporarily unavailable at finalize → MarkPending so the
//     reconciler can settle later; the error is logged, not swallowed, and
//     never causes a double charge (Reserve already idempotent per id).
//
// All settlement calls use the detached finCtx so a cancelled request does not
// drop settlement.
func settleReservation(ctx context.Context, mgr quota.Manager, logClient *logging.Client, logger *slog.Logger, reservationID, requestID, billingPlan string, finalReqs int, startedAt, completedAt time.Time, clientCancelled, committed bool, httpStatus int, userID string) {
	if logger == nil {
		logger = slog.Default()
	}
	// Client disconnected. Whether or not bytes were committed, the
	// terminal is client-cancelled: never bill a success. Pre-commit cancel
	// releases the hold; a committed stream cancel marks pending so the
	// reconciler can settle from evidence (the upstream may or may not have
	// finished). Either way the early processing log row is closed as
	// client_cancelled so it is visible in lists.
	if clientCancelled {
		if !committed {
			if err := mgr.Release(ctx, reservationID); err != nil && !errors.Is(err, quota.ErrConflict) {
				logger.Warn("quota release (client cancel) failed", "error", err, "request_id", requestID)
			}
		} else {
			// Committed-then-cancelled (e.g. mid-stream). Do not guess usage;
			// park for the reconciler. If that fails, the hold stays active and
			// the sweeper resolves it; never fabricate a count.
			if err := mgr.MarkPending(ctx, reservationID); err != nil && !errors.Is(err, quota.ErrConflict) {
				logger.Warn("quota mark pending (client cancel) failed", "error", err, "request_id", requestID)
			}
		}
		logEdgeClientCancelled(logClient, logger, requestID, userID, startedAt, completedAt)
		return
	}

	// Pre-commit failure: nothing was sent to the client, so the upstream call
	// did not produce billable usage. Release the hold.
	preCommitFailure := !committed
	if preCommitFailure {
		if err := mgr.Release(ctx, reservationID); err != nil && !errors.Is(err, quota.ErrConflict) {
			logger.Warn("quota release (pre-commit) failed", "error", err, "request_id", requestID)
		}
		return
	}

	// A committed error response (e.g. upstream 502/5xx returned to the
	// client) means the upstream call failed to produce billable usage even
	// though bytes were sent. Release the hold rather than finalize a failed
	// request.
	if httpStatus >= 400 {
		if err := mgr.Release(ctx, reservationID); err != nil && !errors.Is(err, quota.ErrConflict) {
			logger.Warn("quota release (committed error) failed", "error", err, "request_id", requestID)
		}
		return
	}

	// Committed response (stream or non-stream success). Determine confirmed
	// usage. For coding plans, the unit is one request and tokens are not
	// metered here. For token plans, fetch usage evidence once from the Logging
	// Service (a single bounded call, NOT the old 5x polling loop with a 1-token
	// fallback). If usage is unknown, MarkPending.
	finalTokens := int64(0)
	usageKnown := true
	if billingPlan == "token" {
		finalTokens, usageKnown = confirmedTokenUsage(ctx, logClient, logger, requestID)
	}

	if !usageKnown {
		// Unknown usage must never be guessed at a token count. Park the
		// reservation for the reconciler. If MarkPending is itself
		// unavailable, the hold remains active and the sweeper will eventually
		// expire/reconcile it; we never fabricate a count.
		if err := mgr.MarkPending(ctx, reservationID); err != nil && !errors.Is(err, quota.ErrConflict) {
			logger.Warn("quota mark pending failed", "error", err, "request_id", requestID)
		}
		return
	}

	if err := mgr.Finalize(ctx, reservationID, finalReqs, finalTokens, true); err != nil {
		if errors.Is(err, quota.ErrConflict) {
			// Already settled with a different/opposite terminal — safe, no
			// double charge.
			return
		}
		// Billing temporarily unavailable at finalize time: park the
		// reservation so the reconciler can settle it from evidence. Do not
		// swallow this as success and never double-charge.
		logger.Warn("quota finalize failed; marking pending", "error", err, "request_id", requestID)
		if mpErr := mgr.MarkPending(ctx, reservationID); mpErr != nil && !errors.Is(mpErr, quota.ErrConflict) {
			logger.Warn("quota mark pending (after finalize failure) failed", "error", mpErr, "request_id", requestID)
		}
	}
}

func billingUsageForUser(settingsStore *settings.Store, userID string) (billingPlan string, reservedReqs int, reservedTokens int64, finalReqs int) {
	if settingsStore != nil && settingsStore.Get(userID).PreferredBilling == "token" {
		// Reserve zero to avoid double-counting token balance (Billing token balance
		// sums all token ledger deltas, including reserves). On success, finalize is
		// filled from confirmed Logging usage by confirmedTokenUsage.
		return "token", 0, 0, 0
	}
	return "coding", 1, 0, 1
}

// confirmedTokenUsage fetches confirmed token usage evidence ONCE from the
// Logging Service (a single bounded call, not the old 5x polling loop). It
// returns the total token count and whether the usage was confirmed. When
// the Logging Service is unavailable or the usage is not yet known, it
// returns (0, false) so the caller can MarkPending — it NEVER fabricates a
// 1-token guess.
func confirmedTokenUsage(ctx context.Context, logClient *logging.Client, logger *slog.Logger, requestID string) (int64, bool) {
	if logClient == nil || !logClient.Available() {
		return 0, false
	}
	detail, err := logClient.GetLog(ctx, requestID)
	if err != nil {
		if logger != nil {
			logger.Debug("token usage evidence lookup failed", "error", err, "request_id", requestID)
		}
		return 0, false
	}
	// Only the Logging record's usage_status="final" (set by the executor from
	// a confirmed upstream usage) counts as confirmed evidence.
	if detail.Log.UsageStatus != "final" {
		return 0, false
	}
	total := int64(detail.Log.TotalTokens)
	if total <= 0 {
		total = int64(detail.Log.InputTokens + detail.Log.OutputTokens)
	}
	if total <= 0 {
		return 0, false
	}
	return total, true
}

// isStreamRequest has been removed: settlement is decided by the committed
// flag and HTTP status, not by whether the request was streaming. The prior
// stream flag was a dead parameter that implied special-cased stream logic
// that never existed; the API AGENTS.md no longer claims stream-specific
// settlement.

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
