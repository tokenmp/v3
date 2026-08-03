-- Add billing multiplier to models for per-model quota charging.
-- Supports fractional multipliers (e.g., 0.5, 1.5, 3.0). Default 1.0 = 1:1.
-- When a request uses a model with multiplier M and consumes T tokens,
-- the actual quota charge is ceil(T * M).
ALTER TABLE models
    ADD COLUMN IF NOT EXISTS billing_multiplier NUMERIC(10, 4) NOT NULL DEFAULT 1.0;

-- Ensure multiplier is positive (0 or negative would break billing).
ALTER TABLE models
    ADD CONSTRAINT models_billing_multiplier_chk CHECK (billing_multiplier > 0);

COMMENT ON COLUMN models.billing_multiplier IS '计费倍率：实际扣费 = token 用量 × 倍率（向上取整）。默认 1.0，支持小数如 0.5/1.5/3.0';
