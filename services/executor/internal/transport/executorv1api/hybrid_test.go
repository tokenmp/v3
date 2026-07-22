package executorv1api

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	executorv1 "github.com/tokenmp/v3/services/executor/internal/contract/executorv1"
	"github.com/tokenmp/v3/services/executor/internal/execution"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

type hybridStreamExecutor struct {
	calls atomic.Int32
	run   func(context.Context, StreamRequest) (StreamResult, error)
}

func (e *hybridStreamExecutor) Execute(ctx context.Context, request StreamRequest) (StreamResult, error) {
	e.calls.Add(1)
	return e.run(ctx, request)
}

func hybridServer(t *testing.T, strict *Adapter, streaming StreamExecutor) http.Handler {
	t.Helper()
	return CaptureRawBody(executorv1Handler(NewHybrid(HybridOptions{
		Strict: strict, StreamExecutor: streaming,
		RequestIDs: RequestIDSourceFunc(func(context.Context) string { return "req_hybrid" }),
	})))
}

// executorv1Handler is kept small so these tests exercise the generated Chi
// router while NewHybrid itself remains a plain ServerInterface.
func executorv1Handler(server interface {
	CreateChatCompletion(http.ResponseWriter, *http.Request)
	CreateMessage(http.ResponseWriter, *http.Request)
	CreateImage(http.ResponseWriter, *http.Request)
	CreateResponse(http.ResponseWriter, *http.Request)
	ListModels(http.ResponseWriter, *http.Request)
	ExecutorGetHealthz(http.ResponseWriter, *http.Request)
	ExecutorHeadHealthz(http.ResponseWriter, *http.Request)
}) http.Handler {
	return executorv1.Handler(server)
}

func TestHybridChatStreamingWritesSSEAndDoesNotAppendJSONAfterCommit(t *testing.T) {
	t.Parallel()
	streamer := &hybridStreamExecutor{run: func(ctx context.Context, req StreamRequest) (StreamResult, error) {
		if req.RequestID != "req_hybrid" || req.Protocol != adapter.ProtocolOpenAIChat {
			t.Fatalf("unexpected normalized stream request: %#v", req)
		}
		first := sseEvent(1, streaming.EventSemantic, "chat.completion.chunk", `{"id":"one"}`)
		if err := req.Sink.Commit(ctx, []sdk.StreamEvent{first}); err != nil {
			t.Fatal(err)
		}
		return StreamResult{}, errors.New("private post-commit error")
	}}
	server := hybridServer(t, NewNonStream(Options{}), streamer)
	req := newRequest(http.MethodPost, openAIChatPath, `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)

	if got, want := recorder.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := recorder.Body.String(); got != "data: {\"id\":\"one\"}\n\n" {
		t.Fatalf("body = %q", got)
	}
	if strings.Contains(recorder.Body.String(), "INTERNAL_ERROR") || streamer.calls.Load() != 1 {
		t.Fatalf("post-commit fallback or calls=%d", streamer.calls.Load())
	}
}

func TestHybridFlushesBeforeStreamExecutorReturns(t *testing.T) {
	committed := make(chan struct{})
	release := make(chan struct{})
	streamer := &hybridStreamExecutor{run: func(ctx context.Context, req StreamRequest) (StreamResult, error) {
		if err := req.Sink.Commit(ctx, []sdk.StreamEvent{sseEvent(1, streaming.EventSemantic, "chat.completion.chunk", `{"id":"first"}`)}); err != nil {
			return StreamResult{}, err
		}
		close(committed)
		<-release
		return StreamResult{}, nil
	}}
	server := httptest.NewServer(hybridServer(t, NewNonStream(Options{}), streamer))
	defer server.Close()

	response, err := server.Client().Post(server.URL+openAIChatPath, "application/json", strings.NewReader(`{"model":"gpt","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	select {
	case <-committed:
	case <-time.After(time.Second):
		t.Fatal("stream executor did not commit")
	}
	frame, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || frame != "data: {\"id\":\"first\"}\n" {
		t.Fatalf("first frame = %q, %v", frame, err)
	}
	close(release)
}

func TestHybridMessagePrecommitMappedFailureAndInvalidStream(t *testing.T) {
	t.Parallel()
	streamer := &hybridStreamExecutor{run: func(context.Context, StreamRequest) (StreamResult, error) {
		return StreamResult{Failure: &adapter.MappedResponse{HTTPStatus: http.StatusNotFound, ErrorCode: "not_found_error", ErrorType: "not_found_error", Message: "model: not found"}}, nil
	}}
	server := hybridServer(t, NewNonStream(Options{}), streamer)
	request := func(body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, newRequest(http.MethodPost, anthropicMessagesPath, body))
		return recorder
	}
	failure := request(`{"model":"claude","max_tokens":8,"messages":[{"role":"user","content":"hi"}],"stream":true}`)
	if failure.Code != http.StatusNotFound || !strings.Contains(failure.Body.String(), `"request_id":"req_hybrid"`) {
		t.Fatalf("precommit failure = %d %s", failure.Code, failure.Body.String())
	}
	invalid := request(`{"model":"claude","max_tokens":8,"messages":[{"role":"user","content":"hi"}],"stream":true,"unknown":true}`)
	if invalid.Code != http.StatusBadRequest || streamer.calls.Load() != 1 {
		t.Fatalf("invalid stream = %d calls=%d", invalid.Code, streamer.calls.Load())
	}
}

