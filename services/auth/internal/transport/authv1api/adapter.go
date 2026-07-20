// Package authv1api implements the oapi-codegen StrictServerInterface for
// the Auth v1 OpenAPI contract. It adapts the generated request/response types
// to the existing auth.Service, preserving all security semantics, error
// mapping, body-size limits, trailing-JSON rejection and 204 empty responses.
//
// This is the sole active API handler layer. The old handler.AuthHandler has
// been removed; all test coverage and helpers have been migrated here.
package authv1api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/tokenmp/v3/services/auth/internal/auth"
	"github.com/tokenmp/v3/services/auth/internal/contract/authv1"
	"github.com/tokenmp/v3/services/auth/internal/security/jwt"
)

// ---------------------------------------------------------------------------
// Compile-time assertion: adapter satisfies the generated StrictServerInterface.
// ---------------------------------------------------------------------------

var _ authv1.StrictServerInterface = (*StrictAdapter)(nil)

// ---------------------------------------------------------------------------
// Ports (same narrow interfaces the old handler used)
// ---------------------------------------------------------------------------

// Pinger is the readiness contract the /readyz handler depends on.
type Pinger interface {
	Ping(ctx context.Context) error
}

// UserStore is the minimal port the middleware needs to load a user on each
// request. It mirrors auth.UserRepository but is declared here so the
// middleware does not depend on the concrete repository package.
type UserStore interface {
	FindByID(ctx context.Context, id string) (status string, tokenVersion int, role string, err error)
}

// ---------------------------------------------------------------------------
// Error mapping
// ---------------------------------------------------------------------------

// mapAuthError translates an auth.Service error into an HTTP status + generated
// error code. Unknown errors map to 500 internal_error and never leak the cause.
func mapAuthError(err error) (int, authv1.ErrorErrorCode, string) {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		return http.StatusUnauthorized, authv1.InvalidCredentials, "invalid email or password"
	case errors.Is(err, auth.ErrEmailTaken):
		return http.StatusConflict, authv1.EmailTaken, "email already registered"
	case errors.Is(err, auth.ErrInvalidRefresh):
		return http.StatusUnauthorized, authv1.InvalidRefreshToken, "refresh token is invalid or expired"
	case errors.Is(err, auth.ErrTokenReuse):
		return http.StatusUnauthorized, authv1.InvalidRefreshToken, "refresh token is invalid or expired"
	case errors.Is(err, auth.ErrPasswordTooWeak):
		return http.StatusBadRequest, authv1.PasswordTooWeak, "password does not meet the policy"
	case errors.Is(err, auth.ErrInvalidEmailFormat):
		return http.StatusBadRequest, authv1.InvalidEmail, "email is not valid"
	default:
		return http.StatusInternalServerError, authv1.InternalError, "internal error"
	}
}

// errResp builds a generated Error from code + message.
func errResp(code authv1.ErrorErrorCode, msg string) authv1.Error {
	return authv1.Error{Error: struct {
		Code    authv1.ErrorErrorCode `json:"code"`
		Message string                `json:"message"`
	}{Code: code, Message: msg}}
}

// errHeaders returns the standard error response headers (Cache-Control +
// Content-Type) used by all JSON error responses.
func errHeaders() struct {
	CacheControl *string
	ContentType  *string
} {
	return struct {
		CacheControl *string
		ContentType  *string
	}{
		CacheControl: cacheControl(),
		ContentType:  contentTypeJSON(),
	}
}

// cacheControl returns a *string pointing to "no-store".
func cacheControl() *string {
	s := "no-store"
	return &s
}

// contentTypeJSON returns a *string pointing to the JSON content type.
func contentTypeJSON() *string {
	s := "application/json; charset=utf-8"
	return &s
}

// ---------------------------------------------------------------------------
// clientMeta — shared helper
// ---------------------------------------------------------------------------

func clientMeta(r *http.Request) (ip, userAgent string) {
	ip = r.RemoteAddr
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	if ip == "" {
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			ip = h
		} else {
			ip = r.Host
		}
	}
	userAgent = r.UserAgent()
	return ip, userAgent
}

