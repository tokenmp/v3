-- 000002: 上游凭据改为明文存储
-- credential_ref 保留但改为可选（自动生成），新增 api_key 明文列。
-- key_prefix/key_suffix 仍由后端自动从 api_key 推导填充。

ALTER TABLE upstream_credentials
    ALTER COLUMN credential_ref DROP NOT NULL;

ALTER TABLE upstream_credentials
    ADD COLUMN IF NOT EXISTS api_key text;

COMMENT ON TABLE upstream_credentials IS '上游凭据，明文存储 API key，自动派生 prefix/suffix';
COMMENT ON COLUMN upstream_credentials.api_key IS '明文 API key（如 sk-xxx）';
COMMENT ON COLUMN upstream_credentials.credential_ref IS '自动生成的 vault:// 引用（向后兼容 executor 编译器）';
