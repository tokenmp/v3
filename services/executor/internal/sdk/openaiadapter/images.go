package openaiadapter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

const (
	// The wire cap bounds the SDK's raw JSON, typed response, validation tree,
	// and the final owned Completion payload to a manageable peak.
	maxImageWireResponseBytes     = 16 << 20
	maxImagePromptBytes           = 1 << 20
	maxImageUserBytes             = 512
	maxImageURLBytes              = 16 << 10
	maxRevisedPromptBytes         = 64 << 10
	maxImageExtensionValueBytes   = 64 << 10
	maxDecodedImageDataBytes      = 10 << 20
	maxDecodedImageAggregateBytes = 12 << 20
)

var errImageResponseTooLarge = errors.New("openaiadapter: image response exceeds limit")

// completeImage performs exactly one official Images.Generate invocation.
func (c *Client) completeImage(ctx context.Context, call sdk.Call) (sdk.Completion, error) {
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
	params, effectiveResponseFormat, err := decodeImageParams(ctx, call.Request.Body)
	if err != nil {
		return sdk.Completion{}, ErrInvalidRequest
	}
	params.Model = openai.ImageModel(call.Target.UpstreamModel)
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

	capture := &imageResponseCapture{}
	base := c.perCallHTTPClient(call, apiKey)
	perCall := &http.Client{Transport: imageCapTransport{next: base.Transport, capture: capture}, CheckRedirect: base.CheckRedirect, Jar: base.Jar, Timeout: base.Timeout}
	opts := []option.RequestOption{option.WithBaseURL(baseURL.String()), option.WithHTTPClient(perCall), option.WithMaxRetries(0), option.WithAPIKey(apiKey)}
	opts = append(opts, injectionOptions(call.Request.InjectionPlan)...)
	opts = append(opts, option.WithHeader("Authorization", "Bearer "+apiKey))
	client := openai.NewClient(opts...)
	res, err := client.Images.Generate(ctx, params)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return sdk.Completion{}, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return sdk.Completion{}, sdk.NewClassifiedError(context.DeadlineExceeded, 0, "", "", "")
		}
		if errors.Is(err, errImageResponseTooLarge) {
			return sdk.Completion{}, sdk.NewClassifiedError(sdk.ErrProtocol, capture.status(), capture.requestID(), "", "")
		}
		return sdk.Completion{}, classifyError(err, capture.response())
	}
	if err := ctx.Err(); err != nil {
		return sdk.Completion{}, classifyContextError(err)
	}
	// RawJSON returns a string. This conversion is the single deliberate owned
	// copy: validate exactly the bytes that Completion retains after the SDK's
	// typed response is out of scope.
	raw := []byte(res.RawJSON())
	if err := validateImageResponse(ctx, raw, effectiveResponseFormat); err != nil {
		return sdk.Completion{}, sdk.NewClassifiedError(sdk.ErrProtocol, capture.status(), capture.requestID(), "", "")
	}
	return sdk.Completion{RawJSON: json.RawMessage(raw), Status: capture.status(), RequestID: sdk.SafeRequestID(capture.requestID())}, nil
}

func decodeImageParams(ctx context.Context, body []byte) (openai.ImageGenerateParams, string, error) {
	var p openai.ImageGenerateParams
	if len(body) == 0 || len(body) > maxParamBodyBytes || !utf8.Valid(body) {
		return p, "", ErrInvalidRequest
	}
	v, err := parseStrictJSON(ctx, body)
	if err != nil {
		return p, "", err
	}
	r, ok := v.(map[string]any)
	if !ok || !onlyFields(r, fieldSet("model", "prompt", "n", "size", "quality", "response_format", "style", "user")) {
		return p, "", ErrInvalidRequest
	}
	model, mok := r["model"].(string)
	prompt, pok := r["prompt"].(string)
	if !mok || model == "" || !pok || prompt == "" || len(prompt) > maxImagePromptBytes ||
		!optionalIntegerRange(r, "n", 1, 10) || !optionalEnum(r, "size", "256x256", "512x512", "1024x1024", "1792x1024", "1024x1792") ||
		!optionalEnum(r, "quality", "standard", "hd") || !optionalEnum(r, "response_format", "url", "b64_json") || !optionalEnum(r, "style", "vivid", "natural") {
		return p, "", ErrInvalidRequest
	}
	if user, ok := r["user"]; ok {
		s, ok := user.(string)
		if !ok || len(s) > maxImageUserBytes || hasCTL(s) {
			return p, "", ErrInvalidRequest
		}
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return p, "", err
	}
	effectiveFormat := "url"
	if format, exists := r["response_format"].(string); exists {
		effectiveFormat = format
	}
	// The contract's default is observable on the upstream wire as well as in
	// response validation; omitzero would otherwise silently omit it.
	p.ResponseFormat = openai.ImageGenerateParamsResponseFormat(effectiveFormat)
	return p, effectiveFormat, nil
}

func optionalIntegerRange(r map[string]any, name string, low, high int64) bool {
	v, ok := r[name]
	if !ok {
		return true
	}
	n, ok := v.(json.Number)
	if !ok {
		return false
	}
	i, err := n.Int64()
	return err == nil && i >= low && i <= high
}

