-- Add billing multiplier and charged tokens to request logs.
-- billing_multiplier: the model's billing multiplier at request time (e.g., 1.5).
-- charged_tokens: the actual quota-charged token count = ceil(total_tokens * billing_multiplier).
-- These allow users to see exactly how much was charged vs raw token usage.
ALTER TABLE request_logs
    ADD COLUMN IF NOT EXISTS billing_multiplier NUMERIC(10, 4) DEFAULT 1.0;

ALTER TABLE request_logs
    ADD COLUMN IF NOT EXISTS charged_tokens INT DEFAULT 0;

COMMENT ON COLUMN request_logs.billing_multiplier IS '请求时模型的计费倍率';
COMMENT ON COLUMN request_logs.charged_tokens IS '实际扣费 token 数 = ceil(total_tokens × billing_multiplier)';
