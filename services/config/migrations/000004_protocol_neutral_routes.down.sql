-- Revert protocol-neutral routes: restore NOT NULL protocol, re-add endpoint auth
-- fields, drop the route uniqueness index. Does NOT restore truncated route data.

ALTER TABLE route_mappings
    DROP INDEX IF EXISTS routes_model_provider_upstream_uidx;

DROP INDEX IF EXISTS routes_model_provider_upstream_uidx;

ALTER TABLE route_mappings
    ALTER COLUMN protocol SET NOT NULL;

ALTER TABLE upstream_endpoints
    ADD COLUMN IF NOT EXISTS auth_kind  text,
    ADD COLUMN IF NOT EXISTS auth_header text,
    ADD COLUMN IF NOT EXISTS auth_query  text,
    ADD COLUMN IF NOT EXISTS auth_prefix text;

ALTER TABLE upstream_endpoints
    DROP CONSTRAINT IF EXISTS endpoints_auth_kind_chk,
    ADD CONSTRAINT endpoints_auth_kind_chk CHECK (auth_kind IN ('bearer_header', 'api_key_header', 'api_key_query'));

COMMENT ON COLUMN route_mappings.protocol IS NULL;
COMMENT ON COLUMN route_mappings.adapter_id IS '可空：NULL=不走适配(原样转发)；非空=指定适配器';
COMMENT ON TABLE upstream_endpoints IS '上游端点（provider 子表），执行时按 protocol 选用，不进 route_mappings';
