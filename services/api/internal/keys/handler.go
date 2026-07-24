// Package keys 同时包含 Edge 侧 HTTP handler：它实现生成的 apiv1.ServerInterface
// 中 6 个密钥管理方法，将请求代理到 Auth Service，并在 Auth 的 snake_case wire
// 格式与 Edge 的 camelCase 契约类型之间转换。
//
// 本文件仅注册 /api/v1/keys* 路由；其它契约端点（plans/balance/logs/settings）
// 仍由 apiv1.Unimplemented 返回 501，不在本次实现范围。
package keys

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/tokenmp/v3/services/api/internal/contract/apiv1"
)

// maxRequestBody 限制 Edge 侧请求体大小。
const maxRequestBody = 1 << 20 // 1 MiB

// Handler 实现 /api/v1/keys* 的 Edge 端点。它嵌入 apiv1.Unimplemented 以满足
// 完整 ServerInterface，但仅 keys 方法被实际注册与调用。
type Handler struct {
	apiv1.Unimplemented
	client *Client
	logger *slog.Logger
}

// NewHandler 构造 Handler。client 为 nil 表示未配置 AuthURL，密钥端点返回 503。
func NewHandler(client *Client, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{client: client, logger: logger}
}

// Routes 在给定 chi Router 上注册 /api/v1/keys* 路由。调用方负责套用身份
// 中间件（这些端点需要鉴权但不走配额）。
func (h *Handler) Routes(r chi.Router) {
	r.Get("/api/v1/keys", h.ListApiKeys)
	r.Post("/api/v1/keys", h.CreateApiKey)
	r.Get("/api/v1/keys/{keyId}", h.GetApiKey)
	r.Patch("/api/v1/keys/{keyId}", h.UpdateApiKey)
	r.Delete("/api/v1/keys/{keyId}", h.DeleteApiKey)
	r.Post("/api/v1/keys/{keyId}/rotate", h.RotateApiKey)
}

// ---------------------------------------------------------------------------
// 端点实现
// ---------------------------------------------------------------------------

// ListApiKeys GET /api/v1/keys
func (h *Handler) ListApiKeys(w http.ResponseWriter, r *http.Request) {
	bearer := bearerFromRequest(r)
	if h.unavailable(w) {
		return
	}
	res, err := h.client.List(r.Context(), bearer)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	out := make([]apiv1.ApiKey, 0, len(res.Keys))
	for _, k := range res.Keys {
		out = append(out, ToGeneratedAPIKey(k))
	}
	_ = apiv1.ListApiKeys200JSONResponse{Keys: out}.VisitListApiKeysResponse(w)
}

// CreateApiKey POST /api/v1/keys
func (h *Handler) CreateApiKey(w http.ResponseWriter, r *http.Request) {
	bearer := bearerFromRequest(r)
	if h.unavailable(w) {
		return
	}
	var body apiv1.CreateApiKeyJSONRequestBody
	if !decodeBody(w, r, &body) {
		return
	}
	var name string
	if body.Name != nil {
		name = *body.Name
	}
	res, err := h.client.Create(r.Context(), bearer, name, body.ExpiresAt)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	if res.Created == nil {
		h.writeErr(w, ErrAuthUnavailable)
		return
	}
	_ = apiv1.CreateApiKey201JSONResponse{Key: ToGeneratedAPIKeyCreated(*res.Created)}.VisitCreateApiKeyResponse(w)
}

// GetApiKey GET /api/v1/keys/{keyId}
func (h *Handler) GetApiKey(w http.ResponseWriter, r *http.Request) {
	bearer := bearerFromRequest(r)
	keyID, ok := keyIDFromRequest(w, r)
	if !ok {
		return
	}
	if h.unavailable(w) {
		return
	}
	res, err := h.client.Get(r.Context(), bearer, keyID)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	if res.Key == nil {
		h.writeErr(w, ErrAuthUnavailable)
		return
	}
	_ = apiv1.GetApiKey200JSONResponse{Key: ToGeneratedAPIKey(*res.Key)}.VisitGetApiKeyResponse(w)
}

// UpdateApiKey PATCH /api/v1/keys/{keyId}
func (h *Handler) UpdateApiKey(w http.ResponseWriter, r *http.Request) {
	bearer := bearerFromRequest(r)
	keyID, ok := keyIDFromRequest(w, r)
	if !ok {
		return
	}
	if h.unavailable(w) {
		return
	}
	var body apiv1.UpdateApiKeyJSONRequestBody
	if !decodeBody(w, r, &body) {
		return
	}
	var status *string
	if body.Status != nil {
		s := string(*body.Status)
		status = &s
	}
	res, err := h.client.Update(r.Context(), bearer, keyID, body.Name, status)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	if res.Key == nil {
		h.writeErr(w, ErrAuthUnavailable)
		return
	}
	_ = apiv1.UpdateApiKey200JSONResponse{Key: ToGeneratedAPIKey(*res.Key)}.VisitUpdateApiKeyResponse(w)
}

