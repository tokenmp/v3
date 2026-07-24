-- 000003_create_api_keys.down.sql
-- Reverse of 000003_create_api_keys.up.sql.

DROP TABLE IF EXISTS api_keys;
DROP FUNCTION IF EXISTS touch_updated_at();
