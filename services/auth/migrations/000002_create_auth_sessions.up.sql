-- 000002_create_auth_sessions.up.sql
-- TokenMP v3 auth service: auth_sessions table for refresh token rotation.
--
-- Foundation scope: schema only. No login/JWT/rotation business logic ships
-- in this PR. The columns exist so future login work can apply migrations
-- independently without breaking compatibility.
--
-- This is a new, Auth-owned schema: auth_sessions does not exist in
-- legacy production. Column types are chosen for this service's needs:
-- ip is INET (not VARCHAR) to leverage native Postgres inet semantics,
-- and user_agent is TEXT (not VARCHAR) for unlimited user-agent storage.

CREATE TABLE auth_sessions (
    id                      UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                 UUID          NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- token_family groups a chain of rotated refresh tokens. Defaults to a
    -- fresh UUID so a session created without an explicit family still has a
    -- stable family identifier for future rotation semantics.
    token_family_id         UUID          NOT NULL DEFAULT gen_random_uuid(),
    -- refresh_token_hash is BYTEA; non-empty enforced by CHECK so a blank
    -- hash (which would collide with a real "no value" sentinel) is rejected.
    refresh_token_hash      BYTEA         NOT NULL CHECK (length(refresh_token_hash) > 0),
    -- Self-referential FK preserved for future rotation semantics. The
    -- column name reads "this row was replaced BY session <id>": when a
    -- refresh token is rotated, the OLD session row is updated to set
    -- replaced_by_session_id to the NEW session's id, and the old row is
    -- revoked with revoke_reason='token_rotated'. The new row therefore
    -- never carries a replaced_by_session_id. ON DELETE SET NULL keeps
    -- history when a new row is pruned.
    replaced_by_session_id  UUID          NULL REFERENCES auth_sessions(id) ON DELETE SET NULL,
    expires_at              TIMESTAMPTZ   NOT NULL,
    revoked_at              TIMESTAMPTZ   NULL,
    revoke_reason           VARCHAR(64)   NULL,
    ip                      INET          NULL,
    user_agent              TEXT          NULL,
    created_at              TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ   NOT NULL DEFAULT now(),

    -- A refresh token must outlive the row that minted it; expires_at is
    -- always strictly after created_at. This is a table-level CHECK
    -- constraint inside CREATE TABLE — a bare `CHECK (...)` statement
    -- after CREATE TABLE is not valid PostgreSQL syntax.
    CHECK (expires_at > created_at),

    -- revoke_reason allow-list. NULL is permitted (session not revoked); any
    -- non-null value must be one of the documented lifecycle reasons.
    CONSTRAINT auth_sessions_revoke_reason_chk
        CHECK (
            revoke_reason IS NULL OR revoke_reason IN (
                'logout',
                'logout_all',
                'password_changed',
                'admin_revoked',
                'token_rotated',
                'token_reuse',
                'user_disabled'
            )
        ),

    -- Consistency: revoked_at and revoke_reason must be both NULL or both
    -- NOT NULL. A reason without a timestamp (or vice versa) is a corrupt
    -- rotation state and is rejected.
    CONSTRAINT auth_sessions_revoked_consistency_chk
        CHECK (
            (revoked_at IS NULL) = (revoke_reason IS NULL)
        )
);

-- A refresh token hash is globally unique; rotation compares hashes against
-- this index to detect reuse across families.
CREATE UNIQUE INDEX auth_sessions_refresh_token_hash_unique_idx
    ON auth_sessions (refresh_token_hash);

CREATE INDEX auth_sessions_user_id_idx
    ON auth_sessions (user_id);

CREATE INDEX auth_sessions_token_family_id_idx
    ON auth_sessions (token_family_id);

CREATE INDEX auth_sessions_expires_at_idx
    ON auth_sessions (expires_at);

COMMENT ON TABLE auth_sessions IS 'TokenMP v3 auth refresh sessions (Foundation: schema only).';
COMMENT ON COLUMN auth_sessions.refresh_token_hash IS 'BYTEA, non-empty (CHECK length > 0). Unique index for reuse detection.';
COMMENT ON COLUMN auth_sessions.token_family_id IS 'UUID, default gen_random_uuid(). Groups rotated refresh tokens for future rotation semantics.';
COMMENT ON COLUMN auth_sessions.replaced_by_session_id IS 'Self-FK: set on the OLD row to the NEW session id that replaced it; the old row is revoked with revoke_reason=''token_rotated''. New rows do not carry this value. ON DELETE SET NULL.';
COMMENT ON COLUMN auth_sessions.ip IS 'INET (native Postgres inet type; new Auth-owned schema column).';
COMMENT ON COLUMN auth_sessions.user_agent IS 'TEXT (unlimited; new Auth-owned schema column).';
COMMENT ON COLUMN auth_sessions.revoked_at IS 'TIMESTAMPTZ NULL; must be consistent with revoke_reason (both NULL or both set).';
