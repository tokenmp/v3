# TokenMP v3 Docker 规范

## 资源命名

所有 TokenMP v3 Docker 资源必须包含明确的 `tokenmp-v3` 标识，避免与历史版本或其他项目混用。

- Compose project：固定为 `tokenmp-v3`。
- Compose 文件应设置顶层项目名：

  ```yaml
  name: tokenmp-v3
  ```

- 如果命令行指定项目名，只能使用：

  ```bash
  docker compose -p tokenmp-v3 build
  docker compose -p tokenmp-v3 up -d
  ```

  是否需要 sudo 由目标环境的权限规则决定。

- 镜像名：`tokenmp-v3-<service>:<tag>`，例如 `tokenmp-v3-auth:latest`。
- 容器名：优先使用 Compose 自动生成的 `tokenmp-v3-<service>-<index>`。
- 自定义网络名：`tokenmp-v3-<purpose>`。
- 命名卷：`tokenmp-v3-<purpose>`。
- 不得构建或启动 `tokenmp-auth`、`tokenmp-api`、`tokenmp-executor` 等缺少 v3 标识的新资源。
- 不要设置缺少 v3 标识的 `container_name`。

## 构建与部署

- Monorepo 中每个可部署服务使用独立镜像。
- 禁止制作包含所有服务的单一超级镜像。
- 构建前核对 Compose project、服务名、镜像名和 build context。
- 不得用模糊名称或跨项目批量命令执行构建、部署和清理。
- 不得因为使用 Monorepo 而把全部源码无条件复制到每个镜像。
- 每个服务应有独立健康检查，并验证实际服务能力而不只是进程存在。
- 私有服务器、端口、SSH、部署路径和运行状态由本地私有文档提供，不应写入公开规则。

## 清理约束

- 清理前核对 Compose project、working directory、mount、network 和 volume。
- 未确认数据归属及备份前，不使用 `docker compose down -v`、`docker volume rm` 或等价破坏性命令。
- 公共数据库、缓存、代理和可观测性组件不得作为单个应用的附属资源清理。
- 镜像、容器、网络和卷应按完整 v3 标识精确选择，禁止通过宽泛 grep 结果直接批量删除。

## 验证

Docker 相关变更至少验证：

```bash
docker compose -p tokenmp-v3 config
docker compose -p tokenmp-v3 build
docker compose -p tokenmp-v3 ps
```

并检查实际资源标签：

- `com.docker.compose.project=tokenmp-v3`
- service 与预期一致
- working directory 与目标环境一致
- 镜像名称带 `tokenmp-v3` 标识
- 端口、网络和挂载没有与其他项目冲突
