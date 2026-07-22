# modelcatalog

> Scope: `services/executor/internal/modelcatalog/`; inherits `services/executor/AGENTS.md`.

## Responsibility

Transport-neutral model catalog boundary. Defines `CatalogRequest`/`CatalogResult` shapes, the narrow `CatalogProvider` port, safe sentinel errors, and the capability/thinking mapping from adapter-internal `Capability`/`ThinkingInput` to contract-facing string tags.

Imports only the adapter domain package for `Capability`/`ThinkingInput`. No HTTP, chi, generated contract, or transport code.

## Public surface

| Export | Purpose |
|---|---|
| `Principal` | Secret-free authenticated caller (Subject/KeyID/Role/Status) |
| `RoleService`/`RoleAdmin` | Canonical role values |
| `StatusActive`/`StatusDisabled` | Canonical status values |
| `CatalogRequest` | Protocol-normalized input carrying trusted `Principal` |
| `ThinkingConfig` | Transport-neutral thinking/reasoning description |
| `CatalogEntry` | One model in the catalog result (ID, Capabilities, Thinking, Created) |
| `CatalogResult` | Catalog listing result (`Models []CatalogEntry`) |
| `CatalogProvider` | Narrow catalog boundary interface (`ListModels`) |
| `ErrUnauthenticated` | No trusted Principal or failed revalidation |
| `ErrNoSnapshot` | No compiled snapshot published |
| `ErrMisconfigured` | Nil/typed-nil required dependency |
| `ErrQuarantineUnavailable` | Quarantine read failure, fail-closed |
| `MapCapabilities` | Adapter Capability → contract string tags (sorted, omits streaming/messages/responses) |
| `MapThinking` | Adapter ThinkingInput → transport-neutral ThinkingConfig (nil when unsupported) |

Capability map: Chat→`text`, Tools→`function_calling`, Vision→`vision`, Thinking→`thinking`, Images→`image`. Streaming/Messages/Responses are omitted.

## Safety rules

- Do not import HTTP, chi, generated contract, or transport packages.
- Sentinel errors carry no selector, snapshot, routing, request, response, credential, or upstream detail.
- Only a transport-facing facade may construct a `CatalogRequest` carrying a trusted `Principal`.

## Verification

```bash
cd services/executor
go test -race -count=1 ./internal/modelcatalog/...
```
