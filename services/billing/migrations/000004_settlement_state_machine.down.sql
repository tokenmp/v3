-- 000004_settlement_state_machine.down.sql
-- Reverses 000004_settlement_state_machine.up.sql. Safe on a database that
-- never applied the up migration (IF EXISTS guards). Does not touch data.
--
-- Fail-closed downgrade: the preflight guard runs FIRST, before any
-- destructive change. It refuses to revert whenever ANY data exists that the
-- pre-000004 schema cannot express:
--   * pending_reconciliation reservations (in-flight holds the reconciler
--     still needs to resolve from evidence — reverting would orphan them and
--     could cause a stable under-charge),
--   * usage_ledger rows of ledger_type reconcile/sweep (000004-only types;
--     the restored CHECK would reject them and the ledger is never deleted),
--   * any non-default value in the 000004-only columns settlement_status,
--     usage_known=true, reconciled_at, idempotency_payload_hash (evidence a
--     settlement/reconcile actually happened — the old schema has no place to
--     record it and Billing never silently rewrites the ledger).
-- The guard RAISEs (only counts rows, no secret) and aborts, so a
-- non-transactional executor (raw psql / pgx Exec) never leaves the schema
-- half-reverted. golang-migrate also wraps each file in a transaction, so the
-- abort rolls back cleanly. After the guard passes, the rest is idempotent.

DO $$
DECLARE
    pending_count int;
    ledger_count int;
    settled_count int;
    tbl_exists boolean;
BEGIN
    -- Guards only apply when the tables exist (up migration applied). On a DB
    -- that never applied up, the tables are absent and we skip straight to
    -- the IF EXISTS-guarded DDL below.
    SELECT EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_schema = current_schema() AND table_name = 'quota_reservations'
    ) INTO tbl_exists;
    IF NOT tbl_exists THEN
        RETURN;
    END IF;
    SELECT count(*) INTO pending_count FROM quota_reservations
    WHERE status = 'pending_reconciliation';
    SELECT EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_schema = current_schema() AND table_name = 'usage_ledger'
    ) INTO tbl_exists;
    IF tbl_exists THEN
        SELECT count(*) INTO ledger_count FROM usage_ledger
        WHERE ledger_type IN ('reconcile', 'sweep');
    ELSE
        ledger_count := 0;
    END IF;
    SELECT count(*) INTO settled_count FROM quota_reservations
    WHERE settlement_status IS NOT NULL
       OR usage_known = true
       OR reconciled_at IS NOT NULL
       OR idempotency_payload_hash IS NOT NULL;
    IF pending_count > 0 OR ledger_count > 0 OR settled_count > 0 THEN
        RAISE EXCEPTION
            'cannot downgrade migration 000004: settlement data exists that the '
            'pre-000004 schema cannot express — pending_reconciliation=%, '
            'reconcile/sweep ledger rows=%, settled reservation rows=%; '
            'resolve them (reconcile/release/sweep via the Billing API or '
            'runbook) first',
            pending_count, ledger_count, settled_count
            USING ERRCODE = 'check_violation', HINT =
            'Run the settlement reconciler until no pending rows remain and no '
            'reconcile/sweep ledger rows exist, or explicitly '
            'finalize/release each pending reservation, then retry the downgrade.';
    END IF;
END $$;

DROP INDEX IF EXISTS quota_reservations_pending_reconcile_idx;
DROP INDEX IF EXISTS quota_reservations_expired_sweep_idx;
DROP INDEX IF EXISTS quota_reservations_request_active_uidx;

ALTER TABLE usage_ledger DROP CONSTRAINT IF EXISTS usage_ledger_ledger_type_chk;
ALTER TABLE usage_ledger ADD CONSTRAINT usage_ledger_ledger_type_chk CHECK (ledger_type IN (
    'reserve', 'charge', 'refund', 'recharge',
    'adjustment', 'plan_grant', 'plan_renew'
));

ALTER TABLE quota_reservations DROP CONSTRAINT IF EXISTS quota_reservations_final_tokens_chk;
ALTER TABLE quota_reservations DROP CONSTRAINT IF EXISTS quota_reservations_final_reqs_chk;
ALTER TABLE quota_reservations DROP CONSTRAINT IF EXISTS quota_reservations_reserved_tokens_chk;
ALTER TABLE quota_reservations DROP CONSTRAINT IF EXISTS quota_reservations_reserved_reqs_chk;

ALTER TABLE quota_reservations DROP COLUMN IF EXISTS idempotency_payload_hash;
ALTER TABLE quota_reservations DROP COLUMN IF EXISTS reconciled_at;
ALTER TABLE quota_reservations DROP CONSTRAINT IF EXISTS quota_reservations_settlement_status_chk;
ALTER TABLE quota_reservations DROP COLUMN IF EXISTS settlement_status;
ALTER TABLE quota_reservations DROP COLUMN IF EXISTS usage_known;

ALTER TABLE quota_reservations DROP CONSTRAINT IF EXISTS quota_reservations_status_chk;
ALTER TABLE quota_reservations ADD CONSTRAINT quota_reservations_status_chk CHECK (status IN (
    'reserved', 'finalized', 'released', 'expired'
));
