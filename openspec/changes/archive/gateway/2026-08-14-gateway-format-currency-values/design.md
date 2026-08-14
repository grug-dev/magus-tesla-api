# Design: gateway-format-currency-values

## Context

MAG-9 asks for a centralized, reusable currency formatter used everywhere a currency value is
shown. The leader surveyed the code before this design was written (per this project's
"verify against the code, don't assume" convention) and found the formatter already exists:
`formatMoney(amount float64, currency string) string`
(`internal/gateway/handlers/supercharger.go:294`) already produces exactly the requested
`1,234.56 CUR` shape — two decimals, comma-grouped thousands, period decimal, negative-safe
(`math.Round(amount*100)`), currency code as a space-separated suffix — and is proven correct
by an existing passing test (`supercharger_test.go:272`, `formatMoney(58000,"COP") ->
"58,000.00 COP"`). It delegates thousands-grouping to `commaGroup`
(`internal/gateway/handlers/handlers.go:470`).

So this design is not "build a formatter" — it is "make the existing formatter the one and
only path to a monetary label, and stop the pattern from silently regressing again." Four
render sites build a monetary label in this module; only 1 already calls `formatMoney`.

## Goals / Non-Goals

**Goals**
- Every rendered monetary label (`CostLabel`, `PriceLabel`, `CostPerKWhLabel`, `CostLines`)
  goes through the same `formatMoney` function — one code path, testable once.
- Consolidate the module's small formatting vocabulary (`commaGroup`, `formatMoney`,
  `formatKm`, `formatKmRaw`) into one discoverable file, so the next agent adding a monetary
  or unit-bearing label finds the existing helper before reinventing it — this is the
  AI-efficiency root-cause fix (`CLAUDE.md` "AI efficiency" — discoverability in the docs/
  files an agent already looks at, not re-derived by grepping).
- Add a mechanical guard (`money-guard`) so a future handler that hand-rolls
  `fmt.Sprintf("%.2f %s", ...)` for a currency value fails `make check` instead of shipping
  silently, mirroring how `ui-guard`/`i18n-guard` already close this class of gap for their
  own vocabularies.

**Non-Goals**
- Changing `formatMoney`'s output format, rounding, or negative-number handling — it is
  already correct and already tested.
- Locale-varying the format (es-CO convention is `58.000,00`, the inverse of what this change
  ships) — explicitly rejected, see D2.
- kWh, km, °C, or % formatting — explicitly out of scope, see D3. Recorded as a backlog item.
- Any database, module-interface, or read-path change — this change touches presentation
  code only.

## Decisions

### D1 — New `internal/gateway/handlers/format.go`; move the four existing helpers unchanged

Create `internal/gateway/handlers/format.go` and **move** (not rewrite) `commaGroup`,
`formatMoney`, `formatKm`, `formatKmRaw` into it, verbatim — same signatures, same package
(`handlers`), same behavior. Every existing call site (`supercharger.go:189`, `handlers.go`,
`history.go`) keeps compiling with no changes beyond the removed function bodies, because
Go resolves unqualified calls within a package regardless of which file declares the function.

