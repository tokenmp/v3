package executorv1api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

func TestNormalizeOpenAIResponsesRejectsInvalidRequests(t *testing.T) {
	for _, body := range []string{
		`{"model":"m"}`,
		`{"model":"m","input":123}`,
		`{"model":"m","input":""}`,
		`{"model":"m","input":"hi","previous_response_id":"resp_1"}`,
		`{"model":"m","input":"hi","store":true}`,
		`{"model":"m","input":"hi","conversation":{"id":"c"}}`,
		`{"model":"m","input":"hi","background":true}`,
		`{"model":"m","input":"hi","include":["file_search"]}`,
		`{"model":"m","input":"hi","moderation":"auto"}`,
		`{"model":"m","input":"hi","prompt":{"id":"p"}}`,
		`{"model":"m","input":"hi","truncation":"auto"}`,
		`{"model":"m","input":"hi","service_tier":"auto"}`,
		`{"model":"m","input":"hi","extra":true}`,
		`{"input":"hi"}`,
		`{"model":123,"input":"hi"}`,
		`{"model":"m","input":"hi","tools":[{"type":"web_search"}]}`,
		`{"model":"m","input":"hi","tool_choice":"never"}`,
		`{"model":"m","input":"hi","reasoning":{"effort":"invalid"}}`,
		`{"model":"m","input":"hi","reasoning":{"summary":"invalid"}}`,
		`{"model":"m","input":"hi","temperature":3}`,
		`{"model":"m","input":"hi","top_p":2}`,
		`{"model":"m","input":"hi","max_output_tokens":-1}`,
		`{"model":"m","input":"hi","metadata":{"extra":1}}`,
		`{"model":"m","input":[{"type":"web_search_call"}]}`,
		`{"model":"m","input":[{"type":"message","role":"invalid","content":"hi"}]}`,
		`{"model":"m","input":[{"type":"message","content":[{"type":"file"}]}]}`,
		`{"model":"m","input":"hi","text":{"format":{"extra":1}}}`,
		`{"model":"m","input":"hi","stream":"yes"}`,
	} {
		t.Run(body, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, openAIResponsesPath, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(withRawBody([]byte(body)))
			_, err := NormalizeOpenAIResponses(req.Context(), "test-id")
			if err == nil {
				t.Fatalf("expected error for: %s", body)
			}
		})
	}
}

func TestNormalizeOpenAIResponsesAcceptsValidRequests(t *testing.T) {
	for _, body := range []string{
		`{"model":"m","input":"hi"}`,
		`{"model":"m","input":"hi","stream":false}`,
		`{"model":"m","input":"hi","stream":true}`,
		`{"model":"m","input":"hi","instructions":"be helpful"}`,
		`{"model":"m","input":"hi","max_output_tokens":100}`,
		`{"model":"m","input":"hi","metadata":{"user_id":"u1"}}`,
		`{"model":"m","input":"hi","reasoning":{"effort":"high"}}`,
		`{"model":"m","input":"hi","reasoning":{"effort":"medium","summary":"auto"}}`,
		`{"model":"m","input":"hi","temperature":0.5}`,
		`{"model":"m","input":"hi","top_p":0.9}`,
		`{"model":"m","input":"hi","tool_choice":"auto"}`,
		`{"model":"m","input":"hi","tool_choice":"none"}`,
		`{"model":"m","input":"hi","tool_choice":"required"}`,
		`{"model":"m","input":"hi","tools":[{"type":"function","name":"f","parameters":{}}]}`,
		`{"model":"m","input":"hi","tools":[{"type":"function","name":"f","parameters":{},"strict":true}]}`,
		`{"model":"m","input":[{"type":"message","role":"user","content":"hi"}]}`,
		`{"model":"m","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`,
		`{"model":"m","input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"https://example.com/img.png"}]}]}`,
		`{"model":"m","input":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}]}`,
		`{"model":"m","input":"hi","text":{"format":{"type":"json_object"}}}`,
	} {
		t.Run(body, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, openAIResponsesPath, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(withRawBody([]byte(body)))
			result, err := NormalizeOpenAIResponsesRequest(req.Context(), "test-id")
			if err != nil {
				t.Fatalf("unexpected error: %v for: %s", err, body)
			}
			if result.Request.Protocol != adapter.ProtocolOpenAIResponses {
				t.Fatalf("protocol = %v, want %v", result.Request.Protocol, adapter.ProtocolOpenAIResponses)
			}
		})
	}
}

func TestDetectOpenAIResponsesStream(t *testing.T) {
	for _, tc := range []struct {
		body   string
		stream bool
		err    bool
	}{
		{`{"model":"m","input":"hi"}`, false, false},
		{`{"model":"m","input":"hi","stream":true}`, true, false},
		{`{"model":"m","input":"hi","stream":false}`, false, false},
		{`invalid`, false, true},
	} {
		t.Run(tc.body, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, openAIResponsesPath, strings.NewReader(tc.body))
			req = req.WithContext(withRawBody([]byte(tc.body)))
			stream, err := DetectOpenAIResponsesStream(req.Context())
			if tc.err {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if stream != tc.stream {
				t.Fatalf("stream = %v, want %v", stream, tc.stream)
			}
		})
	}
}

func TestNormalizeOpenAIResponsesRejectsStreaming(t *testing.T) {
	body := `{"model":"m","input":"hi","stream":true}`
	req := httptest.NewRequest(http.MethodPost, openAIResponsesPath, strings.NewReader(body))
	req = req.WithContext(withRawBody([]byte(body)))
	_, err := NormalizeOpenAIResponses(req.Context(), "test-id")
	if !isErrStreamingUnsupported(err) {
		t.Fatalf("error = %v, want ErrStreamingUnsupported", err)
	}
}

func isErrStreamingUnsupported(err error) bool {
	return err != nil && strings.Contains(err.Error(), "streaming is unsupported")
}