func validateImageResponse(ctx context.Context, raw []byte, wanted string) error {
	if len(raw) == 0 || len(raw) > maxImageWireResponseBytes || !utf8.Valid(raw) {
		return errImageResponseTooLarge
	}
	v, err := parseStrictJSON(ctx, raw)
	if err != nil {
		return errImageResponseTooLarge
	}
	r, ok := v.(map[string]any)
	if !ok || len(r) > 32 || !boundedExtensions(r, fieldSet("created", "data", "usage")) {
		return errImageResponseTooLarge
	}
	if !nonnegativeJSONInteger(r["created"]) {
		return errImageResponseTooLarge
	}
	data, ok := r["data"].([]any)
	if !ok || len(data) < 1 || len(data) > 10 {
		return errImageResponseTooLarge
	}
	if usage, exists := r["usage"]; exists && !validImageUsage(usage) {
		return errImageResponseTooLarge
	}
	kind := ""
	total := int64(0)
	for _, value := range data {
		item, ok := value.(map[string]any)
		if !ok || !onlyFields(item, fieldSet("url", "b64_json", "revised_prompt")) {
			return errImageResponseTooLarge
		}
		u, hasURL := item["url"].(string)
		b, hasB64 := item["b64_json"].(string)
		if hasURL == hasB64 || (hasURL && !validImageURL(u)) {
			return errImageResponseTooLarge
		}
		thisKind := "url"
		if hasB64 {
			thisKind = "b64_json"
			n, err := decodedBase64Bytes(b, maxDecodedImageDataBytes)
			if err != nil || !withinDecodedImageAggregate(total, n) {
				return errImageResponseTooLarge
			}
			total += n
		}
		if kind == "" {
			kind = thisKind
		} else if kind != thisKind {
			return errImageResponseTooLarge
		}
		if revised, exists := item["revised_prompt"]; exists {
			s, ok := revised.(string)
			if !ok || len(s) > maxRevisedPromptBytes || hasCTL(s) {
				return errImageResponseTooLarge
			}
		}
	}
	if wanted != "" && wanted != kind {
		return errImageResponseTooLarge
	}
	return nil
}

func withinDecodedImageAggregate(total, next int64) bool {
	return next >= 0 && total <= maxDecodedImageAggregateBytes-next
}

func validImageURL(s string) bool {
	if len(s) == 0 || len(s) > maxImageURLBytes {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Fragment == ""
}
func decodedBase64Bytes(s string, max int64) (int64, error) {
	n, err := io.Copy(io.Discard, io.LimitReader(base64.NewDecoder(base64.StdEncoding, strings.NewReader(s)), max+1))
	if err != nil || n > max {
		return 0, errImageResponseTooLarge
	}
	return n, nil
}
func validImageUsage(v any) bool {
	o, ok := v.(map[string]any)
	if !ok || len(o) > 16 || !boundedExtensions(o, fieldSet("input_tokens", "output_tokens", "total_tokens", "input_tokens_details", "output_tokens_details")) {
		return false
	}
	for _, k := range []string{"input_tokens", "output_tokens", "total_tokens"} {
		if x, exists := o[k]; exists && !nonnegativeJSONInteger(x) {
			return false
		}
	}
	for _, k := range []string{"input_tokens_details", "output_tokens_details"} {
		if x, exists := o[k]; exists {
			details, ok := x.(map[string]any)
			if !ok || len(details) > 8 || !boundedExtensions(details, fieldSet("image_tokens", "text_tokens")) {
				return false
			}
			for name, value := range details {
				if (name == "image_tokens" || name == "text_tokens") && !nonnegativeJSONInteger(value) {
					return false
				}
			}
		}
	}
	return true
}

// boundedExtensions keeps provider-defined fields extensible without allowing
// one ignored value to dominate the validation tree or Completion payload.
func boundedExtensions(object map[string]any, known map[string]struct{}) bool {
	for key, value := range object {
		if _, ok := known[key]; !ok && jsonValueSize(value, maxImageExtensionValueBytes) > maxImageExtensionValueBytes {
			return false
		}
	}
	return true
}

// jsonValueSize computes a conservative serialized JSON size without making a
// second attacker-sized serialization. It intentionally overcounts some
// Unicode escapes, which is safe for extension-size enforcement.
func jsonValueSize(value any, limit int) int {
	var size func(any, int) int
	size = func(v any, used int) int {
		if used > limit {
			return used
		}
		switch x := v.(type) {
		case nil:
			return used + 4
		case bool:
			if x {
				return used + 4
			}
			return used + 5
		case json.Number:
			return used + len(x)
		case string:
			used += 2
			for _, r := range x {
				switch {
				case r < 0x20, r == '"', r == '\\', r == '<', r == '>', r == '&', r == 0x2028, r == 0x2029:
					used += 6
				default:
					used += utf8.RuneLen(r)
				}
				if used > limit {
					return used
				}
			}
			return used
		case []any:
			used++
			for i, item := range x {
				if i > 0 {
					used++
				}
				used = size(item, used)
				if used > limit {
					return used
				}
			}
			return used + 1
		case map[string]any:
			used++
			for key, item := range x {
				if used > limit {
					return used
				}
				if used > 1 {
					used++
				}
				used = size(key, used) + 1
				used = size(item, used)
			}
			return used + 1
		default:
			return limit + 1
		}
	}
	return size(value, 0)
}

func nonnegativeJSONInteger(v any) bool {
	n, ok := v.(json.Number)
	if !ok {
		return false
	}
	i, err := n.Int64()
	return err == nil && i >= 0
}
