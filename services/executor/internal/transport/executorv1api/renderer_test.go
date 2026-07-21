package executorv1api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/execution"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

const validChatRaw = `{"id":"chatcmpl_1","object":"chat.completion","created":1,"model":"model","choices":[{"index":0,"finish_reason":"stop"}]}`
const validMessageRaw = `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hello"}],"model":"model","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`

func TestRenderSuccessPassesValidatedRawJSONByteForByte(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		raw    string
		render func(execution.Result, http.ResponseWriter) error
	}{
		{"chat", validChatRaw, func(result execution.Result, w http.ResponseWriter) error {
			return RenderChatCompletion(result).VisitCreateChatCompletionResponse(w)
		}},
		{"message", validMessageRaw, func(result execution.Result, w http.ResponseWriter) error {
			return RenderMessage(result).VisitCreateMessageResponse(w)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			successRecorder := httptest.NewRecorder()
			result := execution.Result{Completion: sdk.Completion{Status: http.StatusOK, RawJSON: json.RawMessage(tc.raw)}}
			if err := tc.render(result, successRecorder); err != nil {
				t.Fatalf("Visit response: %v", err)
			}
			if successRecorder.Code != http.StatusOK || successRecorder.Header().Get("Content-Type") != "application/json" || successRecorder.Body.String() != tc.raw {
				t.Fatalf("response = status %d headers %#v body %q", successRecorder.Code, successRecorder.Header(), successRecorder.Body.String())
			}
		})
	}
}

