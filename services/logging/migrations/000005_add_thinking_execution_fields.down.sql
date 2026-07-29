ALTER TABLE request_logs
    DROP COLUMN IF EXISTS thinking_effective_budget,
    DROP COLUMN IF EXISTS thinking_requested_budget,
    DROP COLUMN IF EXISTS thinking_effective_effort,
    DROP COLUMN IF EXISTS thinking_requested_effort;
