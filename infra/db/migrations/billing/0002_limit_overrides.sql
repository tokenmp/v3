-- =============================================================================
-- TokenMP V3 — Billing DB 迁移 0002：用户套餐配额覆盖（limit overrides）
-- -----------------------------------------------------------------------------
-- 镜像 services/billing/migrations/000002_limit_overrides.up.sql（golang-migrate 格式）。
-- 此文件是 infra/db 中立 schema 事实来源；services/billing/migrations 为 golang-migrate
-- 运行时副本。两份必须保持一致。
-- Phase 2: 允许对单个 user_plan 的某个 scope（hour5/weekly/period）施加覆盖：
--   * kind='reset'  → 重置该 scope 窗口的 effective start（max(baseStart, latest
--                     reset effective_from)），从而“原谅”该时间点之前的消耗。
--   * kind='bonus'  → 在生效区间内为该 scope 的 limit 追加 bonus_requests。
-- revoke = 将 effective_until 设为 now()（软失效，无需 status 列）。
-- 设计原则:
--   * 不修改 usage_ledger / quota_reservations 的 created_at（请求时间不动）。
--   * 仅影响 enforcement 与 usage-windows 读取的窗口起点与限额计算。
--   * FK → user_plans(id)，跨 user_plan 隔离。
-- 生成日期: 2026-07-29
-- 目标数据库: PostgreSQL 17
-- =============================================================================

BEGIN;

CREATE TABLE user_plan_limit_overrides (
    id              bigserial   PRIMARY KEY,
    user_plan_id    bigint      NOT NULL REFERENCES user_plans(id),
    kind            text        NOT NULL,
    scope           text        NOT NULL,
    effective_from  timestamptz NOT NULL DEFAULT now(),
    effective_until timestamptz,
    bonus_requests  int,
    reason          text        NOT NULL DEFAULT '',
    created_by      text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uplo_kind_chk  CHECK (kind IN ('reset', 'bonus')),
    CONSTRAINT uplo_scope_chk CHECK (scope IN ('hour5', 'weekly', 'period')),
    CONSTRAINT uplo_bonus_chk CHECK (
        (kind = 'bonus' AND bonus_requests IS NOT NULL AND bonus_requests >= 0)
        OR kind = 'reset'
    )
);

CREATE INDEX uplo_user_plan_idx ON user_plan_limit_overrides (user_plan_id);
CREATE INDEX uplo_lookup_idx ON user_plan_limit_overrides (user_plan_id, scope, kind, effective_from);

COMMENT ON TABLE  user_plan_limit_overrides IS '用户套餐配额覆盖（reset 重置窗口起点 / bonus 追加额度）';
COMMENT ON COLUMN user_plan_limit_overrides.kind IS 'reset=重置窗口起点, bonus=追加请求额度';
COMMENT ON COLUMN user_plan_limit_overrides.scope IS 'hour5/weekly/period';
COMMENT ON COLUMN user_plan_limit_overrides.effective_from IS '生效起始（含）';
COMMENT ON COLUMN user_plan_limit_overrides.effective_until IS 'NULL=持续生效；revoke 设为 now() 使其失效';
COMMENT ON COLUMN user_plan_limit_overrides.bonus_requests IS '仅 bonus 必填，追加的请求额度';

COMMIT;
