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
	executorv1 "github.com/tokenmp/v3/services/executor/internal/contract/executorv1"
	"github.com/tokenmp/v3/services/executor/internal/execution"
	"github.com/tokenmp/v3/services/executor/internal/nonstream"
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

func TestRenderModelNotFoundIsProtocolNative404(t *testing.T) {
	t.Parallel()
	chat := httptest.NewRecorder()
	chatResp := RenderChatError(ErrModelNotFound)
	if chatResp == nil {
		t.Fatal("RenderChatError returned nil")
	}
	if err := chatResp.VisitCreateChatCompletionResponse(chat); err != nil {
		t.Fatal(err)
	}
	var chatBody executorv1.OpenAIErrorResponse
	if err := json.Unmarshal(chat.Body.Bytes(), &chatBody); err != nil {
		t.Fatal(err)
	}
	if chat.Code != http.StatusNotFound || chatBody.Error.Code == nil || *chatBody.Error.Code != "model_not_found" || chatBody.Error.Type != "invalid_request_error" {
		t.Fatalf("chat = %d %s", chat.Code, chat.Body.String())
	}

	message := httptest.NewRecorder()
	msgResp := RenderMessageErrorWithRequestID(ErrModelNotFound, "trusted.req/1")
	if msgResp == nil {
		t.Fatal("RenderMessageError returned nil")
	}
	if err := msgResp.VisitCreateMessageResponse(message); err != nil {
		t.Fatal(err)
	}
	var msgBody executorv1.AnthropicErrorResponse
	if err := json.Unmarshal(message.Body.Bytes(), &msgBody); err != nil {
		t.Fatal(err)
	}
	if message.Code != http.StatusNotFound || msgBody.Error.Type != "not_found_error" || !strings.Contains(message.Body.String(), `"request_id":"trusted.req/1"`) {
		t.Fatalf("message = %d %s", message.Code, message.Body.String())
	}
	// A wrapped ErrModelNotFound must still render as 404, proving the facade
	// may return errors.Join(ErrModelNotFound, ...) without losing the category.
	wrapped := errors.Join(ErrModelNotFound, errors.New("internal detail"))
	wrappedChat := httptest.NewRecorder()
	wrappedResp := RenderChatError(wrapped)
	if wrappedResp == nil {
		t.Fatal("wrapped RenderChatError returned nil")
	}
	if err := wrappedResp.VisitCreateChatCompletionResponse(wrappedChat); err != nil {
		t.Fatal(err)
	}
	if wrappedChat.Code != http.StatusNotFound || strings.Contains(wrappedChat.Body.String(), "internal detail") {
		t.Fatalf("wrapped chat = %d %s", wrappedChat.Code, wrappedChat.Body.String())
	}
}

func TestRenderUnauthorizedIsProtocolNative401(t *testing.T) {
	t.Parallel()
	chat := httptest.NewRecorder()
	chatResp := RenderChatError(nonstream.ErrUnauthorized)
	if chatResp == nil {
		t.Fatal("RenderChatError returned nil")
	}
	if err := chatResp.VisitCreateChatCompletionResponse(chat); err != nil {
		t.Fatal(err)
	}
	var chatBody executorv1.OpenAIErrorResponse
	if err := json.Unmarshal(chat.Body.Bytes(), &chatBody); err != nil {
		t.Fatal(err)
	}
	if chat.Code != http.StatusUnauthorized || chatBody.Error.Type != "authentication_error" {
		t.Fatalf("chat = %d %s", chat.Code, chat.Body.String())
	}

	message := httptest.NewRecorder()
	msgResp := RenderMessageErrorWithRequestID(nonstream.ErrUnauthorized, "trusted.req/2")
	if msgResp == nil {
		t.Fatal("RenderMessage returned nil")
	}
	if err := msgResp.VisitCreateMessageResponse(message); err != nil {
		t.Fatal(err)
	}
	if message.Code != http.StatusUnauthorized {
		t.Fatalf("message = %d %s", message.Code, message.Body.String())
	}
	// A wrapped ErrUnauthorized must still render as 401.
	wrapped := errors.Join(nonstream.ErrUnauthorized, errors.New("internal detail"))
	wrappedChat := httptest.NewRecorder()
	wrappedResp := RenderChatError(wrapped)
	if wrappedResp == nil {
		t.Fatal("wrapped RenderChatError returned nil")
	}
	if err := wrappedResp.VisitCreateChatCompletionResponse(wrappedChat); err != nil {
		t.Fatal(err)
	}
	if wrappedChat.Code != http.StatusUnauthorized || strings.Contains(wrappedChat.Body.String(), "internal detail") {
		t.Fatalf("wrapped chat = %d %s", wrappedChat.Code, wrappedChat.Body.String())
	}
}

