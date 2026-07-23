package configreload

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/configsource"
	"github.com/tokenmp/v3/services/executor/internal/snapshot"
)

// testLogger captures log messages for test assertions.
type testLogger struct {
	mu       sync.Mutex
	infoMsgs []string
	errMsgs  []string
}

func (l *testLogger) Infof(template string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.infoMsgs = append(l.infoMsgs, template)
}

func (l *testLogger) Errorf(template string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errMsgs = append(l.errMsgs, template)
}

func (l *testLogger) infos() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	cp := make([]string, len(l.infoMsgs))
	copy(cp, l.infoMsgs)
	return cp
}

func (l *testLogger) errs() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	cp := make([]string, len(l.errMsgs))
	copy(cp, l.errMsgs)
	return cp
}

// minimalEmptyConfig is a secret-free config that compiles to no business routes.
const minimalEmptyConfig = `{
  "Revision": "reload-test",
  "CreatedAt": "2026-07-22T00:00:00Z",
  "Models": {},
  "Providers": {},
  "Routes": [],
  "Adapters": {}
}`

func writeReloadConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func bootstrapStore(t *testing.T, path string) *snapshot.Store {
	t.Helper()
	var store snapshot.Store
	if _, err := configsource.CompileAndPublishInitial(context.Background(), &store, path); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return &store
}

func mutateRevision(t *testing.T, raw []byte, newRevision string) []byte {
	t.Helper()
	var cfg snapshot.ConfigSnapshot
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	cfg.Revision = newRevision
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return out
}

func TestReloadSuccess(t *testing.T) {
	path := writeReloadConfig(t, minimalEmptyConfig)
	store := bootstrapStore(t, path)
	logger := &testLogger{}
	_ = NewReloader(store, path, nil, logger)

	// Write a new config with a different revision.
	newConfig := string(mutateRevision(t, []byte(minimalEmptyConfig), "reload-v2"))
	newPath := writeReloadConfig(t, newConfig)

	r2 := NewReloader(store, newPath, nil, logger)
	if err := r2.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := store.Generation(); got != 2 {
		t.Errorf("generation = %d, want 2", got)
	}
	infos := logger.infos()
	if len(infos) == 0 {
		t.Error("expected info log on success")
	}
}

func TestReloadUnchangedRevision(t *testing.T) {
	path := writeReloadConfig(t, minimalEmptyConfig)
	store := bootstrapStore(t, path)
	logger := &testLogger{}
	r := NewReloader(store, path, nil, logger)

	err := r.Reload(context.Background())
	if !errors.Is(err, ErrReloadUnchanged) {
		t.Fatalf("Reload unchanged err = %v, want ErrReloadUnchanged", err)
	}
	if got := store.Generation(); got != 1 {
		t.Errorf("generation = %d, want 1 (no publish)", got)
	}
}

func TestReloadNoInitialSnapshot(t *testing.T) {
	var store snapshot.Store
	logger := &testLogger{}
	r := NewReloader(&store, "/nonexistent", nil, logger)

	err := r.Reload(context.Background())
	if !errors.Is(err, ErrReloadFailed) {
		t.Fatalf("Reload no initial err = %v, want ErrReloadFailed", err)
	}
}

func TestReloadLoadFailure(t *testing.T) {
	path := writeReloadConfig(t, minimalEmptyConfig)
	store := bootstrapStore(t, path)
	logger := &testLogger{}
	r := NewReloader(store, filepath.Join(t.TempDir(), "missing.json"), nil, logger)

	err := r.Reload(context.Background())
	if !errors.Is(err, ErrReloadFailed) {
		t.Fatalf("Reload load failure err = %v, want ErrReloadFailed", err)
	}
	if got := store.Generation(); got != 1 {
		t.Errorf("generation = %d, want 1 (old preserved)", got)
	}
}

func TestReloadCompileFailure(t *testing.T) {
	path := writeReloadConfig(t, minimalEmptyConfig)
	store := bootstrapStore(t, path)
	logger := &testLogger{}

	// A config with a route referencing a non-existent model fails compilation.
	malformed := `{
  "Revision": "malformed-reload",
  "CreatedAt": "2026-07-22T00:00:00Z",
  "Models": {},
  "Providers": {},
  "Routes": [{"ID":"r1","ModelID":"nonexistent","ProviderID":"p1","AdapterID":"a1","UpstreamModel":"m","Priority":1,"Enabled":true,"Protocol":"openai_chat","Retry":{},"Timeout":{},"Credentials":[]}],
  "Adapters": {}
}`
	badPath := writeReloadConfig(t, malformed)
	r := NewReloader(store, badPath, nil, logger)

	err := r.Reload(context.Background())
	if !errors.Is(err, ErrReloadFailed) {
		t.Fatalf("Reload compile failure err = %v, want ErrReloadFailed", err)
	}
	if got := store.Generation(); got != 1 {
		t.Errorf("generation = %d, want 1 (old preserved)", got)
	}
}

