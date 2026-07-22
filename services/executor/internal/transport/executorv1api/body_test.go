package executorv1api

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCaptureRawBodyCapturesExactBytesAndRestoresBody(t *testing.T) {
	raw := []byte(`{"model":"gpt","n":9007199254740993,"e":1e+09,"messages":[]}`)
	var captured, decoded []byte
	h := CaptureRawBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		captured, ok = RawBody(r.Context())
		if !ok {
			t.Fatal("RawBody missing")
		}
		decoded, _ = io.ReadAll(r.Body)
		captured[0] = 'X'
		copy, ok := RawBody(r.Context())
		if !ok || copy[0] != '{' {
			t.Fatal("RawBody was mutable through returned copy")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, openAIChatPath, bytes.NewReader(raw))
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d", recorder.Code)
	}
	if !bytes.Equal(decoded, raw) {
		t.Fatalf("replacement body = %q, want exact %q", decoded, raw)
	}
	if len(captured) != len(raw) {
		t.Fatalf("captured length = %d, want %d", len(captured), len(raw))
	}
}

func TestCaptureRawBodyUsesOneCaptureSliceAndRawBodyCopies(t *testing.T) {
	raw := []byte(`{"model":"gpt","messages":[]}`)
	h := CaptureRawBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		view, ok := rawBodyView(r.Context())
		if !ok {
			t.Fatal("raw body view missing")
		}
		// The generated decoder is permitted to mutate/default its decoded
		// representation, so its reader must not alias capture-owned bytes.
		view[0] = 'X'
		first := make([]byte, 1)
		if _, err := io.ReadFull(r.Body, first); err != nil || first[0] != '{' {
			t.Fatalf("replacement reader must not alias capture slice: first=%q err=%v", first, err)
		}
		view[0] = '{'
		exported, ok := RawBody(r.Context())
		if !ok || &view[0] == &exported[0] {
			t.Fatal("RawBody must return an independent external copy")
		}
		exported[0] = 'X'
		if view[0] != '{' {
			t.Fatal("RawBody mutation polluted capture-owned context bytes")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, openAIChatPath, bytes.NewReader(raw)))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestCaptureRawBodySkipsGETHEADAndUnknownPaths(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, openAIChatPath}, {http.MethodHead, openAIChatPath}, {http.MethodPost, "/v1/responses"},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			h := CaptureRawBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, ok := RawBody(r.Context()); ok {
					t.Fatal("unexpected raw body")
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("unread"))
			recorder := httptest.NewRecorder()
			h.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusNoContent {
				t.Fatalf("status = %d", recorder.Code)
			}
		})
	}
}

func TestCaptureRawBodyCapturesImagesAndOversizeUsesOpenAINative400(t *testing.T) {
	atLimit := bytes.Repeat([]byte("x"), int(MaxCapturedBodyBytes))
	for _, tc := range []struct {
		body []byte
		want int
	}{{atLimit, http.StatusNoContent}, {append(atLimit, 'x'), http.StatusBadRequest}} {
		called := false
		h := CaptureRawBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			if _, ok := RawBody(r.Context()); !ok {
				t.Fatal("image raw body missing")
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, openAIImagesPath, bytes.NewReader(tc.body)))
		if rec.Code != tc.want || called != (tc.want == http.StatusNoContent) || (tc.want == http.StatusBadRequest && !strings.Contains(rec.Body.String(), `"status":400`)) {
			t.Fatalf("status=%d called=%t body=%q", rec.Code, called, rec.Body.String())
		}
	}
}

