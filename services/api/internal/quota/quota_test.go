package quota

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tokenmp/v3/packages/go/httpresp"
)

// envelopeErrBody is a non-zero-code envelope body used to prove that error
// bodies are never leaked through the stable sentinel errors.
const envelopeErrBody = `{"code":1011,"data":null,"message":"reserve failed: internal explosion at postgres://secret:5432"}`

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encode: %v", err)
	}
}

func TestNoopManager(t *testing.T) {
	m := NewManager("")
	r, err := m.Reserve(context.Background(), "r1", "u1", "req1", "coding", 1, 100)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if r.Status != "reserved" {
		t.Errorf("Status = %q", r.Status)
	}
	if err := m.Finalize(context.Background(), "r1", 1, 80, true); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if err := m.Release(context.Background(), "r1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

// TestBillingManagerReserveFinalizeRelease uses a real httpresp.OK envelope so
// the quota manager must unwrap {code,data,message} before decoding the DTO.
func TestBillingManagerReserveFinalizeRelease(t *testing.T) {
	var reserveHits, finalizeHits, releaseHits, markPendingHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/v1/billing/quota/reserve":
			reserveHits.Add(1)
			if body["reservation_id"] != "r1" || body["user_id"] != "u1" {
				t.Errorf("unexpected reserve body: %+v", body)
			}
			httpresp.OK(w, map[string]string{"reservation_id": "r1", "status": "reserved"})
		case "/v1/billing/quota/finalize":
			finalizeHits.Add(1)
			httpresp.OK(w, map[string]string{"status": "finalized"})
		case "/v1/billing/quota/release":
			releaseHits.Add(1)
			httpresp.OK(w, map[string]string{"status": "released"})
		case "/v1/billing/quota/mark-pending":
			markPendingHits.Add(1)
			httpresp.OK(w, map[string]string{"status": "pending_reconciliation"})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	m := NewManager(srv.URL)
	r, err := m.Reserve(context.Background(), "r1", "u1", "req1", "coding", 1, 100)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if r.ReservationID != "r1" || r.Status != "reserved" {
		t.Errorf("Reservation = %+v (envelope not unwrapped?)", r)
	}
	if err := m.Finalize(context.Background(), "r1", 1, 80, true); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if err := m.Release(context.Background(), "r1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := m.MarkPending(context.Background(), "r1"); err != nil {
		t.Fatalf("MarkPending: %v", err)
	}
	if reserveHits.Load() != 1 || finalizeHits.Load() != 1 || releaseHits.Load() != 1 || markPendingHits.Load() != 1 {
		t.Errorf("hits: reserve=%d finalize=%d release=%d markPending=%d",
			reserveHits.Load(), finalizeHits.Load(), releaseHits.Load(), markPendingHits.Load())
	}
}

// TestBillingManagerGetStatus verifies GetStatus unwraps the envelope and
// decodes the reservation status DTO.
func TestBillingManagerGetStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/billing/quota/reservations/r1" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		httpresp.OK(w, ReservationStatus{
			ReservationID:    "r1",
			UserID:           "u1",
			Status:           "finalized",
			SettlementStatus: "finalized",
			UsageKnown:       true,
		})
	}))
	defer srv.Close()

	m := NewManager(srv.URL)
	st, err := m.GetStatus(context.Background(), "r1")
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if st.ReservationID != "r1" || st.Status != "finalized" || st.SettlementStatus != "finalized" {
		t.Errorf("status = %+v (envelope not unwrapped?)", st)
	}
}

