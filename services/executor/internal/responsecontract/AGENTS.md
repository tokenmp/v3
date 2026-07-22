# Response Contract

> 作用域：`services/executor/internal/responsecontract/`。继承 `services/executor/AGENTS.md`。

## 包职责

- 负责：OpenAI Responses API 请求与成功响应的 provider-neutral 语义校验边界。`ValidateRequest` 以严格字段 allowlist 强制 stateless-only 执行（拒绝 `previous_response_id`、`conversation`、`store`、`background`、`include`、`moderation`、`prompt`、`truncation`、`service_tier` 等 stateful 字段）；`ValidateResponse` 执行 bounded 结构验证（wire cap 16 MiB、output items/usage/extension 边界）。解析、provider 调用、routing、HTTP 渲染均在此包之外。
- 不负责：provider 调用、routing、HTTP 传输、SSE 流、credential 解析、quota 管理。

## 导出 API

| 导出 | 输入 | 返回 | 说明 |
|---|---|---|---|
| `ValidateRequest` | `map[string]any`（strict JSON 解析后） | `bool` | 校验 stateless Responses 请求：required `model`+`input`，optional `instructions`/`max_output_tokens`/`metadata`/`reasoning`/`stream`/`temperature`/`text`/`tool_choice`/`tools`/`top_p`；stateful 字段通过 allowlist 显式拒绝 |
| `ValidateResponse` | `map[string]any`（strict JSON 解析后） | `bool` | 校验 Responses 成功响应：required `id`/`object`=`response`/`status`/`output`/`usage`，bounded output items（≤ 1024）/content parts（≤ 256）/usage（≤ 1e6，一致）/extension values（≤ 64 KiB）；total response fields ≤ 64 |
| `MaxWireResponseBytes` | — | `const` = 16 MiB | Responses 成功响应 wire 硬上限 |

## 校验边界常量

| 常量 | 值 | 用途 |
|---|---|---|
| `maxInputStringLength` | 1 MiB | input 字符串 / content 字符串上限 |
| `maxMetadataUserID` | 256 | metadata.user_id 上限 |
| `maxToolNameBytes` | 128 | tool name 上限 |
| `maxToolDescriptionBytes` | 512 | tool description 上限 |
| `maxReasoningEffortLen` | 16 | reasoning.effort 字符串上限 |
| `maxReasoningSummaryLen` | 16 | reasoning.summary 字符串上限 |
| `maxTextInputBytes` | 1 MiB | input_text / output_text text 字段上限 |
| `maxImageURLBytes` | 16 KiB | input_image image_url 上限 |
| `maxOutputItems` | 1024 | output 数组上限 |
| `maxOutputContentParts` | 256 | message output content parts 上限 |
| `maxUsageTokenCap` | 1,000,000 | usage 各计数器上限（与 `sdk.maxSDKUsageTokens` / `streaming.MaxTotalHardCap` 对齐） |
| `maxExtensionValueBytes` | 64 KiB | response 中未知 key 的 JSON 值大小上限 |

## 请求校验规则

1. **Allowlist 强制**：仅接受 12 个字段（`model`、`input`、`instructions`、`max_output_tokens`、`metadata`、`reasoning`、`stream`、`temperature`、`text`、`tool_choice`、`tools`、`top_p`）；任何 stateful 字段导致拒绝。
2. **model**：required 非空 string。
3. **input**：required，为 string（≤ 1 MiB）或非空 `[]any`（每项为 `type:"message"` 的 input item）。
4. **input item**：`type` 必须为 `"message"`；`role` 枚举 `user`/`system`/`developer`/`assistant`；`content` 为 string 或 content part 数组。
5. **content part**：`input_text`（required `text`）、`input_image`（required `image_url`）或 `output_text`（required `text`）；其余 `type` 拒绝。
6. **reasoning**：optional object，`effort` 枚举 `none`/`minimal`/`low`/`medium`/`high`/`xhigh`/`max`，`summary` 枚举 `auto`/`detailed`/`none`/`concise`。
7. **tools**：optional，每项 `type` 必须为 `"function"`，required `name`+`parameters`；optional `description`/`strict`。
8. **tool_choice**：optional，枚举 `auto`/`none`/`required`。
9. **temperature**：optional `[0, 2]`；**top_p**：optional `[0, 1]`。
10. **max_output_tokens**：optional，≥ 1。
11. **metadata**：optional，仅 `user_id`（CTL-free，≤ 256 bytes）。
12. **text.format**：optional object，key 限于 `name`/`schema`/`type`。

## 响应校验规则

1. **Top-level**：fields ≤ 64；`id`（required string）、`object`=`"response"`（required）、`status` 枚举 `completed`/`failed`/`in_progress`/`cancelled`/`incomplete`/`queued`（required）。
2. **output**：required `[]any`，≤ 1024 items；每 item `type` 为 `message`/`function_call`/`reasoning`。
3. **usage**：required object，`input_tokens`+`output_tokens`+`total_tokens` 非负、≤ 1e6、一致（`input+output==total`）。
4. **Extension values**：已知 key 无大小限制；未知 key 的 JSON 值 ≤ 64 KiB。

## 测试覆盖

| 维度 | 已覆盖断言 |
|---|---|
| 请求接受 | 最小 string input、array input、content parts、full request、output_text part、role developer、reasoning none、nil metadata/tools |
| 请求拒绝 stateful | `previous_response_id`/`conversation`/`store`/`background`/`include`/`moderation`/`prompt`/`truncation`/`service_tier` |
| 请求拒绝无效嵌套 | missing model/input、bad type/role/content/tool_choice/reasoning/temperature/top_p/max_output_tokens/stream/text、unknown root field 等 |
| 响应接受 | completed message、in_progress empty、failed、function_call、reasoning summary |
| 响应拒绝 | missing id/wrong object/bad status/bad output type/usage inconsistent/negative/over cap/missing field/not object/extension too large |
| Usage 边界 | exact cap (1e6) 接受 |
| Fuzz | `FuzzValidateRequest`/`FuzzValidateResponse`：random JSON 无 panic |

## 依赖

- 仅标准库（`encoding/json`、`unicode/utf8`）。
- 不导入 HTTP/chi/generated contract/transport/SDK 代码。

## DO NOT

- **DO NOT** 在此包内执行 provider 调用、HTTP 传输、routing 或 credential 解析。
- **DO NOT** 放宽 stateless allowlist 以接受 `previous_response_id` 等有状态字段——这些字段暗示服务端状态，当前执行模型不支持。
- **DO NOT** 修改常量而不更新测试与文档。