// ---------------------------------------------------------------------------
// Context key for authenticated user ID
// ---------------------------------------------------------------------------

type ctxKey string

const ctxUserID ctxKey = "auth_user_id"

// WithUserID injects the authenticated user id into the context.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, ctxUserID, userID)
}

// UserIDFromContext returns the authenticated user id, or "" if unset.
func UserIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxUserID).(string); ok {
		return v
	}
	return ""
}

// ---------------------------------------------------------------------------
// StrictAdapter implements authv1.StrictServerInterface.
// ---------------------------------------------------------------------------

// StrictAdapter adapts the generated StrictServerInterface to auth.Service.
// It owns request validation, body-size limits, trailing-JSON rejection,
// error mapping and response shaping.
type StrictAdapter struct {
	svc       *auth.Service
	pinger    Pinger
	accessTTL int // seconds, for expires_in
}

// NewStrictAdapter builds a StrictAdapter.
func NewStrictAdapter(svc *auth.Service, pinger Pinger, accessTTL time.Duration) *StrictAdapter {
	return &StrictAdapter{
		svc:       svc,
		pinger:    pinger,
		accessTTL: int(accessTTL.Seconds()),
	}
}

// ----- Health endpoints -----

func (a *StrictAdapter) GetHealthz(ctx context.Context, _ authv1.GetHealthzRequestObject) (authv1.GetHealthzResponseObject, error) {
	return authv1.GetHealthz200JSONResponse{
		Body: authv1.HealthResponse{
			Status:    authv1.Ok,
			Service:   authv1.Auth,
			Timestamp: time.Now().UTC(),
		},
		Headers: authv1.GetHealthz200ResponseHeaders{
			CacheControl: cacheControl(),
			ContentType:  contentTypeJSON(),
		},
	}, nil
}

func (a *StrictAdapter) HeadHealthz(ctx context.Context, _ authv1.HeadHealthzRequestObject) (authv1.HeadHealthzResponseObject, error) {
	return authv1.HeadHealthz200Response{
		Headers: authv1.HeadHealthz200ResponseHeaders{
			CacheControl: cacheControl(),
			ContentType:  contentTypeJSON(),
		},
	}, nil
}

func (a *StrictAdapter) GetReadyz(ctx context.Context, _ authv1.GetReadyzRequestObject) (authv1.GetReadyzResponseObject, error) {
	ctx2, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if err := a.pinger.Ping(ctx2); err != nil {
		return authv1.GetReadyz503JSONResponse{
			Body: authv1.HealthResponse{
				Status:    authv1.Unready,
				Service:   authv1.Auth,
				Timestamp: time.Now().UTC(),
			},
			Headers: authv1.GetReadyz503ResponseHeaders{
				CacheControl: cacheControl(),
				ContentType:  contentTypeJSON(),
			},
		}, nil
	}
	return authv1.GetReadyz200JSONResponse{
		Body: authv1.HealthResponse{
			Status:    authv1.Ok,
			Service:   authv1.Auth,
			Timestamp: time.Now().UTC(),
		},
		Headers: authv1.GetReadyz200ResponseHeaders{
			CacheControl: cacheControl(),
			ContentType:  contentTypeJSON(),
		},
	}, nil
}

func (a *StrictAdapter) HeadReadyz(ctx context.Context, _ authv1.HeadReadyzRequestObject) (authv1.HeadReadyzResponseObject, error) {
	ctx2, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if err := a.pinger.Ping(ctx2); err != nil {
		return authv1.HeadReadyz503Response{
			Headers: authv1.HeadReadyz503ResponseHeaders{
				CacheControl: cacheControl(),
				ContentType:  contentTypeJSON(),
			},
		}, nil
	}
	return authv1.HeadReadyz200Response{
		Headers: authv1.HeadReadyz200ResponseHeaders{
			CacheControl: cacheControl(),
			ContentType:  contentTypeJSON(),
		},
	}, nil
}

