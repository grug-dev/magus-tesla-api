## Context

The `account` module persists each account's Tesla tokens in the `tesla_tokens` table (schema in
`internal/account/db/migrations/`, queries in `internal/account/db/query.sql`, sqlc-generated
access in `db/query.sql.go`). Today `SaveTeslaTokens` calls `InsertTeslaToken`, a plain `INSERT`
with no conflict handling, so the gateway connect flow (`/connect/tesla/callback`) appends a new
row on every reconnect. Reads (`GetLatestTeslaTokenByAccount`, and the `…ForUpdate` variant used by
the refresh transaction) already take only the newest row `ORDER BY updated_at DESC LIMIT 1`, so
older rows are never used.

The original design deliberately allowed 1:N to support connecting multiple Tesla accounts, but
that is an explicit Non-Goal today (no management/disconnect UI) and the accumulation is pure waste.
The owner has decided on one Tesla connection per account.

## Goals / Non-Goals

**Goals:**
- Guarantee at most one `tesla_tokens` row per account at the database level.
- Make reconnecting replace the stored tokens in place, transparently to the gateway.
- Leave the public `account` interface and the gateway routes unchanged.

**Non-Goals:**
- Multi-Tesla-per-account support (removed by this change).
- Any UI for managing/disconnecting connections.
- Changes to token refresh/rotation (`AccessTokenFor`) beyond the storage semantics.

## Decisions

**Enforce uniqueness in the schema, upsert in the query.** Add `UNIQUE (account_id)` to
`tesla_tokens` and turn the insert into `ON CONFLICT (account_id) DO UPDATE` (renamed
`InsertTeslaToken` → `UpsertTeslaToken`). This mirrors the existing `UpsertAccountFromOAuth`
pattern in the same `query.sql`, and pushes the invariant into the database rather than relying on
application logic. Alternative considered: check-then-update in Go — rejected as racy and
non-idiomatic here given the established upsert pattern.

**Keep `account_id` as the conflict key (not a Tesla-identity key).** Since the model is now
one-per-account, `account_id` alone is the natural key. The `tesla_email` column is retained (still
nullable, still unpopulated today) but is not part of the key. Alternative considered: keying on
`(account_id, tesla_sub)` to keep multi-Tesla — rejected per the owner's decision and because
`tesla_sub` isn't currently extracted from the OAuth `id_token`.

**Dedupe existing rows in the forward migration.** Before adding the constraint, delete all but the
most-recently-updated row per account so the `ALTER TABLE ... ADD CONSTRAINT` cannot fail on
pre-existing duplicates. The `UNIQUE` constraint's implicit index supersedes the standalone
`idx_tesla_tokens_account_id`, which is dropped (and recreated on rollback).

**No gateway or interface change.** `SaveTeslaTokens` keeps its signature and simply calls the
upsert; `TeslaCallback` is untouched. Reads are already single-row-correct, so
`GetLatestTeslaTokenByAccount` and `…ForUpdate` are left as-is (the `ORDER BY … LIMIT 1` is now
redundant but harmless — not worth the churn or the risk to the locking behavior in the refresh tx).

## Risks / Trade-offs

- **Migration deletes data** → It only removes superseded duplicate token rows (older `updated_at`),
  which are already unreachable by every read path; the surviving row is the one currently in use.
  The migration is gated and run by the owner (`make migrate-up`).
- **Loss of multi-Tesla capability** → Accepted per the decision; can be reintroduced later behind a
  Tesla-identity key if the product needs it, superseding this change.
- **Stale sqlc output if not regenerated** → The build fails fast because `service.go` references the
  renamed `UpsertTeslaToken`; `make sqlc` must run before `go build`.

## Migration Plan

1. Apply the code changes (migration file, `query.sql`, regenerated `db/query.sql.go`, `service.go`).
2. Owner runs `make migrate-up` on each environment — dedupes then adds the unique constraint.
3. Rollback: `make migrate-down` drops the constraint and restores the non-unique index; token rows
   already collapsed to one per account remain valid under the looser schema.
