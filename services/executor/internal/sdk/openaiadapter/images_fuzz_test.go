package openaiadapter

import (
	"bytes"
	"context"
	"io"
	"testing"
)

func FuzzDecodeImageParams(f *testing.F) {
	f.Add([]byte(`{"model":"m","prompt":"x","n":1}`))
	f.Add([]byte(`{"model":"m","prompt":"x","response_format":"b64_json"}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		_, _, _ = decodeImageParams(context.Background(), body)
	})
}

func FuzzValidateImageResponse(f *testing.F) {
	f.Add([]byte(`{"created":1,"data":[{"url":"https://example.test/a"}]}`), "")
	f.Add([]byte(`{"created":1,"data":[{"b64_json":"aA=="}]}`), "b64_json")
	f.Fuzz(func(t *testing.T, raw []byte, format string) {
		if len(format) > 16 {
			format = ""
		}
		_ = validateImageResponse(context.Background(), raw, format)
	})
}

func FuzzCappedReadCloser(f *testing.F) {
	f.Add([]byte("image"), int64(3))
	f.Fuzz(func(t *testing.T, body []byte, limit int64) {
		if len(body) > 4096 {
			body = body[:4096]
		}
		if limit < 0 {
			limit = 0
		}
		if limit > 4096 {
			limit = 4096
		}
		r := &cappedReadCloser{ReadCloser: io.NopCloser(bytes.NewReader(body)), remaining: limit}
		_, _ = io.ReadAll(r)
		_ = r.Close()
	})
}