type hybridNoFlushWriter struct {
	header http.Header
	status int
	body   strings.Builder
}

func (w *hybridNoFlushWriter) Header() http.Header            { return w.header }
func (w *hybridNoFlushWriter) WriteHeader(status int)         { w.status = status }
func (w *hybridNoFlushWriter) Write(data []byte) (int, error) { return w.body.Write(data) }

func TestHybridStreamRequiresFlushBeforeExecution(t *testing.T) {
	t.Parallel()
	streamer := &hybridStreamExecutor{run: func(context.Context, StreamRequest) (StreamResult, error) {
		t.Fatal("executor called without a flushing response writer")
		return StreamResult{}, nil
	}}
	hybrid := NewHybrid(HybridOptions{Strict: NewNonStream(Options{}), StreamExecutor: streamer})
	handler := CaptureRawBody(http.HandlerFunc(hybrid.CreateChatCompletion))
	writer := &hybridNoFlushWriter{header: make(http.Header)}
	handler.ServeHTTP(writer, newRequest(http.MethodPost, openAIChatPath, `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	if writer.status != http.StatusInternalServerError || streamer.calls.Load() != 0 {
		t.Fatalf("status=%d calls=%d body=%s", writer.status, streamer.calls.Load(), writer.body.String())
	}
}

func TestHybridRequestIDSourcesAreModeBound(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{"chat false", openAIChatPath, `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"stream":false}`},
		{"chat true", openAIChatPath, `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"stream":true}`},
		{"message false", anthropicMessagesPath, `{"model":"claude","max_tokens":8,"messages":[{"role":"user","content":"hi"}],"stream":false}`},
		{"message true", anthropicMessagesPath, `{"model":"claude","max_tokens":8,"messages":[{"role":"user","content":"hi"}],"stream":true}`},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var hybridIDs, adapterIDs atomic.Int32
			strict := NewNonStream(Options{
				Executor: &recorderExecutor{result: execution.Result{}},
				RequestIDs: RequestIDSourceFunc(func(context.Context) string {
					adapterIDs.Add(1)
					return "req_adapter"
				}),
			})
			streamer := &hybridStreamExecutor{run: func(context.Context, StreamRequest) (StreamResult, error) {
				return StreamResult{}, nil
			}}
			handler := CaptureRawBody(executorv1Handler(NewHybrid(HybridOptions{
				Strict: strict, StreamExecutor: streamer,
				RequestIDs: RequestIDSourceFunc(func(context.Context) string {
					hybridIDs.Add(1)
					return "req_hybrid"
				}),
			})))
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, newRequest(http.MethodPost, tc.path, tc.body))
			if strings.HasSuffix(tc.name, "false") {
				if got, want := hybridIDs.Load(), int32(0); got != want {
					t.Fatalf("hybrid request IDs = %d, want %d", got, want)
				}
				if got, want := adapterIDs.Load(), int32(1); got != want {
					t.Fatalf("adapter request IDs = %d, want %d", got, want)
				}
				if got, want := streamer.calls.Load(), int32(0); got != want {
					t.Fatalf("stream calls = %d, want %d", got, want)
				}
			} else {
				if got, want := hybridIDs.Load(), int32(1); got != want {
					t.Fatalf("hybrid request IDs = %d, want %d", got, want)
				}
				if got, want := adapterIDs.Load(), int32(0); got != want {
					t.Fatalf("adapter request IDs = %d, want %d", got, want)
				}
				if got, want := streamer.calls.Load(), int32(1); got != want {
					t.Fatalf("stream calls = %d, want %d", got, want)
				}
			}
		})
	}
}

func TestHybridSchemaErrorRequestIDOwnershipByMode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		path        string
		body        string
		hybridWant  int32
		adapterWant int32
	}{
		{"chat false", openAIChatPath, `{"model":"gpt","messages":[{"role":"invalid","content":"hi"}],"stream":false}`, 0, 1},
		{"chat true", openAIChatPath, `{"model":"gpt","messages":[{"role":"invalid","content":"hi"}],"stream":true}`, 1, 0},
		{"message false", anthropicMessagesPath, `{"model":"claude","max_tokens":8,"messages":[{"role":"invalid","content":"hi"}],"stream":false}`, 0, 1},
		{"message true", anthropicMessagesPath, `{"model":"claude","max_tokens":8,"messages":[{"role":"invalid","content":"hi"}],"stream":true}`, 1, 0},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var hybridIDs, adapterIDs atomic.Int32
			strict := NewNonStream(Options{
				Executor: &recorderExecutor{result: execution.Result{}},
				RequestIDs: RequestIDSourceFunc(func(context.Context) string {
					adapterIDs.Add(1)
					return "req_adapter"
				}),
			})
			handler := CaptureRawBody(executorv1Handler(NewHybrid(HybridOptions{
				Strict: strict, RequestIDs: RequestIDSourceFunc(func(context.Context) string {
					hybridIDs.Add(1)
					return "req_hybrid"
				}),
			})))
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, newRequest(http.MethodPost, tc.path, tc.body))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", recorder.Code)
			}
			if got := hybridIDs.Load(); got != tc.hybridWant {
				t.Fatalf("hybrid request IDs = %d, want %d", got, tc.hybridWant)
			}
			if got := adapterIDs.Load(); got != tc.adapterWant {
				t.Fatalf("adapter request IDs = %d, want %d", got, tc.adapterWant)
			}
		})
	}
}

func TestHybridStructuralDetectionRejectsWithoutRequestID(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{"chat duplicate", openAIChatPath, `{"model":"gpt","model":"gpt","stream":true}`},
		{"message stream type", anthropicMessagesPath, `{"model":"claude","stream":"true"}`},
		{"chat trailing", openAIChatPath, `{"stream":true}{}`},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var hybridIDs, adapterIDs atomic.Int32
			strict := NewNonStream(Options{RequestIDs: RequestIDSourceFunc(func(context.Context) string {
				adapterIDs.Add(1)
				return "req_adapter"
			})})
			handler := CaptureRawBody(executorv1Handler(NewHybrid(HybridOptions{
				Strict: strict, RequestIDs: RequestIDSourceFunc(func(context.Context) string {
					hybridIDs.Add(1)
					return "req_hybrid"
				}),
			})))
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, newRequest(http.MethodPost, tc.path, tc.body))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", recorder.Code)
			}
			if hybridIDs.Load() != 0 || adapterIDs.Load() != 0 {
				t.Fatalf("request IDs hybrid=%d adapter=%d, want 0, 0", hybridIDs.Load(), adapterIDs.Load())
			}
		})
	}
}

func TestHybridNonStreamDelegatesStrictAndMissingStreamExecutorFailsClosed(t *testing.T) {
	t.Parallel()
	nonstream := &recorderExecutor{result: execution.Result{Failure: &adapter.MappedResponse{HTTPStatus: http.StatusNotFound, ErrorCode: "model_not_found", ErrorType: "invalid_request_error", Message: "The requested model does not exist."}}}
	server := hybridServer(t, NewNonStream(Options{Executor: nonstream}), nil)

	nonstreamResponse := httptest.NewRecorder()
	server.ServeHTTP(nonstreamResponse, newRequest(http.MethodPost, openAIChatPath, integrationChatRequestBody()))
	if nonstreamResponse.Code != http.StatusNotFound || nonstream.calls.Load() != 1 {
		t.Fatalf("non-stream delegate = %d calls=%d", nonstreamResponse.Code, nonstream.calls.Load())
	}
	streamResponse := httptest.NewRecorder()
	server.ServeHTTP(streamResponse, newRequest(http.MethodPost, openAIChatPath, `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	if streamResponse.Code != http.StatusInternalServerError || strings.Contains(streamResponse.Body.String(), "stream executor") {
		t.Fatalf("missing stream executor = %d %s", streamResponse.Code, streamResponse.Body.String())
	}
	for _, path := range []string{"/v1/models", "/v1/responses"} {
		recorder := httptest.NewRecorder()
		method := http.MethodPost
		if path == "/v1/models" {
			method = http.MethodGet
		}
		server.ServeHTTP(recorder, newRequest(method, path, `{}`))
		if recorder.Code != http.StatusNotImplemented {
			t.Errorf("strict delegate %s status=%d, want 501", path, recorder.Code)
		}
	}
}