// ----- Auth endpoints -----

func (a *StrictAdapter) Register(ctx context.Context, req authv1.RegisterRequestObject) (authv1.RegisterResponseObject, error) {
	u, err := a.svc.Register(ctx, req.Body.Email, req.Body.Password)
	if err != nil {
		status, code, msg := mapAuthError(err)
		switch status {
		case http.StatusBadRequest:
			return authv1.Register400JSONResponse{Body: errResp(code, msg), Headers: authv1.Register400ResponseHeaders(errHeaders())}, nil
		case http.StatusConflict:
			return authv1.Register409JSONResponse{Body: errResp(code, msg), Headers: authv1.Register409ResponseHeaders(errHeaders())}, nil
		default:
			return authv1.Register500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.Register500ResponseHeaders(errHeaders())}, nil
		}
	}
	return authv1.Register201JSONResponse{
		Body: publicUserToGenerated(u),
		Headers: authv1.Register201ResponseHeaders{
			CacheControl: cacheControl(),
			ContentType:  contentTypeJSON(),
		},
	}, nil
}

func (a *StrictAdapter) Login(ctx context.Context, req authv1.LoginRequestObject) (authv1.LoginResponseObject, error) {
	ip, ua := clientMetaFromCtx(ctx)
	res, err := a.svc.Login(ctx, req.Body.Email, req.Body.Password, ip, ua)
	if err != nil {
		status, code, msg := mapAuthError(err)
		switch status {
		case http.StatusUnauthorized:
			return authv1.Login401JSONResponse{Body: errResp(code, msg), Headers: authv1.Login401ResponseHeaders(errHeaders())}, nil
		case http.StatusBadRequest:
			return authv1.Login400JSONResponse{Body: errResp(code, msg), Headers: authv1.Login400ResponseHeaders(errHeaders())}, nil
		default:
			return authv1.Login500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.Login500ResponseHeaders(errHeaders())}, nil
		}
	}
	return authv1.Login200JSONResponse{
		Body: tokenResponseToGenerated(res),
		Headers: authv1.Login200ResponseHeaders{
			CacheControl: cacheControl(),
			ContentType:  contentTypeJSON(),
		},
	}, nil
}

func (a *StrictAdapter) Refresh(ctx context.Context, req authv1.RefreshRequestObject) (authv1.RefreshResponseObject, error) {
	ip, ua := clientMetaFromCtx(ctx)
	res, err := a.svc.Refresh(ctx, req.Body.RefreshToken, ip, ua)
	if err != nil {
		status, code, msg := mapAuthError(err)
		switch status {
		case http.StatusUnauthorized:
			return authv1.Refresh401JSONResponse{Body: errResp(code, msg), Headers: authv1.Refresh401ResponseHeaders(errHeaders())}, nil
		default:
			return authv1.Refresh500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.Refresh500ResponseHeaders(errHeaders())}, nil
		}
	}
	return authv1.Refresh200JSONResponse{
		Body: tokenResponseToGenerated(res),
		Headers: authv1.Refresh200ResponseHeaders{
			CacheControl: cacheControl(),
			ContentType:  contentTypeJSON(),
		},
	}, nil
}

func (a *StrictAdapter) Logout(ctx context.Context, req authv1.LogoutRequestObject) (authv1.LogoutResponseObject, error) {
	token := ""
	if req.Body != nil {
		token = req.Body.RefreshToken
	}
	_ = a.svc.Logout(ctx, token)
	return authv1.Logout204Response{
		Headers: authv1.Logout204ResponseHeaders{
			CacheControl: cacheControl(),
		},
	}, nil
}

func (a *StrictAdapter) LogoutAll(ctx context.Context, _ authv1.LogoutAllRequestObject) (authv1.LogoutAllResponseObject, error) {
	userID := UserIDFromContext(ctx)
	if userID == "" {
		return authv1.LogoutAll401JSONResponse{Body: errResp(authv1.Unauthorized, "authentication required"), Headers: authv1.LogoutAll401ResponseHeaders(errHeaders())}, nil
	}
	if err := a.svc.LogoutAll(ctx, userID); err != nil {
		return authv1.LogoutAll500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.LogoutAll500ResponseHeaders(errHeaders())}, nil
	}
	return authv1.LogoutAll204Response{
		Headers: authv1.LogoutAll204ResponseHeaders{
			CacheControl: cacheControl(),
		},
	}, nil
}

