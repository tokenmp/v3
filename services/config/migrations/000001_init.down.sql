-- 000001_init.down.sql
-- Reverses 000001_init.up.sql in dependency order (dependents first).
-- Safe to run on an empty database.

DROP TABLE IF EXISTS price_multiplier_rules;
DROP TABLE IF EXISTS global_config;
DROP TABLE IF EXISTS config_audit_log;
DROP TABLE IF EXISTS config_revision_snapshots;
DROP TABLE IF EXISTS config_revisions;
DROP TABLE IF EXISTS route_group_members;
DROP TABLE IF EXISTS routing_policies;
DROP TABLE IF EXISTS route_groups;
DROP TABLE IF EXISTS route_credentials;
DROP TABLE IF EXISTS route_mappings;
DROP TABLE IF EXISTS adapters;
DROP TABLE IF EXISTS models;
DROP TABLE IF EXISTS upstream_credentials;
DROP TABLE IF EXISTS upstream_endpoints;
DROP TABLE IF EXISTS providers;

DROP FUNCTION IF EXISTS touch_updated_at();
