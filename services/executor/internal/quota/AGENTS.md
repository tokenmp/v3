# Executor quota domain

> Scope: `services/executor/internal/quota/`; inherits `services/executor/AGENTS.md`.

## Responsibility

This package owns the in-process quota reservation contracts and their test implementations. The legacy `Port` is retained solely for existing `execution.Runner` and `execution.StreamDriver` during the Phase 12.1→12.2 migration. New quota work must target the typed `Repository` API:

- `ReserveReservation`, `FinalizeReservation`, `ReleaseReservation`, and `Lookup`;
- safe bounded `ReservationID`, metadata, estimate, terminal settlement and accounting values;
- exact semantic replay only; divergent reserve or terminal input is `ErrConflict`;
- pre-cancelled contexts make no write; post-commit typed test faults preserve the committed state.

`DomainInMemory` and `TypedMock` are test implementations, not durable accounting storage. They deliberately have a typed map separate from legacy `Port` state until Phase 12.2 removes the old port. Do not change Runner, StreamDriver, transport, or runtime wiring in this phase.

## Safety rules

- Do not import `execution`, `requestid`, HTTP, runtime composition, or provider SDK packages. Validate `res_` IDs locally.
- Do not add a pricing/token estimation basis without a separately versioned domain contract. Phase 12.1 permits only `BasisNone`.
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
