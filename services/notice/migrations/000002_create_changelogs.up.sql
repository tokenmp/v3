-- 000002_create_changelogs.up.sql
-- TokenMP v3 notice service: product changelogs.

CREATE EXTENSION IF NOT EXISTS pgcrypto; -- gen_random_uuid()

CREATE TABLE changelogs (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    version       VARCHAR(32)  NOT NULL CHECK (version <> ''),
    title         VARCHAR(200) NOT NULL CHECK (title <> ''),
    body          TEXT         NOT NULL CHECK (body <> ''),
    published_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX changelogs_version_unique_idx
    ON changelogs (version);

CREATE INDEX changelogs_published_at_idx
    ON changelogs (published_at DESC);

COMMENT ON TABLE changelogs IS 'TokenMP v3 notice service: product changelogs (version release notes).';
