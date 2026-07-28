package authv1api

import (
	"context"
	"errors"

	"github.com/tokenmp/v3/services/auth/internal/contract/authv1"
	"github.com/tokenmp/v3/services/auth/internal/database/models"
	"github.com/tokenmp/v3/services/auth/internal/security/apikey"
)

// ---------------------------------------------------------------------------
// KeyVerifier — API key verification port (service-to-service)
// ---------------------------------------------------------------------------

// Sentinel errors for key verification.
var (
	errKeyNotFound = errors.New("api key not found")
	errKeyDisabled = errors.New("api key disabled")
)

// KeyVerifier is the port for verifying an API key by its hash and returning
// the owning user identity. It is used by the Edge/BFF via verify-key.
type KeyVerifier interface {
	// VerifyByKey hashes the raw key, looks it up, and returns identity.
	// Returns ErrNotFound if the key is unknown, revoked, or disabled.
	VerifyByKey(ctx context.Context, hash []byte) (VerifiedKey, error)
	// TouchLastUsed updates the last_used_at timestamp. Best-effort.
	TouchLastUsed(ctx context.Context, keyID string) error
}

// VerifiedKey is the identity returned after successful API key verification.
type VerifiedKey struct {
	UserID string
	Email  string
	Role   string
	Status string
	KeyID  string
}

// KeyVerifierAdapter adapts *repository.APIKeyRepository + UserStore into
// KeyVerifier. It verifies the key hash, checks the user account is active,
// and returns the identity.
type KeyVerifierAdapter struct {
	repoKeys interface {
		FindByHash(ctx context.Context, hash []byte) (*models.APIKey, error)
		UpdateLastUsed(ctx context.Context, id string) error
	}
	users UserStore
}

// NewKeyVerifierAdapter returns a KeyVerifier backed by the repository.
func NewKeyVerifierAdapter(
	repoKeys interface {
		FindByHash(ctx context.Context, hash []byte) (*models.APIKey, error)
		UpdateLastUsed(ctx context.Context, id string) error
	},
	users UserStore,
) *KeyVerifierAdapter {
	return &KeyVerifierAdapter{repoKeys: repoKeys, users: users}
}

func (a *KeyVerifierAdapter) VerifyByKey(ctx context.Context, hash []byte) (VerifiedKey, error) {
	key, err := a.repoKeys.FindByHash(ctx, hash)
	if err != nil {
		return VerifiedKey{}, errKeyNotFound
	}
	// Check the user account is active.
	userStatus, _, userRole, err := a.users.FindByID(ctx, key.UserID)
	if err != nil {
		return VerifiedKey{}, errKeyNotFound
	}
	if userStatus != "active" {
		return VerifiedKey{}, errKeyDisabled
	}
	return VerifiedKey{
		UserID: key.UserID,
		Role:   string(userRole),
		Status: userStatus,
		KeyID:  key.ID,
	}, nil
}

func (a *KeyVerifierAdapter) TouchLastUsed(ctx context.Context, keyID string) error {
	return a.repoKeys.UpdateLastUsed(ctx, keyID)
}

// WithKeyVerifier injects the API key verification port.
func (a *StrictAdapter) WithKeyVerifier(v KeyVerifier) *StrictAdapter {
	a.keyVerifier = v
	return a
}

// AuthVerifyKey implements the POST /api/v1/auth/verify-key endpoint.
func (a *StrictAdapter) AuthVerifyKey(ctx context.Context, req authv1.AuthVerifyKeyRequestObject) (authv1.AuthVerifyKeyResponseObject, error) {
	if a.keyVerifier == nil {
		return authv1.AuthVerifyKey500JSONResponse{
			Body:    errResp(authv1.InternalError, "key verification not configured"),
			Headers: authv1.AuthVerifyKey500ResponseHeaders(errHeaders()),
		}, nil
	}
	if req.Body == nil || req.Body.ApiKey == "" {
		return authv1.AuthVerifyKey400JSONResponse{
			Body:    errResp(authv1.BadRequest, "api_key is required"),
			Headers: authv1.AuthVerifyKey400ResponseHeaders(errHeaders()),
		}, nil
	}

	hashes, err := apikey.HashCandidates(req.Body.ApiKey)
	if err != nil {
		// Malformed key — same response as invalid to avoid enumeration.
		return authv1.AuthVerifyKey401JSONResponse{
			Body:    errResp(authv1.Unauthorized, "invalid or expired key"),
			Headers: authv1.AuthVerifyKey401ResponseHeaders(errHeaders()),
		}, nil
	}

	var vk VerifiedKey
	var verifyErr error
	for _, hash := range hashes {
		vk, verifyErr = a.keyVerifier.VerifyByKey(ctx, hash)
		if verifyErr == nil {
			break
		}
		if !errors.Is(verifyErr, errKeyNotFound) {
			break
		}
	}
	if verifyErr != nil {
		if errors.Is(verifyErr, errKeyNotFound) || errors.Is(verifyErr, errKeyDisabled) {
			return authv1.AuthVerifyKey401JSONResponse{
				Body:    errResp(authv1.Unauthorized, "invalid or expired key"),
				Headers: authv1.AuthVerifyKey401ResponseHeaders(errHeaders()),
			}, nil
		}
		return authv1.AuthVerifyKey500JSONResponse{
			Body:    errResp(authv1.InternalError, "internal error"),
			Headers: authv1.AuthVerifyKey500ResponseHeaders(errHeaders()),
		}, nil
	}

	// Best-effort: update last_used_at.
	_ = a.keyVerifier.TouchLastUsed(ctx, vk.KeyID)

	role := authv1.VerifiedIdentityRoleUser
	if vk.Role == "admin" {
		role = authv1.VerifiedIdentityRoleAdmin
	}
	status := authv1.VerifiedIdentityStatusActive
	if vk.Status == "disabled" {
		status = authv1.VerifiedIdentityStatusDisabled
	}
	keyID := vk.KeyID
	email := ""
	return authv1.AuthVerifyKey200JSONResponse{
		Body: authv1.VerifiedIdentity{
			UserId: vk.UserID,
			Email:  &email,
			Role:   role,
			Status: status,
			KeyId:  &keyID,
		},
		Headers: authv1.AuthVerifyKey200ResponseHeaders(errHeaders()),
	}, nil
}