func TestReloadValidationRejects(t *testing.T) {
	path := writeReloadConfig(t, minimalEmptyConfig)
	store := bootstrapStore(t, path)
	logger := &testLogger{}

	// Validator that always rejects.
	validateErr := errors.New("validation rejected")
	validator := func(ctx context.Context, cs *snapshot.CompiledSnapshot) error {
		return validateErr
	}

	newConfig := string(mutateRevision(t, []byte(minimalEmptyConfig), "reload-v2"))
	newPath := writeReloadConfig(t, newConfig)
	r := NewReloader(store, newPath, validator, logger)

	err := r.Reload(context.Background())
	if !errors.Is(err, ErrReloadValidationFailed) {
		t.Fatalf("Reload validation err = %v, want ErrReloadValidationFailed", err)
	}
	if got := store.Generation(); got != 1 {
		t.Errorf("generation = %d, want 1 (validation rejected, old preserved)", got)
	}
}

func TestReloadValidationPasses(t *testing.T) {
	path := writeReloadConfig(t, minimalEmptyConfig)
	store := bootstrapStore(t, path)
	logger := &testLogger{}

	// Validator that always passes.
	validator := func(ctx context.Context, cs *snapshot.CompiledSnapshot) error {
		return nil
	}

	newConfig := string(mutateRevision(t, []byte(minimalEmptyConfig), "reload-v2"))
	newPath := writeReloadConfig(t, newConfig)
	r := NewReloader(store, newPath, validator, logger)

	if err := r.Reload(context.Background()); err != nil {
		t.Fatalf("Reload with passing validation: %v", err)
	}
	if got := store.Generation(); got != 2 {
		t.Errorf("generation = %d, want 2", got)
	}
}

func TestReloadNilValidator(t *testing.T) {
	path := writeReloadConfig(t, minimalEmptyConfig)
	store := bootstrapStore(t, path)
	logger := &testLogger{}

	newConfig := string(mutateRevision(t, []byte(minimalEmptyConfig), "reload-v2"))
	newPath := writeReloadConfig(t, newConfig)
	r := NewReloader(store, newPath, nil, logger)

	if err := r.Reload(context.Background()); err != nil {
		t.Fatalf("Reload nil validator: %v", err)
	}
	if got := store.Generation(); got != 2 {
		t.Errorf("generation = %d, want 2", got)
	}
}

func TestReloadNilLogger(t *testing.T) {
	path := writeReloadConfig(t, minimalEmptyConfig)
	store := bootstrapStore(t, path)

	newConfig := string(mutateRevision(t, []byte(minimalEmptyConfig), "reload-v2"))
	newPath := writeReloadConfig(t, newConfig)
	r := NewReloader(store, newPath, nil, nil)

	if err := r.Reload(context.Background()); err != nil {
		t.Fatalf("Reload nil logger: %v", err)
	}
	if got := store.Generation(); got != 2 {
		t.Errorf("generation = %d, want 2", got)
	}
}

func TestReloadConcurrent(t *testing.T) {
	path := writeReloadConfig(t, minimalEmptyConfig)
	store := bootstrapStore(t, path)
	logger := &testLogger{}

	// All goroutines reload from the same file with a new revision.
	newConfig := string(mutateRevision(t, []byte(minimalEmptyConfig), "concurrent-reload"))
	newPath := writeReloadConfig(t, newConfig)

	var wg sync.WaitGroup
	var successCount atomic.Int64
	var unchangedCount atomic.Int64
	var failedCount atomic.Int64

	for i := 0; i < 10; i++ {
		r := NewReloader(store, newPath, nil, logger)
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := r.Reload(context.Background())
			if err == nil {
				successCount.Add(1)
			} else if errors.Is(err, ErrReloadUnchanged) {
				unchangedCount.Add(1)
			} else {
				failedCount.Add(1)
			}
		}()
	}
	wg.Wait()

	// Exactly one goroutine should succeed (publish gen 2), the rest should
	// get ErrReloadUnchanged (same revision) or ErrReloadFailed (stale gen).
	if successCount.Load() != 1 {
		t.Errorf("success count = %d, want 1", successCount.Load())
	}
	total := successCount.Load() + unchangedCount.Load() + failedCount.Load()
	if total != 10 {
		t.Errorf("total outcomes = %d, want 10", total)
	}
	// Generation should be exactly 2.
	if got := store.Generation(); got != 2 {
		t.Errorf("generation = %d, want 2", got)
	}
}

func TestReloadDoesNotLeakPathInLogs(t *testing.T) {
	path := writeReloadConfig(t, minimalEmptyConfig)
	store := bootstrapStore(t, path)
	logger := &testLogger{}

	missingPath := filepath.Join(t.TempDir(), "unique-leak-marker-reload-99999", "config.json")
	r := NewReloader(store, missingPath, nil, logger)

	_ = r.Reload(context.Background())

	for _, msg := range logger.errs() {
		if containsAny(msg, "unique-leak-marker-reload-99999") {
			t.Errorf("error log leaks path: %q", msg)
		}
	}
}

func TestSentinelErrorsDoNotUnwrap(t *testing.T) {
	for _, s := range []error{ErrReloadFailed, ErrReloadUnchanged, ErrReloadValidationFailed} {
		if errors.Unwrap(s) != nil {
			t.Errorf("%v: Unwrap = %v, want nil", s, errors.Unwrap(s))
		}
	}
}

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && contains(s, sub) {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
