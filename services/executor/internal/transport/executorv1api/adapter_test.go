package executorv1api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/authcontext"
	"github.com/tokenmp/v3/services/executor/internal/identity"
	"github.com/tokenmp/v3/services/executor/internal/modelcatalog"
	executorv1 "github.com/tokenmp/v3/services/executor/internal/contract/executorv1"
)

func TestHealth(t *testing.T) {
	t.Parallel()

	adapter := New()
	getResponse, err := adapter.ExecutorGetHealthz(context.Background(), executorv1.ExecutorGetHealthzRequestObject{})
	if err != nil {
		t.Fatalf("ExecutorGetHealthz() error = %v", err)
	}

	getRecorder := httptest.NewRecorder()
	if err := getResponse.VisitExecutorGetHealthzResponse(getRecorder); err != nil {
		t.Fatalf("GET response visit error = %v", err)
	}
	getResult := getRecorder.Result()
	defer getResult.Body.Close()
	if getResult.StatusCode != http.StatusOK {
		t.Errorf("GET status = %d, want %d", getResult.StatusCode, http.StatusOK)
	}
	if got := getResult.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("GET Cache-Control = %q, want no-store", got)
	}
	if got := getResult.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("GET Content-Type = %q, want application/json", got)
	}
	var health executorv1.HealthResponse
	if err := json.NewDecoder(getResult.Body).Decode(&health); err != nil {
		t.Fatalf("decode GET body: %v", err)
	}
	if health.Status != executorv1.Ok {
		t.Errorf("GET status body = %q, want %q", health.Status, executorv1.Ok)
	}

	headResponse, err := adapter.ExecutorHeadHealthz(context.Background(), executorv1.ExecutorHeadHealthzRequestObject{})
	if err != nil {
		t.Fatalf("ExecutorHeadHealthz() error = %v", err)
	}
	headRecorder := httptest.NewRecorder()
	if err := headResponse.VisitExecutorHeadHealthzResponse(headRecorder); err != nil {
		t.Fatalf("HEAD response visit error = %v", err)
	}
	if got := headRecorder.Code; got != http.StatusOK {
		t.Errorf("HEAD status = %d, want %d", got, http.StatusOK)
	}
	if got := headRecorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("HEAD Cache-Control = %q, want no-store", got)
	}
	if got := headRecorder.Body.String(); got != "" {
		t.Errorf("HEAD body = %q, want empty", got)
	}
}

func TestOpenAINotImplementedResponses(t *testing.T) {
	t.Parallel()

	adapter := New()
	tests := []struct {
		name string
		call func() (func(http.ResponseWriter) error, error)
	}{
		{
			name: "list models",
			call: func() (func(http.ResponseWriter) error, error) {
				response, err := adapter.ListModels(authedContext(), executorv1.ListModelsRequestObject{})
				return response.VisitListModelsResponse, err
			},
		},
		{
			name: "chat completion",
			call: func() (func(http.ResponseWriter) error, error) {
				response, err := adapter.CreateChatCompletion(context.Background(), executorv1.CreateChatCompletionRequestObject{})
				return response.VisitCreateChatCompletionResponse, err
			},
		},
		{
			name: "response",
			call: func() (func(http.ResponseWriter) error, error) {
				response, err := adapter.CreateResponse(context.Background(), executorv1.CreateResponseRequestObject{})
				return response.VisitCreateResponseResponse, err
			},
		},
		{
			name: "image",
			call: func() (func(http.ResponseWriter) error, error) {
				response, err := adapter.CreateImage(context.Background(), executorv1.CreateImageRequestObject{})
				return response.VisitCreateImageResponse, err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			visit, err := test.call()
			if err != nil {
				t.Fatalf("operation error = %v", err)
			}
			recorder := httptest.NewRecorder()
			if err := visit(recorder); err != nil {
				t.Fatalf("response visit error = %v", err)
			}
			assertJSONResponse(t, recorder, map[string]any{
				"error": map[string]any{
					"message": notImplementedMessage,
					"type":    "api_error",
					"code":    "NOT_IMPLEMENTED",
				},
				"status": float64(http.StatusNotImplemented),
			})
		})
	}
}

