// Package identity defines the identity lookup port and its Mock/InMemory
// implementations for the Executor service.
//
// Security invariants:
//
//   - The raw API key is only ever accepted as a lookup input. It is never
//     stored in Identity, never returned, and never persisted verbatim by the
//     InMemory store; the store indexes identities by a salted SHA-256 digest
//     of the raw key.
//   - Identity is the minimal resolved caller value: Subject, KeyID, Role,
//     Status. KeyID is a public, non-secret label and is independent of the
//     raw key digest.
package identity

import (
	"context"
	"errors"
)

// ErrUnknownKey is returned when an API key is not recognized.
var ErrUnknownKey = errors.New("unknown API key")

// ErrKeyDisabled is returned when the resolved identity is not active.
var ErrKeyDisabled = errors.New("API key is disabled")

// Role is the caller role assigned to an identity.
type Role string

const (
	// RoleService is the default role for service callers.
	RoleService Role = "service"
	// RoleAdmin is an elevated role.
	RoleAdmin Role = "admin"
)

// Status is the lifecycle state of an identity.
type Status string

const (
	// StatusActive means the identity may authenticate.
	StatusActive Status = "active"
	// StatusDisabled means the identity may not authenticate.
	StatusDisabled Status = "disabled"
)

// Identity is a resolved caller identity. It deliberately never carries the
// raw API key.
type Identity struct {
	Subject string
	KeyID   string
	Role    Role
	Status  Status
}

// Port resolves a raw API key to an identity.
type Port interface {
	// LookupByKey returns the identity for the given raw API key.
	// Returns ErrUnknownKey if the key is not recognized and ErrKeyDisabled
	// if the resolved identity is not active.
	LookupByKey(ctx context.Context, apiKey string) (Identity, error)
}
