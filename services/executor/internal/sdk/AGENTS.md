# Executor SDK boundary

> 作用域：`services/executor/internal/sdk/`。继承仓库根目录、`services/` 与 `services/executor/AGENTS.md`。

## 模块职责

- 负责：provider adapter 的内部 shared completion/stream boundary（completion/stream capability 独立）、safe call/opening metadata、opaque credential capability 与 classified errors。Phase 8.1 已实施 shared `StreamClient`、`StreamSource` 与 `StreamEvent`：event 只含 monotonic `Sequence`、safe `streaming.Event` metadata 与 adapter-owned canonical JSON `Data`；`sdk.MaxStreamEventDataBytes` 固定为 256 KiB，OpenAI parser/SSE observer 与其对齐；普通格式化不得泄露 payload、provider fields、URL、请求或 credential。
- 已实施：official OpenAI Chat `NewStreaming` internal adapter；每次仅开一个 stream，retry=0、禁止 redirect、per-call 唯一 Bearer auth，返回仅安全的 2xx status/request-ID opening metadata。adapter 严格解析/classify chunk，bounded no-raw SSE observer 证明恰好一个 terminal `[DONE]`；`Close` 幂等并与 cancellation 一起释放 in-flight read。
- 已实施内部 `execution` payload source/sink 边界：以 sequence-indexed owned payload 暂存配对 metadata，最多 35 × 256 KiB；它不含 Driver。
- 不负责：stream-driver orchestration、SSE downstream rendering、HTTP transport/composition 或公开运行时 stream。`AttemptSession.ExecuteStream` 仅提供单 attempt 的 scoped-secret opening 前置，仍不实施 retry/quota driver；schema-valid `stream:true` 仍返回 501；不声称 HTTP atomicity、wire-attempt proof 或 public/provider E2E。

## 开发与验证

```bash
cd services/executor
gofmt -l internal/sdk/
go test -race -count=1 ./internal/sdk/...
go vet ./internal/sdk/...
```

现有 `go-auth` CI job 已以 `./internal/sdk/...` 运行该包及 adapter 的 race tests；无需增加新的 CI package pattern。任何新增 fuzz target 仅本地按需执行，不加入 CI。

## DO NOT

- **DO NOT** 将 raw SSE frame、请求、response body、URL 或 credential 放入 `StreamEvent.Meta`、日志或普通格式化输出。
- **DO NOT** 在 SDK adapter 启用 retry 或 redirect，或让非 per-call provider auth 覆盖唯一认证 header。
- **DO NOT** 将 OpenAI internal stream adapter 写成 public/provider E2E，或写成已连接 `AttemptSession`、retry/quota、transport 或 composition。
- **DO NOT** 接受 EOF 作为成功完成；OpenAI adapter 只接受 bounded observer 证明的精确单个 `[DONE]`。

## 文档维护触发器

stream port、opening/terminal error、secret lifetime、provider adapter、CI coverage 或 runtime consumer 状态变化时，同步更新本文件、`services/executor/AGENTS.md`、`README.md` 与 `docs/executor/{architecture,testing-strategy}.md`。
