package openaiadapter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tokenmp/v3/services/executor/internal/imagecontract"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

const (
	// Compatibility aliases keep package-local tests focused on the shared contract caps.
	maxDecodedImageDataBytes      = imagecontract.MaxDecodedDataBytes
	maxDecodedImageAggregateBytes = imagecontract.MaxDecodedAggregateBytes
	maxImageExtensionValueBytes   = imagecontract.MaxExtensionValueBytes

	// The wire cap bounds the SDK's raw JSON, typed response, validation tree,
	// and the final owned Completion payload to a manageable peak.
	maxImageWireResponseBytes = imagecontract.MaxWireResponseBytes
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
	if !ok {
		return p, "", ErrInvalidRequest
	}
	effectiveFormat, ok := imagecontract.ValidateRequest(r)
	if !ok {
		return p, "", ErrInvalidRequest
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return p, "", err
	}
	p.ResponseFormat = openai.ImageGenerateParamsResponseFormat(effectiveFormat)
	return p, effectiveFormat, nil
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
	if !ok || !imagecontract.ValidateResponse(r, wanted) {
		return errImageResponseTooLarge
	}
	return nil
}

// decodedBase64Bytes remains a small test seam; production response validation
// is owned by imagecontract and uses the same streaming decoder pattern.
func decodedBase64Bytes(s string, max int64) (int64, error) {
	n, err := io.CopyN(io.Discard, base64.NewDecoder(base64.StdEncoding, strings.NewReader(s)), max+1)
	if (err != nil && err != io.EOF) || n > max {
		return 0, errImageResponseTooLarge
	}
	return n, nil
}
func withinDecodedImageAggregate(total, next int64) bool {
	return next >= 0 && total <= maxDecodedImageAggregateBytes-next
}
