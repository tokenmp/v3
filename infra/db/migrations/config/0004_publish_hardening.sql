-- TokenMP V3 Config DB — publish path hardening (draft CAS, singleton
-- published, rollback metadata, audit constraints, secret boundary).
--
-- Expand/contract migration. Does not modify history. Does not read or output
-- any secret (api_key) content. If historical plaintext rows or multiple
-- published revisions exist, index/constraint creation fails closed — operators
-- resolve via private runbook before applying.
-- Also fixes a 0001 legacy schema defect: config_revisions.parent_revision_id
-- was bigserial (NOT NULL + sequence default) but is an optional self-reference;
-- this migration drops NOT NULL and the sequence default.

BEGIN;

-- 1. draft optimistic concurrency version
ALTER TABLE config_revisions
    ADD COLUMN IF NOT EXISTS version int NOT NULL DEFAULT 1;

COMMENT ON COLUMN config_revisions.version IS 'draft 乐观并发版本号，每次 CAS update 自增；published/archived 后不可变';

-- 1a. Fix 0001 legacy defect: parent_revision_id should be an optional
--     self-reference. 0001 used bigserial (NOT NULL + sequence default),
--     blocking parentless draft creation. Drop the default and NOT NULL
--     (FK retained); the sequence is 0001-owned, here only unbound.
ALTER TABLE config_revisions
    ALTER COLUMN parent_revision_id DROP DEFAULT;
ALTER TABLE config_revisions
    ALTER COLUMN parent_revision_id DROP NOT NULL;
DROP SEQUENCE IF EXISTS config_revisions_parent_revision_id_seq;
COMMENT ON COLUMN config_revisions.parent_revision_id IS '可选：基于哪个 revision 创建草稿（追溯链）；顶层 draft 为 NULL';

-- 2. global at-most-one active published (partial unique index)
CREATE UNIQUE INDEX IF NOT EXISTS config_revisions_single_published_uidx
    ON config_revisions ((1)) WHERE status = 'published';

COMMENT ON INDEX config_revisions_single_published_uidx IS '全局最多一个 active published revision';

-- 3. rollback metadata
ALTER TABLE config_revisions
    ADD COLUMN IF NOT EXISTS source_revision_id bigint
        REFERENCES config_revisions(id),
    ADD COLUMN IF NOT EXISTS rollback_note text;

COMMENT ON COLUMN config_revisions.source_revision_id IS 'rollback 时复制来源的不可变 revision（draft/published/archived 均可）；非 rollback 为 NULL';
COMMENT ON COLUMN config_revisions.rollback_note IS 'rollback 说明，仅 rollback 产生的新 revision 携带';

-- 4. audit extension + tightened action enum
ALTER TABLE config_audit_log
    ADD COLUMN IF NOT EXISTS actor_kind text,
    ADD COLUMN IF NOT EXISTS request_id text;

ALTER TABLE config_audit_log
    DROP CONSTRAINT IF EXISTS audit_action_chk,
    ADD CONSTRAINT audit_action_chk CHECK (action IN (
        'create', 'update', 'delete', 'publish', 'archive', 'rollback', 'rollback_publish'
    ));

COMMENT ON COLUMN config_audit_log.actor_kind IS 'actor 类型（user/service/admin），不存敏感信息';
COMMENT ON COLUMN config_audit_log.request_id IS '触发写操作的可选请求 ID（不存 secret）';

CREATE INDEX IF NOT EXISTS config_audit_log_at_desc_idx ON config_audit_log (at DESC);
CREATE INDEX IF NOT EXISTS config_audit_log_action_idx ON config_audit_log (action);

-- 5. secret boundary (expand): credential_ref back to NOT NULL (secret-free).
--    api_key retained nullable for legacy identification only; app layer
--    rejects new plaintext writes. Migration does not migrate/output api_key.
--    credential_ref is a base column from 0001; this ALTER stays fail-closed
--    (no IF EXISTS) so a tampered chain is surfaced.
ALTER TABLE upstream_credentials
    ALTER COLUMN credential_ref SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS upstream_credentials_ref_uidx
    ON upstream_credentials (credential_ref) WHERE status <> 'deleted';

COMMENT ON COLUMN upstream_credentials.credential_ref IS 'opaque vault:// 凭据引用，secret-free，唯一事实来源；明文 api_key 不再写入';

-- api_key may be absent in non-standard chains that skipped 0002. Guard the
-- COMMENT with a DO block; never reads/outputs api_key content.
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

COMMIT;