func (a *StrictAdapter) Me(ctx context.Context, _ authv1.MeRequestObject) (authv1.MeResponseObject, error) {
	userID := UserIDFromContext(ctx)
	if userID == "" {
		return authv1.Me401JSONResponse{Body: errResp(authv1.Unauthorized, "authentication required"), Headers: authv1.Me401ResponseHeaders(errHeaders())}, nil
	}
	u, err := a.svc.Me(ctx, userID)
	if err != nil {
		status, code, msg := mapAuthError(err)
		switch status {
		case http.StatusUnauthorized:
			return authv1.Me401JSONResponse{Body: errResp(code, msg), Headers: authv1.Me401ResponseHeaders(errHeaders())}, nil
		default:
			return authv1.Me500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.Me500ResponseHeaders(errHeaders())}, nil
		}
	}
	return authv1.Me200JSONResponse{
		Body: publicUserToGenerated(u),
		Headers: authv1.Me200ResponseHeaders{
			CacheControl: cacheControl(),
			ContentType:  contentTypeJSON(),
		},
	}, nil
}

func (a *StrictAdapter) ChangePassword(ctx context.Context, req authv1.ChangePasswordRequestObject) (authv1.ChangePasswordResponseObject, error) {
	userID := UserIDFromContext(ctx)
	if userID == "" {
		return authv1.ChangePassword401JSONResponse{Body: errResp(authv1.Unauthorized, "authentication required"), Headers: authv1.ChangePassword401ResponseHeaders(errHeaders())}, nil
	}
	if err := a.svc.ChangePassword(ctx, userID, req.Body.CurrentPassword, req.Body.NewPassword); err != nil {
		status, code, msg := mapAuthError(err)
		switch status {
		case http.StatusUnauthorized:
			return authv1.ChangePassword401JSONResponse{Body: errResp(code, msg), Headers: authv1.ChangePassword401ResponseHeaders(errHeaders())}, nil
		case http.StatusBadRequest:
			return authv1.ChangePassword400JSONResponse{Body: errResp(code, msg), Headers: authv1.ChangePassword400ResponseHeaders(errHeaders())}, nil
		default:
			return authv1.ChangePassword500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.ChangePassword500ResponseHeaders(errHeaders())}, nil
		}
	}
	return authv1.ChangePassword204Response{
		Headers: authv1.ChangePassword204ResponseHeaders{
			CacheControl: cacheControl(),
		},
	}, nil
}

// ---------------------------------------------------------------------------
// Conversion helpers
// ---------------------------------------------------------------------------

func publicUserToGenerated(u auth.PublicUser) authv1.PublicUser {
	uid, _ := uuid.Parse(u.ID)
	return authv1.PublicUser{
		Id:        uid,
		Email:     u.Email,
		Role:      authv1.PublicUserRole(u.Role),
		Status:    authv1.PublicUserStatus(u.Status),
		CreatedAt: u.CreatedAt,
	}
}

func tokenResponseToGenerated(res auth.TokenResponse) authv1.TokenResponse {
	return authv1.TokenResponse{
		AccessToken:  res.AccessToken,
		RefreshToken: res.RefreshToken,
		TokenType:    authv1.Bearer,
		ExpiresIn:    res.ExpiresIn,
	}
}

// clientMetaFromCtx extracts IP and User-Agent from the request stored in
// context by the clientMeta Chi middleware. Falls back to empty strings if the
// request is not available (should not happen in practice).
func clientMetaFromCtx(ctx context.Context) (ip, userAgent string) {
	if r, ok := httpRequestFromCtx(ctx); ok {
		return clientMeta(r)
	}
	return "", ""
}

