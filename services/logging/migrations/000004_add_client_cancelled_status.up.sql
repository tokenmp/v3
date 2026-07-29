-- Add a terminal status for requests where the downstream client disconnected
-- before Edge/Executor could complete the proxied request.
ALTER TABLE request_logs
    DROP CONSTRAINT IF EXISTS request_logs_final_status_chk,
    ADD CONSTRAINT request_logs_final_status_chk CHECK (final_status IN (
        'processing',
        'success', 'client_error', 'client_cancelled', 'upstream_error', 'timeout', 'transport_error'
    ));
