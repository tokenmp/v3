//go:build unix

package main

import (
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	helperEnv       = "EXECUTOR_PROCESS_TEST_HELPER"
	processDeadline = 5 * time.Second
)

func TestExecutorProcess(t *testing.T) {
	if os.Getenv(helperEnv) == "1" {
		runExecutorProcessHelper()
		return
	}

	t.Run("serves healthz and exits cleanly on SIGTERM", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("reserve test address: %v", err)
		}
		address := listener.Addr().String()
		if err := listener.Close(); err != nil {
			t.Fatalf("release test address: %v", err)
		}

		cmd := helperCommand(t, address)
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

	t.Run("rejects invalid shutdown timeout", func(t *testing.T) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestExecutorProcess$")
		cmd.Env = append(os.Environ(), helperEnv+"=1", "EXECUTOR_SHUTDOWN_TIMEOUT=0s")
		output, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatal("executor exit error = nil, want non-zero exit")
		}
		if !strings.Contains(string(output), "EXECUTOR_SHUTDOWN_TIMEOUT") {
			t.Fatalf("executor output = %q, want EXECUTOR_SHUTDOWN_TIMEOUT", output)
		}
	})
}

func runExecutorProcessHelper() {
	if err := run(); err != nil {
		_, _ = os.Stderr.WriteString(err.Error())
		os.Exit(1)
	}
}

func helperCommand(t *testing.T, address string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestExecutorProcess$")
	cmd.Env = append(os.Environ(), helperEnv+"=1", "EXECUTOR_HTTP_ADDR="+address, "EXECUTOR_SHUTDOWN_TIMEOUT=1s")
	return cmd
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
