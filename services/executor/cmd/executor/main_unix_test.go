//go:build unix

package main

import (
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	helperEnv       = "EXECUTOR_PROCESS_TEST_HELPER"
	processDeadline = 5 * time.Second

	// processIdentityMap is a single-entry, active service identity whose API
	// key env is EXECUTOR_API_KEY_PROC. It is non-secret test material.
	processIdentityMap = `{"proc":{"subject":"proc","key_id":"kid-proc","role":"service","status":"active","api_key_env":"EXECUTOR_API_KEY_PROC"}}`
	processAPIKey      = "tm-proc-key-abc123"
)

// minimalEmptyConfig is a secret-free config that compiles to no business
// routes; it is the baseline for a healthy process with no models.
const minimalEmptyConfig = `{
  "Revision": "process-test",
  "CreatedAt": "2026-07-22T00:00:00Z",
  "Models": {},
  "Providers": {},
  "Routes": [],
  "Adapters": {}
}`

func TestExecutorProcess(t *testing.T) {
	if os.Getenv(helperEnv) == "1" {
		runExecutorProcessHelper()
		return
	}

	t.Run("serves healthz anonymous and exits cleanly on SIGTERM", func(t *testing.T) {
		configPath := writeProcessConfig(t, minimalEmptyConfig)
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("reserve test address: %v", err)
		}
		address := listener.Addr().String()
		if err := listener.Close(); err != nil {
			t.Fatalf("release test address: %v", err)
		}

		cmd := helperCommand(t, address, configPath, processIdentityMap)
		if err := cmd.Start(); err != nil {
			t.Fatalf("start executor: %v", err)
		}
		t.Cleanup(func() { stopProcess(cmd) })

		waitForHealthz(t, "http://"+address+"/healthz")
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatalf("send SIGTERM: %v", err)
		}
		if err := waitForExit(cmd, processDeadline); err != nil {
			t.Fatalf("executor exit: %v", err)
		}
	})

	t.Run("unauthorized v1 returns 401", func(t *testing.T) {
		configPath := writeProcessConfig(t, minimalEmptyConfig)
		address := freeAddress(t)
		cmd := helperCommand(t, address, configPath, processIdentityMap)
		if err := cmd.Start(); err != nil {
			t.Fatalf("start executor: %v", err)
		}
		t.Cleanup(func() { stopProcess(cmd) })

		waitForHealthz(t, "http://"+address+"/healthz")
		resp := processRequest(t, address, http.MethodPost, "/v1/chat/completions", `{"model":"x","messages":[],"stream":false}`, "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", resp.StatusCode, readBody(resp))
		}
		body := readBody(resp)
		if !strings.Contains(body, "authentication_error") {
			t.Fatalf("body = %q, want authentication_error", body)
		}
	})

	t.Run("authenticated chat missing model returns 404", func(t *testing.T) {
		configPath := writeProcessConfig(t, minimalEmptyConfig)
		address := freeAddress(t)
		cmd := helperCommand(t, address, configPath, processIdentityMap)
		if err := cmd.Start(); err != nil {
			t.Fatalf("start executor: %v", err)
		}
		t.Cleanup(func() { stopProcess(cmd) })

		waitForHealthz(t, "http://"+address+"/healthz")
		body := `{"model":"missing","messages":[{"role":"user","content":"hi"}],"stream":false}`
		resp := processRequest(t, address, http.MethodPost, "/v1/chat/completions", body, "Bearer "+processAPIKey)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", resp.StatusCode, readBody(resp))
		}
	})

	t.Run("authenticated stream chat missing model returns pre-commit JSON 404", func(t *testing.T) {
		configPath := writeProcessConfig(t, minimalEmptyConfig)
		address := freeAddress(t)
		cmd := helperCommand(t, address, configPath, processIdentityMap)
		if err := cmd.Start(); err != nil {
			t.Fatalf("start executor: %v", err)
		}
		t.Cleanup(func() { stopProcess(cmd) })

		waitForHealthz(t, "http://"+address+"/healthz")
		body := `{"model":"missing","messages":[{"role":"user","content":"hi"}],"stream":true}`
		resp := processRequest(t, address, http.MethodPost, "/v1/chat/completions", body, "Bearer "+processAPIKey)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", resp.StatusCode, readBody(resp))
		}
		if responseBody := readBody(resp); !strings.Contains(responseBody, "invalid_request_error") {
			t.Fatalf("body = %q, want OpenAI JSON pre-commit error", responseBody)
		}
	})

	t.Run("authenticated 501 route stays not-implemented", func(t *testing.T) {
		configPath := writeProcessConfig(t, minimalEmptyConfig)
		address := freeAddress(t)
		cmd := helperCommand(t, address, configPath, processIdentityMap)
		if err := cmd.Start(); err != nil {
			t.Fatalf("start executor: %v", err)
		}
		t.Cleanup(func() { stopProcess(cmd) })

		waitForHealthz(t, "http://"+address+"/healthz")
		// /v1/models is an authenticated route the runtime does not execute:
		// it must remain 501, not 404, proving the route is registered and the
		// adapter short-circuits to not-implemented.
		resp := processRequest(t, address, http.MethodGet, "/v1/models", "", "Bearer "+processAPIKey)
		if resp.StatusCode != http.StatusNotImplemented {
			t.Fatalf("status = %d, want 501; body=%s", resp.StatusCode, readBody(resp))
		}
	})

	t.Run("invalid config fails before listen", func(t *testing.T) {
		// Hold a controlled known address for the whole test so there is no
		// race window in which the process could bind and release it between
		// probes. composition.Build must fail closed before net.Listen, so the
		// process exits with a "build composition" error and must never reach
		// the listener step (which would instead surface a "listen on" error).
		probe, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("reserve probe address: %v", err)
		}
		address := probe.Addr().String()
		t.Cleanup(func() { _ = probe.Close() })

		dir := t.TempDir()
		missingPath := filepath.Join(dir, "missing.json")
		cmd := exec.Command(os.Args[0], "-test.run=^TestExecutorProcess$")
		cmd.Env = append(os.Environ(),
			helperEnv+"=1",
			"EXECUTOR_HTTP_ADDR="+address,
			"EXECUTOR_SHUTDOWN_TIMEOUT=1s",
			"EXECUTOR_CONFIG_FILE="+missingPath,
			"EXECUTOR_CREDENTIAL_REF_MAP_JSON={}",
			"EXECUTOR_IDENTITY_MAP_JSON="+processIdentityMap,
			"EXECUTOR_API_KEY_PROC="+processAPIKey,
		)
		output, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatal("executor exit error = nil, want non-zero exit")
		}
		out := string(output)
		if !strings.Contains(out, "build composition") {
			t.Fatalf("executor output = %q, want build composition failure", out)
		}
		if strings.Contains(out, "listen on") {
			t.Fatalf("executor reached net.Listen on the held address; output = %q", out)
		}
	})

	t.Run("rejects invalid shutdown timeout", func(t *testing.T) {
		configPath := writeProcessConfig(t, minimalEmptyConfig)
		cmd := exec.Command(os.Args[0], "-test.run=^TestExecutorProcess$")
		cmd.Env = append(os.Environ(),
			helperEnv+"=1",
			"EXECUTOR_HTTP_ADDR=127.0.0.1:0",
			"EXECUTOR_SHUTDOWN_TIMEOUT=0s",
			"EXECUTOR_CONFIG_FILE="+configPath,
			"EXECUTOR_CREDENTIAL_REF_MAP_JSON={}",
			"EXECUTOR_IDENTITY_MAP_JSON="+processIdentityMap,
			"EXECUTOR_API_KEY_PROC="+processAPIKey,
		)
		output, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatal("executor exit error = nil, want non-zero exit")
		}
		if !strings.Contains(string(output), "EXECUTOR_SHUTDOWN_TIMEOUT") {
			t.Fatalf("executor output = %q, want EXECUTOR_SHUTDOWN_TIMEOUT", output)
		}
	})

	t.Run("rejects missing required env before listen", func(t *testing.T) {
		// EXECUTOR_CONFIG_FILE unset: config.Load fails before composition or
		// listening. The error names the variable, not any value or path.
		cmd := exec.Command(os.Args[0], "-test.run=^TestExecutorProcess$")
		cmd.Env = append(os.Environ(),
			helperEnv+"=1",
			"EXECUTOR_HTTP_ADDR=127.0.0.1:0",
			"EXECUTOR_SHUTDOWN_TIMEOUT=1s",
			"EXECUTOR_CREDENTIAL_REF_MAP_JSON={}",
			"EXECUTOR_IDENTITY_MAP_JSON="+processIdentityMap,
			"EXECUTOR_API_KEY_PROC="+processAPIKey,
		)
		output, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatal("executor exit error = nil, want non-zero exit")
		}
		if !strings.Contains(string(output), "EXECUTOR_CONFIG_FILE") {
			t.Fatalf("executor output = %q, want EXECUTOR_CONFIG_FILE", output)
		}
	})
}

