-- 000003_create_api_keys.up.sql
-- TokenMP v3 auth service: unified API keys table.
--
-- api_keys replaces the overlapping legacy api_keys, user_api_keys, and
-- bot_keys tables. It is Auth-owned and identity-bound: a key belongs to one
-- user and may carry the user or admin role. Full key material is never
-- persisted; key_hash stores SHA-256(full API key) and display fields expose
-- only a short prefix and suffix.
--
-- Schema is the source of truth. GORM AutoMigrate is forbidden; changes must
-- be made through versioned golang-migrate SQL migrations.

CREATE TABLE api_keys (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            VARCHAR(128) NOT NULL CHECK (name <> ''),
    -- SHA-256 of the complete API key string. The plaintext key is returned
    -- only at creation time and is never stored in this database.
    key_hash        BYTEA        NOT NULL CHECK (length(key_hash) > 0),
    -- Display-only identifiers, e.g. "tmp_abc1...wxyz". They must never be
    -- used for authentication or lookup in place of key_hash.
    key_prefix      VARCHAR(16)  NOT NULL,
    key_suffix      VARCHAR(8)   NOT NULL,
    role            VARCHAR(16)  NOT NULL DEFAULT 'user'
                    CHECK (role IN ('user', 'admin')),
    status          VARCHAR(16)  NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'disabled', 'revoked')),
    expires_at      TIMESTAMPTZ  NULL,
    last_used_at    TIMESTAMPTZ  NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CHECK (expires_at IS NULL OR expires_at > created_at)
);

-- Hash uniqueness prevents a key from being assigned to more than one
-- identity. SHA-256 is a fixed 32-byte value in application use; the non-empty
-- CHECK above remains the database backstop for direct SQL writes.
CREATE UNIQUE INDEX api_keys_key_hash_unique_idx ON api_keys (key_hash);
CREATE INDEX api_keys_user_id_idx ON api_keys (user_id);

-- Earlier Auth migrations define updated_at columns but no automatic touch
-- trigger. Define it here with the first Auth table that requires updates to
-- move updated_at without relying on individual repository call sites.
CREATE FUNCTION touch_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER api_keys_touch_updated_at
    BEFORE UPDATE ON api_keys
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

COMMENT ON TABLE api_keys IS 'TokenMP v3 Auth API keys. Unifies legacy api_keys, user_api_keys, and bot_keys; stores only SHA-256 hashes, never plaintext keys.';
COMMENT ON COLUMN api_keys.user_id IS 'Auth user owning this API key. ON DELETE CASCADE removes keys with their identity.';
COMMENT ON COLUMN api_keys.name IS 'User-managed non-empty display name, at most 128 characters.';
COMMENT ON COLUMN api_keys.key_hash IS 'SHA-256 of the complete API key as BYTEA. Unique and non-empty; plaintext is never stored.';
COMMENT ON COLUMN api_keys.key_prefix IS 'First 12 characters of the complete API key; display/identification only.';
COMMENT ON COLUMN api_keys.key_suffix IS 'Last 4 characters of the complete API key; display only.';
COMMENT ON COLUMN api_keys.role IS 'Authorized identity role for this key: user or admin.';
COMMENT ON COLUMN api_keys.status IS 'Key lifecycle state: active authenticates; disabled and revoked do not.';
COMMENT ON COLUMN api_keys.expires_at IS 'Optional expiry, strictly after created_at when present.';
COMMENT ON COLUMN api_keys.last_used_at IS 'Best-effort timestamp of the most recent successful key use.';
COMMENT ON COLUMN api_keys.updated_at IS 'Updated automatically by api_keys_touch_updated_at trigger.';
