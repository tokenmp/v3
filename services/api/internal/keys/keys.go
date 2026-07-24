// Package keys implements the Edge/BFF HTTP client that proxies API key
// management requests to the Auth Service.
//
// The Edge owns the public /api/v1/keys* contract (packages/contracts/openapi/
// api/v1.yaml) and forwards each request to the Auth Service's internal
// /api/v1/auth/keys* management API, passing the client Bearer token through
// so Auth can resolve the user from the JWT subject. The Edge never touches
// the Auth database and never sees key hashes; it only relays the one-time
// secret returned by create/rotate.
//
// Wire format translation: Auth uses snake_case (key_prefix, created_at, ...);
// the Edge contract uses camelCase (keyPrefix, createdAt, ...). This client
// decodes Auth's snake_case response into AuthDTO and re-encodes it via the
// generated apiv1 types.
package keys

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tokenmp/v3/services/api/internal/contract/apiv1"
)

// ErrAuthUnavailable indicates the Auth Service could not be reached or
// returned an error that the Edge cannot recover. It never embeds the Auth
// URL, request body, or response body.
var ErrAuthUnavailable = errors.New("keys: auth service unavailable")

// maxBodySize 限制 Auth 响应体大小，防止异常大响应耗尽内存。
const maxBodySize = 4 << 20 // 4 MiB

// Client 调用 Auth Service 的密钥管理 API。
type Client struct {
	baseURL string
	hc      *http.Client
}

// New 构造一个指向 Auth 的客户端。baseURL 形如 "http://127.0.0.1:8080"。
func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		hc: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Auth 端 wire 类型（snake_case）
// ---------------------------------------------------------------------------

// authAPIKey 对应 Auth 契约的 ApiKey（不含 secret）。
type authAPIKey struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	KeyPrefix  string     `json:"key_prefix"`
	KeySuffix  string     `json:"key_suffix"`
	Status     string     `json:"status"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

// authAPIKeyCreated 对应 Auth 契约的 ApiKeyCreated（含一次性 secret）。
type authAPIKeyCreated struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	KeyPrefix string     `json:"key_prefix"`
	KeySuffix string     `json:"key_suffix"`
	Secret    string     `json:"secret"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// authError 对应 Auth 契约的统一错误信封。
type authError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Result 是一次 Auth 调用的结构化结果，供 Edge handler 映射为生成响应。
type Result struct {
	// Key 单个密钥（不含 secret），用于 list/get/update。
	Key *authAPIKey
	// Keys 密钥列表，用于 list。
	Keys []authAPIKey
	// Created 含一次性 secret 的密钥，用于 create/rotate。
	Created *authAPIKeyCreated
	// NoContent 表示 204（delete）。
	NoContent bool
}

// StatusError 携带 Auth 返回的 HTTP 状态码与错误信封，便于 Edge 精确映射。
type StatusError struct {
	Code    int
	AuthErr *authError
	Body    string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("keys: auth returned status %d", e.Code)
}

// ---------------------------------------------------------------------------
// 公共方法
// ---------------------------------------------------------------------------

// List 转发 GET /api/v1/keys。
func (c *Client) List(ctx context.Context, bearer string) (Result, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/v1/auth/keys", bearer, nil)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		var body struct {
			Keys []authAPIKey `json:"keys"`
		}
		if err := decodeJSON(resp, &body); err != nil {
			return Result{}, ErrAuthUnavailable
		}
		return Result{Keys: body.Keys}, nil
	}
	return Result{}, statusErr(resp)
}

// Create 转发 POST /api/v1/keys。
func (c *Client) Create(ctx context.Context, bearer, name string, expiresAt *time.Time) (Result, error) {
	reqBody := map[string]any{}
	if name != "" {
		reqBody["name"] = name
	}
	if expiresAt != nil {
		reqBody["expires_at"] = (*expiresAt).Format(time.RFC3339Nano)
	}
	resp, err := c.do(ctx, http.MethodPost, "/api/v1/auth/keys", bearer, reqBody)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		var body struct {
			Key authAPIKeyCreated `json:"key"`
		}
		if err := decodeJSON(resp, &body); err != nil {
			return Result{}, ErrAuthUnavailable
		}
		k := body.Key
		return Result{Created: &k}, nil
	}
	return Result{}, statusErr(resp)
}

// Get 转发 GET /api/v1/keys/{keyId}。
func (c *Client) Get(ctx context.Context, bearer, keyID string) (Result, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/v1/auth/keys/"+keyID, bearer, nil)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		var body struct {
			Key authAPIKey `json:"key"`
		}
		if err := decodeJSON(resp, &body); err != nil {
			return Result{}, ErrAuthUnavailable
		}
		k := body.Key
		return Result{Key: &k}, nil
	}
	return Result{}, statusErr(resp)
}

