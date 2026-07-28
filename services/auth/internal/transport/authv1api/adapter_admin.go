// Package authv1api implements the generated strict-server interface for the
// Auth admin endpoints. All admin endpoints require role=admin, enforced by
// bearerMiddleware (adminOps set). The admin store ports are declared here
// to keep the transport layer decoupled from the concrete repository package.
package authv1api

import (
	"context"
	"errors"

	"github.com/google/uuid"

	authv1 "github.com/tokenmp/v3/services/auth/internal/contract/authv1"
	"github.com/tokenmp/v3/services/auth/internal/database/models"
	"github.com/tokenmp/v3/services/auth/internal/repository"
)

// AdminUserStore is the admin port for user management.
type AdminUserStore interface {
	// ListAll returns a paginated list of users optionally filtered by email,
	// status, and role.
	ListAll(ctx context.Context, search string, status string, role string, limit, offset int) ([]models.User, int, error)
	// FindByID loads a single user by primary key.
	FindByID(ctx context.Context, id string) (*models.User, error)
	// UpdateFields modifies role and/or status.
	UpdateFields(ctx context.Context, userID string, fields map[string]any) error
}

// AdminKeyStore is the admin port for cross-user API key listing.
type AdminKeyStore interface {
	// ListAll returns all non-revoked keys across all users, paginated.
	ListAll(ctx context.Context, limit, offset int) ([]models.APIKey, int, error)
}

// AdminUserRepoAdapter bridges *repository.UserRepository to AdminUserStore.
type AdminUserRepoAdapter struct {
	repo *repository.UserRepository
}

// NewAdminUserRepoAdapter constructs an AdminUserStore backed by GORM.
func NewAdminUserRepoAdapter(repo *repository.UserRepository) *AdminUserRepoAdapter {
	return &AdminUserRepoAdapter{repo: repo}
}

func (a *AdminUserRepoAdapter) ListAll(ctx context.Context, search string, status string, role string, limit, offset int) ([]models.User, int, error) {
	return a.repo.ListAll(ctx, search, status, role, limit, offset)
}

func (a *AdminUserRepoAdapter) FindByID(ctx context.Context, id string) (*models.User, error) {
	return a.repo.FindByID(ctx, id)
}

func (a *AdminUserRepoAdapter) UpdateFields(ctx context.Context, userID string, fields map[string]any) error {
	return a.repo.UpdateFields(ctx, userID, fields)
}

// AdminKeyRepoAdapter bridges *repository.APIKeyRepository to AdminKeyStore.
type AdminKeyRepoAdapter struct {
	repo *repository.APIKeyRepository
}

// NewAdminKeyRepoAdapter constructs an AdminKeyStore backed by GORM.
func NewAdminKeyRepoAdapter(repo *repository.APIKeyRepository) *AdminKeyRepoAdapter {
	return &AdminKeyRepoAdapter{repo: repo}
}

func (a *AdminKeyRepoAdapter) ListAll(ctx context.Context, limit, offset int) ([]models.APIKey, int, error) {
	return a.repo.ListAll(ctx, limit, offset)
}

// ---- Strict interface methods ----

func (a *StrictAdapter) AuthAdminListUsers(ctx context.Context, req authv1.AuthAdminListUsersRequestObject) (authv1.AuthAdminListUsersResponseObject, error) {
	if a.adminUsers == nil {
		return authv1.AuthAdminListUsers500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthAdminListUsers500ResponseHeaders(errHeaders())}, nil
	}
	page := 1
	if req.Params.Page != nil {
		page = *req.Params.Page
	}
	pageSize := 20
	if req.Params.PageSize != nil {
		pageSize = *req.Params.PageSize
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	search := ""
	if req.Params.Search != nil {
		search = *req.Params.Search
	}
	status := ""
	if req.Params.Status != nil {
		status = string(*req.Params.Status)
	}
	role := ""
	if req.Params.Role != nil {
		role = string(*req.Params.Role)
	}
	users, total, err := a.adminUsers.ListAll(ctx, search, status, role, pageSize, (page-1)*pageSize)
	if err != nil {
		return authv1.AuthAdminListUsers500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthAdminListUsers500ResponseHeaders(errHeaders())}, nil
	}
	out := make([]authv1.AdminUser, 0, len(users))
	for i := range users {
		out = append(out, userToAdmin(&users[i]))
	}
	return authv1.AuthAdminListUsers200JSONResponse{
		Body: struct {
			Page     int                `json:"page"`
			PageSize int                `json:"pageSize"`
			Total    int                `json:"total"`
			Users    []authv1.AdminUser `json:"users"`
		}{Page: page, PageSize: pageSize, Total: total, Users: out},
		Headers: authv1.AuthAdminListUsers200ResponseHeaders{CacheControl: cacheControl(), ContentType: contentTypeJSON()},
	}, nil
}

