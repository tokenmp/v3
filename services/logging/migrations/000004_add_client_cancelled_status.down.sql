-- Roll back the dedicated client_cancelled status by remapping existing rows
-- to the pre-existing client_error terminal before restoring the old CHECK.
UPDATE request_logs
SET final_status = 'client_error'
WHERE final_status = 'client_cancelled';

ALTER TABLE request_logs
    DROP CONSTRAINT IF EXISTS request_logs_final_status_chk,
    ADD CONSTRAINT request_logs_final_status_chk CHECK (final_status IN (
        'processing',
        'success', 'client_error', 'upstream_error', 'timeout', 'transport_error'
    ));