func TestRenderSuccessAllowsContractExtensionsByteForByte(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		raw   string
		visit func(execution.Result, http.ResponseWriter) error
	}{
		{
			name: "openai common extensions",
			raw:  `{"id":"chatcmpl_1","object":"chat.completion","created":1,"model":"model","system_fingerprint":"fp_1","service_tier":"default","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok","refusal":null,"annotations":[{"type":"url_citation","url":"https://example.test"}]},"logprobs":{"content":[{"token":"ok","logprob":-1,"top_logprobs":[]}]}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2,"prompt_tokens_details":{"cached_tokens":0,"provider_extra":{"nested":[true,false,null]}}}}`,
			visit: func(result execution.Result, w http.ResponseWriter) error {
				return RenderChatCompletion(result).VisitCreateChatCompletionResponse(w)
			},
		},
		{
			name: "anthropic common extensions",
			raw:  `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok","citations":[],"cache_control":{"type":"ephemeral"}}],"model":"model","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1,"server_tool_use":{"web_search_requests":0}},"container":{"id":"container_1"}}`,
			visit: func(result execution.Result, w http.ResponseWriter) error {
				return RenderMessage(result).VisitCreateMessageResponse(w)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			if err := tc.visit(execution.Result{Completion: sdk.Completion{Status: http.StatusOK, RawJSON: json.RawMessage(tc.raw)}}, recorder); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusOK || recorder.Body.String() != tc.raw {
				t.Fatalf("response = status %d body %q", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestRenderRejectsInvalidOrAmbiguousCompletion(t *testing.T) {
	t.Parallel()
	invalid := []execution.Result{
		{Completion: sdk.Completion{Status: 201, RawJSON: json.RawMessage(validChatRaw)}},
		{Completion: sdk.Completion{Status: 200}},
		{Completion: sdk.Completion{Status: 200, RawJSON: json.RawMessage(`{"id":"x","id":"y","object":"chat.completion","model":"m","choices":[{"index":0}]}`)}},
		{Completion: sdk.Completion{Status: 200, RawJSON: json.RawMessage(`{"id":"x","object":"chat.completion","model":"m","choices":[{"index":"bad","finish_reason":"stop"}]}`)}},
		{Completion: sdk.Completion{Status: 200, RawJSON: json.RawMessage(`{"id":"x","object":"chat.completion","model":"m","choices":[{"index":0,"finish_reason":"invalid"}]}`)}},
		{Completion: sdk.Completion{Status: 200, RawJSON: json.RawMessage(`{"id":"x","object":"chat.completion","model":"m","choices":[{"index":0,"finish_reason":"stop"}],"__proto__":{}}`)}},
		{Completion: sdk.Completion{Status: 200, RawJSON: json.RawMessage(validChatRaw + ` trailing`)}},
		{Completion: sdk.Completion{Status: 200, RawJSON: json.RawMessage{'{', 0xff, '}'}}},
		{Completion: sdk.Completion{Status: 200, RawJSON: json.RawMessage(validChatRaw)}, Failure: &adapter.MappedResponse{}},

		{Failure: &adapter.MappedResponse{}, Completion: sdk.Completion{RequestID: "unexpected"}},
	}
	for index, result := range invalid {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			if err := RenderChatCompletion(result).VisitCreateChatCompletionResponse(recorder); err != nil {
				t.Fatal(err)
			}
			assertOpenAIError(t, recorder, http.StatusInternalServerError, internalErrorCode, "api_error", internalErrorMessage)
		})
	}
}

func TestRenderRejectsSuccessStructuralBoundaryViolations(t *testing.T) {
	t.Parallel()
	tooDeep := `{"id":"x","object":"chat.completion","model":"m","choices":[{"index":0,"finish_reason":"stop"}],"extension":` + strings.Repeat(`[`, maxJSONDepth) + `null` + strings.Repeat(`]`, maxJSONDepth) + `}`
	tooManyNodes := `{"id":"x","object":"chat.completion","model":"m","choices":[{"index":0,"finish_reason":"stop"}],"extension":[` + strings.Repeat(`null,`, maxJSONNodes) + `null]}`
	tests := []string{
		`{"error":{"message":"provider failure"}}`,
		tooDeep,
		tooManyNodes,
		strings.Repeat(" ", int(MaxCapturedBodyBytes)+1),
	}
	for _, raw := range tests {
		recorder := httptest.NewRecorder()
		result := execution.Result{Completion: sdk.Completion{Status: http.StatusOK, RawJSON: json.RawMessage(raw)}}
		if err := RenderChatCompletion(result).VisitCreateChatCompletionResponse(recorder); err != nil {
			t.Fatal(err)
		}
		assertOpenAIError(t, recorder, http.StatusInternalServerError, internalErrorCode, "api_error", internalErrorMessage)
	}
}

func TestRenderFailureStatusAndSanitization(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		failure       adapter.MappedResponse
		chat, message int
	}{
		{"declared", adapter.MappedResponse{HTTPStatus: 429, ErrorCode: "RATE_LIMITED", ErrorType: "rate_limit_error", Message: "Retry later."}, 429, 429},
		{"chat unsupported 504", adapter.MappedResponse{HTTPStatus: 504, ErrorCode: "TIMEOUT", ErrorType: "timeout_error", Message: "Timed out."}, 502, 529},
		{"unsafe content", adapter.MappedResponse{HTTPStatus: 500, ErrorCode: "private\ncode", ErrorType: "private", Message: "https://private.example/secret"}, 500, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := execution.Result{Failure: &tc.failure}
			chat := httptest.NewRecorder()
			if err := RenderChatCompletion(result).VisitCreateChatCompletionResponse(chat); err != nil {
				t.Fatal(err)
			}
			if chat.Code != tc.chat {
				t.Errorf("chat status = %d, want %d", chat.Code, tc.chat)
			}
			message := httptest.NewRecorder()
			if err := RenderMessageWithRequestID(result, "trusted.req/1").VisitCreateMessageResponse(message); err != nil {
				t.Fatal(err)
			}
			if message.Code != tc.message {
				t.Errorf("message status = %d, want %d", message.Code, tc.message)
			}
			if strings.Contains(chat.Body.String(), "private") || strings.Contains(message.Body.String(), "private") {
				t.Fatalf("unsafe response leaked: chat=%s message=%s", chat.Body.String(), message.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(message.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body["request_id"] != "trusted.req/1" {
				t.Errorf("request_id = %#v, want trusted generated ID", body["request_id"])
			}
		})
	}
}

func TestRenderLocalErrorsAreProtocolNativeAndSafe(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		err    error
		status int
		typ    string
	}{
		{"invalid", ErrInvalidRequest, 400, "invalid_request_error"},
		{"stream", ErrStreamingUnsupported, 501, "api_error"},
		{"unsafe internal", errors.New("https://private.example/secret\nvalue"), 500, "api_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chat := httptest.NewRecorder()
			response := RenderChatError(tc.err)
			if response == nil {
				t.Fatal("RenderChatError returned nil")
			}
			if err := response.VisitCreateChatCompletionResponse(chat); err != nil {
				t.Fatal(err)
			}
			if chat.Code != tc.status || strings.Contains(chat.Body.String(), "private") {
				t.Fatalf("chat = %d %s", chat.Code, chat.Body.String())
			}
			message := httptest.NewRecorder()
			responseMessage := RenderMessageErrorWithRequestID(tc.err, "trusted.req/1")
			if responseMessage == nil {
				t.Fatal("RenderMessageError returned nil")
			}
			if err := responseMessage.VisitCreateMessageResponse(message); err != nil {
				t.Fatal(err)
			}
			if message.Code != tc.status || strings.Contains(message.Body.String(), "private") || !strings.Contains(message.Body.String(), `"request_id":"trusted.req/1"`) {
				t.Fatalf("message = %d %s", message.Code, message.Body.String())
			}
		})
	}
	if RenderChatError(context.Canceled) != nil || RenderMessageError(context.DeadlineExceeded) != nil {
		t.Error("context errors must be left to the HTTP adapter")
	}
}

func assertOpenAIError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code, typ, message string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status = %d, want %d", recorder.Code, status)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
		Status int `json:"status"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != status || body.Error.Code != code || body.Error.Type != typ || body.Error.Message != message {
		t.Fatalf("body = %#v", body)
	}
}
