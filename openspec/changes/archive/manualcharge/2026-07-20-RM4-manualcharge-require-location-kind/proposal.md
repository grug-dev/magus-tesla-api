## Why

Manual charge entries currently accept `location_kind` as an **optional** field (`NULL` in the
database, `*string` in the domain type). Roadmap analysis and user feedback confirm that knowing
whether a session occurred at home, work, or elsewhere is essential for charge-cost attribution
(RM4 feature: the UI form in the gateway tier will make this field visually required). This
backend tier locks in that requirement at the data contract level — the service rejects any
create or update that omits `location_kind`.

> **Decisions are pre-confirmed (grill-me session, 2026-07-19, RM4 roadmap).** This proposal
> captures the agreed scope. Design decisions are locked and numbered in design.md — do not
> re-open them here.

## What Changes

**This change is a BEHAVIOR CHANGE (required-field tightening) — it is breaking for callers that
previously omitted `location_kind`.**

Prior behavior: `location_kind` was optional — a create or update without it succeeded (stored as
`NULL`). After this change: `location_kind` is required on both create and update — a nil or
empty value is rejected by the service with a validation error before the query reaches the
database.

**Affected write path:** `manualcharge.Writer` — both `Create` and `Update` methods in
`internal/manualcharge/service.go`.

**No hot read path is affected.** `Reader.ListEntriesByVehicle` and `Reader.ListEntriesByAccount`
are unchanged — they continue to return `Entry.LocationKind` (which, after migration, is always
non-nil for every row).

**No gateway change in this tier.** The form-level required indicator (UI asterisk, `required`
attribute, client-side validation) is gateway work deferred to Tier 4
(`RM4-gateway-require-location-kind`). This tier is the backend contract.

**Artifacts in this change:**

- New goose migration in `internal/manualcharge/db/migrations/`:
  defensive `UPDATE ... 'OTHER' WHERE location_kind IS NULL` then `ALTER COLUMN ... SET NOT NULL`.
- Service validation: `Create` and `Update` in `internal/manualcharge/service.go` reject a nil
  or empty `LocationKind` before reaching the database.
- Spec delta: `location_kind` moves from optional → required in the create contract, is validated
  on update, and remains constrained to `HOME`/`WORK`/`OTHER` via the existing CHECK.

**Modules affected:**

- `internal/manualcharge` — service.go validation + new migration (this worker — owned).
- No changes to `internal/gateway`, `internal/tesla`, `internal/account`, or `internal/telemetry`.
- `sqlc.yaml` and generated code: no change — the DB column type is still `TEXT`; only the
  nullable constraint changes. sqlc regeneration is NOT required because the column's Go type
  in the generated model (`pgtype.Text`) does not change (`NOT NULL` in SQL does not affect the
  pgx/v5 Go type for `TEXT` columns — pgtype.Text is always used; the Valid flag reflects what
  the DB returns). No `query.sql` changes needed: the existing INSERT and UPDATE already pass
  `location_kind` as a parameter; the NOT NULL constraint is enforced by the DB, not by sqlc.

## Read Paths Affected

None. The change touches only the write path (validation in `Create`/`Update`) and the
migration (a one-time schema change). Both read methods (`ListEntriesByVehicle`,
`ListEntriesByAccount`) are unchanged.

## Capabilities

### Modified Capabilities

- `manual-charge-log`: `location_kind` tightened from optional → required. Create and Update
  now reject a missing or empty `location_kind` at the service layer. The DB column is promoted
  from nullable to `NOT NULL` via migration. The valid values (`HOME`, `WORK`, `OTHER`) and the
  existing `CHECK` constraint are unchanged.

## Deferred / Out of Scope

- **Gateway UI required indicator** — deferred to Tier 4 (`RM4-gateway-require-location-kind`).
  The form's required attribute, asterisk, and client-side validation belong in
  `internal/gateway/`. This tier is backend-only.
- **Changing `LocationKind *string` → `string`** — deferred to Tier 4. The field stays `*string`
  in the domain type so the gateway build continues to compile before Tier 4 updates its call
  sites. The service validation and the DB `NOT NULL` already guarantee required-ness without
  changing the Go type today.
- **Backfilling with business logic** — the migration uses `'OTHER'` as the backfill value for
  any NULL rows. This is a migration safety measure (expected to be a no-op after the planned DB
  wipe); it does not imply any business decision about pre-existing data.

## Impact

**Changed files inside `internal/manualcharge`:**

- `internal/manualcharge/db/migrations/20260720000001_require_location_kind.sql` — new goose
  migration: defensive UPDATE then `ALTER COLUMN location_kind SET NOT NULL`;
  `-- +goose Down` reverses with `DROP NOT NULL`.
- `internal/manualcharge/service.go` — `Create` and `Update` gain a `location_kind` required
  check (nil or empty string → validation error) before building DB params.

**Test files (updated/extended):**

- `internal/manualcharge/db_integration_test.go` — add scenarios: reject create without
  `location_kind`; reject update without `location_kind`; accept valid `HOME`/`WORK`/`OTHER`.

**Dependencies:** no new Go dependencies.

**Migrations:** one new goose migration. Applied with `make migrate-up`; never part of a build.