// TestBillingManagerGetStatusNotFound verifies 404 maps to ErrNotFound via the
// envelope path.
func TestBillingManagerGetStatusNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpresp.Error(w, httpresp.CodeNotFound, "not found")
	}))
	defer srv.Close()

	m := NewManager(srv.URL)
	if _, err := m.GetStatus(context.Background(), "r1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

// TestBillingManagerMalformedEnvelope verifies a 2xx response whose body is not
// a valid envelope/JSON maps to ErrQuotaUnavailable and never leaks the body.
func TestBillingManagerMalformedEnvelope(t *testing.T) {
	const bad = `{"code":0,"data":<not-json>, "message":"oops"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(bad))
	}))
	defer srv.Close()

	m := NewManager(srv.URL)
	_, err := m.Reserve(context.Background(), "r1", "u1", "req1", "coding", 1, 100)
	if !errors.Is(err, ErrQuotaUnavailable) {
		t.Errorf("error = %v, want ErrQuotaUnavailable", err)
	}
	if err != nil && strings.Contains(err.Error(), "not-json") {
		t.Errorf("error leaked body: %v", err)
	}
}

// TestBillingManagerNonzeroEnvelope verifies a 2xx response whose envelope
// carries a non-zero code maps to ErrQuotaUnavailable (no EnvelopeError leak).
func TestBillingManagerNonzeroEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(envelopeErrBody))
	}))
	defer srv.Close()

	m := NewManager(srv.URL)
	_, err := m.Reserve(context.Background(), "r1", "u1", "req1", "coding", 1, 100)
	if !errors.Is(err, ErrQuotaUnavailable) {
		t.Errorf("error = %v, want ErrQuotaUnavailable", err)
	}
	if err != nil && (strings.Contains(err.Error(), "postgres") || strings.Contains(err.Error(), "secret")) {
		t.Errorf("error leaked body: %v", err)
	}
}

// TestBillingManagerErrorBodyNoLeak verifies that a non-2xx error body (which
// carries secrets) is never surfaced through the stable sentinel error.
func TestBillingManagerErrorBodyNoLeak(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(envelopeErrBody))
	}))
	defer srv.Close()

	m := NewManager(srv.URL)
	_, err := m.Reserve(context.Background(), "r1", "u1", "req1", "coding", 1, 100)
	if !errors.Is(err, ErrQuotaUnavailable) {
		t.Errorf("error = %v, want ErrQuotaUnavailable", err)
	}
	if err != nil && (strings.Contains(err.Error(), "postgres") || strings.Contains(err.Error(), "secret")) {
		t.Errorf("error leaked body: %v", err)
	}
}

// TestBillingManagerReserveQuotaExceeded verifies a 429 (quota exceeded) maps
// to the distinct ErrQuotaExceeded sentinel.
func TestBillingManagerReserveQuotaExceeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpresp.ErrorWithStatus(w, http.StatusTooManyRequests, httpresp.CodeConflict, "quota_exceeded: hour5")
	}))
	defer srv.Close()

	m := NewManager(srv.URL)
	_, err := m.Reserve(context.Background(), "r1", "u1", "req1", "coding", 1, 0)
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("error = %v, want ErrQuotaExceeded", err)
	}
}

// TestBillingManagerConflict verifies a 409 maps to ErrConflict (already
// settled) for finalize, release and mark-pending.
func TestBillingManagerConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpresp.ErrorWithStatus(w, http.StatusConflict, httpresp.CodeConflict, "conflict")
	}))
	defer srv.Close()

	m := NewManager(srv.URL)
	if err := m.Finalize(context.Background(), "r1", 1, 80, true); !errors.Is(err, ErrConflict) {
		t.Errorf("Finalize error = %v, want ErrConflict", err)
	}
	if err := m.Release(context.Background(), "r1"); !errors.Is(err, ErrConflict) {
		t.Errorf("Release error = %v, want ErrConflict", err)
	}
	if err := m.MarkPending(context.Background(), "r1"); !errors.Is(err, ErrConflict) {
		t.Errorf("MarkPending error = %v, want ErrConflict", err)
	}
}

// TestBillingManagerReserve200EmptyBody verifies that a 200 OK with an empty
// body does NOT yield a zero-value Reservation success — it must be treated
// as an unavailable Billing service so Edge returns 503 instead of trusting a
// hollow reservation.
func TestBillingManagerReserve200EmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := NewManager(srv.URL)
	res, err := m.Reserve(context.Background(), "r1", "u1", "req1", "coding", 1, 100)
	if !errors.Is(err, ErrQuotaUnavailable) {
		t.Fatalf("Reserve error = %v, want ErrQuotaUnavailable", err)
	}
	if res.ReservationID != "" || res.Status != "" {
		t.Errorf("Reservation = %+v, want zero value on empty 200", res)
	}
}

// TestBillingManagerGetStatus200EmptyBody verifies GetStatus does not return an
// empty DTO on an empty 200.
func TestBillingManagerGetStatus200EmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := NewManager(srv.URL)
	st, err := m.GetStatus(context.Background(), "r1")
	if !errors.Is(err, ErrQuotaUnavailable) {
		t.Fatalf("GetStatus error = %v, want ErrQuotaUnavailable", err)
	}
	if st.ReservationID != "" || st.Status != "" {
		t.Errorf("status = %+v, want zero value on empty 200", st)
	}
}

// TestBillingManagerFinalizeReleaseMarkPending200EmptyBody verifies the
// mutation operations do not silently succeed on an unexpected empty 200.
func TestBillingManagerFinalizeReleaseMarkPending200EmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := NewManager(srv.URL)
	if err := m.Finalize(context.Background(), "r1", 1, 80, true); !errors.Is(err, ErrQuotaUnavailable) {
		t.Errorf("Finalize error = %v, want ErrQuotaUnavailable", err)
	}
	if err := m.Release(context.Background(), "r1"); !errors.Is(err, ErrQuotaUnavailable) {
		t.Errorf("Release error = %v, want ErrQuotaUnavailable", err)
	}
	if err := m.MarkPending(context.Background(), "r1"); !errors.Is(err, ErrQuotaUnavailable) {
		t.Errorf("MarkPending error = %v, want ErrQuotaUnavailable", err)
	}
}

// TestBillingManagerGetStatus204StillSucceeds verifies the GET path still
// succeeds on 204 (the only status allowed to succeed without a body), and
// GetStatus does not error solely because no body was present under 204.
func TestBillingManagerGetStatus204StillSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	m := NewManager(srv.URL)
	// 204 is the only status allowed to succeed without a body.
	if err := m.Finalize(context.Background(), "r1", 1, 80, true); err != nil {
		t.Errorf("Finalize 204 error = %v", err)
	}
	if _, err := m.GetStatus(context.Background(), "r1"); err != nil {
		t.Errorf("GetStatus 204 error = %v", err)
	}
}

// errorBodyRoundTripper returns a response whose body Read always fails, to
// prove a body read error maps to ErrQuotaUnavailable instead of success or a
// leaked transport error.
type errorBodyRoundTripper struct {
	status int
}

func (errorBodyRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       errorReader{},
	}, nil
}

// errorReader is an io.ReadCloser whose Read always fails with a sentinel
// error that must never leak.
type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errReaderBoom }
func (errorReader) Close() error             { return nil }

var errReaderBoom = errors.New("reader boom: connection reset by peer at billing://secret:5432")

// TestBillingManagerBodyReadError verifies a 2xx response whose body cannot be
// read maps to ErrQuotaUnavailable and never leaks the read error.
func TestBillingManagerBodyReadError(t *testing.T) {
	m := NewManager("http://billing.local")
	bm := m.(*billingManager)
	bm.client = &http.Client{Transport: errorBodyRoundTripper{}}

	res, err := bm.Reserve(context.Background(), "r1", "u1", "req1", "coding", 1, 100)
	if !errors.Is(err, ErrQuotaUnavailable) {
		t.Fatalf("Reserve error = %v, want ErrQuotaUnavailable", err)
	}
	if res.ReservationID != "" {
		t.Errorf("Reservation = %+v, want zero value", res)
	}
	if err != nil && (strings.Contains(err.Error(), "reader boom") || strings.Contains(err.Error(), "secret")) {
		t.Errorf("error leaked reader failure: %v", err)
	}
}

// TestBillingManagerBodyReadErrorGet verifies the GET path (GetStatus) also maps
// a body read error to ErrQuotaUnavailable.
func TestBillingManagerBodyReadErrorGet(t *testing.T) {
	m := NewManager("http://billing.local")
	bm := m.(*billingManager)
	bm.client = &http.Client{Transport: errorBodyRoundTripper{}}

	st, err := bm.GetStatus(context.Background(), "r1")
	if !errors.Is(err, ErrQuotaUnavailable) {
		t.Fatalf("GetStatus error = %v, want ErrQuotaUnavailable", err)
	}
	if st.ReservationID != "" {
		t.Errorf("status = %+v, want zero value", st)
	}
}

// TestBillingManager204NoContent verifies the 204 path returns success without
// attempting to decode a body (kept correct even though Billing currently
// always returns a 200 envelope).
func TestBillingManager204NoContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpresp.NoContent(w)
	}))
	defer srv.Close()

	m := NewManager(srv.URL)
	if err := m.Finalize(context.Background(), "r1", 1, 80, true); err != nil {
		t.Errorf("Finalize 204 error = %v", err)
	}
	if err := m.Release(context.Background(), "r1"); err != nil {
		t.Errorf("Release 204 error = %v", err)
	}
	if err := m.MarkPending(context.Background(), "r1"); err != nil {
		t.Errorf("MarkPending 204 error = %v", err)
	}
}

// TestBillingManagerUnreachable verifies a connection failure maps to
// ErrQuotaUnavailable.
func TestBillingManagerUnreachable(t *testing.T) {
	m := NewManager("http://127.0.0.1:1")
	_, err := m.Reserve(context.Background(), "r1", "u1", "req1", "coding", 1, 100)
	if !errors.Is(err, ErrQuotaUnavailable) {
		t.Errorf("error = %v, want ErrQuotaUnavailable", err)
	}
}
