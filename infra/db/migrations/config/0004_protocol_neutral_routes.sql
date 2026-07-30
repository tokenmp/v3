-- 000004: Protocol-neutral routes
-- Decouples protocol from route_mappings. A route = (model, provider, upstream_model);
-- protocol becomes a runtime dimension resolved from the provider's endpoints.
-- Also simplifies upstream_endpoints to (provider, protocol, path): auth fields are
-- dead (auth is derived from SDK kind), so they are dropped.
-- Data: route_mappings + route_credentials are TRUNCATED (dev is test data) to clear
-- the protocol-duplicated rows; operators reconfigure after migration.

-- 1. Drop protocol from route identity.
--    Make protocol nullable first (existing rows carry it until truncate); it is no
--    longer a routing dimension. The column is kept nullable so old snapshots that
--    still reference it do not break a partial rollback, but the executor stops using it.
ALTER TABLE route_mappings
    ALTER COLUMN protocol DROP NOT NULL;

-- 2. Enforce one active route per (model, provider, upstream_model).
CREATE UNIQUE INDEX IF NOT EXISTS routes_model_provider_upstream_uidx
    ON route_mappings (model_id, provider_id, upstream_model)
    WHERE status = 'active';

-- 3. route_mappings.adapter_id is no longer set per route; adapter is derived at
--    runtime from (provider, target protocol). The column stays (nullable) for
--    backward compatibility but is no longer populated by the admin API.
COMMENT ON COLUMN route_mappings.protocol IS 'DEPRECATED: protocol-neutral since 000004. Kept nullable for backward compat; routing resolves protocol from provider endpoints.';
COMMENT ON COLUMN route_mappings.adapter_id IS 'DEPRECATED since 000004: adapter derived at runtime from (provider, target protocol). Kept nullable for backward compat.';

-- 4. Simplify upstream_endpoints to (provider, protocol, path).
--    Auth is derived from the provider SDK kind, not stored per endpoint.
ALTER TABLE upstream_endpoints
    DROP CONSTRAINT IF EXISTS endpoints_auth_kind_chk,
    DROP COLUMN IF EXISTS auth_kind,
    DROP COLUMN IF EXISTS auth_header,
    DROP COLUMN IF EXISTS auth_query,
    DROP COLUMN IF EXISTS auth_prefix;

COMMENT ON TABLE upstream_endpoints IS '上游端点（provider 子表）：(provider, protocol, path)。auth 由 provider SDK kind 派生，不存 endpoint。';
COMMENT ON COLUMN upstream_endpoints.protocol IS '该端点支持的外部协议（openai_chat/anthropic_messages/openai_responses/openai_images）';
COMMENT ON COLUMN upstream_endpoints.path IS '该协议的上游请求路径前缀（如 /v1/chat/completions）';

-- 5. Clear protocol-duplicated route data (dev test data).
TRUNCATE TABLE route_credentials;
TRUNCATE TABLE route_mappings;
