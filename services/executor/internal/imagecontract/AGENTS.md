# Executor Images Contract

> 作用域：`services/executor/internal/imagecontract/`。继承仓库根目录、`services/` 与 `services/executor/AGENTS.md`。

## 模块职责

- 负责 legacy OpenAI Images 请求与成功响应的纯 Go、provider-neutral 语义校验：请求 allowlist、非空且不 trim 的 prompt、1 MiB prompt、512-byte CTL-free user、`response_format` 默认 `url`，以及响应的 16 MiB raw、URL/base64、usage/extensions、revised prompt、10 MiB item/12 MiB aggregate 边界。
- base64 校验必须以 `base64.NewDecoder` + `io.Copy`/`io.CopyN` 到 discard 流式计数；禁止 `DecodeString` 分配攻击者控制的解码 slice。
- 不负责 JSON structural parsing、HTTP、SDK、routing、quota 或错误渲染。SDK 与 transport 都必须消费本包，避免语义漂移。

## 开发与验证

```bash
cd services/executor
gofmt -w internal/imagecontract
go test -race -count=1 ./internal/imagecontract/... ./internal/sdk/openaiadapter/... ./internal/transport/executorv1api/...
```

## DO NOT

- **DO NOT** 放宽 provider extension、URL、CTL、response-format 或 cap 边界而不同时更新 SDK/transport parity tests。
- **DO NOT** 在错误中返回 prompt、URL、base64、provider response 或 credential。
