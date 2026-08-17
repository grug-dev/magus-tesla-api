Source: MAG-15 — https://linear.app/magus-monitor/issue/MAG-15/battery-consumed-graph
Roadmap: openspec/roadmaps/RM28-battery-consumed-graph.md
Tier: 4 of 4 (gateway; tier 1 `RM28-telemetry-add-charge-gap-storage` / tier 2
`RM28-manualcharge-add-date-range-reader` / tier 3
`RM28-battery-derive-consumed-per-day` are all archived — this is the final tier)

## Why

MAG-15 wants a "how much battery did the car consume that day?" graph on
`/ui/dashboard/history`, next to the existing odometer and battery-level charts. Tier 3
(`internal/battery`) already computes the corrected per-day figure and exposes it as
`battery.Reader.ConsumedByDay(ctx, accountID, teslaID, start, end) ([]DayConsumption, error)`
— recomputed on read, sparse (one entry per computable day), each entry carrying `Date`,
`ConsumedPct`, `DistanceKm`, `Flagged`, `MissingChargingType`, `DaysSpanned`. This tier's
entire job is to render it: consume the port, never re-derive the math, and never touch a
database.

Two behavior changes ride along, both mandated by the roadmap and scoped to this module
only:

- **D10** — a flagged day (a day whose corrected number does not add up, per tier 3's D5/D5a)
  must never show its raw/negative value and must never be silently dropped from the axis —
  it renders as a zero-height bar with its own distinct warning marker.
- **D11** — `parseHistoryRange`'s default (both `start`/`end` absent) window and its
  `end <= today` cap both move from "browser-today" to "browser-yesterday", matching what
  the dashboard's self-load and preset buttons already do. Today's `EffectiveDate`/bucket row
  is not captured until tomorrow's ≈03:30 poll, so an `end = today` window can only ever
  produce an empty trailing bar on every chart, including the two that already exist.

## What Changes

- **New third chart panel, "Battery consumed" (`v.Consumed`)**, added to
  `fragments.HistoryView` and rendered by `DashboardHistoryContent` alongside the existing
  Odometer/Battery cards, on the SAME fixed `[start..end]` calendar-day axis.
- **New `buildConsumedChart` handler function** (`internal/gateway/handlers/history.go`),
  mirroring `buildOdometerChart`/`buildBatteryChart`'s shape: pre-computes every bar's
  `HeightPct`, `Tooltip`, `Label`, `Present`, and a new `Marker` field — the template does no
  arithmetic. Keys directly on `battery.DayConsumption.Date` — **never** re-bucketed through
  `effectiveDayUTC` (D18/D18a: `internal/battery` buckets in the poller's zone, the two
  existing charts still bucket in UTC; this tier keys the new chart on the port's own final
  bucket day and does not attempt to reconcile the two, per explicit owner instruction).
- **`buildHistoryView` extended** with a second, independent read:
  `h.batteryReader.ConsumedByDay(ctx, uid, teslaID, start, end)`. A `battery.Reader` error
  degrades only `v.Consumed` to empty — it does NOT wipe the odometer/battery charts, which
  already succeeded from the pre-existing `telemetryReader` read.
- **New `battery.Reader` dependency wired into the gateway module**: `Deps.BatteryReader
  battery.Reader` added to both `gateway.Deps` (`internal/gateway/gateway.go`) and
  `handlers.Deps`/`Handler` (`internal/gateway/handlers/handlers.go`), forwarded exactly like
  the existing `SuperchargerReader`/`ManualChargeReader` ports. **`cmd/web`'s actual
  construction of `battery.NewReader(...)` and its `gateway.Deps{BatteryReader: ...}`
  wiring is OUTSIDE `internal/gateway` and therefore outside this dispatch's sandbox** — it is
  a leader/integration task (see tasks.md).
- **`parseHistoryRange` (D11)**: computes `yesterday := today.AddDate(0,0,-1)` once and uses
  it, not `today`, for both the both-absent default window's `end` and the `end <= X` cap
  validation. `today`'s existing role (browser-local calendar day, resolved once per request
  via `browserToday(c)`) is unchanged — only what the default/cap compare against moves by
  one day. `buildHistoryPresets` and `defaultHistoryHref` are UNCHANGED — both already target
  browser-yesterday, so after this change the direct-API default and the dashboard's own
  self-load/preset windows are IDENTICAL for the first time (closing a documented divergence
  in the current spec — see "Breaking" below).
