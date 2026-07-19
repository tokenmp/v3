// Package database provides the PostgreSQL connection for the auth service
// and the readiness checker used by the /readyz endpoint.
//
// GORM is used as the ORM. AutoMigrate is intentionally forbidden: schema
// changes are managed by versioned SQL migrations in services/auth/migrations,
// applied out-of-band by golang-migrate.
//
// Errors returned by Open are stable, classified sentinel errors. They never
// wrap the underlying driver error, whose message may contain the DSN or
// connection string fragments. This keeps the public error surface safe to
// log: Error() reports only a fixed classification (open failed / acquire
// *sql.DB failed / initial ping failed). The underlying cause is retained in
// an unexported field for same-package diagnostics and is never rendered to
// logs.
package database

import (
	"context"
	"errors"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Config carries the runtime parameters for the database pool.
type Config struct {
	DatabaseURL     string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// Pinger is the minimal readiness contract the handler depends on. It is
// implemented by *gorm.DB (via PingDB) and by test doubles.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Stable classified errors returned by Open. They are sentinels — they do not
// wrap the underlying driver error so the DSN/connection string never reaches
// logs through Error(). Use errors.Is(err, ErrPingFailed) to branch on the
// failure class.
var (
	ErrDatabaseURLRequired = errors.New("database: DatabaseURL is required")
	ErrOpenFailed          = errors.New("database: open failed")
	ErrAcquireSQLDB        = errors.New("database: acquire *sql.DB failed")
	ErrPingFailed          = errors.New("database: initial ping failed")
)

// classifiedError carries a stable sentinel plus the underlying driver cause
// for in-process diagnostics. Error() returns only the safe sentinel string;
// Unwrap() returns the sentinel so errors.Is matches. The driver cause is
// intentionally not exposed via Error()/Unwrap() to guarantee it is never
// rendered to logs. It is accessible only to same-package code via cause().
type classifiedError struct {
	sentinel error
	driver   error
}

func (e *classifiedError) Error() string { return e.sentinel.Error() }
func (e *classifiedError) Unwrap() error { return e.sentinel }

// cause returns the underlying driver error for same-package diagnostics. It
// is deliberately unexported so cross-package callers (including main) cannot
// accidentally log the DSN-bearing error text.
func (e *classifiedError) cause() error { return e.driver }

// Open opens a GORM PostgreSQL connection configured from cfg and verifies
// connectivity with an initial ping. All failures are returned as stable
// classified errors; the driver's raw error is never exposed through the
// returned error's Error()/Unwrap() chain.
func Open(ctx context.Context, cfg Config) (*gorm.DB, error) {
	if cfg.DatabaseURL == "" {
		return nil, ErrDatabaseURLRequired
	}
	gormCfg := &gorm.Config{
		Logger:                 gormlogger.Default.LogMode(gormlogger.Silent),
		PrepareStmt:            true,
		SkipDefaultTransaction: true,
	}
	db, err := gorm.Open(postgres.Open(cfg.DatabaseURL), gormCfg)
	if err != nil {
		return nil, &classifiedError{sentinel: ErrOpenFailed, driver: err}
	}
	sqlDB, err := db.DB()
	if err != nil {
		// Best-effort cleanup of the partially-opened gorm handle. The cause
		// is classified and not the driver error's text.
		_ = closeQuiet(db)
		return nil, &classifiedError{sentinel: ErrAcquireSQLDB, driver: err}
	}
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, &classifiedError{sentinel: ErrPingFailed, driver: err}
	}
	return db, nil
}

// Close closes the underlying connection pool.
func Close(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// closeQuiet closes the pool without surfacing an error. Used on cleanup paths
// where the original failure is already being classified.
func closeQuiet(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// PingerFromDB adapts a *gorm.DB into a Pinger that pings the underlying pool.
func PingerFromDB(db *gorm.DB) Pinger {
	return &dbPinger{db: db}
}

type dbPinger struct {
	db *gorm.DB
}

func (p *dbPinger) Ping(ctx context.Context) error {
	if p.db == nil {
		return errors.New("database: nil db")
	}
	sqlDB, err := p.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}
