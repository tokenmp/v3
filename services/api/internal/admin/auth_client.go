// Package admin implements Edge/BFF admin endpoints that aggregate downstream
// services for the admin dashboard. This file provides an HTTP client that
// proxies admin requests to the Auth Service's /api/v1/auth/admin/* endpoints.
package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tokenmp/v3/packages/go/httpresp"
)

// AuthClient calls the Auth Service admin endpoints.
type AuthClient struct {
	baseURL string
	hc      *http.Client
}

// NewAuthClient constructs a client for the Auth Service. baseURL is e.g.
// "http://127.0.0.1:8080".
func NewAuthClient(baseURL string) *AuthClient {
	return &AuthClient{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		hc: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Available returns true if the client was configured with a non-empty URL.
func (c *AuthClient) Available() bool { return c != nil && c.baseURL != "" }

// ErrAuthUnavailable indicates the Auth Service could not be reached.
var ErrAuthUnavailable = errors.New("admin: auth service unavailable")

// StatusError carries a non-2xx downstream status code and body for mapping.
type StatusError struct {
	StatusCode int
	Body       string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("admin: auth returned status %d", e.StatusCode)
}

const maxAuthBodySize = 4 << 20 // 4 MiB

// AdminUser corresponds to Auth's AdminUser schema.
type AdminUser struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// AdminApiKey corresponds to Auth's AdminApiKey schema.
type AdminApiKey struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	Name       string     `json:"name"`
	KeyPrefix  string     `json:"key_prefix"`
	KeySuffix  string     `json:"key_suffix"`
	Status     string     `json:"status"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// ListUsersResult is the paginated response from AuthAdminListUsers.
type ListUsersResult struct {
	Users    []AdminUser `json:"users"`
	Total    int         `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"pageSize"`
}

// ListKeysResult is the paginated response from AuthAdminListKeys.
type ListKeysResult struct {
	Keys     []AdminApiKey `json:"keys"`
	Total    int           `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"pageSize"`
}

// UpdateUserRequest is the body for PATCH /admin/users/{id}.
type UpdateUserRequest struct {
	Role   *string `json:"role,omitempty"`
	Status *string `json:"status,omitempty"`
}

// ListUsers calls GET /api/v1/auth/admin/users on Auth, forwarding the
// client Bearer token so Auth can verify role=admin.
func (c *AuthClient) ListUsers(ctx context.Context, bearer, search string, page, pageSize int) (ListUsersResult, error) {
	var res ListUsersResult
	q := url.Values{}
	if search != "" {
		q.Set("search", search)
	}
	q.Set("page", strconv.Itoa(page))
	q.Set("pageSize", strconv.Itoa(pageSize))
	path := "/api/v1/auth/admin/users?" + q.Encode()
	if err := c.do(ctx, http.MethodGet, bearer, path, nil, &res); err != nil {
		return ListUsersResult{}, err
	}
	return res, nil
}

// GetUser calls GET /api/v1/auth/admin/users/{id} on Auth.
func (c *AuthClient) GetUser(ctx context.Context, bearer, userID string) (AdminUser, error) {
	var res AdminUser
	path := "/api/v1/auth/admin/users/" + userID
	if err := c.do(ctx, http.MethodGet, bearer, path, nil, &res); err != nil {
		return AdminUser{}, err
	}
	return res, nil
}

// UpdateUser calls PATCH /api/v1/auth/admin/users/{id} on Auth.
func (c *AuthClient) UpdateUser(ctx context.Context, bearer, userID string, body UpdateUserRequest) (AdminUser, error) {
	var res AdminUser
	path := "/api/v1/auth/admin/users/" + userID
	if err := c.do(ctx, http.MethodPatch, bearer, path, body, &res); err != nil {
		return AdminUser{}, err
	}
	return res, nil
}

// ListKeys calls GET /api/v1/auth/admin/keys on Auth.
func (c *AuthClient) ListKeys(ctx context.Context, bearer string, page, pageSize int) (ListKeysResult, error) {
	var res ListKeysResult
	q := url.Values{}
	q.Set("page", strconv.Itoa(page))
	q.Set("pageSize", strconv.Itoa(pageSize))
	path := "/api/v1/auth/admin/keys?" + q.Encode()
	if err := c.do(ctx, http.MethodGet, bearer, path, nil, &res); err != nil {
		return ListKeysResult{}, err
	}
	return res, nil
}

// do sends an HTTP request to Auth and decodes the JSON response.
func (c *AuthClient) do(ctx context.Context, method, bearer, path string, body any, out any) error {
	target := c.baseURL + path
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("admin: marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bodyReader)
	if err != nil {
		return ErrAuthUnavailable
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return ErrAuthUnavailable
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxAuthBodySize))
	if err != nil {
		return ErrAuthUnavailable
	}
	if resp.StatusCode >= 400 {
		return &StatusError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}
	if out != nil {
		if err := httpresp.UnwrapData(respBody, out); err != nil {
			return ErrAuthUnavailable
		}
	}
	return nil
}
