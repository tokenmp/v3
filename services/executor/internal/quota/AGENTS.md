# Executor quota domain

> Scope: `services/executor/internal/quota/`; inherits `services/executor/AGENTS.md`.

## Responsibility

This package owns the typed quota reservation domain consumed by all execution paths. The legacy `Port`/`InMemory`/`Mock` were removed in Phase 12.2; `Repository`, `DomainInMemory` and `TypedMock` are now the sole implementations.

`Repository` is the typed quota port:
- `ReserveReservation`, `FinalizeReservation`, `ReleaseReservation`, and `Lookup`;
- safe bounded `ReservationID`, metadata, estimate, terminal settlement and accounting values;
- exact semantic replay only; divergent reserve or terminal input is `ErrConflict`;
- pre-cancelled contexts make no write; post-commit typed test faults preserve the committed state.

Non-stream Runner finalizes with `AccountingUnpricedSuccess`; StreamDriver uses `AccountingConfirmedUsage` when `streaming.UsageKnown` is true (confirmed token counts from protocol usage events), otherwise `AccountingUnpricedSuccess`. Both pass `QuotaIdentity` (subject/key_id/protocol) from the authenticated facade layer.

`DomainInMemory` and `TypedMock` are test implementations, not durable accounting storage.

## Safety rules

- Do not import `execution`, `requestid`, HTTP, runtime composition, or provider SDK packages. Validate `res_` IDs locally.
- Do not add a pricing/token estimation basis without a separately versioned domain contract. Only `BasisNone` is currently supported.
- `Reservation.Format` must remain redacted: no IDs, subject, key ID, model, revision, request content, credential, URL, or settlement quantities.
- Returned records and mock call records must be owned defensive copies.

## Verification

```bash
cd services/executor
gofmt -w internal/quota
go test -race -count=1 ./internal/quota/...
go test ./...
go build ./...
```
