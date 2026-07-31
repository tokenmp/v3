package identity

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"log/slog"

	"github.com/tokenmp/v3/packages/go/httpresp"
)

// APIKeyPrefix is the prefix that identifies an opaque API key (vs a JWT).
// Both V3 keys and legacy TokenMP prod keys use "sk-".
const APIKeyPrefix = "sk-"

// APIKeyVerifier verifies API keys by calling the Auth service's verify-key
// endpoint. It is used when the Bearer token starts with the API key prefix.
type APIKeyVerifier struct {
	authURL string
	http    *http.Client
	logger  *slog.Logger
}

// NewAPIKeyVerifier returns a verifier that calls Auth POST /api/v1/auth/verify-key.
// authURL must be an http(s) base URL without userinfo, query, or fragment.
// An invalid URL deliberately leaves the verifier unable to make a request,
// causing API key authentication to fail closed.
func NewAPIKeyVerifier(authURL string, logger *slog.Logger) *APIKeyVerifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &APIKeyVerifier{
		authURL: verifiedAuthBaseURL(authURL),
		http: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		logger: logger,
	}
}

func verifiedAuthBaseURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return ""
	}
	return strings.TrimSuffix(u.String(), "/")
}

func (v *APIKeyVerifier) Verify(ctx context.Context, apiKey string) (Claims, error) {
	if apiKey == "" || v.authURL == "" {
		return Claims{}, ErrUnauthenticated
	}
	body := fmt.Sprintf(`{"api_key":%q}`, apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.authURL+"/api/v1/auth/verify-key", strings.NewReader(body))
	if err != nil {
		return Claims{}, ErrUnauthenticated
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := v.http.Do(req)
	if err != nil {
		v.logger.Debug("api key verify: auth service unreachable", "error", err)
		return Claims{}, ErrUnauthenticated
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		v.logger.Debug("api key verify: rejected", "status", resp.StatusCode)
		return Claims{}, ErrUnauthenticated
	}
	var identity struct {
		UserID string `json:"user_id"`
		Role   string `json:"role"`
		Status string `json:"status"`
	}
	if err := httpresp.UnwrapData(respBody, &identity); err != nil {
		return Claims{}, ErrUnauthenticated
	}
	if identity.Status != "active" {
		return Claims{}, ErrUnauthenticated
	}
	return Claims{Subject: identity.UserID, Role: identity.Role}, nil
}

// compositeVerifier tries JWT verification first, then falls back to API key
// verification when the token has the API key prefix.
type compositeVerifier struct {
	jwt    Verifier
	apiKey *APIKeyVerifier
}

// NewCompositeVerifier returns a verifier that dispatches to JWT or API key
// verification based on the token prefix. If apiKeyVerifier is nil, only JWT
// verification is used.
func NewCompositeVerifier(jwt Verifier, apiKey *APIKeyVerifier) Verifier {
	if apiKey == nil {
		return jwt
	}
	return &compositeVerifier{jwt: jwt, apiKey: apiKey}
}

func (c *compositeVerifier) Verify(ctx context.Context, token string) (Claims, error) {
	if strings.HasPrefix(token, APIKeyPrefix) {
		return c.apiKey.Verify(ctx, token)
	}
	return c.jwt.Verify(ctx, token)
}
