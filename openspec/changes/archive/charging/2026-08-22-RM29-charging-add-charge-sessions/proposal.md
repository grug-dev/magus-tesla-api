Source: MAG-26 — https://linear.app/magus-monitor/issue/MAG-26/modular-monolith-refactoring
Roadmap: openspec/roadmaps/RM29-modular-monolith-boundaries.md
Tier: 6 of 8 (`charging` gains `charge_sessions`; tiers 1–5 are archived —
`RM29-analytics-rename-from-battery`, `RM29-charging-rename-from-manualcharge`,
`RM29-analytics-add-vehicle-metrics`, `RM29-telemetry-drop-derived-columns`,
`RM29-analytics-own-charge-gaps`; tier 7 is `RM29-app-add-process-vehicle-data`;
tier 8 is parked). T6 depends only on T2 and is independent of T5 and T7.
Design gate: **confirmed by the owner**, with one substantive amendment —
`charge_sessions` is a full mirror of a charge session (it also carries
`site_location_name`, `energy_kwh`, `total_cost`, `currency`, `is_paid`), not a
verification sidecar. See design.md's gate-revision note and **D1**.
Unit tests: **integration-only**. This tier adds no pure/offline logic — the only new Go
is a DB-boundary writer (`internal/charging/session_writer.go`) and a composition-root
mapping in `cmd/poller`, neither of which has a testable pure function. The whole test
surface is the `DATABASE_URL`-gated suite, whose expected values are authored up front in
design.md §Test Contract per `ai/go-conventions.md` §Testing. Roadmap **D10**
("characterization only, no new unit tests for new code") is satisfied: nothing moves, so
there is no prior output to characterize, and no new unit test is added.

## Why

Roadmap **D4** assigns `charge_sessions` to `internal/charging`: the module that owns
charging data should own *all* charging data, not just the half a human types in. Today
the Supercharger half lives in `internal/telemetry.supercharger_sessions`, and with it the
five battery-percentage columns (`start_battery_pct`, `end_battery_pct`,
`battery_pct_source`, `start_battery_pct_est`, `end_battery_pct_est`) that RM27 added.
Three specific symptoms:

1. **`internal/telemetry` stores five columns that no telemetry code writes, reads, or
   can ever write.** They are excluded from `UpsertSuperchargerSession`'s INSERT list
   *and* its `ON CONFLICT DO UPDATE SET` clause on purpose
   (`internal/telemetry/db/query.sql:343`, marked LOAD-BEARING), and
   `internal/telemetry/service.go:805` documents deliberately not reading them. They are
   a human-owned verification channel parked in the module that ingests machine
   observations — the exact boundary blur MAG-26 exists to remove, and the mirror image
   of what tiers 4 and 5 already fixed for `vehicle_snapshots`' `_calc` columns and
   `charge_gaps`.

2. **The verification UI that will fill those columns (backlog item 11) has no legal
   writer.** It would be a gateway → `charging` interaction that must reach into
   `telemetry`'s table. Under `ai/architecture.md` §2 that write has nowhere to live
   today; after this tier it lives in `charging`, next to `manual_charge_entries`, which
   already carries its own `start_battery_pct` / `end_battery_pct`.

3. **The percentages have no provenance constraint.** `supercharger_sessions` allows a
   percentage with a NULL `battery_pct_source`, and the one populated row in the live
   database is exactly that. A charging-owned table can require what telemetry never did.

## What Changes

**Expand only.** This tier adds `internal/charging`'s new `charge_sessions` table and
copies every existing Supercharger session into it. It **drops nothing**, changes **no
consumer**, and leaves `internal/telemetry` **completely untouched** — see design.md
**D4** for why a DROP in the same change would silently destroy the data, and **D9** for
the real (non-trivial) shape of the deferred contract change.

- **ADDED** — `internal/charging/db/migrations/20260823000001_add_charge_sessions.sql`:
  `CREATE TABLE charge_sessions` carrying identity (`account_id`, `vin`, `tesla_id`,
  `session_id`), the session's time window (`charge_start_date_time`,
  `charge_stop_date_time`), the session facts (`site_location_name`, `energy_kwh`,
  `total_cost`, `currency`, `is_paid`) and the five battery-percentage columns — plus one
  index and a one-time guarded backfill from `supercharger_sessions`. Deliberately **not**
  carried, a closed list: `country_code`, `unlatch_date_time`, `billing_type`,
  `vehicle_make_type`, `raw_data`.
