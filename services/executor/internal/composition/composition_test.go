package composition

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/config"
	"github.com/tokenmp/v3/services/executor/internal/execution"
)

// minimalEmptyConfig is a secret-free config that compiles to no business
// routes. It is the baseline for a healthy composition with no models.
const minimalEmptyConfig = `{
  "Revision": "composition-test",
  "CreatedAt": "2026-07-22T00:00:00Z",
  "Models": {},
  "Providers": {},
  "Routes": [],
  "Adapters": {}
}`

// unsupportedRouteConfig compiles an enabled route whose SDK/protocol pair
// (generic_http/openai_responses) has no registered non-stream adapter. The
// adapter uses AuthNone so no credential mapping is required.
const unsupportedRouteConfig = `{
  "Revision": "composition-unsupported",
  "CreatedAt": "2026-07-22T00:00:00Z",
  "Models": {
    "resp-model": {
      "ID": "resp-model",
      "DisplayName": "Responses",
      "Capabilities": ["chat"],
      "Thinking": {"Supported": false}
    }
  },
  "Providers": {
    "generic-provider": {
      "ID": "generic-provider",
      "Selector": "generic",
      "Name": "Generic",
      "BaseURL": "https://upstream.example/v1",
      "SDKKind": "generic_http",
      "Protocol": "openai_responses",
      "Retry": {},
      "Timeout": {}
    }
  },
  "Routes": [
    {
      "ID": "route-resp",
      "ModelID": "resp-model",
      "ProviderID": "generic-provider",
      "AdapterID": "adapter-generic",
      "UpstreamModel": "resp-upstream",
      "Priority": 100,
      "Enabled": true,
      "Protocol": "openai_responses",
      "Retry": {},
      "Timeout": {},
      "Credentials": []
    }
  ],
  "Adapters": {
    "adapter-generic": {
      "ID": "adapter-generic",
      "Name": "Generic Adapter",
      "Version": 1,
      "SDKKind": "generic_http",
      "Protocol": "openai_responses",
      "Auth": {"Kind": "none"},
      "Capability": {"Require": ["chat"], "Deny": []},
      "Thinking": {"Supported": false},
      "Request": {"AllowedHeaders": ["Content-Type"], "AllowedQuery": [], "Rules": []},
      "Response": {"Rules": []},
      "Retry": {},
      "Timeout": {}
    }
  }
}`

// testIdentityMap is a single-entry, active service identity whose API key
// env is EXECUTOR_API_KEY_TEST. The key value is non-secret test material.
const testIdentityMap = `{"test":{"subject":"tester","key_id":"kid-test","role":"service","status":"active","api_key_env":"EXECUTOR_API_KEY_TEST"}}`

const testAPIKey = "tm-test-key-12345"

// writeConfig writes content to a temp file and returns its path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// envLookup returns a lookupEnv over a map, falling back to the real process
// env for keys not in the map. This lets tests inject the composition env vars
// while still allowing os.LookupEnv-based helpers if needed.
func envLookup(env map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		if v, ok := env[key]; ok {
			return v, true
		}
		return os.LookupEnv(key)
	}
}

// healthyEnv returns the env map for a successful composition over an empty
// config with no credential bindings and a single test identity.
func healthyEnv(t *testing.T, configPath string) map[string]string {
	t.Helper()
	return map[string]string{
		"EXECUTOR_CONFIG_FILE":             configPath,
		"EXECUTOR_CREDENTIAL_REF_MAP_JSON": "{}",
		"EXECUTOR_IDENTITY_MAP_JSON":       testIdentityMap,
		"EXECUTOR_API_KEY_TEST":            testAPIKey,
	}
}

func TestBuildReturnsHandlerForEmptyConfig(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, minimalEmptyConfig)
	cfg := testConfig(path, "{}")
	handler, err := Build(context.Background(), cfg, envLookup(healthyEnv(t, path)))
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if handler == nil {
		t.Fatal("Build() handler = nil")
	}

	t.Run("healthz is anonymous 200", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8081/healthz", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["status"] != "ok" {
			t.Errorf("status body = %v, want ok", body["status"])
		}
	})

	t.Run("head healthz is anonymous 200 no body", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodHead, "http://127.0.0.1:8081/healthz", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("HEAD body = %q, want empty", rec.Body.String())
		}
	})
}

