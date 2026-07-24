-- 000001_init.down.sql
-- Reverses 000001_init.up.sql in dependency order (dependents first).
-- Safe to run on an empty database.

DROP TABLE IF EXISTS usage_ledger;
DROP TABLE IF EXISTS quota_reservations;
DROP TABLE IF EXISTS user_plans;
DROP TABLE IF EXISTS plans;
DROP TABLE IF EXISTS users;

DROP FUNCTION IF EXISTS touch_updated_at();