func TestCaptureRawBodyLimitBoundary(t *testing.T) {
	atLimit := bytes.Repeat([]byte("x"), int(MaxCapturedBodyBytes))
	for _, tc := range []struct {
		name string
		body []byte
		want int
	}{
		{"at limit", atLimit, http.StatusNoContent},
		{"over limit", append(atLimit, 'x'), http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			h := CaptureRawBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(http.MethodPost, anthropicMessagesPath, bytes.NewReader(tc.body))
			recorder := httptest.NewRecorder()
			h.ServeHTTP(recorder, req)
			if recorder.Code != tc.want || called != (tc.want == http.StatusNoContent) {
				t.Fatalf("status/called = %d/%t, want %d/%t", recorder.Code, called, tc.want, tc.want == http.StatusNoContent)
			}
			if tc.want == http.StatusBadRequest && (!strings.Contains(recorder.Body.String(), "invalid_request_error") || strings.Contains(recorder.Body.String(), "xxxxxxxx")) {
				t.Fatalf("unsafe native error: %q", recorder.Body.String())
			}
		})
	}
}

func TestCaptureRawBodyReadFailureUsesProtocolNativeSafeErrors(t *testing.T) {
	for _, tc := range []struct{ path, marker string }{
		{openAIChatPath, `"status":400`}, {anthropicMessagesPath, `"type":"error"`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			h := CaptureRawBody(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("downstream called") }))
			req := httptest.NewRequest(http.MethodPost, tc.path, io.NopCloser(errorReader{errors.New("private failure")}))
			recorder := httptest.NewRecorder()
			h.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusBadRequest || recorder.Header().Get("Content-Type") != "application/json" || !strings.Contains(recorder.Body.String(), tc.marker) || strings.Contains(recorder.Body.String(), "private") {
				t.Fatalf("unsafe response: status=%d headers=%v body=%q", recorder.Code, recorder.Header(), recorder.Body.String())
			}
		})
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

// TestCaptureRawBodyOversizedEmitsProtocolNative400UnderRealServer guards the
// regression where http.MaxBytesReader, mounted under a real net/http Server,
// called response.requestTooLarge() and pre-wrote a generic 413 before this
// package could emit its protocol-native 400. The explicit bounded read must
// keep full ownership of the status and body even through a live server.
func TestCaptureRawBodyOversizedEmitsProtocolNative400UnderRealServer(t *testing.T) {
	for _, tc := range []struct {
		path   string
		marker string
	}{
		{openAIChatPath, `"status":400`},
		{anthropicMessagesPath, `"type":"error"`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			atLimit := bytes.Repeat([]byte("x"), int(MaxCapturedBodyBytes))
			oversized := append(atLimit, 'x')
			for _, c := range []struct {
				name string
				body []byte
				want int
			}{
				{"at limit passes through", atLimit, http.StatusNoContent},
				{"over limit native 400", oversized, http.StatusBadRequest},
			} {
				t.Run(c.name, func(t *testing.T) {
					called := false
					h := CaptureRawBody(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						called = true
						w.WriteHeader(http.StatusNoContent)
					}))
					server := httptest.NewServer(h)
					defer server.Close()
					req, err := http.NewRequest(http.MethodPost, server.URL+tc.path, bytes.NewReader(c.body))
					if err != nil {
						t.Fatal(err)
					}
					req.Header.Set("Content-Type", "application/json")
					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					body, _ := io.ReadAll(resp.Body)
					resp.Body.Close()
					if resp.StatusCode != c.want {
						t.Fatalf("status = %d, want %d; body=%q", resp.StatusCode, c.want, string(body))
					}
					if c.want == http.StatusBadRequest {
						if called {
							t.Fatal("downstream handler must not be called for oversized body")
						}
						if !strings.Contains(string(body), "invalid_request_error") || strings.Contains(string(body), "xxxx") {
							t.Fatalf("unsafe native error body: %q", string(body))
						}
						if !strings.Contains(string(body), tc.marker) {
							t.Fatalf("body = %q, want marker %q", string(body), tc.marker)
						}
					}
					if c.want == http.StatusNoContent && !called {
						t.Fatal("downstream handler must be called for in-limit body")
					}
				})
			}
		})
	}
}
