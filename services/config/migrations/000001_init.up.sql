-- =============================================================================
-- TokenMP V3 — Config DB 初始化迁移
-- -----------------------------------------------------------------------------
-- 依据: docs/v3-db-schema-draft.md「# 一、Config DB（配置库）」(1.1–1.15)
-- 目标: 管理 provider/model/route/credential/adapter 配置，支持版本草稿/发布/
--       回滚，编译成 ConfigSnapshot 下发给 executor。
-- 设计原则:
--   * 配置带版本 (draft/published/archived)，解决旧版"改即生效、无法回滚"。
--   * 凭据密文独立 Secret Store，本库只存 credential ref (vault://)，不下发明文。
--   * FK 强制 VALID，命名统一 snake_case，软删除用 status 不物理删。
--   * 字段名与 V3 ConfigSnapshot 编译目标对齐 (model_id/provider_id/adapter_id/
--     protocol/sdk_kind/effort_mapping 等)，Config Service 编译时映射简单。
-- 生成日期: 2026-07-24
-- 目标数据库: PostgreSQL 17
-- =============================================================================


-- ============================================================================
-- 1.1 providers（上游 provider）
-- ============================================================================
CREATE TABLE providers (
    id              text        PRIMARY KEY,
    name            text        NOT NULL,
    display_label   text        NOT NULL,
    selector        text        NOT NULL,
    base_url        text        NOT NULL,
    sdk_kind        text        NOT NULL,
    protocol        text        NOT NULL,
    default_retry   jsonb,
    default_timeout jsonb,
    status          text        NOT NULL DEFAULT 'active',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT providers_status_chk CHECK (status IN ('active', 'disabled', 'deleted')),
    CONSTRAINT providers_sdk_kind_chk CHECK (sdk_kind IN ('openai', 'anthropic'))
);

CREATE UNIQUE INDEX providers_display_label_uidx
    ON providers (display_label) WHERE status <> 'deleted';
CREATE UNIQUE INDEX providers_selector_uidx
    ON providers (selector) WHERE status <> 'deleted';
CREATE INDEX providers_status_idx ON providers (status);

COMMENT ON TABLE providers IS '上游 provider（如 mi-chat），对齐 V3 ProviderConfig.ID';
COMMENT ON COLUMN providers.display_label IS '对外代号（如 a/b），用于 /v1/models 的 model_id@provider 标识，不暴露内部 selector';
COMMENT ON COLUMN providers.default_retry IS '默认 retry policy (V3 RetryPolicy JSON)';
COMMENT ON COLUMN providers.default_timeout IS '默认 timeout policy (V3 TimeoutPolicy JSON)';

-- ============================================================================
-- 1.2 upstream_endpoints（上游端点）
--   不进 route_mappings。route 只绑 provider；执行期 executor 按 route.protocol
--   从该 provider 的 endpoints 选匹配协议的那条，拼出 URL + 鉴权方式。
-- ============================================================================
CREATE TABLE upstream_endpoints (
    id           bigserial   PRIMARY KEY,
    provider_id  text        NOT NULL REFERENCES providers(id),
    path         text        NOT NULL,
    protocol     text        NOT NULL,
    auth_kind    text        NOT NULL,
    auth_header  text,
    auth_query   text,
    auth_prefix  text,
    status       text        NOT NULL DEFAULT 'active',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT endpoints_status_chk CHECK (status IN ('active', 'disabled', 'deleted')),
    CONSTRAINT endpoints_auth_kind_chk CHECK (auth_kind IN ('bearer_header', 'api_key_header', 'api_key_query'))
);

CREATE INDEX endpoints_provider_idx ON upstream_endpoints (provider_id);
CREATE INDEX endpoints_provider_protocol_idx ON upstream_endpoints (provider_id, protocol) WHERE status <> 'deleted';
CREATE UNIQUE INDEX endpoints_provider_protocol_uidx
    ON upstream_endpoints (provider_id, protocol) WHERE status <> 'deleted';

COMMENT ON TABLE upstream_endpoints IS '上游端点（provider 子表），执行时按 protocol 选用，不进 route_mappings';
COMMENT ON COLUMN upstream_endpoints.auth_kind IS 'bearer_header/api_key_header/api_key_query (V3 AuthRule.Kind)';

-- ============================================================================
-- 1.3 upstream_credentials（上游凭据元数据）
--   不存明文。明文在 Secret Store，这里只存 credential ref + 展示用 prefix/suffix。
-- ============================================================================
CREATE TABLE upstream_credentials (
    id              text        PRIMARY KEY,
    provider_id     text        NOT NULL REFERENCES providers(id),
    credential_ref  text        NOT NULL,
    key_prefix      text,
    key_suffix      text,
    priority        int         NOT NULL DEFAULT 0,
    max_concurrency int,
    daily_quota     int,
    status          text        NOT NULL DEFAULT 'active',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT creds_status_chk CHECK (status IN ('active', 'disabled', 'deleted'))
);

CREATE INDEX creds_provider_idx ON upstream_credentials (provider_id);
CREATE INDEX creds_status_idx ON upstream_credentials (status);

COMMENT ON TABLE upstream_credentials IS '上游凭据元数据，不存明文，只存 vault:// ref + 展示 prefix/suffix';
COMMENT ON COLUMN upstream_credentials.credential_ref IS 'vault://provider/key (V3 CredentialRef，Secret Store 引用)';
COMMENT ON COLUMN upstream_credentials.priority IS '候选优先级 (V3 CredentialConfig.Priority)';

-- ============================================================================
-- 1.4 models（公共模型）
--   对外公共模型产品，model_id 全局唯一。同一 model 名可在不同 provider 下接入
--   （通过多条 route_mappings 实现）。模型产品级属性放这里；provider 特定参数差异
--   （context_window/max_output_tokens 覆盖）放 route_mappings。
--   /v1/models 采用多通道模式 + default：每个 (model,provider) 通道独立成条目，
--   用 model_id@display_label 标识。
-- ============================================================================
CREATE TABLE models (
    id                        text        PRIMARY KEY,
    display_name              text        NOT NULL,
    input_modalities          jsonb       NOT NULL DEFAULT '["text"]'::jsonb,
    output_modalities         jsonb       NOT NULL DEFAULT '["text"]'::jsonb,
    capabilities              jsonb       NOT NULL DEFAULT '[]'::jsonb,
    context_window            int,
    max_output_tokens         int,
    thinking_supported        boolean     NOT NULL DEFAULT false,
    thinking_default_effort   text,
    thinking_max_effort       text,
    thinking_min_budget_token int,
    thinking_max_budget_token int,
    status                    text        NOT NULL DEFAULT 'active',
    created_at                timestamptz NOT NULL DEFAULT now(),
    updated_at                timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT models_status_chk CHECK (status IN ('active', 'disabled', 'deleted')),
    CONSTRAINT models_thinking_effort_chk CHECK (
        thinking_default_effort IS NULL OR
        thinking_default_effort IN ('none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max')
    ),
    CONSTRAINT models_thinking_max_effort_chk CHECK (
        thinking_max_effort IS NULL OR
        thinking_max_effort IN ('none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max')
    ),
    CONSTRAINT models_context_chk CHECK (context_window IS NULL OR context_window > 0),
    CONSTRAINT models_max_output_chk CHECK (max_output_tokens IS NULL OR max_output_tokens > 0)
);

CREATE INDEX models_status_idx ON models (status);

COMMENT ON TABLE models IS '公共模型产品，model_id 全局唯一；同一 model 可多 provider 接入（多条 route）';
COMMENT ON COLUMN models.input_modalities IS '输入模态 ["text","image"]，枚举: text/image/audio/video/embedding/tool';
COMMENT ON COLUMN models.output_modalities IS '输出模态 ["text"]，同 input 枚举';
COMMENT ON COLUMN models.capabilities IS '能力 ["chat","streaming","thinking","vision","tools","responses","image","audio","video","embedding"]';
COMMENT ON COLUMN models.context_window IS '默认上下文窗口(token)；汇总条目返回 default route 的值或本默认值';

-- ============================================================================
-- 1.5 model_fallbacks（模型 fallback）— 系统级，当前搁置
--   V3 有 FallbackModelIDs，但 fallback 是系统路由策略决定（routing_policies +
--   quarantine 驱动），不是用户配置项。当前不启用，保留 schema 待系统路由层需要。
--   若启用: model_id FK→models.id, fallback_model_id text, position int,
--           PK(model_id, position)
-- ============================================================================

-- ============================================================================
-- 1.6 adapters（适配策略）
--   一个 adapter = 一套"如何适配上游"的规则。绑定 provider (继承关系):
--   provider_id 可空，NULL=通用适配器(所有 provider)；非空=provider 专属。
--   route_mappings.adapter_id 可空: NULL=不走适配(原样转发)；非空=指定适配器。
--   所有 JSONB 字段有固定规范 (见 schema draft 1.6.1–1.6.6)。
-- ============================================================================
CREATE TABLE adapters (
    id                 text        PRIMARY KEY,
    name               text        NOT NULL,
    version            int         NOT NULL DEFAULT 1,
    provider_id        text        REFERENCES providers(id),
    sdk_kind           text        NOT NULL,
    protocol           text        NOT NULL,
    capability_require jsonb       NOT NULL DEFAULT '[]'::jsonb,
    capability_deny    jsonb       NOT NULL DEFAULT '[]'::jsonb,
    thinking           jsonb,
    request_policy     jsonb,
    response_policy    jsonb,
    retry_policy       jsonb,
    timeout_policy     jsonb,
    status             text        NOT NULL DEFAULT 'active',
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT adapters_status_chk CHECK (status IN ('active', 'disabled', 'deleted')),
    CONSTRAINT adapters_sdk_kind_chk CHECK (sdk_kind IN ('openai', 'anthropic')),
    CONSTRAINT adapters_version_chk CHECK (version >= 1)
);

CREATE INDEX adapters_provider_idx ON adapters (provider_id);
CREATE INDEX adapters_status_idx ON adapters (status);

COMMENT ON TABLE adapters IS '适配策略：一套如何适配上游的规则；provider_id 可空(NULL=通用)';
COMMENT ON COLUMN adapters.provider_id IS '可空：NULL=通用适配器(所有 provider)；非空=仅该 provider 生效';
COMMENT ON COLUMN adapters.capability_require IS '需要的能力，枚举见 schema 1.6.1';
COMMENT ON COLUMN adapters.thinking IS 'ThinkingPolicy JSON，结构见 schema 1.6.2';
COMMENT ON COLUMN adapters.request_policy IS '请求规则 JSON，结构见 schema 1.6.3 (含 set_if_missing/clamp_number 等 action)';
COMMENT ON COLUMN adapters.response_policy IS '响应规则 JSON，结构见 schema 1.6.4';
COMMENT ON COLUMN adapters.retry_policy IS '重试规则 JSON，结构见 schema 1.6.5';
COMMENT ON COLUMN adapters.timeout_policy IS '超时规则 JSON，结构见 schema 1.6.6';

-- ============================================================================
-- 1.7 route_mappings（路由）— 核心
--   ConfigSnapshot 编译成 Routes[] 的主表。不存 endpoint：route 只绑 provider +
--   adapter + 凭据候选；endpoint 是 provider 子表，执行时按 protocol 选用。
--   provider 特定模型参数(context_window/max_output_tokens)放这里，空则用 models 默认。
-- ============================================================================
CREATE TABLE route_mappings (
    id                text        PRIMARY KEY,
    model_id          text        NOT NULL REFERENCES models(id),
    provider_id       text        NOT NULL REFERENCES providers(id),
    adapter_id        text        REFERENCES adapters(id),
    upstream_model    text        NOT NULL,
    protocol          text        NOT NULL,
    priority          int         NOT NULL DEFAULT 0,
    enabled           boolean     NOT NULL DEFAULT true,
    is_default        boolean     NOT NULL DEFAULT false,
    context_window    int,
    max_output_tokens int,
    route_group       text,
    retry_policy      jsonb,
    timeout_policy    jsonb,
    status            text        NOT NULL DEFAULT 'active',
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT routes_status_chk CHECK (status IN ('active', 'disabled', 'deleted')),
    CONSTRAINT routes_context_chk CHECK (context_window IS NULL OR context_window > 0),
    CONSTRAINT routes_max_output_chk CHECK (max_output_tokens IS NULL OR max_output_tokens > 0)
);

CREATE INDEX routes_model_idx ON route_mappings (model_id);
CREATE INDEX routes_provider_idx ON route_mappings (provider_id);
CREATE INDEX routes_model_enabled_idx ON route_mappings (model_id, enabled) WHERE status <> 'deleted';
CREATE INDEX routes_route_group_idx ON route_mappings (route_group) WHERE route_group IS NOT NULL;
-- is_default 部分唯一索引：一个 model 下最多一条 active default route
CREATE UNIQUE INDEX routes_model_default_uidx
    ON route_mappings (model_id) WHERE is_default = true AND status = 'active';

COMMENT ON TABLE route_mappings IS '路由（核心）：model+provider+adapter+凭据候选；不存 endpoint，执行期按 protocol 选';
COMMENT ON COLUMN route_mappings.adapter_id IS '可空：NULL=不走适配(原样转发)；非空=指定适配器';
COMMENT ON COLUMN route_mappings.is_default IS 'default 通道标记：无@调用 model_id 时走这条；(model_id)下最多一条 active default';
COMMENT ON COLUMN route_mappings.context_window IS 'provider 特定上下文窗口(空=用 models 默认)；进 /v1/models 多通道条目';

-- ============================================================================
-- 1.8 route_credentials（路由凭据候选）
--   对齐 V3 RouteConfig.Credentials (多候选 + priority + enabled)。
-- ============================================================================
CREATE TABLE route_credentials (
    route_id      text    NOT NULL REFERENCES route_mappings(id),
    credential_id text    NOT NULL REFERENCES upstream_credentials(id),
    priority      int     NOT NULL DEFAULT 0,
    enabled       boolean NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (route_id, credential_id)
);

CREATE INDEX route_credentials_credential_idx ON route_credentials (credential_id);

COMMENT ON TABLE route_credentials IS '路由凭据候选（多候选 + priority + enabled）';

-- ============================================================================
-- 1.9 route_fallbacks（路由 fallback）— 系统级，当前搁置
--   fallback 由 routing_policies + quarantine 驱动的系统路由策略决定，不是用户配置项。
--   当前不启用。若启用: route_id FK→route_mappings.id, fallback_route_id text,
--   position int, PK(route_id, position)
-- ============================================================================

-- ============================================================================
-- 1.10 route_groups / route_group_members / routing_policies
--   借鉴旧版多维度权重路由。
-- ============================================================================
CREATE TABLE route_groups (
    id           text        PRIMARY KEY,
    name         text        NOT NULL,
    display_name text,
    is_system    boolean     NOT NULL DEFAULT false,
    status       text        NOT NULL DEFAULT 'active',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT route_groups_status_chk CHECK (status IN ('active', 'disabled', 'deleted'))
);

CREATE UNIQUE INDEX route_groups_name_uidx ON route_groups (name) WHERE status <> 'deleted';

CREATE TABLE route_group_members (
    route_group_id text NOT NULL REFERENCES route_groups(id),
    route_id       text NOT NULL REFERENCES route_mappings(id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (route_group_id, route_id)
);

CREATE INDEX route_group_members_route_idx ON route_group_members (route_id);

CREATE TABLE routing_policies (
    id          bigserial   PRIMARY KEY,
    name        text        NOT NULL,
    weights     jsonb       NOT NULL DEFAULT '{}'::jsonb,
    temperature numeric     NOT NULL DEFAULT 1.0,
    status      text        NOT NULL DEFAULT 'active',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT routing_policies_status_chk CHECK (status IN ('active', 'disabled', 'deleted')),
    CONSTRAINT routing_policies_temperature_chk CHECK (temperature >= 0 AND temperature <= 2)
);

CREATE UNIQUE INDEX routing_policies_name_uidx ON routing_policies (name) WHERE status <> 'deleted';

COMMENT ON COLUMN routing_policies.weights IS '权重 jsonb: price/speed/success/availability/concurrency/quota';

-- ============================================================================
-- 1.11 config_revisions（版本/草稿/发布）— 解决旧版无版本痛点
--   旧版配置改即生效无法回滚。V3 每次发布生成一条 revision，executor 拉取的是某个
--   published revision 编译出的 snapshot。
-- ============================================================================
CREATE TABLE config_revisions (
    id                  bigserial   PRIMARY KEY,
    revision            text        NOT NULL,
    status              text        NOT NULL DEFAULT 'draft',
    created_by          text,
    created_at          timestamptz NOT NULL DEFAULT now(),
    published_at        timestamptz,
    archived_at         timestamptz,
    change_log          text,
    parent_revision_id  bigserial   REFERENCES config_revisions(id),
    CONSTRAINT config_revisions_status_chk CHECK (status IN ('draft', 'published', 'archived'))
);

CREATE UNIQUE INDEX config_revisions_revision_uidx ON config_revisions (revision);

COMMENT ON TABLE config_revisions IS '配置版本(draft/published/archived)，解决旧版无版本无法回滚痛点';
COMMENT ON COLUMN config_revisions.parent_revision_id IS '基于哪个 revision 草稿(可追溯链)';

-- ============================================================================
-- 1.12 config_revision_snapshots（发布的完整 snapshot 快照）
--   发布时把全量配置序列化成 JSON 存一份(不可变)。executor 拉取的就是这个。
--   对齐 V3 ConfigSnapshot 全部字段。
-- ============================================================================
CREATE TABLE config_revision_snapshots (
    id            bigserial   PRIMARY KEY,
    revision_id   bigint      NOT NULL REFERENCES config_revisions(id),
    snapshot_json jsonb       NOT NULL,
    compiled_meta jsonb,
    sha256        text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX config_revision_snapshots_revision_idx ON config_revision_snapshots (revision_id);
CREATE UNIQUE INDEX config_revision_snapshots_revision_uidx ON config_revision_snapshots (revision_id);

COMMENT ON TABLE config_revision_snapshots IS '发布的完整 ConfigSnapshot JSON(不可变)，executor 拉取的就是这个';
COMMENT ON COLUMN config_revision_snapshots.snapshot_json IS '完整 ConfigSnapshot 序列化(编译输入)';
COMMENT ON COLUMN config_revision_snapshots.compiled_meta IS '编译结果元数据(route 计数/generation/校验状态)';
COMMENT ON COLUMN config_revision_snapshots.sha256 IS 'snapshot_json 摘要(完整性校验)';

-- ============================================================================
-- 1.13 config_audit_log（配置变更审计）
-- ============================================================================
CREATE TABLE config_audit_log (
    id           bigserial   PRIMARY KEY,
    revision_id  bigint      REFERENCES config_revisions(id),
    actor        text,
    action       text        NOT NULL,
    entity_type  text        NOT NULL,
    entity_id    text,
    before       jsonb,
    after        jsonb,
    at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT audit_action_chk CHECK (action IN ('create', 'update', 'delete', 'publish', 'archive', 'rollback'))
);

CREATE INDEX config_audit_log_revision_idx ON config_audit_log (revision_id);
CREATE INDEX config_audit_log_entity_idx ON config_audit_log (entity_type, entity_id);
CREATE INDEX config_audit_log_at_idx ON config_audit_log (at);

COMMENT ON TABLE config_audit_log IS '配置变更审计';
COMMENT ON COLUMN config_audit_log.revision_id IS '关联 revision(NULL=直接改表，也应记录)';

-- ============================================================================
-- 1.14 global_config（全局策略 KV）
--   对齐 V3 GlobalPolicy (默认 retry/timeout/auto_model_ids) + 旧版 system_config。
-- ============================================================================
CREATE TABLE global_config (
    key        text        PRIMARY KEY,
    value      jsonb       NOT NULL,
    updated_by text,
    updated_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE global_config IS '全局策略 KV，对齐 V3 GlobalPolicy (default_retry/default_timeout/auto_model_ids)';

-- ============================================================================
-- 1.15 price_multiplier_rules（动态定价，可选）
--   借鉴旧版(provider/key/model/protocol 维度 + 时间窗口)。放 Config DB 因属定价配置；
--   实际计费在 Billing DB 结算。
-- ============================================================================
CREATE TABLE price_multiplier_rules (
    id                 bigserial   PRIMARY KEY,
    provider_id        text        REFERENCES providers(id),
    credential_id      text        REFERENCES upstream_credentials(id),
    model_id           text        REFERENCES models(id),
    protocol           text,
    input_multiplier   numeric     NOT NULL DEFAULT 1.0,
    output_multiplier  numeric     NOT NULL DEFAULT 1.0,
    valid_from         timestamptz,
    valid_to           timestamptz,
    compose_mode       text        NOT NULL DEFAULT 'replace',
    status             text        NOT NULL DEFAULT 'active',
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT price_mult_status_chk CHECK (status IN ('active', 'disabled')),
    CONSTRAINT price_mult_compose_chk CHECK (compose_mode IN ('replace', 'multiply')),
    CONSTRAINT price_mult_input_chk CHECK (input_multiplier >= 0),
    CONSTRAINT price_mult_output_chk CHECK (output_multiplier >= 0)
);

CREATE INDEX price_mult_match_idx ON price_multiplier_rules (provider_id, credential_id, model_id, protocol) WHERE status = 'active';

COMMENT ON TABLE price_multiplier_rules IS '动态定价规则(可选)，放 Config DB 因属定价配置；计费在 Billing DB 结算';
COMMENT ON COLUMN price_multiplier_rules.compose_mode IS 'replace/multiply';

-- ============================================================================
-- updated_at 触发器（统一自动更新 updated_at）
-- ============================================================================
CREATE OR REPLACE FUNCTION touch_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- 为所有带 updated_at 的表挂触发器
DO $$
DECLARE
    t text;
BEGIN
    FOR t IN
        SELECT table_name FROM information_schema.columns
        WHERE column_name = 'updated_at'
          AND table_schema = 'public'
          AND table_name IN (
              'providers', 'upstream_endpoints', 'upstream_credentials', 'models',
              'adapters', 'route_mappings', 'route_groups', 'routing_policies',
              'price_multiplier_rules'
          )
    LOOP
        EXECUTE format('CREATE TRIGGER set_updated_at BEFORE UPDATE ON %I '
                       'FOR EACH ROW EXECUTE FUNCTION touch_updated_at();', t);
    END LOOP;
END $$;

