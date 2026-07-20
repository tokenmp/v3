// Package requestlog defines the request logging port and its Mock/InMemory
// implementations for the Executor service.
package requestlog

import (
	"context"
	"time"
)

// CallEntry represents a logged request call.
type CallEntry struct {
	Method    string
	Path      string
	Timestamp time.Time
}

// Port records and retrieves request call logs.
type Port interface {
	// Record logs a single call entry.
	Record(ctx context.Context, entry CallEntry) error

	// Calls returns all recorded call entries in order.
	Calls(ctx context.Context) []CallEntry
}
