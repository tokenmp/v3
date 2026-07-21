package executorv1api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/identity"

	"github.com/tokenmp/v3/services/executor/internal/authcontext"
)

type authPort struct {
	id    identity.Identity
	err   error
	calls atomic.Int32
}

func (p *authPort) LookupByKey(_ context.Context, key string) (identity.Identity, error) {
	p.calls.Add(1)
	if key != "tm-good" {
		return identity.Identity{}, identity.ErrUnknownKey
	}
	return p.id, p.err
}

type readSpy struct{ reads atomic.Int32 }

func (s *readSpy) Read(p []byte) (int, error) { s.reads.Add(1); return 0, io.EOF }
func (s *readSpy) Close() error               { return nil }

func TestAuthMiddlewareProtocolsAndContext(t *testing.T) {
	port := &authPort{id: identity.Identity{Subject: "sub", KeyID: "kid", Role: identity.RoleAdmin, Status: identity.StatusActive}}
	var downstream atomic.Int32
	h := AuthMiddleware(port)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downstream.Add(1)
		id, ok := authcontext.IdentityFromContext(r.Context())
		if !ok || id.Subject != "sub" {
			t.Error("missing identity")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, path := range []string{"/v1/chat/completions", "/v1/messages", "/v1/nope"} {
		r := httptest.NewRequest(http.MethodPost, path, nil)
		r.Header.Set("Authorization", "bEaReR tm-good")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusNoContent {
			t.Fatalf("%s status %d", path, w.Code)
		}
	}
	if downstream.Load() != 3 {
		t.Fatal("downstream calls")
	}
}
func TestAuthMiddlewareRejectsBeforeBodyAndUsesNativeErrors(t *testing.T) {
	port := &authPort{id: identity.Identity{Role: identity.RoleService, Status: identity.StatusActive}}
	var downstream atomic.Int32
	h := AuthMiddleware(port)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { downstream.Add(1) }))
	for _, tc := range []struct{ path, header, want string }{
		{"/v1/messages", "", `"type":"error"`}, {"/v1/chat/completions", "Basic tm-good", `"status":401`},
	} {
		spy := &readSpy{}
		r := httptest.NewRequest(http.MethodPost, tc.path, spy)
		if tc.header != "" {
			r.Header.Set("Authorization", tc.header)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 || !strings.Contains(w.Body.String(), tc.want) || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
		if spy.reads.Load() != 0 || downstream.Load() != 0 {
			t.Fatal("unauthorized request read body or called downstream")
		}
	}
}
func TestAuthMiddlewareDuplicateUnknownDisabledAndCanceled(t *testing.T) {
	for _, tc := range []struct {
		name   string
		port   identity.Port
		cancel bool
	}{
		{"unknown", &authPort{id: identity.Identity{Status: identity.StatusActive}}, false},
		{"disabled", &authPort{id: identity.Identity{Status: identity.StatusDisabled}}, false},
		{"failure", &authPort{err: errors.New("secret env failure")}, false},
		{"cancel", &authPort{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := AuthMiddleware(tc.port)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("downstream") }))
			r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			r.Header.Set("Authorization", "Bearer tm-good")
			if tc.cancel {
				ctx, cancel := context.WithCancel(r.Context())
				cancel()
				r = r.WithContext(ctx)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if tc.cancel {
				if w.Code != 200 || w.Body.Len() != 0 {
					t.Fatalf("cancel wrote %d %q", w.Code, w.Body.String())
				}
			} else if w.Code != 401 || strings.Contains(w.Body.String(), "secret") {
				t.Fatalf("got %d %q", w.Code, w.Body.String())
			}
		})
	}
}
func TestAuthMiddlewareHeaderAndHealthRules(t *testing.T) {
	port := &authPort{id: identity.Identity{Status: identity.StatusActive, Role: identity.RoleService}}
	var calls atomic.Int32
	h := AuthMiddleware(port)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 || port.calls.Load() != 0 {
		t.Fatal("health was authenticated")
	}
	for _, header := range []string{"Bearer tm-good tm-extra", "Bearer tm-good\t", "Bearer tm-good\n"} {
		r = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		r.Header.Add("Authorization", header)
		r.Header.Add("Authorization", "Bearer tm-good")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("accepted malformed/duplicate %q", header)
		}
	}
}
func FuzzBearerToken(f *testing.F) {
	f.Add("Bearer tm-good")
	f.Add("")
	f.Fuzz(func(t *testing.T, v string) { _, _ = bearerToken([]string{v}) })
}
