// Package quota manages the reserve-finalize-release lifecycle for requests
// passing through the Edge/BFF. It calls the Billing Service HTTP API to
// reserve quota before forwarding to the executor, finalize on success, and
// release on failure.
package quota

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
)

// Reservation holds the result of a successful Reserve call.
type Reservation struct {
	ReservationID string
	Status        string
}

// ErrQuotaUnavailable indicates the Billing Service could not be reached or
// returned an error. It never embeds the URL, request body, or response body.
var ErrQuotaUnavailable = errors.New("quota: billing service unavailable")

// Manager coordinates the reserve-finalize-release lifecycle.
type Manager interface {
	// Reserve creates a quota reservation for the given user/request.
	Reserve(ctx context.Context, reservationID, userID, requestID, billingPlan string, reservedReqs int, reservedTokens int64) (Reservation, error)
	// Finalize settles a reservation with the actual usage.
	Finalize(ctx context.Context, reservationID string, finalReqs int, finalTokens int64) error
	// Release cancels a reservation (failure/cancel).
	Release(ctx context.Context, reservationID string) error
}

// noopManager skips all billing calls. Used when API_BILLING_URL is unset
// (dev/degraded mode).
type noopManager struct{}

func (noopManager) Reserve(_ context.Context, _, _, _, _ string, _ int, _ int64) (Reservation, error) {
	return Reservation{Status: "reserved"}, nil
}
func (noopManager) Finalize(_ context.Context, _ string, _ int, _ int64) error { return nil }
func (noopManager) Release(_ context.Context, _ string) error                  { return nil }

// billingManager calls the Billing Service HTTP API.
type billingManager struct {
	client  *http.Client
	baseURL string // e.g. "http://127.0.0.1:8085"
}

// NewManager creates a quota Manager. If billingURL is empty, a noop manager
// is returned (dev-only; production must set a URL).
func NewManager(billingURL string) Manager {
	if billingURL == "" {
		return noopManager{}
	}
	return &billingManager{
		client:  &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		baseURL: strings.TrimSuffix(billingURL, "/"),
	}
}

func (m *billingManager) Reserve(ctx context.Context, reservationID, userID, requestID, billingPlan string, reservedReqs int, reservedTokens int64) (Reservation, error) {
	body := map[string]any{
		"reservation_id":    reservationID,
		"user_id":           userID,
		"request_id":        requestID,
		"billing_plan":      billingPlan,
		"reserved_requests": reservedReqs,
		"reserved_tokens":   reservedTokens,
	}
	resp, err := m.post(ctx, "/v1/billing/quota/reserve", body)
	if err != nil {
		return Reservation{}, err
	}
	var result struct {
		ReservationID string `json:"reservation_id"`
		Status        string `json:"status"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return Reservation{}, ErrQuotaUnavailable
	}
	return Reservation{ReservationID: result.ReservationID, Status: result.Status}, nil
}

func (m *billingManager) Finalize(ctx context.Context, reservationID string, finalReqs int, finalTokens int64) error {
	body := map[string]any{
		"reservation_id": reservationID,
		"final_requests": finalReqs,
		"final_tokens":   finalTokens,
	}
	_, err := m.post(ctx, "/v1/billing/quota/finalize", body)
	return err
}

func (m *billingManager) Release(ctx context.Context, reservationID string) error {
	body := map[string]any{
		"reservation_id": reservationID,
	}
	_, err := m.post(ctx, "/v1/billing/quota/release", body)
	return err
}

func (m *billingManager) post(ctx context.Context, path string, body any) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, ErrQuotaUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, ErrQuotaUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, ErrQuotaUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ErrQuotaUnavailable
	}
	return data, nil
}

// String returns a description for debugging. It never includes the URL.
func (m *billingManager) String() string {
	return fmt.Sprintf("quota.billingManager(base=%T)", m.client)
}