type httpReqKey struct{}

// withHTTPRequest stores the *http.Request in context for clientMetaFromCtx.
func withHTTPRequest(ctx context.Context, r *http.Request) context.Context {
	return context.WithValue(ctx, httpReqKey{}, r)
}

func httpRequestFromCtx(ctx context.Context) (*http.Request, bool) {
	r, ok := ctx.Value(httpReqKey{}).(*http.Request)
	return r, ok
}

// ---------------------------------------------------------------------------
// Bearer authentication middleware (StrictMiddlewareFunc)
// ---------------------------------------------------------------------------

// bearerMiddleware returns a StrictMiddlewareFunc that validates Bearer tokens
// for operations that require authentication (logout-all, me, change-password).
// It injects the authenticated user ID into the context so the adapter methods
// can retrieve it via UserIDFromContext.
func bearerMiddleware(verifier *jwt.Verifier, store UserStore) authv1.StrictMiddlewareFunc {
	return func(f authv1.StrictHandlerFunc, operationID string) authv1.StrictHandlerFunc {
		authedOps := map[string]bool{
			"LogoutAll":      true,
			"Me":             true,
			"ChangePassword": true,
		}
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
			if !authedOps[operationID] {
				return f(ctx, w, r, request)
			}
			raw := bearerFromHeader(r.Header)
			if raw == "" {
				return nil, &unauthorizedErr{msg: "authentication required"}
			}
			claims, err := verifier.Verify(raw)
			if err != nil {
				return nil, &invalidTokenErr{msg: "invalid or expired access token"}
			}
			status, tv, _, sErr := store.FindByID(r.Context(), claims.RegisteredClaims.Subject)
			if sErr != nil {
				return nil, &invalidTokenErr{msg: "invalid or expired access token"}
			}
			if status != "active" {
				return nil, &invalidTokenErr{msg: "invalid or expired access token"}
			}
			if claims.TokenVersion != tv {
				return nil, &invalidTokenErr{msg: "token has been revoked"}
			}
			ctx = WithUserID(ctx, claims.RegisteredClaims.Subject)
			return f(ctx, w, r, request)
		}
	}
}