func TestBuildUnauthorizedV1ReturnsProtocolNative401(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, minimalEmptyConfig)
	handler, err := Build(context.Background(), testConfig(path, "{}"), envLookup(healthyEnv(t, path)))
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	// No Authorization header: the outer AuthMiddleware must reject before the
	// body is read or parsed. The response must be OpenAI-native for the chat
	// path and carry no routing/credential detail.
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8081/v1/chat/completions", strings.NewReader(`{"model":"x","messages":[],"stream":false}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "authentication_error") {
		t.Fatalf("body = %q, want authentication_error", rec.Body.String())
	}

	// /v1/messages must yield the Anthropic-native 401 shape.
	msgReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8081/v1/messages", strings.NewReader(`{"model":"x","max_tokens":1,"messages":[],"stream":false}`))
	msgReq.Header.Set("Content-Type", "application/json")
	msgRec := httptest.NewRecorder()
	handler.ServeHTTP(msgRec, msgReq)
	if msgRec.Code != http.StatusUnauthorized {
		t.Fatalf("messages status = %d, want 401", msgRec.Code)
	}
	if !strings.Contains(msgRec.Body.String(), `"type":"error"`) {
		t.Fatalf("messages body = %q, want Anthropic error envelope", msgRec.Body.String())
	}
}

func TestBuildAuthenticatedChatMissingModelReturns404(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, minimalEmptyConfig)
	handler, err := Build(context.Background(), testConfig(path, "{}"), envLookup(healthyEnv(t, path)))
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	for _, tc := range []struct {
		name, path, body, wantType string
	}{
		{
			name: "non-stream chat", path: "/v1/chat/completions",
			body:     `{"model":"missing","messages":[{"role":"user","content":"hi"}],"stream":false}`,
			wantType: "invalid_request_error",
		},
		{
			name: "stream chat pre-commit", path: "/v1/chat/completions",
			body:     `{"model":"missing","messages":[{"role":"user","content":"hi"}],"stream":true}`,
			wantType: "invalid_request_error",
		},
		{
			name: "stream messages pre-commit", path: "/v1/messages",
			body:     `{"model":"missing","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"stream":true}`,
			wantType: `"type":"error"`,
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8081"+tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+testAPIKey)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantType) {
				t.Fatalf("body = %q, want %s", rec.Body.String(), tc.wantType)
			}
		})
	}
}