- **New `HistoryBar.MarkerFlagged`/`MarkerSpan bool` fields** (independent, not a
  single-valued enum — a bar can carry both markers at once per roadmap **D21**, the owner's
  ruling that a day both flagged and a multi-day span shows BOTH facts, never one hiding the
  other) and two small independent marker-chip additions to the shared `historyBarChart`
  Templ component — reused by the new chart only; existing odometer/battery bars leave both
  new fields at their zero value (`false`) and render exactly as before (additive, no visual
  change to the two existing charts).
- **New `formatPctRaw` helper** (`internal/gateway/handlers/format.go`), mirroring
  `formatKmRaw`: one-decimal-place percentage string, no `%` suffix (the catalogue format
  string supplies it), no thousands grouping needed at this magnitude.
- **Seven new i18n catalogue keys** (`internal/gateway/i18n/catalog.go`), both ES/EN
  non-empty: chart title, four tooltip shapes (normal / flagged / span / span+flagged), two
  charge-type labels (manual / Supercharger), and one no-data tooltip distinct from the
  existing `KeyHistoryNoSnapshotTooltip` (see design.md D-G6 for why a new key, not reuse).
- **`internal/gateway/AGENTS.md` update**: document the `battery.Reader` dependency in the
  module's public-interface section, alongside the existing `TelemetryReader`/
  `SuperchargerReader`/`ManualChargeReader` entries.

## Breaking

**No new HTTP contract break** — `GET /ui/dashboard/history` keeps its existing
`?start=&end=` params and response shape (one more chart card, additive markup). But D11 IS
a **behavior change** on the endpoint's existing default/validation, and must be called out
as such:

- A direct API call with no `start`/`end` params, and any explicit request with
  `end = browser-today`, **currently succeeds** (default window ends today; cap accepts
  `end == today`). **After this change, both are rejected/shifted**: the no-params default
  now ends at browser-yesterday, and `end = browser-today` now fails the cap with HTTP 400
  (only browser-yesterday-or-earlier is accepted).
- This resolves a divergence the CURRENT synced spec documents explicitly (`openspec/specs/
  gateway/spec.md`, "Scenario: Dashboard self-load and presets default to the browser's local
  yesterday" — its closing bullet states the direct-API default is unchanged and still ends
  at browser-today). That bullet becomes false after this change and is corrected in the spec
  delta.
- No stored data, migration, or API caller outside this project is known to depend on the
  `end = today` acceptance; the dashboard's own UI never issued that request in the first
  place (self-load/presets already targeted yesterday). Existing tests in
  `internal/gateway/handlers/history_test.go` that assert `end = today` is default/accepted
  must be updated to assert `end = yesterday` instead (see tasks.md).

## Modules Affected

- **`internal/gateway/`** — the ONLY module touched by this worker's implementation
  dispatch: `gateway.go` (`Deps.BatteryReader`), `handlers/handlers.go`
  (`Deps.BatteryReader`, `Handler.batteryReader`, `New()`), `handlers/history.go`
  (`parseHistoryRange` D11, `buildConsumedChart`, `buildHistoryView` extension),
  `handlers/format.go` (`formatPctRaw`), `templates/fragments/history_vm.go`
  (`HistoryView.Consumed`, `HistoryBar.MarkerFlagged`, `HistoryBar.MarkerSpan`),
  `templates/fragments/history.templ` (two independent marker-chip blocks, third
  `ui.Card`), `i18n/catalog.go` (seven new keys), `handlers/history_test.go` (D11 test
  updates + new consumed-chart tests), `AGENTS.md`.
