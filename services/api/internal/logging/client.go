// Package logging 提供 Edge/BFF 调用 Logging Service 的 HTTP 客户端。
//
// Edge 不直连 Log DB，全部通过 Logging Service 的 HTTP API 获取请求日志。
// 客户端在 loggingURL 为空（dev/降级模式）时返回 ErrUnavailable，由上层
// handler 映射为 503；任何下游错误均归一为 ErrUnavailable，绝不泄漏 URL、
// 请求体或响应体。
package logging

import (
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
)

// ErrUnavailable 表示 Logging Service 不可达或返回错误，绝不携带 URL 或响应体。
var ErrUnavailable = errors.New("logging: service unavailable")

// RequestLog 对应 Logging Service 返回的单条请求日志摘要（snake_case JSON）。
// 字段与 services/logging/internal/repository.RequestLog 的 json tag 对齐。
type RequestLog struct {
	RequestID    string    `json:"request_id"`
	UserID       string    `json:"user_id,omitempty"`
	ModelName    string    `json:"model_name,omitempty"`
	ProviderID   string    `json:"provider_id,omitempty"`
	Protocol     string    `json:"protocol,omitempty"`
	Stream       bool      `json:"stream"`
	FinalStatus  string    `json:"final_status"`
	HTTPStatus   int       `json:"http_status,omitempty"`
	InputTokens  int       `json:"input_tokens,omitempty"`
	OutputTokens int       `json:"output_tokens,omitempty"`
	TotalTokens  int       `json:"total_tokens,omitempty"`
	LatencyMS    int       `json:"latency_ms,omitempty"`
	ErrorCode    string    `json:"error_code,omitempty"`
	ErrorType    string    `json:"error_type,omitempty"`
	BillingPlan  string    `json:"billing_plan,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// Attempt 对应 Logging Service 返回的单个 attempt。
type Attempt struct {
	RequestID     string    `json:"request_id"`
	AttemptIndex  int       `json:"attempt_index"`
	ProviderID    string    `json:"provider_id,omitempty"`
	UpstreamModel string    `json:"upstream_model,omitempty"`
	Status        string    `json:"status"`
	HTTPStatus    int       `json:"http_status,omitempty"`
	LatencyMS     int       `json:"latency_ms,omitempty"`
	ErrorCode     string    `json:"error_code,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// LogDetail 是 GET /v1/logs/{request_id} 的响应：日志摘要 + attempts。
type LogDetail struct {
	Log      RequestLog `json:"log"`
	Attempts []Attempt  `json:"attempts"`
}

// ListResult 是 GET /v1/logs 的分页响应。
type ListResult struct {
	Logs     []RequestLog `json:"logs"`
	Total    int          `json:"total"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
}

// ModelStat 是按模型聚合的用量统计。
type ModelStat struct {
	Model        string `json:"model"`
	Requests     int64  `json:"requests"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
}

// Stats 是 GET /v1/logs/stats 的响应。
type Stats struct {
	Days              int         `json:"days"`
	TotalRequests     int64       `json:"total_requests"`
	TotalInputTokens  int64       `json:"total_input_tokens"`
	TotalOutputTokens int64       `json:"total_output_tokens"`
	ByModel           []ModelStat `json:"by_model"`
}

// ListFilter 是 Edge 调用 Logging Service 列表端点时的过滤参数。
// Statuses 为空表示不限状态。
type ListFilter struct {
	UserID    string
	Model     string
	Statuses  []string
	StartTime time.Time
	EndTime   time.Time
	Page      int
	PageSize  int
}

// Client 调用 Logging Service。baseURL 为空时所有方法返回 ErrUnavailable。
type Client struct {
	httpClient *http.Client
	baseURL    string
}

// NewClient 创建 Logging Service 客户端。loggingURL 为空返回降级客户端。
func NewClient(loggingURL string) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		baseURL: strings.TrimSuffix(loggingURL, "/"),
	}
}

// Available 表示客户端是否配置了下游地址。
func (c *Client) Available() bool {
	return c != nil && c.baseURL != ""
}

// ListLogs 调用 GET /v1/logs 获取分页请求日志。
func (c *Client) ListLogs(ctx context.Context, filter ListFilter) (ListResult, error) {
	var out ListResult
	if !c.Available() {
		return out, ErrUnavailable
	}
	q := url.Values{}
	if filter.UserID != "" {
		q.Set("user_id", filter.UserID)
	}
	if filter.Model != "" {
		q.Set("model", filter.Model)
	}
	if n := len(filter.Statuses); n > 0 {
		q.Set("status", strings.Join(filter.Statuses, ","))
	}
	if !filter.StartTime.IsZero() {
		q.Set("start_date", filter.StartTime.UTC().Format(time.RFC3339))
	}
	if !filter.EndTime.IsZero() {
		q.Set("end_date", filter.EndTime.UTC().Format(time.RFC3339))
	}
	if filter.Page > 0 {
		q.Set("page", strconv.Itoa(filter.Page))
	}
	if filter.PageSize > 0 {
		q.Set("page_size", strconv.Itoa(filter.PageSize))
	}
	if err := c.get(ctx, "/v1/logs?"+q.Encode(), &out); err != nil {
		return out, err
	}
	if out.Logs == nil {
		out.Logs = []RequestLog{}
	}
	return out, nil
}

// GetLog 调用 GET /v1/logs/{request_id} 获取单个请求日志详情。
func (c *Client) GetLog(ctx context.Context, requestID string) (LogDetail, error) {
	var out LogDetail
	if !c.Available() {
		return out, ErrUnavailable
	}
	if err := c.get(ctx, "/v1/logs/"+url.PathEscape(requestID), &out); err != nil {
		return out, err
	}
	if out.Attempts == nil {
		out.Attempts = []Attempt{}
	}
	return out, nil
}

// GetStats 调用 GET /v1/logs/stats 获取用量统计。
func (c *Client) GetStats(ctx context.Context, userID string, days int) (Stats, error) {
	var out Stats
	if !c.Available() {
		return out, ErrUnavailable
	}
	q := url.Values{}
	if userID != "" {
		q.Set("user_id", userID)
	}
	if days > 0 {
		q.Set("days", strconv.Itoa(days))
	}
	if err := c.get(ctx, "/v1/logs/stats?"+q.Encode(), &out); err != nil {
		return out, err
	}
	if out.ByModel == nil {
		out.ByModel = []ModelStat{}
	}
	return out, nil
}

// NotFound 表示下游返回 404，便于上层区分「不存在」与「不可用」。
var NotFound = errors.New("logging: not found")

// get 执行 GET 请求并将响应解码到 dst。非 2xx 归一为 ErrUnavailable（404 为
// NotFound）。响应体限制 1 MiB，禁止重定向，不泄漏 URL/响应体到错误信息。
func (c *Client) get(ctx context.Context, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return ErrUnavailable
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		return NotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrUnavailable
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return ErrUnavailable
	}
	return nil
}

// String 仅供调试，绝不包含完整 URL。
func (c *Client) String() string {
	return fmt.Sprintf("logging.Client(base=%T)", c.httpClient)
}
