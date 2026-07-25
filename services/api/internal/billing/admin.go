package billing

import (
	"bytes"
	"context"
	"io"
	"net/http"
)

// ProxyAdmin forwards an admin request to the Billing Service and returns
// the raw response body, status code, and content type. This is a transparent
// proxy: the Edge does not interpret the admin request/response shape, only
// relays it. The method is one of http.MethodGet, http.MethodPost, etc.
// path is the full path beginning with "/v1/billing/admin/".
// body may be nil for GET/DELETE.
func (c *Client) ProxyAdmin(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	if !c.Available() {
		return 0, nil, ErrUnavailable
	}
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return 0, nil, ErrUnavailable
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, data, nil
}
