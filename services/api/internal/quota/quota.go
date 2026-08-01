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
	"net/url"
	"strings"
	"time"

	"github.com/tokenmp/v3/packages/go/httpresp"
)

// Reservation holds the result of a successful Reserve call.
type Reservation struct {
	ReservationID string
	Status        string
}

// ErrQuotaUnavailable indicates the Billing Service could not be reached or
// returned an error. It never embeds the URL, request body, or response body.
var ErrQuotaUnavailable = errors.New("quota: billing service unavailable")

// ErrQuotaExceeded indicates Billing rejected the request with a quota limit
// breach (HTTP 429). It is distinct from ErrQuotaUnavailable so Edge can
// return a client-visible 429 instead of a service 503.
var ErrQuotaExceeded = errors.New("quota: exceeded")

// ErrNotFound indicates Billing returned 404 for a reservation.
var ErrNotFound = errors.New("quota: not found")

// Manager coordinates the reserve-finalize-release lifecycle.
type Manager interface {
	// Reserve creates a quota reservation for the given user/request.
	Reserve(ctx context.Context, reservationID, userID, requestID, billingPlan string, reservedReqs int, reservedTokens int64) (Reservation, error)
	// Finalize settles a reservation with confirmed usage. usageKnown must be
	// true; the caller must MarkPending when usage is unknown.
	Finalize(ctx context.Context, reservationID string, finalReqs int, finalTokens int64, usageKnown bool) error
	// Release cancels a reservation (failure/cancel).
	Release(ctx context.Context, reservationID string) error
	// MarkPending parks a committed-but-unsettled reservation when usage is
	// unknown or Billing was temporarily unavailable at finalize time. The
	// background reconciler resolves it; Edge never guesses a token count.
	MarkPending(ctx context.Context, reservationID string) error
	// GetStatus returns the safe settlement status of a reservation so the
	// Edge coordinator can decide the terminal action without polling logs.
	// Returns ErrQuotaUnavailable when Billing is unreachable.
	GetStatus(ctx context.Context, reservationID string) (ReservationStatus, error)
}

