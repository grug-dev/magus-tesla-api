Source: MAG-19 — https://linear.app/magus-monitor/issue/MAG-19/allow-editing-supercharger-sessions
Roadmap: openspec/roadmaps/RM31-supercharger-session-verification.md
Tier: 2 of 5 (`charging` gains the two `SessionReader` methods `analytics` needs; tier 1 was
`charging`'s `SessionVerifier` write port, tier 3 is `analytics`'s Supercharger-source
switch — which depends on this tier — tier 4 is `gateway`'s display-only columns, tier 5 is
the gateway edit itself, which depends on tiers 1, 2, 3 and 4). Tier 2 has no dependency on
any other tier (roadmap Decision 7 — "Tiers 1, 2 and 4 have no dependency on one another and
could be done in any order or in parallel").
Design gate: **not tripped.** This change creates or alters no database object — no
migration, no table, no column, no index, no constraint. Both new queries are served
entirely by the existing `idx_charge_sessions_vehicle_stop (account_id, tesla_id,
charge_stop_date_time)` index, built by RM29 tier 6 (see design.md §"Database Changes" for
the index proof). `openspec/config.yaml`'s blanket "design.md is REQUIRED for any DB-touching
change" still applies — this change adds two new `SELECT` queries — so design.md documents
both queries and the existing index that already serves them, without requiring the owner's
gate confirmation a schema change would need.
Unit tests: **integration-only**, matching RM29 tier 6 / RM30 tier 1 / RM31 tier 1's
precedent for this module's `charge_sessions` ports. Neither new method's Go-side logic
(a two-line `limit <= 0` clamp to the existing `defaultLimit`, mirroring
`readerService.ListEntriesByVehicle`'s identical clamp, which itself has no dedicated
offline unit test) is a separable pure function worth isolating, and both `sessionReader`
methods are — like `ListSessionsByVehicleBetween` before them — tested only via the real
`DATABASE_URL`-gated integration suite (design.md D7), not `charging_test.go`'s
fake-backed `store` interface. `Session` gains no new field or value-receiver method.

## Why

RM31's roadmap Decision 9 found the blocker for MAG-19's core requirement: `analytics`
consumes three methods of `telemetry.SuperchargerReader` today (`…ByVehicleBetween`,
`…ByVehicleUpdatedSince`, `…ByVehicle(limit)`), but `charging.SessionReader` — the port
`analytics` must switch to so a human-verified percentage reaches `vehicle_metrics`
(Decision 1) — exposes only the `Between` shape (RM30 tier 1). Moving `analytics`'s
Supercharger source to `charging` (tier 3) cannot happen until this gap closes; this tier
closes it.

The two methods this tier adds are not interchangeable in importance. **Method 1,
`ListSessionsByVehicleUpdatedSince`, is the mechanism that makes RM31 work at all**:
`SessionVerifier.VerifySession` (tier 1) sets `updated_at = now()` and touches no other
timestamp column, so a human's battery-percentage correction becomes visible to
`analytics.Recalculator.Reconcile`'s watermark-driven re-derivation through this method and
no other. Without it, tier 3's `analytics` switch would compile and read the right table,
but a verified session would never trigger a recalculation — the whole point of MAG-19.
Method 2, `ListSessionsByVehicle`, serves the simpler "most recent N" access pattern
`RecentEfficiency`'s Wh/km calculation needs and is untouched by a human edit (roadmap
Decision 8 — both Supercharger reads move together, not just the one Decision 1 forces).

Field parity was verified while planning the roadmap (Decision 9) and **passes**: the three
consuming calculations read only `ChargeStartDateTime`, `ChargeStopDateTime`,
`StartBatteryPct`, `EndBatteryPct` and `EnergyKWh`, all already present on `charging.Session`
since RM30 tier 1. This tier adds no field to `Session` — only two new read methods over the
existing type.

## What Changes

**Additive only — no schema change.**

- **ADDED** — `charging.SessionReader.ListSessionsByVehicleUpdatedSince(ctx, accountID,
  teslaID int64, since time.Time) ([]Session, error)` — every session for the vehicle whose
  `updated_at` is at or after `since`. Mirrors `Reader.ListEntriesByVehicleUpdatedSince`'s
  contract shape (residual `updated_at` filter within the existing vehicle-scoped index
  scan, no new index, no `LIMIT`) — see design.md D1.
