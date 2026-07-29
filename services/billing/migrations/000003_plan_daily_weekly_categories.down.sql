ALTER TABLE plans DROP CONSTRAINT IF EXISTS plans_category_chk;
ALTER TABLE plans ADD CONSTRAINT plans_category_chk CHECK (category IN ('monthly', 'quarterly', 'yearly'));
