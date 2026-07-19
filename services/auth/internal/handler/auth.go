// Package handler exposes the HTTP handlers for the auth service.
//
// This file holds the auth identity flow handlers (register, login, refresh,
// logout, logout-all, me, password) and the uniform error response shape.
// Health endpoints live in health.go.
//
// All non-2xx responses use the uniform body {error:{code,message}}. Raw
// database, password and token errors never reach the response: the auth
// service returns typed errors that the handler maps to stable codes.
package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/tokenmp/v3/services/auth/internal/auth"
)

// ErrorCode is the stable machine-readable error code.
type ErrorCode string

const (
	CodeInvalidCredentials ErrorCode = "invalid_credentials"
	CodeEmailTaken         ErrorCode = "email_taken"
	CodeInvalidToken       ErrorCode = "invalid_token"
	CodeInvalidRefresh     ErrorCode = "invalid_refresh_token"
	CodePasswordTooWeak    ErrorCode = "password_too_weak"
	CodeInvalidEmail       ErrorCode = "invalid_email"
	CodeBadRequest         ErrorCode = "bad_request"
	CodeUnauthorized       ErrorCode = "unauthorized"
	CodeInternal           ErrorCode = "internal_error"
)

// errorBody is the uniform {error:{code,message}} shape.
type errorBody struct {
	Error errorFields `json:"error"`
}
type errorFields struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

// writeError writes the uniform error response. The message is a stable,
// human-readable string; it never includes raw DB / password / token material.
func writeError(w http.ResponseWriter, status int, code ErrorCode, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Error: errorFields{Code: code, Message: message}})
}

// writeJSON writes a 2xx JSON response with no-store caching.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// mapAuthError translates an auth.Service error into an HTTP status + code.
// Unknown errors map to 500 internal_error and never leak the cause.
// ErrUserDisabled is intentionally NOT mapped here: the service returns
// ErrInvalidCredentials (login) or ErrInvalidRefresh (refresh) for disabled
// accounts so the handler never signals account status to an attacker.
func mapAuthError(err error) (int, ErrorCode, string) {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		return http.StatusUnauthorized, CodeInvalidCredentials, "invalid email or password"
	case errors.Is(err, auth.ErrEmailTaken):
		return http.StatusConflict, CodeEmailTaken, "email already registered"
	case errors.Is(err, auth.ErrInvalidRefresh):
		return http.StatusUnauthorized, CodeInvalidRefresh, "refresh token is invalid or expired"
	case errors.Is(err, auth.ErrTokenReuse):
		// Reuse returns the same shape as invalid refresh to avoid signalling
		// to an attacker that reuse was detected.
		return http.StatusUnauthorized, CodeInvalidRefresh, "refresh token is invalid or expired"
	case errors.Is(err, auth.ErrPasswordTooWeak):
		return http.StatusBadRequest, CodePasswordTooWeak, "password does not meet the policy"
	case errors.Is(err, auth.ErrInvalidEmailFormat):
		return http.StatusBadRequest, CodeInvalidEmail, "email is not valid"
	default:
		return http.StatusInternalServerError, CodeInternal, "internal error"
	}
}

// AuthHandler wires the auth service to HTTP handlers.
type AuthHandler struct {
	svc *auth.Service
}

// NewAuthHandler builds an AuthHandler.
func NewAuthHandler(svc *auth.Service) *AuthHandler {
	return &AuthHandler{svc: svc}
}

// registerRequest is the JSON body for POST /register.
type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Register handles POST /api/v1/auth/register. On success it returns 201 with
// the public user view and does NOT auto-login (no tokens are issued).
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, "request body is not valid JSON")
		return
	}
	u, err := h.svc.Register(r.Context(), req.Email, req.Password)
	if err != nil {
		status, code, msg := mapAuthError(err)
		writeError(w, status, code, msg)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

// loginRequest is the JSON body for POST /login.
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// tokenResponse is the JSON body for login/refresh success.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// clientMeta extracts the IP and User-Agent from the request for session
// attribution. The IP is taken from the request RemoteAddr (Chi RealIP
// middleware rewrites it from X-Forwarded-For when present at the trusted
// boundary) with the port stripped so only a bare IP is written to the
// Postgres INET column. net.SplitHostPort handles both IPv4 ("1.2.3.4:1234")
// and IPv6 ("[::1]:1234") forms; if splitting fails the raw value is used
// as-is (e.g. a bare IP already stripped by a prior proxy).
//
// Security boundary: Chi's RealIP middleware trusts the last entry in
// X-Forwarded-For by default. Deployments MUST ensure that only trusted
// reverse proxies can set X-Forwarded-For (typically by stripping/overwriting
// it at the edge load balancer). An untrusted X-FF allows IP spoofing, which
// affects session attribution and any future rate limiting. This service does
// not validate X-FF provenance itself — that is a deployment responsibility.
func clientMeta(r *http.Request) (ip, userAgent string) {
	ip = r.RemoteAddr
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host // bare IP, no port — compatible with Postgres INET
	}
	// Fallback: if RemoteAddr was empty, use r.Host (rare, mainly tests).
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

// Login handles POST /api/v1/auth/login.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, "request body is not valid JSON")
		return
	}
	ip, ua := clientMeta(r)
	res, err := h.svc.Login(r.Context(), req.Email, req.Password, ip, ua)
	if err != nil {
		status, code, msg := mapAuthError(err)
		writeError(w, status, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, tokenResponse{
		AccessToken:  res.AccessToken,
		RefreshToken: res.RefreshToken,
		TokenType:    res.TokenType,
		ExpiresIn:    res.ExpiresIn,
	})
}

