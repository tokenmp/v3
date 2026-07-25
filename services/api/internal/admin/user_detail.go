package admin

import (
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
	ID             string     `json:"id"`
	PlanID         string     `json:"planId"`
	PlanType       string     `json:"planType"`
	TotalQuota     string     `json:"totalQuota"`
	RemainingQuota string     `json:"remainingQuota"`
	Status         string     `json:"status"`
	ActivatedAt    time.Time  `json:"activatedAt"`
	ExpiresAt      *time.Time `json:"expiresAt,omitempty"`
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

	// 2. API Keys from Auth — list all and filter by user_id.
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
			}
		}
	}

	// 3. User plans from Billing.
	if h.Billing != nil && h.Billing.Available() {
		if plans, err := h.Billing.ListUserPlans(r.Context(), userID); err == nil {
			// Try to get balance for remaining quota.
			var balance *billing.Balance
			if b, bErr := h.Billing.GetBalance(r.Context(), userID); bErr == nil {
				balance = &b
			}
			for _, p := range plans {
				var remaining string
				if balance != nil {
					remaining = balance.TokenRemaining
				}
				resp.UserPlans = append(resp.UserPlans, userPlanResp{
					ID:             formatInt64(p.ID),
					PlanID:         formatInt64(p.PlanID),
					PlanType:       p.PlanType,
					TotalQuota:     "", // Billing doesn't return plan token_limit here
					RemainingQuota: remaining,
					Status:         p.Status,
					ActivatedAt:    p.ActivatedAt,
					ExpiresAt:      p.ExpiresAt,
				})
			}
		}
	}

	// 4. Recent requests from Logging.
	if h.Logging != nil && h.Logging.Available() {
		logsResult, err := h.Logging.ListLogs(r.Context(), logging.ListFilter{
			UserID:   userID,
			Page:     1,
			PageSize: 10,
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
