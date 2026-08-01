package repository

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// requiredTestDatabaseName is the only database the repository integration
// tests are permitted to operate on. The destructive schema reset
// (resetSchema -> DROP SCHEMA public CASCADE) must never target any other
// database, so this name is enforced from the *parsed* DSN — never from a
// substring match on the raw string, which a host/user/query containing the
// same token can defeat.
const requiredTestDatabaseName = "tokenmp_billing"

// Sentinel errors returned by validateTestDSN. They are stable, non-wrapping
// values that never embed the raw DSN, the password, the host, or the query
// string. Each names only the expected database so it is safe to log.
var (
	errTestDSNParseFailed   = errors.New("BILLING_REPO_TEST_DSN is not a valid PostgreSQL connection string")
	errTestDSNNoDatabase    = errors.New("BILLING_REPO_TEST_DSN does not specify a database name; expected database " + requiredTestDatabaseName)
	errTestDSNWrongDatabase = errors.New("BILLING_REPO_TEST_DSN must target database " + requiredTestDatabaseName + "; a different database was parsed from the DSN")
)

// validateTestDSN is the security boundary for the destructive repository
// integration tests. It parses connString with the real libpq-compatible
// parser (pgconn.ParseConfig), which understands BOTH the URL form
// (postgres://user:pass@host:port/dbname?key=val) and the keyword=value form
// (host=... user=... dbname=...) and resolves the target database into a
// single, unambiguous Config.Database field — independent of host, user,
// password, or query parameters.
//
// This is strictly stronger than a substring match on the raw DSN:
// strings.Contains(raw, "tokenmp_billing") is bypassed by a DSN whose host,
// user, password, or query happens to contain that token while the real
// target database differs (e.g. postgres://tokenmp_billing:pass@h/db/other).
// Because the parser extracts the database from its dedicated field, such
// bypasses fail closed here.
//
// The function is pure (parse only, no network) so it can be unit-tested
// without a live PostgreSQL. It never echoes the raw DSN, password, host, or
// query in its errors; each error names only the expected database. The
// returned *pgconn.Config may be nil on error and must not be used.
func validateTestDSN(connString string) (*pgconn.Config, error) {
	if strings.TrimSpace(connString) == "" {
		return nil, errTestDSNNoDatabase
	}
	cfg, err := pgconn.ParseConfig(connString)
	if err != nil {
		return nil, errTestDSNParseFailed
	}
	if cfg.Database == "" {
		return nil, errTestDSNNoDatabase
	}
	if cfg.Database != requiredTestDatabaseName {
		return nil, errTestDSNWrongDatabase
	}
	return cfg, nil
}

// TestValidateTestDSN_AcceptsCorrectURLs covers the happy paths without a
// live database: the URL form (TCP host and Unix-socket host= form), and the
// keyword=value form. PG* environment variables are neutralized so the parse
// is deterministic regardless of the developer machine's shell environment.
func TestValidateTestDSN_AcceptsCorrectURLs(t *testing.T) {
	neutralizePGEnv(t)
	cases := map[string]string{
		"url tcp":                 "postgres://user:pass@localhost:5432/tokenmp_billing?sslmode=disable",
		"postgresql scheme":       "postgresql://u:p@db.example.com:5432/tokenmp_billing",
		"url unix socket host=":   "postgres://u@/tokenmp_billing?host=/var/run/postgresql",
		"keyword value":           "host=localhost port=5432 user=u dbname=tokenmp_billing",
		"keyword value unix sock": "host=/tmp user=u dbname=tokenmp_billing",
	}
	for name, dsn := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, err := validateTestDSN(dsn)
			if err != nil {
				t.Fatalf("expected accept, got error: %v", err)
			}
			if cfg.Database != requiredTestDatabaseName {
				t.Fatalf("parsed database = %q, want %q", cfg.Database, requiredTestDatabaseName)
			}
		})
	}
}

// TestValidateTestDSN_RejectsWhenTokenInWrongPlace is the core security
// regression: a raw DSN whose host, user, password, or query string contains
// the token "tokenmp_billing" while the real target database differs MUST be
// rejected. These would all slip past a naive strings.Contains(raw, ...) guard
// and let the destructive schema reset hit the wrong database.
func TestValidateTestDSN_RejectsWhenTokenInWrongPlace(t *testing.T) {
	neutralizePGEnv(t)
	cases := map[string]string{
		"token in hostname":           "postgres://user:secret@host.tokenmp_billing.example.com:5432/otherdb",
		"token in username":           "postgres://tokenmp_billing:pass@localhost:5432/otherdb",
		"token in password":           "postgres://user:tokenmp_billing@localhost:5432/otherdb",
		"token in query application":  "postgres://u:p@localhost:5432/otherdb?application_name=tokenmp_billing",
		"keyword host contains token": "host=tokenmp_billing.example.com user=u dbname=otherdb",
	}
	for name, dsn := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, err := validateTestDSN(dsn)
			if err == nil {
				t.Fatalf("expected rejection, but DSN was accepted with database %q", cfg.Database)
			}
			if !errors.Is(err, errTestDSNWrongDatabase) {
				t.Fatalf("expected errTestDSNWrongDatabase, got %v", err)
			}
			// The error must not leak the raw DSN, password, host, or query.
			if strings.Contains(err.Error(), "otherdb") {
				t.Fatalf("error leaks the actual database name: %v", err)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "pass") {
				t.Fatalf("error leaks a password fragment: %v", err)
			}
			if strings.Contains(err.Error(), "example.com") {
				t.Fatalf("error leaks the host: %v", err)
			}
		})
	}
}

