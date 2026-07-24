-- 000001_create_announcements.up.sql
-- TokenMP v3 notice service: announcements table.
--
-- Schema is the source of truth; GORM models are application-layer only.
-- AutoMigrate is forbidden; schema changes come through versioned SQL
-- migrations applied by golang-migrate.

CREATE EXTENSION IF NOT EXISTS pgcrypto; -- gen_random_uuid()

CREATE TABLE announcements (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    title         VARCHAR(200) NOT NULL CHECK (title <> ''),
    summary       VARCHAR(500) NOT NULL CHECK (summary <> ''),
    body          TEXT         NOT NULL CHECK (body <> ''),
    severity      VARCHAR(16)  NOT NULL DEFAULT 'info'
                  CHECK (severity IN ('info','warning','maintenance')),
    published_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX announcements_published_at_idx
    ON announcements (published_at DESC);

COMMENT ON TABLE announcements IS 'TokenMP v3 notice service: project-scoped announcements.';
