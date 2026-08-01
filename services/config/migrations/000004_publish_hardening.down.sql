-- 000004 down: 回退 publish 加固迁移（contract 回退）。
--
-- 设计原则：
--   * golang-migrate 默认把整个文件包在单个事务里执行；下方的 preflight DO
--     block 在任何破坏性 DDL 之前运行，一旦 RAISE EXCEPTION 即回滚整条 down
--     （不会留下半回退状态）。
--   * preflight 只拦截「schema 正常可达、但回退后的旧 schema 无法表达」的
--     数据：parentless draft（旧 schema 的 parent_revision_id 为 bigserial
--     NOT NULL）、CAS 版本历史（version>1）、rollback provenance
--     （source_revision_id/rollback_note）、audit 新增 action
--     （rollback_publish）与 audit 新增元数据（actor_kind/request_id）。
--   * 迁移绝不静默回填或丢弃历史；操作者须按私有 runbook 把这些数据转成旧
--     schema 可表达的形式后再重试 down。
--   * 所有破坏性 DDL（DROP INDEX / DROP COLUMN）排在 preflight 之后，并用 DO
--     block 守卫表/列存在，使 down 在干净库上幂等。

-- ===========================================================================
-- Preflight guards（全部前置，破坏性 DDL 之前）
-- ===========================================================================
DO $$
DECLARE
    n int;
    has_version       bool;
    has_rollback_meta bool;
    has_audit_meta    bool;
BEGIN
    IF to_regclass('public.config_revisions') IS NOT NULL THEN
        -- Guard 1: parentless drafts. 000001 定义 parent_revision_id 为 bigserial
        -- (NOT NULL + 序列默认)，NULL 父 revision 在旧 schema 无法表达。
        EXECUTE 'SELECT count(*) FROM config_revisions WHERE parent_revision_id IS NULL' INTO n;
        IF n > 0 THEN
            RAISE EXCEPTION 'down 000004 aborted: % row(s) in config_revisions have parent_revision_id IS NULL (parentless draft); the pre-000004 schema (bigserial NOT NULL) cannot express this. Convert or remove these rows via the private runbook before retrying.', n
                USING ERRCODE = 'check_violation';
        END IF;

        -- Guard 2: CAS version history. version 列由 000004 新增；version > 1
        -- 表示该 draft 已被乐观并发更新过，删除列会丢失该历史。version = 1 是
        -- 默认值，删除不丢数据。
        SELECT EXISTS (
            SELECT 1 FROM information_schema.columns
            WHERE table_schema = 'public' AND table_name = 'config_revisions' AND column_name = 'version'
        ) INTO has_version;
        IF has_version THEN
            EXECUTE 'SELECT count(*) FROM config_revisions WHERE version IS NOT NULL AND version <> 1' INTO n;
            IF n > 0 THEN
                RAISE EXCEPTION 'down 000004 aborted: % row(s) in config_revisions have version <> 1 (CAS-updated); dropping the version column would lose this history. Finalize or convert these drafts before retrying.', n
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;

        -- Guard 3: rollback provenance. source_revision_id / rollback_note 由
        -- 000004 新增；非空值表示 rollback 来源/说明，删除列会丢失溯源历史。
        SELECT EXISTS (
            SELECT 1 FROM information_schema.columns
            WHERE table_schema = 'public' AND table_name = 'config_revisions' AND column_name = 'source_revision_id'
        ) INTO has_rollback_meta;
        IF has_rollback_meta THEN
            EXECUTE $q$SELECT count(*) FROM config_revisions WHERE source_revision_id IS NOT NULL OR (rollback_note IS NOT NULL AND rollback_note <> '')$q$ INTO n;
            IF n > 0 THEN
                RAISE EXCEPTION 'down 000004 aborted: % row(s) in config_revisions carry rollback provenance (source_revision_id / rollback_note); the pre-000004 schema cannot express this. Convert or remove these rows before retrying.', n
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;
    END IF;

    IF to_regclass('public.config_audit_log') IS NOT NULL THEN
        -- Guard 4: rollback_publish action. 000004 把 action 枚举收紧并新增
        -- 'rollback_publish'；旧枚举（000001）不含该值，回退约束后这类行无法
        -- 表达。
        EXECUTE $q$SELECT count(*) FROM config_audit_log WHERE action = 'rollback_publish'$q$ INTO n;
        IF n > 0 THEN
            RAISE EXCEPTION 'down 000004 aborted: % audit row(s) use action=''rollback_publish''; the pre-000004 action enum cannot express this. Convert or remove these rows before retrying.', n
                USING ERRCODE = 'check_violation';
        END IF;

        -- Guard 5: audit actor_kind / request_id. 两列由 000004 新增；非空值
        -- 表示服务间授权审计元数据，删除列会丢失该历史。
        SELECT EXISTS (
            SELECT 1 FROM information_schema.columns
            WHERE table_schema = 'public' AND table_name = 'config_audit_log' AND column_name = 'actor_kind'
        ) INTO has_audit_meta;
        IF has_audit_meta THEN
            EXECUTE $q$SELECT count(*) FROM config_audit_log WHERE (actor_kind IS NOT NULL AND actor_kind <> '') OR (request_id IS NOT NULL AND request_id <> '')$q$ INTO n;
            IF n > 0 THEN
                RAISE EXCEPTION 'down 000004 aborted: % audit row(s) carry actor_kind / request_id metadata; the pre-000004 schema cannot express this. Convert or remove these rows before retrying.', n
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;
    END IF;
