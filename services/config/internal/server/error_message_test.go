package server

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/tokenmp/v3/services/config/internal/repository"
)

func TestErrorMessage(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "nil",
			err:  nil,
			want: "",
		},
		{
			name: "foreign key model_id → 模型不存在",
			err: classifyWrap(t, &pgconn.PgError{
				Code:           "23503",
				Message:        `insert or update on table "route_mappings" violates foreign key constraint`,
				ConstraintName: "route_mappings_model_id_fkey",
			}, repository.ErrForeignKeyViolation),
			want: "模型不存在，请先在「模型配置」中创建该模型后再配置路由",
		},
		{
			name: "foreign key provider_id → Provider 不存在",
			err: classifyWrap(t, &pgconn.PgError{
				Code:           "23503",
				ConstraintName: "route_mappings_provider_id_fkey",
			}, repository.ErrForeignKeyViolation),
			want: "Provider 不存在，请先在 Provider 配置中创建",
		},
		{
			name: "foreign key credential_id → 账号不存在",
			err: classifyWrap(t, &pgconn.PgError{
				Code:           "23503",
				ConstraintName: "route_credentials_credential_id_fkey",
			}, repository.ErrForeignKeyViolation),
			want: "关联的上游账号不存在，请检查账号是否已删除",
		},
		{
			name: "foreign key unknown constraint → generic with name",
			err: classifyWrap(t, &pgconn.PgError{
				Code:           "23503",
				ConstraintName: "some_other_fkey",
			}, repository.ErrForeignKeyViolation),
			want: "引用的数据不存在（外键约束 some_other_fkey），请检查关联数据是否存在",
		},
		{
			name: "unique violation → 该记录已存在",
			err: classifyWrap(t, &pgconn.PgError{
				Code:           "23505",
				ConstraintName: "route_mappings_pkey",
			}, repository.ErrConflict),
			want: "该记录已存在（主键或唯一约束冲突），请勿重复创建",
		},
		{
			name: "non pg error → 通用写入失败",
			err:  errors.New("connection refused"),
			want: "数据写入失败，请稍后重试或联系管理员",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := errorMessage(tc.err)
			if got != tc.want {
				t.Fatalf("errorMessage() = %q, want %q", got, tc.want)
			}
		})
	}
}

// classifyWrap mirrors repository.classifyWriteErr's wrapping so the error
// chain carries both the sentinel and the original pgconn.PgError (needed for
// errorMessage's errors.As to recover the PgError). Defined here to avoid
// reaching into the unexported repository.classifyWriteErr.
func classifyWrap(t *testing.T, pg *pgconn.PgError, sentinel error) error {
	t.Helper()
	return wrapErr(sentinel, pg)
}

// wrapErr returns an error whose chain includes sentinel (via %w) and pg
// (via %w), matching repository.classifyWriteErr's fmt.Errorf("%w: %w", ...).
func wrapErr(sentinel, cause error) error {
	return &doubleWrap{sentinel: sentinel, cause: cause}
}

type doubleWrap struct {
	sentinel error
	cause    error
}

func (e *doubleWrap) Error() string { return e.sentinel.Error() + ": " + e.cause.Error() }
func (e *doubleWrap) Unwrap() []error { return []error{e.sentinel, e.cause} }
