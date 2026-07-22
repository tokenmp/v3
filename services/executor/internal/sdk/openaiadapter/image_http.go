package openaiadapter

import (
	"io"
	"net/http"
	"sync"
)

// imageResponseCapture is per-call state: it never shares a response between
// calls and exists only to classify failures with safe HTTP metadata.
type imageResponseCapture struct {
	mu   sync.Mutex
	resp *http.Response
}

func (c *imageResponseCapture) set(r *http.Response) { c.mu.Lock(); c.resp = r; c.mu.Unlock() }
func (c *imageResponseCapture) response() *http.Response {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.resp
}
func (c *imageResponseCapture) status() int {
	if r := c.response(); r != nil {
		return r.StatusCode
	}
	return 0
}
func (c *imageResponseCapture) requestID() string {
	if r := c.response(); r != nil {
		return r.Header.Get("x-request-id")
	}
	return ""
}

type imageCapTransport struct {
	next    http.RoundTripper
	capture *imageResponseCapture
}

func (t imageCapTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r, err := t.next.RoundTrip(req)
	if r == nil {
		return nil, err
	}
	t.capture.set(r)
	if r.Body == nil {
		return r, err
	}
	if r.ContentLength > maxImageWireResponseBytes {
		_ = r.Body.Close()
		return nil, errImageResponseTooLarge
	}
	r.Body = &cappedReadCloser{ReadCloser: r.Body, remaining: maxImageWireResponseBytes}
	return r, err
}

// cappedReadCloser rejects chunked (or lying Content-Length) bodies before a
// decoder can consume more than the adapter's bounded response budget.
type cappedReadCloser struct {
	io.ReadCloser
	remaining int64
	exceeded  bool
}

func (r *cappedReadCloser) Read(p []byte) (int, error) {
	if r.exceeded {
		return 0, errImageResponseTooLarge
	}
	if r.remaining == 0 {
		var probe [1]byte
		n, err := r.ReadCloser.Read(probe[:])
		if n > 0 {
			r.exceeded = true
			return 0, errImageResponseTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.ReadCloser.Read(p)
	r.remaining -= int64(n)
	return n, err
}