END $$;

-- ===========================================================================
-- Destructive DDL（preflight 全部通过后才执行）
-- ===========================================================================

-- 5. secret 边界回退（放宽约束，不丢数据）
DROP INDEX IF EXISTS upstream_credentials_ref_uidx;
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'upstream_credentials'
          AND column_name = 'credential_ref'
    ) THEN
        ALTER TABLE upstream_credentials
            ALTER COLUMN credential_ref DROP NOT NULL;
    END IF;
END $$;

-- 4. audit 回退
DROP INDEX IF EXISTS config_audit_log_action_idx;
DROP INDEX IF EXISTS config_audit_log_at_desc_idx;
DO $$
BEGIN
    IF to_regclass('public.config_audit_log') IS NOT NULL THEN
        ALTER TABLE config_audit_log
            DROP CONSTRAINT IF EXISTS audit_action_chk,
            ADD CONSTRAINT audit_action_chk CHECK (action IN ('create', 'update', 'delete', 'publish', 'archive', 'rollback'));
        ALTER TABLE config_audit_log
            DROP COLUMN IF EXISTS request_id,
            DROP COLUMN IF EXISTS actor_kind;
    END IF;
END $$;

-- 3. rollback 元数据回退
DO $$
BEGIN
    IF to_regclass('public.config_revisions') IS NOT NULL THEN
        ALTER TABLE config_revisions
            DROP COLUMN IF EXISTS rollback_note,
            DROP COLUMN IF EXISTS source_revision_id;
    END IF;
END $$;

-- 2. singleton published index 回退
DROP INDEX IF EXISTS config_revisions_single_published_uidx;

-- 1. draft version 回退
DO $$
BEGIN
    IF to_regclass('public.config_revisions') IS NOT NULL THEN
        ALTER TABLE config_revisions
            DROP COLUMN IF EXISTS version;
    END IF;
END $$;

-- 1a. 回退 parent_revision_id 修正：恢复 NOT NULL + 序列默认（重建序列）。
--     仅在列存在时执行。注意：恢复后无父 draft 会再次被阻塞，这是 000001
--     的原始语义；preflight 已保证此时不存在 parent_revision_id IS NULL 的行。
DO $$
BEGIN
    IF to_regclass('public.config_revisions') IS NOT NULL THEN
        IF NOT EXISTS (
            SELECT 1 FROM pg_sequences
            WHERE schemaname='public' AND sequencename='config_revisions_parent_revision_id_seq'
        ) THEN
            CREATE SEQUENCE config_revisions_parent_revision_id_seq;
        END IF;
        ALTER TABLE config_revisions
            ALTER COLUMN parent_revision_id SET DEFAULT nextval('config_revisions_parent_revision_id_seq'::regclass);
        ALTER TABLE config_revisions
            ALTER COLUMN parent_revision_id SET NOT NULL;
    END IF;
END $$;
