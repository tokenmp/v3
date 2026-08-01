BEGIN;

-- =============================================================================
-- TokenMP V3 — Billing DB 迁移 000004：持久化计费结算闭环
-- 镜像 services/billing/migrations/000004_settlement_state_machine.up.sql
--
-- Downgrade policy (见 services/billing/migrations/...down.sql)：fail-closed。
-- 不得在存在任何 000004-only 数据时回退（pending_reconciliation 行、
-- usage_ledger ledger_type=reconcile/sweep 行、或 reservation 的
-- settlement_status/usage_known=true/reconciled_at/idempotency_payload_hash
-- 非默认值），否则会孤儿化在途 hold / 违反账本永不删除 / 丢失已结算证据，
-- 导致稳定少收费。down 脚本先 RAISE EXCEPTION（仅计数、无 secret），
-- 要求操作者先经 reconciler/runbook 清理/转换这些数据后再回退。
-- 该 preflight 在任何 DDL 前执行，golang-migrate 逐文件事务包裹，失败回滚干净。
-- =============================================================================

-- 1. quota_reservations：扩展结算状态机
ALTER TABLE quota_reservations DROP CONSTRAINT IF EXISTS quota_reservations_status_chk;
ALTER TABLE quota_reservations ADD CONSTRAINT quota_reservations_status_chk CHECK (status IN (
    'reserved', 'finalized', 'released', 'expired', 'pending_reconciliation'
));

ALTER TABLE quota_reservations ADD COLUMN IF NOT EXISTS usage_known boolean NOT NULL DEFAULT false;

ALTER TABLE quota_reservations ADD COLUMN IF NOT EXISTS settlement_status text;
ALTER TABLE quota_reservations DROP CONSTRAINT IF EXISTS quota_reservations_settlement_status_chk;
ALTER TABLE quota_reservations ADD CONSTRAINT quota_reservations_settlement_status_chk CHECK (
    settlement_status IS NULL OR settlement_status IN ('held','settled','released','expired','pending')
);

ALTER TABLE quota_reservations ADD COLUMN IF NOT EXISTS reconciled_at timestamptz;
ALTER TABLE quota_reservations ADD COLUMN IF NOT EXISTS idempotency_payload_hash text;

ALTER TABLE quota_reservations DROP CONSTRAINT IF EXISTS quota_reservations_reserved_reqs_chk;
ALTER TABLE quota_reservations ADD CONSTRAINT quota_reservations_reserved_reqs_chk CHECK (
    reserved_requests IS NULL OR reserved_requests >= 0
);
ALTER TABLE quota_reservations DROP CONSTRAINT IF EXISTS quota_reservations_reserved_tokens_chk;
ALTER TABLE quota_reservations ADD CONSTRAINT quota_reservations_reserved_tokens_chk CHECK (
    reserved_tokens IS NULL OR reserved_tokens >= 0
);
ALTER TABLE quota_reservations DROP CONSTRAINT IF EXISTS quota_reservations_final_reqs_chk;
ALTER TABLE quota_reservations ADD CONSTRAINT quota_reservations_final_reqs_chk CHECK (
    final_requests IS NULL OR final_requests >= 0
);
ALTER TABLE quota_reservations DROP CONSTRAINT IF EXISTS quota_reservations_final_tokens_chk;
ALTER TABLE quota_reservations ADD CONSTRAINT quota_reservations_final_tokens_chk CHECK (
    final_tokens IS NULL OR final_tokens >= 0
);

-- 2. 请求级唯一约束：同一 request_id 在 active 期间最多一个 hold
CREATE UNIQUE INDEX IF NOT EXISTS quota_reservations_request_active_uidx
    ON quota_reservations (request_id)
    WHERE status IN ('reserved', 'pending_reconciliation');

-- 3. sweeper / reconciler 查询索引
CREATE INDEX IF NOT EXISTS quota_reservations_expired_sweep_idx
    ON quota_reservations (status, expires_at)
    WHERE status = 'reserved';
CREATE INDEX IF NOT EXISTS quota_reservations_pending_reconcile_idx
    ON quota_reservations (reserved_at)
    WHERE status = 'pending_reconciliation';

-- 4. usage_ledger：新增 'reconcile' 与 'sweep' ledger_type
ALTER TABLE usage_ledger DROP CONSTRAINT IF EXISTS usage_ledger_ledger_type_chk;
ALTER TABLE usage_ledger ADD CONSTRAINT usage_ledger_ledger_type_chk CHECK (ledger_type IN (
    'reserve', 'charge', 'refund', 'recharge',
    'adjustment', 'plan_grant', 'plan_renew', 'reconcile', 'sweep'
));

COMMENT ON COLUMN quota_reservations.usage_known IS 'Finalize 时是否拿到 confirmed usage；false→pending_reconciliation';
COMMENT ON COLUMN quota_reservations.settlement_status IS 'held/settled/released/expired/pending';
COMMENT ON COLUMN quota_reservations.reconciled_at IS 'reconciler 补结算时间戳';
COMMENT ON COLUMN quota_reservations.idempotency_payload_hash IS 'Finalize payload 摘要，重复 Finalize 一致性校验';

COMMIT;
