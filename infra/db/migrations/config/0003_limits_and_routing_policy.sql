-- TokenMP V3 Config DB — provider/route/account limits and routing policy config
-- Nullable fields are configuration only. NULL means inherit/default/unlimited.

ALTER TABLE providers
    ADD COLUMN IF NOT EXISTS context_window int,
    ADD COLUMN IF NOT EXISTS max_output_tokens int,
    ADD COLUMN IF NOT EXISTS rpm int,
    ADD COLUMN IF NOT EXISTS tpm int;

ALTER TABLE route_mappings
    ADD COLUMN IF NOT EXISTS rpm int,
    ADD COLUMN IF NOT EXISTS tpm int;

ALTER TABLE upstream_credentials
    ADD COLUMN IF NOT EXISTS rpm int,
    ADD COLUMN IF NOT EXISTS tpm int;

ALTER TABLE route_credentials
    ADD COLUMN IF NOT EXISTS rpm int,
    ADD COLUMN IF NOT EXISTS tpm int;

ALTER TABLE providers
    DROP CONSTRAINT IF EXISTS providers_context_window_chk,
    DROP CONSTRAINT IF EXISTS providers_max_output_tokens_chk,
    DROP CONSTRAINT IF EXISTS providers_rpm_chk,
    DROP CONSTRAINT IF EXISTS providers_tpm_chk,
    ADD CONSTRAINT providers_context_window_chk CHECK (context_window IS NULL OR context_window > 0),
    ADD CONSTRAINT providers_max_output_tokens_chk CHECK (max_output_tokens IS NULL OR max_output_tokens > 0),
    ADD CONSTRAINT providers_rpm_chk CHECK (rpm IS NULL OR rpm > 0),
    ADD CONSTRAINT providers_tpm_chk CHECK (tpm IS NULL OR tpm > 0);

ALTER TABLE route_mappings
    DROP CONSTRAINT IF EXISTS routes_rpm_chk,
    DROP CONSTRAINT IF EXISTS routes_tpm_chk,
    ADD CONSTRAINT routes_rpm_chk CHECK (rpm IS NULL OR rpm > 0),
    ADD CONSTRAINT routes_tpm_chk CHECK (tpm IS NULL OR tpm > 0);

ALTER TABLE upstream_credentials
    DROP CONSTRAINT IF EXISTS creds_rpm_chk,
    DROP CONSTRAINT IF EXISTS creds_tpm_chk,
    ADD CONSTRAINT creds_rpm_chk CHECK (rpm IS NULL OR rpm > 0),
    ADD CONSTRAINT creds_tpm_chk CHECK (tpm IS NULL OR tpm > 0);

ALTER TABLE route_credentials
    DROP CONSTRAINT IF EXISTS route_credentials_rpm_chk,
    DROP CONSTRAINT IF EXISTS route_credentials_tpm_chk,
    ADD CONSTRAINT route_credentials_rpm_chk CHECK (rpm IS NULL OR rpm > 0),
    ADD CONSTRAINT route_credentials_tpm_chk CHECK (tpm IS NULL OR tpm > 0);

COMMENT ON COLUMN providers.context_window IS 'Provider 默认上下文窗口(token)，route_mappings.context_window 可覆盖；NULL=不设置';
COMMENT ON COLUMN providers.max_output_tokens IS 'Provider 默认最大输出 token，route_mappings.max_output_tokens 可覆盖；NULL=不设置';
COMMENT ON COLUMN providers.rpm IS 'Provider 默认 requests per minute，route/account 可覆盖；NULL=不限制';
COMMENT ON COLUMN providers.tpm IS 'Provider 默认 tokens per minute，route/account 可覆盖；NULL=不限制';
COMMENT ON COLUMN route_mappings.rpm IS 'Provider+model(route) RPM 覆盖；NULL=继承 provider/account 策略';
COMMENT ON COLUMN route_mappings.tpm IS 'Provider+model(route) TPM 覆盖；NULL=继承 provider/account 策略';
COMMENT ON COLUMN upstream_credentials.rpm IS '账号级 RPM 覆盖；NULL=继承 provider/route 策略';
COMMENT ON COLUMN upstream_credentials.tpm IS '账号级 TPM 覆盖；NULL=继承 provider/route 策略';
COMMENT ON COLUMN route_credentials.rpm IS '路由-账号绑定级 RPM 覆盖；NULL=继承账号/provider/route 策略';
COMMENT ON COLUMN route_credentials.tpm IS '路由-账号绑定级 TPM 覆盖；NULL=继承账号/provider/route 策略';
