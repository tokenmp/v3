# configreload

> 作用域：`services/executor/internal/configreload/`。继承 `services/executor/AGENTS.md`。

## 模块职责

- 负责：Executor 配置热重载。`Reloader.Reload(ctx)` 执行 LoadFile → Compile → validate → Publish 流水线，验证在发布前执行，失败保留旧 generation。
- 不负责：SIGHUP 信号监听、mtime 轮询、composition 组装、credential/identity 解析。

## 对外能力

| 导出 | 输入 | 返回/错误 | 稳定性 |
|---|---|---|---|
| `NewReloader` | store、path、CompiledValidator、Logger | `*Reloader` | internal |
| `Reloader.Reload` | `context.Context` | 成功 nil；revision 未变 `ErrReloadUnchanged`；失败 `ErrReloadFailed`/`ErrReloadValidationFailed` | internal |
| `CompiledValidator` | `func(ctx, *CompiledSnapshot) error` | 验证回调 | internal |
| `Logger` | `Infof`/`Errorf` | 最小日志接口 | internal |

## 安全约束

- 日志不泄 path/content/secret
- Sentinel error non-wrapping
- 验证在 publish 前，失败不修改 store

## 测试

```bash
go test -race -count=1 ./internal/configreload/...
```