// DeleteApiKey DELETE /api/v1/keys/{keyId}
func (h *Handler) DeleteApiKey(w http.ResponseWriter, r *http.Request) {
	bearer := bearerFromRequest(r)
	keyID, ok := keyIDFromRequest(w, r)
	if !ok {
		return
	}
	if h.unavailable(w) {
		return
	}
	res, err := h.client.Delete(r.Context(), bearer, keyID)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	if !res.NoContent {
		h.writeErr(w, ErrAuthUnavailable)
		return
	}
	_ = apiv1.DeleteApiKey204Response{}.VisitDeleteApiKeyResponse(w)
}

// RotateApiKey POST /api/v1/keys/{keyId}/rotate
func (h *Handler) RotateApiKey(w http.ResponseWriter, r *http.Request) {
	bearer := bearerFromRequest(r)
	keyID, ok := keyIDFromRequest(w, r)
	if !ok {
		return
	}
	if h.unavailable(w) {
		return
	}
	res, err := h.client.Rotate(r.Context(), bearer, keyID)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	if res.Created == nil {
		h.writeErr(w, ErrAuthUnavailable)
		return
	}
	_ = apiv1.RotateApiKey200JSONResponse{Key: ToGeneratedAPIKeyCreated(*res.Created)}.VisitRotateApiKeyResponse(w)
}

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

// unavailable 在未配置 AuthURL 时返回 503。
func (h *Handler) unavailable(w http.ResponseWriter) bool {
	if h.client != nil {
		return false
	}
	writeError(w, http.StatusServiceUnavailable, "auth_unavailable", "Auth service is not configured")
	return true
}

// writeErr 把 Auth 调用错误映射为 Edge 响应。
func (h *Handler) writeErr(w http.ResponseWriter, err error) {
	var se *StatusError
	switch {
	case errors.Is(err, ErrAuthUnavailable):
		writeError(w, http.StatusServiceUnavailable, "auth_unavailable", "Auth service is unavailable")
	case errors.As(err, &se):
		h.writeStatusErr(w, se)
	default:
		h.logger.Error("keys client unexpected error", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
	}
}

// writeStatusErr 把 Auth 返回的状态码映射为 Edge 状态码，保留错误信封但不
// 泄漏 Auth 的内部细节。Auth 401→Edge 401，404→404，400→400，其余→502。
func (h *Handler) writeStatusErr(w http.ResponseWriter, se *StatusError) {
	switch se.Code {
	case http.StatusUnauthorized:
		code := "unauthorized"
		msg := "authentication required"
		if se.AuthErr != nil && se.AuthErr.Error.Message != "" {
			msg = se.AuthErr.Error.Message
		}
		writeError(w, http.StatusUnauthorized, code, msg)
	case http.StatusNotFound:
		code := "not_found"
		msg := "api key not found"
		if se.AuthErr != nil && se.AuthErr.Error.Message != "" {
			msg = se.AuthErr.Error.Message
		}
		writeError(w, http.StatusNotFound, code, msg)
	case http.StatusBadRequest:
		code := "bad_request"
		msg := "invalid request"
		if se.AuthErr != nil && se.AuthErr.Error.Message != "" {
			msg = se.AuthErr.Error.Message
		}
		writeError(w, http.StatusBadRequest, code, msg)
	default:
		h.logger.Error("auth keys upstream error", "status", se.Code)
		writeError(w, http.StatusBadGateway, "auth_error", "Auth service returned an error")
	}
}

// bearerFromRequest 提取 Authorization: Bearer <token>。
func bearerFromRequest(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

// keyIDFromRequest 解析路径参数 keyId 为 UUID 字符串；非法返回 400。
func keyIDFromRequest(w http.ResponseWriter, r *http.Request) (string, bool) {
	raw := chi.URLParam(r, "keyId")
	if _, err := uuid.Parse(raw); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "keyId must be a valid UUID")
		return "", false
	}
	return raw, true
}

// decodeBody 在大小限制内解码 JSON；失败返回 400。
func decodeBody(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "request body is not valid JSON")
		return false
	}
	// 拒绝 trailing 内容。
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "bad_request", "request body is not valid JSON")
		return false
	}
	return true
}

// writeError 写入 Edge 契约的 Error 信封。
func writeError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	code := errType
	_ = json.NewEncoder(w).Encode(apiv1.Error{
		Error: struct {
			Code    *string `json:"code,omitempty"`
			Message string  `json:"message"`
			Param   *string `json:"param,omitempty"`
			Type    string  `json:"type"`
		}{
			Code:    &code,
			Message: message,
			Type:    "error",
		},
	})
}
