## Why

Follow-up to MAG-6 / RM9, formerly numbered RM7 (`openspec/roadmaps/archive/RM9-history-graph-improvements/RM9-history-graph-improvements.md`,
archived 2026-08-11). RM9 fixed the history charts' date labeling and tooltip formatting; it left the
"Battery history" bar's *meaning* untouched — each bar still plots the **absolute battery level
%** captured that day (`buildBatteryChart` in `internal/gateway/handlers/history.go`).

The user wants the bar to plot **battery consumed that day** instead: the drop from the
previous day's level to the current day's, mirroring how "Odometer history" already plots
**km driven per day** rather than the cumulative odometer. Concrete example from the user:
08-07 was at 93%, 08-08 was at 69% → the 08-08 bar should read **24**.

Explicit user constraints carried into this design:

- **The tooltip does not change.** It stays `MM-DD · <level>% · <range> km range` — the
  snapshot's own absolute level and range — even though the bar now shows a delta. The user
  has been told, and accepts, that the bar and the tooltip will describe two different
  quantities after this change.
- **The odometer chart is untouched.** No file under this change may alter
  `buildOdometerChart`, `fragments.HistoryChart`'s odometer usage, or the odometer card markup.
- **The first bar must be a real delta**, computed against the day before it — never a stub,
  never a dropped/skipped bar.

## What Changes

Primary module: **`internal/gateway/`** only. No other `internal/` module is touched.

- **`buildBatteryChart` (`internal/gateway/handlers/history.go`) is rewritten to mirror
  `buildOdometerChart`'s established shape** (RD5, `2026-07-31-gateway-dashboard-history-charts`):
  take the `days+1` most recent snapshots already returned by the existing
  `SnapshotsByVehicleSince(..., since)` call — **no reader change, no new query, no DB
  object** — and compute `days` consecutive deltas `pts[i-1].BatteryLevelPct −
  pts[i].BatteryLevelPct` (operand order reversed from the odometer builder, because
  consumption is a *decrease* in level, not an increase in a monotonic counter).
- **Height scaling changes from "0–100 absolute" to "% of the day's max consumption"**,
  mirroring `buildOdometerChart`'s `maxKm` scaling, so the tallest bar in the window is always
  100% — consistent with how the odometer chart already scales.
- **The empty guard moves from `len(pts) == 0` to `len(pts) < 2`** — a single snapshot can no
  longer render a (meaningless, always-0%) bar; it now needs a pair to form a delta, matching the
  odometer builder's guard.
- **The tooltip string is generated exactly as before** — `MM-DD · <level>% · <range> km range`,
  built from the *current* snapshot's (`pts[i]`) own `BatteryLevelPct` / `BatteryRangeKm`, not
  from the delta. This is a deliberate, user-directed choice: see design.md D5 for the honest
  trade-off it accepts.
- **A charging day (battery level rose) still produces a bar.** This change provisionally clamps
  a negative computed delta to 0 — the same clamp the odometer builder already applies to its own
  anomaly case — so the chart never renders a negative bar. **This is not the final word**: see
  "Open Questions" in design.md. Clamping under-reports true consumption on any day the vehicle
  both drove and charged, and the user's own preferred fix (charge-aware "true consumption" using
  stored charge records) is deliberately **out of scope for this change** — it is multi-module and
  is recorded as a follow-up question, not resolved here.

No new database object, no new `telemetry.Reader` method, no new gateway route. The existing
`GET /ui/dashboard/history?days=N` endpoint, its `since = startOfDay(now).AddDate(0,0,-days)`
window (which already yields `days+1` daily snapshots — verified by reading `history.go`), and
its response contract are all unchanged; only the *interpretation* of the "Battery history" bars
changes.

## Capabilities

### Modified Capabilities

- **`gateway`** — the "Dashboard History Charts" requirement's Battery-history scenarios change:
  bars now represent battery **% consumed per day** (a clamped delta) instead of the absolute
  level; the tooltip's content and wording are unchanged; the empty-state threshold moves from
  "0 snapshots" to "fewer than 2 snapshots" for this chart specifically (the odometer chart's
  scenarios are unchanged and are not touched by this delta).

### Consumed Capabilities (no change to their specs)

- **`telemetry` — Snapshot History Read Port** (`SnapshotsByVehicleSince`) — same call, same
  parameters, same `days+1`-snapshot window already in use. No delta against the `telemetry`
  spec.

## Impact

- **Module:** `internal/gateway` only.
- **Files:** `internal/gateway/handlers/history.go` (`buildBatteryChart` rewritten;
  `buildOdometerChart` untouched), `internal/gateway/handlers/history_test.go` (or equivalent —
  new/updated table-driven cases), no `.templ` change expected (the chart markup already reads a
  pre-computed `fragments.HistoryChart`/`HistoryBar` view model generically; verify at
  implementation time whether the card's static copy needs a one-word update, e.g. "Battery
  history" → "Battery consumed" — left to the implementer, see tasks.md).
- **APIs:** `GET /ui/dashboard/history` — no route, parameter, or response-shape change; only the
  numeric values inside the existing `HistoryChart`/`HistoryBar` fields for the battery chart
  change meaning. Non-breaking at the HTTP level.
- **Read paths:** none added. The single existing read
  (`telemetry.Reader.SnapshotsByVehicleSince`, one call per history-fragment render, already
  bounded by the `days` preset set `{6, 14, 30}`) is unchanged in shape and volume.
- **Database:** **none.** No table, column, index, or migration. The `database` design gate does
  not trigger. (If implementation discovers a genuine need for a DB object, design.md says to stop
  and flag it — not design it silently.)
- **Breaking:** No.
- **Dependencies:** none added.

## Open Questions (not resolved here — see design.md)

Battery level **rises** on a charging day, which a naive two-snapshot delta cannot distinguish
from "the car didn't consume anything." Three candidate resolutions are recorded in design.md's
`## Open Questions`, none of which this change adopts as final: (1) clamp negatives to 0 — the
provisional behavior shipped by this change; (2) show the signed net change (bidirectional bars);
(3) charge-aware true consumption, reconstructed from stored charge records
(`internal/manualcharge` + `telemetry.SuperchargerReader`), mirroring the derivation
`internal/battery.RecentEfficiency` already performs. Option 3 is the user's preferred direction
but is a **multi-module change** (see design.md) and is deliberately deferred to a follow-up
grill-me / roadmap tier, per `openspec/config.yaml`'s "spans multiple modules → roadmap, not one
big change" rule.
