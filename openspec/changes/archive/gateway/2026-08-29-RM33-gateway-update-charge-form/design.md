# Design — RM33-gateway-update-charge-form

Source ticket: MAG-18 · Roadmap: `openspec/roadmaps/RM33-manual-record-status.md`, tier 2 of 3.
Roadmap decisions **D7, D9, D12, D15, D16** are binding, plus the 2026-08-29 tier-2 interview
decisions **D-RM33-5..8**. This document does not re-open any of them; where it fills a gap none
of them covers, it says so explicitly (**D-Fields**, **D-Suffix**, **D-Colspan**).

No database object is added, changed, or removed by this tier — the `database` design gate
(`CLAUDE.md` §Pipeline config) does not apply. This design.md exists to fix the Test Contract and
the client-side-JS rationale (mandatory per `internal/gateway/AGENTS.md` §"Standing convention
(RD8)") before implementation, not because a schema changed.

---

## Context

Facts read directly out of the repository, not recalled.

1. **`internal/charging` already exports everything this tier needs.** `Status`
   (`StatusInProgress = "IN_PROGRESS"`, `StatusDone = "DONE"`), `Field` (`FieldChargedOn`,
   `FieldLocationKind`, `FieldEndedAt`, `FieldEndBatteryPct` — each `Field`'s string value **is**
   the form input name), and `RequiredFieldsFor(s Status) []Field` (`internal/charging/validation.go:24`):
   `IN_PROGRESS` → `[ChargedOn, LocationKind]`, `DONE` → `[ChargedOn, LocationKind, EndedAt, EndBatteryPct]`.
   `Entry.EnergyAddedKWh` is `*float64`, `Entry.OdometerKm` is `*int`, `Entry.EnergySource` is
   always module-computed (the gateway must never set it — already true today, since the gateway
   never touched that field).
2. **The two forms diverge only in a few places today**: `charge_create_form.templ` has no
   `vehicle`/hidden id and posts to `/ui/charges/create`; `charge_row_edit.templ` has a hidden
   `id`, a read-only `VehicleLabel` span (no Vehicle field to submit), and posts to
   `/ui/charges/row/{id}`. Every other field (`charged_on`, `energy_added_kwh`, `price`,
   `currency`, `location_kind`, battery percentages, the "More details" block) is structurally
   identical between the two, which is why this tier changes both files in lockstep.
3. **`ui.InputProps` has no suffix/addon capability today** (`internal/gateway/templates/ui/input.templ`).
   Every other `ui.*` form wrapper (`Field`, `Select`, `Textarea`) is similarly a plain wrap with
   no compound-content support.
4. **`parseChargeForm` returns a zero-value `charging.Entry{}` on any validation failure**
   (`internal/gateway/handlers/charges.go:807`, `return charging.Entry{}, errs, false`) — not a
   partially-populated one. This is the actual root cause of D15: `chargeEntryVMFromEntry` (used
   by the edit-row error path) and `buildChargesPage` (used by the create-form error path) both
   receive either a wholly-empty `Entry` or a freshly-queried one, so no submitted value survives
   a validation failure, regardless of which single field was invalid.
5. **`static/app.js` already has two sanctioned zero-JS exceptions** — RD9 (`browser_tz` cookie
   script) and RD10 (the `htmx:confirm` interception) — both are `document.body`-scoped listeners
   using event delegation, not per-element `addEventListener` calls at render time. That pattern
   is exactly what a form that can appear multiple times on one page (several open inline edit
   rows) and can be swapped in dynamically by htmx needs: delegation requires no re-binding on
   swap.
6. **DaisyUI v5's suffix idiom, verified via Context7 (`/saadeghi/daisyui`, 2026-08-29 query
   against v5.0.46 docs)**: `<label class="input"><input class="grow" .../><span class="label">SUFFIX</span></label>`.
   The `label` element (not a wrapping `div`) is the DaisyUI v5 `input` component's own root when
   it needs compound content — the plain `<input class="input">` form current `ui.Input` renders
   is the single-element special case of the same component.
7. **`charge_row_edit.templ`'s `<tr><td colspan="8">` spans the entries table's current column
   count.** Tier 3 changes that column count (adds a Status column, adds a battery-range column,
   removes the Vehicle column) — this tier does not touch the entries table at all, so the
   colspan is untouched here and is deliberately tier 3's problem (**D-Colspan** below).

