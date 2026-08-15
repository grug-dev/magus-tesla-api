Source: MAG-15 — https://linear.app/magus-monitor/issue/MAG-15/battery-consumed-graph
Roadmap: openspec/roadmaps/RM28-battery-consumed-graph.md
Tier: 1 of 4 (telemetry; tier 2 is `RM28-manualcharge-add-date-range-reader`, module
`manualcharge`; tier 3 is `RM28-battery-derive-consumed-per-day`, module `battery`; tier 4
is `RM28-gateway-add-consumed-graph`, module `gateway`)

## Why

MAG-15 wants a "how much battery did the car consume today" graph. The number requires
correcting the existing raw day-over-day battery delta (`battery_used_pct_calc`) for
charging that happened that day, using the charge records the platform already stores in
two places: `manual_charge_entries` (`internal/manualcharge`) and `supercharger_sessions`
(`internal/telemetry`). The derivation itself, and the decision to flag a day whose
corrected number still does not add up (D5/D5a), belong to `internal/battery` — tier 3 of
this roadmap — because `internal/battery` is already the platform's derived-metrics module
and already composes exactly the four ports this needs (D15).

This tier — the telemetry side — supplies the two pieces `internal/battery` cannot supply
itself:

1. **Somewhere durable to record a flagged day.** A day whose battery math does not add up
   is a signal that a charge record is missing or incomplete; a later ticket will notify the
   user about it. `internal/battery` computes the signal but must not own its own database
   package (no domain module in this codebase does; `internal/battery` in particular is
   explicitly a "pure read-side derivation" with "no database and no store" —
   `internal/battery/battery.go:9-11`). The owner's call (D3, against the assistant's own
   recommendation) is for `internal/telemetry` to own this table and expose a write port,
   reusing telemetry's existing persistence rather than giving `internal/battery` a `db/`
   folder of its own. No import cycle results: `battery → telemetry` already flows one way
   (root `README.md` §Dependency graph, LAYER 2), and this port keeps that direction —
   telemetry never calls battery (D4a).
