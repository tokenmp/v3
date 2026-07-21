package anthropicadapter

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/snapshot"
)

func loadAnthropicFixtureAdapter(t *testing.T) adapter.CompiledAdapter {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	fixturePath := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "fixtures", "configs", "anthropic.json")
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read Anthropic fixture: %v", err)
	}
	var cfg snapshot.ConfigSnapshot
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		t.Fatalf("decode Anthropic fixture: %v", err)
	}
	compiled, err := snapshot.Compile(cfg)
	if err != nil {
		t.Fatalf("compile Anthropic fixture: %v", err)
	}
	compiledAdapter, ok := compiled.Adapters["adapter-anthropic-default"]
	if !ok {
		t.Fatal("compiled Anthropic adapter is missing")
	}
	return compiledAdapter
}

func TestCompiledAnthropicFixtureMapsClassifiedFailures(t *testing.T) {
	compiledAdapter := loadAnthropicFixtureAdapter(t)
	engine := adapter.Engine{}

	for _, tc := range []struct {
		name      string
		upstream  adapter.UpstreamResponse
		matchedID string
	}{
		{
			name:      "529 overloaded error",
			upstream:  sdk.NewClassifiedError(sdk.ErrUnavailable, 529, "", "", "overloaded_error").ToUpstreamResponse(),
			matchedID: "resp-529-to-429",
		},
		{
			name:      "429 rate limit error status only",
			upstream:  sdk.NewClassifiedError(sdk.ErrRateLimited, 429, "", "", "rate_limit_error").ToUpstreamResponse(),
			matchedID: "resp-429",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := engine.MapResponse(compiledAdapter, tc.upstream)
			if got.HTTPStatus != 429 || got.ErrorCode != "rate_limited" || got.ErrorType != "rate_limited" || got.MatchedID != tc.matchedID {
				t.Fatalf("MapResponse(%#v) = %#v, want 429/rate_limited/rate_limited matched by %q", tc.upstream, got, tc.matchedID)
			}
		})
	}
}