func TestRenderImageEnforcesFormatAndSharedResponseBoundary(t *testing.T) {
	t.Parallel()
	validURL := `{"created":1,"data":[{"url":"https://images.example/a"}],"usage":{"input_tokens":1,"input_tokens_details":{"image_tokens":1}},"provider_extension":{"nested":true}}`
	cases := []struct {
		name, raw, format string
		want              int
	}{
		{"url", validURL, "url", http.StatusOK},
		{"format mismatch", validURL, "b64_json", http.StatusInternalServerError},
		{"mixed items", `{"created":1,"data":[{"url":"https://images.example/a"},{"b64_json":"aA=="}]}`, "url", http.StatusInternalServerError},
		{"ctl revised", `{"created":1,"data":[{"url":"https://images.example/a","revised_prompt":"x\ny"}]}`, "url", http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			err := RenderImage(execution.Result{Completion: sdk.Completion{Status: http.StatusOK, RawJSON: json.RawMessage(tc.raw)}}, tc.format).VisitCreateImageResponse(rec)
			if err != nil || rec.Code != tc.want || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("err=%v status=%d cache=%q", err, rec.Code, rec.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestRetryAfterHeaderOnRateLimitErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		failure        adapter.MappedResponse
		wantRetryAfter string // empty means header must be absent
		chatStatus     int
		messageStatus  int
	}{
		{
			name:           "chat 429 with Retry-After",
			failure:        adapter.MappedResponse{HTTPStatus: 429, ErrorCode: "RATE_LIMITED", ErrorType: "rate_limit_error", Message: "Slow down.", RetryAfterSeconds: 30},
			wantRetryAfter: "30",
			chatStatus:     429,
			messageStatus:  429,
		},
		{
			name:           "429 without Retry-After",
			failure:        adapter.MappedResponse{HTTPStatus: 429, ErrorCode: "RATE_LIMITED", ErrorType: "rate_limit_error", Message: "Slow down."},
			wantRetryAfter: "",
			chatStatus:     429,
			messageStatus:  429,
		},
		{
			name:           "529 with Retry-After",
			failure:        adapter.MappedResponse{HTTPStatus: 529, ErrorCode: "OVERLOADED", ErrorType: "overloaded_error", Message: "Overloaded.", RetryAfterSeconds: 60},
			wantRetryAfter: "60",
			chatStatus:     502, // chat maps 529 → 502
			messageStatus:  529, // anthropic keeps 529
		},
		{
			name:           "529 without Retry-After",
			failure:        adapter.MappedResponse{HTTPStatus: 529, ErrorCode: "OVERLOADED", ErrorType: "overloaded_error", Message: "Overloaded."},
			wantRetryAfter: "",
			chatStatus:     502,
			messageStatus:  529,
		},
		{
			name:           "non-rate-limit status no Retry-After even if field set",
			failure:        adapter.MappedResponse{HTTPStatus: 500, ErrorCode: "INTERNAL", ErrorType: "api_error", Message: "Error.", RetryAfterSeconds: 10},
			wantRetryAfter: "",
			chatStatus:     500,
			messageStatus:  500,
		},
		{
			name:           "less than 1 second not set",
			failure:        adapter.MappedResponse{HTTPStatus: 429, ErrorCode: "RATE_LIMITED", ErrorType: "rate_limit_error", Message: "Slow.", RetryAfterSeconds: 0},
			wantRetryAfter: "",
			chatStatus:     429,
			messageStatus:  429,
		},
		{
			name:           "clamp to 300",
			failure:        adapter.MappedResponse{HTTPStatus: 429, ErrorCode: "RATE_LIMITED", ErrorType: "rate_limit_error", Message: "Slow.", RetryAfterSeconds: 500},
			wantRetryAfter: "300",
			chatStatus:     429,
			messageStatus:  429,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := execution.Result{Failure: &tc.failure}

			// Chat path
			chat := httptest.NewRecorder()
			if err := RenderChatCompletion(result).VisitCreateChatCompletionResponse(chat); err != nil {
				t.Fatal(err)
			}
			if chat.Code != tc.chatStatus {
				t.Errorf("chat status = %d, want %d", chat.Code, tc.chatStatus)
			}
			// 429 retains Retry-After on chat path; 529 maps to 502 so no Retry-After
			chatExpectedRA := tc.wantRetryAfter
			if tc.failure.HTTPStatus == 529 && tc.wantRetryAfter != "" {
				// chat maps 529 → 502, so 502 should not have Retry-After
				chatExpectedRA = ""
			}
			if got := chat.Header().Get("Retry-After"); got != chatExpectedRA {
				t.Errorf("chat Retry-After = %q, want %q", got, chatExpectedRA)
			}

			// Message path (Anthropic)
			message := httptest.NewRecorder()
			if err := RenderMessageWithRequestID(result, "req_test").VisitCreateMessageResponse(message); err != nil {
				t.Fatal(err)
			}
			if message.Code != tc.messageStatus {
				t.Errorf("message status = %d, want %d", message.Code, tc.messageStatus)
			}
			if got := message.Header().Get("Retry-After"); got != tc.wantRetryAfter {
				t.Errorf("message Retry-After = %q, want %q", got, tc.wantRetryAfter)
			}
		})
	}
}

func TestRetryAfterNotSetOnLocalErrors(t *testing.T) {
	t.Parallel()
	localErrors := []struct {
		name string
		err  error
	}{
		{"invalid request", ErrInvalidRequest},
		{"unauthorized", ErrUnauthorized},
		{"model not found", ErrModelNotFound},
		{"streaming unsupported", ErrStreamingUnsupported},
	}
	for _, tc := range localErrors {
		t.Run(tc.name+" chat", func(t *testing.T) {
			rec := httptest.NewRecorder()
			resp := RenderChatError(tc.err)
			if resp == nil {
				t.Fatal("nil response")
			}
			if err := resp.VisitCreateChatCompletionResponse(rec); err != nil {
				t.Fatal(err)
			}
			if ra := rec.Header().Get("Retry-After"); ra != "" {
				t.Errorf("local error must not set Retry-After, got %q", ra)
			}
		})
		t.Run(tc.name+" message", func(t *testing.T) {
			rec := httptest.NewRecorder()
			resp := RenderMessageErrorWithRequestID(tc.err, "req_test")
			if resp == nil {
				t.Fatal("nil response")
			}
			if err := resp.VisitCreateMessageResponse(rec); err != nil {
				t.Fatal(err)
			}
			if ra := rec.Header().Get("Retry-After"); ra != "" {
				t.Errorf("local error must not set Retry-After, got %q", ra)
			}
		})
	}
}

func TestRetryAfterOnImageAndResponseFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		failure        adapter.MappedResponse
		wantRetryAfter string
	}{
		{
			name:           "image 429 with RA",
			failure:        adapter.MappedResponse{HTTPStatus: 429, ErrorCode: "RATE_LIMITED", ErrorType: "rate_limit_error", Message: "Slow.", RetryAfterSeconds: 30},
			wantRetryAfter: "30",
		},
		{
			name:           "image 429 without RA",
			failure:        adapter.MappedResponse{HTTPStatus: 429, ErrorCode: "RATE_LIMITED", ErrorType: "rate_limit_error", Message: "Slow."},
			wantRetryAfter: "",
		},
		{
			name:           "response 429 with RA",
			failure:        adapter.MappedResponse{HTTPStatus: 429, ErrorCode: "RATE_LIMITED", ErrorType: "rate_limit_error", Message: "Slow.", RetryAfterSeconds: 45},
			wantRetryAfter: "45",
		},
		{
			name:           "response 500 with RA field set ignored",
			failure:        adapter.MappedResponse{HTTPStatus: 500, ErrorCode: "INTERNAL", ErrorType: "api_error", Message: "Error.", RetryAfterSeconds: 10},
			wantRetryAfter: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := execution.Result{Failure: &tc.failure}

			image := httptest.NewRecorder()
			if err := RenderImage(result, "url").VisitCreateImageResponse(image); err != nil {
				t.Fatal(err)
			}
			if got := image.Header().Get("Retry-After"); got != tc.wantRetryAfter {
				t.Errorf("image Retry-After = %q, want %q", got, tc.wantRetryAfter)
			}

			response := httptest.NewRecorder()
			if err := RenderResponse(result).VisitCreateResponseResponse(response); err != nil {
				t.Fatal(err)
			}
			if got := response.Header().Get("Retry-After"); got != tc.wantRetryAfter {
				t.Errorf("response Retry-After = %q, want %q", got, tc.wantRetryAfter)
			}
		})
	}
}

func TestRetryAfterNotSetOnImageLocalErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"invalid request", ErrInvalidRequest},
		{"unauthorized", ErrUnauthorized},
		{"model not found", ErrModelNotFound},
	} {
		t.Run(tc.name+" image", func(t *testing.T) {
			rec := httptest.NewRecorder()
			resp := RenderImageError(tc.err)
			if resp == nil {
				t.Fatal("nil response")
			}
			if err := resp.VisitCreateImageResponse(rec); err != nil {
				t.Fatal(err)
			}
			if ra := rec.Header().Get("Retry-After"); ra != "" {
				t.Errorf("local image error must not set Retry-After, got %q", ra)
			}
		})
		t.Run(tc.name+" response", func(t *testing.T) {
			rec := httptest.NewRecorder()
			resp := RenderResponseError(tc.err)
			if resp == nil {
				t.Fatal("nil response")
			}
			if err := resp.VisitCreateResponseResponse(rec); err != nil {
				t.Fatal(err)
			}
			if ra := rec.Header().Get("Retry-After"); ra != "" {
				t.Errorf("local response error must not set Retry-After, got %q", ra)
			}
		})
	}
}

func TestRetryAfterOnStreamPrecommitFailure(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		failure        adapter.MappedResponse
		wantRetryAfter string
	}{
		{
			name:           "chat stream 429 with RA",
			failure:        adapter.MappedResponse{HTTPStatus: 429, ErrorCode: "RATE_LIMITED", ErrorType: "rate_limit_error", Message: "Slow.", RetryAfterSeconds: 20},
			wantRetryAfter: "20",
		},
		{
			name:           "message stream 529 with RA",
			failure:        adapter.MappedResponse{HTTPStatus: 529, ErrorCode: "OVERLOADED", ErrorType: "overloaded_error", Message: "Overloaded.", RetryAfterSeconds: 90},
			wantRetryAfter: "90",
		},
		{
			name:           "chat stream 429 without RA",
			failure:        adapter.MappedResponse{HTTPStatus: 429, ErrorCode: "RATE_LIMITED", ErrorType: "rate_limit_error", Message: "Slow."},
			wantRetryAfter: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			streamResult := StreamResult{Failure: &tc.failure}

			if strings.Contains(tc.name, "chat") {
				rec := httptest.NewRecorder()
				resp := RenderChatStreamResult(streamResult)
				if err := resp.VisitCreateChatCompletionResponse(rec); err != nil {
					t.Fatal(err)
				}
				if got := rec.Header().Get("Retry-After"); got != tc.wantRetryAfter {
					t.Errorf("chat stream Retry-After = %q, want %q", got, tc.wantRetryAfter)
				}
			}
			if strings.Contains(tc.name, "message") {
				rec := httptest.NewRecorder()
				resp := RenderMessageStreamResult(streamResult, "req_test")
				if err := resp.VisitCreateMessageResponse(rec); err != nil {
					t.Fatal(err)
				}
				if got := rec.Header().Get("Retry-After"); got != tc.wantRetryAfter {
					t.Errorf("message stream Retry-After = %q, want %q", got, tc.wantRetryAfter)
				}
			}
		})
	}
}