// Update 转发 PATCH /api/v1/keys/{keyId}。
func (c *Client) Update(ctx context.Context, bearer, keyID string, name *string, status *string) (Result, error) {
	reqBody := map[string]any{}
	if name != nil {
		reqBody["name"] = *name
	}
	if status != nil {
		reqBody["status"] = *status
	}
	resp, err := c.do(ctx, http.MethodPatch, "/api/v1/auth/keys/"+keyID, bearer, reqBody)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		var body struct {
			Key authAPIKey `json:"key"`
		}
		if err := decodeJSON(resp, &body); err != nil {
			return Result{}, ErrAuthUnavailable
		}
		k := body.Key
		return Result{Key: &k}, nil
	}
	return Result{}, statusErr(resp)
}

// Delete 转发 DELETE /api/v1/keys/{keyId}。
func (c *Client) Delete(ctx context.Context, bearer, keyID string) (Result, error) {
	resp, err := c.do(ctx, http.MethodDelete, "/api/v1/auth/keys/"+keyID, bearer, nil)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return Result{NoContent: true}, nil
	}
	return Result{}, statusErr(resp)
}

// Rotate 转发 POST /api/v1/keys/{keyId}/rotate。
func (c *Client) Rotate(ctx context.Context, bearer, keyID string) (Result, error) {
	resp, err := c.do(ctx, http.MethodPost, "/api/v1/auth/keys/"+keyID+"/rotate", bearer, nil)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		var body struct {
			Key authAPIKeyCreated `json:"key"`
		}
		if err := decodeJSON(resp, &body); err != nil {
			return Result{}, ErrAuthUnavailable
		}
		k := body.Key
		return Result{Created: &k}, nil
	}
	return Result{}, statusErr(resp)
}

// ---------------------------------------------------------------------------
// 内部辅助
// ---------------------------------------------------------------------------

func (c *Client) do(ctx context.Context, method, path, bearer string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, ErrAuthUnavailable
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	return resp, nil
}

// decodeJSON 在 maxBodySize 限制内解码 JSON。
func decodeJSON(resp *http.Response, out any) error {
	limited := io.LimitReader(resp.Body, maxBodySize)
	dec := json.NewDecoder(limited)
	if err := dec.Decode(out); err != nil {
		return err
	}
	// 拒绝 trailing 内容。
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("trailing content")
	}
	return nil
}

// statusErr 将非成功响应转换为 StatusError，保留状态码与错误信封但不泄漏 URL。
func statusErr(resp *http.Response) error {
	limited := io.LimitReader(resp.Body, maxBodySize)
	raw, _ := io.ReadAll(limited)
	se := &StatusError{Code: resp.StatusCode, Body: string(raw)}
	var ae authError
	if json.Unmarshal(raw, &ae) == nil && ae.Error.Code != "" {
		se.AuthErr = &ae
	}
	return se
}

// ---------------------------------------------------------------------------
// Edge 生成类型映射辅助
// ---------------------------------------------------------------------------

// ToGeneratedAPIKey 将 Auth wire 密钥转换为 Edge 契约 ApiKey。
func ToGeneratedAPIKey(k authAPIKey) apiv1.ApiKey {
	return apiv1.ApiKey{
		Id:         parseUUID(k.ID),
		Name:       k.Name,
		KeyPrefix:  k.KeyPrefix,
		KeySuffix:  k.KeySuffix,
		Status:     apiv1.ApiKeyStatus(k.Status),
		LastUsedAt: k.LastUsedAt,
		ExpiresAt:  k.ExpiresAt,
		CreatedAt:  k.CreatedAt,
	}
}

// ToGeneratedAPIKeyCreated 将 Auth wire 密钥（含 secret）转换为 Edge 契约 ApiKeyCreated。
func ToGeneratedAPIKeyCreated(k authAPIKeyCreated) apiv1.ApiKeyCreated {
	return apiv1.ApiKeyCreated{
		Id:        parseUUID(k.ID),
		Name:      k.Name,
		KeyPrefix: k.KeyPrefix,
		KeySuffix: k.KeySuffix,
		Secret:    k.Secret,
		Status:    apiv1.ApiKeyCreatedStatus(k.Status),
		CreatedAt: k.CreatedAt,
	}
}

// parseUUID 安全解析，失败返回零值 UUID（避免单条坏数据中断列表）。
func parseUUID(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.UUID{}
	}
	return id
}
