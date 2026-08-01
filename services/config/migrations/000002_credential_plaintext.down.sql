-- Revert: 明文 api_key 列移除，credential_ref 恢复 NOT NULL
-- 用 DO block 守卫表/列存在，使 down 在干净库上幂等。

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'upstream_credentials'
          AND column_name = 'api_key'
    ) THEN
        ALTER TABLE upstream_credentials
            DROP COLUMN api_key;
    END IF;
END $$;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'upstream_credentials'
          AND column_name = 'credential_ref'
    ) THEN
        ALTER TABLE upstream_credentials
            ALTER COLUMN credential_ref SET NOT NULL;
    END IF;
END $$;

DO $$
BEGIN
    IF to_regclass('public.upstream_credentials') IS NOT NULL THEN
        COMMENT ON TABLE upstream_credentials IS '上游凭据元数据，不存明文，只存 vault:// ref + 展示 prefix/suffix';
        COMMENT ON COLUMN upstream_credentials.credential_ref IS 'vault://provider/key (V3 CredentialRef，Secret Store 引用)';
    END IF;
END $$;
