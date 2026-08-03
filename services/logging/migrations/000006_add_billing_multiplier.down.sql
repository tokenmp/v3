ALTER TABLE request_logs
    DROP COLUMN IF EXISTS charged_tokens;

ALTER TABLE request_logs
    DROP COLUMN IF EXISTS billing_multiplier;