func TestBuildModelsReturns200AndResponsesIs501(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, minimalEmptyConfig)
	handler, err := Build(context.Background(), testConfig(path, "{}"), envLookup(healthyEnv(t, path)))
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	// /v1/models is auth-protected and returns 200 with an empty model list
	// when authenticated with a valid bearer.
	t.Run("models anonymous 401", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8081/v1/models", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("models authenticated 200", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8081/v1/models", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["object"] != "list" {
			t.Errorf("object = %v, want list", body["object"])
		}
		data, ok := body["data"].([]any)
		if !ok {
			t.Fatalf("data type = %T, want []any", body["data"])
		}
		if len(data) != 0 {
			t.Errorf("len(data) = %d, want 0 (empty config has no models)", len(data))
		}
	})

	// /v1/responses remains 501.
	t.Run("responses anonymous 401", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8081/v1/responses", strings.NewReader(`{"model":"x","input":"hi"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("responses authenticated 501", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8081/v1/responses", strings.NewReader(`{"model":"x","input":"hi"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want 501", rec.Code)
		}
	})
}

func TestBuildSDKRegistryRegistersExactCompletionAndStreamPairs(t *testing.T) {
	t.Parallel()

	registry, err := buildSDKRegistry()
	if err != nil {
		t.Fatalf("buildSDKRegistry() error = %v", err)
	}
	for _, pair := range []struct {
		kind     adapter.SDKKind
		protocol adapter.Protocol
	}{
		{adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat},
		{adapter.SDKKindAnthropic, adapter.ProtocolAnthropic},
	} {
		completion, err := registry.Client(pair.kind, pair.protocol)
		if err != nil {
			t.Fatalf("Client(%q, %q): %v", pair.kind, pair.protocol, err)
		}
		stream, err := registry.StreamClient(pair.kind, pair.protocol)
		if err != nil {
			t.Fatalf("StreamClient(%q, %q): %v", pair.kind, pair.protocol, err)
		}
		if any(completion) != any(stream) {
			t.Fatalf("pair (%q, %q) uses distinct completion and stream instances", pair.kind, pair.protocol)
		}
	}
	if _, err := registry.Client(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIImages); err != nil {
		t.Fatalf("image completion client: %v", err)
	}
	if _, err := registry.StreamClient(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIImages); !errors.Is(err, execution.ErrSDKClientUnknown) {
		t.Fatalf("image stream client error = %v, want unknown", err)
	}
}

func TestBuildAcceptsEnabledRoutesWithCompletionAndStreamCapabilities(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, fixture, credentialRef string
	}{
		{"openai", "default.json", "vault://openai-default/credential/default"},
		{"anthropic", "anthropic.json", "vault://anthropic-default/credential/default"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			fixturePath := filepath.Join("..", "..", "fixtures", "configs", tc.fixture)
			content, err := os.ReadFile(fixturePath)
			if err != nil {
				t.Fatalf("read fixture %s: %v", fixturePath, err)
			}
			path := writeConfig(t, string(content))
			env := healthyEnv(t, path)
			env["EXECUTOR_CREDENTIAL_REF_MAP_JSON"] = `{"` + tc.credentialRef + `":"EXECUTOR_CREDENTIAL_TEST"}`
			env["EXECUTOR_CREDENTIAL_TEST"] = "test-credential"
			if _, err := Build(context.Background(), testConfig(path, env["EXECUTOR_CREDENTIAL_REF_MAP_JSON"]), envLookup(env)); err != nil {
				t.Fatalf("Build() error = %v, want enabled route accepted", err)
			}
		})
	}
}

func TestBuildAcceptsEnabledOpenAIImagesWithoutStreamCapability(t *testing.T) {
	t.Parallel()
	const imageOnlyConfig = `{
		"Revision":"composition-images", "CreatedAt":"2026-07-22T00:00:00Z",
		"Models":{"image":{"ID":"image","DisplayName":"Image","Capabilities":["images"],"Thinking":{"Supported":false}}},
		"Providers":{"openai":{"ID":"openai","Selector":"openai","Name":"OpenAI","BaseURL":"https://upstream.example/v1","SDKKind":"openai","Protocol":"openai_images","Retry":{},"Timeout":{}}},
		"Routes":[{"ID":"image-route","ModelID":"image","ProviderID":"openai","AdapterID":"image-adapter","UpstreamModel":"dall-e-3","Priority":1,"Enabled":true,"Protocol":"openai_images","Retry":{},"Timeout":{},"Credentials":[]}],
		"Adapters":{"image-adapter":{"ID":"image-adapter","Name":"Images","Version":1,"SDKKind":"openai","Protocol":"openai_images","Auth":{"Kind":"bearer_header","Header":"Authorization","CredentialRef":"vault://openai/images"},"Capability":{"Require":["images"],"Deny":[]},"Thinking":{"Supported":false},"Request":{"AllowedHeaders":["Content-Type"],"AllowedQuery":[],"Rules":[]},"Response":{"Rules":[]},"Retry":{},"Timeout":{}}}
	}`
	path := writeConfig(t, imageOnlyConfig)
	env := healthyEnv(t, path)
	env["EXECUTOR_CREDENTIAL_REF_MAP_JSON"] = `{"vault://openai/images":"EXECUTOR_CREDENTIAL_TEST"}`
	env["EXECUTOR_CREDENTIAL_TEST"] = "test-credential"
	if _, err := Build(context.Background(), testConfig(path, env["EXECUTOR_CREDENTIAL_REF_MAP_JSON"]), envLookup(env)); err != nil {
		t.Fatalf("Build() image-only route error = %v", err)
	}
}

func TestBuildRejectsUnsupportedEnabledRoute(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, unsupportedRouteConfig)
	env := healthyEnv(t, path)
	_, err := Build(context.Background(), testConfig(path, "{}"), envLookup(env))
	if !errors.Is(err, ErrUnsupportedRoute) {
		t.Fatalf("Build() error = %v, want ErrUnsupportedRoute", err)
	}
}

func TestBuildRejectsMissingConfigFile(t *testing.T) {
	t.Parallel()

	// A path that does not exist: the config source returns a non-leaking
	// sentinel, and composition maps it to ErrConfigSource without surfacing
	// the path.
	env := map[string]string{
		"EXECUTOR_CONFIG_FILE":             filepath.Join(t.TempDir(), "missing.json"),
		"EXECUTOR_CREDENTIAL_REF_MAP_JSON": "{}",
		"EXECUTOR_IDENTITY_MAP_JSON":       testIdentityMap,
		"EXECUTOR_API_KEY_TEST":            testAPIKey,
	}
	_, err := Build(context.Background(), testConfig(env["EXECUTOR_CONFIG_FILE"], "{}"), envLookup(env))
	if !errors.Is(err, ErrConfigSource) {
		t.Fatalf("Build() error = %v, want ErrConfigSource", err)
	}
}

func TestBuildRejectsMalformedCredentialMap(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, minimalEmptyConfig)
	env := healthyEnv(t, path)
	env["EXECUTOR_CREDENTIAL_REF_MAP_JSON"] = "not-json"
	_, err := Build(context.Background(), testConfig(path, "not-json"), envLookup(env))
	if !errors.Is(err, ErrCredentialResolver) {
		t.Fatalf("Build() error = %v, want ErrCredentialResolver", err)
	}
}

func TestBuildRejectsMalformedIdentityMap(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, minimalEmptyConfig)
	env := healthyEnv(t, path)
	env["EXECUTOR_IDENTITY_MAP_JSON"] = "not-json"
	_, err := Build(context.Background(), testConfig(path, "{}"), envLookup(env))
	if !errors.Is(err, ErrIdentityResolver) {
		t.Fatalf("Build() error = %v, want ErrIdentityResolver", err)
	}
}

// testConfig builds a config.Config for the composition root without importing
// the config package's constructor (which would re-read env). It sets only the
// fields composition consumes.
func testConfig(configPath, credentialRefMapJSON string) config.Config {
	return config.Config{
		ConfigFile:           configPath,
		CredentialRefMapJSON: credentialRefMapJSON,
	}
}
