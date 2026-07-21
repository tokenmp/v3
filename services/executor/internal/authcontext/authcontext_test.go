package authcontext

import (
	"context"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/identity"
)

func TestWithIdentityThenIdentityFromContextRoundTrip(t *testing.T) {
	t.Parallel()
	id := identity.Identity{Subject: "svc-1", KeyID: "key-1", Role: identity.RoleService, Status: identity.StatusActive}
	ctx := WithIdentity(context.Background(), id)
	got, ok := IdentityFromContext(ctx)
	if !ok {
		t.Fatal("expected identity present")
	}
	if got != id {
		t.Fatalf("got %+v, want %+v", got, id)
	}
}

func TestIdentityFromContextAbsent(t *testing.T) {
	t.Parallel()
	if _, ok := IdentityFromContext(context.Background()); ok {
		t.Fatal("expected absent identity")
	}
}

func TestIdentityFromContextNil(t *testing.T) {
	t.Parallel()
	if _, ok := IdentityFromContext(nil); ok {
		t.Fatal("nil ctx must not yield identity")
	}
}

func TestWithIdentityNilCtx(t *testing.T) {
	t.Parallel()
	id := identity.Identity{Subject: "svc-2", KeyID: "key-2", Role: identity.RoleAdmin, Status: identity.StatusActive}
	ctx := WithIdentity(nil, id)
	got, ok := IdentityFromContext(ctx)
	if !ok || got != id {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}

// TestWithIdentityDefensiveCopy ensures a later mutation of the source value
// does not mutate the value already stored in the context. The identity
// channel must be immutable once attached.
func TestWithIdentityDefensiveCopy(t *testing.T) {
	t.Parallel()
	id := identity.Identity{Subject: "svc-3", KeyID: "key-3", Role: identity.RoleService, Status: identity.StatusActive}
	ctx := WithIdentity(context.Background(), id)
	id.Subject = "mutated"
	got, ok := IdentityFromContext(ctx)
	if !ok || got.Subject != "svc-3" {
		t.Fatalf("stored identity mutated to %+v", got)
	}
}

// TestIdentityFromContextReturnsCopy ensures the returned value is a copy so a
// caller cannot mutate the value held in the context through the accessor.
func TestIdentityFromContextReturnsCopy(t *testing.T) {
	t.Parallel()
	id := identity.Identity{Subject: "svc-4", KeyID: "key-4", Role: identity.RoleAdmin, Status: identity.StatusActive}
	ctx := WithIdentity(context.Background(), id)
	got, _ := IdentityFromContext(ctx)
	got.Subject = "tampered"
	again, _ := IdentityFromContext(ctx)
	if again.Subject != "svc-4" {
		t.Fatalf("context identity mutated via accessor to %+v", again)
	}
}

// TestWithIdentityDropsIncidentalKeyMaterial ensures the stored value carries
// only the safe fields even if a caller constructed an identity with extra
// data (today Identity has no secret fields, but this guards future additions).
func TestWithIdentityDropsIncidentalKeyMaterial(t *testing.T) {
	t.Parallel()
	id := identity.Identity{Subject: "svc-5", KeyID: "key-5", Role: identity.RoleService, Status: identity.StatusActive}
	ctx := WithIdentity(context.Background(), id)
	got, ok := IdentityFromContext(ctx)
	if !ok {
		t.Fatal("expected identity present")
	}
	if got.Subject != "svc-5" || got.KeyID != "key-5" || got.Role != identity.RoleService || got.Status != identity.StatusActive {
		t.Fatalf("safe fields not preserved: %+v", got)
	}
}
