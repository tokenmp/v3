-- =============================================================================
-- TokenMP V3 — Billing DB 初始化迁移
-- -----------------------------------------------------------------------------
-- 依据: docs/v3-db-schema-draft.md「# 三、Billing DB（计费库）」(3.1–3.6)
-- 目标: 套餐/配额/记账独立于 executor。executor 不直连此库，由 Edge/BFF +
--       Billing Service 操作。
-- 借鉴旧版 plans/user_plans/quota_reservations/usage_ledger（先预留后结算）。
-- 设计原则:
--   * 借鉴旧版先预留后结算 (quota_reservations)。
--   * 用户主数据建议独立 Auth/Identity 库（旧版 api_keys/user_api_keys/bot_keys
--     三表重叠的教训），Billing 只引用 user_id。
--   * 与 Log DB 的 request_logs 是逻辑关联（不跨库 FK）。
--   * marketplace_* 作为可选独立模块，schema 占位，不耦合 executor。
-- 生成日期: 2026-07-24
-- 目标数据库: PostgreSQL 17
-- =============================================================================

BEGIN;

-- ============================================================================
-- 3.1 plans（套餐定义）
-- ============================================================================
CREATE TABLE plans (
    id            bigserial   PRIMARY KEY,
    name          text        NOT NULL,
    plan_type     text        NOT NULL,
    price         numeric(12,2) NOT NULL DEFAULT 0,
    category      text        NOT NULL,
    hourly_limit  int,
    weekly_limit  int,
    monthly_limit int,
    token_limit   bigint,
    allowed_models jsonb      NOT NULL DEFAULT '[]'::jsonb,
    status        text        NOT NULL DEFAULT 'active',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT plans_status_chk CHECK (status IN ('active', 'disabled', 'deleted')),
    CONSTRAINT plans_plan_type_chk CHECK (plan_type IN ('coding', 'token', 'image', 'free')),
    CONSTRAINT plans_category_chk CHECK (category IN ('monthly', 'quarterly', 'yearly')),
    CONSTRAINT plans_price_chk CHECK (price >= 0),
    CONSTRAINT plans_token_limit_chk CHECK (token_limit IS NULL OR token_limit >= 0)
);

CREATE UNIQUE INDEX plans_name_uidx ON plans (name) WHERE status <> 'deleted';
CREATE INDEX plans_status_idx ON plans (status);

COMMENT ON TABLE plans IS '套餐定义（coding/token/image/free）';
COMMENT ON COLUMN plans.hourly_limit IS 'coding 套餐请求配额';
COMMENT ON COLUMN plans.allowed_models IS '模型白名单 jsonb';

-- ============================================================================
-- 3.2 users（用户）— 与 Auth/Identity 库对齐
--   用户主数据建议放独立 Auth/Identity 库（旧版 api_keys/user_api_keys/bot_keys
--   三表重叠的教训）。这里只存计费所需的最小用户引用。
-- ============================================================================
CREATE TABLE users (
    id          text        PRIMARY KEY,
    status      text        NOT NULL DEFAULT 'active',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_status_chk CHECK (status IN ('active', 'disabled'))
);

COMMENT ON TABLE users IS '用户（计费最小引用，主数据在 Auth/Identity 库）';

-- ============================================================================
-- 3.3 user_plans（用户套餐绑定）
-- ============================================================================
CREATE TABLE user_plans (
    id           bigserial   PRIMARY KEY,
    user_id      text        NOT NULL REFERENCES users(id),
    plan_id      bigint      NOT NULL REFERENCES plans(id),
    plan_type    text        NOT NULL,
    status       text        NOT NULL DEFAULT 'active',
    activated_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT user_plans_status_chk CHECK (status IN ('active', 'expired', 'cancelled')),
    CONSTRAINT user_plans_plan_type_chk CHECK (plan_type IN ('coding', 'token', 'image', 'free'))
);

CREATE INDEX user_plans_user_idx ON user_plans (user_id);
CREATE INDEX user_plans_user_status_idx ON user_plans (user_id, status) WHERE status = 'active';

COMMENT ON TABLE user_plans IS '用户套餐绑定（active/expired/cancelled）';

