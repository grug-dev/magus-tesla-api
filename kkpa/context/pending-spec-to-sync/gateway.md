# Sync proposal — gateway

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `use-case/gateway/read-dashboard-history.md`
Source spec:  `openspec/specs/gateway/spec.md`
Generated:    2026-09-02
Status: PENDING REVIEW

Derived from the single requirement RM40 (ticket MAG-41) modified in this capability:
**"Dashboard History Charts"**. No other requirement in the spec changed.

Target is a **use-case** file, so per the skill's own rule only `## Input / output`,
`## Conventions & gotchas` and `## Related use cases` are proposed. `## Flow`, `## Database`
and `## Entry point` are left untouched — a spec carries behavior, not file paths, and
`--with-filemap` was not passed. **Note for the reviewer:** the guide's `## Flow` and
`## Database` sections still describe the battery chart reading
`telemetry.Reader.SnapshotsByVehicleBetween` with a `-1` day lookback. That is now wrong, but
correcting it needs the file map, so either re-run this with `--with-filemap` or fix those two
sections by hand when applying.

---

## [guide] ## Input / output — REPLACE

- **Input:** the session user id; `?start=` and `?end=` calendar dates (both omitted ⇒ a
  default 6-day window, `historyRangeWindowDays`); the session's selected vehicle;
  "browser today" from `browserToday(c)`.
- **Output:** the `"dashboard-history"` fragment — three bar charts (odometer km/day,
  battery %/day, consumed %/day) plus the window-preset selector, as
  `fragments.HistoryView`. `200` normally; `400` with the empty-state placeholder and **no**
  preset selector when the window is malformed.
- **All three charts read exclusively through `analytics.Reader`** — battery via
  `BatteryLevelByDay`, odometer via `OdometerDeltaByDay`, consumed via `ConsumedByDay`.
  Since RM40 the gateway does not name `internal/telemetry` anywhere, for any purpose.

## [guide] ## Conventions & gotchas — APPEND

- **The battery chart reads `analytics.Reader.BatteryLevelByDay`, NOT telemetry** — changed by
  RM40 (MAG-41). It previously read `telemetry.Reader.SnapshotsByVehicleBetween`. The rejected
  alternative was a gateway-local interface still backed by telemetry: that satisfies
  `make boundary-guard`'s letter while keeping the runtime dependency the guard exists to
  prevent. `internal/analytics` was chosen because it already owns the precomputed table the
  data lives in and already served the other two charts.
  _Source: spec gateway — Requirement: Dashboard History Charts._
- **There is NO gateway-level lookback for any of the three charts** — changed by RM40. The
  battery fetch used to compute `readStart = start - 1 day`; it no longer does, because
  `vehicle_metrics.metric_date` is already the effective day, so the port returns exactly
  `[start, end]`. Re-introducing a lookback would fetch a row nothing renders.
  _Source: spec gateway — Requirement: Dashboard History Charts._
- **A day the nightly recalculation watermark has not reached renders as the SAME empty bar as a
  day with no data at all** — an accepted, bounded, self-healing, typically single-day-wide gap
  introduced by reading the precomputed `vehicle_metrics` table instead of raw
  `vehicle_snapshots`. It is deliberately not backfilled, and it is **not a new UI state**: the
  bar is byte-identical to the pre-existing "no snapshot" one. Do not "fix" it by widening the
  window or falling back to raw snapshots.
  _Source: spec gateway — Requirement: Dashboard History Charts._
- **The three reads fail INDEPENDENTLY** — a battery-read error empties only the battery chart,
  an odometer-read error only the odometer chart, a consumed-read error only the consumed chart.
  None of the three may blank a sibling chart that already succeeded.
  _Source: spec gateway — Requirement: Dashboard History Charts._
- **Bucket on each port's returned date VERBATIM** — never re-project it through
  `effectiveDayUTC`. The port's date is already a final bucket key. This rule already governed
  the consumed and odometer charts; RM40 brought the battery chart under it too.
  _Source: spec gateway — Requirement: Dashboard History Charts._
- **The battery bar's value, its absolute 0–100 scale, and the absence of any delta or clamp are
  unchanged by RM40** — only the port it is read from changed. If a battery bar's rendering
  moves, that is a regression, not an intended consequence of the port swap.
  _Source: spec gateway — Requirement: Dashboard History Charts._
- **The i18n key `KeyHistoryNoSnapshotTooltip` now reads slightly wrong** — it says "no
  snapshot", but after RM40 an empty bar means "no precomputed metric row". Flagged
  deliberately, NOT renamed: renaming a bilingual catalogue key was out of the ticket's scope.
  _Source: spec gateway — Requirement: Dashboard History Charts._

## [index] ## Glossary & routing — entities — ADD ROWS

| `battery chart` | `buildBatteryChart` / `analytics.Reader.BatteryLevelByDay` (since RM40; previously `telemetry.Reader`) | entity | `use-case/gateway/read-dashboard-history.md` |
| `battery history chart` | synonym of `battery chart` | entity | `use-case/gateway/read-dashboard-history.md` |