- **ADDED** — `charging.SessionMirror`, `charging.SessionWriter` and
  `charging.NewSessionWriter` (a write-only port; deliberately no reader — design.md
  **D9**), plus `internal/charging/session_writer.go` and one new query in
  `internal/charging/db/query.sql`. `SessionMirror` carries every mirrored column and
  **no battery-percentage field at all**, so the sync path cannot clobber a human-verified
  value — a compile error rather than a comment (design.md **D6**).
- **ADDED** — `cmd/poller` mirrors each account's Supercharger sessions into
  `charge_sessions` after every successful collection cycle, reading them back through
  `telemetry.SuperchargerReader` (a public port) and writing them through
  `charging.SessionWriter` (a public port). No module reaches into another module's
  database at any point.
- **CHANGED** — `internal/charging/testdb_test.go` switches from `testdb.Provision` to
  `testdb.ProvisionDirs` so the backfill can be exercised against seeded source rows
  (design.md **D8**).
- **UNCHANGED** — `internal/telemetry` (not one file), `internal/analytics`,
  `internal/gateway`. No column, index, constraint, query, port or type is removed
  anywhere.

**Breaking:** no. Nothing is removed, no signature changes, no consumer is re-pointed.
The change is purely additive at every level — schema, Go API and behavior.

**Modules affected:** `charging` (owns everything new). `cmd/poller` gains one
orchestration step (composition root, leader-owned — outside every module sandbox).
`telemetry` is **read** through its existing public `SuperchargerReader` port by
`cmd/poller`, and is otherwise untouched.

**Known, accepted consequences of the gate's amendment** (full treatment in design.md
D1): four mirrored columns (`energy_kwh`, `total_cost`, `currency`, `is_paid`) are
rewritten at the source as fees settle, so they genuinely drift — reconciled by an
idempotent re-mirror running in the same nightly cycle that refreshes the source. And the
mirror uses telemetry's column names verbatim, so `internal/charging` now names the same
concepts two ways across its two tables (`energy_kwh` vs `energy_added_kwh`, `total_cost`
vs `price`), which makes backlog item 12's convergence a rename as well as a merge.

## Read paths affected

Per `openspec/config.yaml` §proposal ("performance-sensitive proposals must name the read
paths they affect"):

- **No existing read path changes.** Every current reader of the five percentage columns
  — `internal/analytics/consumed.go`'s `sumSuperchargerPctBetween` (the battery-consumed
  correction) and `inferMissingChargingType` (which produces the `charge_gaps` flags tier
  5 moved) — keeps reading `telemetry.SuperchargerSession` exactly as it does today.
  Their query plans, indexes and results are untouched.
- **One read path is *prepared*, not built:** the future re-point of that same
  consumed-per-day derivation onto `charge_sessions`. The table is shaped and indexed for
  it now — dense (one row per session, no join into `telemetry`) and indexed on
  `(account_id, tesla_id, charge_stop_date_time)`, leading with the stop instant because
  energy is fully delivered at session stop (roadmap **D12**). The gate's amendment makes
  that future read fully self-sufficient: energy and cost now live here too, so the
  re-point is expected to drop analytics' telemetry-session dependency outright rather
  than join two ports (design.md D9, step 4). See design.md §"Index Plan".
- **One write path is added:** the nightly mirror in `cmd/poller`, which runs immediately
  after the 03:30 collection cycle and rewrites every mirrored row on every pass
  (design.md D6 drops the change-detection predicate, deliberately). Per the project's
  `Performance-Profile`, a nightly write paying for a cheaper future read is the trade
  this platform wants.

## Impact

- **Affected specs:** `charge-session-log` (new capability, ADDED requirements only). No
  existing capability's spec changes — in particular `telemetry`'s does not, because
  nothing is removed from it.
- **Affected code:** `internal/charging/` (migration, `query.sql`, `charging.go`,
  `session_writer.go`, `db_integration_test.go`, `testdb_test.go`, `AGENTS.md`),
  `cmd/poller/main.go`, `ai/go-conventions.md` and the root `README.md` (docs-track-change).
- **Design gate:** this change trips the built-in **`database`** design gate. design.md
  carries the complete DDL, every constraint, the index plan justified against the
  declared read patterns, the backfill SQL and the goose Up/Down. **The owner confirmed it
  with the D1 amendment recorded above.**
- **Deferred, explicitly NOT in scope:** manual ↔ Supercharger convergence (roadmap D4 /
  backlog item 12); the verification UI that writes the percentages (backlog item 11);
  dropping the five percentage columns from `telemetry` (design.md **D9** describes what
  that actually costs); replacing `total_cost`'s `DOUBLE PRECISION` with `NUMERIC`
  (design.md D1 records why float money is a problem worth its own entry).
