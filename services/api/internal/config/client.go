package config

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Client calls the Config Service admin API.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient returns a Config Service client. baseURL should not have trailing slash.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		http:    &http.Client{},
	}
}

// do performs an HTTP request and returns the raw response body.
// On non-2xx it returns a compact error without leaking the upstream URL.
func (c *Client) do(ctx context.Context, method, path string, body io.Reader) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, 0, fmt.Errorf("config client: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, http.StatusBadGateway, fmt.Errorf("config service unavailable: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 400 {
		return data, resp.StatusCode, fmt.Errorf("config service returned %d", resp.StatusCode)
	}
	return data, resp.StatusCode, nil
}

// Proxy forwards a request to the Config Service, preserving the path suffix
// after the given prefix. It returns the raw body and status code.
func (c *Client) Proxy(ctx context.Context, method, path string, body io.Reader) ([]byte, int, error) {
	return c.do(ctx, method, path, body)
}

// GetModelIDs returns the list of active model IDs (for plan allowedModels).
func (c *Client) GetModelIDs(ctx context.Context) ([]string, error) {
	data, _, err := c.do(ctx, http.MethodGet, "/v1/config/models/catalog", nil)
	if err != nil {
		return nil, err
	}
	var ids []string
	if err := json.Unmarshal(data, &ids); err != nil {
		return nil, fmt.Errorf("config client: decode model ids: %w", err)
	}
	return ids, nil
}

// Paginate returns the limit and offset query params for the given page.
func Paginate(page, pageSize int) string {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}
	limit := pageSize
	offset := (page - 1) * pageSize
	return "limit=" + strconv.Itoa(limit) + "&offset=" + strconv.Itoa(offset)
}

// BuildListPath appends pagination query to a base path.
func BuildListPath(base string, rawQuery string) string {
	q := url.Values{}
	for _, pair := range strings.Split(rawQuery, "&") {
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			q.Set(kv[0], kv[1])
		}
	}
	if !q.Has("limit") {
		q.Set("limit", "50")
	}
	if !q.Has("offset") {
		q.Set("offset", "0")
	}
	enc := q.Encode()
	if enc == "" {
		return base
	}
	return base + "?" + enc
}
