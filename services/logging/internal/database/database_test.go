package database

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestOpen_EmptyURLReturnsSentinel(t *testing.T) {
	_, err := Open(context.Background(), Config{})
	if !errors.Is(err, ErrDatabaseURLRequired) {
		t.Fatalf("expected ErrDatabaseURLRequired, got %v", err)
	}
}

// TestClassifiedErrorDoesNotLeakCause ensures the public error surface never
// renders the underlying driver error text (which may contain the DSN).
func TestClassifiedErrorDoesNotLeakCause(t *testing.T) {
	const dsnFragment = "password=hunter2 host=db.internal.example"
	driverErr := errors.New("pq: " + dsnFragment)
	ce := &classifiedError{sentinel: ErrPingFailed, driver: driverErr}

	if ce.Error() != ErrPingFailed.Error() {
		t.Errorf("Error() = %q, want %q", ce.Error(), ErrPingFailed.Error())
	}
	if !errors.Is(ce, ErrPingFailed) {
		t.Error("errors.Is must match the sentinel")
	}
	if ce.cause() == nil || ce.cause().Error() != driverErr.Error() {
		t.Errorf("cause() must expose the driver error for in-package diagnostics")
	}
	if strings.Contains(ce.Error(), dsnFragment) ||
		strings.Contains(ce.Error(), "password") ||
		strings.Contains(ce.Error(), "db.internal") {
		t.Errorf("public Error() leaked driver details: %s", ce.Error())
	}
}

func TestClose_NilIsNoop(t *testing.T) {
	if err := Close(nil); err != nil {
		t.Fatalf("Close(nil) must be a no-op, got %v", err)
	}
}
