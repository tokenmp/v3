# Git 开发规范

## 1. 基本原则

- 禁止直接在 `main` 分支修改、创建或删除文件。
- `main` 分支只用于执行 `git pull`，同步远端最新代码。
- 所有开发、修复、重构、文档和配置改动都必须在独立工作分支完成。
- 一个分支只处理一个明确任务，避免混入无关改动。
- 未经用户明确要求，不得自动提交、推送、合并、变基或删除分支。
- 禁止提交密钥、令牌、密码、私有证书、生产数据及其他敏感信息。

## 2. 开始开发

1. 检查工作区和当前分支：

   ```bash
   git status --short
   git branch --show-current
   ```

2. 如果当前位于 `main`，且工作区干净，先同步远端：

   ```bash
   git pull --ff-only
   ```

3. 从最新的 `main` 创建工作分支：

   ```bash
   git switch -c <type>/<short-description>
   ```

4. 再次确认当前分支不是 `main`，然后才能修改文件。

如果在 `main` 上发现未提交改动，不得继续修改或直接提交；应先保留现场，并在不丢失改动的前提下切换或创建工作分支。

## 3. 分支命名

格式：`<type>/<short-description>`。

常用类型：

- `feat/`：新功能
- `fix/`：缺陷修复
- `refactor/`：不改变外部行为的重构
- `perf/`：性能优化
- `test/`：测试相关
- `docs/`：文档改动
- `build/`：构建系统或依赖改动
- `ci/`：持续集成配置
- `chore/`：维护性任务
- `hotfix/`：需要紧急发布的生产修复

命名要求：

- 使用小写英文和连字符，例如 `feat/quota-reservation`。
- 简短、明确地描述任务，不使用 `test`、`temp`、`new` 等模糊名称。
- 不在同一分支混合多个不相关任务。

## 4. 开发过程

- 修改前先阅读相关代码、项目文档和规则文件。
- 定期执行 `git status` 和 `git diff`，确认改动范围符合预期。
- 不随意覆盖、回退或删除用户已有改动。
- 不使用 `git reset --hard`、`git clean -fd`、强制推送等破坏性命令，除非用户明确授权。
- 不直接修改生成文件；如果生成文件必须入库，应通过项目规定的生成命令更新。
- 遇到与当前任务无关的问题，不顺手大范围修改；应单独记录或另建分支处理。

## 5. 提交前检查

提交前必须：

1. 查看状态及完整差异：

   ```bash
   git status --short
   git diff
   git diff --staged
   ```

2. 确认没有误提交：
   - 密钥、密码、令牌和证书
   - `.env` 等本地配置
   - 日志、缓存、构建产物和临时文件
   - 数据库导出、生产数据或用户数据
   - 与当前任务无关的改动

3. 运行与改动相关的格式化、静态检查、类型检查和测试。
4. 确认测试通过；如果无法运行或存在失败，必须明确说明原因及影响。
5. 按 `.agents/documentation.md` 检查文档影响，搜索旧名称、旧路径、旧分支和旧状态；确保文档描述的是提交合并后的状态。
6. 只暂存本次提交需要的文件或代码块，避免无条件使用 `git add .`。

推荐：

```bash
git add <files>
git diff --staged
git status --short
```

## 6. 提交规范

提交信息采用 Conventional Commits：

```text
<type>(<scope>): <subject>
```

`scope` 可省略。常用 `type`：

- `feat`：新功能
- `fix`：缺陷修复
- `refactor`：重构
- `perf`：性能优化
- `test`：测试
- `docs`：文档
- `style`：仅格式调整，不改变逻辑
- `build`：构建系统或依赖
- `ci`：持续集成
- `chore`：维护任务
- `revert`：回退提交

示例：

```text
feat(auth): add refresh token rotation
fix(quota): prevent duplicate reservation
refactor(executor): simplify upstream routing
docs: document local development workflow
```

提交信息要求：

- 使用祈使句，简洁描述“本提交做了什么”。
- 标题应具体，避免 `update`、`fix bug`、`changes` 等无意义描述。
- 一个提交只表达一个逻辑变更，并尽量保持可独立审查、测试和回退。
- 不把格式化、无关重构和功能变更混在同一提交中。
- 复杂改动可在正文说明原因、方案、影响和迁移方式。
- 破坏性变更使用 `!` 或 `BREAKING CHANGE:` 明确标记。
- 除非用户明确要求，不使用 `git commit --amend` 改写已共享的提交历史。

## 7. 同步远端变更

- `main` 使用 `git pull --ff-only`，避免产生意外合并提交。
- 工作分支需要同步最新 `main` 时，应先获取远端状态：

  ```bash
  git fetch origin
  ```

- 采用 merge 还是 rebase，应遵循仓库现有约定；没有明确约定时，操作前向用户确认。
- 执行 rebase、解决冲突或改写历史前，确认工作区干净并告知用户潜在影响。
- 解决冲突时理解双方改动，不得机械选择 `ours` 或 `theirs`。
- 同步后重新运行相关检查和测试。

## 8. 推送规范

- 首次推送工作分支：

  ```bash
  git push -u origin <branch-name>
  ```

- 推送前确认分支名、提交记录、测试结果和远端目标正确。
- 不直接推送到 `main`。
- 禁止对共享分支使用 `git push --force`。
- 如确需更新个人分支的变基历史，应在用户明确授权后使用 `git push --force-with-lease`，不得使用裸 `--force`。
- 未经用户明确要求，不得自动推送代码。

## 9. Pull Request / Merge Request

PR/MR 应保持范围单一，并包含：

- 改动目的和背景
- 主要实现内容
- 测试方式与结果
- 配置、数据库或部署影响
- 风险及回滚方式
- 关联 issue（如有）
- UI 改动的截图或录屏（如适用）

合并前必须：

- 通过必要的 CI、测试和代码审查。
- 处理所有阻塞性评审意见。
- 确认不存在敏感信息和无关文件。
- 确认数据库迁移向前、向后兼容及回滚策略。
- 根据仓库约定选择 squash、merge 或 rebase merge，不擅自改变合并策略。

## 10. 合并后的处理

合并由用户或仓库既定自动化流程执行。合并完成后，必须按 `.agents/documentation.md` 检查并更新阶段状态、分支相关措辞和实施结果，然后可按以下顺序更新本地环境：

```bash
git switch main
git pull --ff-only
git branch -d <branch-name>
```

- 只有确认工作已合并且不再需要后，才能删除本地分支。
- 删除远端分支前必须确认不再被其他人使用。
- 不使用 `git branch -D` 强制删除尚未合并的分支，除非用户明确授权。

## 11. 紧急修复

- 从最新 `main` 创建 `hotfix/<short-description>` 分支。
- 只包含解决生产问题所需的最小改动。
- 即使是紧急修复，也必须进行必要测试并提供回滚方案。
- 不以紧急为由直接在 `main` 修改或提交。

## 12. Agent 执行边界

Agent 在执行 Git 操作时必须：

- 修改文件前检查当前分支；位于 `main` 时先创建工作分支。
- 提交前展示或核对待提交范围，并报告测试结果。
- 只有用户明确要求“提交”时才执行 `git commit`。
- 只有用户明确要求“推送”时才执行 `git push`。
- 只有用户明确要求并确认策略时才执行 merge、rebase、cherry-pick、revert、历史改写或分支删除。
- 遇到脏工作区、冲突、未知改动或可能丢失数据的情况时，停止操作并向用户说明。