func TestAnthropicNotImplementedResponse(t *testing.T) {
	t.Parallel()

	response, err := New().CreateMessage(context.Background(), executorv1.CreateMessageRequestObject{})
	if err != nil {
		t.Fatalf("CreateMessage() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	if err := response.VisitCreateMessageResponse(recorder); err != nil {
		t.Fatalf("response visit error = %v", err)
	}
	assertJSONResponse(t, recorder, map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    "api_error",
			"message": notImplementedMessage,
		},
	})
}

func assertJSONResponse(t *testing.T, recorder *httptest.ResponseRecorder, want map[string]any) {
	t.Helper()
	if got := recorder.Code; got != http.StatusNotImplemented {
		t.Errorf("status = %d, want %d", got, http.StatusNotImplemented)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	var got any
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode raw JSON body: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("raw JSON body = %#v, want %#v", got, want)
	}
}

// stubCatalog is a test double for modelcatalog.CatalogProvider.
type stubCatalog struct {
	result modelcatalog.CatalogResult
	err    error
}

func (s *stubCatalog) ListModels(_ context.Context, _ modelcatalog.CatalogRequest) (modelcatalog.CatalogResult, error) {
	return s.result, s.err
}

// authedContext returns a context with a trusted active service identity.
func authedContext() context.Context {
	return authcontext.WithIdentity(context.Background(), identity.Identity{
		Subject: "svc-1",
		KeyID:   "key-1",
		Role:    identity.RoleService,
		Status:  identity.StatusActive,
	})
}

func TestListModelsReturnsCatalogWhenProviderWired(t *testing.T) {
	t.Parallel()

	minBudget := 1024
	maxBudget := 64000
	catalog := &stubCatalog{
		result: modelcatalog.CatalogResult{
			Models: []modelcatalog.CatalogEntry{
				{
					ID:           "gpt-4o",
					Capabilities: []string{"text", "vision"},
					Created:      1721606400,
				},
				{
					ID:           "claude-3-opus",
					Capabilities: []string{"text", "function_calling", "thinking"},
					Thinking: &modelcatalog.ThinkingConfig{
						Supported:      true,
						DefaultEffort:  "medium",
						MaxEffort:      "high",
						EffortLevels:   []string{"medium", "high"},
						MinBudgetTokens: &minBudget,
						MaxBudgetTokens: &maxBudget,
					},
				},
			},
		},
	}
	adapter := NewNonStream(Options{Executor: nil, Catalog: catalog})
	response, err := adapter.ListModels(authedContext(), executorv1.ListModelsRequestObject{})
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	if err := response.VisitListModelsResponse(recorder); err != nil {
		t.Fatalf("visit error = %v", err)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	var body executorv1.ModelListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Object != executorv1.List {
		t.Errorf("object = %q, want %q", body.Object, executorv1.List)
	}
	if len(body.Data) != 2 {
		t.Fatalf("len(data) = %d, want 2", len(body.Data))
	}

	// First model: gpt-4o
	m0 := body.Data[0]
	if m0.Id != "gpt-4o" {
		t.Errorf("data[0].id = %q, want gpt-4o", m0.Id)
	}
	if m0.Object != executorv1.Model {
		t.Errorf("data[0].object = %q, want model", m0.Object)
	}
	if m0.OwnedBy != "tokenmp" {
		t.Errorf("data[0].owned_by = %q, want tokenmp", m0.OwnedBy)
	}
	if m0.Created == nil || *m0.Created != 1721606400 {
		t.Errorf("data[0].created = %v, want 1721606400", m0.Created)
	}
	if m0.Capabilities == nil || len(*m0.Capabilities) != 2 {
		t.Fatalf("data[0].capabilities = %v, want 2 items", m0.Capabilities)
	}
	if m0.Thinking != nil {
		t.Errorf("data[0].thinking = %v, want nil", m0.Thinking)
	}

	// Second model: claude-3-opus with thinking
	m1 := body.Data[1]
	if m1.Id != "claude-3-opus" {
		t.Errorf("data[1].id = %q, want claude-3-opus", m1.Id)
	}
	if m1.Thinking == nil {
		t.Fatal("data[1].thinking is nil, want non-nil")
	}
	if !m1.Thinking.Supported {
		t.Error("data[1].thinking.supported = false, want true")
	}
	if m1.Thinking.DefaultEffort != executorv1.ModelThinkingConfigDefaultEffortMedium {
		t.Errorf("data[1].thinking.default_effort = %q, want medium", m1.Thinking.DefaultEffort)
	}
	if string(m1.Thinking.MaxEffort) != "high" {
		t.Errorf("data[1].thinking.max_effort = %q, want high", m1.Thinking.MaxEffort)
	}
	if m1.Thinking.MinBudgetTokens == nil || *m1.Thinking.MinBudgetTokens != 1024 {
		t.Errorf("data[1].thinking.min_budget_tokens = %v, want 1024", m1.Thinking.MinBudgetTokens)
	}
	if m1.Thinking.MaxBudgetTokens == nil || *m1.Thinking.MaxBudgetTokens != 64000 {
		t.Errorf("data[1].thinking.max_budget_tokens = %v, want 64000", m1.Thinking.MaxBudgetTokens)
	}
}

func TestListModelsReturns501WhenNoProvider(t *testing.T) {
	t.Parallel()

	adapter := New()
	response, err := adapter.ListModels(authedContext(), executorv1.ListModelsRequestObject{})
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	if err := response.VisitListModelsResponse(recorder); err != nil {
		t.Fatalf("visit error = %v", err)
	}
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", recorder.Code)
	}
}

