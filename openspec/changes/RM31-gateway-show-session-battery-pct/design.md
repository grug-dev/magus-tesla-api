# Design — RM31-gateway-show-session-battery-pct

## Context

The Supercharger Stats slice already performs one bounded, account- and selected-vehicle-scoped
read through `charging.SessionReader.ListSessionsByVehicleBetween`. `buildSuperchargerChart`
already returns the shared `fragments.HistoryChart`, whose bars support `Label` and whose chart
supports `LabelVertical` and `YAxisTicks`; the shared `historyBarChart` template already renders
all three without template arithmetic. The builder currently leaves those fields empty.

Likewise, `charging.Session` already exposes `StartBatteryPct`, `EndBatteryPct`,
`StartBatteryPctEst`, and `EndBatteryPctEst`, while `SuperchargerRowVM` and the table currently
show only date, site, energy, and cost. This is a gateway presentation gap, not a charging
port or database gap.

## Goals / Non-Goals

**Goals**

- Give every monthly Supercharger bar a stable `YYYY-MM` axis label, a kWh y-axis, and a
  legible vertical-label layout using the established history-chart contract.
- Display all four session battery percentage fields in the existing table using preformatted
  view-model strings, preserving `"—"` for missing values.
- Add complete ES/EN catalogue coverage for all new user-facing headers.

**Non-Goals**

- No edit flow, new route, write, database object, chart library, new chart renderer, estimator,
  Country restoration, or new cross-module dependency.

## Decisions

### D1 — Reuse `HistoryBar.Label` and `HistoryChart.YAxisTicks` (binding — tier scope)

`buildSuperchargerChart` SHALL set each `HistoryBar.Label` from its bucket month using
`Format("2006-01")`, while retaining the existing human-readable tooltip month text. It SHALL
set `YAxisTicks` with `buildYAxisTicks(maxKWh, func(v float64) string { return fmt.Sprintf("%.1f kWh", v) })`
or the existing local kWh-equivalent formatter if one is already present. The pre-existing
`historyBarChart` renderer consumes these values; no new tick algorithm, renderer, chart library,
or template math is added.

This reuses a typed, tested closed vocabulary. Five ticks stay proportional to the tallest
monthly bucket and are nil for a zero maximum exactly as `buildYAxisTicks` already specifies.

### D2 — `YYYY-MM` labels render vertically for every Supercharger month chart (judgment call)

`buildSuperchargerChart` SHALL set `LabelVertical: true` for both non-empty and empty chart
results. Unlike the dashboard's `MM-DD` labels, `YYYY-MM` is wider, and the page supports 3/6/12
month windows; always vertical makes label layout deterministic across presets and avoids an
additional threshold heuristic. The shared renderer already has this mode, so this is a one-field
selection rather than new rendering behavior.

### D3 — Four battery values stay presentation strings on `SuperchargerRowVM` (binding — tier scope / roadmap Decision 3)

Add `StartBatteryPctLabel`, `EndBatteryPctLabel`, `StartBatteryPctEstLabel`, and
`EndBatteryPctEstLabel` to `SuperchargerRowVM`. `buildSuperchargerRows` SHALL map a non-nil
integer percentage to `"<n>%"` and any nil pointer to exactly `"—"`. The template receives only
these ready-to-render strings; it performs neither pointer handling nor number formatting.

The two estimate labels intentionally render `"—"` today. Roadmap Decision 3 says the frozen
estimate pair remains NULL until an estimator exists; fabricating, calculating, or persisting an
estimate here would contradict that decision.

### D4 — Add four headers to the one bilingual catalogue; Country remains absent (binding — tier scope / roadmap Decision 5)

Add one semantic `KeySupercharger...` constant and one same-line `{ES: ..., EN: ...}` catalogue
entry for each header: verified start battery %, verified end battery %, start battery % estimate,
and end battery % estimate. The table calls `i18n.T(ctx, key)` for every header. It SHALL not
restore Country, its removed key, or a billing-type column.

