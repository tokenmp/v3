package quota

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestNoopManager(t *testing.T) {
	m := NewManager("")
	r, err := m.Reserve(context.Background(), "r1", "u1", "req1", "coding", 1, 100)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if r.Status != "reserved" {
		t.Errorf("Status = %q", r.Status)
	}
	if err := m.Finalize(context.Background(), "r1", 1, 80); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if err := m.Release(context.Background(), "r1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestBillingManagerReserveFinalizeRelease(t *testing.T) {
	var reserveHits, finalizeHits, releaseHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/v1/billing/quota/reserve":
			reserveHits.Add(1)
			if body["reservation_id"] != "r1" || body["user_id"] != "u1" {
				t.Errorf("unexpected reserve body: %+v", body)
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"reservation_id": "r1", "status": "reserved"})
		case "/v1/billing/quota/finalize":
			finalizeHits.Add(1)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "finalized"})
		case "/v1/billing/quota/release":
			releaseHits.Add(1)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "released"})
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
		t.Errorf("Reservation = %+v", r)
	}
	if err := m.Finalize(context.Background(), "r1", 1, 80); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if err := m.Release(context.Background(), "r1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if reserveHits.Load() != 1 || finalizeHits.Load() != 1 || releaseHits.Load() != 1 {
		t.Errorf("hits: reserve=%d finalize=%d release=%d", reserveHits.Load(), finalizeHits.Load(), releaseHits.Load())
	}
}

func TestBillingManagerReserveError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	m := NewManager(srv.URL)
	_, err := m.Reserve(context.Background(), "r1", "u1", "req1", "coding", 1, 100)
	if err != ErrQuotaUnavailable {
		t.Errorf("error = %v, want ErrQuotaUnavailable", err)
	}
}

func TestBillingManagerUnreachable(t *testing.T) {
	m := NewManager("http://127.0.0.1:1")
	_, err := m.Reserve(context.Background(), "r1", "u1", "req1", "coding", 1, 100)
	if err != ErrQuotaUnavailable {
		t.Errorf("error = %v, want ErrQuotaUnavailable", err)
	}
}
