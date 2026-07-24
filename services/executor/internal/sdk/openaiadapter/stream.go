package openaiadapter

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

// openingCapture is deliberately allocated per Stream call. NewStreaming
// keeps its response local, so option.WithResponseInto cannot observe it; this
// RoundTripper captures the exact response without shared cross-call state.
type openingCapture struct {
	mu       sync.Mutex
	response *http.Response
	observer *sseObserver
}

func (c *openingCapture) RoundTrip(req *http.Request, next http.RoundTripper) (*http.Response, error) {
	resp, err := next.RoundTrip(req)
	if resp != nil {
		if resp.Body != nil {
			resp.Body = observingBody(resp.Body, c.observer)
		}
		c.mu.Lock()
		c.response = resp
		c.mu.Unlock()
	}
	return resp, err
}
func (c *openingCapture) get() *http.Response { c.mu.Lock(); defer c.mu.Unlock(); return c.response }

// Stream opens exactly one official OpenAI Chat Completions stream. It uses
// NewStreaming as the primary API, disables retries and redirects, and returns
// only safe opening metadata.
func (c *Client) Stream(ctx context.Context, call sdk.StreamCall) (sdk.StreamOpen, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if call.Target.Protocol != adapter.ProtocolOpenAIChat && call.Target.Protocol != adapter.ProtocolOpenAIResponses {
		return sdk.StreamOpen{}, ErrUnsupportedProtocol
	}
	if call.Target.Protocol == adapter.ProtocolOpenAIResponses {
		return c.streamResponse(ctx, call)
	}
	if err := ctx.Err(); err != nil {
		return sdk.StreamOpen{}, classifyContextError(err)
	}
	baseURL, err := parseBaseURL(call.Target.BaseURL)
	if err != nil {
		return sdk.StreamOpen{}, ErrInvalidBaseURL
	}
	if strings.TrimSpace(call.Target.UpstreamModel) == "" {
		return sdk.StreamOpen{}, ErrMissingUpstreamModel
	}
	if err := validateInjection(call.Request.InjectionPlan); err != nil {
		return sdk.StreamOpen{}, ErrInvalidInjection
	}
	params, err := decodeChatParams(ctx, call.Request.Body, call.Request.Thinking)
	if err != nil {
		return sdk.StreamOpen{}, ErrInvalidRequest
	}
	params.Model = openai.ChatModel(call.Target.UpstreamModel)

	var apiKey string
	if err := call.Secret.Use(func(key []byte) error {
		if strings.TrimSpace(string(key)) == "" {
			return ErrMissingAPIKey
		}
		apiKey = string(key)
		return nil
	}); err != nil {
		return sdk.StreamOpen{}, err
	}

	openingCtx, cancel := context.WithCancel(ctx)
	observer := &sseObserver{}
	capture := &openingCapture{observer: observer}
	base := c.perCallHTTPClient(sdk.Call{Candidate: call.Candidate, Target: call.Target, Request: call.Request, Secret: call.Secret}, apiKey)
	perCall := &http.Client{Transport: captureTransport{capture: capture, next: base.Transport}, CheckRedirect: base.CheckRedirect, Jar: base.Jar, Timeout: base.Timeout}
	opts := []option.RequestOption{option.WithBaseURL(baseURL.String()), option.WithHTTPClient(perCall), option.WithMaxRetries(0), option.WithAPIKey(apiKey)}
	opts = append(opts, injectionOptions(call.Request.InjectionPlan)...)
	opts = append(opts, option.WithHeader("Authorization", "Bearer "+apiKey))
	client := openai.NewClient(opts...)
	stream := client.Chat.Completions.NewStreaming(openingCtx, params)
	response := capture.get()
	if response == nil || response.StatusCode < 200 || response.StatusCode >= 300 || stream.Err() != nil {
		cancel()
		if stream != nil {
			_ = stream.Close()
		}
		if err := ctx.Err(); err != nil {
			return sdk.StreamOpen{}, classifyContextError(err)
		}
		if response != nil && (response.StatusCode < 200 || response.StatusCode >= 300) {
			kind := kindForHTTPStatus(response.StatusCode)
			reqID := response.Header.Get("x-request-id")
			if isRetryableHTTPStatus(response.StatusCode) {
				if ra, ok := sdk.ParseRetryAfter(response.Header); ok {
					return sdk.StreamOpen{}, sdk.NewClassifiedErrorWithRetryAfter(kind, response.StatusCode, reqID, "", "", ra, true)
				}
			}
			return sdk.StreamOpen{}, sdk.NewClassifiedError(kind, response.StatusCode, reqID, "", "")
		}
		if stream != nil && stream.Err() != nil {
			return sdk.StreamOpen{}, classifyStreamOpenError(stream.Err(), response)
		}
		return sdk.StreamOpen{}, sdk.NewClassifiedError(sdk.ErrTransport, 0, "", "", "")
	}
	return sdk.StreamOpen{Source: newChunkSource(stream, cancel, observer), Status: response.StatusCode, RequestID: sdk.SafeRequestID(response.Header.Get("x-request-id"))}, nil
}

type captureTransport struct {
	capture *openingCapture
	next    http.RoundTripper
}

func (t captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.capture.RoundTrip(req, t.next)
}

func classifyStreamOpenError(err error, response *http.Response) *sdk.ClassifiedError {
	if errors.Is(err, context.DeadlineExceeded) {
		return sdk.NewClassifiedError(context.DeadlineExceeded, 0, "", "", "")
	}
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		return classifyError(err, response)
	}
	if response != nil {
		status := response.StatusCode
		requestID := response.Header.Get("x-request-id")
		kind := kindForHTTPStatus(status)
		if isRetryableHTTPStatus(status) {
			if ra, ok := sdk.ParseRetryAfter(response.Header); ok {
				return sdk.NewClassifiedErrorWithRetryAfter(kind, status, requestID, "", "", ra, true)
			}
		}
		return sdk.NewClassifiedError(sdk.ErrProtocol, status, requestID, "", "")
	}
	return sdk.NewClassifiedError(sdk.ErrTransport, 0, "", "", "")
}

var _ sdk.StreamClient = (*Client)(nil)
