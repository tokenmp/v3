-- =============================================================================
-- TokenMP V3 — Billing DB 迁移 000004：持久化计费结算闭环
-- -----------------------------------------------------------------------------
-- 目标: 把 Reserve/Finalize/Release 从“机械落账”升级为可审计的持久化状态机：
--   * 主动 hold（active reserved）计入 coding/token 窗口，阻止并发穿透。
--   * 未知 usage 不再猜测，进入 pending_reconciliation；reconciler 事后补结算。
--   * Finalize/Release 幂等：同 payload 幂等，不同/对立终态返回稳定冲突。
--   * 所有 amount 非负有界；请求级唯一约束防止重复 hold/双扣。
--   * 不得删除账本；sweeper 只改 reservation 终态 + 补 pending/adjustment 行。
-- 兼容性: 纯增量，不修改历史 migration；对旧数据安全（新增列可空/有默认）。
-- 生成日期: 2026-07-31
-- 目标数据库: PostgreSQL 17
-- =============================================================================

-- ---------------------------------------------------------------------------
-- 1. quota_reservations：扩展结算状态机
-- ---------------------------------------------------------------------------
-- status 新增 'pending_reconciliation'：Executor/Edge 在 commit 后但 usage
-- unknown（或 Billing 暂不可用）时，把 reservation 置为该态，等待 reconciler
-- 凭持久化 usage evidence 补结算。原 'expired' 仍由 sweeper 对超时 reserved
-- 行回收。新列均可空/带默认，旧数据安全。
-- ---------------------------------------------------------------------------

-- 1.1 status CHECK 扩展（pending_reconciliation）
ALTER TABLE quota_reservations DROP CONSTRAINT IF EXISTS quota_reservations_status_chk;
ALTER TABLE quota_reservations ADD CONSTRAINT quota_reservations_status_chk CHECK (status IN (
    'reserved', 'finalized', 'released', 'expired', 'pending_reconciliation'
));

-- 1.2 usage_known：Finalize 时是否拿到 confirmed usage（未知=false→pending）
ALTER TABLE quota_reservations ADD COLUMN IF NOT EXISTS usage_known boolean NOT NULL DEFAULT false;

-- 1.3 settlement_status：细分终态来源，便于 reconciler/sweeper 区分
--     settled   = Finalize 以 confirmed usage 结算
--     released  = Release 释放（已有 status=released）
--     expired   = sweeper 超时回收
--     pending   = 等待补结算（status=pending_reconciliation）
--     held      = 仍持有（status=reserved）
ALTER TABLE quota_reservations ADD COLUMN IF NOT EXISTS settlement_status text;
ALTER TABLE quota_reservations DROP CONSTRAINT IF EXISTS quota_reservations_settlement_status_chk;
ALTER TABLE quota_reservations ADD CONSTRAINT quota_reservations_settlement_status_chk CHECK (
    settlement_status IS NULL OR settlement_status IN ('held','settled','released','expired','pending')
);

-- 1.4 reconciled_at：reconciler 补结算时间戳（审计）
ALTER TABLE quota_reservations ADD COLUMN IF NOT EXISTS reconciled_at timestamptz;

-- 1.5 idempotency_payload_hash：Finalize 幂等输入一致性校验用的 payload 摘要。
--     首次 Finalize 写入；重复 Finalize 比对，不同 payload → 稳定冲突。
ALTER TABLE quota_reservations ADD COLUMN IF NOT EXISTS idempotency_payload_hash text;

-- 1.6 amount 非负检查（reserve/final 均非负有界）
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

-- ---------------------------------------------------------------------------
-- 2. 请求级唯一约束：同一 request_id 在 active(reserved/pending) 期间最多一个 hold
--    防止 Edge 重试/双发导致双扣。终态行不参与唯一（部分索引）。
-- ---------------------------------------------------------------------------
CREATE UNIQUE INDEX IF NOT EXISTS quota_reservations_request_active_uidx
    ON quota_reservations (request_id)
    WHERE status IN ('reserved', 'pending_reconciliation');

-- ---------------------------------------------------------------------------
-- 3. sweeper / reconciler 查询索引
--    - expired_sweep_idx: 查找超时仍 reserved 的行（status + expires_at）。
--    - pending_reconcile_idx: 查找 pending_reconciliation 行（按 reserved_at）。
-- ---------------------------------------------------------------------------
CREATE INDEX IF NOT EXISTS quota_reservations_expired_sweep_idx
    ON quota_reservations (status, expires_at)
    WHERE status = 'reserved';
CREATE INDEX IF NOT EXISTS quota_reservations_pending_reconcile_idx
    ON quota_reservations (reserved_at)
    WHERE status = 'pending_reconciliation';

-- ---------------------------------------------------------------------------
-- 4. usage_ledger：新增 'reconcile' 与 'sweep' ledger_type，并补 amount 非负检查
--    - reconcile: reconciler 把 pending 行补结算为 settled 的 charge 行。
--    - sweep:     sweeper 把超时 reserved 行回收为 released 的 refund 行。
--    既有 'adjustment' 仍用于对账修正。账本永不删除。
-- ---------------------------------------------------------------------------
ALTER TABLE usage_ledger DROP CONSTRAINT IF EXISTS usage_ledger_ledger_type_chk;
ALTER TABLE usage_ledger ADD CONSTRAINT usage_ledger_ledger_type_chk CHECK (ledger_type IN (
    'reserve', 'charge', 'refund', 'recharge',
    'adjustment', 'plan_grant', 'plan_renew', 'reconcile', 'sweep'
));

-- token/request delta 方向约定不变（reserve/charge 负、refund/reconcile-charge 负、
-- adjustment 可正可负）。这里不限制 delta 符号，仅保证 NOT NULL（已有默认 0）。

COMMENT ON COLUMN quota_reservations.usage_known IS 'Finalize 时是否拿到 confirmed usage；false→pending_reconciliation';
COMMENT ON COLUMN quota_reservations.settlement_status IS 'held/settled/released/expired/pending';
COMMENT ON COLUMN quota_reservations.reconciled_at IS 'reconciler 补结算时间戳';
COMMENT ON COLUMN quota_reservations.idempotency_payload_hash IS 'Finalize payload 摘要，重复 Finalize 一致性校验';