func (a *StrictAdapter) AuthAdminGetUser(ctx context.Context, req authv1.AuthAdminGetUserRequestObject) (authv1.AuthAdminGetUserResponseObject, error) {
	if a.adminUsers == nil {
		return authv1.AuthAdminGetUser500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthAdminGetUser500ResponseHeaders(errHeaders())}, nil
	}
	u, err := a.adminUsers.FindByID(ctx, req.UserId.String())
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return authv1.AuthAdminGetUser404JSONResponse{Body: errResp(authv1.NotFound, "user not found"), Headers: authv1.AuthAdminGetUser404ResponseHeaders(errHeaders())}, nil
		}
		return authv1.AuthAdminGetUser500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthAdminGetUser500ResponseHeaders(errHeaders())}, nil
	}
	return authv1.AuthAdminGetUser200JSONResponse{Body: userToAdmin(u), Headers: authv1.AuthAdminGetUser200ResponseHeaders{CacheControl: cacheControl(), ContentType: contentTypeJSON()}}, nil
}

func (a *StrictAdapter) AuthAdminUpdateUser(ctx context.Context, req authv1.AuthAdminUpdateUserRequestObject) (authv1.AuthAdminUpdateUserResponseObject, error) {
	if a.adminUsers == nil {
		return authv1.AuthAdminUpdateUser500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthAdminUpdateUser500ResponseHeaders(errHeaders())}, nil
	}
	if req.Body == nil || (req.Body.Role == nil && req.Body.Status == nil) {
		return authv1.AuthAdminUpdateUser400JSONResponse{Body: errResp(authv1.BadRequest, "at least one field is required"), Headers: authv1.AuthAdminUpdateUser400ResponseHeaders(errHeaders())}, nil
	}
	fields := make(map[string]any)
	if req.Body.Role != nil {
		fields["role"] = string(*req.Body.Role)
	}
	if req.Body.Status != nil {
		fields["status"] = string(*req.Body.Status)
	}
	if err := a.adminUsers.UpdateFields(ctx, req.UserId.String(), fields); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return authv1.AuthAdminUpdateUser404JSONResponse{Body: errResp(authv1.NotFound, "user not found"), Headers: authv1.AuthAdminUpdateUser404ResponseHeaders(errHeaders())}, nil
		}
		return authv1.AuthAdminUpdateUser500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthAdminUpdateUser500ResponseHeaders(errHeaders())}, nil
	}
	u, err := a.adminUsers.FindByID(ctx, req.UserId.String())
	if err != nil {
		return authv1.AuthAdminUpdateUser500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthAdminUpdateUser500ResponseHeaders(errHeaders())}, nil
	}
	return authv1.AuthAdminUpdateUser200JSONResponse{Body: userToAdmin(u), Headers: authv1.AuthAdminUpdateUser200ResponseHeaders{CacheControl: cacheControl(), ContentType: contentTypeJSON()}}, nil
}

func (a *StrictAdapter) AuthAdminListKeys(ctx context.Context, req authv1.AuthAdminListKeysRequestObject) (authv1.AuthAdminListKeysResponseObject, error) {
	if a.adminKeys == nil {
		return authv1.AuthAdminListKeys500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthAdminListKeys500ResponseHeaders(errHeaders())}, nil
	}
	page := 1
	if req.Params.Page != nil {
		page = *req.Params.Page
	}
	pageSize := 20
	if req.Params.PageSize != nil {
		pageSize = *req.Params.PageSize
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	keys, total, err := a.adminKeys.ListAll(ctx, pageSize, (page-1)*pageSize)
	if err != nil {
		return authv1.AuthAdminListKeys500JSONResponse{Body: errResp(authv1.InternalError, "internal error"), Headers: authv1.AuthAdminListKeys500ResponseHeaders(errHeaders())}, nil
	}
	out := make([]authv1.AdminApiKey, 0, len(keys))
	for i := range keys {
		out = append(out, apiKeyToAdmin(&keys[i]))
	}
	return authv1.AuthAdminListKeys200JSONResponse{
		Body: struct {
			Keys     []authv1.AdminApiKey `json:"keys"`
			Page     int                  `json:"page"`
			PageSize int                  `json:"pageSize"`
			Total    int                  `json:"total"`
		}{Keys: out, Page: page, PageSize: pageSize, Total: total},
		Headers: authv1.AuthAdminListKeys200ResponseHeaders{CacheControl: cacheControl(), ContentType: contentTypeJSON()},
	}, nil
}

// ---- mapping helpers ----

func userToAdmin(u *models.User) authv1.AdminUser {
	return authv1.AdminUser{
		Id:        uuid.MustParse(u.ID),
		Email:     u.Email,
		Role:      authv1.AdminUserRole(string(u.Role)),
		Status:    authv1.AdminUserStatus(string(u.Status)),
		CreatedAt: u.CreatedAt,
	}
}

func apiKeyToAdmin(k *models.APIKey) authv1.AdminApiKey {
	return authv1.AdminApiKey{
		Id:         uuid.MustParse(k.ID),
		UserId:     uuid.MustParse(k.UserID),
		Name:       k.Name,
		KeyPrefix:  k.KeyPrefix,
		KeySuffix:  k.KeySuffix,
		Status:     authv1.AdminApiKeyStatus(string(k.Status)),
		LastUsedAt: k.LastUsedAt,
		ExpiresAt:  k.ExpiresAt,
		CreatedAt:  k.CreatedAt,
	}
}