func TestRetryAfterOnResponseStreamFailure(t *testing.T) {
	t.Parallel()
	failure := adapter.MappedResponse{HTTPStatus: 429, ErrorCode: "RATE_LIMITED", ErrorType: "rate_limit_error", Message: "Slow.", RetryAfterSeconds: 25}
	streamResult := StreamResult{Failure: &failure}

	rec := httptest.NewRecorder()
	resp := RenderResponseStreamResult(streamResult)
	if err := resp.VisitCreateResponseResponse(rec); err != nil {
		t.Fatal(err)
	}
	if got := rec.Header().Get("Retry-After"); got != "25" {
		t.Errorf("response stream Retry-After = %q, want %q", got, "25")
	}
}

func TestValidChatMessageToolCallsNull(t *testing.T) {
	t.Parallel()

	// MiMo-style response: tool_calls key present with null value.
	// Per OpenAI spec, tool_calls:null is equivalent to omitting the key.
	mimoRaw := `{"id":"chatcmpl_mimo","object":"chat.completion","created":1,"model":"mimo","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"hello","tool_calls":null}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`

	t.Run("tool_calls null (MiMo regression)", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		result := execution.Result{Completion: sdk.Completion{Status: http.StatusOK, RawJSON: json.RawMessage(mimoRaw)}}
		if err := RenderChatCompletion(result).VisitCreateChatCompletionResponse(rec); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		if rec.Body.String() != mimoRaw {
			t.Fatalf("body mismatch: got %q", rec.Body.String())
		}
	})

	// Standard OpenAI: no tool_calls key at all.
	t.Run("no tool_calls key", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		result := execution.Result{Completion: sdk.Completion{Status: http.StatusOK, RawJSON: json.RawMessage(validChatRaw)}}
		if err := RenderChatCompletion(result).VisitCreateChatCompletionResponse(rec); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	// Non-empty tool_calls array must still pass strict validation.
	t.Run("valid non-empty tool_calls", func(t *testing.T) {
		t.Parallel()
		raw := `{"id":"chatcmpl_tc","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}]}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
		rec := httptest.NewRecorder()
		result := execution.Result{Completion: sdk.Completion{Status: http.StatusOK, RawJSON: json.RawMessage(raw)}}
		if err := RenderChatCompletion(result).VisitCreateChatCompletionResponse(rec); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
	})

	// Invalid tool_calls type must be rejected.
	t.Run("tool_calls is string", func(t *testing.T) {
		t.Parallel()
		raw := `{"id":"chatcmpl_bad","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"hi","tool_calls":"invalid"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
		rec := httptest.NewRecorder()
		result := execution.Result{Completion: sdk.Completion{Status: http.StatusOK, RawJSON: json.RawMessage(raw)}}
		if err := RenderChatCompletion(result).VisitCreateChatCompletionResponse(rec); err != nil {
			t.Fatal(err)
		}
		assertOpenAIError(t, rec, http.StatusInternalServerError, internalErrorCode, "api_error", internalErrorMessage)
	})

	t.Run("tool_calls is number", func(t *testing.T) {
		t.Parallel()
		raw := `{"id":"chatcmpl_bad2","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"hi","tool_calls":42}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
		rec := httptest.NewRecorder()
		result := execution.Result{Completion: sdk.Completion{Status: http.StatusOK, RawJSON: json.RawMessage(raw)}}
		if err := RenderChatCompletion(result).VisitCreateChatCompletionResponse(rec); err != nil {
			t.Fatal(err)
		}
		assertOpenAIError(t, rec, http.StatusInternalServerError, internalErrorCode, "api_error", internalErrorMessage)
	})

	// Empty array tool_calls: validChatToolCalls([]any{}) returns true,
	// so the message passes. This is consistent — an empty array is
	// structurally valid even if semantically unusual.
	t.Run("tool_calls empty array", func(t *testing.T) {
		t.Parallel()
		raw := `{"id":"chatcmpl_empty","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"done","tool_calls":[]}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
		rec := httptest.NewRecorder()
		result := execution.Result{Completion: sdk.Completion{Status: http.StatusOK, RawJSON: json.RawMessage(raw)}}
		if err := RenderChatCompletion(result).VisitCreateChatCompletionResponse(rec); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
	})
}