// ReservationStatus is the safe settlement projection returned by GetStatus.
type ReservationStatus struct {
	ReservationID    string     `json:"reservation_id"`
	UserID           string     `json:"user_id"`
	RequestID        string     `json:"request_id"`
	BillingPlan      string     `json:"billing_plan"`
	Status           string     `json:"status"`
	SettlementStatus string     `json:"settlement_status,omitempty"`
	ReservedRequests *int       `json:"reserved_requests,omitempty"`
	ReservedTokens   *int64     `json:"reserved_tokens,omitempty"`
	FinalRequests    *int       `json:"final_requests,omitempty"`
	FinalTokens      *int64     `json:"final_tokens,omitempty"`
	UsageKnown       bool       `json:"usage_known"`
	ReservedAt       time.Time  `json:"reserved_at"`
	FinalizedAt      *time.Time `json:"finalized_at,omitempty"`
	ReconciledAt     *time.Time `json:"reconciled_at,omitempty"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
}

// ErrConflict indicates Billing returned a stable conflict (the reservation
// is in a conflicting terminal state, or the finalize payload differs from
// the settled one). It is distinct from unavailability so Edge can treat a
// 409 as “already settled” rather than retrying.
var ErrConflict = errors.New("quota: conflicting state")

// noopManager skips all billing calls. Used when API_BILLING_URL is unset
// (dev/degraded mode).
type noopManager struct{}

func (noopManager) Reserve(_ context.Context, _, _, _, _ string, _ int, _ int64) (Reservation, error) {
	return Reservation{Status: "reserved"}, nil
}
func (noopManager) Finalize(_ context.Context, _ string, _ int, _ int64, _ bool) error { return nil }
func (noopManager) Release(_ context.Context, _ string) error                          { return nil }
func (noopManager) MarkPending(_ context.Context, _ string) error                      { return nil }
func (noopManager) GetStatus(_ context.Context, _ string) (ReservationStatus, error) {
	return ReservationStatus{}, ErrQuotaUnavailable
}

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
	var result struct {
		ReservationID string `json:"reservation_id"`
		Status        string `json:"status"`
	}
	if err := m.postDecode(ctx, "/v1/billing/quota/reserve", body, &result); err != nil {
		return Reservation{}, err
	}
	return Reservation{ReservationID: result.ReservationID, Status: result.Status}, nil
}

func (m *billingManager) Finalize(ctx context.Context, reservationID string, finalReqs int, finalTokens int64, usageKnown bool) error {
	body := map[string]any{
		"reservation_id": reservationID,
		"final_requests": finalReqs,
		"final_tokens":   finalTokens,
		"usage_known":    usageKnown,
	}
	return m.postDecode(ctx, "/v1/billing/quota/finalize", body, nil)
}

func (m *billingManager) Release(ctx context.Context, reservationID string) error {
	body := map[string]any{
		"reservation_id": reservationID,
	}
	return m.postDecode(ctx, "/v1/billing/quota/release", body, nil)
}

func (m *billingManager) MarkPending(ctx context.Context, reservationID string) error {
	body := map[string]any{"reservation_id": reservationID}
	return m.postDecode(ctx, "/v1/billing/quota/mark-pending", body, nil)
}

func (m *billingManager) GetStatus(ctx context.Context, reservationID string) (ReservationStatus, error) {
	var out ReservationStatus
	if err := m.get(ctx, "/v1/billing/quota/reservations/"+url.PathEscape(reservationID), &out); err != nil {
		return ReservationStatus{}, err
	}
	return out, nil
}

// postDecode performs a POST, classifies the HTTP status, and — for 2xx
// responses that carry a body — unwraps the {code,data,message} envelope via
// httpresp.UnwrapData into dst. Only HTTP 204 No Content is allowed to
// succeed with no body. Any other 2xx must read successfully, be non-empty,
// and unwrap/decode to a valid envelope (even when dst is nil the envelope is
// validated). Malformed envelopes, non-zero codes, shape mismatches, body
// read errors, or unexpected empty bodies map to ErrQuotaUnavailable and
// never leak the URL or body.
func (m *billingManager) postDecode(ctx context.Context, path string, body any, dst any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return ErrQuotaUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return ErrQuotaUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return ErrQuotaUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	return decodeEnvelope(resp, dst)
}

// get performs a GET, classifies the HTTP status, and unwraps the
// {code,data,message} envelope via httpresp.UnwrapData into dst for 2xx
// responses with a body. Only HTTP 204 No Content is allowed to succeed with
// no body. 404 → ErrNotFound, other non-2xx → ErrQuotaUnavailable. Any other
// 2xx must read successfully, be non-empty, and unwrap/decode to a valid
// envelope; malformed envelopes, non-zero codes, shape mismatches, body read
// errors, or unexpected empty bodies map to ErrQuotaUnavailable and never
// leak the URL or body.
func (m *billingManager) get(ctx context.Context, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.baseURL+path, nil)
	if err != nil {
		return ErrQuotaUnavailable
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return ErrQuotaUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	return decodeEnvelope(resp, dst)
}

// decodeEnvelope classifies the HTTP status of an already-received Billing
// response and unwraps its {code,data,message} envelope into dst. Only HTTP
// 204 No Content is allowed to succeed with no body; any other 2xx must read
// successfully, be non-empty, and unwrap/decode to a valid envelope (the
// envelope is validated even when dst is nil). Body read errors, malformed
// envelopes, non-zero codes, shape mismatches, or unexpected empty bodies map
// to ErrQuotaUnavailable and never leak the URL or body.
func decodeEnvelope(resp *http.Response, dst any) error {
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		return ErrQuotaExceeded
	case http.StatusConflict:
		return ErrConflict
	case http.StatusNotFound:
		return ErrNotFound
	}
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrQuotaUnavailable
	}
	// Any other 2xx must carry a non-empty, decodable envelope.
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return ErrQuotaUnavailable
	}
	if len(data) == 0 {
		return ErrQuotaUnavailable
	}
	target := dst
	if target == nil {
		var throwaway json.RawMessage
		target = &throwaway
	}
	if err := httpresp.UnwrapData(data, target); err != nil {
		return ErrQuotaUnavailable
	}
	return nil
}

// String returns a description for debugging. It never includes the URL.
func (m *billingManager) String() string {
	return fmt.Sprintf("quota.billingManager(base=%T)", m.client)
}
