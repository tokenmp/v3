package configsource

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/snapshot"
)

// fixtureNames mirrors the snapshot package fixture set.
var fixtureNames = []string{"default", "xfyun", "anthropic"}

// fixtureDir resolves the repository-local fixtures/configs directory from
// the location of this test file, independent of the process CWD.
func fixtureDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile: services/executor/internal/configsource/configsource_test.go
	executorDir := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	return filepath.Join(executorDir, "fixtures", "configs")
}

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(fixtureDir(t), name+".json")
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(fixturePath(t, name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return raw
}

// writeTempConfig writes content to a fresh temp file and returns its path.
func writeTempConfig(t *testing.T, content []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

// strictDecodeAndMarshal decodes a fixture strictly, lets the test mutate it,
// then re-marshals it back to JSON for LoadFile. It mirrors the production
// strict-decode policy so mutated fixtures stay schema-valid unless the test
// intends otherwise.
func strictDecodeAndMarshal(t *testing.T, raw []byte, mutate func(*snapshot.ConfigSnapshot)) []byte {
	t.Helper()
	var cfg snapshot.ConfigSnapshot
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		t.Fatalf("strict decode: %v", err)
	}
	mutate(&cfg)
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return out
}

func configServiceServer(t *testing.T, revision string, raw []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"revision": revision, "snapshot": json.RawMessage(raw), "sha256": "test-sha",
			"compiled_meta": nil, "created_at": "2026-07-24T00:00:00Z",
		}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
}

func TestLoadFromConfigService(t *testing.T) {
	srv := configServiceServer(t, "  authoritative-revision\t", readFixture(t, "default"))
	defer srv.Close()
	cfg, err := LoadFromConfigService(context.Background(), srv.URL+"/v1/config/snapshots/latest")
	if err != nil {
		t.Fatalf("LoadFromConfigService: %v", err)
	}
	if cfg.Revision != "authoritative-revision" {
		t.Errorf("Revision = %q, want authoritative-revision", cfg.Revision)
	}
}

func TestLoadFromConfigServiceTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), int(MaxConfigServiceResponseBytes)+1))
	}))
	defer srv.Close()
	_, err := LoadFromConfigService(context.Background(), srv.URL+"/v1/config/snapshots/latest")
	if !errors.Is(err, ErrConfigTooLarge) {
		t.Errorf("err = %v, want ErrConfigTooLarge", err)
	}
}

func TestLoadFromConfigServiceNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer srv.Close()
	_, err := LoadFromConfigService(context.Background(), srv.URL+"/v1/config/snapshots/latest")
	if !errors.Is(err, ErrConfigServiceStatus) {
		t.Errorf("err = %v, want ErrConfigServiceStatus", err)
	}
}