func runExecutorProcessHelper() {
	if err := run(); err != nil {
		_, _ = os.Stderr.WriteString(err.Error())
		os.Exit(1)
	}
}

func writeProcessConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func helperCommand(t *testing.T, address, configPath, identityMap string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestExecutorProcess$")
	cmd.Env = append(os.Environ(),
		helperEnv+"=1",
		"EXECUTOR_HTTP_ADDR="+address,
		"EXECUTOR_SHUTDOWN_TIMEOUT=1s",
		"EXECUTOR_CONFIG_FILE="+configPath,
		"EXECUTOR_CREDENTIAL_REF_MAP_JSON={}",
		"EXECUTOR_IDENTITY_MAP_JSON="+identityMap,
		"EXECUTOR_API_KEY_PROC="+processAPIKey,
	)
	return cmd
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve test address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release test address: %v", err)
	}
	return address
}

func waitForHealthz(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(processDeadline)
	client := &http.Client{Timeout: 200 * time.Millisecond}
	for {
		response, err := client.Get(url)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
			err = errors.New(response.Status)
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET /healthz before deadline: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func processRequest(t *testing.T, address, method, path, body, auth string) *http.Response {
	t.Helper()
	deadline := time.Now().Add(processDeadline)
	client := &http.Client{Timeout: 2 * time.Second}
	var lastErr error
	for {
		req, err := http.NewRequest(method, "http://"+address+path, strings.NewReader(body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := client.Do(req)
		if err == nil {
			return resp
		}
		lastErr = err
		if time.Now().After(deadline) {
			t.Fatalf("%s %s before deadline: %v", method, path, lastErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func readBody(resp *http.Response) string {
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	return string(data)
}

func waitForExit(cmd *exec.Cmd, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-done
		return errors.New("timed out waiting for executor to exit")
	}
}

func stopProcess(cmd *exec.Cmd) {
	if cmd.Process == nil || cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		return
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}
