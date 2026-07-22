package anthropicadapter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

// Complete performs one non-streaming Anthropic Messages call. The target,
// secret and SDK client are all call-local; no process environment is mutated.
func (c *Client) Complete(ctx context.Context, call sdk.Call) (sdk.Completion, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if call.Target.Protocol != adapter.ProtocolAnthropic {
		return sdk.Completion{}, ErrUnsupportedProtocol
	}
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
	params, err := decodeMessageParams(call.Request.Body, call.Request.Thinking, call.Target.UpstreamModel)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return sdk.Completion{}, classifyContextError(ctxErr)
		}
		return sdk.Completion{}, ErrInvalidRequest
	}
	// decodeMessageParams rebuilds model and thinking from execution-
	// authoritative values (target model + EffectiveThinking), so the caller's
	// body can never override them.

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

	// option.WithoutEnvironmentDefaults makes anthropic.NewClient skip its
	// environment autoload (ANTHROPIC_API_KEY, ANTHROPIC_AUTH_TOKEN,
	// ANTHROPIC_BASE_URL, ANTHROPIC_PROFILE, ANTHROPIC_CUSTOM_HEADERS), so
	// only the explicit call-local credential and target reach the SDK. The
	// remaining options pin the call-local target, transport, retry budget,
	// credential, and non-stream mode; sanitizingRoundTripper is the final
	// defense-in-depth boundary that rebuilds the protocol allowlist.
	callOpts := []option.RequestOption{
		option.WithoutEnvironmentDefaults(),
		option.WithBaseURL(baseURL.String()),
		option.WithHTTPClient(c.perCallHTTPClient(call, apiKey, "application/json")),
		option.WithMaxRetries(0),
		option.WithAPIKey(apiKey),
		option.WithJSONSet("stream", false),
	}
	callOpts = append(callOpts, injectionOptions(call.Request.InjectionPlan)...)
	// Ensure the explicit credential is last among regular SDK options too.
	callOpts = append(callOpts, option.WithHeader("x-api-key", apiKey))

	client := anthropic.NewClient(callOpts...)
	var response *http.Response
	res, err := client.Messages.New(ctx, params, option.WithResponseInto(&response))
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
	raw := []byte(res.RawJSON())
	if err := validateMessageResponse(raw); err != nil {
		status, requestID := 0, ""
		if response != nil {
			status = response.StatusCode
			requestID = response.Header.Get("request-id")
		}
		return sdk.Completion{}, sdk.NewClassifiedError(sdk.ErrProtocol, status, requestID, "", "")
	}
	completion := sdk.Completion{RawJSON: json.RawMessage(raw)}
	if response != nil {
		completion.Status = response.StatusCode
		completion.RequestID = sdk.SafeRequestID(response.Header.Get("request-id"))
	}
	return completion, nil
}

func classifyContextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return sdk.NewClassifiedError(context.DeadlineExceeded, 0, "", "", "")
	}
	return err
}

func classifyError(err error, response *http.Response) *sdk.ClassifiedError {
	var apiErr *anthropic.Error
	status, requestID, code, typ := 0, "", "", ""
	if response != nil {
		status = response.StatusCode
		requestID = response.Header.Get("request-id")
	}
	if errors.As(err, &apiErr) {
		status, typ = apiErr.StatusCode, string(apiErr.Type())
		if apiErr.RequestID != "" {
			requestID = apiErr.RequestID
		}
		// Anthropic errors carry no "code" field; the error type is the only
		// sanitized classifier. RawJSON is never retained or echoed.
	}

	if status >= 200 && status < 300 {
		// An HTTP success that the SDK cannot decode is a provider protocol
		// violation, never a successful completion or a transport failure.
		return sdk.NewClassifiedError(sdk.ErrProtocol, status, requestID, "", "")
	}
	if status == 0 {
		return sdk.NewClassifiedError(sdk.ErrTransport, 0, "", "", "")
	}
	return sdk.NewClassifiedError(kindForHTTPStatus(status), status, requestID, code, typ)
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
		// Anthropic returns 529 for overloaded_error; it is a 5xx outcome and
		// maps to unavailable so the retry layer can treat it uniformly.
		return sdk.ErrUnavailable
	default:
		return sdk.ErrUpstream
	}
}
