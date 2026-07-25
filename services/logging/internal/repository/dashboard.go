package repository

import (
	"context"
	"time"
)

// DashboardStats is the aggregated dashboard data for the admin overview.
type DashboardStats struct {
	TotalRequests    int64        `json:"totalRequests"`
	TodayRequests    int64        `json:"todayRequests"`
	TodaySuccess     int64        `json:"todaySuccess"`
	TodayActiveUsers int64        `json:"todayActiveUsers"`
	TodayTokens      int64        `json:"todayTokens"`
	TodayModelUsage  []ModelStat  `json:"todayModelUsage"`
	TodayTopUsers    []TopUserRow `json:"todayTopUsers"`
	Trend            []TrendRow   `json:"trend"`
}

// TopUserRow is a per-user aggregation for today's top users.
type TopUserRow struct {
	UserID       string `json:"user_id"`
	Requests     int64  `json:"requests"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	TotalTokens  int64  `json:"total_tokens"`
}

// TrendRow is one day of the request trend.
type TrendRow struct {
	Date         string `json:"date"`
	Requests     int64  `json:"requests"`
	Success      int64  `json:"success"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
}

// GetDashboardStats aggregates today's metrics, a 15-day trend, today's
// per-model usage, and today's top users. This is a cross-user (global)
// aggregation — no user_id filter.
func (r *GormRepository) GetDashboardStats(ctx context.Context, trendDays int) (DashboardStats, error) {
	if trendDays <= 0 {
		trendDays = 15
	}
	if trendDays > 90 {
		trendDays = 90
	}
	now := time.Now().UTC()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	trendStart := todayStart.AddDate(0, 0, -(trendDays - 1))

	var ds DashboardStats

	// totalRequests (all-time count)
	if err := r.db.WithContext(ctx).Model(&RequestLog{}).Count(&ds.TotalRequests).Error; err != nil {
		return DashboardStats{}, err
	}

	// today aggregates
	const todayQ = `SELECT
		COUNT(*) AS today_requests,
		COUNT(CASE WHEN final_status = 'success' THEN 1 END) AS today_success,
		COUNT(DISTINCT user_id) AS today_active_users,
		COALESCE(SUM(input_tokens + output_tokens), 0) AS today_tokens
		FROM request_logs
		WHERE created_at >= ?`
	row := r.db.WithContext(ctx).Raw(todayQ, todayStart).Row()
	if err := row.Scan(&ds.TodayRequests, &ds.TodaySuccess, &ds.TodayActiveUsers, &ds.TodayTokens); err != nil {
		return DashboardStats{}, err
	}

	// today model usage
	const modelQ = `SELECT
		COALESCE(NULLIF(resolved_model, ''), model_name) AS model,
		COUNT(*) AS requests,
		COALESCE(SUM(input_tokens), 0) AS input_tokens,
		COALESCE(SUM(output_tokens), 0) AS output_tokens
		FROM request_logs
		WHERE created_at >= ?
		GROUP BY model
		ORDER BY requests DESC`
	var models []ModelStat
	if err := r.db.WithContext(ctx).Raw(modelQ, todayStart).Scan(&models).Error; err != nil {
		return DashboardStats{}, err
	}
	if models == nil {
		models = []ModelStat{}
	}
	ds.TodayModelUsage = models

	// today top users
	const topUsersQ = `SELECT
		user_id,
		COUNT(*) AS requests,
		COALESCE(SUM(input_tokens), 0) AS input_tokens,
		COALESCE(SUM(output_tokens), 0) AS output_tokens,
		COALESCE(SUM(input_tokens + output_tokens), 0) AS total_tokens
		FROM request_logs
		WHERE created_at >= ? AND user_id <> ''
		GROUP BY user_id
		ORDER BY total_tokens DESC
		LIMIT 10`
	var topUsers []TopUserRow
	if err := r.db.WithContext(ctx).Raw(topUsersQ, todayStart).Scan(&topUsers).Error; err != nil {
		return DashboardStats{}, err
	}
	if topUsers == nil {
		topUsers = []TopUserRow{}
	}
	ds.TodayTopUsers = topUsers

	// trend (N days)
	const trendQ = `SELECT
		TO_CHAR(DATE(created_at), 'YYYY-MM-DD') AS date,
		COUNT(*) AS requests,
		COUNT(CASE WHEN final_status = 'success' THEN 1 END) AS success,
		COALESCE(SUM(input_tokens), 0) AS input_tokens,
		COALESCE(SUM(output_tokens), 0) AS output_tokens
		FROM request_logs
		WHERE created_at >= ?
		GROUP BY DATE(created_at)
		ORDER BY date ASC`
	var trend []TrendRow
	if err := r.db.WithContext(ctx).Raw(trendQ, trendStart).Scan(&trend).Error; err != nil {
		return DashboardStats{}, err
	}
	if trend == nil {
		trend = []TrendRow{}
	}
	ds.Trend = trend

	return ds, nil
}
