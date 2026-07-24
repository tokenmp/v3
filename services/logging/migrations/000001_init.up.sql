-- =============================================================================
-- TokenMP V3 — Log DB 初始化迁移
-- -----------------------------------------------------------------------------
-- 依据: docs/v3-db-schema-draft.md「# 二、Log DB（日志库）」(2.1–2.4)
-- 目标: 记录请求生命周期事件，不存明文 body，按天分区 + 自动清理。
-- 借鉴旧版三层结构 (request_logs → request_attempts → request_log_events)，
-- 修正：无明文、PostgreSQL 原生按天分区（非旧版 2 小时分表 2000 张）、
--       全部轮转（不只归档失败）、统一落库方 = Logging Service。
-- 设计原则:
--   * 日志不存明文 body，只存摘要/计数/错误分类。
--   * 按天 RANGE 分区，自动 detach+drop 旧分区（如保留 90 天）。
--   * executor 不直连此库，通过 HTTP/队列推给 Logging Service 落库。
--   * 分区表间不强制跨分区 FK（逻辑关联用 request_log_id / request_id）。
-- 生成日期: 2026-07-24
-- 目标数据库: PostgreSQL 17
-- =============================================================================


-- ============================================================================
-- 2.1 request_logs（请求级汇总）— 按天分区
--   分区: PARTITION BY RANGE (created_at)，每天一分区，自动 detach+drop 旧分区。
--   不存: request_body / response_body（旧版的隐私痛点）。
--   executor 不直连此库，Logging Service 异步落库。
-- ============================================================================
CREATE TABLE request_logs (
    id                          bigserial   ,
    request_id                  text        NOT NULL,
    trace_id                    text,
    user_id                     text,
    client_key_id               text,
    model_name                  text,
    resolved_model              text,
    route_id                    text,
    provider_id                 text,
    credential_id               text,
    protocol                    text,
    stream                      boolean     NOT NULL DEFAULT false,
    final_status                text        NOT NULL,
    http_status                 int,
    input_tokens                int,
    output_tokens               int,
    total_tokens                int,
    cache_tokens                int,
    latency_ms                  int,
    ttft_ms                     int,
    error_code                  text,
    error_type                  text,
    upstream_http_status        int,
    usage_status                text,
    thinking_mode               text,
    thinking_effort             text,
    thinking_effort_degraded    text,
    reservation_id              text,
    billing_plan                text,
    created_at                  timestamptz NOT NULL DEFAULT now(),
    completed_at                timestamptz,
    CONSTRAINT request_logs_pkey PRIMARY KEY (id, created_at),
    CONSTRAINT request_logs_final_status_chk CHECK (final_status IN (
        'success', 'client_error', 'upstream_error', 'timeout', 'transport_error'
    )),
    CONSTRAINT request_logs_usage_status_chk CHECK (
        usage_status IS NULL OR usage_status IN ('final', 'pending', 'estimated', 'missing')
    )
) PARTITION BY RANGE (created_at);

-- 初始分区：建最近若干天 + 未来预留（实际由 pg_partman / cron 维护）
CREATE TABLE request_logs_2026_07_24 PARTITION OF request_logs
    FOR VALUES FROM ('2026-07-24') TO ('2026-07-25');
CREATE TABLE request_logs_2026_07_25 PARTITION OF request_logs
    FOR VALUES FROM ('2026-07-25') TO ('2026-07-26');
CREATE TABLE request_logs_default PARTITION OF request_logs DEFAULT;

CREATE INDEX request_logs_request_id_idx ON request_logs (request_id);
CREATE INDEX request_logs_user_idx ON request_logs (user_id);
CREATE INDEX request_logs_created_idx ON request_logs (created_at);
CREATE INDEX request_logs_trace_idx ON request_logs (trace_id);
CREATE INDEX request_logs_status_idx ON request_logs (final_status, created_at);
CREATE INDEX request_logs_model_idx ON request_logs (model_name, created_at);

COMMENT ON TABLE request_logs IS '请求级汇总（按天分区），不存明文 body，Logging Service 异步落库';
COMMENT ON COLUMN request_logs.client_key_id IS '客户端 API key ID（哈希后，不存明文）';
COMMENT ON COLUMN request_logs.error_code IS '分类错误码（不存原始 message）';
COMMENT ON COLUMN request_logs.reservation_id IS 'Billing 预留 ID（关联 Billing DB，逻辑关联）';
COMMENT ON COLUMN request_logs.usage_status IS 'final/pending/estimated/missing';

-- ============================================================================
-- 2.2 request_attempts（attempt 级）— 按天分区
--   一次请求可能多次 attempt（retry/fallback）。
--   request_log_id 与分区表不强制跨分区 FK，逻辑关联。
-- ============================================================================
CREATE TABLE request_attempts (
    id                  bigserial   ,
    request_log_id      bigint      NOT NULL,
    request_id          text        NOT NULL,
    attempt_index       int         NOT NULL DEFAULT 0,
    route_id            text,
    provider_id         text,
    credential_id       text,
    upstream_model      text,
    upstream_url        text,
    status              text        NOT NULL,
    http_status         int,
    latency_ms         int,
    error_code          text,
    error_type          text,
    upstream_http_status int,
    retry_classified    text,
    metadata            jsonb,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT request_attempts_pkey PRIMARY KEY (id, created_at),
    CONSTRAINT request_attempts_status_chk CHECK (status IN (
        'success', 'upstream_error', 'timeout', 'transport_error'
    )),
    CONSTRAINT request_attempts_retry_chk CHECK (
        retry_classified IS NULL OR retry_classified IN ('retryable', 'non_retryable', 'terminal')
    )
) PARTITION BY RANGE (created_at);