// bearerFromHeader extracts the Bearer token from the Authorization header.
func bearerFromHeader(h http.Header) string {
	v := h.Get("Authorization")
	const prefix = "Bearer "
	if len(v) < len(prefix) || !strings.EqualFold(v[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(v[len(prefix):])
}

// unauthorizedErr / invalidTokenErr are sentinel errors the custom
// response error handler maps to the appropriate 401 response.
type unauthorizedErr struct{ msg string }
type invalidTokenErr struct{ msg string }

func (e *unauthorizedErr) Error() string { return e.msg }
func (e *invalidTokenErr) Error() string { return e.msg }

// ---------------------------------------------------------------------------
// Custom strict handler options
// ---------------------------------------------------------------------------

// strictResponseErrorHandler maps errors from the strict middleware/handler
// to proper HTTP responses instead of the default plain-text 500.
func strictResponseErrorHandler(w http.ResponseWriter, r *http.Request, err error) {
	switch e := err.(type) {
	case *unauthorizedErr:
		writeErrorJSON(w, http.StatusUnauthorized, authv1.Unauthorized, e.msg)
	case *invalidTokenErr:
		writeErrorJSON(w, http.StatusUnauthorized, authv1.InvalidToken, e.msg)
	default:
		writeErrorJSON(w, http.StatusInternalServerError, authv1.InternalError, "internal error")
	}
}

// strictRequestErrorHandler handles request parsing errors (bad JSON, etc.)
// with the uniform error envelope.
func strictRequestErrorHandler(w http.ResponseWriter, r *http.Request, err error) {
	writeErrorJSON(w, http.StatusBadRequest, authv1.BadRequest, "request body is not valid JSON")
}

// writeErrorJSON writes the uniform {error:{code,message}} response.
func writeErrorJSON(w http.ResponseWriter, status int, code authv1.ErrorErrorCode, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errResp(code, msg))
}

// ---------------------------------------------------------------------------
// Pre-decode body middleware (net/http middleware, runs BEFORE generated
// strict handler decode). This is the critical compatibility layer that
// preserves the old handler's body validation semantics:
//   - 1 KiB limit
//   - DisallowUnknownFields
//   - single JSON value (trailing rejection)
//   - re-marshal to canonical JSON for generated decoder
//   - Logout: normalize empty/invalid/oversize to {"refresh_token":""}
//     and always 204
// ---------------------------------------------------------------------------
//
// The generated strict handler decodes JSON bodies BEFORE StrictMiddlewareFunc
// runs, so a StrictMiddlewareFunc cannot enforce DisallowUnknownFields or
// trailing rejection. We use a regular Chi middleware (net/http middleware)
// instead, which runs in the request chain before the generated handler
// receives the request.

const maxBodySize = 1 << 10 // 1 KiB

// bodyPreDecodeMiddleware returns a Chi middleware that validates and
// normalizes request bodies before the generated strict handler decodes them.
//
// For Register/Login/Refresh/ChangePassword:
//   - Limits body to 1 KiB (400 on overflow)
//   - Decodes with DisallowUnknownFields (400 on unknown field)
//   - Rejects trailing JSON (400)
//   - Re-marshals canonical JSON and replaces r.Body
//
// For Logout:
//   - Limits body to 1 KiB; on overflow, writes 204 and stops
//   - On empty/invalid JSON, normalizes to {"refresh_token":""}
//   - On valid JSON, re-marshals canonical and replaces r.Body
//   - Never returns an error status; always lets the handler return 204
//
// All 400 errors use the contract error envelope {error:{code,message}}.
func bodyPreDecodeMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/auth/register", "/api/v1/auth/login",
				"/api/v1/auth/refresh", "/api/v1/auth/password":
				if err := validateStrictBody(w, r); err != nil {
					// validateStrictBody already wrote the error response.
					return
				}
				next.ServeHTTP(w, r)
			case "/api/v1/auth/logout":
				normalizeLogoutBody(w, r, next)
			default:
				next.ServeHTTP(w, r)
			}
		})
	}
}

// validateStrictBody enforces body-size limits, DisallowUnknownFields, and
// trailing-JSON rejection for ordinary endpoints. On success it re-marshals
// the decoded value as canonical JSON and replaces r.Body so the generated
// strict handler can decode it cleanly. On failure it writes a 400 error
// envelope and returns a non-nil error.
func validateStrictBody(w http.ResponseWriter, r *http.Request) error {
	if r.Body == nil {
		writeErrorJSON(w, http.StatusBadRequest, authv1.BadRequest, "request body is not valid JSON")
		return errors.New("empty body")
	}
	limited := http.MaxBytesReader(w, r.Body, maxBodySize)
	defer limited.Close()

	dec := json.NewDecoder(limited)
	dec.DisallowUnknownFields()

	// Decode into the correct generated request schema type so that
	// DisallowUnknownFields actually rejects unknown fields. We switch on
	// the request path to determine the target type.
	var val any
	switch r.URL.Path {
	case "/api/v1/auth/register":
		var v authv1.RegisterRequest
		if err := dec.Decode(&v); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, authv1.BadRequest, "request body is not valid JSON")
			return err
		}
		val = v
	case "/api/v1/auth/login":
		var v authv1.LoginRequest
		if err := dec.Decode(&v); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, authv1.BadRequest, "request body is not valid JSON")
			return err
		}
		val = v
	case "/api/v1/auth/refresh":
		var v authv1.RefreshRequest
		if err := dec.Decode(&v); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, authv1.BadRequest, "request body is not valid JSON")
			return err
		}
		val = v
	case "/api/v1/auth/password":
		var v authv1.ChangePasswordRequest
		if err := dec.Decode(&v); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, authv1.BadRequest, "request body is not valid JSON")
			return err
		}
		val = v
	default:
		// Fallback: decode into generic map.
		if err := dec.Decode(&val); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, authv1.BadRequest, "request body is not valid JSON")
			return err
		}
	}

	// Reject trailing content after the first JSON value.
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeErrorJSON(w, http.StatusBadRequest, authv1.BadRequest, "request body is not valid JSON")
		return errors.New("trailing content")
	}

	// Re-marshal to canonical JSON and replace r.Body so the generated
	// strict handler's json.NewDecoder(r.Body).Decode(&body) succeeds.
	canonical, err := json.Marshal(val)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, authv1.BadRequest, "request body is not valid JSON")
		return err
	}
	r.Body = io.NopCloser(strings.NewReader(string(canonical)))
	r.ContentLength = int64(len(canonical))
	return nil
}

