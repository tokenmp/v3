package openaiadapter

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

func sseStreamCall(baseURL, secret string) sdk.StreamCall {
	return sdk.StreamCall{Candidate: sdk.CandidateIdentity{ModelID: "m", ProviderID: "p", RouteID: "r", CredentialID: "c", AdapterID: "a"}, Target: sdk.Target{BaseURL: baseURL, UpstreamModel: "upstream", Protocol: adapter.ProtocolOpenAIChat}, Request: adapter.AppliedRequest{Body: []byte(`{"model":"caller","messages":[{"role":"user","content":"hi"}]}`), InjectionPlan: adapter.InjectionPlan{Headers: map[string]string{}, Query: map[string]string{}}}, Secret: sdk.NewCredentialSecret([]byte(secret))}
}

func TestSSEObserverFragmentedDoneAndPreservesBytes(t *testing.T) {
	const wire = ": comment\r\ndata: ignored\r\n\r\ndata: [DONE]\n\n"
	// Use fragmented exact data to prove the observer recognizes [DONE] across
	// reads; a multi-data frame is intentionally not the exact sentinel.
	o := &sseObserver{}
	for _, part := range []string{": keep\r\ndata: [D", "ONE]\r", "\n\r\n"} {
		if err := o.observe([]byte(part)); err != nil {
			t.Fatalf("observe: %v", err)
		}
	}
	if !o.cleanEOF() {
		t.Fatal("exact fragmented [DONE] was not observed")
	}
	body := observingBody(io.NopCloser(bytes.NewBufferString(wire)), &sseObserver{})
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != wire {
		t.Fatalf("observer changed bytes: %q", got)
	}
}

func TestSSEObserverBoundedAndSafeTerminalForms(t *testing.T) {
	for name, wire := range map[string]string{
		"missing done":    "data: x\n\n",
		"duplicate done":  "data: [DONE]\n\ndata: [DONE]\n\n",
		"post done":       "data: [DONE]\n\ndata: x\n\n",
		"post done field": "data: [DONE]\n\nevent: completion\n\n",
		"multidata done":  "data: [DO\ndata: NE]\n\n",
		"oversized line":  "data: " + strings.Repeat("x", maxObservedSSELineBytes+1),
		"oversized frame": "data: " + strings.Repeat("x", maxObservedSSEFrameBytes+1) + "\n\n",
		"too many lines":  strings.Repeat(": x\n", maxObservedSSEFrameLines+1),
		"too much data":   strings.Repeat("data: x\n", maxObservedSSEDataLines+1),
		"field too long":  strings.Repeat("x", maxObservedSSEFieldBytes+1) + ": value\n",
	} {
		t.Run(name, func(t *testing.T) {
			o := &sseObserver{}
			_ = o.observe([]byte(wire))
			if name == "missing done" || name == "multidata done" {
				if o.cleanEOF() || o.terminalErr() != nil {
					t.Fatalf("non-DONE state: terminal=%v clean=%v", o.terminalErr(), o.cleanEOF())
				}
				return
			}
			if o.cleanEOF() || o.terminalErr() == nil {
				t.Fatalf("unsafe stream accepted: terminal=%v clean=%v", o.terminalErr(), o.cleanEOF())
			}
		})
	}
}

func TestSSEObserverIgnoresUnknownFieldsAndComments(t *testing.T) {
	o := &sseObserver{}
	for _, fragment := range [][]byte{
		[]byte(": comment\r\nunknown: provider-value\r\nevent: completion\r\n"),
		[]byte("data: [D"), []byte("ONE]\r\n\r\n"),
	} {
		if err := o.observe(fragment); err != nil {
			t.Fatalf("observe: %v", err)
		}
	}
	if !o.cleanEOF() || o.terminalErr() != nil {
		t.Fatalf("unknown/comment frame = terminal=%v clean=%v", o.terminalErr(), o.cleanEOF())
	}
}

