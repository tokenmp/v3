// Package envelope wraps all JSON responses in a unified
// {code, data, message} envelope. It is designed for contract-first services
// (Auth, Edge keys) that use oapi-codegen generated strict handlers which
// write JSON directly to the ResponseWriter.
//
// For hand-written services, prefer the github.com/tokenmp/v3/packages/go/httpresp
// package which writes the envelope directly.
//
// Behaviour:
//   - 2xx JSON responses → {code:0, data:<original body>, message:"success"}
//   - 4xx/5xx JSON responses → {code:<mapped from status>, data:null, message:<extracted>}
//   - 204 No Content → passed through unchanged (no body)
//   - Non-JSON responses (e.g. text/plain, SSE) → passed through unchanged
package envelope

import (
	"bytes"
	"encoding/json"
	"net/http"
)

// codeFromStatus maps an HTTP status to a numeric envelope code.
func codeFromStatus(status int) int {
	switch {
	case status >= 200 && status < 300:
		return 0
	case status == 400:
		return 1000
	case status == 401:
		return 1007
	case status == 403:
		return 1008
	case status == 404:
		return 1009
	case status == 409:
		return 1010
	case status == 503:
		return 1012
	case status == 502:
		return 1016
	case status == 501:
		return 1017
	case status >= 500:
		return 1011
	default:
		return 1000
	}
}

// extractErrorMessage tries to extract a human-readable message from an
// error response body. It handles the legacy {error:{code,message}} and
// {error:"code"} formats.
func extractErrorMessage(body []byte) string {
	if len(body) == 0 {
		return "error"
	}
	var legacy1 struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &legacy1) == nil && legacy1.Error.Message != "" {
		return legacy1.Error.Message
	}
	var legacy2 struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &legacy2) == nil && legacy2.Error != "" {
		return legacy2.Error
	}
	// OpenAI-style: {error:{message,type,code,param}, status}
	var openai struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &openai) == nil && openai.Error.Message != "" {
		return openai.Error.Message
	}
	return "error"
}

// captureWriter buffers the response body and headers so the middleware
// can inspect and re-wrap it before sending to the client.
type captureWriter struct {
	header http.Header
	buf    bytes.Buffer
	status int
}

func (cw *captureWriter) Header() http.Header {
	if cw.header == nil {
		cw.header = http.Header{}
	}
	return cw.header
}

func (cw *captureWriter) WriteHeader(status int) {
	cw.status = status
}

func (cw *captureWriter) Write(b []byte) (int, error) {
	return cw.buf.Write(b)
}

// Wrap returns middleware that envelopes JSON responses.
// Non-JSON and 204 responses are passed through.
func Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cw := &captureWriter{}
		next.ServeHTTP(cw, r)

		status := cw.status
		if status == 0 {
			status = http.StatusOK
		}

		ct := cw.Header().Get("Content-Type")

		// 204 No Content: write status + headers, no body.
		if status == http.StatusNoContent {
			copyHeaders(w.Header(), cw.Header())
			w.WriteHeader(status)
			return
		}

		// Non-JSON: pass through as-is.
		if !isJSON(ct) || cw.buf.Len() == 0 {
			copyHeaders(w.Header(), cw.Header())
			w.WriteHeader(status)
			_, _ = w.Write(cw.buf.Bytes())
			return
		}

		// JSON response — wrap in envelope.
		var envBody []byte
		if status >= 200 && status < 300 {
			// Success: wrap in {code:0, data:<body>, message:"success"}.
			var raw json.RawMessage
			if json.Unmarshal(cw.buf.Bytes(), &raw) != nil {
				// not valid JSON, pass through
				copyHeaders(w.Header(), cw.Header())
				w.WriteHeader(status)
				_, _ = w.Write(cw.buf.Bytes())
				return
			}
			envBody, _ = json.Marshal(map[string]any{
				"code":    0,
				"data":    raw,
				"message": "success",
			})
		} else {
			// Error: wrap in {code:<mapped>, data:<original body>, message:<extracted>}.
			// Preserve original body as data so clients can access error details.
			var raw json.RawMessage
			if json.Unmarshal(cw.buf.Bytes(), &raw) == nil {
				envBody, _ = json.Marshal(map[string]any{
					"code":    codeFromStatus(status),
					"data":    raw,
					"message": extractErrorMessage(cw.buf.Bytes()),
				})
			} else {
				envBody, _ = json.Marshal(map[string]any{
					"code":    codeFromStatus(status),
					"data":    nil,
					"message": "error",
				})
			}
		}

		// Write wrapped response.
		hdr := w.Header()
		copyHeaders(hdr, cw.Header())
		hdr.Del("Content-Length")
		hdr.Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write(envBody)
	})
}

func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		dst[k] = vs
	}
}

func isJSON(ct string) bool {
	return ct == "application/json" || ct == "application/json; charset=utf-8" || ct == "application/json;charset=utf-8"
}
