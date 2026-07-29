package admin

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/tokenmp/v3/packages/go/httpresp"
	"github.com/tokenmp/v3/services/api/internal/billing"
	"github.com/tokenmp/v3/services/api/internal/logging"
)

// userDetailResponse is the aggregated user detail returned to the frontend.
// JSON tags use camelCase to match the frontend's AdminUserDetail type.
type userDetailResponse struct {
	ID             string           `json:"id"`
	Email          string           `json:"email"`
	Role           string           `json:"role"`
	Status         string           `json:"status"`
	CreatedAt      time.Time        `json:"createdAt"`
	APIKeys        []apiKeyResp     `json:"apiKeys"`
	UserPlans      []userPlanResp   `json:"userPlans"`
	RecentRequests []requestLogResp `json:"recentRequests"`
	TotalRequests  int              `json:"totalRequests"`
}

type apiKeyResp struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	KeyPrefix  string     `json:"keyPrefix"`
	KeySuffix  string     `json:"keySuffix"`
	Status     string     `json:"status"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
}

type userPlanResp struct {
	ID             string            `json:"id"`
	PlanID         string            `json:"planId"`
	PlanName       string            `json:"planName,omitempty"`
	PlanType       string            `json:"planType"`
	Category       string            `json:"category,omitempty"`
	Price          *float64          `json:"price,omitempty"`
	HourlyLimit    *int              `json:"hourlyLimit,omitempty"`
	WeeklyLimit    *int              `json:"weeklyLimit,omitempty"`
	MonthlyLimit   *int              `json:"monthlyLimit,omitempty"`
	TokenLimit     *string           `json:"tokenLimit,omitempty"`
	TotalQuota     string            `json:"totalQuota"`
	RemainingQuota string            `json:"remainingQuota"`
	Status         string            `json:"status"`
	ActivatedAt    time.Time         `json:"activatedAt"`
	ExpiresAt      *time.Time        `json:"expiresAt,omitempty"`
	UsageWindows   []usageWindowResp `json:"usageWindows,omitempty"`
}

type usageWindowResp struct {
	Scope       string     `json:"scope"`
	Limit       *int       `json:"limit"`
	Consumed    int        `json:"consumed"`
	Remaining   int        `json:"remaining"`
	WindowStart time.Time  `json:"windowStart"`
	WindowEnd   *time.Time `json:"windowEnd"`
}

type requestLogResp struct {
	RequestID    string `json:"requestId"`
	Model        string `json:"model"`
	Status       string `json:"status"`
	InputTokens  *int   `json:"inputTokens,omitempty"`
	OutputTokens *int   `json:"outputTokens,omitempty"`
	Cost         string `json:"cost,omitempty"`
	DurationMs   *int   `json:"durationMs,omitempty"`
	CreatedAt    string `json:"createdAt"`
}

// adminGetUserDetail aggregates user data from Auth + Billing + Logging and
// returns a camelCase response matching the frontend's AdminUserDetail type.
// Missing downstream data degrades gracefully to empty slices / zero values
// rather than failing the entire request.
func (h *Handlers) adminGetUserDetail(w http.ResponseWriter, r *http.Request, bearer, userID string) {
	// 1. Basic user info from Auth (required — if this fails, we can't proceed).
	authUser, err := h.Auth.GetUser(r.Context(), bearer, userID)
	if err != nil {
		h.handleAuthErr(w, err)
		return
	}

	resp := userDetailResponse{
		ID:             authUser.ID,
		Email:          authUser.Email,
		Role:           authUser.Role,
		Status:         authUser.Status,
		CreatedAt:      authUser.CreatedAt,
		APIKeys:        []apiKeyResp{},
		UserPlans:      []userPlanResp{},
		RecentRequests: []requestLogResp{},
	}

	// 2. API Keys from Auth — list and return at most 5 for the detail summary.
	if keysResult, err := h.Auth.ListKeys(r.Context(), bearer, 1, 100); err == nil {
		for _, k := range keysResult.Keys {
			if k.UserID == userID {
				resp.APIKeys = append(resp.APIKeys, apiKeyResp{
					ID:         k.ID,
					Name:       k.Name,
					KeyPrefix:  k.KeyPrefix,
					KeySuffix:  k.KeySuffix,
					Status:     k.Status,
					LastUsedAt: k.LastUsedAt,
					ExpiresAt:  k.ExpiresAt,
					CreatedAt:  k.CreatedAt,
				})
				if len(resp.APIKeys) >= 5 {
					break
				}
			}
		}
	}

	// 3. User plans from Billing.
	if h.Billing != nil && h.Billing.Available() {
		if plans, err := h.Billing.ListUserPlans(r.Context(), userID); err == nil {
			var balance *billing.Balance
			if b, bErr := h.Billing.GetBalance(r.Context(), userID); bErr == nil {
				balance = &b
			}
			windows, winErr := h.Billing.GetUsageWindows(r.Context(), userID)
			for _, p := range plans {
				resp.UserPlans = append(resp.UserPlans, mapAdminUserPlanResp(p, balance, windows, winErr))
			}
		}
	}

	// 4. Recent requests from Logging.
	if h.Logging != nil && h.Logging.Available() {
		logsResult, err := h.Logging.ListLogs(r.Context(), logging.ListFilter{
			UserID:   userID,
			Page:     1,
			PageSize: 5,
		})
		if err == nil {
			resp.TotalRequests = logsResult.Total
			for _, lg := range logsResult.Logs {
				resp.RecentRequests = append(resp.RecentRequests, mapLogToResp(lg))
			}
		}
	}

	httpresp.OK(w, resp)
}

func mapAdminUserPlanResp(p billing.UserPlan, balance *billing.Balance, windows []billing.UsageWindow, winErr error) userPlanResp {
	remaining := "0"
	if balance != nil {
		if p.PlanType == "token" {
			remaining = balance.TokenRemaining
		} else {
			remaining = balance.CodingRemaining
		}
	}
	totalQuota := "0"
	if p.PlanType == "token" && p.TokenLimit != nil {
		totalQuota = strconv.FormatInt(*p.TokenLimit, 10)
	} else if p.MonthlyLimit != nil {
		totalQuota = strconv.Itoa(*p.MonthlyLimit)
	}
	out := userPlanResp{
		ID:             formatInt64(p.ID),
		PlanID:         formatInt64(p.PlanID),
		PlanName:       p.PlanName,
		PlanType:       p.PlanType,
		Category:       p.Category,
		Price:          floatPtr(p.Price),
		HourlyLimit:    p.HourlyLimit,
		WeeklyLimit:    p.WeeklyLimit,
		MonthlyLimit:   p.MonthlyLimit,
		TokenLimit:     int64StringPtr(p.TokenLimit),
		TotalQuota:     totalQuota,
		RemainingQuota: remaining,
		Status:         p.Status,
		ActivatedAt:    p.ActivatedAt,
		ExpiresAt:      p.ExpiresAt,
	}
	if p.PlanType == "coding" && winErr == nil && len(windows) > 0 {
		out.UsageWindows = make([]usageWindowResp, 0, len(windows))
		for _, w := range windows {
			out.UsageWindows = append(out.UsageWindows, usageWindowResp{
				Scope:       w.Scope,
				Limit:       w.Limit,
				Consumed:    w.Consumed,
				Remaining:   w.Remaining,
				WindowStart: w.WindowStart,
				WindowEnd:   w.WindowEnd,
			})
		}
	} else if errors.Is(winErr, billing.NotFound) {
		out.UsageWindows = nil
	}
	return out
}

func floatPtr(v float64) *float64 {
	if v == 0 {
		return nil
	}
	return &v
}

func int64StringPtr(v *int64) *string {
	if v == nil {
		return nil
	}
	s := strconv.FormatInt(*v, 10)
	return &s
}

func mapLogToResp(lg logging.RequestLog) requestLogResp {
	out := requestLogResp{
		RequestID: lg.RequestID,
		Model:     lg.ModelName,
		Status:    lg.FinalStatus,
		CreatedAt: lg.CreatedAt.Format(time.RFC3339),
	}
	if lg.InputTokens > 0 {
		v := lg.InputTokens
		out.InputTokens = &v
	}
	if lg.OutputTokens > 0 {
		v := lg.OutputTokens
		out.OutputTokens = &v
	}
	if lg.LatencyMS > 0 {
		v := lg.LatencyMS
		out.DurationMs = &v
	}
	return out
}

func formatInt64(n int64) string {
	return strconv.FormatInt(n, 10)
}
