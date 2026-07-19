-- 000001_create_users.down.sql
-- Reverse of 000001_create_users.up.sql.

DROP INDEX IF EXISTS users_email_unique_idx;
DROP TABLE IF EXISTS users;
-- pgcrypto is intentionally NOT dropped: it may be used by other migrations
-- (e.g. auth_sessions) and by future schemas in this database.
