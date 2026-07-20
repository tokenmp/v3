package executorv1api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

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
				response, err := adapter.ListModels(context.Background(), executorv1.ListModelsRequestObject{})
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
