package identity

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"log/slog"

	"github.com/tokenmp/v3/packages/go/httpresp"
)

// APIKeyPrefix is the prefix that identifies an API key (vs a JWT).
const APIKeyPrefix = "tmp_"

// APIKeyVerifier verifies API keys by calling the Auth service's verify-key
// endpoint. It is used when the Bearer token starts with "tmp_".
type APIKeyVerifier struct {
	authURL string
	http    *http.Client
	logger  *slog.Logger
}

// NewAPIKeyVerifier returns a verifier that calls Auth POST /api/v1/auth/verify-key.
// authURL is the base URL of the Auth service (no trailing slash).
func NewAPIKeyVerifier(authURL string, logger *slog.Logger) *APIKeyVerifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &APIKeyVerifier{
		authURL: strings.TrimSuffix(authURL, "/"),
		http:    &http.Client{},
		logger:  logger,
	}
}

func (v *APIKeyVerifier) Verify(ctx context.Context, apiKey string) (Claims, error) {
	if apiKey == "" {
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
	if resp.StatusCode != http.StatusOK {
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
