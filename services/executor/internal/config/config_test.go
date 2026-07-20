package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	got, err := Load(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := Config{
		HTTPAddr:          "127.0.0.1:8081",
		ShutdownTimeout:   10 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if got != want {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}

func TestLoadHTTPAddr(t *testing.T) {
	t.Parallel()

	const defaultAddr = "127.0.0.1:8081"
	tests := []struct {
		name    string
		value   string
		present bool
		want    string
		wantErr string
	}{
		{name: "unset", want: defaultAddr},
		{name: "empty", value: "", present: true, want: defaultAddr},
		{name: "whitespace", value: " \t\n", present: true, wantErr: "EXECUTOR_HTTP_ADDR must not contain only whitespace"},
		{name: "preserves valid value", value: " 127.0.0.1:9090 ", present: true, want: " 127.0.0.1:9090 "},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := Load(func(key string) (string, bool) {
				if key == "EXECUTOR_HTTP_ADDR" {
					return test.value, test.present
				}
				return "", false
			})
			if test.wantErr != "" {
				if err == nil || err.Error() != test.wantErr {
					t.Fatalf("Load() error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got.HTTPAddr != test.want {
				t.Errorf("Load().HTTPAddr = %q, want %q", got.HTTPAddr, test.want)
			}
		})
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"EXECUTOR_HTTP_ADDR":           "127.0.0.1:9090",
		"EXECUTOR_SHUTDOWN_TIMEOUT":    "250ms",
		"EXECUTOR_READ_HEADER_TIMEOUT": "500ms",
		"EXECUTOR_IDLE_TIMEOUT":        "1m",
	}
	got, err := Load(func(key string) (string, bool) { value, ok := env[key]; return value, ok })
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := Config{
		HTTPAddr:          "127.0.0.1:9090",
		ShutdownTimeout:   250 * time.Millisecond,
		ReadHeaderTimeout: 500 * time.Millisecond,
		IdleTimeout:       time.Minute,
	}
	if got != want {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}

func TestLoadRejectsInvalidDurations(t *testing.T) {
	t.Parallel()

	for _, key := range []string{
		"EXECUTOR_SHUTDOWN_TIMEOUT",
		"EXECUTOR_READ_HEADER_TIMEOUT",
		"EXECUTOR_IDLE_TIMEOUT",
	} {
		for _, value := range []string{"", "invalid", "0s", "-1s"} {
			t.Run(key+"/"+value, func(t *testing.T) {
				t.Parallel()

				_, err := Load(func(lookupKey string) (string, bool) {
					if lookupKey == key {
						return value, true
					}
					return "", false
				})
				if err == nil {
					t.Fatal("Load() error = nil, want error")
				}
			})
		}
	}
}
