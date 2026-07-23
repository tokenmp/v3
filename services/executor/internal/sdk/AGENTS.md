# Executor SDK boundary

> 作用域：`services/executor/internal/sdk/`。继承仓库根目录、`services/` 与 `services/executor/AGENTS.md`。

## 模块职责

- 负责：provider adapter 的内部 shared completion/stream boundary（completion/stream capability 独立）、safe call/opening metadata、opaque credential capability 与 classified errors。Phase 8.1 已实施 shared `StreamClient`、`StreamSource` 与 `StreamEvent`：event 只含 monotonic `Sequence`、safe `streaming.Event` metadata 与 adapter-owned canonical JSON `Data`；`sdk.MaxStreamEventDataBytes` 固定为 256 KiB，OpenAI parser/SSE observer 与其对齐；普通格式化不得泄露 payload、provider fields、URL、请求或 credential。
- 已实施：official OpenAI Chat `NewStreaming` internal adapter；每次仅开一个 stream，retry=0、禁止 redirect、per-call 唯一 Bearer auth，返回仅安全的 2xx status/request-ID opening metadata。adapter 严格解析/classify chunk，bounded no-raw SSE observer 证明恰好一个 terminal `[DONE]`；`Close` 幂等并与 cancellation 一起释放 in-flight read。
- Phase 9 已实施：official `github.com/anthropics/anthropic-sdk-go` v1.58.0 Messages internal `StreamClient`。opening 强制 HTTPS target/path prefix、execution-authoritative model/thinking、`WithoutEnvironmentDefaults`、retry=0/no redirects、sole per-call `x-api-key`、fixed `anthropic-version` 与 `Accept: text/event-stream`。adapter-owned bounded SSE parser/state（而非 SDK event object）严格解析 `message_start`、text/thinking/tool delta、thinking signature、ping、native error、usage 与 `message_stop`；source sequence 单调递增，canonical payload 不超过 256 KiB。signature 仅保存在 canonical payload 供下游，不进入 meta/log/普通格式化；native error payload 为空且分类 first-wins，后续 SDK decoder error 不覆盖。
- 已实施内部 `execution` payload source/sink 边界：以 sequence-indexed owned payload 暂存配对 metadata，最多 35 × 256 KiB；它不含 Driver。
- Phase 11.1 已实施：official `github.com/openai/openai-go/v3` v3.44.0 OpenAI Images legacy `Images.Generate` 的内部 `sdk.Client.Complete` capability（`openai_images`）。每次调用使用 call-local HTTPS base URL/model/opaque secret、retry=0、禁止 redirect、唯一 Bearer auth 与既有末端 header scrubber；严格的 legacy request allowlist 不接受 GPT Image 特有参数。缺省 `response_format` 强制为并在上游 wire 显式发送 `url`。成功响应严格要求一致的 `url` 或标准 padded `b64_json` item、HTTPS URL（无 userinfo/fragment）、bounded revised prompt 与非负整数 usage；wire JSON 上限 16 MiB、单 item decoded base64 10 MiB、aggregate 12 MiB。此 capability 只保留 safe status/request-ID metadata，并通过 TLS/race/fuzz 测试；它已作为 completion-only non-stream capability 注册至 execution registry、composition 与鉴权 `/v1/images/generations` runtime，不作 usage quota，也不注册为 `StreamClient`。
- Phase 12.3 non-stream usage normalization（已实施）：`sdk.Completion` 新增 `Usage`/`Known` 字段；`sdk.Usage` 为 bounded token 计数（`PromptTokens`/`CompletionTokens`/`TotalTokens`，`Valid()` 校验 ≤ `maxSDKUsageTokens`（1e6，与 `streaming.MaxTotalHardCap` 对齐）且 `prompt+completion==total`）。OpenAI Chat adapter 经 `extractOpenAIChatUsage` 从 `RawJSON` 提取 `usage.prompt_tokens`/`completion_tokens`/`total_tokens`（三字段必须存在、非负、一致且 ≤ 1e6，否则 `Known=false`）；Anthropic Messages adapter 经 `extractAnthropicMessagesUsage` 提取 `usage.input_tokens`/`output_tokens`（计算 `total=input+output`，overflow/非负/≤ 1e6 校验，cache 字段忽略；否则 `Known=false`）。Images adapter 不提取 usage（`Known` 恒为 `false`）。Runner 经 `runnerFinalizeOutcome` 映射：`Known=true` 且 `Valid()` 时以 `AccountingConfirmedUsage` + `ConfirmedUsage{InputTokens,OutputTokens,TotalTokens}` finalize；否则回退 `AccountingUnpricedSuccess`。此路径与 StreamDriver 的 `confirmedUsage`（`streaming.UsageKnown` → `AccountingConfirmedUsage`）对称，non-stream 与 stream 共享同一 quota 终态语义。
- Phase 15 Retry-After parsing（已实施）：`sdk.ParseRetryAfter` 按 RFC 7231 §7.1.3 解析 `Retry-After` header（delta-seconds 优先，fallback HTTP-date RFC 1123），结果 clamp 至 `[0, HardMaxRetryAfter]`（5 分钟）；`ClassifiedError` 新增 `RetryAfter() (time.Duration, bool)` 与 `NewClassifiedErrorWithRetryAfter`（仅 retryable status 429/5xx 使用，非 retryable status 保持 `NewClassifiedError` 无 RetryAfter）；`CloneClassifiedError` 保留 RetryAfter。OpenAI Chat/Responses/Stream 与 Anthropic Messages/Stream adapter 的 `classifyError`/`classifyStreamOpenError` 对 retryable status 解析 `Retry-After` header 并注入 `ClassifiedError`；non-retryable status（401/403/404 等）即使 header 存在也不解析。Runner 与 StreamDriver 从 `ClassifiedError.RetryAfter()` 提取 `*time.Duration` 传入 `retry.Failure.RetryAfter`；retry State 的 `RecordFailure` 以 `max(backoff, RetryAfter)` 计算 delay，并额外 clamp 至 `sdk.HardMaxRetryAfter`，防止恶意上游施加无界延迟。`HardMaxRetryAfter` = 5 分钟是全局硬上限，不可配置。
- 不负责：stream-driver orchestration、SSE downstream rendering或 HTTP transport。Phase 10 的 composition 将本包精确 OpenAI/Anthropic `StreamClient` 注册给 `StreamDriver`，公开 Chat/Messages `stream:true` 因此已启用；本包仍不拥有 retry/quota driver 或 SSE downstream 渲染，也不声称 HTTP atomicity、wire-attempt proof 或 public/provider E2E。Images capability 不拥有 registry、composition、transport、GPT Image 特有参数或 usage quota；其 runtime 接线由上层负责，且仅 completion-only non-stream。

