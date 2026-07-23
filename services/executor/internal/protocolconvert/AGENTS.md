# internal/protocolconvert

> 作用域：`services/executor/internal/protocolconvert`。本文件继承从仓库根目录到本目录之间的所有 `AGENTS.md`。

## 模块职责

- 负责：OpenAI Chat ↔ Anthropic Messages 协议的双向纯转换（请求、非流式响应、流式 chunk/事件）。transport-neutral 纯函数，不含 secret/credential!credential/HTTP，纯 JSON→JSON。StreamState 由调用方持有。
- 不负责：Runner 接线、credential 解析、HTTP framing、thinking/reasoning 转换（本轮跳过，保留字段不转）、Responses/Images 协议。
- 所有者：TokenMP

## 必读文档

- 模块说明：本文件
- 上游协议形状：`internal/sdk/openaiadapter/params.go`、`internal/sdk/anthropicadapter/params.go`
- Protocol 枚举：`internal/adapter/types.go`
- 流式事件形状：`internal/streaming/types.go`

## 对外能力与返回契约

| 能力/导出 | 输入与前置条件 | 返回/错误/副作用 | 稳定性 | 契约来源 |
|---|---|---|---|---|
| `ConvertRequest` | 有效 JSON body + fromProtocol + toProtocol（仅 OpenAI Chat ↔ Anthropic Messages） | 转换后 JSON body 或 `ErrUnsupportedConversion`/`ErrInvalidRequest` | internal | `convert.go` |
| `ConvertResponse` | 有效 JSON body + fromProtocol + toProtocol | 转换后 JSON body 或 `ErrUnsupportedConversion`/`ErrInvalidResponse` | internal | `convert.go` |
| `ConvertStreamChunk` | raw SSE data payload + fromProtocol + toProtocol + `*StreamState` | `[][]byte`（零或多个转换单个 chunk 后的事件 JSON）或错误 sentinel | internal | `convert.go` |
| `StreamState` | 调用方持有，per-stream | 无（纯状态容器） | internal | `convert.go` |

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
- **DO NOT** 处理 thinking/reasoning 转换 — 正确做法：保留字段不转，后续 issue 处理
- **DO NOT** 支持 Responses/Images 协议 — 正确做法：返回 `ErrUnsupportedConversion`

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
- 新增协议转换组合（如 Responses↔Messages）。
- 发现并确认新的非显然陷阱，或旧陷阱已不再适用。
