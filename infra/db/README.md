# TokenMP V3 数据库 Schema 迁移

三库物理分离的中立基建层。详见 [`AGENTS.md`](./AGENTS.md)。

## 目录结构

```
infra/db/
├── AGENTS.md                      # 模块文档
├── README.md                      # 本文件
└── migrations/
    ├── config/                    # Config DB
    │   └── 0001_init.sql          # 初始化（15 张表）
    ├── log/                       # Log DB（按天分区）
    │   └── 0001_init.sql          # 待生成
    └── billing/                   # Billing DB
        └── 0001_init.sql          # 待生成
```

## 三库职责

| 库 | 职责 | 关键表 |
|----|------|--------|
| **Config DB** | provider/model/route/credential/adapter 配置，带版本，编译成 ConfigSnapshot 下发 executor | providers, models, route_mappings, adapters, config_revisions |
| **Log DB** | 请求生命周期事件，不存明文，按天分区 + 自动清理 | request_logs, request_attempts, request_log_events |
| **Billing DB** | 套餐/配额/记账，executor 不直连 | plans, users, user_plans, quota_reservations, usage_ledger |

设计依据：`docs/v3-db-schema-draft.md`（本地草案）、`docs/v3-layered-architecture.md`、`docs/legacy-db-recon.md`（本地）。

## 如何 apply

```bash
# 启动临时 PostgreSQL 17/18 实例校验（不依赖线上）
initdb -D /tmp/pg-test -A trust --no-locale
pg_ctl -D /tmp/pg-test -o "-p 55432 -k /tmp" start

# 执行迁移（遇错即停）
psql -p 55432 -h /tmp -d postgres -v ON_ERROR_STOP=1 -f infra/db/migrations/config/0001_init.sql
psql -p 55432 -h /tmp -d postgres -v ON_ERROR_STOP=1 -f infra/db/migrations/log/0001_init.sql
psql -p 55432 -h /tmp -d postgres -v ON_ERROR_STOP=1 -f infra/db/migrations/billing/0001_init.sql
```

生产部署由 Config/Logging/Billing 各 service 的部署流程 apply，不在本目录处理连接串。

## 回滚

暂无 `down` migration。配置回滚靠 `config_revisions` 版本链：

1. 当前 published revision 编译出的 snapshot 有问题 → 把目标 revision 重新 publish（或基于它新建 draft）。
2. `config_audit_log` 记录全部变更，可追溯。
3. schema 层级回滚（加列/改约束）通过新迁移文件 `0002_*.sql` 处理，不修改已发布文件。

## 约束

- 不提交密钥/连接串/生产数据；凭据只存 `vault://` ref。
- FK 强制 VALID，软删除用 `status`，命名统一 snake_case。
- 字段名与 V3 `ConfigSnapshot` 编译目标对齐。

详见 [`AGENTS.md`](./AGENTS.md)。
