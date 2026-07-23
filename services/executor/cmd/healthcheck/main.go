// Command healthcheck probes the executor service /healthz endpoint and exits 0
// on HTTP 200, non-zero otherwise. It is intended for Docker HEALTHCHECK so
// the image does not depend on curl or wget being installed in the final
// runtime image.
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	addr := os.Getenv("EXECUTOR_HEALTHCHECK_ADDR")
	if addr == "" {
		addr = "http://127.0.0.1:8081/healthz"
	}
	if err := probe(addr); err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck failed: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func probe(addr string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("non-success status %d", resp.StatusCode)
	}
	return nil
}