---

## Goals / Non-Goals

**Goals:**
- Render a `status` control on both forms that defaults to `IN_PROGRESS` (create) or the
  persisted status (edit), and drives which fields the form marks required — both at initial
  render (server-side, from `RequiredFieldsFor`) and live, on change (client-side JS, no
  round-trip).
- Make `energy_added_kwh` and `price` genuinely optional at the gateway layer, with `price`
  defaulting to `0` and rendering a `COP` suffix instead of a disabled Currency field.
- Preserve every submitted value — valid or not — across a 4xx/5xx re-render of either form.
- Add the odometer input and the descriptive AC/DC option text.
- Record the two new client-side-JS exceptions this tier introduces, per RD8.

**Non-goals:**
- Any change to `internal/charging`'s schema, domain rules, or ports (tier 1's territory,
  already archived).
- The entries table, the date filter, or the aggregation tiles (tier 3's territory).
- A general-purpose "restore form state" mechanism beyond the one 4xx/5xx re-render path this
  tier's two handlers already own.
- A `ui.InputProps.Prefix` or any other DaisyUI `label`-compound-content variant beyond the one
  `Suffix` this tier needs — added narrowly, not as a speculative general facility.

---

## Decisions

### D-Fields — the gateway imports `RequiredFieldsFor`, it does not re-derive the rule

`parseChargeForm` resolves the submitted `status` form value to a `charging.Status` **first**
(validating it is one of `IN_PROGRESS`/`DONE` — see the validation table below), then calls
`charging.RequiredFieldsFor(status)` and builds a `map[charging.Field]bool` from the result. Every
field-presence check that maps to a `charging.Field` (`charged_on`→`FieldChargedOn`,
`location_kind`→`FieldLocationKind`, `ended_at`→`FieldEndedAt`, `end_battery_pct`→`FieldEndBatteryPct`)
looks up that map instead of hardcoding "always required" or "always optional". This is the same
"single source of truth, gateway imports it" shape roadmap **D5** specified for tier 1, applied at
the call site tier 1 could not reach (a `charging`-scoped worker may not edit `internal/gateway`).

`start_battery_pct` has **no** `charging.Field` constant (it is not in `RequiredFieldsFor`'s
domain) — it stays unconditionally required in the gateway exactly as it is today, unchanged by
this tier. `energy_added_kwh` and `price` likewise have no `charging.Field` constant — they were
never part of the module's required-field rule even before this tier (the module never required
them; only the gateway's own, now-removed `Required: true` markup did), so making them optional at
the gateway is not a `RequiredFieldsFor` change at all, just deleting the gateway's own stricter
markup and validation branch.

**Server-rendered initial `required` state.** Both templates receive the same required-field
lookup the handler already computed for validation, so the initial HTML the browser sees already
has the correct `required` attribute on `ended_at`/`end_battery_pct` for the status being
rendered — no flash-of-wrong-state before JS runs. Concretely: `ChargesPageData` (create form) and
`ChargeEntryVM` (edit row) each carry a `RequiredEndedAt bool` / `RequiredEndBatteryPct bool` pair,
computed by the handler from `charging.RequiredFieldsFor` the same way `parseChargeForm` does — the
handler, not the template, calls `RequiredFieldsFor` (no business logic in templates,
`ai/htmx-conventions.md`).

### D-Suffix — `ui.InputProps.Suffix string`, DaisyUI v5's `label`-compound idiom

Add one new optional field to `InputProps`:

```go
type InputProps struct {
	Type     string
	Name     string
	Value    string
	Required bool
	Disabled bool
	Suffix   string // NEW: renders a DaisyUI v5 compound `label` wrapper with this trailing text
	Class    string
	Attrs    templ.Attributes
}
```

When `Suffix == ""` (every existing call site), `Input` renders **exactly** the markup it renders
today — zero footprint on the ~15 other `ui.Input` call sites across the app. When `Suffix != ""`,
`Input` renders:

```templ
if p.Suffix == "" {
	<input type={ inputType(p.Type) } name={ p.Name } value={ p.Value } class={ "input font-mono w-full", p.Class } required?={ p.Required } disabled?={ p.Disabled } { p.Attrs... }/>
} else {
	<label class={ "input w-full", p.Class }>
		<input type={ inputType(p.Type) } name={ p.Name } value={ p.Value } class="grow font-mono" required?={ p.Required } disabled?={ p.Disabled } { p.Attrs... }/>
		<span class="label">{ p.Suffix }</span>
	</label>
}
```

This keeps the DaisyUI `input`/`label` component classes owned inside `ui/` (the anti-corruption
adapter rule, `ai/htmx-conventions.md` §Styling) — the price field in both fragments becomes
`@ui.Input(ui.InputProps{Type: "number", Name: "price", Suffix: "COP", ...})` with **no** DaisyUI
class ever appearing in a fragment. "COP" is an ISO 4217 currency code, not translatable copy — it
carries an `i18n:allow: ISO currency code` marker per `internal/gateway/AGENTS.md` §i18n, same
treatment as a unit or brand noun.

**Rejected alternative:** a separate `ui.InputGroup`/`ui.CurrencyInput` wrapper component. Rejected
because the only structural difference DaisyUI v5 requires is the `label` root vs. a bare `input`
root — a single conditional inside the existing `Input` component is a smaller diff than a second
component that would duplicate every other `InputProps` field, and "COP" is the only suffix this
codebase needs today (no speculative generality, `CLAUDE.md` §AI-efficiency "do not over-abstract").

### D-Values — `fragments.ChargeFormValues`, the D15 fix

A new struct in `charges_vm.go`, one string field per form input that has no other default source:

```go
// ChargeFormValues carries the raw, unparsed POST values a user submitted, so a
// 4xx/5xx re-render can echo exactly what they typed — including a value that
// failed validation — rather than a blank or default field (roadmap D15).
// Every field is the literal c.PostForm(name) string; no parsing, no trimming
// beyond what parseChargeForm already applies for its own validation.
type ChargeFormValues struct {
	Status          string // "IN_PROGRESS" | "DONE" | "" (fresh page load)
	EnergyAddedKWh  string
	Price           string
	LocationKind    string
	StartBatteryPct string
	EndBatteryPct   string
	ChargingType    string
	LocationLabel   string
	Notes           string
	OdometerKm      string
}
```

`ChargesPageData` gains `FormValues ChargeFormValues`. `parseChargeForm`'s signature grows a return
value:

```go
func (h *Handler) parseChargeForm(c *gin.Context, uid uuid.UUID, vehicles []account.Vehicle) (
	entry charging.Entry, raw fragments.ChargeFormValues, validationErrors map[string]string, ok bool,
)
```

`raw` is built from `c.PostForm(...)` calls **at the top of the function**, before any parsing —
so it is populated identically on both the success and failure paths and a `go vet`-visible
signature change forces every call site to handle it (the deterministic-signal payoff of a
compile-time break over a silently-stale caller).

**Wiring into the two error paths** (`ChargeCreate`'s 422/500 branches, `ChargeRowUpdate`'s 422/500
branch):
- `d := h.buildChargesPage(...)` as today, then **overwrite** `d.DefaultChargedOn`,
  `d.DefaultStartedAt`, `d.DefaultEndedAt` with the submitted `charged_on`/`started_at`/`ended_at`
  raw strings (these three already have a Default* home on `ChargesPageData` for the fresh-load
  case; the error path is the one place that source switches from "today's date" to "what the user
  typed") and set `d.FormValues = raw`.
- `vm := chargeEntryVMFromEntry(entry, vehicles)` for the edit-row path is replaced, **on the error
  branch only**, by a small mapping from `raw` (+ the untouched `id`/`vehicle` context the handler
  already has) into a `ChargeEntryVM` whose `Raw*` fields come from `raw` instead of from `entry`.
  The success path's `vm := chargeEntryVMFromEntry(updated, vehicles)` is unchanged — `raw` is
  irrelevant once the write commits (the re-render then shows the durably stored values, which is
  correct, not a regression of this fix).

**Fresh-load fields keep rendering blank exactly as today.** On a fresh `GET /charges`,
`buildChargesPage` never touches `FormValues`, so it is the zero-value `ChargeFormValues{}` — every
field driven by it (`energy_added_kwh`, `price`, `location_kind`'s selection, both battery
percentages, charging type, location label, notes, odometer) renders identically to today's
behavior (blank input, no `<option selected>`). Only `Status` needs an explicit default: the
handler sets `d.FormValues.Status = string(charging.StatusInProgress)` when building the **fresh**
page (not the error re-render, where the submitted value already carries a real status), so the
create form's status `<select>` opens on IN PROGRESS without a special-cased template branch.

**Template wiring (both forms), each Value/selected attribute switches from a hardcoded default to
this struct:**

| Input | Today | After this tier |
|---|---|---|
| `energy_added_kwh` | no `Value` attr | `Value: d.FormValues.EnergyAddedKWh` / `vm.RawEnergyKWh` (edit row keeps its existing persisted-value path on the non-error render; only the error branch takes `raw`) |
| `price` | no `Value` attr | `Value: d.FormValues.Price` (create) |
| `location_kind` `<option>` | no `selected?` (create form bug fixed here too) | `selected?={ d.FormValues.LocationKind == "HOME" }` etc. |
| `start_battery_pct`/`end_battery_pct` | no `Value` attr (create) | `Value: d.FormValues.StartBatteryPct` / `EndBatteryPct` |
| `charging_type` `<option>` | no `selected?` (create form) | `selected?={ d.FormValues.ChargingType == "AC" }` etc. |
| `location_label` | no `Value` attr | `Value: d.FormValues.LocationLabel` |
| `notes` | no body text | `{ d.FormValues.Notes }` |
| `status` `<option>` | (new field) | `selected?={ d.FormValues.Status == "IN_PROGRESS" }` / `"DONE"` |
| `odometer_km` | (new field) | `Value: d.FormValues.OdometerKm` |

The edit row's already-working persisted-value path (`vm.RawChargedOn`, `vm.RawPrice`, etc., fed
from `chargeEntryVMFromEntry(entry, ...)` on a **non-error** render) is untouched — this table
describes the **error-path** re-render values only. `ChargeRowEdit`'s template signature is
unchanged (`vm ChargeEntryVM, csrfToken string, validationErrors map[string]string`); the handler
picks which `ChargeEntryVM` to build (from `raw` on error, from the stored `Entry` otherwise)
before calling the same template.

**Rejected alternative:** keep `parseChargeForm` returning a zero-value `Entry` on failure and add
nil-guards at every template call site. Rejected because `charging.Entry`'s typed fields
(`time.Time`, `*int`, `*float64`) cannot hold a value that failed to parse — the raw submitted
string for an invalid `charged_on` (say, `"2026-13-45"`) has no representation in `time.Time` at
all, so only a parallel string-typed struct can echo it back for the user to correct.

### D-JS — two new zero-JS exceptions, recorded per RD8

Both live in `internal/gateway/static/app.js`, both use `document.body`-scoped delegation (no
per-element `addEventListener` at render time — required so a dynamically-swapped inline edit row
and multiple simultaneously-open edit rows all work with no re-binding step), both are documented
in `internal/gateway/AGENTS.md` as **RD12** and **RD13** in the same change (RD8's own rule).

**RD12 — date→time-preserving sync (roadmap D12).** A `change` listener on `input[name="charged_on"]`
(delegated on `document.body`) that, for each of `started_at`/`ended_at` **within the same
`<form>`** (`evt.target.closest("form")`), rewrites only the date portion when that field is
non-empty: `input.value = newDate + input.value.slice(10)` (a `datetime-local` value is always
`YYYY-MM-DDTHH:MM`, so `.slice(10)` is exactly `"THH:MM"`). An empty `started_at`/`ended_at` is
left empty — never auto-filled (roadmap D12's explicit rejection of "clobber to midnight").
**Rejected alternative:** a CSS-only DaisyUI pattern. Rejected because this is a value
*transformation* (splice one substring into another field's value), which no CSS mechanism can
express — this is not a presentational state toggle DaisyUI's `dropdown`/`<dialog>`/`collapse`
patterns are built for.

**RD13 — status-driven required toggle (D-RM33-6).** A `change` listener on `select[name="status"]`
(delegated on `document.body`) plus an `htmx:load` listener (fires on both the initial full-page
load and every htmx-swapped fragment, so it covers a freshly-swapped-in edit row with no separate
`DOMContentLoaded` handler — verify the event name against current htmx docs via Context7,
`/context7/htmx_org`, before implementing) that, for the `<select>`'s own `<form>`, sets
`ended_at.required` and `end_battery_pct.required` to `select.value === "DONE"`. Running on
`htmx:load` as well as `change` means a freshly-rendered or freshly-swapped form is always correct
immediately, not just after the user's first interaction with the dropdown — this is the literal
ask in D-RM33-6 ("must run on page load as well as on change") and is a deliberate belt-and-braces
duplication of the server-rendered initial state from D-Fields: if the two ever disagree, the
disagreement should be inert (the browser HTML5 validation gate uses whichever `required` state is
live in the DOM, always the JS's after it runs), never something that can only be fixed by
special-casing the template. **Rejected alternative:** no client-side toggle, rely solely on the
server-rendered initial `required` state and let a status change take effect only after a full
submit/re-render round-trip. Rejected because D-RM33-6 explicitly asks for "no htmx round-trip" —
the whole point is the user sees the required asterisk change the instant they pick DONE, before
they've filled in anything else.

Neither listener needs a null-check as defensive as RD10's ("no `#confirm-dialog` on this page")
because both target elements this tier itself guarantees exist wherever a `status` or `charged_on`
input exists (both forms); a page with neither form present simply never fires the delegated
`change` handler's `matches()` check.

### D-Colspan — the entries-table colspan is untouched, flagged for tier 3

`charge_row_edit.templ`'s `<td colspan="8">` matches today's 8-column `charges_list.templ` table.
This tier adds no column and removes no column from that table (it only changes the two *forms*),
so the colspan is correct as-is for this tier and is **not edited here**. Tier 3
(`RM33-gateway-add-entries-dashboard`) adds a Status column, adds a battery-range column, and
removes the Vehicle column and the Refresh action — a net column-count change tier 3 must carry
its own colspan update for. Recorded here so tier 3's design.md does not have to rediscover it.

---

## Roadmap-decision mapping

| Decision | Where honored |
|---|---|
| **D7** — price optional, empty→0, COP as an input suffix | D-Suffix, D-Values (price handling in parseChargeForm) |
| **D9** — table Status column carries both signals | NOT this tier — tier 3 owns the table; this tier's status *control* is the form-side half of the same roadmap decision, cross-referenced here only |
| **D12** — date-change handler preserves time | D-JS (RD12) |
| **D15** — form-clearing bug is a server re-render bug | D-Values |
| **D16** — AC/DC descriptive option text | i18n catalogue change (tasks.md) |
| **D-RM33-5** — both forms in scope | Every decision above applies to both `charge_create_form.templ` and `charge_row_edit.templ` |
| **D-RM33-6** — client-side required toggle, no round-trip | D-JS (RD13), D-Fields (server-rendered initial state) |
| **D-RM33-7** — odometer in "More details" | tasks.md (template placement) |
| **D-RM33-8** — Currency input removed | D-Suffix (COP suffix replaces it), tasks.md |

---

## Test Contract

Fixed here, before implementation, per `ai/go-conventions.md` §Testing authoring order. All of
these are `httptest`-style handler tests in `internal/gateway/handlers/charges_test.go`, mirroring
the existing suite's fake-port style (no DB). No `DATABASE_URL`-gated test is added by this tier —
the gateway owns no database.

### Group A — `parseChargeForm` / handler-level (offline, fake ports)

- **A1.** Submitting the create form with `status=IN_PROGRESS`, no `energy_added_kwh`, no `price`,
  no `ended_at`, no `end_battery_pct`, valid `charged_on`/`location_kind`/`start_battery_pct` →
  `ok == true`, `entry.EnergyAddedKWh == nil`, `entry.Price == 0`, `entry.EndedAt == nil`,
  `entry.EndBatteryPct == nil`, `entry.Status == charging.StatusInProgress`.
- **A2.** Same as A1 but `status=DONE`, still no `ended_at`/`end_battery_pct` → `ok == false`,
  `validationErrors["ended_at"] != ""`, `validationErrors["end_battery_pct"] != ""`, the fake
  `Writer.Create` is never called.
- **A3.** `status=DONE` with `ended_at` and `end_battery_pct` both supplied and valid → `ok ==
  true`, `entry.Status == charging.StatusDone`.
- **A4.** `status` missing or an unrecognized value (e.g. `"BOGUS"`) → `ok == false`,
  `validationErrors["status"] != ""`.
- **A5.** `price=""` → `entry.Price == 0`, no validation error on `price`. `price="-1"` →
  `validationErrors["price"] != ""`, `entry.Price` not asserted (parse failed). `price="150.50"` →
  `entry.Price == 150.50`.
- **A6.** `energy_added_kwh=""` → `entry.EnergyAddedKWh == nil`, no validation error.
  `energy_added_kwh="0"` → `validationErrors["energy_added_kwh"] != ""` (the existing
  "must be positive" rule, now conditioned on non-empty rather than always-on).
- **A7.** `odometer_km=""` → `entry.OdometerKm == nil`. `odometer_km="45210"` →
  `*entry.OdometerKm == 45210`. `odometer_km="-1"` → `validationErrors["odometer_km"] != ""`.
  `odometer_km="abc"` → `validationErrors["odometer_km"] != ""`.
- **A8.** `start_battery_pct` missing, for both `status=IN_PROGRESS` and `status=DONE` →
  `validationErrors["start_battery_pct"] != ""` in both cases (unconditionally required,
  unchanged by this tier).

### Group B — D15 value-preservation (offline, fake ports)

- **B1.** Submit the create form with a valid `location_kind=WORK`, valid battery percentages, but
  an out-of-range `end_battery_pct="150"` under `status=DONE` → the re-rendered 422 body contains
  `value="WORK"`-selected on the `location_kind` option (not the default `HOME`) and the submitted
  (invalid) `"150"` in the `end_battery_pct` input's value — assert via a Templ-rendered-HTML
  substring check, mirroring `TestChargeCreate_MissingRequiredField`'s existing assertion style.
- **B2.** Same shape for the inline edit row (`ChargeRowUpdate`): submit with a typo'd
  `charging_type` bypassing the `<select>` (a raw form POST, since the browser's own `<select>`
  cannot submit an invalid option) alongside an invalid battery percentage → the re-rendered 422
  row still carries the submitted `notes`/`location_label` text.
- **B3.** `status="DONE"`, `ended_at` cleared, everything else valid → the 422 re-render's `status`
  `<select>` still shows `DONE` selected (not reset to `IN_PROGRESS`).
- **B4.** A fresh `GET /charges` (no submission at all) still renders `energy_added_kwh` and
  `price` with no `value` attribute and `location_kind` with no option pre-selected — proving
  `ChargeFormValues{}`'s zero value reproduces today's fresh-load behavior exactly (no
  regression from adding the struct).

### Group C — template/markup assertions (offline, rendered-HTML substring checks)

- **C1.** Both forms: `energy_added_kwh` and `price` inputs carry no `required` attribute.
  `start_battery_pct`, `charged_on`, `location_kind` still carry `required`.
- **C2.** Create form, fresh render: `status` select has `IN_PROGRESS` selected; `ended_at` and
  `end_battery_pct` carry no `required` attribute.
- **C3.** Create form rendered with a fake vehicle whose entry (edit-row equivalent) status is
  `DONE`: `ended_at` and `end_battery_pct` carry `required`.
- **C4.** Edit row for a persisted `DONE` entry: the `status` select's `DONE` option carries
  `selected`, not `IN_PROGRESS`'s.
  Edit row for a persisted `IN_PROGRESS` entry: the reverse.
- **C5.** Neither form renders a Currency `<input>` (by name or by DaisyUI class) anywhere;
  the price input's rendered HTML contains a `<span class="label">COP</span>` inside a
  `<label class="input...">` wrapper.
- **C6.** Both forms' AC/DC `<option>` text matches the roadmap D16 strings (assert both ES and EN
  via the i18n test harness's language-switch pattern, mirroring the catalogue completeness test).
- **C7.** Both forms render an `odometer_km` number input inside the `<details>`/"More details"
  block (assert its markup appears after the `<summary>` tag and before `</details>`).

### Owner-verified, not automatable here

- **JS behavior (RD12/RD13)** — a `httptest` render proves the markup and attributes are correct;
  it cannot execute `static/app.js` in a real DOM. The owner manually verifies: (a) changing the
  create form's date field updates a non-empty `started_at`'s date portion while keeping its time;
  (b) picking `DONE` on either form's status select immediately marks `ended_at`/`end_battery_pct`
  required (visible via the browser's native validation UI on submit) with no network request
  fired; (c) the same on an inline edit row opened via htmx, and on two rows opened simultaneously,
  independently. Record the manual pass/fail in the wave-report, per `Test-Execution-Policy`.
- `make i18n-guard` / `TestCatalog_AllKeysHaveBothLanguages` — deterministic signals Claude runs
  itself (`CLAUDE.md` §Builds & local checks), not owner-only, but listed here because they are
  the actual enforcement mechanism for every new/changed catalogue key this tier adds.
