package repository

import (
	"errors"
	"testing"
)

func TestClassifyWriteErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{"nil", nil, nil},
		{"unique SQLSTATE", errors.New(`pq: duplicate key value violates unique constraint "providers_pkey" (SQLSTATE 23505)`), ErrConflict},
		{"unique constraint text", errors.New("ERROR: duplicate key value violates unique constraint (edge cases)"), ErrConflict},
		{"not null SQLSTATE", errors.New(`pq: null value in column "name" violates not-null constraint (SQLSTATE 23502)`), ErrInvalidInput},
		{"foreign key SQLSTATE", errors.New(`pq: insert or update on table "route_mappings" violates foreign key constraint (SQLSTATE 23503)`), ErrInvalidInput},
		{"check SQLSTATE", errors.New(`pq: new row for relation "adapters" violates check constraint "adapters_sdk_kind_chk" (SQLSTATE 23514)`), ErrInvalidInput},
		{"undefined column", errors.New(`pq: column "nonexistent_col" of relation "providers" does not exist (SQLSTATE 42703)`), ErrInvalidInput},
		{"unrelated error", errors.New("connection refused"), ErrInsertFailed},
		{"empty error", errors.New(""), ErrInsertFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyWriteErr(tc.err)
			if !errors.Is(got, tc.want) {
				t.Fatalf("classifyWriteErr(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
