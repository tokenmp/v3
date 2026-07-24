// Package billing 提供 Edge/BFF 调用 Billing Service 查询端点（套餐/用户套餐/余额）的
// HTTP 客户端。
//
// 与 internal/quota 分离：quota 包负责 reserve/finalize/release 写入路径，本包负责
// 只读查询路径。Edge 不直连 Billing DB，全部经 Billing Service HTTP API。
// billingURL 为空时返回 ErrUnavailable（上层映射为 503）；任何下游错误均归一为
// ErrUnavailable，绝不泄漏 URL、请求体或响应体。
package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrUnavailable 表示 Billing Service 不可达或返回错误，绝不携带 URL 或响应体。
var ErrUnavailable = errors.New("billing: service unavailable")

// Plan 对应 Billing Service 返回的套餐定义（snake_case JSON）。金额/配额使用
// 字符串以保留精度（price 为 numeric(12,2)，token_limit 为 bigint）。
type Plan struct {
	ID            int64           `json:"id"`
	Name          string          `json:"name"`
	PlanType      string          `json:"plan_type"`
	Price         float64         `json:"price"`
	Category      string          `json:"category"`
	HourlyLimit   *int            `json:"hourly_limit,omitempty"`
	WeeklyLimit   *int            `json:"weekly_limit,omitempty"`
	MonthlyLimit  *int            `json:"monthly_limit,omitempty"`
	TokenLimit    *int64          `json:"token_limit,omitempty"`
	AllowedModels json.RawMessage `json:"allowed_models"`
	Status        string          `json:"status"`
}

// UserPlan 对应 Billing Service 返回的用户套餐绑定。
type UserPlan struct {
	ID          int64      `json:"id"`
	UserID      string     `json:"user_id"`
	PlanID      int64      `json:"plan_id"`
	PlanType    string     `json:"plan_type"`
	Status      string     `json:"status"`
	ActivatedAt time.Time  `json:"activated_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// Balance 对应 Billing Service 返回的用户余额（十进制字符串）。
type Balance struct {
	CodingRemaining string `json:"coding_remaining"`
	TokenRemaining  string `json:"token_remaining"`
}

// Client 调用 Billing Service 只读端点。baseURL 为空时返回降级客户端。
type Client struct {
	httpClient *http.Client
	baseURL    string
}

// NewClient 创建 Billing Service 查询客户端。billingURL 为空返回降级客户端。
func NewClient(billingURL string) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		baseURL: strings.TrimSuffix(billingURL, "/"),
	}
}

// Available 表示客户端是否配置了下游地址。
func (c *Client) Available() bool {
	return c != nil && c.baseURL != ""
}

// ListPlans 调用 GET /v1/billing/plans 获取可用套餐列表。
func (c *Client) ListPlans(ctx context.Context) ([]Plan, error) {
	var out struct {
		Plans []Plan `json:"plans"`
	}
	if err := c.get(ctx, "/v1/billing/plans", &out); err != nil {
		return nil, err
	}
	if out.Plans == nil {
		out.Plans = []Plan{}
	}
	return out.Plans, nil
}

// ListUserPlans 调用 GET /v1/billing/users/{user_id}/plan 获取用户当前生效套餐。
// Billing Service 当前返回单个 active plan（最新的）；本方法返回单元素切片以便
// 上层直接映射为 OpenAPI 列表响应。
func (c *Client) ListUserPlans(ctx context.Context, userID string) ([]UserPlan, error) {
	if userID == "" {
		return nil, ErrUnavailable
	}
	var plan UserPlan
	if err := c.get(ctx, "/v1/billing/users/"+url.PathEscape(userID)+"/plan", &plan); err != nil {
		return nil, err
	}
	return []UserPlan{plan}, nil
}

// GetBalance 调用 GET /v1/billing/users/{user_id}/balance 获取用户余额。
func (c *Client) GetBalance(ctx context.Context, userID string) (Balance, error) {
	var out Balance
	if userID == "" {
		return out, ErrUnavailable
	}
	if err := c.get(ctx, "/v1/billing/users/"+url.PathEscape(userID)+"/balance", &out); err != nil {
		return out, err
	}
	return out, nil
}

// NotFound 表示下游返回 404，便于上层区分「不存在」与「不可用」。
var NotFound = errors.New("billing: not found")

// get 执行 GET 请求并将响应解码到 dst。非 2xx 归一为 ErrUnavailable（404 为
// NotFound）。响应体限制 1 MiB，禁止重定向，不泄漏 URL/响应体到错误信息。
func (c *Client) get(ctx context.Context, path string, dst any) error {
	if !c.Available() {
		return ErrUnavailable
	}
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
	return fmt.Sprintf("billing.Client(base=%T)", c.httpClient)
}
