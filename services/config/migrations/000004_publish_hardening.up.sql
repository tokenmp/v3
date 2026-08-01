-- 000004: 加固 Config 写路径 — draft CAS、singleton published、rollback 元数据、
-- audit 约束与 secret 边界。
--
-- 设计目标（expand/contract，不修改历史 migration）:
--   * draft 引入乐观并发控制：新增 version 列与 CAS 语义（UPDATE ... WHERE version=?）。
--   * 全局最多一个 active published：在 config_revisions 上加 partial unique index
--     (WHERE status='published')，从 schema 层杜绝多 published 竞态。
--   * rollback 元数据：新增 source_revision_id（回滚来源的不可变快照）与 rollback_note。
--   * audit 列扩展：新增 actor_kind、request_id 以支持服务间授权审计；收紧 action 枚举。
--   * secret 边界（expand）: upstream_credentials.credential_ref 恢复为 secret-free 唯一事实，
--     api_key 保留为 nullable 仅用于历史数据识别，新写入由应用层强制拒绝明文。
--     本 migration 不自动外迁/输出任何 secret 内容（fail-closed 由应用层 + 运维 runbook 负责）。
--   * 修正 000001 遗留 schema 缺陷：config_revisions.parent_revision_id 原为 bigserial
--     (NOT NULL + 序列默认)，但它是可选的自引用父 revision，应允许 NULL。本 migration
--     DROP NOT NULL 并移除序列默认，不修改历史 migration。
--
-- 所有变更均为可逆的 expand，down migration 完整 contract 回退。

-- 1. draft 乐观并发版本号
ALTER TABLE config_revisions
    ADD COLUMN IF NOT EXISTS version int NOT NULL DEFAULT 1;

COMMENT ON COLUMN config_revisions.version IS 'draft 乐观并发版本号，每次 CAS update 自增；published/archived 后不可变';

-- 1a. 修正 000001 遗留缺陷：parent_revision_id 应为可选自引用父 revision。
--     000001 用 bigserial 使其 NOT NULL + 序列默认，阻塞了无父 draft 的创建。
--     移除序列默认并 DROP NOT NULL（不删 FK）；序列对象由 000001 拥有，此处仅解绑默认。
ALTER TABLE config_revisions
    ALTER COLUMN parent_revision_id DROP DEFAULT;
ALTER TABLE config_revisions
    ALTER COLUMN parent_revision_id DROP NOT NULL;
-- 删除遗留的无用序列（000001 创建，现在已无默认引用）。IF EXISTS 容忍跨版本差异。
DROP SEQUENCE IF EXISTS config_revisions_parent_revision_id_seq;
COMMENT ON COLUMN config_revisions.parent_revision_id IS '可选：基于哪个 revision 创建草稿（追溯链）；顶层 draft 为 NULL';

-- 2. 全局最多一个 active published（partial unique index）
--    现有多 published 历史数据（如有）会阻塞本索引创建。生产部署前需先归档多余
--    published 行至 archived（运维 runbook 步骤，不在 migration 内自动处理数据，
--    以免误改未知生产状态）。若存在冲突，CREATE INDEX 失败 → fail-closed。
CREATE UNIQUE INDEX IF NOT EXISTS config_revisions_single_published_uidx
    ON config_revisions ((1)) WHERE status = 'published';

COMMENT ON INDEX config_revisions_single_published_uidx IS '全局最多一个 active published revision';

-- 3. rollback 元数据
ALTER TABLE config_revisions
    ADD COLUMN IF NOT EXISTS source_revision_id bigint
        REFERENCES config_revisions(id),
    ADD COLUMN IF NOT EXISTS rollback_note text;

COMMENT ON COLUMN config_revisions.source_revision_id IS 'rollback 时复制来源的不可变 revision（draft/published/archived 均可）；非 rollback 为 NULL';
COMMENT ON COLUMN config_revisions.rollback_note IS 'rollback 说明，仅 rollback 产生的新 revision 携带';

-- 4. audit 扩展与收紧
ALTER TABLE config_audit_log
    ADD COLUMN IF NOT EXISTS actor_kind text,
    ADD COLUMN IF NOT EXISTS request_id text;

-- 收紧 action 枚举：新增 rollback_publish（rollback 产生的新 publish 动作）。
-- 旧 CHECK 仅含 create/update/delete/publish/archive/rollback；替换为含 rollback_publish。
ALTER TABLE config_audit_log
    DROP CONSTRAINT IF EXISTS audit_action_chk,
    ADD CONSTRAINT audit_action_chk CHECK (action IN (
        'create', 'update', 'delete', 'publish', 'archive', 'rollback', 'rollback_publish'
    ));

COMMENT ON COLUMN config_audit_log.actor_kind IS 'actor 类型（user/service/admin），不存敏感信息';
COMMENT ON COLUMN config_audit_log.request_id IS '触发写操作的可选请求 ID（不存 secret）';

CREATE INDEX IF NOT EXISTS config_audit_log_at_desc_idx ON config_audit_log (at DESC);
CREATE INDEX IF NOT EXISTS config_audit_log_action_idx ON config_audit_log (action);

-- 5. secret 边界（expand）: credential_ref 恢复为 NOT NULL，强制 secret-free 引用。
--    api_key 列保留（nullable）仅供历史数据识别，应用层禁止写入明文。
--    若历史行存在 credential_ref IS NULL，本 ALTER 会失败 → fail-closed，提示运维
--    先用私有 runbook 补齐 vault:// ref（migration 不读取/输出任何 api_key 内容）。
--    credential_ref 是 000001 基础列，所有合法迁移链在 000004 前必然存在；
--    此 ALTER 不加 IF EXISTS 守卫以保持 fail-closed 语义（列缺失说明链被篡改）。
ALTER TABLE upstream_credentials
    ALTER COLUMN credential_ref SET NOT NULL;

-- credential_ref 应唯一（同一 vault ref 不应被多账号复用），但历史可能有空串/重复，
-- 故仅在非空值上建 partial unique index，不阻塞历史 NULL（已被 NOT NULL 排除）。
CREATE UNIQUE INDEX IF NOT EXISTS upstream_credentials_ref_uidx
    ON upstream_credentials (credential_ref) WHERE status <> 'deleted';

COMMENT ON COLUMN upstream_credentials.credential_ref IS 'opaque vault:// 凭据引用，secret-free，唯一事实来源；明文 api_key 不再写入';

-- api_key 列可能不存在于被跳过 000002 的非标准迁移链中。仅在列存在时安全加 COMMENT，
-- 不泄露数据、不假定列存在（兼容 DO block）。
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'upstream_credentials'
          AND column_name = 'api_key'
    ) THEN
        COMMENT ON COLUMN upstream_credentials.api_key IS '历史明文列，仅用于识别遗留数据；新写入由应用层拒绝，迁移不外迁/输出其内容';
    END IF;
END $$;
