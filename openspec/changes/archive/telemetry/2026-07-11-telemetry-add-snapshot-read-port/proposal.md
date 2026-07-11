## Why

Tier 3 (`telemetry-add-nightly-snapshots`) stores nightly vehicle snapshots in
`vehicle_snapshots`, but exposes no read path. The telemetry module's public port
(`Collector`) is write-only, so the dashboard (tier 5, `gateway-read-stored-vehicles`)
currently has no way to surface snapshot data — battery %, range, charge state,
last-updated — without violating the boundary rule that forbids cross-module DB access.

This change (tier 4 of `openspec/roadmaps/nightly-vehicle-telemetry.md`) closes that
gap by adding a **read port** to `internal/telemetry`: a Go interface the gateway can
consume in-process, returning the latest stored snapshot per vehicle for a given
account. The telemetry module maps `pgtype` → `Snapshot` at its own boundary; `pgtype`
never leaks out. The `Snapshot` domain type (already carrying `BatteryRangeKm()` /
`OdometerKm()` companions) is returned as-is. No new tables, no migration, no
collection logic — **read-only** over the existing `vehicle_snapshots` table.

Roadmap Data Access Model: dashboards read only platform-stored data; users never
trigger live Tesla calls. This change is the telemetry-side prerequisite for tier 5.

## What Changes

- **New read port on `internal/telemetry`**: a `Reader` interface exposing a batch
  method that returns the latest stored `Snapshot` per vehicle for a given account ID
  in a single query (Postgres `DISTINCT ON` — avoids N+1). A per-account result
  naturally handles the zero-snapshot case (empty slice, no error).
- **New sqlc query** in `internal/telemetry/db/query.sql`: a `:many` DISTINCT ON
  query scoped to one `account_id`, returning at most one row per `tesla_id` (the
  one with the maximum `captured_at`). Regenerated with `make sqlc` as a
  leader-integrated step.
- **New `store` method** mapping the sqlc row (`telemetrydb.VehicleSnapshot`) to the
  domain `Snapshot` at the DB → domain boundary (`pgtype.Timestamptz` → `time.Time`,
  `pgtype.Bool` → `*bool` for sentry_mode nil↔NULL).
- **Updated `AGENTS.md`** public-interface section: `Reader` port listed alongside
  `Collector` (append-only, existing content unchanged).
- **Tests**: offline unit test via a fake store seam (no DB, no network); plus a
  `DATABASE_URL`-gated store integration test (insert two snapshots per vehicle,
  assert the newer one is returned; multi-vehicle; empty account returns nil/empty).

**Not breaking.** The change adds a new interface and a new sqlc query — nothing is
removed, renamed, or modified. The `Collector` interface, `Snapshot` type, all domain
types, the DB schema, and all existing methods are untouched.

## Capabilities

### Modified Capabilities

- `telemetry`: Adds a **read** port (`Reader`) exposing the latest stored snapshot per
  vehicle for a given account — the telemetry-side prerequisite for the gateway
  dashboard display (tier 5).

## Impact

- **New / modified (within `internal/telemetry` sandbox)**
  - `internal/telemetry/telemetry.go` — declare `Reader` interface and update the
    `dbStore` unexported seam interface to include the new read method.
  - `internal/telemetry/store.go` — implement the batch latest-snapshot read on the
    concrete `dbStore` (calls the new sqlc query; maps `telemetrydb.VehicleSnapshot`
    → `Snapshot`).
  - `internal/telemetry/db/query.sql` — add `LatestSnapshotsByAccount :many` DISTINCT
    ON query.
  - `internal/telemetry/db/` — regenerated `telemetrydb` (leader-integrated `make sqlc`).
  - `internal/telemetry/reader.go` — `NewReader(pool *pgxpool.Pool) Reader` constructor.
  - `internal/telemetry/reader_test.go` — offline unit test (fake store seam).
  - `internal/telemetry/db_read_integration_test.go` — `DATABASE_URL`-gated store test.
  - `internal/telemetry/AGENTS.md` — append `Reader` to the public-interface section.
- **Leader-integrated (outside sandbox)**
  - `internal/telemetry/db/` regeneration via `make sqlc` — same pattern as tier-3
    task 3.2.
- **Not touched**: `cmd/poller`, `cmd/web`, `internal/gateway`, `internal/account`,
  `internal/tesla`, `internal/config`, `sqlc.yaml`, `Makefile`, DB migrations.
- **Dependencies**: no new Go modules; pgx/v5 + pgxpool + sqlc + uuid already in use.
- **Operational**: read-only; no new Tesla API calls; no new DB tables or migrations.

> Grill-me interview outcomes are binding: roadmap Decisions section
> (`openspec/roadmaps/nightly-vehicle-telemetry.md`) plus the 2026-07-11 tier-4 scope
> note (Option A: surface nightly data on the dashboard via a telemetry read port).
> The interview was conducted at the roadmap level and is recorded there; no new
> in-proposal interview is required for this additive, single-module change.
