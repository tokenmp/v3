package repository

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

func TestClassify_Nil(t *testing.T) {
	if err := classify(nil); err != nil {
		t.Errorf("classify(nil) = %v, want nil", err)
	}
}

func TestClassify_GormErrRecordNotFound(t *testing.T) {
	if err := classify(gorm.ErrRecordNotFound); !errors.Is(err, ErrNotFound) {
		t.Errorf("classify(ErrRecordNotFound) = %v, want ErrNotFound", err)
	}
}

func TestClassify_PgError_UniqueViolation_EmailConstraint(t *testing.T) {
	cases := []struct {
		name           string
		constraintName string
		want           error
	}{
		{"users_email_unique_idx", "users_email_unique_idx", ErrDuplicateEmail},
		{"users_email_key", "users_email_key", ErrDuplicateEmail},
		{"unknown unique", "some_other_idx", ErrConstraint},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pgErr := &pgconn.PgError{
				Code:           "23505",
				ConstraintName: c.constraintName,
			}
			got := classify(pgErr)
			if !errors.Is(got, c.want) {
				t.Errorf("classify(pgErr 23505 %q) = %v, want %v", c.constraintName, got, c.want)
			}
		})
	}
}

func TestClassify_PgError_UniqueViolation_RefreshTokenHash(t *testing.T) {
	pgErr := &pgconn.PgError{
		Code:           "23505",
		ConstraintName: "auth_sessions_refresh_token_hash_key",
	}
	got := classify(pgErr)
	if !errors.Is(got, ErrConstraint) {
		t.Errorf("classify(23505 refresh_token_hash_key) = %v, want ErrConstraint", got)
	}
}

func TestClassify_PgError_ForeignKeyViolation(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "23503"}
	got := classify(pgErr)
	if !errors.Is(got, ErrConstraint) {
		t.Errorf("classify(23503) = %v, want ErrConstraint", got)
	}
}

func TestClassify_PgError_CheckViolation(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "23514"}
	got := classify(pgErr)
	if !errors.Is(got, ErrConstraint) {
		t.Errorf("classify(23514) = %v, want ErrConstraint", got)
	}
}

func TestClassify_PgError_OtherCode(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "08006"} // connection_failure
	got := classify(pgErr)
	if !errors.Is(got, ErrInternal) {
		t.Errorf("classify(08006) = %v, want ErrInternal", got)
	}
}

func TestClassify_PgError_UniqueViolation_DefaultConstraint(t *testing.T) {
	// A 23505 with an unrecognized constraint name must map to ErrConstraint,
	// not ErrDuplicateEmail.
	pgErr := &pgconn.PgError{
		Code:           "23505",
		ConstraintName: "users_some_other_col_key",
	}
	got := classify(pgErr)
	if !errors.Is(got, ErrConstraint) {
		t.Errorf("classify(23505 unknown constraint) = %v, want ErrConstraint", got)
	}
	if errors.Is(got, ErrDuplicateEmail) {
		t.Error("unexpectedly matched ErrDuplicateEmail for non-email constraint")
	}
}

func TestClassify_GormErrDuplicatedKey(t *testing.T) {
	// GORM's ErrDuplicatedKey cannot distinguish which constraint was violated,
	// so it maps to the generic ErrConstraint.
	got := classify(gorm.ErrDuplicatedKey)
	if !errors.Is(got, ErrConstraint) {
		t.Errorf("classify(ErrDuplicatedKey) = %v, want ErrConstraint", got)
	}
}

func TestClassify_UnknownError(t *testing.T) {
	got := classify(errors.New("something unexpected"))
	if !errors.Is(got, ErrInternal) {
		t.Errorf("classify(unknown) = %v, want ErrInternal", got)
	}
}

func TestClassify_PgError_Wrapped(t *testing.T) {
	// errors.As must unwrap to find the pgconn.PgError inside a wrapped error.
	inner := &pgconn.PgError{
		Code:           "23505",
		ConstraintName: "users_email_unique_idx",
	}
	wrapped := wrappedErr{err: inner}
	got := classify(wrapped)
	if !errors.Is(got, ErrDuplicateEmail) {
		t.Errorf("classify(wrapped pgErr) = %v, want ErrDuplicateEmail", got)
	}
}

// wrappedErr simulates a one-level error wrapper so errors.As must unwrap.
type wrappedErr struct{ err error }

func (e wrappedErr) Error() string { return "wrapped: " + e.err.Error() }
func (e wrappedErr) Unwrap() error { return e.err }