- **`cmd/web/`** — wiring only (LEADER-OWNED, NOT this dispatch's sandbox): construct
  `battery.NewReader(telemetry.NewReader(pool), telemetry.NewSuperchargerReader(pool),
  manualcharge.NewReader(pool), acct, battery.DefaultWindow)` (mirrors `cmd/poller/main.go`'s
  existing identical construction) and pass it as `gateway.Deps.BatteryReader`. See
  design.md's "cmd/web wiring" section and tasks.md for the exact diff.
- **No other module.** `internal/battery`, `internal/telemetry`, and `internal/manualcharge`
  are read-only dependencies through already-shipped ports (tiers 1–3); none of their code
  changes here.

## Database Changes

**None.** `internal/gateway` owns no database and this tier adds no table, column, index, or
migration. The new chart is populated by calling `battery.Reader.ConsumedByDay` — a port that
itself is recompute-on-read with no cache (tier 3's D2) — so there is nothing to design at
the persistence layer. The `database` design gate therefore does not trip; design.md states
this explicitly per the project's design rule that any DB-touching conclusion (even "none")
carries its reasoning.

## Read Paths Affected

- **`battery.Reader.ConsumedByDay`** (new caller): one additional bounded read per
  `/ui/dashboard/history` render, on top of the existing
  `telemetry.Reader.SnapshotsByVehicleBetween` call. `ConsumedByDay` itself performs three
  bounded reads internally (tier 3) — so this endpoint now issues up to four bounded,
  indexed reads per render instead of one. All four are windowed by the same caller-supplied
  `[start,end]` (≤90 days, `historyRangeMaxDays`), consistent with the read-heavy
  Performance-Profile's tolerance for "denormalize, index aggressively, precompute" — no read
  becomes unbounded.
- **`GET /ui/dashboard/history`'s existing default/cap boundary** shifts by one calendar day
  (D11) — no change to query shape or index usage, only to which dates are requested by
  default.
- No existing read path (`telemetry.Reader.SnapshotsByVehicleBetween`,
  `account.Service.RegisteredVehicles`, etc.) changes in shape, query plan, or cost.

## Capabilities

### Modified Capabilities

- **`gateway`** — extends the "Dashboard History Charts" requirement with a third chart
  panel and the D11 default/cap change. See `specs/gateway/spec.md`.

### Out of scope (explicitly deferred / already shipped elsewhere)

- **The derivation itself** (D13's formula, gap detection D5/D5a, charge matching D12,
  span handling D8, the `[start-1,end]` lookback) — all `internal/battery`, tier 3, already
  archived. This tier calls the port; it does not recompute anything the port already
  computed.
- **The `charge_gaps` table, its write port, and `cmd/poller`'s nightly reconciliation** —
  `internal/telemetry`/`cmd/poller`, tier 1 + tier 3's wiring section, already
  shipped/specified. This tier never writes `charge_gaps` and never calls
  `telemetry.GapWriter` — the gateway is read-only at request time
  (`internal/gateway/AGENTS.md` §"Read-only at request time").
- **Reviving Supercharger battery-% estimation** (D14) — out of scope; a `SUPERCHARGER`-typed
  flagged day renders exactly like a `MANUAL`-typed one (D10), distinguished only by its
  tooltip's charge-type label.

## Resolved decisions

D1–D20 were settled with the owner via grill-me before tier 1's proposal was written
(2026-08-15), plus D17/D18/D18a/D19/D20 added by direct owner ruling on 2026-08-16 (recorded
verbatim in `openspec/roadmaps/RM28-battery-consumed-graph.md`). This tier does not
re-litigate any of them and does not re-run grill-me — the roadmap's own "Binding decisions
— do not renegotiate" header governs. This tier specifically implements:

- **D10** — flagged-day rendering (zero-height bar, distinct warning marker, bilingual label).
- **D11** — the default/cap move to yesterday.
- **D18/D18a** — the gateway keys the new chart on `DayConsumption.Date` directly, never
  re-bucketing it through `effectiveDayUTC`; the resulting one-day label mismatch against the
  UTC-bucketed odometer/battery charts is known and accepted, not "fixed" here.
- **D19** — the consumed chart's relative-to-window-max scale (mirroring `buildOdometerChart`,
  not `buildBatteryChart`'s absolute 0–100), with flagged-zero bars excluded from setting the
  max.
- **D20** — a multi-day span plots its real `ConsumedPct` with its own distinct marker, never
  a zero-height bar, visually separate from D10's warning marker.
- **D21** — a day that is BOTH flagged (D10) and a multi-day span (D20) carries BOTH markers,
  with both facts in the tooltip; markers are a set, not a mutually-exclusive choice.
- **D8** — the span bar sits on the row's own `Date` (already the span's last calendar day,
  per D17/tier-3's D-B3) — no date arithmetic performed in the gateway.
- **D2** — nothing is cached; every render recomputes via the port.

**D21's history is worth recording here.** This tier's first artifact revision found a genuine
gap — the roadmap's D10 and D20 did not explicitly say what happens when a day is both flagged
and a multi-day span — and flagged it loudly rather than picking silently, per this dispatch's
own instructions. The owner ruled directly (roadmap D21, 2026-08-16): both markers, both facts.
The leader's review of that same revision also caught that the "which marker wins the height"
framing the interpretation was built around was moot to begin with — tier 3's D5 makes
`Flagged` imply `ConsumedPct <= 0` unconditionally, so the height was never actually in dispute,
only which marker(s) show and what the tooltip says. design.md's D-G1, D-G4, D-G5, and D-G7
were all revised accordingly; none of the roadmap-decision implementations above changed as a
result — only this tier's own design decisions did.
