package openaiadapter

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

func FuzzDecodeResponseParams(f *testing.F) {
	f.Add([]byte(`{"model":"m","input":"hi"}`))
	f.Add([]byte(`{"model":"m","input":[{"type":"message","role":"user","content":"hi"}]}`))
	f.Add([]byte(`{"model":"m","input":"hi","previous_response_id":"resp_1"}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		_, _ = decodeResponseParams(context.Background(), body)
	})
}

func FuzzValidateResponseResponse(f *testing.F) {
	f.Add([]byte(`{"id":"r","object":"response","status":"completed","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`))
	f.Add([]byte(`{"id":"r","object":"response","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_ = validateResponseResponse(context.Background(), raw)
	})
}

// FuzzCappedReadCloser is kept referenced alongside other response fuzz tests
// via the shared cappedReadCloser already covered in images_fuzz_test.go; here
// we add a small no-op body to keep this file focused on Responses.
var _ = io.CopyN

func TestValidateResponseResponseCaps(t *testing.T) {
	minimal := []byte(`{"id":"r","object":"response","status":"completed","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`)
	atCap := append(bytes.Repeat([]byte(" "), maxResponseWireResponseBytes-len(minimal)), minimal...)
	if err := validateResponseResponse(context.Background(), atCap); err != nil {
		t.Fatalf("wire exact cap: %v", err)
	}
	if err := validateResponseResponse(context.Background(), append([]byte(" "), atCap...)); !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("wire cap + 1: %v", err)
	}
}