-- ============================================================================
-- 3.4 quota_reservations（配额预留）— 借鉴旧版先预留后结算
--   一次请求开始时 Edge 调 Billing 预留，结束时结算。
--   request_id 关联 Log DB 的 request_logs.request_id（逻辑关联，不跨库 FK）。
-- ============================================================================
CREATE TABLE quota_reservations (
    id                  text        PRIMARY KEY,
    user_id             text        NOT NULL REFERENCES users(id),
    request_id          text        NOT NULL,
    billing_plan        text        NOT NULL,
    status              text        NOT NULL DEFAULT 'reserved',
    reserved_requests  int,
    reserved_tokens     bigint,
    final_requests      int,
    final_tokens        bigint,
    reserved_at         timestamptz NOT NULL DEFAULT now(),
    finalized_at        timestamptz,
    expires_at          timestamptz,
    CONSTRAINT quota_reservations_status_chk CHECK (status IN (
        'reserved', 'finalized', 'released', 'expired'
    )),
    CONSTRAINT quota_reservations_billing_chk CHECK (billing_plan IN (
        'coding', 'token', 'image', 'free'
    ))
);

CREATE INDEX quota_reservations_user_idx ON quota_reservations (user_id);
CREATE INDEX quota_reservations_request_idx ON quota_reservations (request_id);
CREATE INDEX quota_reservations_status_idx ON quota_reservations (status, reserved_at);

COMMENT ON TABLE quota_reservations IS '配额预留（借鉴旧版先预留后结算），request_id 逻辑关联 Log DB';
COMMENT ON COLUMN quota_reservations.id IS 'reservation_id（对齐 V3 请求附带的 reservation_id）';

-- ============================================================================
-- 3.5 usage_ledger（用量账本）— 借鉴旧版
--   所有额度变动流水。
-- ============================================================================
CREATE TABLE usage_ledger (
    id               bigserial   PRIMARY KEY,
    user_id          text        NOT NULL REFERENCES users(id),
    request_id       text,
    ledger_type      text        NOT NULL,
    billing_plan     text        NOT NULL,
    token_delta      bigint      NOT NULL DEFAULT 0,
    request_delta    int         NOT NULL DEFAULT 0,
    reason           text,
    idempotency_key  text        NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT usage_ledger_ledger_type_chk CHECK (ledger_type IN (
        'reserve', 'charge', 'refund', 'recharge',
        'adjustment', 'plan_grant', 'plan_renew'
    )),
    CONSTRAINT usage_ledger_billing_chk CHECK (billing_plan IN (
        'coding', 'token', 'image', 'free'
    ))
);

CREATE UNIQUE INDEX usage_ledger_idempotency_uidx ON usage_ledger (idempotency_key);
CREATE INDEX usage_ledger_user_idx ON usage_ledger (user_id);
CREATE INDEX usage_ledger_request_idx ON usage_ledger (request_id);
CREATE INDEX usage_ledger_created_idx ON usage_ledger (created_at);

COMMENT ON TABLE usage_ledger IS '用量账本（所有额度变动流水）';
COMMENT ON COLUMN usage_ledger.token_delta IS '正=增 负=减';
COMMENT ON COLUMN usage_ledger.idempotency_key IS '幂等 key（去重）';

-- ============================================================================
-- 3.6 marketplace_*（市场机制，可选独立模块）
--   旧版 marketplace（卖家上架/消费者付费/供应商奖励/平台费率/账本）作为可选独立
--   模块。schema 占位，按需展开。与 executor 解耦。
--   若启用，放本库或独立库。当前不建表，保留占位。
-- ============================================================================

-- ============================================================================
-- updated_at 触发器（统一自动更新 updated_at）
-- ============================================================================
CREATE OR REPLACE FUNCTION touch_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DO $$
DECLARE
    t text;
BEGIN
    FOR t IN
        SELECT unnest(ARRAY['plans', 'users', 'user_plans'])
    LOOP
        EXECUTE format('CREATE TRIGGER set_updated_at BEFORE UPDATE ON %I '
                       'FOR EACH ROW EXECUTE FUNCTION touch_updated_at();', t);
    END LOOP;
END $$;

COMMIT;
