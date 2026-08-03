-- Reverse: remove billing multiplier from models.
ALTER TABLE models
    DROP CONSTRAINT IF EXISTS models_billing_multiplier_chk;

ALTER TABLE models
    DROP COLUMN IF EXISTS billing_multiplier;
