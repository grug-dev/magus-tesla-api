Source: MAG-15 — https://linear.app/magus-monitor/issue/MAG-15/battery-consumed-graph
Roadmap: openspec/roadmaps/RM28-battery-consumed-graph.md
Tier: 3 of 4 (battery; tier 1 is `RM28-telemetry-add-charge-gap-storage`, module
`telemetry`, already archived; tier 2 is `RM28-manualcharge-add-date-range-reader`,
module `manualcharge`, already archived; tier 4 is `RM28-gateway-add-consumed-graph`,
module `gateway`)

## Why

MAG-15 wants a "how much battery did the car consume that day?" graph. The raw
day-over-day delta already stored on `telemetry.Snapshot` (`BatteryUsedPctCalc`) cannot
answer it alone: any day the vehicle charged produces a meaningless or negative number,
because the battery went up from charging while also going down from driving. Correcting
for it needs summing every charge event's `end_battery_pct − start_battery_pct` across
**both** places the platform stores charge records — `supercharger_sessions`
(`internal/telemetry`, tier 1) and `manual_charge_entries` (`internal/manualcharge`, tier
2) — and adding that back to the raw delta (D13). Both date-range read ports tier 3 needs
already shipped in tiers 1–2; this tier is the derivation itself.

`internal/battery` is the platform's derived-metrics module and already composes exactly
the dependencies this needs (`telemetry.Reader`, `telemetry.SuperchargerReader`,
`manualcharge.Reader`) via its existing `NewReader` constructor (D15) — no new module, no
new database, no new dependency wired into the constructor.

A day whose corrected number does not add up (negative, or zero despite real driving) is
itself a signal that a charge record is missing or incomplete. `internal/telemetry`'s
`charge_gaps` ledger (tier 1) exists to store that signal durably; this tier computes it
and hands it to `cmd/poller`, the composition root, to write via `telemetry.GapWriter`
(D3/D4/D4a — telemetry never calls battery).

## What Changes

- **New method on the existing `battery.Reader` port**: `ConsumedByDay(ctx, accountID,
  teslaID, start, end time.Time) ([]DayConsumption, error)`. Recomputed on every call, no
  cache (D2). Reuses `NewReader`'s existing four dependencies as-is — **no constructor
  signature change**, since `ConsumedByDay` needs only the three ports the reader already
  holds (`telemetry.Reader`, `telemetry.SuperchargerReader`, `manualcharge.Reader`); it
  does not need the account/vehicle-lookup dependency `RecentEfficiency` uses.
