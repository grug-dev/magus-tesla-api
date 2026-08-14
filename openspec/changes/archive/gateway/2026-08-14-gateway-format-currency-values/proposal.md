Source: MAG-9 — https://linear.app/magus-monitor/issue/MAG-9/currency-format

## Why

Every currency value the gateway renders should read `1,234.56 CUR` — comma thousands
separator, period decimal separator, two decimals, currency code suffix. That formatter
**already exists**: `formatMoney(amount float64, currency string) string`
(`internal/gateway/handlers/supercharger.go:294`) produces exactly this shape —
`formatMoney(58000, "COP") -> "58,000.00 COP"`, two decimals, comma-grouped, negative-safe
(`math.Round(amount*100)`), currency code as a space-separated suffix — and is already
covered by a passing test (`supercharger_test.go:272`, `"58,000.00 COP"`).

The defect is **reach, not format**: `formatMoney` is called at only 1 of the 4 places the
gateway renders a monetary value. The other 3 build their own ad hoc `fmt.Sprintf("%.2f %s",
...)` label, which never comma-groups and diverges from `formatMoney`'s output the moment the
amount crosses 1,000 (e.g. a `12500.00 COP` price renders as `"12500.00 COP"` instead of
`"12,500.00 COP"`).

| Site | File:line | Current code | State |
|---|---|---|---|
| Supercharger Stats — `CostLines` tiles | `handlers/supercharger.go:189` | `formatMoney(costByCurrency[cur], cur)` | already correct |
| Supercharger Stats — session `CostLabel` | `handlers/supercharger.go:274` | `fmt.Sprintf("%.2f %s", *s.TotalCost, *s.Currency)` | **broken** |
| Charge log — `PriceLabel` | `handlers/charges.go:540` | `fmt.Sprintf("%.2f %s", e.Price, e.Currency)` | **broken** |
| Charge log — `CostPerKWhLabel` | `handlers/charges.go:480` | `fmt.Sprintf("%.2f %s/kWh", *v, e.Currency)` | **broken** |

**Verified non-breaking.** No existing test asserts the broken (unformatted) output —
`charges_test.go:868` only checks `CostPerKWhLabel != ""`. `supercharger_test.go:272` already
asserts the correct `"58,000.00 COP"` shape for the one already-correct site. Routing the 3
broken sites through `formatMoney` breaks no existing assertion.

## What Changes

Primary module: **`internal/gateway/`** only. No other `internal/` module is touched.

- **Create `internal/gateway/handlers/format.go`** and MOVE the existing formatting helpers
  into it **unchanged** (same signatures, same package, same behavior): `commaGroup`
  (`handlers.go:470`), `formatMoney` (`supercharger.go:294`), `formatKm` (`handlers.go:444`),
  `formatKmRaw` (`history.go:424`). All four stay in package `handlers`, so every existing
  call site compiles untouched. This is a pure move, no rewrite — the bug happened because the
  money formatter was buried in a page-specific feature file (`supercharger.go`) where an
  agent working on `charges.go` would not think to look for it; consolidating the formatting
  vocabulary into one obviously-named file fixes the discoverability root cause, not just the
  3 call sites.
- **Fix the 3 broken call sites** to call `formatMoney(amount, currency)` instead of their own
  `fmt.Sprintf("%.2f %s", ...)`. `CostPerKWhLabel` keeps its `/kWh` suffix appended after the
  `formatMoney` result (e.g. `"1,200.00 COP/kWh"`).
- **Add a `money-guard` Makefile target**, wired into `check`, that fails the build when a
  handler builds a monetary label with a `%.Nf` verb directly adjacent to a currency
  placeholder instead of calling `formatMoney` — the mechanical guard against this class of
  bug recurring, mirroring the existing `ui-guard`/`i18n-guard` shape and escape-hatch
  convention (`// money:allow: <reason>`).

**Explicitly NOT touched:** the two `RawEnergyKWh` / `RawPrice` fields
(`internal/gateway/handlers/charges.go:550-551`), built with
`strconv.FormatFloat(..., 'f', 2, 64)`. These populate the inline edit form's HTML input
`value` attributes and **must stay machine-parseable** (no comma grouping) or the edit form
breaks on submit (a browser number input cannot parse `"1,200.50"`). This change adds an
explicit negative requirement and scenario for this so no future implementer "fixes" it.

**Explicitly out of scope:** kWh, km, °C, and % formatting are untouched. kWh values are
currently **not** comma-grouped (`%.1f kWh` / `%.2f kWh`) while km values **are**
(`formatKm`/`formatKmRaw`) — that inconsistency is real but is not what MAG-9 asked for.
Recorded as a future-work item in `openspec/roadmaps/backlog.md` (item 9).

## Capabilities

### Modified Capabilities

- **`gateway`** — adds a "Monetary Value Display Formatting" requirement: every rendered
  monetary label (Supercharger session cost, charge-log price, charge-log cost-per-kWh) uses
  the shared `formatMoney` helper's `1,234.56 CUR` format; the two machine-parseable raw form
  values are explicitly exempted.

### Consumed Capabilities (no change to their specs)

None — this change reads no module port and adds no new read path. It is a pure
presentation-layer fix inside `internal/gateway/handlers`.

## Impact

- **Module:** `internal/gateway` only.
- **Files:** new `internal/gateway/handlers/format.go` (moved helpers); edits to
  `internal/gateway/handlers/supercharger.go` (remove the moved helper, fix `CostLabel`),
  `internal/gateway/handlers/charges.go` (fix `PriceLabel`, `CostPerKWhLabel`),
  `internal/gateway/handlers/handlers.go` (remove moved helpers), `internal/gateway/handlers/
  history.go` (remove moved helper); new/updated unit tests in `handlers/format_test.go` and
  targeted assertions in `charges_test.go` / `supercharger_test.go`; `Makefile` (new
  `money-guard` target, wired into `check`); `internal/gateway/AGENTS.md` if the "How to add
  or modify a page" recipe or a doc reference needs a pointer to `format.go` (see design.md).
- **APIs:** no route, parameter, or response-shape change. Purely presentation-string output
  changes for 3 of the existing HTML labels.
- **Read paths:** none added or changed. No `telemetry.Reader`, `manualcharge.Reader`, or
  `telemetry.SuperchargerReader` call is added, removed, or reshaped.
- **Database:** **none.** No table, column, index, view, or migration. The `database` design
  gate (`openspec/config.yaml`) does not trigger — stated explicitly per that gate's
  requirement, not omitted.
- **Breaking:** No. Every affected label is a display string inside an HTML fragment; no
  machine-consumed field changes shape (the two `Raw*` fields are explicitly preserved
  unformatted — see "Explicitly NOT touched" above).
- **Dependencies:** none added.

## Grill-me outcomes (binding — see design.md for the full decision record)

This proposal was produced from a leader-run interview against the actual code (per
`openspec/config.yaml`'s "Must use grill-me skill" rule), not from the raw ticket text alone.
Five decisions were settled and are recorded as D1–D5 in `design.md`: file location/shape
(D1), locale — the format is fixed `1,234.56` for **both** ES and EN, `formatMoney` stays a
pure function with no i18n dependency (D2), scope — money only, kWh/km/°C/% untouched (D3),
enforcement — the `money-guard` Makefile target (D4), and unit tests — included (D5).
