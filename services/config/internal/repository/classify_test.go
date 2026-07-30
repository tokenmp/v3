package repository

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestClassifyWriteErr(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		want    error
		wantMsg string
	}{
		{
			name: "foreign key violation 23503",
			err: &pgconn.PgError{
				Code:           "23503",
				Message:        `insert or update on table "route_mappings" violates foreign key constraint`,
				TableName:      "route_mappings",
				ConstraintName: "route_mappings_model_id_fkey",
			},
			want:    ErrForeignKeyViolation,
			wantMsg: `insert or update on table "route_mappings" violates foreign key constraint`,
		},
		{
			name: "unique violation 23505",
			err: &pgconn.PgError{
				Code:           "23505",
				Message:        "duplicate key value violates unique constraint",
				TableName:      "route_mappings",
				ConstraintName: "route_mappings_model_provider_upstream_key",
			},
			want:    ErrConflict,
			wantMsg: "duplicate key value violates unique constraint",
		},
		{
			name: "other pg error falls back to insert failed",
			err: &pgconn.PgError{
				Code:    "23514",
				Message: "check constraint failed",
			},
			want:    ErrInsertFailed,
			wantMsg: "check constraint failed",
		},
		{
			name:    "non pg error falls back to insert failed",
			err:     errors.New("connection refused"),
			want:    ErrInsertFailed,
			wantMsg: "connection refused",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyWriteErr(tc.err)
			if !errors.Is(got, tc.want) {
				t.Fatalf("classifyWriteErr() sentinel = %v, want %v (got=%v)", got, tc.want, got)
			}
			if got != nil && !strings.Contains(got.Error(), tc.wantMsg) {
				t.Fatalf("classifyWriteErr() message = %q, want it to contain %q", got.Error(), tc.wantMsg)
			}
		})
	}
}

// Nil input must short-circuit to nil so callers can chain classifyWriteErr on
// an error returned from a no-op path without synthesizing a failure.
func TestClassifyWriteErrNil(t *testing.T) {
	if got := classifyWriteErr(nil); got != nil {
		t.Fatalf("classifyWriteErr(nil) = %v, want nil", got)
	}
}
