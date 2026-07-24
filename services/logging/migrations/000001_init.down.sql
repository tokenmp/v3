-- 000001_init.down.sql
-- Reverses 000001_init.up.sql in dependency order (dependents first).
-- Safe to run on an empty database.

-- Partition child tables must be dropped before their parents.
DROP TABLE IF EXISTS request_log_events_2026_07_24;
DROP TABLE IF EXISTS request_log_events_2026_07_25;
DROP TABLE IF EXISTS request_log_events_default;
DROP TABLE IF EXISTS request_log_events;

DROP TABLE IF EXISTS request_attempts_2026_07_24;
DROP TABLE IF EXISTS request_attempts_2026_07_25;
DROP TABLE IF EXISTS request_attempts_default;
DROP TABLE IF EXISTS request_attempts;

DROP TABLE IF EXISTS request_logs_2026_07_24;
DROP TABLE IF EXISTS request_logs_2026_07_25;
DROP TABLE IF EXISTS request_logs_default;
DROP TABLE IF EXISTS request_logs;

DROP TABLE IF EXISTS log_archive_runs;