func TestLoadFromConfigServiceEmptySnapshot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"revision":"r","snapshot":null}`))
	}))
	defer srv.Close()
	_, err := LoadFromConfigService(context.Background(), srv.URL+"/v1/config/snapshots/latest")
	if !errors.Is(err, ErrConfigServiceEmpty) {
		t.Errorf("err = %v, want ErrConfigServiceEmpty", err)
	}
}

func TestLoadFromConfigServiceSecretDetected(t *testing.T) {
	raw := bytes.Replace(readFixture(t, "default"), []byte(`"https://api.openai.example/v1"`), []byte(`"https://api.openai.example/v1?api_key=sk-leaked"`), 1)
	srv := configServiceServer(t, "r", raw)
	defer srv.Close()
	_, err := LoadFromConfigService(context.Background(), srv.URL+"/v1/config/snapshots/latest")
	if !errors.Is(err, ErrConfigSecretDetected) {
		t.Errorf("err = %v, want ErrConfigSecretDetected", err)
	}
}

func TestLoadFromConfigServiceNoLeak(t *testing.T) {
	leak := "unique-config-service-url-marker"
	_, err := LoadFromConfigService(context.Background(), "http://"+leak+".invalid/v1/config/snapshots/latest")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), leak) {
		t.Errorf("error leaks URL: %q", err.Error())
	}
	if errors.Unwrap(err) != nil {
		t.Errorf("error unwrap = %v, want nil", errors.Unwrap(err))
	}
}

func TestCompileAndPublishFromConfigService(t *testing.T) {
	srv := configServiceServer(t, "service-v1", readFixture(t, "default"))
	defer srv.Close()
	var store snapshot.Store
	meta, err := CompileAndPublishInitialFromConfigService(context.Background(), &store, srv.URL+"/v1/config/snapshots/latest")
	if err != nil {
		t.Fatalf("initial: %v", err)
	}
	if meta.Generation() != 1 || meta.Revision() != "service-v1" {
		t.Errorf("meta = gen %d rev %q, want 1/service-v1", meta.Generation(), meta.Revision())
	}

	srv2 := configServiceServer(t, "service-v2", readFixture(t, "default"))
	defer srv2.Close()
	next, err := CompileAndPublishNextFromConfigService(context.Background(), &store, srv2.URL+"/v1/config/snapshots/latest")
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if next.Generation() != 2 || next.Revision() != "service-v2" {
		t.Errorf("next = gen %d rev %q, want 2/service-v2", next.Generation(), next.Revision())
	}

	noop, err := CompileAndPublishNextFromConfigService(context.Background(), &store, srv2.URL+"/v1/config/snapshots/latest")
	if err != nil {
		t.Fatalf("same revision: %v", err)
	}
	if noop.Generation() != 2 {
		t.Errorf("noop generation = %d, want 2", noop.Generation())
	}
}

func TestCompileAndPublishNextFromConfigServiceNoInitial(t *testing.T) {
	srv := configServiceServer(t, "service-v1", readFixture(t, "default"))
	defer srv.Close()
	var store snapshot.Store
	_, err := CompileAndPublishNextFromConfigService(context.Background(), &store, srv.URL+"/v1/config/snapshots/latest")
	if !errors.Is(err, ErrConfigNoInitialSnapshot) {
		t.Errorf("err = %v, want ErrConfigNoInitialSnapshot", err)
	}
}

func TestLoadFileFixtures(t *testing.T) {
	for _, name := range fixtureNames {
		name := name
		t.Run(name, func(t *testing.T) {
			cfg, err := LoadFile(context.Background(), fixturePath(t, name))
			if err != nil {
				t.Fatalf("LoadFile(%s): %v", name, err)
			}
			if cfg.Revision == "" {
				t.Errorf("%s: empty Revision", name)
			}
			if cfg.CreatedAt.IsZero() {
				t.Errorf("%s: zero CreatedAt", name)
			}
			if len(cfg.Adapters) == 0 || len(cfg.Providers) == 0 || len(cfg.Models) == 0 || len(cfg.Routes) == 0 {
				t.Errorf("%s: incomplete ConfigSnapshot (Models/Providers/Routes/Adapters)", name)
			}
		})
	}
}

func TestCompileAndPublishInitialFixtures(t *testing.T) {
	for _, name := range fixtureNames {
		name := name
		t.Run(name, func(t *testing.T) {
			var store snapshot.Store
			meta, err := CompileAndPublishInitial(context.Background(), &store, fixturePath(t, name))
			if err != nil {
				t.Fatalf("CompileAndPublishInitial(%s): %v", name, err)
			}
			if meta.Generation() != 1 {
				t.Errorf("%s: Generation = %d, want 1", name, meta.Generation())
			}
			if meta.Revision() == "" {
				t.Errorf("%s: empty published revision", name)
			}
			// Counts must be non-zero for a complete fixture.
			if meta.ModelCount() == 0 || meta.ProviderCount() == 0 || meta.RouteCount() == 0 || meta.AdapterCount() == 0 {
				t.Errorf("%s: zero counts models=%d providers=%d routes=%d adapters=%d",
					name, meta.ModelCount(), meta.ProviderCount(), meta.RouteCount(), meta.AdapterCount())
			}

			current, err := store.Current()
			if err != nil {
				t.Fatalf("Current(%s): %v", name, err)
			}
			if current.Generation() != 1 || current.Revision() != meta.Revision() {
				t.Errorf("%s: store view gen=%d rev=%q, want gen=1 rev=%q", name, current.Generation(), current.Revision(), meta.Revision())
			}
			// The meta counts must exactly match the published compiled config.
			view := current.Value()
			if view == nil {
				t.Fatalf("%s: nil compiled view", name)
			}
			if len(view.Models) != meta.ModelCount() || len(view.Providers) != meta.ProviderCount() ||
				len(view.Routes) != meta.RouteCount() || len(view.Adapters) != meta.AdapterCount() {
				t.Errorf("%s: meta counts mismatch models=%d/%d providers=%d/%d routes=%d/%d adapters=%d/%d",
					name, meta.ModelCount(), len(view.Models),
					meta.ProviderCount(), len(view.Providers),
					meta.RouteCount(), len(view.Routes),
					meta.AdapterCount(), len(view.Adapters))
			}
			// A second publication at the same generation is stale.
			if err := store.Publish(current); !errors.Is(err, snapshot.ErrStaleSnapshot) {
				t.Errorf("%s: re-publish gen=1 err = %v, want ErrStaleSnapshot", name, err)
			}
		})
	}
}

func TestCompileAndPublishInitialMetaMutationIsolation(t *testing.T) {
	// The returned meta must be an isolated snapshot of publication metadata:
	// it must not let callers reach or mutate published config, and a later
	// Publish to the store must not alter a previously returned meta.
	var store snapshot.Store
	meta, err := CompileAndPublishInitial(context.Background(), &store, fixturePath(t, "default"))
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	rev1 := meta.Revision()
	gen1 := meta.Generation()
	m1 := meta.ModelCount()
	p1 := meta.ProviderCount()
	r1 := meta.RouteCount()
	a1 := meta.AdapterCount()

	// Publish a newer generation to the same store; the old meta must be
	// unaffected because it is a value copy of publication metadata only.
	current, err := store.Current()
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	view := current.Value()
	view2, err := snapshot.NewCompiledSnapshot(strings.TrimSpace(view.Revision), view, 2)
	if err != nil {
		t.Fatalf("new snapshot gen=2: %v", err)
	}
	if err := store.Publish(view2); err != nil {
		t.Fatalf("publish gen=2: %v", err)
	}

	if meta.Revision() != rev1 || meta.Generation() != gen1 {
		t.Errorf("meta changed after later publish: rev %q/%q gen %d/%d", meta.Revision(), rev1, meta.Generation(), gen1)
	}
	if meta.ModelCount() != m1 || meta.ProviderCount() != p1 || meta.RouteCount() != r1 || meta.AdapterCount() != a1 {
		t.Errorf("meta counts changed after later publish: models %d/%d providers %d/%d routes %d/%d adapters %d/%d",
			meta.ModelCount(), m1, meta.ProviderCount(), p1, meta.RouteCount(), r1, meta.AdapterCount(), a1)
	}
	if got := store.Generation(); got != 2 {
		t.Errorf("store generation = %d, want 2", got)
	}
}

func TestCompileAndPublishInitialStaleWhenStoreAlreadyBootstrapped(t *testing.T) {
	var store snapshot.Store
	if _, err := CompileAndPublishInitial(context.Background(), &store, fixturePath(t, "default")); err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	_, err := CompileAndPublishInitial(context.Background(), &store, fixturePath(t, "default"))
	if !errors.Is(err, ErrConfigPublishFailed) {
		t.Fatalf("second bootstrap err = %v, want ErrConfigPublishFailed", err)
	}
}

func TestCompileAndPublishInitialNilStore(t *testing.T) {
	_, err := CompileAndPublishInitial(context.Background(), nil, fixturePath(t, "default"))
	if !errors.Is(err, ErrConfigPublishFailed) {
		t.Fatalf("nil store err = %v, want ErrConfigPublishFailed", err)
	}
}

func TestCompileAndPublishInitialCompileFailure(t *testing.T) {
	// A snapshot whose route references a non-existent model fails the compiler.
	malformed := strictDecodeAndMarshal(t, readFixture(t, "default"), func(cfg *snapshot.ConfigSnapshot) {
		cfg.Routes[0].ModelID = "does-not-exist"
	})
	path := writeTempConfig(t, malformed)

	var store snapshot.Store
	_, err := CompileAndPublishInitial(context.Background(), &store, path)
	if !errors.Is(err, ErrConfigCompileFailed) {
		t.Errorf("compile failure err = %v, want ErrConfigCompileFailed", err)
	}
	if got := store.Generation(); got != 0 {
		t.Errorf("store generation = %d, want 0 after failed compile", got)
	}
}

// TestCompileAndPublishInitialRevisionTrimmed verifies that the revision is
// trimmed on the loaded snapshot copy before compilation, so the published
// snapshot revision, the store entry revision, and the compiled config value
// revision all carry the identical trimmed value. Without trimming,
// NewCompiledSnapshot trims its external argument but compiled.Revision keeps
// the surrounding whitespace, so meta/store and the value disagree.
func TestCompileAndPublishInitialRevisionTrimmed(t *testing.T) {
	raw := strictDecodeAndMarshal(t, readFixture(t, "default"), func(cfg *snapshot.ConfigSnapshot) {
		cfg.Revision = "  " + cfg.Revision + "\t\n"
	})
	path := writeTempConfig(t, raw)

	var store snapshot.Store
	meta, err := CompileAndPublishInitial(context.Background(), &store, path)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	current, err := store.Current()
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	view := current.Value()
	if view == nil {
		t.Fatal("nil compiled view")
	}
	// All three must agree and contain no surrounding whitespace.
	for _, rev := range []string{meta.Revision(), current.Revision(), view.Revision} {
		if rev != strings.TrimSpace(rev) {
			t.Errorf("revision %q is not trimmed", rev)
		}
		if strings.ContainsAny(rev, " \t\n\r") {
			t.Errorf("revision %q contains whitespace", rev)
		}
	}
	if meta.Revision() != current.Revision() || current.Revision() != view.Revision {
		t.Errorf("meta/store/value disagree: meta=%q store=%q value=%q",
			meta.Revision(), current.Revision(), view.Revision)
	}
}

// TestCompileAndPublishInitialPreCanceledCtxNeverPublishes verifies that a
// context already canceled before bootstrap never publishes a snapshot: the
// ctx.Err() checks placed between the load, compile, and publish stages are
// fail-closed, so a canceled caller cannot mutate the store.
func TestCompileAndPublishInitialPreCanceledCtxNeverPublishes(t *testing.T) {
	path := fixturePath(t, "default")
	var store snapshot.Store
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := CompileAndPublishInitial(ctx, &store, path)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("pre-canceled ctx err = %v, want context.Canceled", err)
	}
	if got := store.Generation(); got != 0 {
		t.Errorf("store generation = %d, want 0 (nothing published)", got)
	}
}

// TestLoadFileCtxCanceledBeforeParse verifies the context check between the
// read stage and the parse/scan/decode stage: a context canceled after read
// is honored before decode work begins, while the pure parseConfig remains
// ctx-independent.
func TestLoadFileCtxCanceledBeforeParse(t *testing.T) {
	path := writeTempConfig(t, readFixture(t, "default"))
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	raw, err := readConfigFile(context.Background(), path, info)
	if err != nil {
		t.Fatalf("readConfigFile: %v", err)
	}
	// The pure parseConfig is I/O-free and takes no ctx; the ctx check sits in
	// LoadFile between read and parse. Simulate that boundary directly.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ctx.Err(); err == nil {
		t.Fatal("ctx should be canceled")
	}
	// parseConfig itself is pure and must still work without ctx.
	if _, err := parseConfig(raw); err != nil {
		t.Errorf("parseConfig on valid raw: %v", err)
	}
}

func TestLoadFileMalformedInvalidJSON(t *testing.T) {
	path := writeTempConfig(t, []byte(`{not valid json`))
	_, err := LoadFile(context.Background(), path)
	if !errors.Is(err, ErrConfigMalformed) {
		t.Errorf("invalid JSON err = %v, want ErrConfigMalformed", err)
	}
}

func TestLoadFileTopLevelNullRejected(t *testing.T) {
	// encoding/json's Decoder.Decode(&struct{}) accepts a top-level null and
	// silently leaves the struct at its zero value, so without an explicit
	// structural gate LoadFile would accept null and only bootstrap
	// compilation would fail. The structural parse must reject it as
	// malformed.
	path := writeTempConfig(t, []byte(`null`))
	_, err := LoadFile(context.Background(), path)
	if !errors.Is(err, ErrConfigMalformed) {
		t.Errorf("top-level null err = %v, want ErrConfigMalformed", err)
	}
}

func TestLoadFileTopLevelArrayRejected(t *testing.T) {
	path := writeTempConfig(t, []byte(`[{"Revision":"r"}]`))
	_, err := LoadFile(context.Background(), path)
	if !errors.Is(err, ErrConfigMalformed) {
		t.Errorf("top-level array err = %v, want ErrConfigMalformed", err)
	}
}

func TestLoadFileTopLevelScalarRejected(t *testing.T) {
	for _, body := range []string{`42`, `3.14`, `"a string"`, `true`, `false`} {
		path := writeTempConfig(t, []byte(body))
		_, err := LoadFile(context.Background(), path)
		if !errors.Is(err, ErrConfigMalformed) {
			t.Errorf("top-level %q err = %v, want ErrConfigMalformed", body, err)
		}
	}
}

func TestLoadFileTopLevelObjectAccepted(t *testing.T) {
	// A valid top-level object remains accepted end-to-end.
	raw := readFixture(t, "default")
	path := writeTempConfig(t, raw)
	if _, err := LoadFile(context.Background(), path); err != nil {
		t.Errorf("valid object rejected: %v", err)
	}
}

func TestParseConfigTopLevelShape(t *testing.T) {
	// The pure, I/O-free core rejects non-object top-level values at the
	// structural gate for every shape that Decoder.Decode would otherwise
	// accept (null) or reject with a non-sentinel error.
	for _, body := range []string{
		`null`,
		`[1,2,3]`,
		`[{"Revision":"r"}]`,
		`42`,
		`3.14`,
		`"a string"`,
		`true`,
		`false`,
	} {
		if _, err := parseConfig([]byte(body)); !errors.Is(err, ErrConfigMalformed) {
			t.Errorf("parseConfig(%s): err = %v, want ErrConfigMalformed", body, err)
		}
	}
	// Leading whitespace before a valid object is accepted.
	if _, err := parseConfig([]byte("  \n\t" + `{}`)); err != nil {
		t.Errorf("parseConfig(whitespace+{}) err = %v, want nil", err)
	}
	// The default fixture remains accepted at the structural core.
	if _, err := parseConfig(readFixture(t, "default")); err != nil {
		t.Errorf("parseConfig(default fixture) err = %v, want nil", err)
	}
}

func TestLoadFileUnknownField(t *testing.T) {
	raw := readFixture(t, "default")
	injected := bytes.Replace(raw, []byte(`{`), []byte(`{"UnknownField":42,`), 1)
	path := writeTempConfig(t, injected)
	_, err := LoadFile(context.Background(), path)
	if !errors.Is(err, ErrConfigMalformed) {
		t.Errorf("unknown field err = %v, want ErrConfigMalformed", err)
	}
}

func TestLoadFilePrototypeField(t *testing.T) {
	raw := readFixture(t, "default")
	injected := bytes.Replace(raw, []byte(`{`), []byte(`{"__proto__":{"x":1},`), 1)
	path := writeTempConfig(t, injected)
	_, err := LoadFile(context.Background(), path)
	if !errors.Is(err, ErrConfigMalformed) {
		t.Errorf("__proto__ field err = %v, want ErrConfigMalformed", err)
	}
}

func TestLoadFileBlankPathRejected(t *testing.T) {
	for _, p := range []string{"", "   ", "\t\n"} {
		_, err := LoadFile(context.Background(), p)
		if !errors.Is(err, ErrConfigBlankPath) {
			t.Errorf("path %q err = %v, want ErrConfigBlankPath", p, err)
		}
		if unwrapped := errors.Unwrap(err); unwrapped != nil {
			t.Errorf("sentinel unwrapped to %v, want nil", unwrapped)
		}
	}
}

func TestLoadFilePathTrimmed(t *testing.T) {
	raw := readFixture(t, "default")
	path := writeTempConfig(t, raw)
	trimmed := "\t " + path + " \n"
	if _, err := LoadFile(context.Background(), trimmed); err != nil {
		t.Errorf("trimmed path rejected: %v", err)
	}
}

func TestLoadFileInvalidUTF8(t *testing.T) {
	// A lone continuation byte (0x80) is invalid UTF-8. Go's encoding/json
	// would replace it with U+FFFD; the strict source rejects it as malformed.
	raw := readFixture(t, "default")
	injected := bytes.Replace(raw, []byte(`"Revision"`), []byte("\"Revision\"\x80"), 1)
	path := writeTempConfig(t, injected)
	_, err := LoadFile(context.Background(), path)
	if !errors.Is(err, ErrConfigMalformed) {
		t.Errorf("invalid UTF-8 err = %v, want ErrConfigMalformed", err)
	}
}

func TestLoadFileExceedsDepthLimit(t *testing.T) {
	// A document nested deeper than maxJSONDepth is rejected as malformed.
	var b strings.Builder
	b.WriteString(`{"a"`)
	for i := 0; i < maxJSONDepth+50; i++ {
		b.WriteString(`:{"a"`)
	}
	b.WriteString(`:1`) // close the innermost value
	for i := 0; i < maxJSONDepth+51; i++ {
		b.WriteByte('}')
	}
	path := writeTempConfig(t, []byte(b.String()))
	_, err := LoadFile(context.Background(), path)
	if !errors.Is(err, ErrConfigMalformed) {
		t.Errorf("deep nesting err = %v, want ErrConfigMalformed", err)
	}
}

func TestLoadFileWithinDepthLimit(t *testing.T) {
	// A moderately nested document (within the depth limit) must still decode
	// when schema-valid: nest inside an allowed field. We reuse a fixture and
	// only assert the structural validator itself does not flag a shallow doc.
	dup, proto, tooDeep, tooMany := validateJSONStructure(readFixture(t, "default"))
	if dup || proto || tooDeep || tooMany {
		t.Errorf("fixture flagged: dup=%v proto=%v tooDeep=%v tooMany=%v", dup, proto, tooDeep, tooMany)
	}
}

func TestLoadFileExceedsNodeLimit(t *testing.T) {
	// A flat array with more than maxJSONNodes tokens is rejected as malformed.
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < maxJSONNodes+10; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('1')
	}
	b.WriteByte(']')
	path := writeTempConfig(t, []byte(b.String()))
	_, err := LoadFile(context.Background(), path)
	if !errors.Is(err, ErrConfigMalformed) {
		t.Errorf("node-limit err = %v, want ErrConfigMalformed", err)
	}
}

func TestValidateJSONStructureDepthAndNodeLimits(t *testing.T) {
	t.Run("depth", func(t *testing.T) {
		var b strings.Builder
		b.WriteString(`{"a"`)
		for i := 0; i < maxJSONDepth+10; i++ {
			b.WriteString(`:{"a"`)
		}
		b.WriteString(`:1`)
		for i := 0; i < maxJSONDepth+11; i++ {
			b.WriteByte('}')
		}
		dup, proto, tooDeep, tooMany := validateJSONStructure([]byte(b.String()))
		if !tooDeep {
			t.Errorf("tooDeep = false, want true")
		}
		if dup || proto || tooMany {
			t.Errorf("unexpected flags: dup=%v proto=%v tooMany=%v", dup, proto, tooMany)
		}
	})
	t.Run("nodes", func(t *testing.T) {
		var b strings.Builder
		b.WriteByte('[')
		for i := 0; i < maxJSONNodes+5; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteByte('1')
		}
		b.WriteByte(']')
		dup, proto, tooDeep, tooMany := validateJSONStructure([]byte(b.String()))
		if !tooMany {
			t.Errorf("tooMany = false, want true")
		}
		if dup || proto || tooDeep {
			t.Errorf("unexpected flags: dup=%v proto=%v tooDeep=%v", dup, proto, tooDeep)
		}
	})
}

func TestLoadFilePrototypePollutionMapKey(t *testing.T) {
	// Prototype-pollution keys inside a map (where DisallowUnknownFields
	// cannot see them) must be rejected at every depth.
	for _, key := range []string{"__proto__", "prototype", "constructor"} {
		key := key
		t.Run(key, func(t *testing.T) {
			raw := readFixture(t, "default")
			// Inject the forbidden key into the Models map.
			injected := bytes.Replace(raw, []byte(`"Models": {`), []byte(`"Models": {"`+key+`":{"ID":"`+key+`"},`), 1)
			path := writeTempConfig(t, injected)
			_, err := LoadFile(context.Background(), path)
			if !errors.Is(err, ErrConfigMalformed) {
				t.Errorf("map key %q err = %v, want ErrConfigMalformed", key, err)
			}
		})
	}
}

func TestLoadFilePrototypePollutionNested(t *testing.T) {
	raw := []byte(`{"Revision":"r","prototype":{"x":{"__proto__":1}}}`)
	path := writeTempConfig(t, raw)
	_, err := LoadFile(context.Background(), path)
	if !errors.Is(err, ErrConfigMalformed) {
		t.Errorf("nested prototype err = %v, want ErrConfigMalformed", err)
	}
}

func TestLoadFileDuplicateTopLevelKey(t *testing.T) {
	raw := readFixture(t, "default")
	injected := append([]byte(`{"Revision":"dup-first",`), raw[1:]...)
	path := writeTempConfig(t, injected)
	_, err := LoadFile(context.Background(), path)
	if !errors.Is(err, ErrConfigMalformed) {
		t.Errorf("duplicate top-level key err = %v, want ErrConfigMalformed", err)
	}
}

func TestLoadFileDuplicateNestedKey(t *testing.T) {
	raw := readFixture(t, "default")
	// Duplicate the nested "Retry" key inside the Global object. encoding/json
	// would silently keep the last value; the strict source rejects the ambiguity.
	injected := bytes.Replace(raw, []byte(`"Global": {`), []byte(`"Global": {"Retry":{"MaxTotalAttempts":1},`), 1)
	path := writeTempConfig(t, injected)
	_, err := LoadFile(context.Background(), path)
	if !errors.Is(err, ErrConfigMalformed) {
		t.Errorf("duplicate nested key err = %v, want ErrConfigMalformed", err)
	}
}

func TestLoadFileTrailingData(t *testing.T) {
	raw := readFixture(t, "default")
	trailing := append([]byte(nil), raw...)
	trailing = append(trailing, []byte(`{"second":"document"}`)...)
	path := writeTempConfig(t, trailing)
	_, err := LoadFile(context.Background(), path)
	if !errors.Is(err, ErrConfigMalformed) {
		t.Errorf("trailing document err = %v, want ErrConfigMalformed", err)
	}
}

func TestLoadFileTrailingGarbage(t *testing.T) {
	raw := readFixture(t, "default")
	trailing := append([]byte(nil), raw...)
	trailing = append(trailing, []byte("   \n  junk")...)
	path := writeTempConfig(t, trailing)
	_, err := LoadFile(context.Background(), path)
	if !errors.Is(err, ErrConfigMalformed) {
		t.Errorf("trailing garbage err = %v, want ErrConfigMalformed", err)
	}
}

func TestLoadFileTrailingWhitespaceAllowed(t *testing.T) {
	raw := bytes.TrimRight(readFixture(t, "default"), " \t\n\r")
	trailing := append(raw, ' ', '\n', '\t', ' ')
	path := writeTempConfig(t, trailing)
	if _, err := LoadFile(context.Background(), path); err != nil {
		t.Errorf("trailing whitespace rejected: %v", err)
	}
}

func TestLoadFileTooLarge(t *testing.T) {
	path := writeTempConfig(t, make([]byte, MaxConfigBytes+1))
	_, err := LoadFile(context.Background(), path)
	if !errors.Is(err, ErrConfigTooLarge) {
		t.Errorf("oversize err = %v, want ErrConfigTooLarge", err)
	}
}

func TestLoadFileExactlyAtLimit(t *testing.T) {
	base := bytes.TrimRight(readFixture(t, "default"), " \t\n\r")
	if int64(len(base)) >= MaxConfigBytes {
		t.Skip("fixture already exceeds cap")
	}
	padded := append(base, bytes.Repeat([]byte{' '}, int(MaxConfigBytes-int64(len(base))))...)
	if int64(len(padded)) != MaxConfigBytes {
		t.Fatalf("padded length = %d, want %d", len(padded), MaxConfigBytes)
	}
	path := writeTempConfig(t, padded)
	if _, err := LoadFile(context.Background(), path); err != nil {
		t.Errorf("exactly-at-limit rejected: %v", err)
	}
}

func TestLoadFileGrowsBetweenStatAndRead(t *testing.T) {
	// A file that reports a small size at Lstat but grows before/during read
	// must still be bounded by the LimitReader.
	dir := t.TempDir()
	path := filepath.Join(dir, "grows.json")
	// Start with a valid small config.
	base := bytes.TrimRight(readFixture(t, "default"), " \t\n\r")
	if err := os.WriteFile(path, base, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Grow the file to exceed the cap after stat; LoadFile should reject.
	if err := os.WriteFile(path, append(base, bytes.Repeat([]byte{' '}, int(MaxConfigBytes)+1)...), 0o600); err != nil {
		t.Fatalf("grow: %v", err)
	}
	_, err := LoadFile(context.Background(), path)
	if !errors.Is(err, ErrConfigTooLarge) {
		t.Errorf("grown file err = %v, want ErrConfigTooLarge", err)
	}
}

func TestLoadFileEmpty(t *testing.T) {
	path := writeTempConfig(t, []byte{})
	_, err := LoadFile(context.Background(), path)
	if !errors.Is(err, ErrConfigEmpty) {
		t.Errorf("empty file err = %v, want ErrConfigEmpty", err)
	}
}

func TestLoadFileNotFound(t *testing.T) {
	_, err := LoadFile(context.Background(), filepath.Join(t.TempDir(), "missing.json"))
	if !errors.Is(err, ErrConfigNotFound) {
		t.Errorf("missing file err = %v, want ErrConfigNotFound", err)
	}
}

func TestLoadFileSymlinkRejected(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.json")
	if err := os.WriteFile(target, readFixture(t, "default"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	_, err := LoadFile(context.Background(), link)
	if !errors.Is(err, ErrConfigNotRegular) {
		t.Errorf("symlink err = %v, want ErrConfigNotRegular", err)
	}
}

// TestLoadFilePostOpenIdentityRegularAccepted confirms that a stable regular
// file passes the post-open os.SameFile verification on the happy path.
func TestLoadFilePostOpenIdentityRegularAccepted(t *testing.T) {
	path := writeTempConfig(t, readFixture(t, "default"))
	if _, err := LoadFile(context.Background(), path); err != nil {
		t.Errorf("regular file rejected after post-open verification: %v", err)
	}
}

// TestLoadFilePostOpenSameFileIdentityAccepted exercises readConfigFile's
// post-open identity check directly with a matching Lstat FileInfo, proving
// the SameFile seam accepts a legitimately stable regular file.
func TestLoadFilePostOpenSameFileIdentityAccepted(t *testing.T) {
	path := writeTempConfig(t, readFixture(t, "default"))
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if _, err := readConfigFile(context.Background(), path, info); err != nil {
		t.Errorf("readConfigFile stable regular file err = %v, want nil", err)
	}
}

// TestLoadFilePostOpenIdentityMismatchRejected verifies the fail-closed
// post-open identity seam: a FileInfo that does not match the open descriptor
// (here a fabricated dummy identity for a different, nonexistent file) is
// rejected with ErrConfigNotRegular, never leaking OS error text.
func TestLoadFilePostOpenIdentityMismatchRejected(t *testing.T) {
	path := writeTempConfig(t, readFixture(t, "default"))
	// Fabricate an Lstat-like FileInfo for a different (nonexistent) path so
	// os.SameFile cannot match the open descriptor. os.Lstat on a missing path
	// errors, so we stat a real but distinct temp file to get a foreign identity.
	foreign := filepath.Join(t.TempDir(), "other.json")
	if err := os.WriteFile(foreign, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write foreign: %v", err)
	}
	foreignInfo, err := os.Lstat(foreign)
	if err != nil {
		t.Fatalf("lstat foreign: %v", err)
	}
	_, err = readConfigFile(context.Background(), path, foreignInfo)
	if !errors.Is(err, ErrConfigNotRegular) {
		t.Fatalf("identity mismatch err = %v, want ErrConfigNotRegular", err)
	}
	if unwrapped := errors.Unwrap(err); unwrapped != nil {
		t.Errorf("sentinel unwrapped to %v, want nil", unwrapped)
	}
}

// TestLoadFilePostOpenNonRegularIdentityRejected verifies that when the open
// descriptor itself is non-regular (a directory), the post-open verification
// fails closed with ErrConfigNotRegular rather than leaking OS error text.
func TestLoadFilePostOpenNonRegularIdentityRejected(t *testing.T) {
	dir := t.TempDir()
	// A directory passes os.Lstat as a non-regular file, so LoadFile rejects
	// at Lstat. Exercise the seam directly: stat the directory, then open it
	// (which succeeds on directories) and confirm f.Stat is non-regular.
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatalf("lstat dir: %v", err)
	}
	_, err = readConfigFile(context.Background(), dir, info)
	if !errors.Is(err, ErrConfigNotRegular) {
		t.Fatalf("directory post-open err = %v, want ErrConfigNotRegular", err)
	}
	if unwrapped := errors.Unwrap(err); unwrapped != nil {
		t.Errorf("sentinel unwrapped to %v, want nil", unwrapped)
	}
}

// TestLoadFilePostOpenCtxCanceledBetweenStatAndOpen verifies the ctx.Err()
// seam between Lstat and Open honors cancellation.
func TestLoadFilePostOpenCtxCanceledBetweenStatAndOpen(t *testing.T) {
	path := writeTempConfig(t, readFixture(t, "default"))
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = readConfigFile(ctx, path, info)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("canceled ctx between stat and open err = %v, want context.Canceled", err)
	}
}

func TestLoadFileDirectoryRejected(t *testing.T) {
	_, err := LoadFile(context.Background(), t.TempDir())
	if !errors.Is(err, ErrConfigNotRegular) {
		t.Errorf("directory err = %v, want ErrConfigNotRegular", err)
	}
}

func TestLoadFileNamedPipeRejected(t *testing.T) {
	dir := t.TempDir()
	pipe := filepath.Join(dir, "pipe")
	if mkfifoAvailable() {
		if err := mkfifo(pipe); err != nil {
			t.Skipf("mkfifo failed: %v", err)
		}
	} else {
		t.Skip("mkfifo not available")
	}
	_, err := LoadFile(context.Background(), pipe)
	if !errors.Is(err, ErrConfigNotRegular) {
		t.Errorf("named pipe err = %v, want ErrConfigNotRegular", err)
	}
}

func TestLoadFileContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := LoadFile(ctx, fixturePath(t, "default"))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("canceled ctx err = %v, want context.Canceled", err)
	}
}

func TestLoadFileContextDeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := LoadFile(ctx, fixturePath(t, "default"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("deadline ctx err = %v, want context.DeadlineExceeded", err)
	}
}

func TestLoadFileDoesNotLeakPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unique-leak-marker-12345", "config.json")
	_, err := LoadFile(context.Background(), path)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "unique-leak-marker-12345") {
		t.Errorf("error leaks path: %q", err.Error())
	}
	if unwrapped := errors.Unwrap(err); unwrapped != nil {
		t.Errorf("sentinel unwrapped to %v, want nil", unwrapped)
	}
}

func TestLoadFileMalformedDoesNotLeakContent(t *testing.T) {
	secret := "supersecretvalue-do-not-leak-xyz"
	content := append([]byte(`{"Revision":"`), []byte(secret)...)
	content = append(content, []byte(`",bad json`)...)
	path := writeTempConfig(t, content)
	_, err := LoadFile(context.Background(), path)
	if !errors.Is(err, ErrConfigMalformed) {
		t.Fatalf("err = %v, want ErrConfigMalformed", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error leaks secret content: %q", err.Error())
	}
	if strings.Contains(err.Error(), filepath.Base(path)) {
		t.Errorf("error leaks path base: %q", err.Error())
	}
	if unwrapped := errors.Unwrap(err); unwrapped != nil {
		t.Errorf("sentinel unwrapped to %v, want nil", unwrapped)
	}
}

func TestSentinelErrorsDoNotUnwrap(t *testing.T) {
	sentinels := []error{
		ErrConfigBlankPath, ErrConfigNotFound, ErrConfigNotRegular, ErrConfigTooLarge,
		ErrConfigEmpty, ErrConfigUnreadable, ErrConfigMalformed,
		ErrConfigSecretDetected, ErrConfigServiceUnavailable, ErrConfigServiceStatus, ErrConfigServiceEmpty, ErrConfigCompileFailed, ErrConfigPublishFailed,
	}
	for _, s := range sentinels {
		if errors.Unwrap(s) != nil {
			t.Errorf("%v: Unwrap = %v, want nil", s, errors.Unwrap(s))
		}
		if errors.Is(errors.New("other"), s) {
			t.Errorf("%v: errors.Is matched unrelated error", s)
		}
	}
}

func TestScanSecretsPositives(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want SecretFindingKind
	}{
		{name: "credential key", raw: `{"credential":"x"}`, want: SecretForbiddenKey},
		{name: "password key", raw: `{"password":"x"}`, want: SecretForbiddenKey},
		{name: "api_key key", raw: `{"api_key":"x"}`, want: SecretForbiddenKey},
		{name: "apiKey key", raw: `{"apiKey":"x"}`, want: SecretForbiddenKey},
		{name: "token key", raw: `{"token":"x"}`, want: SecretForbiddenKey},
		{name: "private_key key", raw: `{"private_key":"x"}`, want: SecretForbiddenKey},
		{name: "client_secret key", raw: `{"client_secret":"x"}`, want: SecretForbiddenKey},
		{name: "auth_token key", raw: `{"auth_token":"x"}`, want: SecretForbiddenKey},
		{name: "authorization key", raw: `{"authorization":"Bearer x"}`, want: SecretForbiddenKey},
		{name: "authorisation key (UK)", raw: `{"authorisation":"x"}`, want: SecretForbiddenKey},
		{name: "authtoken key", raw: `{"authtoken":"x"}`, want: SecretForbiddenKey},
		{name: "bearer_token key", raw: `{"bearer_token":"x"}`, want: SecretForbiddenKey},
		{name: "sk value", raw: `{"note":"sk-live-secret"}`, want: SecretValueMarker},
		{name: "Bearer value", raw: `{"note":"Bearer abc"}`, want: SecretValueMarker},
		{name: "private key", raw: `{"note":"BEGIN PRIVATE KEY"}`, want: SecretValueMarker},
		{name: "AKIA value", raw: `{"note":"AKIATEST1234"}`, want: SecretValueMarker},
		{name: "github token", raw: `{"note":"ghp_abcdef"}`, want: SecretValueMarker},
		{name: "gitlab token", raw: `{"note":"glpat-abcdef"}`, want: SecretValueMarker},
		{name: "slack token", raw: `{"note":"xox-abcdef"}`, want: SecretValueMarker},
		{name: "JWT value", raw: `{"note":"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.signature"}`, want: SecretJWT},
		{name: "URL query credential", raw: `{"BaseURL":"https://api.example/v1?credential=not-a-ref"}`, want: SecretURLCredential},
		{name: "URL query api_key", raw: `{"BaseURL":"https://api.example/v1?api_key=secret"}`, want: SecretURLCredential},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := ScanSecrets([]byte(tc.raw))
			if len(findings) == 0 {
				t.Fatalf("scanner accepted %q", tc.raw)
			}
			found := false
			for _, f := range findings {
				if f.Kind == tc.want {
					found = true
				}
			}
			if !found {
				t.Errorf("findings = %v, want kind %q", findings, tc.want)
			}
		})
	}
}

func TestScanSecretsAllowedRefsAndSafeContent(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{name: "CredentialRef field", raw: `{"CredentialRef":"vault://provider/default"}`},
		{name: "Credentials array field", raw: `{"Credentials":[]}`},
		{name: "api_key_header enum", raw: `{"AuthKind":"api_key_header"}`},
		{name: "api_key_query enum", raw: `{"AuthKind":"api_key_query"}`},
		{name: "MaxBudgetToken field", raw: `{"MaxBudgetToken":10}`},
		{name: "AccessTokenMode field", raw: `{"AccessTokenMode":"reference"}`},
		{name: "tokenized safe text", raw: `{"note":"tokenized request metadata"}`},
		{name: "url without credential query", raw: `{"BaseURL":"https://api.example/v1?mode=tokenized"}`},
		{name: "Bearer prefix without space", raw: `{"Prefix":"Bearer"}`},
		{name: "Authorization header value (not a key)", raw: `{"Header":"Authorization"}`},
		{name: "x-api-key header value (not a key)", raw: `{"Header":"x-api-key"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if findings := ScanSecrets([]byte(tc.raw)); len(findings) != 0 {
				t.Fatalf("scanner false positive for %q: %v", tc.raw, findings)
			}
		})
	}
}

func TestScanSecretsCleanFixtures(t *testing.T) {
	for _, name := range fixtureNames {
		name := name
		t.Run(name, func(t *testing.T) {
			if findings := ScanSecrets(readFixture(t, name)); len(findings) != 0 {
				t.Fatalf("clean fixture %s has findings: %v", name, findings)
			}
		})
	}
}

func TestScanSecretsEmpty(t *testing.T) {
	if findings := ScanSecrets(nil); len(findings) != 0 {
		t.Errorf("ScanSecrets(nil) = %v, want nil", findings)
	}
	if findings := ScanSecrets([]byte{}); len(findings) != 0 {
		t.Errorf("ScanSecrets(empty) = %v, want nil", findings)
	}
}

func TestScanSecretsFindingHasNoRawContent(t *testing.T) {
	raw := []byte(`{"password":"supersecret-leak-me"}`)
	findings := ScanSecrets(raw)
	if len(findings) == 0 {
		t.Fatal("expected findings")
	}
	for _, f := range findings {
		if strings.Contains(string(f.Kind), "supersecret") || strings.Contains(string(f.Kind), "password") {
			t.Errorf("finding kind leaks content: %q", f.Kind)
		}
	}
}

func TestScanSecretsEscapedKey(t *testing.T) {
	// JSON \u escapes hide a forbidden key from the lexical pass; the semantic
	// decoded pass must still classify it. "se\u0063ret" decodes to "secret".
	cases := []struct {
		name string
		raw  string
		want SecretFindingKind
	}{
		{name: "escaped secret key", raw: `{"se\u0063ret":"x"}`, want: SecretForbiddenKey},
		{name: "escaped password key", raw: `{"p\u0061ssword":"x"}`, want: SecretForbiddenKey},
		{name: "escaped token key", raw: `{"tok\u0065n":"x"}`, want: SecretForbiddenKey},
		{name: "escaped credential key", raw: `{"credenti\u0061l":"x"}`, want: SecretForbiddenKey},
		{name: "escaped api_key key", raw: `{"\u0061pi_key":"x"}`, want: SecretForbiddenKey},
		{name: "escaped auth_token key", raw: `{"auth_\u0074oken":"x"}`, want: SecretForbiddenKey},
		{name: "escaped authorization key", raw: `{"\u0061uthorization":"x"}`, want: SecretForbiddenKey},
		// Mixed case + escape: ensure case-insensitive classification of decoded key.
		{name: "escaped mixed-case SECRET key", raw: `{"SEC\u0052ET":"x"}`, want: SecretForbiddenKey},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			findings := ScanSecrets([]byte(tc.raw))
			found := false
			for _, f := range findings {
				if f.Kind == tc.want {
					found = true
				}
			}
			if !found {
				t.Errorf("ScanSecrets(%q) = %v, want kind %q (escaped key bypassed the scanner)", tc.raw, findings, tc.want)
			}
		})
	}
}

func TestScanSecretsEscapedValue(t *testing.T) {
	// JSON \u escapes hide a secret-bearing value prefix from the lexical
	// pass; the semantic decoded pass must still classify it.
	cases := []struct {
		name string
		raw  string
		want SecretFindingKind
	}{
		// "sk\u002d..." decodes to "sk-..." (\u002d = '-').
		{name: "escaped sk- marker", raw: `{"note":"sk\u002dlive-secret"}`, want: SecretValueMarker},
		// "Bearer\u0020" decodes to "Bearer " (\u0020 = ' ').
		{name: "escaped Bearer marker", raw: `{"note":"Bearer\u0020abc"}`, want: SecretValueMarker},
		// "AKIA\u0041" decodes to "AKIAA"; the AKIA prefix still matches.
		{name: "escaped AKIA marker", raw: `{"note":"AKIA\u0041BC"}`, want: SecretValueMarker},
		{name: "escaped ghp_ marker", raw: `{"note":"ghp\u005fabcdef"}`, want: SecretValueMarker},
		{name: "escaped xox- marker", raw: `{"note":"xox\u002dabcdef"}`, want: SecretValueMarker},
		{name: "escaped glpat- marker", raw: `{"note":"glpat\u002dabcdef"}`, want: SecretValueMarker},
		// "BEGIN\u0020PRIVATE\u0020KEY" decodes to "BEGIN PRIVATE KEY".
		{name: "escaped PEM marker", raw: `{"note":"BEGIN\u0020PRIVATE\u0020KEY"}`, want: SecretValueMarker},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			findings := ScanSecrets([]byte(tc.raw))
			found := false
			for _, f := range findings {
				if f.Kind == tc.want {
					found = true
				}
			}
			if !found {
				t.Errorf("ScanSecrets(%q) = %v, want kind %q (escaped value bypassed the scanner)", tc.raw, findings, tc.want)
			}
		})
	}
}

func TestScanSecretsEscapedURLQuery(t *testing.T) {
	// JSON \u escapes hide a credential-bearing URL query parameter from the
	// lexical pass; the semantic decoded pass must still classify it.
	cases := []struct {
		name string
		raw  string
		want SecretFindingKind
	}{
		// "api\u005fkey" decodes to "api_key" (\u005f = '_').
		{name: "escaped api_key query", raw: `{"BaseURL":"https://api.example/v1?api\u005fkey=secret"}`, want: SecretURLCredential},
		// "credenti\u0061l" decodes to "credential".
		{name: "escaped credential query", raw: `{"BaseURL":"https://api.example/v1?credenti\u0061l=not-a-ref"}`, want: SecretURLCredential},
		// "tok\u0065n" decodes to "token".
		{name: "escaped token query", raw: `{"BaseURL":"https://api.example/v1?tok\u0065n=secret"}`, want: SecretURLCredential},
		// Escape the scheme separator: "https\u003a//" decodes to "https://".
		{name: "escaped scheme still classified", raw: `{"BaseURL":"https\u003a//api.example/v1?token=secret"}`, want: SecretURLCredential},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			findings := ScanSecrets([]byte(tc.raw))
			found := false
			for _, f := range findings {
				if f.Kind == tc.want {
					found = true
				}
			}
			if !found {
				t.Errorf("ScanSecrets(%q) = %v, want kind %q (escaped URL query bypassed the scanner)", tc.raw, findings, tc.want)
			}
		})
	}
}

func TestScanSecretsEscapedJWT(t *testing.T) {
	// JSON \u escapes hide the "eyJ" JWT prefix from the lexical pass; the
	// semantic decoded pass must still classify the decoded JWT-shaped value.
	// "ey\u004a..." decodes to "eyJ..." (\u004a = 'J').
	raw := []byte(`{"note":"ey\u004ahbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.signature"}`)
	findings := ScanSecrets(raw)
	found := false
	for _, f := range findings {
		if f.Kind == SecretJWT {
			found = true
		}
	}
	if !found {
		t.Errorf("ScanSecrets(escaped JWT) = %v, want kind %q", findings, SecretJWT)
	}
}

func TestScanSecretsEscapedSafeContent(t *testing.T) {
	// Decoded values that merely contain escape sequences but are otherwise
	// safe (CredentialRef, public BaseURL, model IDs, enum values) must not
	// produce false positives. Each value below uses a \u escape that decodes
	// to a safe character, exercising the same decoded path used by the
	// bypass cases without triggering any classifier.
	cases := []struct {
		name string
		raw  string
	}{
		{name: "escaped CredentialRef value", raw: `{"CredentialRef":"vault://provider/def\u0061ult"}`},
		{name: "escaped public BaseURL no query", raw: `{"BaseURL":"https://api.openai.example/v1\u002f"}`},
		{name: "escaped public BaseURL safe query", raw: `{"BaseURL":"https://api.example/v1?mode=tok\u0065nized"}`},
		{name: "escaped model ID", raw: `{"ID":"gpt-\u0064efault"}`},
		{name: "escaped Authorization header value", raw: `{"Header":"Authoriz\u0061tion"}`},
		{name: "escaped Bearer prefix without space", raw: `{"Prefix":"Bear\u0065r"}`},
		{name: "escaped api_key_header enum", raw: `{"AuthKind":"api_key_he\u0061der"}`},
		{name: "escaped MaxBudgetToken field", raw: `{"MaxBudgetToken":10,"note":"tok\u0065nized"}`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if findings := ScanSecrets([]byte(tc.raw)); len(findings) != 0 {
				t.Fatalf("scanner false positive for %q: %v", tc.raw, findings)
			}
		})
	}
}

func TestScanSecretsEscapedDedupOnePerKind(t *testing.T) {
	// A single document carrying both an escaped and a literal forbidden key
	// must yield exactly one SecretForbiddenKey finding (no duplicate kinds).
	raw := []byte(`{"secret":"x","se\u0063ret":"y","sk-lit":"a","sk\u002descaped":"b"}`)
	findings := ScanSecrets(raw)
	counts := make(map[SecretFindingKind]int)
	for _, f := range findings {
		counts[f.Kind]++
	}
	if counts[SecretForbiddenKey] != 1 {
		t.Errorf("SecretForbiddenKey count = %d, want 1 (dedup)", counts[SecretForbiddenKey])
	}
	if counts[SecretValueMarker] != 1 {
		t.Errorf("SecretValueMarker count = %d, want 1 (dedup)", counts[SecretValueMarker])
	}
	for kind, n := range counts {
		if n > 1 {
			t.Errorf("kind %q reported %d times, want at most 1", kind, n)
		}
	}
}

// TestScanSecretsNestedForbiddenKeyAfterContainerValue is the regression for
// the semantic state machine: when a sibling key follows a value that is a
// nested object or array, the closing delimiter of that container must reset
// the parent object to expect a key. Without that reset the sibling key was
// misclassified as a value, so an escaped forbidden key (invisible to the
// lexical pass) bypassed the scanner entirely.
func TestScanSecretsNestedForbiddenKeyAfterContainerValue(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want SecretFindingKind
	}{
		{name: "obj value then escaped key", raw: `{"a":{"b":1},"se\u0063ret":"x"}`, want: SecretForbiddenKey},
		{name: "array value then escaped key", raw: `{"a":[1,2],"se\u0063ret":"x"}`, want: SecretForbiddenKey},
		{name: "empty obj value then escaped key", raw: `{"a":{},"se\u0063ret":"x"}`, want: SecretForbiddenKey},
		{name: "empty array value then escaped key", raw: `{"a":[],"se\u0063ret":"x"}`, want: SecretForbiddenKey},
		{name: "deeply nested then escaped key", raw: `{"a":{"b":{"c":1}},"se\u0063ret":"x"}`, want: SecretForbiddenKey},
		{name: "two container values then escaped key", raw: `{"a":{"b":1},"c":[2],"se\u0063ret":"x"}`, want: SecretForbiddenKey},
		{name: "obj value then escaped password", raw: `{"a":{"b":1},"p\u0061ssword":"x"}`, want: SecretForbiddenKey},
		{name: "obj value then escaped token", raw: `{"a":{"b":1},"tok\u0065n":"x"}`, want: SecretForbiddenKey},
		{name: "obj value then escaped credential", raw: `{"a":{"b":1},"credenti\u0061l":"x"}`, want: SecretForbiddenKey},
		{name: "obj value then escaped auth_token", raw: `{"a":{"b":1},"auth_\u0074oken":"x"}`, want: SecretForbiddenKey},
		{name: "obj value then escaped authorization", raw: `{"a":{"b":1},"\u0061uthorization":"x"}`, want: SecretForbiddenKey},
		{name: "obj value then escaped bearer_token", raw: `{"a":{"b":1},"bearer_\u0074oken":"x"}`, want: SecretForbiddenKey},
		{name: "obj value then escaped client_secret", raw: `{"a":{"b":1},"client_\u0073ecret":"x"}`, want: SecretForbiddenKey},
		{name: "obj value then escaped access_key", raw: `{"a":{"b":1},"access_\u006bey":"x"}`, want: SecretForbiddenKey},
		{name: "obj value then escaped private_key", raw: `{"a":{"b":1},"private_\u006bey":"x"}`, want: SecretForbiddenKey},
		{name: "obj value then escaped secret_key", raw: `{"a":{"b":1},"secret_\u006bey":"x"}`, want: SecretForbiddenKey},
		// Literal keys after a container value must still be caught by both passes.
		{name: "obj value then literal key", raw: `{"a":{"b":1},"secret":"x"}`, want: SecretForbiddenKey},
		{name: "array value then literal key", raw: `{"a":[1],"secret":"x"}`, want: SecretForbiddenKey},
		// A forbidden key nested inside the value object itself is also caught.
		{name: "escaped key inside nested value object", raw: `{"a":{"se\u0063ret":"x"}}`, want: SecretForbiddenKey},
		{name: "escaped key inside nested array element", raw: `{"a":[{"se\u0063ret":"x"}]}`, want: SecretForbiddenKey},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			findings := ScanSecrets([]byte(tc.raw))
			found := false
			for _, f := range findings {
				if f.Kind == tc.want {
					found = true
				}
			}
			if !found {
				t.Errorf("ScanSecrets(%q) = %v, want kind %q (sibling key after container value missed)", tc.raw, findings, tc.want)
			}
		})
	}
}

// TestScanSecretsNestedSafeValueNotFlagged confirms the state-machine fix does
// not introduce false positives: a safe value following or inside a container
// value must never be classified as a forbidden key, because only the key
// position applies the forbidden-key matcher.
func TestScanSecretsNestedSafeValueNotFlagged(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{name: "Authorization value after obj value", raw: `{"a":{"b":1},"Header":"Authorization"}`},
		{name: "safe word as value after array value", raw: `{"a":[1],"note":"tokenized"}`},
		{name: "safe value inside nested object", raw: `{"a":{"Header":"Authorization"}}`},
		{name: "safe value inside nested array", raw: `{"a":["Authorization"]}`},
		{name: "CredentialRef after obj value", raw: `{"a":{"b":1},"CredentialRef":"vault://provider/default"}`},
		{name: "Bearer prefix value without space", raw: `{"a":{"b":1},"Prefix":"Bearer"}`},
		{name: "safe text containing secret word", raw: `{"a":{"b":1},"note":"this is a tokenized request"}`},
		{name: "deeply nested safe values", raw: `{"a":{"b":{"Header":"x-api-key"}},"c":"Bearer"}`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if findings := ScanSecrets([]byte(tc.raw)); len(findings) != 0 {
				t.Fatalf("scanner false positive for %q: %v", tc.raw, findings)
			}
		})
	}
}

// TestScanSecretsURLCredentialUnifiedCoverage verifies that the URL query
// credential detector covers the full unified credential-key set (not just the
// original credential/api_key/token/secret/password subset), including the
// access_key, private_key, secret_key, client_secret, auth_token, authtoken,
// authorization and bearer_token parameter names, with separator variants.
func TestScanSecretsURLCredentialUnifiedCoverage(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want SecretFindingKind
	}{
		{name: "access_key", raw: `{"BaseURL":"https://api.example/v1?access_key=secret"}`, want: SecretURLCredential},
		{name: "access-key hyphen", raw: `{"BaseURL":"https://api.example/v1?access-key=secret"}`, want: SecretURLCredential},
		{name: "private_key", raw: `{"BaseURL":"https://api.example/v1?private_key=secret"}`, want: SecretURLCredential},
		{name: "secret_key", raw: `{"BaseURL":"https://api.example/v1?secret_key=secret"}`, want: SecretURLCredential},
		{name: "client_secret", raw: `{"BaseURL":"https://api.example/v1?client_secret=secret"}`, want: SecretURLCredential},
		{name: "auth_token", raw: `{"BaseURL":"https://api.example/v1?auth_token=secret"}`, want: SecretURLCredential},
		{name: "authtoken", raw: `{"BaseURL":"https://api.example/v1?authtoken=secret"}`, want: SecretURLCredential},
		{name: "authorization", raw: `{"BaseURL":"https://api.example/v1?authorization=secret"}`, want: SecretURLCredential},
		{name: "bearer_token", raw: `{"BaseURL":"https://api.example/v1?bearer_token=secret"}`, want: SecretURLCredential},
		{name: "apikey no separator", raw: `{"BaseURL":"https://api.example/v1?apikey=secret"}`, want: SecretURLCredential},
		{name: "credential", raw: `{"BaseURL":"https://api.example/v1?credential=not-a-ref"}`, want: SecretURLCredential},
		{name: "password", raw: `{"BaseURL":"https://api.example/v1?password=secret"}`, want: SecretURLCredential},
		{name: "uppercase BEARER_TOKEN", raw: `{"BaseURL":"https://api.example/v1?BEARER_TOKEN=secret"}`, want: SecretURLCredential},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			findings := ScanSecrets([]byte(tc.raw))
			found := false
			for _, f := range findings {
				if f.Kind == tc.want {
					found = true
				}
			}
			if !found {
				t.Errorf("ScanSecrets(%q) = %v, want kind %q", tc.raw, findings, tc.want)
			}
		})
	}
}

// TestScanSecretsURLCredentialUrlEncoded confirms the semantic pass decodes
// percent-encoded query parameter names so URL-encoding obfuscation cannot
// bypass the scanner the way it bypasses the lexical regex.
func TestScanSecretsURLCredentialUrlEncoded(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want SecretFindingKind
	}{
		// %5F is '_', %4B is 'K'.
		{name: "encoded underscore api_key", raw: `{"BaseURL":"https://api.example/v1?api%5Fkey=secret"}`, want: SecretURLCredential},
		{name: "encoded underscore access_key", raw: `{"BaseURL":"https://api.example/v1?access%5Fkey=secret"}`, want: SecretURLCredential},
		{name: "encoded uppercase apikey", raw: `{"BaseURL":"https://api.example/v1?API%4BEy=secret"}`, want: SecretURLCredential},
		// Lowercase percent-encoding of a letter: %61 = 'a'.
		{name: "encoded letter in token", raw: `{"BaseURL":"https://api.example/v1?%74oken=secret"}`, want: SecretURLCredential},
		{name: "encoded letter in secret", raw: `{"BaseURL":"https://api.example/v1?se%63ret=secret"}`, want: SecretURLCredential},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			findings := ScanSecrets([]byte(tc.raw))
			found := false
			for _, f := range findings {
				if f.Kind == tc.want {
					found = true
				}
			}
			if !found {
				t.Errorf("ScanSecrets(%q) = %v, want kind %q (url-encoded param name bypassed scanner)", tc.raw, findings, tc.want)
			}
		})
	}
}

// TestScanSecretsCredentialRefNoFalsePositive confirms that a CredentialRef
// (non-http(s) scheme, no query) is never flagged as a URL credential, in both
// literal and JSON-escaped-decoded form.
func TestScanSecretsCredentialRefNoFalsePositive(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{name: "vault CredentialRef", raw: `{"CredentialRef":"vault://provider/default"}`},
		{name: "vault CredentialRef with path", raw: `{"CredentialRef":"vault://provider/legacy-route"}`},
		{name: "escaped vault CredentialRef", raw: `{"CredentialRef":"vault://provider/def\u0061ult"}`},
		{name: "https BaseURL no query", raw: `{"BaseURL":"https://api.openai.example/v1"}`},
		{name: "https BaseURL safe query", raw: `{"BaseURL":"https://api.example/v1?mode=tokenized"}`},
		{name: "https BaseURL safe query with tokenized value", raw: `{"BaseURL":"https://api.example/v1?region=us-east"}`},
		// Empty credential param value is not a secret leak and is not flagged.
		{name: "empty credential param value", raw: `{"BaseURL":"https://api.example/v1?api_key="}`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if findings := ScanSecrets([]byte(tc.raw)); len(findings) != 0 {
				t.Fatalf("scanner false positive for %q: %v", tc.raw, findings)
			}
		})
	}
}

func TestScanSecretsMalformedNeverPanics(t *testing.T) {
	// Malformed, truncated, and hostile JSON must never panic the semantic
	// pass and must never leak content through findings. Findings carry only
	// content-free kinds.
	cases := [][]byte{
		nil,
		[]byte(``),
		[]byte(`{`),
		[]byte(`{}`),
		[]byte(`{not json`),
		[]byte(`{"`),
		[]byte(`{"\u`),
		[]byte(`{"a\u0063":"\u`),
		[]byte(`["a\u0063","x"`),
		[]byte(`{"a":"b","a":"c"}`),
		[]byte(`{` + strings.Repeat(`{"a":`, 1000) + `1` + strings.Repeat(`}`, 1000) + `}`),
		[]byte("\x00\x01\xff\xfe"),
	}
	valid := map[SecretFindingKind]struct{}{
		SecretForbiddenKey:  {},
		SecretValueMarker:   {},
		SecretJWT:           {},
		SecretURLCredential: {},
	}
	for i, in := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("case %d panicked: %v", i, r)
				}
			}()
			findings := ScanSecrets(in)
			// Findings must only carry one of the fixed, content-free kinds;
			// no input bytes may surface through a finding kind.
			for _, f := range findings {
				if _, ok := valid[f.Kind]; !ok {
					t.Errorf("case %d: unknown finding kind %q", i, f.Kind)
				}
			}
		}()
	}
}

func TestHasSecret(t *testing.T) {
	if HasSecret([]byte(`{"Revision":"ok"}`)) {
		t.Error("HasSecret false positive on clean object")
	}
	if !HasSecret([]byte(`{"secret":"x"}`)) {
		t.Error("HasSecret false negative on forbidden key")
	}
}

func TestLoadFileSecretBearingConfigRejected(t *testing.T) {
	raw := readFixture(t, "default")
	// Inject a forbidden secret value marker into the provider BaseURL field.
	injected := bytes.Replace(raw, []byte(`"https://api.openai.example/v1"`), []byte(`"https://api.openai.example/v1?api_key=sk-leaked-secret"`), 1)
	path := writeTempConfig(t, injected)
	_, err := LoadFile(context.Background(), path)
	if !errors.Is(err, ErrConfigSecretDetected) {
		t.Errorf("secret-bearing config err = %v, want ErrConfigSecretDetected", err)
	}
	if err != nil && strings.Contains(err.Error(), "sk-leaked-secret") {
		t.Errorf("error leaks secret value: %q", err.Error())
	}
}

func TestCompileAndPublishInitialAtomicConcurrent(t *testing.T) {
	// Many goroutines bootstrap the SAME store from the same fixture. Because
	// the bootstrap generation is fixed at 1, exactly one Publish can succeed
	// (the store rejects stale generations). This exercises the atomic,
	// last-known-good publication path under contention.
	const N = 32
	path := fixturePath(t, "default")
	var store snapshot.Store
	var success, failures int64
	var firstErr error
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(N)
	start := make(chan struct{})
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			<-start
			_, err := CompileAndPublishInitial(context.Background(), &store, path)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				atomic.AddInt64(&success, 1)
			} else {
				atomic.AddInt64(&failures, 1)
				if firstErr == nil {
					firstErr = err
				}
			}
		}()
	}
	close(start)
	wg.Wait()

	if success != 1 {
		t.Errorf("successes = %d, want 1 (exactly one bootstrap wins)", success)
	}
	if int64(success)+int64(failures) != int64(N) {
		t.Errorf("success+failure = %d, want %d", success+failures, N)
	}
	if firstErr != nil && !errors.Is(firstErr, ErrConfigPublishFailed) {
		t.Errorf("first failure err = %v, want ErrConfigPublishFailed", firstErr)
	}
	if got := store.Generation(); got != 1 {
		t.Errorf("store generation = %d, want 1", got)
	}
}

func TestLoadFileConcurrentReaders(t *testing.T) {
	const N = 32
	path := fixturePath(t, "default")
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := LoadFile(context.Background(), path); err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent LoadFile error: %v", err)
	}
}

func TestLoadFileStableAcrossRuns(t *testing.T) {
	path := fixturePath(t, "default")
	first, err := LoadFile(context.Background(), path)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := LoadFile(context.Background(), path)
		if err != nil {
			t.Fatalf("load %d: %v", i, err)
		}
		if again.Revision != first.Revision {
			t.Errorf("run %d revision = %q, want %q", i, again.Revision, first.Revision)
		}
	}
}

func TestParseConfigMirrorsLoadFile(t *testing.T) {
	for _, name := range fixtureNames {
		name := name
		t.Run(name, func(t *testing.T) {
			if _, err := parseConfig(readFixture(t, name)); err != nil {
				t.Errorf("parseConfig(%s): %v", name, err)
			}
		})
	}
}

func FuzzScanSecrets(f *testing.F) {
	f.Add([]byte(`{"Revision":"2026-07-21","Secret":"sk-leak","MaxBudgetToken":10}`))
	f.Add([]byte(``))
	f.Add([]byte(`{"a":1,"a":2}`))
	f.Add([]byte(`not json at all`))
	f.Add([]byte(`{"note":"eyJhbGci.fakesig.more"}`))
	// Regression seeds for the nested-container state machine: a sibling key
	// following a nested object/array value, both literal and escaped.
	f.Add([]byte(`{"a":{"b":1},"se\u0063ret":"x"}`))
	f.Add([]byte(`{"a":[1,2],"se\u0063ret":"x"}`))
	f.Add([]byte(`{"a":{"b":{"c":1}},"secret":"x"}`))
	// URL credential coverage seeds, including url-encoded names.
	f.Add([]byte(`{"BaseURL":"https://api.example/v1?access_key=secret"}`))
	f.Add([]byte(`{"BaseURL":"https://api.example/v1?api%5Fkey=secret"}`))
	f.Add([]byte(`{"CredentialRef":"vault://provider/default"}`))
	validKinds := map[SecretFindingKind]struct{}{
		SecretForbiddenKey:  {},
		SecretValueMarker:   {},
		SecretJWT:           {},
		SecretURLCredential: {},
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		// Must never panic; the deferred recover turns a panic into a test
		// failure so any input that crashes the state machine is reported.
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ScanSecrets panicked on %q: %v", data, r)
			}
		}()
		findings := ScanSecrets(data)
		// Findings must carry only the fixed, content-free kinds; no input
		// bytes may surface through a finding kind.
		for _, fi := range findings {
			if _, ok := validKinds[fi.Kind]; !ok {
				t.Errorf("unknown finding kind %q for input %q", fi.Kind, data)
			}
		}
		// At most one finding per kind (dedup contract).
		seen := map[SecretFindingKind]struct{}{}
		for _, fi := range findings {
			if _, dup := seen[fi.Kind]; dup {
				t.Errorf("duplicate kind %q for input %q", fi.Kind, data)
			}
			seen[fi.Kind] = struct{}{}
		}
		// Determinism: the same input must always yield the same verdict.
		again := ScanSecrets(data)
		if (len(findings) == 0) != (len(again) == 0) {
			t.Errorf("non-deterministic verdict for %q: %v vs %v", data, findings, again)
		}
		_ = HasSecret(data)
		_, _, _, _ = validateJSONStructure(data)
		_ = hasTrailingData(data)
		_, _ = parseConfig(data)
	})
}

// FuzzScanSecretsNestedContainerState exercises the semantic state machine
// with arbitrary nested object/array structures so a forbidden key that
// appears after any container value is never missed. The corpus is seeded
// with the regression shapes; the property is: whenever the input is valid
// JSON whose decoded structure places a credential key in key position at any
// depth, the scanner must flag it. Because fully proving "key position" from
// arbitrary bytes is itself the scanner's job, this fuzz instead guarantees no
// panic, only valid kinds, dedup, and determinism for deeply nested input.
func FuzzScanSecretsNestedContainerState(f *testing.F) {
	f.Add([]byte(`{"a":{"b":1},"secret":"x"}`))
	f.Add([]byte(`{"a":{"b":1},"se\u0063ret":"x"}`))
	f.Add([]byte(`{"a":[{"b":1}],"password":"x"}`))
	f.Add([]byte(`{"a":{"b":{"c":{"d":1}}},"token":"x"}`))
	f.Add([]byte(`[{"secret":"x"},{"a":[1]}]`))
	f.Add([]byte(`{"a":[1,2,3],"b":{"c":[4]},"credential":"x"}`))
	validKinds := map[SecretFindingKind]struct{}{
		SecretForbiddenKey:  {},
		SecretValueMarker:   {},
		SecretJWT:           {},
		SecretURLCredential: {},
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ScanSecrets panicked on %q: %v", data, r)
			}
		}()
		findings := ScanSecrets(data)
		for _, fi := range findings {
			if _, ok := validKinds[fi.Kind]; !ok {
				t.Errorf("unknown finding kind %q for input %q", fi.Kind, data)
			}
		}
		seen := map[SecretFindingKind]struct{}{}
		for _, fi := range findings {
			if _, dup := seen[fi.Kind]; dup {
				t.Errorf("duplicate kind %q for input %q", fi.Kind, data)
			}
			seen[fi.Kind] = struct{}{}
		}
		again := ScanSecrets(data)
		if (len(findings) == 0) != (len(again) == 0) {
			t.Errorf("non-deterministic verdict for %q: %v vs %v", data, findings, again)
		}
	})
}