**Rejected alternative: a new exported `internal/gateway/format` package.** Rejected because
(a) the project's own `ai/go-conventions.md`/`internal/gateway/AGENTS.md` boundary conventions
draw the module line at `internal/<domain>`, not sub-packages inside the gateway — creating one
here would be an unjustified new package for four small, gateway-internal helper functions with
no external consumer; (b) the `ui/` kit and view models are documented to never format — "Pass
VM-ready strings into components (`ui/` components hold no domain imports and no business
logic)" (`ai/htmx-conventions.md`) — so an exported formatting package would invite a future
agent to call it from `templates/ui/` or a `.templ` file, which this project's "no business
logic in templates" rule (`ai/htmx-conventions.md` "Component & fragment rules") forbids
outright; (c) four functions and ~40 lines does not meet this project's own bar for adding an
indirection layer ("add a wrapper/layer only where it buys change-locality on a *volatile or
repeated* surface" — `CLAUDE.md` "AI efficiency"). A same-package file the handlers already
import nothing extra to reach is the smaller, more discoverable move.

**Why this actually fixes the root cause, not just the 3 sites.** The proposal's own root-cause
finding is that `formatMoney` was buried inside `supercharger.go` — a page-specific feature
file — where an agent implementing `charges.go`'s `PriceLabel` had no reason to look. Moving it
(and its sibling formatting helpers) into a file literally named `format.go` is the
discoverability fix: the next agent adding a currency or km label greps `format.go` first
because the name says what it holds, rather than rediscovering the pattern by re-reading
`supercharger.go` or reinventing `fmt.Sprintf("%.2f %s", ...)` from scratch.

### D2 — Format is locale-invariant: `1,234.56` for both ES and EN; `formatMoney` takes no context

The ticket explicitly asks for comma-thousands + period-decimal for **all** currency values.
This is the **opposite** of Spanish/Colombian locale convention (es-CO renders `58000.00 COP`
as `58.000,00`, period-thousands + comma-decimal). This change ships the ticket's literal
request as a **locale-invariant** format — it does **not** vary by the page's active language
(`i18n` ES/EN toggle).

Consequently `formatMoney(amount float64, currency string) string` stays a **pure function**:
it takes no `context.Context` and calls no `i18n.T`. This is a **deliberate deviation from
Spanish locale convention**, recorded here explicitly so a future agent reading this code does
not "fix" it into an es-CO-aware formatter without first re-confirming the request with the
user.

**Why:** (a) the ticket says so, explicitly, for "ALL currency values" with no per-language
carve-out; (b) it keeps money mutually consistent with `formatKm`/`formatKmRaw`, which are
already locale-invariant in exactly the same way (`commaGroup` is shared by both); (c) it keeps
one single testable code path — a locale-aware formatter would need two format tables and a
context-threading change to every call site, none of which the ticket asked for.

**Rejected alternative:** format money per active language (es-CO `58.000,00` in Spanish,
en-US `58,000.00` in English). Rejected because it directly contradicts the ticket's literal
ask ("comma as thousand separator for ALL currency values ... period decimal separator"),
would break the shared `commaGroup` helper's contract with `formatKm` (which would then be the
only remaining locale-invariant numeric format on the page, an inconsistency of its own), and
was not requested — reopening it is future work if the user asks, not a default.

### D3 — Scope: monetary values only; kWh/km/°C/% untouched

Only the 3 broken call sites are fixed: `CostLabel` (`supercharger.go`), `PriceLabel` and
`CostPerKWhLabel` (`charges.go`). No kWh, km, °C, or percent value's formatting changes.

**A real, adjacent inconsistency exists and is deliberately NOT fixed here:** kWh values are
rendered with `fmt.Sprintf("%.1f kWh", ...)` / `fmt.Sprintf("%.2f kWh", ...)` — **not**
comma-grouped — while km values already go through `formatKm`/`formatKmRaw`, which **are**
comma-grouped. A large kWh figure (e.g. a yearly Supercharger total) would render `"12500.4
kWh"` with no thousands separator today. This is out of MAG-9's scope (the ticket is about
currency only) and is recorded as backlog item 9 (`openspec/roadmaps/backlog.md`) rather than
silently bundled into this change or silently dropped.

**Rejected alternative:** fix kWh grouping in the same change, since `commaGroup` is right
there and the fix is small. Rejected because it broadens this change's blast radius beyond
what MAG-9 asked for and beyond what was reviewed with the user — scope creep on a
ticket-sourced change is exactly the kind of undocumented expansion this project's process
disallows; a one-line backlog entry costs less than a silent scope change and keeps the
decision visible for whoever picks it up.

### D4 — `money-guard` Makefile target, wired into `check`

Add a `money-guard` target mirroring `ui-guard`/`i18n-guard`'s existing grep-based shape and
escape-hatch convention (Makefile, `ui-guard` at line ~295, `i18n-guard` at line ~322):

- **Pattern:** flag any `fmt.Sprintf("%.[0-9]+f %s...` occurrence in `internal/gateway/
  handlers/*.go` (excluding `_test.go` and excluding `format.go` itself, since `formatMoney`'s
  own internals legitimately build the string a different way via `commaGroup` +
  `strconv.FormatInt`, never via `fmt.Sprintf("%.Nf %s"`). This targets the exact bug shape:
  a `%.Nf` decimal verb immediately followed by a `%s` placeholder for a currency code — the
  signature of "I hand-rolled a money string instead of calling `formatMoney`." It does not
  flag `fmt.Sprintf("%.1f kWh", ...)` (a literal unit suffix, not a `%s` placeholder) or
  `fmt.Sprintf("%.0f km", ...)`, so it stays scoped to currency, consistent with D3.