CREATE TABLE request_attempts_2026_07_24 PARTITION OF request_attempts
    FOR VALUES FROM ('2026-07-24') TO ('2026-07-25');
CREATE TABLE request_attempts_2026_07_25 PARTITION OF request_attempts
    FOR VALUES FROM ('2026-07-25') TO ('2026-07-26');
CREATE TABLE request_attempts_default PARTITION OF request_attempts DEFAULT;

CREATE INDEX request_attempts_log_idx ON request_attempts (request_log_id);
CREATE INDEX request_attempts_request_idx ON request_attempts (request_id);
CREATE INDEX request_attempts_created_idx ON request_attempts (created_at);

COMMENT ON TABLE request_attempts IS 'attempt 级（按天分区），一次请求可多次 attempt';
COMMENT ON COLUMN request_attempts.upstream_url IS '上游 URL（不含 query 凭据，V3 已脱敏）';
COMMENT ON COLUMN request_attempts.metadata IS '安全元数据（不含请求/响应明文，V3 attempt observer 已脱敏）';
COMMENT ON COLUMN request_attempts.retry_classified IS 'retryable/non_retryable/terminal';

-- ============================================================================
-- 2.3 request_log_events（事件级时间线）— 按天分区
--   借鉴旧版事件时间线，事件来自 executor + Edge 推送。
-- ============================================================================
CREATE TABLE request_log_events (
    id              bigserial   ,
    request_log_id  bigint      NOT NULL,
    request_id      text        NOT NULL,
    trace_id        text,
    source          text        NOT NULL,
    stage           text        NOT NULL,
    status          text        NOT NULL DEFAULT 'info',
    attempt_index    int,
    duration_ms     int,
    message         text,
    metadata        jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT request_log_events_pkey PRIMARY KEY (id, created_at),
    CONSTRAINT request_log_events_source_chk CHECK (source IN ('edge', 'executor')),
    CONSTRAINT request_log_events_status_chk CHECK (status IN ('info', 'success', 'failed', 'skipped')),
    CONSTRAINT request_log_events_stage_chk CHECK (stage IN (
        'received', 'key_verified', 'route_selected', 'quota_reserved',
        'upstream_started', 'upstream_finished', 'terminal', 'completed'
    ))
) PARTITION BY RANGE (created_at);

CREATE TABLE request_log_events_2026_07_24 PARTITION OF request_log_events
    FOR VALUES FROM ('2026-07-24') TO ('2026-07-25');
CREATE TABLE request_log_events_2026_07_25 PARTITION OF request_log_events
    FOR VALUES FROM ('2026-07-25') TO ('2026-07-26');
CREATE TABLE request_log_events_default PARTITION OF request_log_events DEFAULT;

CREATE INDEX request_log_events_log_idx ON request_log_events (request_log_id);
CREATE INDEX request_log_events_request_idx ON request_log_events (request_id);
CREATE INDEX request_log_events_created_idx ON request_log_events (created_at);
CREATE INDEX request_log_events_stage_idx ON request_log_events (stage, created_at);

COMMENT ON TABLE request_log_events IS '事件级时间线（按天分区），事件来自 executor + Edge';
COMMENT ON COLUMN request_log_events.source IS 'edge/executor（事件来源）';
COMMENT ON COLUMN request_log_events.message IS '摘要（不含明文）';

-- ============================================================================
-- 2.4 log_archive_runs（归档运行记录）
--   不再像旧版产生 2000 张分表。用 PostgreSQL 原生 partition + pg_partman
--   或 cron 自动 drop 旧分区。此表记录每次清理操作。
-- ============================================================================
CREATE TABLE log_archive_runs (
    id              bigserial   PRIMARY KEY,
    table_name      text        NOT NULL,
    partition_name  text        NOT NULL,
    rows_archived   bigint,
    started_at      timestamptz NOT NULL DEFAULT now(),
    finished_at     timestamptz,
    status          text        NOT NULL DEFAULT 'running',
    error           text,
    CONSTRAINT log_archive_runs_status_chk CHECK (status IN ('running', 'success', 'failed')),
    CONSTRAINT log_archive_runs_table_chk CHECK (table_name IN (
        'request_logs', 'request_attempts', 'request_log_events'
    ))
);

CREATE INDEX log_archive_runs_started_idx ON log_archive_runs (started_at);
CREATE INDEX log_archive_runs_table_status_idx ON log_archive_runs (table_name, status);

COMMENT ON TABLE log_archive_runs IS '分区清理运行记录（pg_partman / cron drop 旧分区）';

-- log_archive_runs 的 started_at/finished_at 由清理应用逻辑设置，不挂触发器。
