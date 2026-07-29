-- Add "processing" to the final_status CHECK constraint so non-terminal
-- request log rows can be created at request receipt time and updated as
-- the request progresses through the lifecycle.
ALTER TABLE request_logs
    DROP CONSTRAINT IF EXISTS request_logs_final_status_chk,
    ADD CONSTRAINT request_logs_final_status_chk CHECK (final_status IN (
        'processing',
        'success', 'client_error', 'upstream_error', 'timeout', 'transport_error'
    ));