// normalizeLogoutBody handles the Logout endpoint's idempotent body semantics.
// Any body issue (empty, invalid JSON, oversize) results in a normalized
// {"refresh_token":""} body so the generated handler can decode it and the
// adapter will call svc.Logout with an empty token (which returns 204).
// Oversize bodies are handled via MaxBytesReader; the read is bounded.
//
// Unlike validateStrictBody, this does NOT use DisallowUnknownFields — the
// old handler only decoded the first JSON value and ignored unknown fields.
// A body like {"refresh_token":"valid","extra":1} must still revoke the
// valid token. Trailing content after the first JSON value is also ignored
// (the old handler only called Decode once). Any decode failure or empty
// result normalizes to an empty-token body; the handler always returns 204.
func normalizeLogoutBody(w http.ResponseWriter, r *http.Request, next http.Handler) {
	if r.Body == nil || r.ContentLength == 0 {
		// No body — normalize to empty-token JSON.
		if r.Body != nil {
			_ = r.Body.Close()
		}
		r.Body = io.NopCloser(strings.NewReader(`{"refresh_token":""}`))
		r.ContentLength = 20
		next.ServeHTTP(w, r)
		return
	}

	limited := http.MaxBytesReader(w, r.Body, maxBodySize)

	dec := json.NewDecoder(limited)
	// NOTE: No DisallowUnknownFields — unknown fields must not cause the
	// valid refresh_token to be cleared. The old handler only decoded the
	// first JSON value and ignored trailing content.

	var v authv1.LogoutRequest
	decodeErr := dec.Decode(&v)

	if decodeErr != nil {
		// Any decode error (invalid JSON, oversize) → normalize.
		// We've already bounded the read via MaxBytesReader.
		_ = limited.Close()
		r.Body = io.NopCloser(strings.NewReader(`{"refresh_token":""}`))
		r.ContentLength = 20
		next.ServeHTTP(w, r)
		return
	}

	// Valid JSON — re-marshal canonical and replace r.Body.
	// Trailing content after the first JSON value is ignored (old handler
	// only called Decode once).
	_ = limited.Close()
	canonical, marshalErr := json.Marshal(v)
	if marshalErr != nil {
		r.Body = io.NopCloser(strings.NewReader(`{"refresh_token":""}`))
		r.ContentLength = 20
		next.ServeHTTP(w, r)
		return
	}
	r.Body = io.NopCloser(strings.NewReader(string(canonical)))
	r.ContentLength = int64(len(canonical))
	next.ServeHTTP(w, r)
}

// ---------------------------------------------------------------------------
// clientMeta middleware (Chi middleware, stores *http.Request in context)
// ---------------------------------------------------------------------------

// cacheControlNoStoreMiddleware sets Cache-Control: no-store on responses
// for known contract paths only. This includes Chi's 405 response for an
// unsupported method on a known path, but not a 404 for an unknown path.
// This is a safety net: the generated response structs and writeErrorJSON
// already set Cache-Control, but this middleware ensures no response path
// through a contract path can accidentally omit it.
func cacheControlNoStoreMiddleware() func(http.Handler) http.Handler {
	contractPaths := map[string]bool{
		"/healthz":                true,
		"/readyz":                 true,
		"/api/v1/auth/register":   true,
		"/api/v1/auth/login":      true,
		"/api/v1/auth/refresh":    true,
		"/api/v1/auth/logout":     true,
		"/api/v1/auth/logout-all": true,
		"/api/v1/auth/me":         true,
		"/api/v1/auth/password":   true,
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if contractPaths[r.URL.Path] {
				nw := &cacheControlWriter{ResponseWriter: w}
				next.ServeHTTP(nw, r)
				if !nw.headerWritten {
					w.Header().Set("Cache-Control", "no-store")
				}
			} else {
				next.ServeHTTP(w, r)
			}
		})
	}
}

