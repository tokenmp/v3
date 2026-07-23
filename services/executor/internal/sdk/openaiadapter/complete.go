package openaiadapter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

// Complete performs exactly one non-streaming official OpenAI SDK operation.
// The target, secret and SDK client are all call-local; no process environment
// is mutated. Chat behavior remains in completeChat; Images is intentionally an
// internal capability only until a later transport/composition phase.
func (c *Client) Complete(ctx context.Context, call sdk.Call) (sdk.Completion, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	switch call.Target.Protocol {
	case adapter.ProtocolOpenAIChat:
		return c.completeChat(ctx, call)
	case adapter.ProtocolOpenAIImages:
		return c.completeImage(ctx, call)
	case adapter.ProtocolOpenAIResponses:
		return c.completeResponse(ctx, call)
	default:
		return sdk.Completion{}, ErrUnsupportedProtocol
	}
}

func (c *Client) completeChat(ctx context.Context, call sdk.Call) (sdk.Completion, error) {
	if err := ctx.Err(); err != nil {
		return sdk.Completion{}, classifyContextError(err)
	}
	baseURL, err := parseBaseURL(call.Target.BaseURL)
	if err != nil {
		return sdk.Completion{}, ErrInvalidBaseURL
	}
	if strings.TrimSpace(call.Target.UpstreamModel) == "" {
		return sdk.Completion{}, ErrMissingUpstreamModel
	}
	if err := validateInjection(call.Request.InjectionPlan); err != nil {
		return sdk.Completion{}, ErrInvalidInjection
	}
	params, err := decodeChatParams(ctx, call.Request.Body)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return sdk.Completion{}, classifyContextError(ctxErr)
		}
		return sdk.Completion{}, ErrInvalidRequest
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
		return sdk.Completion{}, err
	}

	// v3.44 NewClient always reads environment defaults. Explicit options win
	// for configuration, while sanitizingRoundTripper below removes all
	// environment-derived headers at the actual send boundary.
	callOpts := []option.RequestOption{
		option.WithBaseURL(baseURL.String()),
		option.WithHTTPClient(c.perCallHTTPClient(call, apiKey)),
		option.WithMaxRetries(0),
		option.WithAPIKey(apiKey),
		option.WithJSONSet("stream", false),
	}
	callOpts = append(callOpts, injectionOptions(call.Request.InjectionPlan)...)
	// Ensure the explicit credential is last among regular SDK options too.
	callOpts = append(callOpts, option.WithHeader("Authorization", "Bearer "+apiKey))

	client := openai.NewClient(callOpts...)
	var response *http.Response
	res, err := client.Chat.Completions.New(ctx, params, option.WithResponseInto(&response))
	if err != nil {
		// Cancellation remains the original context sentinel so callers can stop
		// control flow without treating it as an upstream failure. A deadline is
		// a retry-relevant upstream timeout, represented safely while
		// ClassifiedError.Is still matches context.DeadlineExceeded.
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return sdk.Completion{}, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return sdk.Completion{}, sdk.NewClassifiedError(context.DeadlineExceeded, 0, "", "", "")
		}
		return sdk.Completion{}, classifyError(err, response)
	}
	if err := ctx.Err(); err != nil {
		return sdk.Completion{}, classifyContextError(err)
	}
	completion := sdk.Completion{RawJSON: json.RawMessage(res.RawJSON())}
	if response != nil {
		completion.Status = response.StatusCode
		completion.RequestID = sdk.SafeRequestID(response.Header.Get("x-request-id"))
	}
	completion.Usage, completion.Known = extractOpenAIChatUsage(completion.RawJSON)
	return completion, nil
}

func classifyContextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return sdk.NewClassifiedError(context.DeadlineExceeded, 0, "", "", "")
	}
	return err
}

// extractOpenAIChatUsage extracts usage counters from the raw OpenAI Chat
// Completions response JSON. It performs strict validation: all three
// counters must be present as non-negative integers, each ≤ 1e6, and
// prompt+completion==total. Any missing, negative, inconsistent, or
// out-of-bounds value results in Known=false so the runner falls back to
// unpriced success. Extra fields (e.g. completion_tokens_details) are
// silently ignored.
func extractOpenAIChatUsage(raw json.RawMessage) (sdk.Usage, bool) {
	if len(raw) == 0 {
		return sdk.Usage{}, false
	}
	var wrapper struct {
		Usage *struct {
			PromptTokens     *int64 `json:"prompt_tokens"`
			CompletionTokens *int64 `json:"completion_tokens"`
			TotalTokens      *int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return sdk.Usage{}, false
	}
	if wrapper.Usage == nil {
		return sdk.Usage{}, false
	}
	u := wrapper.Usage
	if u.PromptTokens == nil || u.CompletionTokens == nil || u.TotalTokens == nil {
		return sdk.Usage{}, false
	}
	if *u.PromptTokens < 0 || *u.CompletionTokens < 0 || *u.TotalTokens < 0 {
		return sdk.Usage{}, false
	}
	usage := sdk.Usage{
		PromptTokens:     uint64(*u.PromptTokens),
		CompletionTokens: uint64(*u.CompletionTokens),
		TotalTokens:      uint64(*u.TotalTokens),
	}
	if !usage.Valid() {
		return sdk.Usage{}, false
	}
	return usage, true
}

func classifyError(err error, response *http.Response) *sdk.ClassifiedError {
	var apiErr *openai.Error
	status, requestID, code, typ := 0, "", "", ""
	if response != nil {
		status = response.StatusCode
		requestID = response.Header.Get("x-request-id")
	}
	if errors.As(err, &apiErr) {
		status, code, typ = apiErr.StatusCode, apiErr.Code, apiErr.Type
		if apiErr.Response != nil {
			requestID = apiErr.Response.Header.Get("x-request-id")
		}
	}

	if status >= 200 && status < 300 {
		// An HTTP success that the SDK cannot decode is a provider protocol
		// violation, never a successful completion or a transport failure.
		return sdk.NewClassifiedError(sdk.ErrProtocol, status, requestID, "", "")
	}
	if status == 0 {
		return sdk.NewClassifiedError(sdk.ErrTransport, 0, "", "", "")
	}
	kind := kindForHTTPStatus(status)
	// Parse Retry-After only for retryable statuses (429, 5xx).
	if isRetryableHTTPStatus(status) && response != nil {
		if ra, ok := sdk.ParseRetryAfter(response.Header); ok {
			return sdk.NewClassifiedErrorWithRetryAfter(kind, status, requestID, code, typ, ra, true)
		}
	}
	return sdk.NewClassifiedError(kind, status, requestID, code, typ)
}

// isRetryableHTTPStatus reports whether the HTTP status is retryable and
// therefore Retry-After header parsing is applicable.
func isRetryableHTTPStatus(status int) bool {
	return status == http.StatusTooManyRequests || (status >= 500 && status < 600)
}

func kindForHTTPStatus(status int) error {
	switch {
	case status == http.StatusUnauthorized:
		return sdk.ErrUnauthorized
	case status == http.StatusForbidden:
		return sdk.ErrForbidden
	case status == http.StatusNotFound:
		return sdk.ErrNotFound
	case status == http.StatusTooManyRequests:
		return sdk.ErrRateLimited
	case status >= 500 && status < 600:
		return sdk.ErrUnavailable
	default:
		return sdk.ErrUpstream
	}
}