- **ADDED** — `charging.SessionReader.ListSessionsByVehicle(ctx, accountID, teslaID int64,
  limit int) ([]Session, error)` — the `limit` most recent sessions for the vehicle,
  ordered newest-first by `ChargeStopDateTime`. `limit <= 0` uses the module's existing
  `defaultLimit` (100), mirroring `Reader.ListEntriesByVehicle`'s clamp exactly. Sort
  direction is **DESC**, deliberately unlike `ListSessionsByVehicleBetween`'s ASC — see
  design.md D3 for the justification against the table's own index.
- **ADDED** — `chargingdb.ListSessionsByVehicleUpdatedSince` and
  `chargingdb.ListSessionsByVehicle`, two new sqlc queries in
  `internal/charging/db/query.sql`, regenerated via `make sqlc`. No migration accompanies
  either — the existing `idx_charge_sessions_vehicle_stop` already serves both (design.md
  §"Database Changes").
- **CHANGED** — `internal/charging/session_reader.go` gains both new methods on the existing
  `sessionReader` struct — no new file, no new struct (design.md D6): this is the same file
  `ListSessionsByVehicleBetween` already lives in.
- **CHANGED** — `internal/charging/charging.go`'s `SessionReader` interface gains both new
  methods' doc comments, plus an interface-level note that the three methods do not share a
  sort-direction convention (design.md D3).
- **CHANGED** — `internal/charging/AGENTS.md` — documents both new methods under §Public
  Interface and updates the `charge_sessions` §Data Ownership / §Testing Notes references to
  the read port (docs-track-structural-change, `CLAUDE.md` §Non-negotiables).
- **UNCHANGED** — the migration file, `SessionMirror`, `SessionWriter`, `MirrorChargeSession`,
  `SessionVerifier`, `VerifyChargeSession`, `ListSessionsByVehicleBetween`,
  `manual_charge_entries`, and every file outside `internal/charging/`. No column, index,
  constraint, port, or type is removed anywhere.

**Breaking:** no. Purely additive — two new interface methods and two new queries with no
existing caller yet.

**Modules affected:** `charging` only. No other module's code changes in this tier.
**`cmd/web` wiring** (nothing new to wire — `NewSessionReader` is already constructed and
injected as of RM30 tier 2's gateway swap; this tier only widens the interface it already
returns) is unaffected. `internal/analytics`'s consumption of these two methods is tier 3's
scope, not this tier's.

## Read paths affected

Per `openspec/config.yaml` §proposal ("performance-sensitive proposals must name the read
path(s) they affect"):

- **Two read paths are added, with no caller yet.** Both `ListSessionsByVehicleUpdatedSince`
  and `ListSessionsByVehicle` are new, but nothing in this tier calls them — tier 3
  (`analytics`) is the consumer. Both reuse `idx_charge_sessions_vehicle_stop (account_id,
  tesla_id, charge_stop_date_time)` — the same index `ListSessionsByVehicleBetween` already
  uses, one via a residual filter within a forward scan, the other via a backward scan for
  its DESC order (design.md §"Database Changes"). No new index, no schema change.
- **No existing read path changes.** `SessionReader.ListSessionsByVehicleBetween`,
  `charging.Reader` (`manual_charge_entries`), `SessionWriter`'s nightly mirror,
  `SessionVerifier`'s point-write, and `analytics`'s current
  `telemetry.SuperchargerReader`-backed read (unchanged until tier 3) all keep identical
  query plans, indexes, and results before and after this change.

## Impact

- **Affected specs:** `charge-session-log` (existing capability; **ADDED** requirements for
  the two new retrieval behaviors only — nothing about the existing write, synchronization,
  or windowed-retrieval requirements changes).
- **Affected code:** `internal/charging/` (`charging.go`, `session_reader.go`, `db/query.sql`,
  a new `db_session_reader_updated_since_integration_test.go` and additions to the existing
  `db_session_reader_integration_test.go` (or a second dedicated file — task 3.1 decides),
  `AGENTS.md`).
- **Design gate:** not tripped — see header. design.md carries both queries, the shared
  index-proof table, and the Test Contract, per `openspec/config.yaml`'s blanket design.md
  requirement for any DB-touching change.
- **Deferred, explicitly NOT in scope:** `analytics`'s source switch and its consumption of
  these two methods (tier 3), any gateway rendering or edit UI (tiers 4–5), any change to
  `SessionMirror`, `SessionWriter`, `SessionVerifier`, `ListSessionsByVehicleBetween`, or the
  migration file, and any new database object of any kind — if this design's index plan had
  concluded one was needed, that conclusion would have been reported as blocked rather than
  implemented (dispatch instruction; see design.md D2).
