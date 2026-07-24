# Infra 分区

> 作用域：`infra/`。继承仓库根目录 `AGENTS.md`，并遵循 `.agents/docker.md` 与 `.agents/operations.md`。

## 分区职责

`infra/` 用于可提交的基础设施定义，例如 Docker、Compose、代理、可观测性和部署模板。

## 已实施模块

- **DB Schema 迁移**：`infra/db/AGENTS.md`（三库物理分离的中立基建层。Config DB 初始化迁移 `migrations/config/0001_init.sql` 已实施：providers/models/route_mappings/adapters/config_revisions 等 15 张表，含 is_default 部分唯一索引、FK VALID、软删除 status、updated_at 触发器，经 PostgreSQL 17/18 语法与约束校验。Log DB 与 Billing DB 迁移待生成。）

## 新增模块准入

新增基础设施内容前必须确认：

- 目标环境和资源所有权。
- 服务、端口、网络、卷、配置和健康检查。
- 敏感信息注入方式。
- 验证、迁移、发布和回滚流程。
- 是否影响公共基础设施或其他项目。
- README 和需要的模块级 `AGENTS.md`。

## 边界

- 公开仓库只保存模板、schema 和非敏感默认值。
- 私有服务器、SSH、部署路径和实时拓扑从可选 `.agents/local.md` 获取。
- TokenMP v3 Docker 资源统一使用 `tokenmp-v3` 标识。
- 基础设施定义不得隐式拥有业务数据。

## 开发与验证

每种基础设施必须记录可复现的静态校验、渲染检查或 dry-run。Docker/Compose 变更按 `.agents/docker.md` 验证。

## DO NOT

- **DO NOT** 提交 IP、密码、令牌、连接串或生产数据——使用本地私有上下文和安全注入。
- **DO NOT** 在未确认挂载和备份前删除卷或数据——先核对所有权和回滚。
- **DO NOT** 用模糊名称批量清理容器、网络或镜像——按完整项目标签精确操作。
- **DO NOT** 将公共数据库、缓存或代理当作单一服务附属资源处理。

## 文档维护

基础设施角色、命名、部署或回滚方式变化时，同步维护本文件、相关模块文档及公开/私有边界。