// cacheControlWriter wraps http.ResponseWriter to track whether headers
// have been written. If the handler writes headers without Cache-Control,
// the middleware will add it before the response is flushed.
type cacheControlWriter struct {
	http.ResponseWriter
	headerWritten bool
}

func (w *cacheControlWriter) WriteHeader(code int) {
	if !w.headerWritten {
		if w.ResponseWriter.Header().Get("Cache-Control") == "" {
			w.ResponseWriter.Header().Set("Cache-Control", "no-store")
		}
		w.headerWritten = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *cacheControlWriter) Write(b []byte) (int, error) {
	if !w.headerWritten {
		if w.ResponseWriter.Header().Get("Cache-Control") == "" {
			w.ResponseWriter.Header().Set("Cache-Control", "no-store")
		}
		w.headerWritten = true
	}
	return w.ResponseWriter.Write(b)
}

// ---------------------------------------------------------------------------
// clientMeta middleware (Chi middleware, stores *http.Request in context)
// ---------------------------------------------------------------------------

// clientMetaMiddleware is a Chi middleware that stores the *http.Request in
// context so the StrictAdapter can extract client IP and User-Agent via
// clientMetaFromCtx. This is the replacement for the old
// bodyValidationMiddleware's context-injection logic.
func clientMetaMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := withHTTPRequest(r.Context(), r)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ---------------------------------------------------------------------------
// Server wiring
// ---------------------------------------------------------------------------

// ServerConfig holds the dependencies needed to build the HTTP server.
type ServerConfig struct {
	Addr        string
	Pinger      Pinger
	JWTVerifier *jwt.Verifier
	UserStore   UserStore
	AuthService *auth.Service
	AccessTTL   time.Duration
}

// NewServer builds a Chi HTTP server with generated routes, strict handler,
// Bearer authentication middleware, and global middleware (RequestID, RealIP,
// Recoverer, body pre-decode validation, clientMeta injection).
//
// The body pre-decode middleware runs as a Chi middleware BEFORE the generated
// strict handler, ensuring body-size limits, DisallowUnknownFields, and
// trailing-JSON rejection are enforced before the generated handler's
// json.NewDecoder(r.Body).Decode(&body) call.
func NewServer(cfg ServerConfig) *Server {
	adapter := NewStrictAdapter(cfg.AuthService, cfg.Pinger, cfg.AccessTTL)

	middlewares := []authv1.StrictMiddlewareFunc{}
	if cfg.JWTVerifier != nil && cfg.UserStore != nil {
		middlewares = append(middlewares, bearerMiddleware(cfg.JWTVerifier, cfg.UserStore))
	}

	strictHandler := authv1.NewStrictHandlerWithOptions(adapter, middlewares, authv1.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  strictRequestErrorHandler,
		ResponseErrorHandlerFunc: strictResponseErrorHandler,
	})

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(cacheControlNoStoreMiddleware())
	r.Use(bodyPreDecodeMiddleware())
	r.Use(clientMetaMiddleware())

	authv1.HandlerWithOptions(strictHandler, authv1.ChiServerOptions{
		BaseRouter: r,
	})

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return &Server{httpSrv: srv}
}

// Server wraps an *http.Server with the auth service routes.
type Server struct {
	httpSrv *http.Server
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	if err := s.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown gracefully stops the server within the given timeout.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpSrv.Shutdown(ctx)
}

// Router exposes the underlying mux for testing.
func (s *Server) Router() http.Handler {
	return s.httpSrv.Handler
}
