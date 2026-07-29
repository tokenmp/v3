-- Store a bounded, sanitized User-Agent captured by the trusted Edge.
-- No other client request headers are persisted.
ALTER TABLE request_logs
    ADD COLUMN user_agent text;