2. **A date-range read of Supercharger sessions.** `internal/battery`'s derivation needs
   every Supercharger session whose energy finished landing inside the window it is
   recomputing, so it can sum `end_battery_pct − start_battery_pct` across all of them (D13)
   and detect the `SUPERCHARGER`-type gap (D7a: a session exists that day with NULL
   percentages). The two existing `SuperchargerReader` methods are limit-based ("most recent
   N sessions") — with a limit there is no safe number to guess for an unbounded window
   (D9). `telemetry.Reader.SnapshotsByVehicleBetween` already exists and is reused as-is by
   tier 3 — it is **not** touched by this change.

## What Changes

- **New table `charge_gaps`**, owned by `internal/telemetry/db`: one row per
  `(account_id, tesla_id, gap_date)` (UNIQUE), columns `vin`, `tesla_id`, `gap_date`,
  `missing_charging_type` (`MANUAL` | `SUPERCHARGER`, CHECK-constrained) all `NOT NULL`,
  plus `created_at`/`updated_at` (D7). `tesla_id` is `NOT NULL` here even though it is
  nullable on `supercharger_sessions` — a session that cannot be attributed to a registered
  vehicle is skipped by detection entirely and can never reach this table.
- **New write port `telemetry.GapWriter`** with one method, `ReconcileWindow`: given a
  vehicle, a `[start, end]` window, and the full set of currently-flagged days for that
  window, it upserts every day still flagged and deletes every previously-stored day in the
  window that is no longer flagged (D7b). No `resolved_at`, no soft delete — the lifecycle is
  entirely upsert-or-delete, driven by what the caller recomputed. `internal/battery` (tier
  3) is the port's only caller; `internal/battery` writes to it, `internal/telemetry` never
  reads or calls `internal/battery` (D3/D4a).
- **New domain types** `ChargeGap` and `MissingChargingType` (with
  `MissingChargingTypeManual`/`MissingChargingTypeSupercharger` constants) in `telemetry.go`
  — our own models, no vendor suffix (`ai/architecture.md` §6).
- **New read method on the existing `SuperchargerReader` port**:
  `SuperchargerSessionsByVehicleBetween(ctx, accountID, teslaID, start, end)
  ([]SuperchargerSession, error)`, filtering on `charge_stop_date_time` — not
  `charge_start_date_time` — per D12: energy is fully delivered at session stop, which is
  what `end_battery_pct` corresponds to, so a session belongs to the window containing its
  stop instant even when it started the day before (a session spanning midnight is
  deliberately included). Purely additive: `SuperchargerSessionsByAccount` and
  `SuperchargerSessionsByVehicle` are unchanged.
- **One new migration** (`internal/telemetry/db/migrations/20260815000002_add_charge_gaps.sql`)
  creating `charge_gaps` and one supporting index. No existing table, column, index, or
  query is altered.

## Breaking

**No.** Purely additive:

- A new table with no foreign key into any existing table (or any other module's schema).
- A new port (`GapWriter`) and two new domain types — nothing existing changes shape.
- `SuperchargerReader` gains a third method; its two existing methods and every existing
  caller of them are untouched. `SuperchargerSession` itself gains no new field.
- No change to any migration, query, or Go file authored before this change, other than the
  additive query and interface-method additions this proposal introduces.

## Modules Affected

- **`internal/telemetry/`** — sole module touched: one migration, `telemetry.go` (new
  domain types + `GapWriter` interface + `SuperchargerReader` method addition), a new
  `gap_writer.go` (the `GapWriter` implementation), `db/query.sql` (four new queries:
  `UpsertChargeGap`, `DeleteChargeGap`, `ChargeGapDatesByVehicleBetween`,
  `SuperchargerSessionsByVehicleBetween`), `mapping.go` (one small `pgtype.Date → time.Time`
  helper), `AGENTS.md`, new DB-integration tests.
- No other `internal/` module. `internal/battery` — this port's only intended caller — is
  **not created, read, or touched** by this change; it is a separate tier (T3,
  `RM28-battery-derive-consumed-per-day`) with its own proposal, and it already exists in
  this repo today for an unrelated efficiency metric (`internal/battery/battery.go`) —
  nothing in that existing code is read or modified here.
- `internal/manualcharge` — its own date-range reader is tier 2 of this roadmap
  (`RM28-manualcharge-add-date-range-reader`), a separate proposal. Not touched here.
- `internal/gateway` — no gateway change. Tier 4 of this roadmap.

## Database Changes

One migration on a new table (`charge_gaps`, owned solely by `internal/telemetry/db`): full
schema, one supporting index beyond the UNIQUE constraint's own implicit index, no foreign
keys, no change to any existing table. **design.md is REQUIRED** (this change creates a new
table) and includes: the full `Up`/`Down` DDL with the module's comment conventions; the
rationale for one-row-per-vehicle-day vs one-row-per-(day,type); the rationale for
delete-on-resolve vs a `resolved_at` column; the rationale for `tesla_id NOT NULL` here vs
nullable on `supercharger_sessions`; the rationale for omitting a cross-module FK (mirroring
`manual_charge_entries`' identical precedent); and an index plan justified against the two
declared read patterns (the nightly reconciliation, and the future account-wide
notification query) — stating which index serves which and why the UNIQUE constraint's own
index does or does not suffice for each. The database design gate applies; this proposal
does not itself constitute approval — the user reviews and confirms design.md before
implementation is dispatched.

## Read Paths Affected

- **`SuperchargerReader.SuperchargerSessionsByVehicleBetween`** (new): a bounded date-range
  scan under the per-vehicle index's `(account_id, tesla_id)` prefix, filtered post-scan on
  `charge_stop_date_time` (no new index — justified in design.md's Index Plan). Called once
  per vehicle per nightly `internal/battery` run over a window capped by the same 90-day
  HTTP date-filter convention the rest of the platform already uses
  (`ai/go-conventions.md` §"HTTP date-filter convention"). Not called on any user-facing
  request path in this tier (tier 3/`internal/battery` is the only caller; no gateway
  handler calls it directly).
- **`GapWriter.ReconcileWindow`** is a **write** path, not a read path, and runs only inside
  the nightly poller (D4/D4a) — never on a dashboard request. Its own internal read (the
  existing-dates lookup needed to compute what to delete) is scoped to one vehicle's rows in
  a bounded window via the UNIQUE constraint's own index; see design.md.
- No existing read path (`LatestSnapshotsByAccount`, `SnapshotsByVehicleSince`,
  `SnapshotsByVehicleBetween`, `SuperchargerSessionsByAccount`,
  `SuperchargerSessionsByVehicle`) changes in shape, query plan, or cost.

## Capabilities

### Added Capabilities

- **`telemetry`** — adds a new "Charge Gap Ledger" capability: the ability to durably record
  and reconcile, per vehicle-day, that a charge record is missing or incomplete, via a
  dedicated write port consumed by `internal/battery`. See `specs/telemetry/spec.md`.

### Modified Capabilities

- **`telemetry`** — extends "Supercharger Session Read Port" with a third, date-range read
  method (`SuperchargerSessionsByVehicleBetween`), filtered on stop time per D12. See
  `specs/telemetry/spec.md`.

### Out of scope (explicitly deferred)

- **A read port for `charge_gaps`** (e.g. "outstanding gaps for this account"). The future
  notification feature that will need it is not part of this roadmap; this tier ships
  storage and the write path only, matching the roadmap's own framing ("that is recorded
  durably so a later ticket can notify the user").
- **The consumed-per-day derivation and gap-detection logic itself** (D5, D5a, D7a's
  inference rule, D12's charge-matching, D13's summation formula) — all `internal/battery`'s
  job, tier 3 (`RM28-battery-derive-consumed-per-day`).
- **`cmd/poller` wiring** that calls the derivation and `ReconcileWindow` together (D4, D4a)
  — tier 3.
- **`manualcharge.ListEntriesByVehicleBetween`** — tier 2
  (`RM28-manualcharge-add-date-range-reader`), a separate proposal, separate module.
- **Any gateway change** — tier 4 (`RM28-gateway-add-consumed-graph`).
- **Any change to `vehicle_snapshots`, its five `_calc` columns, or snapshot attribution.**
  The roadmap's original tier 1 (`RM28-telemetry-shift-consumption-attribution`) proposed
  exactly this and was **dropped before any implementation**, once `EffectiveDate` was found
  to already provide the attribution (roadmap D1). Do not re-propose it here or elsewhere in
  this tier.

## Resolved decisions

D1–D16 were settled with the owner via grill-me before this proposal was written (recorded
verbatim in `openspec/roadmaps/RM28-battery-consumed-graph.md`, 2026-08-15; D16 supersedes
D1/D9/D9a as noted in that file's own decision table — the roadmap's D1/D9/D9a rows already
carry their "Corrected 2026-08-15" text, so there is nothing further to reconcile here).
This tier implements D3, D7, D7a, D7b (the table and write port) and D9/D12 (the new read
method). D1, D2, D4, D4a, D5, D5a, D6, D8, D10, D11, D13, D14, D15 bound what this tier must
**not** do — see "Out of scope" above. design.md restates the directly-relevant decisions
with full schema/rationale, not re-litigated.
