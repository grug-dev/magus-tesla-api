# RM57-telemetry-rekey-supercharger-history-on-tesla-id

> Source: MAG-67 — https://linear.app/magus-monitor/issue/MAG-67/4-re-key-the-supercharger-pair-on-tesla-id-reshape-the-mirror
> Tier 1 of `openspec/roadmaps/RM57-rekey-supercharger-pair-on-tesla-id.md`.
> Step 4 of the MAG-63 re-key decision record. Blocker MAG-66 (the
> authorization seam) is done.

## Why

`telemetry.supercharger_history` is keyed on `account_id`. That is wrong for
data about a car. A Supercharger session belongs to the vehicle that charged.
`account.vehicles` already says which vehicles a user may see, so the account
link does not need to be repeated on every session row.

The roadmap's own decision sets the scope: `tesla_id` becomes `NOT NULL` and
`account_id` is dropped. Nothing except `account_id` ties a row to an account.
`vin` only reaches an account through `account.vehicles`, and that lists
**registered** vehicles only. So once `account_id` is gone, a row with
`tesla_id IS NULL` can never be read by any query again. A nullable column
would store rows nobody can reach.

That forces a real behaviour change, not only a schema change.
`collectChargingHistory` writes `tesla_id = NULL` today when a session's VIN
is not a registered vehicle (`internal/telemetry/service.go:348`). It must
now **skip** that session and count the skip. Tesla still returns it; we stop
storing it.

## What changes

- **Migration** (`internal/telemetry/db/migrations/20260912000001_rekey_supercharger_history_on_tesla_id.sql`):
  delete rows where `tesla_id IS NULL`, `ALTER COLUMN tesla_id SET NOT NULL`,
  `DROP COLUMN account_id`, and replace the three `account_id` indexes with
  two `tesla_id` ones. `UNIQUE (session_id)` and the primary key are
  untouched. Full SQL and the index plan: `design.md`.
- **Queries** (`internal/telemetry/db/query.sql`): drop `account_id` from
  `UpsertSuperchargerHistory` (its INSERT list and both change-detection
  deny-lists) and from the `WHERE` of `SuperchargerHistoryByVehicle`,
  `SuperchargerHistoryByVehicleBetween` and
  `SuperchargerHistoryByVehicleUpdatedSince`. Delete
  `SuperchargerHistoryByAccount` and `SuperchargerHistoryByAccountUpdatedSince`
  — both filter on a column that no longer exists (`design.md` D3, D4).
- **Domain type** (`internal/telemetry/telemetry.go`): `SuperchargerHistory`
  drops `AccountID` and `TeslaID` becomes a plain `int64` instead of `*int64`.
- **Port** (`SuperchargerHistoryReader`): five methods become three. The three
  survivors drop their `accountID` parameter.
- **Write path** (`internal/telemetry/service.go`): `collectChargingHistory`
  skips a session whose VIN is not in the account's VIN→TeslaID map and
  increments a new `CycleReport.ChargingSessionsSkippedUnregistered` counter.
  `LogCycle` prints it.
- **Tests**: updated, not new. Every telemetry test that seeds or asserts on
  `account_id` moves to `tesla_id`. The seven tests of the removed
  account-wide port are deleted with it (`design.md` D4, "lost coverage").
- **Docs**: `internal/telemetry/AGENTS.md`, this change's delta on
  `openspec/specs/telemetry/spec.md`, and three KB guides —
  `kkpa/context/architecture/telemetry-ingest-only.md`,
  `kkpa/context/architecture/telemetry-tables.md` and
  `kkpa/context/architecture/nightly-cycle.md` (the last two are additions to
  the dispatch's list; both name facts this change invalidates — see
  `design.md` §Docs).

## Breaking?

**Yes, internally.** No external API and no HTTP surface on this module, so
nothing outside the repository breaks.

Inside the repository the `SuperchargerHistoryReader` port changes shape:

- `SuperchargerHistoryByAccount` — removed.
- `SuperchargerHistoryByAccountUpdatedSince` — removed. This is the one
  production caller of the port today (`internal/app/processor.go:223`).
- `SuperchargerHistoryByVehicle`, `...ByVehicleBetween`,
  `...ByVehicleUpdatedSince` — lose their `accountID` parameter.
- `SuperchargerHistory` loses `AccountID`; `TeslaID` changes type.

`go build ./...` therefore fails outside `internal/telemetry` until the
`internal/app` bridge lands. That bridge is small and leader-owned — see
`design.md` D7 and `tasks.md` T8.

## Modules affected

- `internal/telemetry` — owns the table, the queries, the port, the write
  path, and every test fixup inside the module. This worker's sandbox.
- `internal/app` — **leader-owned**. `processor.go`'s mirror step is the port's
  only production caller. It must fan out over the account's vehicles using
  the surviving per-vehicle updated-since method, keeping its account-keyed
  watermark (`design.md` D7). `app.go` and `processor_test.go`'s two fakes
  follow. Tier 3 of the roadmap finishes this work by moving the watermark to
  `tesla_id`; this change only keeps the build green.
- `internal/analytics` — **leader-owned**, test only.
  `db_integration_test.go` seeds `telemetry.SuperchargerHistory{AccountID:…}`
  and inserts `account_id` in raw SQL. Both must go.
- `internal/gateway` — untouched. It must never import `internal/telemetry`
  (`make boundary-guard`), and this change adds no import.

## Read paths affected

- **Bounded per-vehicle reads stay bounded.** `SuperchargerHistoryByVehicle`
  and `...ByVehicleUpdatedSince` each gain an exactly matching index
  (`design.md` §Index Plan), so both keep running as a single index range scan
  with no sort step — the same shape they have today, one column narrower.
- **The nightly mirror read changes grain**, from one account-wide read per
  account to one read per vehicle. Each read is narrower and indexed. It runs
  in the midnight batch, not on a user request.
- **No user-facing page reads this table.** The Supercharger Stats page reads
  `charging.supercharger_sessions`, not telemetry.

## Non-goals

- `charging.supercharger_sessions` and `charging.mirror_watermarks` — roadmap
  tier 2.
- The gateway authorization work and the per-`tesla_id` mirror rewrite —
  roadmap tier 3.
- `charging.manual_charge_entries` (MAG-68), `analytics.vehicle_metrics`
  (MAG-69), the conventions/spec/KB sweep (MAG-70),
  `telemetry.vehicle_snapshots.account_id` (MAG-76).
- No `poll_runs` column for the new skip counter (`design.md` D6).
- The table's `COMMENT ON TABLE` still says "per account". It is not edited —
  this project does not ship a migration to correct comment text.
