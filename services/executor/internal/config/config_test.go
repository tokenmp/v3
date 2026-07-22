package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"EXECUTOR_CONFIG_FILE":             "/tmp/executor.json",
		"EXECUTOR_CREDENTIAL_REF_MAP_JSON": "{}",
		"EXECUTOR_IDENTITY_MAP_JSON":       "{}",
	}
	got, err := Load(func(key string) (string, bool) { v, ok := env[key]; return v, ok })
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := Config{
		HTTPAddr:             "127.0.0.1:8081",
		ShutdownTimeout:      10 * time.Second,
		ReadHeaderTimeout:    10 * time.Second,
		IdleTimeout:          60 * time.Second,
		ConfigFile:           "/tmp/executor.json",
		CredentialRefMapJSON: "{}",
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
				switch key {
				case "EXECUTOR_CONFIG_FILE":
					return "/tmp/executor.json", true
				case "EXECUTOR_CREDENTIAL_REF_MAP_JSON", "EXECUTOR_IDENTITY_MAP_JSON":
					return "{}", true
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
		"EXECUTOR_HTTP_ADDR":               "127.0.0.1:9090",
		"EXECUTOR_SHUTDOWN_TIMEOUT":        "250ms",
		"EXECUTOR_READ_HEADER_TIMEOUT":     "500ms",
		"EXECUTOR_IDLE_TIMEOUT":            "1m",
		"EXECUTOR_CONFIG_FILE":             "/etc/executor/config.json",
		"EXECUTOR_CREDENTIAL_REF_MAP_JSON": `{"vault://p/c/default":"EXECUTOR_CREDENTIAL_P"}`,
		"EXECUTOR_IDENTITY_MAP_JSON":       `{"a":{"subject":"s","key_id":"k","role":"service","status":"active","api_key_env":"EXECUTOR_API_KEY_A"}}`,
	}
	got, err := Load(func(key string) (string, bool) { value, ok := env[key]; return value, ok })
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := Config{
		HTTPAddr:             "127.0.0.1:9090",
		ShutdownTimeout:      250 * time.Millisecond,
		ReadHeaderTimeout:    500 * time.Millisecond,
		IdleTimeout:          time.Minute,
		ConfigFile:           "/etc/executor/config.json",
		CredentialRefMapJSON: `{"vault://p/c/default":"EXECUTOR_CREDENTIAL_P"}`,
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

func TestLoadRequiresCompositionEnv(t *testing.T) {
	t.Parallel()

	base := map[string]string{
		"EXECUTOR_CONFIG_FILE":             "/tmp/executor.json",
		"EXECUTOR_CREDENTIAL_REF_MAP_JSON": "{}",
		"EXECUTOR_IDENTITY_MAP_JSON":       "{}",
	}
	cases := []struct {
		name string
		key  string
		set  func(map[string]string)
	}{
		{"config file unset", "EXECUTOR_CONFIG_FILE", func(m map[string]string) { delete(m, "EXECUTOR_CONFIG_FILE") }},
		{"credential map unset", "EXECUTOR_CREDENTIAL_REF_MAP_JSON", func(m map[string]string) { delete(m, "EXECUTOR_CREDENTIAL_REF_MAP_JSON") }},
		{"identity map unset", "EXECUTOR_IDENTITY_MAP_JSON", func(m map[string]string) { delete(m, "EXECUTOR_IDENTITY_MAP_JSON") }},
		{"config file whitespace", "EXECUTOR_CONFIG_FILE", func(m map[string]string) { m["EXECUTOR_CONFIG_FILE"] = "  " }},
		{"credential map whitespace", "EXECUTOR_CREDENTIAL_REF_MAP_JSON", func(m map[string]string) { m["EXECUTOR_CREDENTIAL_REF_MAP_JSON"] = "\t" }},
		{"identity map whitespace", "EXECUTOR_IDENTITY_MAP_JSON", func(m map[string]string) { m["EXECUTOR_IDENTITY_MAP_JSON"] = " \n " }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := make(map[string]string, len(base))
			for k, v := range base {
				env[k] = v
			}
			tc.set(env)
			_, err := Load(func(key string) (string, bool) { v, ok := env[key]; return v, ok })
			if err == nil {
				t.Fatalf("Load() error = nil, want error for %s", tc.key)
			}
			// The error must name the variable but never echo its value.
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("Load() error = %q, want it to name %s", err.Error(), tc.key)
			}
			// Whitespace values must not leak into the message.
			if strings.Contains(err.Error(), "  ") || strings.Contains(err.Error(), "\t") || strings.Contains(err.Error(), "\n") {
				t.Errorf("Load() error leaked whitespace value: %q", err.Error())
			}
		})
	}
}