Suggested catalogue copy is deliberately concise and unambiguous: ES `Batería inicial`,
`Batería final`, `Estimación inicial`, `Estimación final`; EN `Start battery`, `End battery`,
`Start estimate`, `End estimate`. The percent unit is carried by the cell value, preventing
repeated noisy header text while retaining a clear type label.

### D5 — Database design gate does not apply (binding — dispatch)

No database object changes. The existing `charging.Session` fields arrive through the existing
public reader port and the gateway continues to make its single bounded read. Therefore there is
no schema, migration, SQL query, index plan, or database confirmation to present; the database
design gate is not triggered.

### D6 — Tests are pure/offline and template codegen is explicit (binding — project test policy)

Tests belong in the existing `supercharger_test.go` pattern: direct `charging.Session` fixtures,
pure chart/row-builder assertions, and existing `httptest` page/fragment rendering fakes. The
test contract asserts output values before implementation. After the `.templ` edit, the
implementation worker runs `make templ` to regenerate the committed
`supercharger_stats_templ.go`; `make css` is unnecessary unless a new Tailwind/DaisyUI class is
introduced (none is designed here). Per the project policy, the worker does not run build or test
commands; the owner later runs `go test ./internal/gateway/...`.

## Database Changes

**None.** No migration, schema, column, index, constraint, view, SQL query, or db-package file
is added or changed. The display reads values already supplied by `charging.SessionReader`, so
the database design gate does not trip and no index plan is applicable.

## Test Contract

**T1 — chart labels and ticks.** Given sessions in a March–May 2026 window with 10 kWh in March
and 30 kWh in May, when `buildSuperchargerChart` runs, then its three labels are exactly
`2026-03`, `2026-04`, `2026-05`; May is 100%; and its y-axis has five ticks labeled with kWh
values from 30.0 kWh down to 0.0 kWh.

**T2 — zero energy has no y-axis ticks.** Given non-empty sessions whose monthly energy totals
are zero or nil, when the chart is built, then bars remain present with zero heights and
`YAxisTicks` is nil/empty, preserving the shared renderer's no-gutter behavior.

**T3 — label orientation.** Given either an empty chart or a chart for any supported 3/6/12
month selector window, when it is built, then `LabelVertical` is true.

**T4 — populated battery fields.** Given a `charging.Session` with all four battery pointers
set to 40, 80, 42, and 78, when rows are built, then the corresponding VM labels are exactly
`40%`, `80%`, `42%`, and `78%`.

**T5 — nil mapping.** Given a session with all four battery pointers nil (the normal estimate
state), when rows are built, then every corresponding VM label is exactly `"—"`; no empty
string, zero percentage, or invented estimate is emitted.

**T6 — rendered bilingual headers and cells.** Given a rendered Supercharger Stats fragment in
each existing language context, when it has a populated battery-row VM, then the four translated
headers and all four formatted cell values appear. A nil fixture renders four em dashes. This
also protects the table's header/cell count alignment.

**T7 — regression exclusion.** The test/code review sweep confirms no Country header/key/cell is
added back, and existing date/site/energy/cost output remains unchanged.

## Risks / Trade-offs

- The four new table columns make the desktop table wider. This is intentional ticket scope;
  vertical chart labels recover chart space independently. A responsive table redesign is a
  separate UX change, not hidden inside this display-only tier.
- Estimate columns mostly show `"—"` until a future estimator exists. Showing the true missing
  state is preferable to presenting a fabricated value that looks authoritative.
- Reusing five relative y-axis ticks means the labels are tied to the selected window's maximum,
  matching every other relative-scale history chart. A fixed global kWh scale would make cross-
  window comparison easier but would be a different chart behavior and is out of scope.

## Verification signals

- Implementation: run `make templ` after changing `supercharger_stats.templ`; include the
  generated `supercharger_stats_templ.go` artifact. Do not hand-edit generated files.
- Owner verification (not the worker): `go test ./internal/gateway/...`.
- Artifact validation for the leader: `openspec validate RM31-gateway-show-session-battery-pct --strict`.