- **New domain type `DayConsumption`**: one entry per calendar day that has a computable
  value — `Date`, `ConsumedPct` (D13's formula, raw/unrounded, may be negative),
  `DistanceKm`, `Flagged` (D5/D5a), `MissingChargingType` (reuses
  `telemetry.MissingChargingType`, D7a, valid only when `Flagged`), `DaysSpanned` (D8). A
  calendar day with **no** computable value (no snapshot at all, or the account's
  first-ever snapshot per D5a) is represented by its **absence** from the returned slice
  — see design.md D-B2 for why that is the right "no-data" signal, not an extra field.
- **New file `internal/battery/consumed.go`**: the pure derivation
  (`deriveConsumedByDay`) plus its charge-matching helpers
  (`sumSuperchargerPctBetween`, `sumManualPctBetween`, `inferMissingChargingType`), fully
  offline, zero I/O — mirrors the existing `derive.go` pattern for `RecentEfficiency`.
- **`reader.go` addition**: `(*reader).ConsumedByDay`, fetching `telemetry.Snapshot`s over
  `[start−1, end]` (D9a), Supercharger sessions and manual entries over the matching
  windows, then calling `deriveConsumedByDay`.
- **New exported constant `battery.GapReconciliationWindow`** (30 days) — the rolling
  window `cmd/poller` re-derives and reconciles against `charge_gaps` every nightly run.
- **`cmd/poller` wiring (LEADER-OWNED, outside this module's sandbox)**: after
  `collector.CollectAll` writes the night's snapshots, loop
  `account.AllRegisteredVehicles`, call `battery.Reader.ConsumedByDay` for
  `[yesterday−29, yesterday]`, filter the flagged days into `[]telemetry.ChargeGap`, and
  call `telemetry.GapWriter.ReconcileWindow` — per-vehicle error isolation, mirroring
  `CollectAll`'s existing pattern (D4/D4a). Specified precisely in design.md; implemented
  by the leader, not this module's worker.
- **`internal/battery/AGENTS.md` update**: document `ConsumedByDay`/`DayConsumption` in
  the module's public-interface section.

## Breaking

**No.** Purely additive: one new interface method, one new domain type, one new exported
constant, one new file. `RecentEfficiency`, `Efficiency`, `NewReader`'s signature,
`derive.go`, and `capacity.go` are all untouched.

## Modules Affected

- **`internal/battery/`** — sole module touched by this worker's dispatch:
  `battery.go` (`Reader` interface addition, `DayConsumption` type, `GapReconciliationWindow`
  constant), `consumed.go` (new — pure derivation), `reader.go` (`ConsumedByDay`
  implementation), `consumed_test.go` (new — offline unit tests), `reader_test.go`
  (extended — port-wiring tests, plus un-panicking the three `...Between` fake methods
  tiers 1–2 already added defensively), `AGENTS.md`.
- **`cmd/poller/`** — wiring only (LEADER-OWNED, not this dispatch): construct
  `telemetry.SuperchargerReader`, `manualcharge.Reader`, `telemetry.GapWriter`,
  `battery.Reader`; call `ConsumedByDay` then `GapWriter.ReconcileWindow` per vehicle
  after each nightly collection cycle. See design.md's "cmd/poller wiring" section for the
  exact contract.
- **No other module.** `internal/telemetry` and `internal/manualcharge` are read-only
  dependencies (their ports already exist, archived tiers 1–2); neither's code changes
  here. `internal/gateway` is untouched — tier 4.

## Database Changes

**None.** `internal/battery` owns no database (D15) and this tier adds no table, column,
index, or migration. `ConsumedByDay` is recomputed on every call from three existing read
ports (D2) — no cache table, matching the roadmap's explicitly rejected
precomputed-`battery_consumed` alternative. The `database` design gate therefore does not
trip; design.md states this explicitly per the project's design rule that any DB-touching
change (even a "none" conclusion) carries the reasoning.

## Read Paths Affected

- **`battery.Reader.ConsumedByDay`** (new): three bounded-window reads
  (`telemetry.Reader.SnapshotsByVehicleBetween`,
  `telemetry.SuperchargerReader.SuperchargerSessionsByVehicleBetween`,
  `manualcharge.Reader.ListEntriesByVehicleBetween`) per call, each already indexed and
  capped by its own caller-supplied window (tiers 1–2). Two intended callers: the
  dashboard's consumed-graph fragment (tier 4, request-time) and `cmd/poller`'s nightly
  gap reconciliation (batch, off-hours) — both read-only from `internal/battery`'s
  perspective; the write to `charge_gaps` happens one layer up, in `cmd/poller` via
  `telemetry.GapWriter`, never inside this module.
- No existing `battery.Reader` read path (`RecentEfficiency`) changes in shape, query
  plan, or cost.

## Capabilities

### Modified Capabilities

- **`battery`** — adds a new requirement, per-day battery-consumed derivation with gap
  detection, to the capability's read surface. See `specs/battery/spec.md`.

### Out of scope (explicitly deferred)

- **Rendering** — the bar chart, flagged-day visualization (D10), and multi-day-span
  marker are `internal/gateway`'s job, tier 4 (`RM28-gateway-add-consumed-graph`).
- **`parseHistoryRange`'s default/cap move to yesterday (D11)** — gateway-only, tier 4.
- **Any change to `telemetry.GapWriter`, `charge_gaps`'s schema, or
  `SuperchargerSessionsByVehicleBetween`/`ListEntriesByVehicleBetween`'s own contracts** —
  all shipped as-is in tiers 1–2 and consumed here unmodified.
- **Reviving Supercharger battery-% estimation** (D14) — explicitly out of scope; this
  tier accepts D14a's known under-reporting limitation and documents it, never engineers
  around it.

## Resolved decisions

D1–D15 were settled with the owner via grill-me before this proposal was written
(recorded verbatim in `openspec/roadmaps/RM28-battery-consumed-graph.md`, 2026-08-15).
This tier implements D1 (consumes `EffectiveDate` as-is, no shift), D2 (no cache), D4/D4a
(poller orchestration contract), D5/D5a (gap detection), D7a (inferred type, computed
here and handed to the writer), D8 (multi-day spans), D9a (the `[start−1, end]` lookback
fetch), D12 (source-specific charge matching), D13 (the summation formula), D14/D14a
(accepted, documented limitation), D15 (battery owns the derivation). D3, D6, D7, D7b, D9,
D10, D11 bound what this tier must **not** re-litigate or re-implement — see design.md's
own decisions for how D6's poller-zone note reconciles with D1's `EffectiveDate`-is-final
correction. design.md restates the directly-relevant decisions with full rationale, not
re-litigated here.
