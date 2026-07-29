-- Store both caller-requested and execution-effective thinking settings.
-- thinking_effort remains a compatibility alias for the effective effort.
ALTER TABLE request_logs
    ADD COLUMN IF NOT EXISTS thinking_requested_effort text,
    ADD COLUMN IF NOT EXISTS thinking_effective_effort text,
    ADD COLUMN IF NOT EXISTS thinking_requested_budget int,
    ADD COLUMN IF NOT EXISTS thinking_effective_budget int;
