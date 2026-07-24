# internal/protocolconvert

> 作用域：`services/executor/internal/protocolconvert`。本文件继承从仓库根目录到本目录之间的所有 `AGENTS.md`。

## 模块职责

- 负责：OpenAI Chat、Anthropic Messages、OpenAI Responses 三者之间的双向纯转换（请求、非流式响应、流式 chunk/事件）。Chat↔Messages 覆盖 `reasoning_content`↔thinking block（thinking 在 text 前且互斥）、`image_url`↔image block（base64 data URI 与 URL source）、严格 tool name sanitize/响应还原；Chat↔Responses 直接转换（含 custom-tool wrapper）；Messages↔Responses 通过既有 Chat leg 组合。流结束时可合成缺失的协议原生终态事件。transport-neutral 纯 JSON→JSON，不含 secret/credential/HTTP。StreamState 由调用方持有。
- 不负责：Runner 接线、credential 解析、HTTP framing、Images 协议；流式 image delta 转换。
- 所有者：TokenMP

## 必读文档

- 模块说明：本文件
- 上游协议形状：`internal/sdk/openaiadapter/params.go`、`internal/sdk/anthropicadapter/params.go`
- Protocol 枚举：`internal/adapter/types.go`
- 流式事件形状：`internal/streaming/types.go`

## 对外能力与返回契约

| 能力/导出 | 输入与前置条件 | 返回/错误/副作用 | 稳定性 | 契约来源 |
|---|---|---|---|---|
| `ConvertRequest` | 有效 JSON body + fromProtocol + toProtocol（Chat↔Messages、Chat↔Responses、Messages↔Responses） | 转换后 JSON body 或 `ErrUnsupportedConversion`/`ErrInvalidRequest` | internal | `convert.go`、`responses.go` |
| `ConvertResponse` | 有效 JSON body + fromProtocol + toProtocol（同上） | 转换后 JSON body 或 `ErrUnsupportedConversion`/`ErrInvalidResponse` | internal | `convert.go`、`responses.go` |
| `ConvertStreamChunk` | raw SSE data payload + fromProtocol + toProtocol + `*StreamState` | `[][]byte`（零或多个转换单个 chunk 后的事件 JSON）或错误 sentinel；覆盖 Chat↔Messages 与 Responses 组合流 | internal | `convert.go`、`responses_stream.go` |
| `FinalizeStream` | from/to protocol + per-stream `*StreamState`，仅在正常 EOF 调用一次 | 对尚未显式终态的转换流合成协议原生终态事件；已有终态或重复调用返回空结果 | internal | `convert.go`、`responses_stream.go` |
| `StreamState` | 调用方持有，per-stream | 保存流式转换、终态合成及 tool name 还原所需状态；无 I/O | internal | `convert.go` |

## 依赖关系与消费者

| 方向 | 模块/资源 | 使用功能 | 依赖方式 | 契约/入口 | 变更后验证 |
|---|---|---|---|---|---|
| 依赖 | `internal/adapter` | `Protocol` 枚举 | Go import | `adapter/types.go` | `go test ./internal/protocolconvert/...` |
| 被依赖 | （未来）Runner/wiring 层 | 跨协议转换能力 | import | `protocolconvert.ConvertRequest/ConvertResponse/ConvertStreamChunk` | `go test` |

## 开发与验证

```bash
gofmt -l internal/protocolconvert/
go vet ./internal/protocolconvert/
go test ./internal/protocolconvert/ -count=1 -race
```

- 最小验证：gofmt 干净 + go vet + race test
- 契约测试：请求/响应/流式 round-trip 一致性
- 集成测试：无（纯函数，无 I/O）

## 模块边界

- 允许访问：`internal/adapter`（Protocol 枚举）、标准库 `encoding/json`
- 禁止访问：HTTP transport、credential/secret、SDK 客户端、streaming Sink/Source、runtime config、其他服务
- 数据所有权：无（纯转换，不持有数据）
- 配置和环境变量：无

## DO NOT

- **DO NOT** 引入新外部依赖 — 正确做法：仅用标准库 `encoding/json`
- **DO NOT** 暴露 secret 或敏感信息 — 正确做法：转换仅操作 JSON 结构，不接触 credential/URL/header
- **DO NOT** 使用全局状态 — 正确做法：StreamState 由调用方持有
- **DO NOT** 支持 Images 协议 — 正确做法：返回 `ErrUnsupportedConversion`；流式 image delta 亦不支持。
- **DO NOT** 把 Messages↔Responses 写成无损直连转换 — 正确做法：经 Chat 中间形态组合，专有字段可能降级。
- **DO NOT** 将 protocolconvert 的 effort↔budget 映射当作执行权威值 — 正确做法：它仅提供保守默认值，精确值由运行时 `EffectiveThinking` 决定。
- **DO NOT** 绕过 Responses 流生命周期或 EOF finalizer — 正确做法：Responses 输入流必须先出现一次 `response.created`，再接受 output/terminal event；每个流使用独立 `StreamState`，正常 EOF 经 `FinalizeStream` 至多合成一次终态。

## 已知陷阱与历史教训

### tool_choice "none" 映射不对称

- 症状：OpenAI `tool_choice: "none"` 无 Anthropic 精确对应
- 根因：Anthropic 无 `type: "none"`；当前映射为 `type: "auto"`（最接近）
- DO：文档化此 trade-off
- DO NOT：假设 `none` → `auto` 语义完全等价
- 验证：`TestConvertRequest_OpenAIToAnthropic_ToolChoiceRequired`
- 证据：`convert.go:convertOpenAIToolChoiceToAnthropic`
- 适用范围：所有 OpenAI→Anthropic tool_choice 转换

### 流式转换有状态

- 症状：StreamState 跨 chunk 必须是同一实例
- 根因：Anthropic 流式事件序列依赖前序状态（message_start → content_block_start → delta → stop → message_delta → message_stop）
- DO：每个流使用独立 StreamState
- DO NOT：跨流复用 StreamState 或传 nil
- 验证：`TestConvertStreamChunk_NilState`
- 证据：`convert.go:ConvertStreamChunk`
- 适用范围：所有流式转换

### Chat↔Messages effort/budget 是保守默认

- 症状：跨协议请求的 `reasoning_effort` 与 thinking `budget_tokens` 不存在精确一一对应。
- 根因：两个协议的表达粒度不同；转换层不拥有模型选择和最终执行策略。
- DO：仅将 protocolconvert 映射作为保守默认，并由运行时 `EffectiveThinking` 覆盖精确有效值。
- DO NOT：将转换层输出视为模型最终 thinking 配置。
- 验证：`thinking_test.go` 中的请求转换测试。
- 适用范围：所有 Chat↔Messages thinking 请求转换。

### Anthropic max_tokens 必填

- 症状：OpenAI 可省略 max_tokens，Anthropic 不可
- 根因：Anthropic API 要求 max_tokens；转换层缺失时给默认值 4096
- DO：OpenAI→Anthropic 转换时确保 max_tokens 存在
- DO NOT：假设 max_tokens 可选
- 验证：`TestConvertRequest_OpenAIToAnthropic_PlainText`（验证默认 4096）
- 证据：`convert.go:defaultMaxTokens`
- 适用范围：所有 OpenAI→Anthropic 请求转换

## 文档维护触发器

出现以下变化时同步更新本文件：

- 公开导出、返回结构、错误语义或副作用变化。
- 新增、删除或改变直接依赖及消费者功能。
- 新增或删除协议转换组合，或改变 Responses custom-tool wrapper/流生命周期规则。
- 发现并确认新的非显然陷阱，或旧陷阱已不再适用。
