-- Revert provider/route/account rate/capacity configuration fields.
-- DO block guards make down idempotent on a clean schema (test harness runs
-- down before up). Never touches data.

DO $$
BEGIN
    IF to_regclass('public.route_credentials') IS NOT NULL THEN
        ALTER TABLE route_credentials
            DROP CONSTRAINT IF EXISTS route_credentials_rpm_chk,
            DROP CONSTRAINT IF EXISTS route_credentials_tpm_chk,
            DROP COLUMN IF EXISTS rpm,
            DROP COLUMN IF EXISTS tpm;
    END IF;
END $$;

DO $$
BEGIN
    IF to_regclass('public.upstream_credentials') IS NOT NULL THEN
        ALTER TABLE upstream_credentials
            DROP CONSTRAINT IF EXISTS creds_rpm_chk,
            DROP CONSTRAINT IF EXISTS creds_tpm_chk,
            DROP COLUMN IF EXISTS rpm,
            DROP COLUMN IF EXISTS tpm;
    END IF;
END $$;

DO $$
BEGIN
    IF to_regclass('public.route_mappings') IS NOT NULL THEN
        ALTER TABLE route_mappings
            DROP CONSTRAINT IF EXISTS routes_rpm_chk,
            DROP CONSTRAINT IF EXISTS routes_tpm_chk,
            DROP COLUMN IF EXISTS rpm,
            DROP COLUMN IF EXISTS tpm;
    END IF;
END $$;

DO $$
BEGIN
    IF to_regclass('public.providers') IS NOT NULL THEN
        ALTER TABLE providers
            DROP CONSTRAINT IF EXISTS providers_context_window_chk,
            DROP CONSTRAINT IF EXISTS providers_max_output_tokens_chk,
            DROP CONSTRAINT IF EXISTS providers_rpm_chk,
            DROP CONSTRAINT IF EXISTS providers_tpm_chk,
            DROP COLUMN IF EXISTS context_window,
            DROP COLUMN IF EXISTS max_output_tokens,
            DROP COLUMN IF EXISTS rpm,
            DROP COLUMN IF EXISTS tpm;
    END IF;
END $$;
