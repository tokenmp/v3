-- 000003_create_notifications.up.sql
-- TokenMP v3 notice service: per-user notifications.
--
-- user_id references the Auth Service users.id (UUID). The notice service
-- owns this table but does NOT own the users table; the user_id column is a
-- loose reference (no FK) because the users table lives in a separate
-- database (tokenmp_auth). Cross-database FKs are not enforced by Postgres.
--
-- action is a JSONB column holding a generic, data-driven affordance. It is
-- nullable: a NULL action means the notification is purely informational.
-- The application never hardcodes action targets; the client renders the
-- action purely from the stored fields (type/label/href).

CREATE EXTENSION IF NOT EXISTS pgcrypto; -- gen_random_uuid()

CREATE TABLE notifications (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID        NOT NULL,
    type        VARCHAR(64) NOT NULL CHECK (type <> ''),
    title       VARCHAR(200) NOT NULL CHECK (title <> ''),
    body        TEXT        NOT NULL DEFAULT '',
    action      JSONB       NULL,
    read_at     TIMESTAMPTZ NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX notifications_user_created_idx
    ON notifications (user_id, created_at DESC);

CREATE INDEX notifications_user_unread_idx
    ON notifications (user_id)
    WHERE read_at IS NULL;

COMMENT ON TABLE notifications IS 'TokenMP v3 notice service: per-user notifications with a generic data-driven action.';
COMMENT ON COLUMN notifications.user_id IS 'References Auth Service users.id (separate database tokenmp_auth); loose reference, no cross-database FK.';
COMMENT ON COLUMN notifications.action IS 'JSONB generic action affordance (type/label/href), NULL for informational-only.';
