-- Revert: remove "processing" from final_status CHECK
ALTER TABLE request_logs
    DROP CONSTRAINT IF EXISTS request_logs_final_status_chk,
    ADD CONSTRAINT request_logs_final_status_chk CHECK (final_status IN (
        'success', 'client_error', 'upstream_error', 'timeout', 'transport_error'
    ));