## 开发与验证

```bash
cd services/executor
gofmt -l internal/sdk/
go test -race -count=1 ./internal/sdk/...
go vet ./internal/sdk/...
```

现有 `go-auth` CI job 已以 `./internal/sdk/...` 运行该包及 adapter（包括 legacy OpenAI Images）的 race tests；共享 validator `./internal/imagecontract/...` 是独立 package，必须显式加入 CI race package pattern。任何新增 fuzz target 仅本地按需执行，不加入 CI。

## DO NOT

- **DO NOT** 将 raw SSE frame、请求、response body、URL 或 credential 放入 `StreamEvent.Meta`、日志或普通格式化输出。
- **DO NOT** 在 SDK adapter 启用 retry 或 redirect，或让非 per-call provider auth 覆盖唯一认证 header。
- **DO NOT** 将 OpenAI 或 Anthropic stream adapter 写成 public/provider E2E，或让 adapter 自己拥有 HTTP transport/composition；Phase 10 composition 是唯一 runtime consumer，Chat/Messages `stream:true` 已启用。
- **DO NOT** 在 adapter 外部设置 `Completion.Usage` 或 `Completion.Known`；这些字段仅供 adapter 提取逻辑填充，调用方不得直接赋值。
- **DO NOT** 对非 retryable status（401/403/404 等）解析 `Retry-After` header；只有 429 和 5xx 是 retryable status，`isRetryableHTTPStatus` 是唯一判断入口。
- **DO NOT** 修改 `HardMaxRetryAfter`（5 分钟）或使其可配置；它是全局硬上限，防止恶意上游施加无界延迟。
- **DO NOT** 接受 EOF 作为成功完成；OpenAI adapter 只接受 bounded observer 证明的精确单个 `[DONE]`。
- **DO NOT** 将 legacy Images completion-only non-stream capability 表述为 streaming capability、GPT Image 特有参数或 usage quota；`/v1/images/generations` 已鉴权真实执行，`/v1/models` 与 `/v1/responses` 已不再 501（Phase 13/14）。Images `Known` 恒为 `false`，不提取 token usage。

## 文档维护触发器

stream port、opening/terminal error、secret lifetime、provider adapter、Retry-After parsing、CI coverage 或 runtime consumer 状态变化时，同步更新本文件、`services/executor/AGENTS.md`、`README.md` 与 `docs/executor/{architecture,testing-strategy}.md`。