func TestListModelsReturns500OnNoSnapshot(t *testing.T) {
	t.Parallel()

	catalog := &stubCatalog{err: modelcatalog.ErrNoSnapshot}
	adapter := NewNonStream(Options{Executor: nil, Catalog: catalog})
	response, err := adapter.ListModels(authedContext(), executorv1.ListModelsRequestObject{})
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	if err := response.VisitListModelsResponse(recorder); err != nil {
		t.Fatalf("visit error = %v", err)
	}
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestListModelsReturns500OnQuarantineUnavailable(t *testing.T) {
	t.Parallel()

	catalog := &stubCatalog{err: modelcatalog.ErrQuarantineUnavailable}
	adapter := NewNonStream(Options{Executor: nil, Catalog: catalog})
	response, err := adapter.ListModels(authedContext(), executorv1.ListModelsRequestObject{})
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	if err := response.VisitListModelsResponse(recorder); err != nil {
		t.Fatalf("visit error = %v", err)
	}
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestListModelsOmitsZeroCreated(t *testing.T) {
	t.Parallel()

	catalog := &stubCatalog{
		result: modelcatalog.CatalogResult{
			Models: []modelcatalog.CatalogEntry{
				{ID: "test-model", Capabilities: []string{"text"}, Created: 0},
			},
		},
	}
	adapter := NewNonStream(Options{Executor: nil, Catalog: catalog})
	response, err := adapter.ListModels(authedContext(), executorv1.ListModelsRequestObject{})
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	if err := response.VisitListModelsResponse(recorder); err != nil {
		t.Fatalf("visit error = %v", err)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}

	var body executorv1.ModelListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Data) != 1 {
		t.Fatalf("len(data) = %d, want 1", len(body.Data))
	}
	if body.Data[0].Created != nil {
		t.Errorf("created = %v, want nil (zero should be omitted)", body.Data[0].Created)
	}
}
