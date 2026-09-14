# RM57-charging-rekey-supercharger-sessions-on-tesla-id

> Source: MAG-67 — https://linear.app/magus-monitor/issue/MAG-67/4-re-key-the-supercharger-pair-on-tesla-id-reshape-the-mirror
> Tier 2 of `openspec/roadmaps/RM57-rekey-supercharger-pair-on-tesla-id.md`.
> Depends on tier 1 (`RM57-telemetry-rekey-supercharger-history-on-tesla-id`), archived.

## Why

`charging.supercharger_sessions` and `charging.mirror_watermarks` are keyed on
`account_id`. A Supercharger session is data about a car, not about a user.
`account.vehicles` already records which vehicles a user may see, so repeating
the account on every session row adds nothing and splits one car's history
across accounts.

Tier 1 already did this to the source table, `telemetry.supercharger_history`.
The mirror must follow, or the two tables stop agreeing on what identifies a
session.

The roadmap's own decision sets the scope: `tesla_id` becomes `NOT NULL` and
`account_id` is dropped. Nothing except `account_id` tied a row here to an
account. After the drop, a row with `tesla_id IS NULL` is unreachable by every
query this module has, so keeping the column nullable would only store rows
nobody can read.

## What changes

- **Migration 1** (`internal/charging/db/migrations/20260912000002_rekey_supercharger_sessions_on_tesla_id.sql`):
  delete rows where `tesla_id IS NULL`, de-duplicate `session_id`, replace
  `UNIQUE (account_id, session_id)` with `UNIQUE (session_id)`,
  `ALTER COLUMN tesla_id SET NOT NULL`, `DROP COLUMN account_id`, and replace
  `idx_supercharger_sessions_vehicle_stop` with its one-column-narrower
  vehicle-keyed form. Full SQL and index plan: `design.md`.
- **Migration 2** (`…/20260912000003_rekey_mirror_watermarks_on_tesla_id.sql`):
  delete every cursor, drop `account_id`, add `tesla_id BIGINT NOT NULL`, and
  replace `UNIQUE (account_id)` with `UNIQUE (tesla_id)`. An absent cursor
  already means "epoch", so deleting them costs one longer nightly run and no
  data (`design.md` D4).
- **Queries** (`internal/charging/db/query.sql`): `account_id` leaves
  `MirrorSuperchargerSession` (its INSERT list, its `ON CONFLICT` target and its
  change-detection deny-list), `ListSessionsByVehicleBetween`,
  `ListSessionsByVehicleUpdatedSince`, `ListSessionsByVehicle`,
  `LockSessionForVerification`, `VerifySuperchargerSession`,
  `GetMirrorWatermark` and `UpsertMirrorWatermark`. The two verification
  queries re-key their scope on `tesla_id` rather than losing it
  (`design.md` D3). `ListValidSessionCapacitiesForPeriod` drops its now-
  redundant `tesla_id IS NOT NULL`.
- **Domain types** (`internal/charging/charging.go`): `SessionMirror` and
  `Session` both drop `AccountID`, and `TeslaID` becomes a plain `int64`
  instead of `*int64`.
- **Ports**: `SessionWriter.MirrorSessions`, `SessionReader.ListSessionsByVehicleBetween`,
  `SuperchargerSessionAnalyticsReader.ListSessionsByVehicleUpdatedSince` /
  `…ListSessionsByVehicle`, `MirrorWatermarkStore.MirrorWatermark` /
  `AdvanceMirrorWatermark` drop their `accountID` parameter.
  `SessionVerifier.VerifySession` replaces `accountID uuid.UUID` with
  `teslaID int64` — it keeps a tenant boundary instead of losing one
  (`design.md` D3).
- **Tests**: updated, not new. Every charging test that seeds or asserts on
  `supercharger_sessions.account_id` or `mirror_watermarks.account_id` moves to
  `tesla_id`. `db_backfill_integration_test.go` is deleted — it is a migration
  test, which this project does not keep (`design.md` D7, with the lost
  coverage named).
- **Docs**: `internal/charging/AGENTS.md`, this change's delta on
  `openspec/specs/charging/spec.md`, and six KB guides (`design.md` §Docs).

`charging.manual_charge_entries` is **not touched**. MAG-68 owns it.

## Breaking?

**Yes, internally.** This module exposes no external API and no HTTP surface,
so nothing outside the repository breaks.

Inside the repository six port methods change shape and two domain types lose a
field. `go build ./...` fails outside `internal/charging` until the
cross-module bridge lands. That bridge is small and leader-owned — see
`design.md` D5 and `tasks.md` T8.

## Modules affected

- `internal/charging` — owns both tables, the queries, the ports and every test
  fixup inside the module. This worker's sandbox.
- `internal/app` — **leader-owned**. `processor.go`'s mirror loop must become
  one pass per `tesla_id`. This is forced, not optional: the watermark is
  per vehicle after this change, so an account-grouped loop has nothing
  coherent to pass it (`design.md` D5).
- `internal/analytics` — **leader-owned**. Three call sites drop their
  `accountID` argument (`reader.go:141`, `recalculate.go:158`,
  `recalculate.go:279`), plus the test fakes.
- `internal/gateway` — **leader-owned**. Two `ListSessionsByVehicleBetween`
  call sites and one `VerifySession` call site
  (`internal/gateway/handlers/supercharger.go`), and the `updated.TeslaID != nil`
  branch, which can no longer be nil. Tier 3 then routes these through
  `authorizeVehicle`; this change only keeps the build green and the tenant
  boundary intact.

## Read paths affected

- **The Supercharger Stats page** (`GET /supercharger-stats`,
  `GET /ui/supercharger-stats`) does one `ListSessionsByVehicleBetween` read per
  render. It keeps running as a single ascending index range scan with no sort
  step — the same shape as today, one column narrower (`design.md` §Index plan).
- **The analytics recalculation** reads `ListSessionsByVehicleBetween` and
  `ListSessionsByVehicleUpdatedSince` per vehicle. Both keep their current plan
  shape; the updated-since read keeps `updated_at` as a residual filter inside
  the vehicle-pruned scan, exactly as it is today (`design.md` D6).
- **The nightly mirror read changes grain**, from one pass per account to one
  pass per vehicle. It runs in the midnight batch, not on a user request.
- **One extra nightly run is slower, once.** Deleting the cursors makes the
  first run after the migration re-read each vehicle's whole Supercharger
  history. The mirror is idempotent, so it writes nothing new.

## Non-goals

- `charging.manual_charge_entries` — MAG-68.
- `analytics.vehicle_metrics` and `vehicle_metric_watermarks` — MAG-69.
- The gateway authorization rewrite (`authorizeVehicle` at every Supercharger
  read) — roadmap tier 3.
- The conventions / spec / KB sweep — MAG-70. This change still fixes the docs
  it invalidates itself.
- The table and column `COMMENT`s that still say "per account". They are not
  edited: this project does not ship a migration to correct comment text
  (`design.md` D8).
