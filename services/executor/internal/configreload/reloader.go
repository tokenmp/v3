// Package configreload provides hot-reload of the Executor compiled configuration
// snapshot. It loads, compiles, validates, and publishes a new generation,
// preserving the old generation on any failure.
//
// All errors are stable sentinel values that never leak filesystem paths,
// configuration content, or secret material. Logging follows the same
// discipline: success messages carry only generation/revision/counts; failure
// messages carry only the sentinel text.
package configreload

import (
	"context"
	"errors"
	"strings"

	"github.com/tokenmp/v3/services/executor/internal/configsource"
	"github.com/tokenmp/v3/services/executor/internal/snapshot"
)

// Sentinel errors. Each is a non-wrapping errors.New value: errors.Unwrap on
// any of them returns nil, and no message embeds a path, OS error text, or
// raw JSON content.
var (
	// ErrReloadFailed is returned when a reload attempt fails for any reason
	// (load, compile, validate, or publish). The old generation is preserved.
	ErrReloadFailed = errors.New("config reload: failed")
	// ErrReloadUnchanged is returned when the loaded revision matches the
	// current snapshot revision and no publish occurs. This is not an error
	// condition; callers may treat it as a successful no-op.
	ErrReloadUnchanged = errors.New("config reload: revision unchanged")
	// ErrReloadValidationFailed is returned when the validation callback
	// rejects the newly compiled snapshot. The old generation is preserved.
	ErrReloadValidationFailed = errors.New("config reload: validation failed")
)

// CompiledValidator is a callback that validates a newly compiled snapshot
// before it is published. It is injected by the composition root and typically
// wraps rejectUnsupportedEnabledRoutes + credentialenv.ValidateCompiled.
// If it returns a non-nil error, the new snapshot is not published and the
// old generation is preserved.
type CompiledValidator func(context.Context, *snapshot.CompiledSnapshot) error

// Logger is the minimal logging interface required by the Reloader. It
// intentionally avoids structured logging or leveled logging to keep the
// dependency surface minimal. Implementations must not leak paths, content,
// or secrets in formatted messages.
type Logger interface {
	Infof(template string, args ...any)
	Errorf(template string, args ...any)
}

// Reloader performs hot-reload of the compiled configuration snapshot. It
// is safe to call Reload concurrently from multiple goroutines; the underlying
// Store provides atomic publication with generation ordering.
type Reloader struct {
	store    *snapshot.Store
	path     string
	validate CompiledValidator
	logger   Logger
}

// NewReloader creates a Reloader that will reload from path into store, with
// an optional validation callback. If validate is nil, no validation is
// performed (the snapshot is published directly after compilation).
func NewReloader(store *snapshot.Store, path string, validate CompiledValidator, logger Logger) *Reloader {
	return &Reloader{
		store:    store,
		path:     strings.TrimSpace(path),
		validate: validate,
		logger:   logger,
	}
}

// WithLogger returns a new Reloader with the same store, path, and validator
// but with the given logger. The original Reloader is not modified.
func (r *Reloader) WithLogger(logger Logger) *Reloader {
	if r == nil {
		return nil
	}
	return &Reloader{
		store:    r.store,
		path:     r.path,
		validate: r.validate,
		logger:   logger,
	}
}

// Reload loads the configuration file, compiles it, optionally validates the
// compiled snapshot, and publishes it as the next generation. On success it
// logs the generation transition and returns nil. On failure the old
// generation is preserved, a sentinel error is returned, and the failure is
// logged without leaking paths or content.
//
// Validation happens BEFORE publication: if the validator rejects the new
// snapshot, the store is never mutated and the old generation remains current.
//
// If the loaded revision matches the current snapshot's revision, Reload
// returns ErrReloadUnchanged and does not publish.
func (r *Reloader) Reload(ctx context.Context) error {
	current, err := r.store.Current()
	if err != nil {
		if r.logger != nil {
			r.logger.Errorf("config reload: %s", ErrReloadFailed)
		}
		return ErrReloadFailed
	}
	oldGen := current.Generation()
	oldRev := current.Revision()

	// Load the config file.
	cfg, err := configsource.LoadFile(ctx, r.path)
	if err != nil {
		if r.logger != nil {
			r.logger.Errorf("config reload: %s", ErrReloadFailed)
		}
		return ErrReloadFailed
	}
	cfg.Revision = strings.TrimSpace(cfg.Revision)

	// Skip publish if revision is unchanged.
	if cfg.Revision == oldRev {
		if r.logger != nil {
			r.logger.Infof("config reload: revision unchanged gen=%d rev=%q", oldGen, oldRev)
		}
		return ErrReloadUnchanged
	}

	// Honor cancellation between load and compile.
	if err := ctx.Err(); err != nil {
		return ErrReloadFailed
	}

	// Compile.
	compiled, err := snapshot.Compile(cfg)
	if err != nil {
		if r.logger != nil {
			r.logger.Errorf("config reload: %s", ErrReloadFailed)
		}
		return ErrReloadFailed
	}

	nextGeneration := oldGen + 1
	frozen, err := snapshot.NewCompiledSnapshotWithTime(cfg.Revision, &compiled, nextGeneration, cfg.CreatedAt)
	if err != nil {
		if r.logger != nil {
			r.logger.Errorf("config reload: %s", ErrReloadFailed)
		}
		return ErrReloadFailed
	}

	// Validate BEFORE publish.
	if r.validate != nil {
		if valErr := r.validate(ctx, frozen); valErr != nil {
			if r.logger != nil {
				r.logger.Errorf("config reload: %s", ErrReloadValidationFailed)
			}
			return ErrReloadValidationFailed
		}
	}

	// Honor cancellation between validate and publish.
	if err := ctx.Err(); err != nil {
		return ErrReloadFailed
	}

	// Publish.
	if err := r.store.Publish(frozen); err != nil {
		if r.logger != nil {
			r.logger.Errorf("config reload: %s", ErrReloadFailed)
		}
		return ErrReloadFailed
	}

	if r.logger != nil {
		view := frozen.Value()
		mc, pc, rc, ac := 0, 0, 0, 0
		if view != nil {
			mc = len(view.Models)
			pc = len(view.Providers)
			rc = len(view.Routes)
			ac = len(view.Adapters)
		}
		r.logger.Infof("config reload: success gen %d→%d rev=%q models=%d providers=%d routes=%d adapters=%d",
			oldGen, nextGeneration, frozen.Revision(), mc, pc, rc, ac)
	}
	return nil
}
