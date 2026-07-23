package composition

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/config"
	"github.com/tokenmp/v3/services/executor/internal/execution"
	"github.com/tokenmp/v3/services/executor/internal/jwtverifier"
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

func TestBuildModelsReturns200AndResponsesExecutes(t *testing.T) {
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

	// /v1/responses is now executable; with an empty config the model is not found.
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

	t.Run("responses authenticated model-not-found 404", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8081/v1/responses", strings.NewReader(`{"model":"x","input":"hi"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
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

// testConfigWithJWT builds a config.Config with JWT fields set.
func testConfigWithJWT(configPath, credentialRefMapJSON, jwtPublicKeyFile, jwtIssuer, jwtAudience string) config.Config {
	return config.Config{
		ConfigFile:           configPath,
		CredentialRefMapJSON: credentialRefMapJSON,
		JWTPublicKeyFile:     jwtPublicKeyFile,
		JWTIssuer:            jwtIssuer,
		JWTAudience:          jwtAudience,
	}
}

// generateEd25519KeyPair creates a test key pair.
func generateEd25519KeyPair(t *testing.T) (ed25519.PrivateKey, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	return priv, pub
}

// writeEd25519PublicKeyPEM writes the public key as PKIX PEM.
func writeEd25519PublicKeyPEM(t *testing.T, pub ed25519.PublicKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	block := &pem.Block{Type: "PUBLIC KEY", Bytes: der}
	dir := t.TempDir()
	path := filepath.Join(dir, "jwt_public.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o644); err != nil {
		t.Fatalf("write public key: %v", err)
	}
	return path
}

// issueTestJWT signs a JWT with the given private key and claims.
func issueTestJWT(t *testing.T, priv ed25519.PrivateKey, claims *jwtverifier.Claims) string {
	t.Helper()
	token := jwtv5.NewWithClaims(jwtv5.SigningMethodEdDSA, claims)
	signed, err := token.SignedString(priv)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

// ─── JWT composition tests ───────────────────────────────────────────

func TestBuildWithJWTSource(t *testing.T) {
	t.Parallel()

	priv, pub := generateEd25519KeyPair(t)
	pubKeyFile := writeEd25519PublicKeyPEM(t, pub)
	configPath := writeConfig(t, minimalEmptyConfig)

	cfg := testConfigWithJWT(configPath, "{}", pubKeyFile, "tokenmp-auth", "tokenmp-web")
	// No EXECUTOR_IDENTITY_MAP_JSON needed when JWT is configured.
	env := map[string]string{
		"EXECUTOR_CONFIG_FILE":             configPath,
		"EXECUTOR_CREDENTIAL_REF_MAP_JSON": "{}",
	}
	handler, err := Build(context.Background(), cfg, envLookup(env))
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if handler == nil {
		t.Fatal("Build() handler = nil")
	}

	// Issue a valid JWT and use it as Bearer token.
	now := time.Now()
	claims := &jwtverifier.Claims{
		RegisteredClaims: jwtv5.RegisteredClaims{
			Issuer:    "tokenmp-auth",
			Subject:   "user-42",
			Audience:  jwtv5.ClaimStrings{"tokenmp-web"},
			ExpiresAt: jwtv5.NewNumericDate(now.Add(15 * time.Minute)),
			NotBefore: jwtv5.NewNumericDate(now),
			IssuedAt:  jwtv5.NewNumericDate(now),
			ID:        "jti-test-1",
		},
		Role:         "user",
		TokenVersion: 1,
	}
	jwtToken := issueTestJWT(t, priv, claims)

	t.Run("JWT Bearer authenticated models 200", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8081/v1/models", nil)
		req.Header.Set("Authorization", "Bearer "+jwtToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("JWT Bearer authenticated chat missing model 404", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8081/v1/chat/completions", strings.NewReader(`{"model":"missing","messages":[{"role":"user","content":"hi"}],"stream":false}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+jwtToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("expired JWT Bearer returns 401", func(t *testing.T) {
		t.Parallel()
		expiredClaims := &jwtverifier.Claims{
			RegisteredClaims: jwtv5.RegisteredClaims{
				Issuer:    "tokenmp-auth",
				Subject:   "user-42",
				Audience:  jwtv5.ClaimStrings{"tokenmp-web"},
				ExpiresAt: jwtv5.NewNumericDate(now.Add(-1 * time.Hour)),
				NotBefore: jwtv5.NewNumericDate(now.Add(-2 * time.Hour)),
				IssuedAt:  jwtv5.NewNumericDate(now.Add(-2 * time.Hour)),
				ID:        "jti-expired",
			},
			Role:         "user",
			TokenVersion: 1,
		}
		expiredToken := issueTestJWT(t, priv, expiredClaims)
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8081/v1/models", nil)
		req.Header.Set("Authorization", "Bearer "+expiredToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("invalid JWT Bearer returns 401", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8081/v1/models", nil)
		req.Header.Set("Authorization", "Bearer invalid-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("no Authorization returns 401", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8081/v1/models", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("healthz remains anonymous", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8081/healthz", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})
}

func TestBuildJWTSourceRejectsMissingPublicKeyFile(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, minimalEmptyConfig)
	missingKeyFile := filepath.Join(t.TempDir(), "missing.pem")

	cfg := testConfigWithJWT(configPath, "{}", missingKeyFile, "tokenmp-auth", "tokenmp-web")
	env := map[string]string{
		"EXECUTOR_CONFIG_FILE":             configPath,
		"EXECUTOR_CREDENTIAL_REF_MAP_JSON": "{}",
	}
	_, err := Build(context.Background(), cfg, envLookup(env))
	if !errors.Is(err, ErrJWTVerifier) {
		t.Fatalf("Build() error = %v, want ErrJWTVerifier", err)
	}
}

func TestBuildJWTSourceRejectsMalformedPublicKeyFile(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, minimalEmptyConfig)
	dir := t.TempDir()
	badKeyFile := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(badKeyFile, []byte("not a pem"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg := testConfigWithJWT(configPath, "{}", badKeyFile, "tokenmp-auth", "tokenmp-web")
	env := map[string]string{
		"EXECUTOR_CONFIG_FILE":             configPath,
		"EXECUTOR_CREDENTIAL_REF_MAP_JSON": "{}",
	}
	_, err := Build(context.Background(), cfg, envLookup(env))
	if !errors.Is(err, ErrJWTVerifier) {
		t.Fatalf("Build() error = %v, want ErrJWTVerifier", err)
	}
}

func TestBuildFallsBackToIdentityEnvWhenNoJWT(t *testing.T) {
	t.Parallel()

	// Without JWT configured, identityenv is required and used.
	configPath := writeConfig(t, minimalEmptyConfig)
	env := healthyEnv(t, configPath)
	cfg := testConfig(configPath, "{}")
	handler, err := Build(context.Background(), cfg, envLookup(env))
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	// API key auth still works.
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8081/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestBuildJWTPrioritizedOverIdentityEnv(t *testing.T) {
	t.Parallel()

	priv, pub := generateEd25519KeyPair(t)
	pubKeyFile := writeEd25519PublicKeyPEM(t, pub)
	configPath := writeConfig(t, minimalEmptyConfig)

	// Both JWT and identityenv are configured; JWT should be used.
	cfg := testConfigWithJWT(configPath, "{}", pubKeyFile, "tokenmp-auth", "tokenmp-web")
	env := map[string]string{
		"EXECUTOR_CONFIG_FILE":             configPath,
		"EXECUTOR_CREDENTIAL_REF_MAP_JSON": "{}",
		"EXECUTOR_IDENTITY_MAP_JSON":       testIdentityMap,
		"EXECUTOR_API_KEY_TEST":            testAPIKey,
	}
	handler, err := Build(context.Background(), cfg, envLookup(env))
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	// JWT token should work.
	now := time.Now()
	claims := &jwtverifier.Claims{
		RegisteredClaims: jwtv5.RegisteredClaims{
			Issuer:    "tokenmp-auth",
			Subject:   "jwt-user",
			Audience:  jwtv5.ClaimStrings{"tokenmp-web"},
			ExpiresAt: jwtv5.NewNumericDate(now.Add(15 * time.Minute)),
			NotBefore: jwtv5.NewNumericDate(now),
			IssuedAt:  jwtv5.NewNumericDate(now),
			ID:        "jti-priority",
		},
		Role:         "user",
		TokenVersion: 1,
	}
	jwtToken := issueTestJWT(t, priv, claims)

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8081/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("JWT auth status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// API key should NOT work when JWT source is active (different source).
	apiReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8081/v1/models", nil)
	apiReq.Header.Set("Authorization", "Bearer "+testAPIKey)
	apiRec := httptest.NewRecorder()
	handler.ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusUnauthorized {
		t.Fatalf("API key auth status = %d, want 401 (JWT source active, API key not in JWT source)", apiRec.Code)
	}
}
