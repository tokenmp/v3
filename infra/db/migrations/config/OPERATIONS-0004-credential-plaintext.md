# Runbook — Upstream Credential Plaintext Migration (000004)

> Scope: Config DB `tokenmp_config.upstream_credentials`. Private operator runbook
> template. Contains only counts and steps — never paste secret values, DSNs, or
> logs into this file. Keep this file in the repo as a template; fill in counts
> per-environment out-of-band.

## Background

Migration `000004_publish_hardening.up.sql` restores
`upstream_credentials.credential_ref` to `NOT NULL` (secret-free `vault://`
reference only) and retains the legacy `api_key` column solely to identify
historical plaintext data. The migration does **not** read, copy, or output any
`api_key` value. The application layer now rejects new plaintext writes
(`ErrSecretRejected`).

If historical rows have `credential_ref IS NULL` (or empty), the `ALTER COLUMN
... SET NOT NULL` will fail closed. Operators must resolve these rows before
applying the migration. The migration never auto-migrates or outputs secrets.

## Pre-apply inventory (run on the target DB, read-only)

These queries return counts only — they never select secret values.

```sql
-- Total credentials
SELECT count(*) FROM upstream_credentials;

-- Rows blocking NOT NULL (credential_ref IS NULL or empty)
SELECT count(*) FROM upstream_credentials
WHERE credential_ref IS NULL OR btrim(credential_ref) = '';

-- Rows still carrying legacy plaintext (existence only, never SELECT the value)
SELECT count(*) FROM upstream_credentials WHERE api_key IS NOT NULL AND api_key <> '';
```

## Decision

- If the blocking count is `0`: apply `000004` normally.
- If the blocking count is `> 0`: each such row must be assigned an opaque
  `vault://` reference in the Secret Store by the credential owner. Do **not**
  derive `credential_ref` from the plaintext `api_key`. Do **not** copy
  `api_key` into any other column, log, ticket, or this runbook.

## Resolution steps (per blocking row, owner-driven)

1. Provision the secret in the Secret Store under a stable `vault://` path.
2. Update only the `credential_ref` column to the new `vault://` path (leave
   `api_key` untouched during resolution — it is dropped conceptually, not by
   the migration).
3. Re-run the blocking-count query until it returns `0`.
4. Apply migration `000004_publish_hardening.up.sql`.
5. Verify: `SELECT count(*) FROM upstream_credentials WHERE credential_ref IS
   NULL` returns `0`; the partial unique index `upstream_credentials_ref_uidx`
   exists.

## Down / rollback

`000004_publish_hardening.down.sql` is a **fail-closed** contract revert. Before
any destructive DDL it runs preflight guards that abort (and, via golang-
migrate's per-file transaction, roll back the whole migration) when data exists
that the pre-000004 schema cannot express:

- `config_revisions.parent_revision_id IS NULL` — parentless drafts (the old
  schema defines the column as `bigserial NOT NULL`);
- `config_revisions.version <> 1` — CAS-updated drafts (dropping the column
  would lose optimistic-concurrency history);
- non-empty `source_revision_id` / `rollback_note` — rollback provenance;
- `config_audit_log.action = 'rollback_publish'` — not in the old action enum;
- non-empty `actor_kind` / `request_id` audit metadata.

Operators must convert these rows to a pre-000004-expressible form (e.g. give a
parentless draft a parent, or remove rollback audit rows) before retrying the
down. The migration never silently backfills or drops historical data. On a
clean DB (no 000004-era data) the down succeeds and reverts the schema: drops
`credential_ref` NOT NULL, the singleton published partial unique index, the
`version` column and rollback metadata, and restores `parent_revision_id` as
`bigserial NOT NULL` + sequence default. It does **not** restore plaintext
`api_key` semantics. Rollback is schema-only.

## Forbidden

- Do not paste `api_key` values, DSNs, or full connection strings here.
- Do not log or echo `api_key` values during resolution.
- Do not derive `credential_ref` from `api_key` content.
- Do not run this against production without a tested backup and a maintenance
  window.

## Verification record (filled per-environment, counts only)

- Environment: ______
- Date (UTC): ______
- Blocking rows before resolution: ______
- Blocking rows after resolution: ______
- Multiple-published revisions before `000004` (must archive extras first):
  ______
- Applied by: ______ (operator handle only, no secrets)
