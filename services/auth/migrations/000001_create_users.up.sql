-- 000001_create_users.up.sql
-- TokenMP v3 auth service: users table.
--
-- Schema is the source of truth; GORM models are application-layer only.
-- AutoMigrate is forbidden; schema changes must come through versioned SQL
-- migrations applied by golang-migrate.
--
-- Type compatibility: the production legacy users table is confirmed to use
-- UUID / VARCHAR / TEXT / TIMESTAMPTZ types. This migration preserves those
-- types and the stored values so it remains fully compatible with the
-- existing production table:
--   id            UUID        (gen_random_uuid())
--   email         VARCHAR(255)
--   password_hash TEXT        (bcrypt; Argon2id upgrade is future work)
--   role/status   VARCHAR(16)
--   token_version INTEGER
--   timestamps    TIMESTAMPTZ
-- password_hash is TEXT (not VARCHAR) to match the legacy column exactly and
-- to avoid any length cap on future hash formats.

CREATE EXTENSION IF NOT EXISTS pgcrypto; -- gen_random_uuid()

CREATE TABLE users (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    email           VARCHAR(255) NOT NULL,
    -- The stored email value MUST equal its canonical normalized form
    -- LOWER(BTRIM(email)). This invariant guarantees every written row is
    -- already lowercase and trimmed, so the expression unique index below
    -- behaves identically to a plain unique index while still being safe
    -- under future driver/collation changes. Non-empty is enforced too.
    password_hash   TEXT        NOT NULL CHECK (password_hash <> ''),
    role            VARCHAR(16)  NOT NULL DEFAULT 'user'
                    CHECK (role IN ('user','admin')),
    status          VARCHAR(16)  NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active','disabled')),
    token_version   INTEGER      NOT NULL DEFAULT 1
                    CHECK (token_version >= 1),
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    -- Email must be non-empty AND stored exactly as LOWER(BTRIM(email)).
    -- The application is responsible for normalizing before insert; this
    -- CHECK is the backstop so malformed inserts (mixed case / whitespace)
    -- are rejected at the database rather than silently accepted.
    CONSTRAINT users_email_normalized_chk
        CHECK (email <> '' AND email = LOWER(BTRIM(email)))
);

-- Unique case-insensitive, trimmed email. Because the CHECK above already
-- guarantees the stored value equals LOWER(BTRIM(email)), a plain unique index
-- on email would be functionally equivalent. The expression index is retained
-- by contract to remain robust under future relaxations of the CHECK and to
-- keep the canonical-form invariant explicit at the index level. Citext is
-- intentionally NOT used.
CREATE UNIQUE INDEX users_email_unique_idx
    ON users (LOWER(BTRIM(email)));

COMMENT ON TABLE users IS 'TokenMP v3 auth users (Foundation: no login/JWT yet).';
COMMENT ON COLUMN users.email IS 'VARCHAR(255). CHECK requires email <> '''' AND email = LOWER(BTRIM(email)); application must normalize before insert.';
COMMENT ON COLUMN users.password_hash IS 'TEXT (matches legacy production column). bcrypt hash; planned progressive upgrade to Argon2id (see ADR 0004). CHECK password_hash <> '''' .';
COMMENT ON COLUMN users.token_version IS 'Monotonic token version; default 1, must be >= 1. Increments invalidate active sessions.';