- **Escape hatch:** a trailing `// money:allow: <reason>` comment on the same line — same
  placement rule as `i18n-guard`'s handler pass (`.go` files have real comment syntax, so the
  same-line trailing comment is sufficient; there is no `.templ` pass for this guard since
  money labels are built exclusively in `handlers/*.go`, never in markup, per "no business
  logic in templates").
- **Wiring:** add `money-guard` to the `.PHONY` list (Makefile line ~62, alongside
  `ui-guard i18n-guard`) and to `check`'s prerequisite list, changing `check: build vet
  ui-guard i18n-guard test` to `check: build vet ui-guard i18n-guard money-guard test`, plus
  updating `check`'s `##` help text to mention it.

**Rejected alternative:** a Go `go vet`-style custom analyzer. Rejected for the same reason
`ui-guard`/`i18n-guard` are grep-based, not AST-based: this project's own convention explicitly
accepts a grep heuristic at "the same rigor bar as `ui-guard`, not a parser"
(`internal/gateway/AGENTS.md` §i18n) — consistency with the two existing guards' mechanism
outweighs a marginally more precise but heavier custom analyzer for a single narrow pattern.

### D5 — Unit tests: INCLUDED

Per this project's binding rule ("ask every time, before writing the plan... default NO, but
still ask"), unit tests were explicitly scoped **in** for this change (see tasks.md). Required
coverage: a table-driven `TestFormatMoney` (0, negative, boundary-at-1000, and the ticket's own
worked `58000` example) plus one assertion per fixed call site (`PriceLabel`,
`CostPerKWhLabel` — including its `/kWh` suffix — and the Supercharger session `CostLabel`).

## Critical constraint: `RawEnergyKWh` / `RawPrice` MUST stay unformatted

`internal/gateway/handlers/charges.go:550-551` builds:

```go
RawEnergyKWh: strconv.FormatFloat(e.EnergyAddedKWh, 'f', 2, 64),
RawPrice:     strconv.FormatFloat(e.Price, 'f', 2, 64),
```

These populate the inline edit form's HTML `<input value="...">` attributes
(`fragments.ChargeRowEdit`, the gold-standard inline edit form per
`ai/htmx-conventions.md`). A `number` input's `value` must be a plain machine-parseable decimal
string with **no thousands separator** — `"1,200.50"` is not a valid `<input type="number">`
value and the browser either rejects it or silently clears the field, breaking the edit form's
round-trip on submit. **This change MUST NOT route `RawEnergyKWh` or `RawPrice` through
`formatMoney` or `commaGroup`.** This is captured as an explicit negative requirement + scenario
in `specs/gateway/spec.md` and called out in `tasks.md` so no implementer "helpfully"
comma-groups them.

## Data flow (presentation-only, no port change)

```
handler builds a ChargeEntryVM / SuperchargerRowVM / SuperchargerTiles
  amount, currency  ->  formatMoney(amount, currency)  ->  "1,234.56 CUR" string   [CHANGED for 3 sites]
  RawEnergyKWh, RawPrice  ->  strconv.FormatFloat(v, 'f', 2, 64)  ->  "1234.56"    [UNCHANGED — machine-parseable]
  .templ renders the pre-computed string verbatim — no arithmetic, no formatting in markup [UNCHANGED]
```

No `telemetry.Reader`, `manualcharge.Reader`/`Writer`, or `telemetry.SuperchargerReader` call
is added, removed, or reshaped. This is a pure string-building change inside
`internal/gateway/handlers`.

## Database Changes

**None.** This change introduces no table, column, index, constraint, view, or migration. The
`database` design gate (`openspec/config.yaml`) does not trigger — stated explicitly per the
gate's requirement that a design.md exist and say so, not because a DB-touching design was
omitted.

## Risks / Trade-offs

- **[`money-guard`'s grep pattern is a heuristic, not a parser]** — accepted, consistent with
  `ui-guard`/`i18n-guard`'s own documented rigor bar; a determined author can still write
  around it (e.g. building the string via `+` concatenation instead of `Sprintf`), same
  residual risk the two existing guards already accept.
- **[Locale-invariant money format diverges from es-CO convention]** — accepted per D2, on
  explicit ticket instruction; flagged here so a future agent does not "fix" it unprompted.
- **[kWh formatting stays inconsistent with km formatting]** — accepted per D3, recorded as
  backlog item 9, not silently dropped.

## Migration Plan

None. No DB, no data, no deployment step. Rollback = revert the gateway commit; the 3 sites
return to their unformatted `fmt.Sprintf` output.
