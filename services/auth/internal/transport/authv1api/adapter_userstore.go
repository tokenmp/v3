package authv1api

import (
	"context"

	"github.com/tokenmp/v3/services/auth/internal/auth"
)

// UserRepoAdapter adapts an auth.UserRepository into the UserStore interface
// required by bearerMiddleware. It loads a user and returns the fields the
// middleware needs (status, token_version, role). Any repository error is
// surfaced to the middleware, which maps every error to a uniform 401 so the
// cause never reaches the client.
type UserRepoAdapter struct {
	repo auth.UserRepository
}

// NewUserRepoAdapter builds an adapter over the given user repository.
func NewUserRepoAdapter(repo auth.UserRepository) *UserRepoAdapter {
	return &UserRepoAdapter{repo: repo}
}

// FindByID implements UserStore.
func (a *UserRepoAdapter) FindByID(ctx context.Context, id string) (string, int, string, error) {
	u, err := a.repo.FindByID(ctx, id)
	if err != nil {
		return "", 0, "", err
	}
	return string(u.Status), u.TokenVersion, string(u.Role), nil
}