// refreshRequest is the JSON body for POST /refresh.
type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// Refresh handles POST /api/v1/auth/refresh.
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, "request body is not valid JSON")
		return
	}
	ip, ua := clientMeta(r)
	res, err := h.svc.Refresh(r.Context(), req.RefreshToken, ip, ua)
	if err != nil {
		status, code, msg := mapAuthError(err)
		writeError(w, status, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, tokenResponse{
		AccessToken:  res.AccessToken,
		RefreshToken: res.RefreshToken,
		TokenType:    res.TokenType,
		ExpiresIn:    res.ExpiresIn,
	})
}

// logoutRequest is the JSON body for POST /logout.
type logoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// Logout handles POST /api/v1/auth/logout. It is idempotent: any token,
// including an invalid or already-revoked one, returns 204.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	// Always close the body, even if we ignore decode errors.
	if r.Body != nil {
		const maxBodySize = 1 << 10
		limited := http.MaxBytesReader(w, r.Body, maxBodySize)
		defer limited.Close()
	}
	var req logoutRequest
	// A missing/invalid body still yields 204 (logout is idempotent and must
	// not leak whether the token existed).
	_ = json.NewDecoder(r.Body).Decode(&req)
	_ = h.svc.Logout(r.Context(), req.RefreshToken)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// LogoutAll handles POST /api/v1/auth/logout-all. It requires a Bearer token;
// the RequireUser middleware loads the user and injects it into the context.
func (h *AuthHandler) LogoutAll(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromContext(r.Context())
	if userID == "" {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "authentication required")
		return
	}
	if err := h.svc.LogoutAll(r.Context(), userID); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// Me handles GET /api/v1/auth/me. Requires a Bearer token.
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromContext(r.Context())
	if userID == "" {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "authentication required")
		return
	}
	u, err := h.svc.Me(r.Context(), userID)
	if err != nil {
		status, code, msg := mapAuthError(err)
		writeError(w, status, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// changePasswordRequest is the JSON body for PUT /password.
type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangePassword handles PUT /api/v1/auth/password. Requires a Bearer token.
func (h *AuthHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromContext(r.Context())
	if userID == "" {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "authentication required")
		return
	}
	var req changePasswordRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, "request body is not valid JSON")
		return
	}
	if err := h.svc.ChangePassword(r.Context(), userID, req.CurrentPassword, req.NewPassword); err != nil {
		status, code, msg := mapAuthError(err)
		writeError(w, status, code, msg)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// decodeJSON decodes the request body into dst. It rejects bodies that are not
// valid JSON or that contain unknown fields (DisallowUnknownFields) so a
// malformed request fails fast rather than silently defaulting. Trailing
// content after the first JSON value is detected by a second Decode that must
// return io.EOF — dec.More is unreliable at the top level.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	// Cap the request body to prevent OOM from unreasonably large payloads.
	// 1 KiB is generous for our small JSON bodies (email + password / refresh
	// token) and blocks any attempt to exhaust server memory.
	const maxBodySize = 1 << 10 // 1 KiB
	limited := http.MaxBytesReader(w, r.Body, maxBodySize)
	defer limited.Close()

	dec := json.NewDecoder(limited)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	// Reject trailing content (e.g. two JSON objects). A second Decode must
	// return io.EOF to confirm the body contained exactly one JSON value.
	// dec.More() is not reliable at the top level — it can return false even
	// when trailing non-JSON bytes remain.
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("unexpected trailing content")
	}
	return nil
}

// Trimmed helper to detect empty bearer tokens.
func bearerFromHeader(h http.Header) string {
	v := h.Get("Authorization")
	const prefix = "Bearer "
	if len(v) < len(prefix) || !strings.EqualFold(v[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(v[len(prefix):])
}
