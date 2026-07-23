# jwtverifier

`services/executor/internal/jwtverifier` 提供基于 Ed25519 (EdDSA) 的本地 JWT 验证，作为 Executor 的身份认证来源之一。它不依赖 Auth 服务的任何内部包，仅使用公钥进行本地验证。

## 职责

- `verifier.go`:
  - `Claims` 结构体 (`jwtv5.RegisteredClaims` + `Role string` + `TokenVersion int`)
  - `loadPublicKey(path)` — PKIX PEM 解析，sentinel errors，不泄露 path/content
  - `Verifier` 结构体 (`publicKey ed25519.PublicKey, issuer, audience string`)
  - `NewVerifier(publicKeyFile, issuer, audience) (*Verifier, error)`
  - `Verify(raw string) (*Claims, error)` — EdDSA only，校验 iss/aud/exp/nbf/sub/jti/role/token_version>=1
- `source.go`:
  - `Source` 结构体 (`verifier *Verifier`)，实现 `identity.Port`
  - `NewSource(publicKeyFile, issuer, audience) (*Source, error)`
  - `LookupByKey(ctx, rawToken) (identity.Identity, error)`
  - role 映射：`"user"`→`RoleService`，`"admin"`→`RoleAdmin`，其他→`ErrUnknownKey`
  - `KeyID` 留空（JWT 无 kid）
  - `Status`=`StatusActive`（纯本地验证，不查禁用状态）
  - 尊重 context 取消

## 安全模型

- 纯本地验证：Executor 不连接 Auth 数据库，不查询用户禁用状态或 `token_version` 的当前值。
- **已知 trade-off**：15min TTL 窗口内的 revoked token 仍可用。这是 accepted trade-off，在文档中记录。
- alg confusion 防御：`WithValidMethods([]string{"EdDSA"})` + keyfunc 中显式断言 `*jwtv5.SigningMethodEd25519`。
- sentinel errors：所有错误为稳定 sentinel，不泄露 token、path 或 PEM content。
- `String()`/`GoString()`/`Format()` 返回 `jwtverifier.Source([REDACTED])`，不泄露 issuer/audience。

## 与 identityenv 的关系

- `composition.Build` 中：若 `cfg.JWTPublicKeyFile` 非空 → JWT source 优先；否则 → identityenv fallback。
- JWT 配置时 `EXECUTOR_IDENTITY_MAP_JSON` 变为 optional。
- 启动 fail-fast：公钥文件缺失/格式错误 → `ErrJWTVerifier` → 拒绝启动。

## 依赖

- `github.com/golang-jwt/jwt/v5 v5.3.1`（与 Auth 相同版本）
- `services/executor/internal/identity`（`identity.Port`、`identity.Identity`）

## 测试矩阵

完整测试在 `verifier_test.go` 中：有效 round-trip、过期、nbf 未来、签名篡改、错误公钥、错误 iss、错误 aud、alg=none、缺失 sub/jti/role、token_version=0、role 映射、未知 role、空 token、并发、fuzz、公钥文件加载/缺失/格式错误/RSA 误用。
