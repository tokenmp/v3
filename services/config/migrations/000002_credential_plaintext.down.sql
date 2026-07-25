-- Revert: 明文 api_key 列移除，credential_ref 恢复 NOT NULL

ALTER TABLE upstream_credentials
    DROP COLUMN IF EXISTS api_key;

ALTER TABLE upstream_credentials
    ALTER COLUMN credential_ref SET NOT NULL;

COMMENT ON TABLE upstream_credentials IS '上游凭据元数据，不存明文，只存 vault:// ref + 展示 prefix/suffix';
COMMENT ON COLUMN upstream_credentials.credential_ref IS 'vault://provider/key (V3 CredentialRef，Secret Store 引用)';
