# RM40 — Drop the Gateway's Dependency on `internal/telemetry`

Source ticket: MAG-41 — https://linear.app/magus-monitor/issue/MAG-41/fix-boundary-guard

## Intention

`make boundary-guard` passes clean, with **zero** `// boundary:allow:` escape hatches
added. The gateway stops naming `internal/telemetry` at all — no import, no
`telemetry.Reader` field, no `telemetry.Snapshot` type — and gets the battery-history
data it needs from `internal/analytics`, the module that already owns the table holding
that data.

## Status

**COMPLETE.** Both tiers implemented, reviewer-approved and archived on 2026-09-02.
`make boundary-guard` passes clean with zero `// boundary:allow:` escape hatches, and
`grep -rn "internal/telemetry" internal/gateway/` returns nothing — the roadmap's stated
intention, met exactly.

## Findings that shaped this roadmap (read before touching anything)

Established by reading the real code, not by reasoning about it.

| Finding | Evidence | Consequence |
|---|---|---|
| Only **ONE** production read remains | `internal/gateway/handlers/history.go:321` is the sole `telemetryReader.*` call site in the whole gateway | The change is one call, not a migration — **D1** |
| The other 5 guard hits are **type leakage**, not reads | `gateway.go:44`, `handlers.go:61,119` (port fields); `history.go:462,470` (`telemetry.Snapshot` param + map); 2 `_test.go` fakes | They all vanish once the single call moves — **D2** |
| The gateway consumes exactly **3 fields** | `buildBatteryChart` uses only `EffectiveDate`, `BatteryLevelPct`, `BatteryRangeKm` | The new port is tiny — **D3** |
| `vehicle_metrics` already stores all 3, `NOT NULL` | `20260821000001_add_vehicle_metrics.sql:54,56` + `metric_date`; original columns, **not** nullable RM38 additions | **No migration, no new column** — **D4** |
| The existing UNIQUE index already serves the query | `UNIQUE (account_id, tesla_id, metric_date)` at line 83 — the same index `ConsumedByDay` / `OdometerDeltaByDay` rely on | **No new index** — **D5** |

The fourth and fifth findings are why this is a refactor and not a data change: there is
**no database object** to create or alter, so the `database` design gate has nothing to
approve. The only new artifact is a `SELECT`.

## Decisions (binding — settled with the owner before any artifact was written)

**D1 — `internal/analytics` owns the read.** Rejected: keeping the read on
`internal/telemetry` behind a gateway-local interface. That satisfies the guard's letter
while leaving the gateway depending on telemetry at runtime — the guard exists to stop
exactly that. `analytics` already owns `vehicle_metrics` and already serves the two
sibling history charts, so this is the module the data lives in, not merely a module that
can hold an interface.

**D2 — One new method on the existing `analytics.Reader` port**, not a new port. Name:
`BatteryLevelByDay(ctx, accountID, teslaID, start, end) ([]DayBattery, error)`. It mirrors
`ConsumedByDay` / `OdometerDeltaByDay` exactly — same signature shape, same
precomputed-and-sparse contract, same non-nil-empty-slice rule. The result struct is
`DayBattery`, completing the existing closed vocabulary `DayConsumption` / `DayDistance` /
`DayBattery`. Reusing the established names is the point: an agent looks the pattern up
once instead of re-inventing it.

**D3 — The new query carries NO `IS NOT NULL` filter**, unlike both its siblings. They
filter because their columns are NULL on a predecessor-less day. `battery_level_pct` and
`battery_range_km` are `NOT NULL` raw per-day observations with no predecessor
requirement, so filtering would silently hide a vehicle's first tracked day from the
battery chart for no reason.

**D4 — `DayBattery.Date` is a FINAL bucket key.** The gateway buckets on it verbatim and
must never re-project it through `effectiveDayUTC` — the identical rule
`buildConsumedChart` and `buildOdometerChart` already follow (design D18/D-G2).

**D5 — The 1-day lookback is DROPPED.** `buildHistoryView`'s
`readStart := start.AddDate(0, 0, -1)` exists only because the raw snapshot read had to
convert `captured_at` → `EffectiveDate`. `metric_date` is already the effective day, so
`BatteryLevelByDay(start, end)` returns exactly `[start, end]`. Keeping the lookback would
fetch a row nobody renders and preserve a comment explaining a conversion that no longer
happens.

**D6 — The day-coverage difference is ACCEPTED and documented, not backfilled.**
`vehicle_metrics` holds a row per day analytics recalculated; `vehicle_snapshots` holds
every raw capture. A day with no metric row renders as the existing empty
"no snapshot" bar — byte-identical to what a missing snapshot already produces. In
practice coverage tracks snapshots 1:1, lagging only by the recalculator watermark, so the
visible effect is bounded to a recent day briefly showing an empty bar. Rejected: adding a
backfill tier, which would turn a clean refactor into a DB-touching data change.

**D7 — Unit tests: NEW tests excluded; EXISTING tests repaired.** The owner's standing
default is no new unit tests. But `handlers_test.go` and `history_test.go` stop compiling
the moment `telemetry.Reader` leaves `Deps`, so reworking their fakes onto the analytics
port is in scope — it is repair, not new coverage. No brand-new test cases beyond keeping
the suite compiling and green.

**D8 — Zero escape hatches.** `make boundary-guard` must pass with no
`// boundary:allow:` comment added anywhere. The guard pattern is not to be widened.

## Tiers

Status legend: `[ ]` pending (change not created) · `[~]` in progress (change created, not
archived) · `[x]` done (archived).

| Status | Change | Module | Scope | depends_on | Proposal prompt |
|---|---|---|---|---|---|
| `[x]` | `RM40-analytics-add-battery-level-read` | `analytics` | Add `BatteryLevelByDay` to the `Reader` port: the `DayBattery` struct in `analytics.go`, the `VehicleMetricsBatteryByVehicleBetween` query in `db/query.sql` (+ `sqlc generate`), the store seam and reader method in `reader.go`, and the row→domain mapping. No migration, no new index, no schema change. | — | *(artifacts produced in this run)* |
| `[x]` | `RM40-gateway-drop-telemetry-dependency` | `gateway` | Consume `BatteryLevelByDay` in `buildHistoryView` (dropping the `-1` lookback, D5); retype `buildBatteryChart` off `telemetry.Snapshot` onto `analytics.DayBattery`; delete `TelemetryReader` from `gateway.Deps`, `handlers.Deps` and the `Handler` struct; remove the `cmd/web/main.go:58` injection (leader-owned cross-module wiring — lines 72 and 86 keep their `telemetry.NewReader`, they are not gateway); rework the two `_test.go` fakes onto the analytics port (D7). Verify `make boundary-guard` passes with zero escape hatches (D8). | 1 | Generate the OpenSpec proposal for removing every `internal/telemetry` reference from `internal/gateway/`, consuming the `analytics.Reader.BatteryLevelByDay` port added in tier 1. Binding decisions: D2, D4, D5, D7, D8 of this roadmap. |

Both tiers commit to the single shared branch
`ft/RM40-MAG-41-gateway-drop-telemetry-dependency`.

## Future work

None descoped. One observation worth recording but explicitly **not** actioned here: the
i18n key `KeyHistoryNoSnapshotTooltip` reads "no snapshot", which after this change means
"no metric row". The wording stays as-is — renaming a bilingual catalogue key is a
separate concern and the owner did not ask for it.