// TestValidateTestDSN_RejectsEmptyAndMissingDatabase ensures a DSN with no
// database target (empty path, or no dbname keyword) is rejected rather than
// silently falling back to a default/user database.
func TestValidateTestDSN_RejectsEmptyAndMissingDatabase(t *testing.T) {
	neutralizePGEnv(t)
	cases := map[string]string{
		"url empty path":  "postgres://user@localhost:5432/",
		"url no path":     "postgres://user@localhost:5432",
		"keyword no db":   "host=localhost user=u",
		"blank string":    "   ",
		"different db":    "postgres://u:p@localhost:5432/postgres",
		"keyword diff db": "host=localhost user=u dbname=postgres",
	}
	for name, dsn := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := validateTestDSN(dsn); err == nil {
				t.Fatalf("expected rejection for %q, but was accepted", name)
			}
		})
	}
}

// TestValidateTestDSN_RejectsMalformed ensures a malformed or non-postgres
// connection string is rejected with the parse-failed sentinel, not accepted.
// pgconn.ParseConfig only accepts postgres/postgresql schemes, so a foreign
// scheme (mysql://) surfaces as a parse failure rather than slipping through.
func TestValidateTestDSN_RejectsMalformed(t *testing.T) {
	neutralizePGEnv(t)
	cases := map[string]string{
		"garbage":    "not a valid dsn at all",
		"bad scheme": "mysql://u:p@localhost/tokenmp_billing",
		"broken kw":  "host=localhost = dbname",
	}
	for name, dsn := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := validateTestDSN(dsn)
			if err == nil {
				t.Fatalf("expected rejection for %q, but was accepted", name)
			}
			if !errors.Is(err, errTestDSNParseFailed) {
				t.Fatalf("expected errTestDSNParseFailed, got %v", err)
			}
			// The parse-failed sentinel must not leak the raw DSN or password.
			if strings.Contains(err.Error(), dsn) || strings.Contains(err.Error(), "localhost") {
				t.Fatalf("error leaks the raw DSN or host: %v", err)
			}
		})
	}
}

// TestValidateTestDSN_ErrorsAreStable asserts every error is one of the three
// defined sentinels (no ad-hoc fmt.Errorf leaking the DSN) and never embeds
// the raw DSN, password, host, or query string.
func TestValidateTestDSN_ErrorsAreStable(t *testing.T) {
	neutralizePGEnv(t)
	sentinelDSNs := []string{
		"postgres://user:supersecret@somehost.example:9999/realdb?application_name=app&tokenmp_billing=1",
		"postgres://user@localhost:5432/",
		"totally bogus",
	}
	for _, raw := range sentinelDSNs {
		_, err := validateTestDSN(raw)
		if err == nil {
			continue // accept is fine for this loop; we focus on error leakage
		}
		switch {
		case errors.Is(err, errTestDSNParseFailed),
			errors.Is(err, errTestDSNNoDatabase),
			errors.Is(err, errTestDSNWrongDatabase):
			// ok: stable sentinel
		default:
			t.Fatalf("error is not one of the stable sentinels: %v", err)
		}
		// No sensitive fragment may appear in the message.
		for _, leak := range []string{"supersecret", "somehost.example", "realdb", "user:", "postgres://"} {
			if strings.Contains(err.Error(), leak) {
				t.Fatalf("error leaks %q: %v", leak, err)
			}
		}
	}
}

// neutralizePGEnv unsets the libpq PG* environment variables that
// pgconn.ParseConfig consults, so validateTestDSN behavior is deterministic
// across developer machines (e.g. a machine with PGDATABASE set must not
// flip an empty-path DSN into acceptance). It uses t.Setenv to a blank value
// rather than os.Unsetenv so the original value is restored after the test.
func neutralizePGEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"PGHOST", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD",
		"PGSERVICE", "PGSERVICEFILE", "PGSSLMODE", "PGPASSFILE",
	} {
		t.Setenv(key, "")
	}
}
