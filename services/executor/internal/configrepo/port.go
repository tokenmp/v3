// Package configrepo defines the configuration repository port and its
// Mock/InMemory implementations for the Executor service.
package configrepo

import "context"

// Snapshot holds the current Executor configuration.
type Snapshot struct {
	HTTPAddr        string
	ShutdownTimeout string
}

// Port provides read access to the Executor configuration.
type Port interface {
	// Snapshot returns the current configuration snapshot.
	Snapshot(ctx context.Context) (Snapshot, error)
}