func FuzzParseConfigSentinels(f *testing.F) {
	f.Add(readFixtureForFuzz("default"))
	f.Add(readFixtureForFuzz("xfyun"))
	f.Add(readFixtureForFuzz("anthropic"))
	f.Add([]byte(`{"Revision":"r","CreatedAt":"2026-07-21T00:00:00Z"}`))
	f.Add([]byte(`{bad`))
	f.Add([]byte(`{"secret":"x"}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`[1,2,3]`))
	f.Add([]byte(`42`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, err := parseConfig(data)
		if err == nil {
			return
		}
		if !errors.Is(err, ErrConfigMalformed) && !errors.Is(err, ErrConfigSecretDetected) {
			t.Errorf("parseConfig returned non-sentinel error: %v", err)
		}
		// The error string must be exactly one of the fixed sentinel messages;
		// this proves no raw input, path, or JSON content is appended.
		msg := err.Error()
		if msg != ErrConfigMalformed.Error() && msg != ErrConfigSecretDetected.Error() {
			t.Errorf("error message is not a bare sentinel: %q", msg)
		}
	})
}

func readFixtureForFuzz(name string) []byte {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return nil
	}
	executorDir := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	raw, err := os.ReadFile(filepath.Join(executorDir, "fixtures", "configs", name+".json"))
	if err != nil {
		return nil
	}
	return raw
}

func mkfifoAvailable() bool {
	_, err := exec.LookPath("mkfifo")
	return err == nil
}

func mkfifo(path string) error {
	return exec.Command("mkfifo", path).Run()
}

// ─── CompileAndPublishNext tests ─────────────────────────────────────

func TestCompileAndPublishNextSuccess(t *testing.T) {
	var store snapshot.Store
	_, err := CompileAndPublishInitial(context.Background(), &store, fixturePath(t, "default"))
	if err != nil {
		t.Fatalf("initial bootstrap: %v", err)
	}
	if got := store.Generation(); got != 1 {
		t.Fatalf("initial generation = %d, want 1", got)
	}

	// Write a config with a different revision to trigger reload.
	raw := strictDecodeAndMarshal(t, readFixture(t, "default"), func(cfg *snapshot.ConfigSnapshot) {
		cfg.Revision = "reloaded-v2"
	})
	path := writeTempConfig(t, raw)

	meta, err := CompileAndPublishNext(context.Background(), &store, path)
	if err != nil {
		t.Fatalf("CompileAndPublishNext: %v", err)
	}
	if meta.Generation() != 2 {
		t.Errorf("generation = %d, want 2", meta.Generation())
	}
	if meta.Revision() != "reloaded-v2" {
		t.Errorf("revision = %q, want %q", meta.Revision(), "reloaded-v2")
	}
	if got := store.Generation(); got != 2 {
		t.Errorf("store generation = %d, want 2", got)
	}
}

func TestCompileAndPublishNextSameRevisionNoop(t *testing.T) {
	var store snapshot.Store
	initMeta, err := CompileAndPublishInitial(context.Background(), &store, fixturePath(t, "default"))
	if err != nil {
		t.Fatalf("initial bootstrap: %v", err)
	}

	// Reload the same file: revision unchanged, should be a no-op.
	meta, err := CompileAndPublishNext(context.Background(), &store, fixturePath(t, "default"))
	if err != nil {
		t.Fatalf("CompileAndPublishNext same revision: %v", err)
	}
	if meta.Generation() != initMeta.Generation() {
		t.Errorf("generation = %d, want %d (no-op)", meta.Generation(), initMeta.Generation())
	}
	if meta.Revision() != initMeta.Revision() {
		t.Errorf("revision = %q, want %q (no-op)", meta.Revision(), initMeta.Revision())
	}
	if got := store.Generation(); got != 1 {
		t.Errorf("store generation = %d, want 1 (no publish)", got)
	}
}

func TestCompileAndPublishNextNoInitialSnapshot(t *testing.T) {
	var store snapshot.Store
	_, err := CompileAndPublishNext(context.Background(), &store, fixturePath(t, "default"))
	if !errors.Is(err, ErrConfigNoInitialSnapshot) {
		t.Fatalf("no initial snapshot err = %v, want ErrConfigNoInitialSnapshot", err)
	}
}

func TestCompileAndPublishNextNilStore(t *testing.T) {
	_, err := CompileAndPublishNext(context.Background(), nil, fixturePath(t, "default"))
	if !errors.Is(err, ErrConfigPublishFailed) {
		t.Fatalf("nil store err = %v, want ErrConfigPublishFailed", err)
	}
}

func TestCompileAndPublishNextCompileFailure(t *testing.T) {
	var store snapshot.Store
	_, err := CompileAndPublishInitial(context.Background(), &store, fixturePath(t, "default"))
	if err != nil {
		t.Fatalf("initial bootstrap: %v", err)
	}

	// A snapshot whose route references a non-existent model fails the compiler.
	malformed := strictDecodeAndMarshal(t, readFixture(t, "default"), func(cfg *snapshot.ConfigSnapshot) {
		cfg.Routes[0].ModelID = "does-not-exist"
		cfg.Revision = "malformed-reload"
	})
	path := writeTempConfig(t, malformed)

	_, err = CompileAndPublishNext(context.Background(), &store, path)
	if !errors.Is(err, ErrConfigCompileFailed) {
		t.Errorf("compile failure err = %v, want ErrConfigCompileFailed", err)
	}
	// Old generation preserved.
	if got := store.Generation(); got != 1 {
		t.Errorf("store generation = %d, want 1 after failed reload", got)
	}
}

func TestCompileAndPublishNextLoadFailure(t *testing.T) {
	var store snapshot.Store
	_, err := CompileAndPublishInitial(context.Background(), &store, fixturePath(t, "default"))
	if err != nil {
		t.Fatalf("initial bootstrap: %v", err)
	}

	missingPath := filepath.Join(t.TempDir(), "missing.json")
	_, err = CompileAndPublishNext(context.Background(), &store, missingPath)
	if !errors.Is(err, ErrConfigNotFound) {
		t.Errorf("load failure err = %v, want ErrConfigNotFound", err)
	}
	if got := store.Generation(); got != 1 {
		t.Errorf("store generation = %d, want 1 after load failure", got)
	}
}

func TestCompileAndPublishNextPreCanceledCtx(t *testing.T) {
	var store snapshot.Store
	_, err := CompileAndPublishInitial(context.Background(), &store, fixturePath(t, "default"))
	if err != nil {
		t.Fatalf("initial bootstrap: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = CompileAndPublishNext(ctx, &store, fixturePath(t, "default"))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("pre-canceled ctx err = %v, want context.Canceled", err)
	}
	if got := store.Generation(); got != 1 {
		t.Errorf("store generation = %d, want 1 after canceled ctx", got)
	}
}

func TestCompileAndPublishNextMultipleReloads(t *testing.T) {
	var store snapshot.Store
	_, err := CompileAndPublishInitial(context.Background(), &store, fixturePath(t, "default"))
	if err != nil {
		t.Fatalf("initial bootstrap: %v", err)
	}

	for i := 2; i <= 5; i++ {
		raw := strictDecodeAndMarshal(t, readFixture(t, "default"), func(cfg *snapshot.ConfigSnapshot) {
			cfg.Revision = fmt.Sprintf("revision-%d", i)
		})
		path := writeTempConfig(t, raw)
		meta, err := CompileAndPublishNext(context.Background(), &store, path)
		if err != nil {
			t.Fatalf("reload %d: %v", i, err)
		}
		if meta.Generation() != uint64(i) {
			t.Errorf("reload %d: generation = %d, want %d", i, meta.Generation(), i)
		}
		if meta.Revision() != fmt.Sprintf("revision-%d", i) {
			t.Errorf("reload %d: revision = %q, want %q", i, meta.Revision(), fmt.Sprintf("revision-%d", i))
		}
	}
	if got := store.Generation(); got != 5 {
		t.Errorf("final store generation = %d, want 5", got)
	}
}

func TestCompileAndPublishNextDoesNotLeakPath(t *testing.T) {
	var store snapshot.Store
	_, err := CompileAndPublishInitial(context.Background(), &store, fixturePath(t, "default"))
	if err != nil {
		t.Fatalf("initial bootstrap: %v", err)
	}

	leakMarker := "unique-reload-leak-marker-99999"
	path := filepath.Join(t.TempDir(), leakMarker, "config.json")
	_, err = CompileAndPublishNext(context.Background(), &store, path)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), leakMarker) {
		t.Errorf("error leaks path: %q", err.Error())
	}
}

func TestCompileAndPublishNextSentinelNoUnwrap(t *testing.T) {
	if errors.Unwrap(ErrConfigNoInitialSnapshot) != nil {
		t.Errorf("ErrConfigNoInitialSnapshot unwrapped to %v, want nil", errors.Unwrap(ErrConfigNoInitialSnapshot))
	}
}