func TestSSEObserverDoesNotRetainRawBuffers(t *testing.T) {
	typeOf := reflect.TypeOf(sseObserver{})
	for i := 0; i < typeOf.NumField(); i++ {
		field := typeOf.Field(i)
		if field.Type.Kind() == reflect.String || (field.Type.Kind() == reflect.Slice && field.Type.Elem().Kind() == reflect.Uint8) {
			t.Fatalf("sseObserver retains raw-capable field %s %s", field.Name, field.Type)
		}
	}
}

func FuzzSSEObserver(f *testing.F) {
	f.Add([]byte("data: [DONE]\n\n"), []byte{1, 3, 8})
	f.Add([]byte("data: raw-provider-secret\r\n\r\n"), []byte{2, 1, 5})
	f.Fuzz(func(t *testing.T, wire, cuts []byte) {
		o := &sseObserver{}
		for offset, i := 0, 0; offset < len(wire); i++ {
			width := 1
			if len(cuts) > 0 {
				width = int(cuts[i%len(cuts)]%32) + 1
			}
			end := offset + width
			if end > len(wire) {
				end = len(wire)
			}
			err := o.observe(wire[offset:end])
			if err != nil && !errors.Is(err, errObservedSSEProtocol) {
				t.Fatalf("unexpected error: %v", err)
			}
			offset = end
		}
	})
}

func TestStreamOpeningMetadataAndLifecycle(t *testing.T) {
	const secret = "do-not-leak"
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+secret {
			t.Errorf("authorization = %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("x-request-id", "req.safe/1")
		_, _ = io.WriteString(w, "data: {\"id\":\"c\",\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":null,\"index\":0}],\"created\":1,\"model\":\"upstream\",\"object\":\"chat.completion.chunk\"}\n\ndata: [DONE]\n\n")
	}))
	defer ts.Close()
	open, err := newTestClient(t, ts, nil).Stream(context.Background(), sseStreamCall(ts.URL, secret))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if open.Source == nil || open.Status != http.StatusOK || open.RequestID != "req.safe/1" {
		t.Fatalf("open = %+v", open)
	}
	defer open.Source.Close()
	ev, err := open.Source.Next(context.Background())
	if err != nil || ev.Meta.Kind != streaming.EventSemantic {
		t.Fatalf("first Next = %#v, %v", ev, err)
	}
	if ev.Sequence != 1 || ev.Meta.Sequence != ev.Sequence || !bytes.Contains(ev.Data, []byte(`"content":"hi"`)) {
		t.Fatalf("event ownership/sequence = %#v", ev)
	}
	_, err = open.Source.Next(context.Background())
	if !errors.Is(err, streaming.ErrEndOfStream) {
		t.Fatalf("terminal = %v", err)
	}
}

func TestStreamNextStrictClassificationSequenceAndTruncation(t *testing.T) {
	for name, wire := range map[string]string{
		"full":      "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\ndata: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
		"truncated": "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, wire)
			}))
			defer ts.Close()
			open, err := newTestClient(t, ts, nil).Stream(context.Background(), sseStreamCall(ts.URL, "key"))
			if err != nil {
				t.Fatal(err)
			}
			defer open.Source.Close()
			var events []sdk.StreamEvent
			for {
				ev, err := open.Source.Next(context.Background())
				if err != nil {
					if name == "truncated" {
						if !errors.Is(err, sdk.ErrProtocol) {
							t.Fatalf("err=%v", err)
						}
						return
					}
					if !errors.Is(err, streaming.ErrEndOfStream) {
						t.Fatal(err)
					}
					break
				}
				events = append(events, ev)
			}
			if len(events) != 4 {
				t.Fatalf("events=%d", len(events))
			}
			for i, e := range events {
				if e.Sequence != uint64(i+1) || e.Meta.Sequence != e.Sequence {
					t.Fatalf("sequence=%d meta.sequence=%d", e.Sequence, e.Meta.Sequence)
				}
			}
			if events[0].Meta.Kind != streaming.EventLifecycle || events[1].Meta.Kind != streaming.EventSemantic || events[2].Meta.Kind != streaming.EventUsage || events[3].Meta.Kind != streaming.EventFinish {
				t.Fatalf("kinds=%+v", events)
			}
		})
	}
}

func TestStreamOpeningRejectsRedirectNon2xxAndNoRetry(t *testing.T) {
	for name, status := range map[string]int{"redirect": http.StatusFound, "failure": http.StatusInternalServerError} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("x-request-id", "req")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "remote secret")
			}))
			defer ts.Close()
			open, err := newTestClient(t, ts, nil).Stream(context.Background(), sseStreamCall(ts.URL, "key"))
			if err == nil || open.Source != nil || calls.Load() != 1 {
				t.Fatalf("open=%+v err=%v calls=%d", open, err, calls.Load())
			}
			if strings.Contains(err.Error(), "remote secret") {
				t.Fatalf("leaked upstream payload: %v", err)
			}
		})
	}
}

func TestStreamEOFRequiresExactDone(t *testing.T) {
	for name, payload := range map[string]string{"missing": "data: {\"id\":\"c\",\"choices\":[],\"created\":1,\"model\":\"u\",\"object\":\"chat.completion.chunk\"}\n\n", "duplicate": "data: [DONE]\n\ndata: [DONE]\n\n", "postdone": "data: [DONE]\n\ndata: x\n\n"} {
		t.Run(name, func(t *testing.T) {
			ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, payload)
			}))
			defer ts.Close()
			open, err := newTestClient(t, ts, nil).Stream(context.Background(), sseStreamCall(ts.URL, "key"))
			if err != nil {
				t.Fatal(err)
			}
			defer open.Source.Close()
			for {
				_, err = open.Source.Next(context.Background())
				if err != nil {
					break
				}
			}
			if !errors.Is(err, sdk.ErrProtocol) {
				t.Fatalf("terminal=%v, want protocol", err)
			}
		})
	}
}

func TestStreamConcurrentCallsKeepCredentialsAndOpeningMetadataLocal(t *testing.T) {
	var wrong atomic.Bool
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if key != "one" && key != "two" {
			wrong.Store(true)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("x-request-id", "req-"+key)
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer ts.Close()
	client := newTestClient(t, ts, nil)
	results := make(chan sdk.StreamOpen, 2)
	errs := make(chan error, 2)
	for _, key := range []string{"one", "two"} {
		go func(key string) {
			open, err := client.Stream(context.Background(), sseStreamCall(ts.URL, key))
			results <- open
			errs <- err
		}(key)
	}
	seen := map[string]bool{}
	for range 2 {
		open, err := <-results, <-errs
		if err != nil {
			t.Fatal(err)
		}
		if open.Source == nil {
			t.Fatal("nil source")
		}
		seen[open.RequestID] = true
		_ = open.Source.Close()
	}
	if wrong.Load() || !seen["req-one"] || !seen["req-two"] {
		t.Fatalf("cross-call isolation failed: wrong=%v seen=%v", wrong.Load(), seen)
	}
}

func TestStreamConcurrentNextReturnsSafeError(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer ts.Close()
	open, err := newTestClient(t, ts, nil).Stream(context.Background(), sseStreamCall(ts.URL, "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer open.Source.Close()
	done := make(chan struct{})
	go func() { _, _ = open.Source.Next(context.Background()); close(done) }()
	time.Sleep(20 * time.Millisecond)
	_, err = open.Source.Next(context.Background())
	if !errors.Is(err, errConcurrentNext) || strings.Contains(err.Error(), "key") {
		t.Fatalf("concurrent Next = %v", err)
	}
	_ = open.Source.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("first Next hung")
	}
}

func TestStreamCloseAndContextCancelUnblockNext(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer ts.Close()
	open, err := newTestClient(t, ts, nil).Stream(context.Background(), sseStreamCall(ts.URL, "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer open.Source.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := open.Source.Next(ctx); done <- e }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatalf("Next = %v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Next hung")
	}
	if err := open.Source.Close(); err != nil {
		t.Fatal(err)
	}
}
