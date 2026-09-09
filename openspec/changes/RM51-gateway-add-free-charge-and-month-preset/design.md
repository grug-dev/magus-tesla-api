# Design — RM51-gateway-add-free-charge-and-month-preset

No design gate. This change touches no database object — no table, column, index, or migration.
`openspec/config.yaml` §design only requires owner confirmation for a DB-touching change; this is
form markup, view-model fields, and date arithmetic, all inside `internal/gateway`.

Source ticket: MAG-58 · Roadmap: `openspec/roadmaps/RM51-external-charges-completion.md`, tier 2 of
2. Roadmap decisions **RD6–RD8** are binding and were confirmed with the user at the 2026-09-09
`grill-me` interview. This document does not re-open them, and it does not re-open **RD1–RD5**
(tier 1, already archived at
`openspec/changes/archive/charging/2026-09-09-RM51-charging-derive-status-and-price-source/`).
Where this document says something the roadmap does not, it says so explicitly (**D-Checkbox**,
**D-Order** below).

---

## Context

Facts read out of the repository, not recalled.

1. **Tier 1 already shipped `charging.Entry.PriceConfirmed bool` and `charging.Entry.PriceSource
   PriceSource`** (`internal/charging/charging.go`). `PriceConfirmed` is read only when
   `Price == 0` and is never persisted directly; `PriceSource` is module-computed and always
   overwrites any caller-supplied value. This tier's ONLY job on the write side is to start
   sending a real `PriceConfirmed` — every gateway write today implicitly sends `false` (the Go
   zero value), which is why every zero price still resolves to `UNCONFIRMED` after tier 1.
2. **No checkbox wrapper exists in `templates/ui/`** (`grep -rn "checkbox" internal/gateway/
   templates/ui/`). `ui.Field` renders a `fieldset`/`legend` shaped for a labeled input, select, or
   textarea — wrong shape for a checkbox, whose label sits beside the control, not above it.
   `ai/htmx-conventions.md` §"Compose the `ui/` kit": a raw DaisyUI component class in a
   page/fragment is a bug; a repeated element with no wrapper gets one added to `ui/`. This is a
   new, small, reusable wrapper — not an over-abstraction: `checkbox` is a genuine DaisyUI
   component class, used at two call sites in this tier alone.
3. **`parseExternalChargeForm` builds `raw` from `c.PostForm(...)` calls FIRST**, before any other
   parsing, so every field is populated identically on every return path
   (`internal/gateway/handlers/external_charges.go:1212`, design.md §D-Values precedent from
   RM33/MAG-18). The new `price_confirmed` field follows the same shape.
4. **A checkbox's wire format is presence, not value.** An unchecked HTML checkbox is not sent in
   the POST body at all; a checked one sends its `value` attribute (browser default `"on"` when
   none is set). `c.PostForm("price_confirmed")` therefore returns `""` on BOTH "unchecked" and
   "field absent entirely" — there is no third state to distinguish, unlike every other field on
   this form. The correct parse is presence: `c.PostForm("price_confirmed") != ""`.
5. **`buildExternalChargesPresets`'s own existing comment already anticipated a third preset**
   (`internal/gateway/handlers/external_charges_range.go:80`: "no third preset type" — meaning no
   new *type* is needed, not that a third value was unplanned). Adding one is an `append`, not a
   restructure.
6. **`endOfMonth` already exists in the same file** (`external_charges_range.go:74`) and
   **`startOfMonth` already exists in `supercharger.go:57`** — both reused verbatim, no new date
   primitive (roadmap RD7).
7. **`buildSuperchargerPresets` (`supercharger.go:267`) is a SEPARATE function** from
   `buildExternalChargesPresets` — the two pages have never shared a preset builder. Adding a
   preset to one cannot touch the other by construction, not just by discipline (roadmap RD8).
8. **The gateway never special-cases `price > 0` for this checkbox.** Tier 1's `resolvePriceSource`
   already ignores `PriceConfirmed` whenever `Price > 0` (tier 1 design.md D5, "a positive price
   always wins"). The gateway's job is to pass the submitted intent through unconditionally — never
   to duplicate that precedence rule client-side or server-side in `internal/gateway`.

## Goals / Non-Goals

**Goals**

- A user can mark a zero-priced charge as a confirmed real free charge, from both the create form
  and the inline edit row, with one checkbox click and no extra round-trip.
- A user can filter the charge list to the previous calendar month with one click, same as the
  existing two presets.
- Neither change adds a database object, a new route, or a line of client-side JavaScript.

**Non-Goals**

- Re-deciding anything RD1–RD5 already settled (tier 1). This change does not touch
  `internal/charging`.
- A prompt, a 422 confirm panel, or a `confirm()` interceptor for the free-charge case — RD6
  explicitly rejects all three.
- Hiding or disabling the checkbox based on the price value — would need client-side JS, which is
  exactly what RD6 avoids.
- A rolling last-30-days window (RD7 rejects it — overlaps "this month" for most of any month).
- Any change to `/supercharger-stats`'s own preset selector (RD8).

---

## Decisions

### D-Checkbox — a new `ui.Checkbox` component, placed inside the price `ui.Field`

*Implements roadmap **RD6**.*

```go
// CheckboxProps configures a Checkbox. Label renders beside the control, inside
// the same <label> element, so the whole row is one click target. Checked maps
// to the HTML boolean `checked` attribute. Class is for extra layout utilities.
type CheckboxProps struct {
    Name    string
    Label   string
    Checked bool
    Class   string
    Attrs   templ.Attributes
}
```

```templ
templ Checkbox(p CheckboxProps) {
    <label class={ "label cursor-pointer justify-start gap-2", p.Class }>
        <input type="checkbox" name={ p.Name } class="checkbox" checked?={ p.Checked } { p.Attrs... }/>
        <span class="label-text">{ p.Label }</span>
    </label>
}
```

New file: `internal/gateway/templates/ui/checkbox.templ`. The DaisyUI `checkbox` class is owned
here exactly like `ui.Input` owns `input` and `ui.Button` owns `btn` — an engine upgrade that
renames or restructures it is a one-file change (Context fact 2).

**Placement — inside the price `ui.Field`, as a second child, not a new grid cell.** The form's
main grid is `grid gap-3 sm:grid-cols-2`; each `ui.Field` occupies one cell. A checkbox as its own
grid cell would land in the row AFTER price, not beside it. Placing it as a second child inside the
SAME `ui.Field` that wraps the price `ui.Input` keeps it visually and structurally attached to
price — "next to the price input" in the roadmap's own words — with no grid math and no new
`sm:col-span` bookkeeping:

```templ
@ui.Field(ui.FieldProps{Label: i18n.T(ctx, i18n.KeyChargesFormPrice), Error: validationErrors["price"], Optional: true}) {
    // i18n:allow: ISO currency code
    @ui.Input(ui.InputProps{Type: "number", Name: "price", Value: d.FormValues.Price, Suffix: "COP", Attrs: templ.Attributes{"step": "0.01", "min": "0"}})
    @ui.Checkbox(ui.CheckboxProps{Name: "price_confirmed", Label: i18n.T(ctx, i18n.KeyChargesFormPriceConfirmed), Checked: d.FormValues.PriceConfirmed, Class: "mt-1"})
}
```

Same shape in `ExternalChargeRowEdit`, with `Checked: vm.RawPriceConfirmed` instead.

**Always visible, never `disabled`.** No `Disabled:` binding on price value — that would need a
`change` listener on the price input to react live, which is exactly the JS RD6 rules out. The
checkbox is interactive at every price; a checked box on a `price > 0` submission is simply ignored
by `charging` (Context fact 8), with no error and no visible feedback — RD6's own accepted cost.

| Alternative | Why rejected |
|---|---|
| A new grid cell after price | Lands in the NEXT row on the 2-column grid, not beside price — fails the roadmap's own placement ask. |
| `Disabled` when `price != "0"`/empty, toggled by a `change` listener | Needs new JS — RD6 explicitly avoids this; the sanctioned-exception list stays closed at six. |
| Inline raw `<input type="checkbox" class="checkbox">` in the page/fragment | Violates `ai/htmx-conventions.md` §"Compose the `ui/` kit" — a DaisyUI component class never appears outside `ui/`. |

### D-Parse — presence, not value; no validation; always passed through

*Implements roadmap **RD4**'s gateway half (tier 1) and **RD6**.*

```go
raw := fragments.ExternalChargeFormValues{
    // ...existing fields unchanged...
    PriceConfirmed: c.PostForm("price_confirmed") != "",
}
```

`entry.PriceConfirmed = raw.PriceConfirmed` is set alongside the other `charging.Entry{...}` field
assignments — no branch, no error path. A checkbox has exactly two wire states (present / absent,
Context fact 4), so unlike every text field on this form, there is nothing to validate. `raw` is
built FIRST as usual (Context fact 3), so `PriceConfirmed` is populated on every return path,
including the ownership-403 short-circuit at the top of the function.

**Gateway does NOT re-implement the `price > 0` precedence rule.** `entry.PriceConfirmed` is set
unconditionally from the submitted checkbox state, regardless of the submitted price. Test Contract
**B3** proves the gateway sends `true` through even when `price > 0` — the ignoring happens in
`charging` (tier 1, already tested there), not here. Duplicating that check in the gateway would be
two places asserting the same rule, the exact drift `ai/architecture.md`'s boundary rules and this
project's AI-efficiency principle (`CLAUDE.md`) both warn against.

### D-Echo — the checkbox state on every render path

*Implements roadmap RD6's "preserve it on the error re-render path" instruction, verbatim.*

Three render paths, three sources, mirroring how every other field on this form already works:

| Render path | Source | Field |
|---|---|---|
| Create form, fresh load | zero value | `d.FormValues.PriceConfirmed` (`false` — unchecked, matches "no submission yet") |
| Create form, 4xx/5xx re-render | the submitted `raw` | `d.FormValues = raw` (existing assignment, no new call site) |
| Edit row, normal open | the persisted entry | `vm.RawPriceConfirmed = e.Price == 0 && e.PriceSource == charging.PriceSourceUser` |
| Edit row, 4xx/5xx re-render | the submitted `raw` | `externalChargeEntryVMFromRawValues`: `RawPriceConfirmed: raw.PriceConfirmed` |

The edit row's normal-open formula is new logic (not a straight field copy) because the checkbox
does not correspond 1:1 to a stored column — `PriceConfirmed` is never persisted (tier 1 design.md
D3: "a round-trip through `Reader` always returns `PriceConfirmed: false` on every entry"). The
checkbox's job on open is to show the CONSEQUENCE that would reproduce the current stored state if
re-submitted unchanged: a confirmed free charge is exactly `Price == 0 && PriceSource ==
PriceSourceUser`. A `price > 0` entry always renders the box unchecked, which is correct — ticking
it on a positive-price entry is a no-op per D-Parse, so showing it unchecked never misleads.

No new struct is needed for this: `fragments.ExternalChargeFormValues` gains `PriceConfirmed bool`;
`fragments.ExternalChargeEntryVM` gains `RawPriceConfirmed bool`. Both are added next to their
sibling `Raw*`/status fields, following the file's existing field-grouping convention
(`external_charges_vm.go`).

| Alternative | Why rejected |
|---|---|
| Store `PriceConfirmed` as a real column | Tier 1 already rejected this (tier 1 design.md D3) — redundant with `price_source`, which already encodes everything needed. Out of this tier's scope to re-open. |
| Derive the edit-row checked state from `RawPrice == "0"` alone (ignore `PriceSource`) | Would check the box for every UNCONFIRMED zero-price entry too, misrepresenting rows nobody ever confirmed as if they had been. |

### D-Order — the "last month" preset is a third, appended entry

*Implements roadmap **RD7**, verbatim math.*

```go
func buildExternalChargesPresets(ctx context.Context, start, end, today time.Time) []fragments.RangePreset {
    last7Start := today.AddDate(0, 0, -(externalChargesRangeDefaultDays - 1))
    monthStart := startOfMonth(today)
    monthEnd := endOfMonth(today)
    prevStart := startOfMonth(today).AddDate(0, -1, 0)
    prevEnd := endOfMonth(prevStart)
    return []fragments.RangePreset{
        {Label: i18n.T(ctx, i18n.KeyChargesRangeLast7Days), StartStr: last7Start.Format("2006-01-02"), EndStr: today.Format("2006-01-02"), Active: start.Equal(last7Start) && end.Equal(today)},
        {Label: i18n.T(ctx, i18n.KeyChargesRangeThisMonth), StartStr: monthStart.Format("2006-01-02"), EndStr: monthEnd.Format("2006-01-02"), Active: start.Equal(monthStart) && end.Equal(monthEnd)},
        {Label: i18n.T(ctx, i18n.KeyChargesRangeLastMonth), StartStr: prevStart.Format("2006-01-02"), EndStr: prevEnd.Format("2006-01-02"), Active: start.Equal(prevStart) && end.Equal(prevEnd)},
    }
}
```

**Order is last 7 days, this month, last month** — the order the roadmap itself lists them in
("The filter row gains a third preset next to 'last 7 days' and 'this month'"), and the order a
user reads left-to-right as "shortest window first, most recent first." No reordering of the two
existing entries.

`time.Time.AddDate(0, -1, 0)` on `startOfMonth(today)` correctly rolls the year backward at a
January boundary — Go's `AddDate` normalizes month overflow/underflow across year boundaries, so no
special-cased "if January" branch is needed. Test Contract **C2** proves this rather than trusting
the stdlib claim unverified.

`fragments.RangePreset` is reused unchanged — no new type (Context fact 5's comment already said
so). `buildSuperchargerPresets` is untouched (Context fact 7; RD8).

| Alternative | Why rejected |
|---|---|
| A rolling last-30-days window | Rejected at the interview (roadmap RD7) — overlaps "this month" for most of any month, so the two buttons would look redundant. |
| A new `monthsBack(n)` helper generalizing `startOfMonth`/`endOfMonth` | No second caller exists yet; a two-line inline computation is cheaper to read than a one-caller abstraction (`CLAUDE.md` "do not over-abstract"). Revisit only if a fourth month-relative preset is ever added. |

---

## i18n — new catalogue keys

Both added to `internal/gateway/i18n/catalog.go`, mirroring the existing `charges_form.*` /
`charges_range.*` entries' exact style (`Key` constant + one map entry, `ES`/`EN` on the same
line):

| Key | ES | EN |
|---|---|---|
| `charges_form.price_confirmed` | "Esta carga fue gratis" | "This charge was free" |
| `charges_range.last_month` | "Mes pasado" | "Last month" |

`TestCatalog_AllKeysHaveBothLanguages` and `make i18n-guard` both cover these — no exemption
marker needed, since both are real user-facing copy.

---

## Test Contract

Expected values authored **before** implementation, per `ai/go-conventions.md` §Testing. Tests
written later must assert THIS contract.

**Conventions** (mirrors `internal/gateway/AGENTS.md` §Testing and §"Do not test what the page
looks like"): `httptest` against `NewEngine` with `Deps` fakes for anything hitting a handler;
assert attributes (`checked`, `name`, `value`) and status codes, never markup order or CSS classes;
pure-function cases need no `httptest` at all.

### Group A — offline unit tests (no DB, no `httptest`)

New/extended file: `internal/gateway/handlers/external_charges_range_test.go` (extended for C1–C5
below) plus new checkbox-parsing cases inside the existing
`internal/gateway/handlers/external_charges_test.go` for A1–A2 (mirrors where
`parseExternalChargeForm`'s other field cases already live).

| ID | Input | Expected | What it proves |
|---|---|---|---|
| **A1** | POST includes `price_confirmed=on` | `raw.PriceConfirmed == true` | Checked box parses to `true` (D-Parse). |
| **A2** | POST omits `price_confirmed` entirely | `raw.PriceConfirmed == false` | Unchecked/absent parses to `false` — the only other real browser-sent state (D-Parse, Context fact 4). |

### Group B — handler tests through `httptest` (price>0 pass-through, error echo)

Same file(s) as the existing create/edit handler tests, new cases appended.

| ID | Scenario | Expected | What it proves |
|---|---|---|---|
| **B1** | POST the create form with `price=100.00`, `price_confirmed=on`, all other fields valid | `charging.Writer.Create` (fake) receives `Entry.PriceConfirmed == true`, `Entry.Price == 100.00` | The gateway passes the checkbox through UNCONDITIONALLY — it does not special-case `price > 0` itself (D-Parse, Context fact 8). The ignoring is `charging`'s job, already proven in tier 1. |
| **B2** | POST the create form with `price=""`, `price_confirmed=on`, all other fields valid | `Entry.PriceConfirmed == true`, `Entry.Price == 0` | The confirmed-free-charge path through the real form parse. |
| **B3** | POST the create form with `price_confirmed=on` AND an invalid `location_kind` (triggers 422) | HTTP 422; re-rendered form's `price_confirmed` `<input>` carries the `checked` attribute | **RD6's error-preservation requirement**, proven on markup: `checked` is a data-binding attribute, explicitly on the required-assertions list (`AGENTS.md` §"Do not test what the page looks like"). |
| **B4** | PUT the edit row with `price_confirmed` omitted AND an invalid `location_kind` (triggers 422) | HTTP 422; re-rendered row's `price_confirmed` `<input>` carries NO `checked` attribute | Symmetric to B3 for the edit row, and for the "was unchecked" case. |
| **B5** | An existing entry stored with `Price == 0`, `PriceSource == PriceSourceUser` (tier 1 shape) | `GET` its edit fragment; `price_confirmed` `<input>` carries `checked` | The edit row shows the TRUE current state of a previously-confirmed free charge (D-Echo). |
| **B6** | An existing entry stored with `Price == 0`, `PriceSource == PriceSourceUnconfirmed` | `GET` its edit fragment; `price_confirmed` `<input>` carries NO `checked` | The edit row does not fabricate a confirmation that was never given (D-Echo). |
| **B7** | An existing entry stored with `Price == 8000.00` (any `PriceSource`) | `GET` its edit fragment; `price_confirmed` `<input>` carries NO `checked` | A positive-price entry never shows a stale/misleading "confirmed free" state (D-Echo). |

### Group C — "last month" preset (pure function, no DB, no `httptest`)

| ID | `today` | Expected `prevStart` / `prevEnd` | What it proves |
|---|---|---|---|
| **C1** | `2026-09-09` | `2026-08-01` / `2026-08-31` | The headline example from the roadmap itself (RD7). |
| **C2** | `2027-01-15` (January — the previous month crosses a year boundary) | `2026-12-01` / `2026-12-31` | `AddDate(0, -1, 0)` rolls the year back correctly with no special-cased branch (D-Order). |
| **C3** | `2026-09-09`, called with `start=2026-08-01, end=2026-08-31` | The THIRD preset's `Active == true`; the other two `Active == false` | The "last month" preset is selectable and exclusive against the other two (exact-match rule, unchanged). |
| **C4** | `2026-09-09`, called with `start=2026-09-03, end=2026-09-09` (last 7 days) | The FIRST preset's `Active == true`; the other two `Active == false`, including the new third one | Adding a third preset does not change the first preset's own `Active` computation (regression guard). |
| **C5** | any `today` | `buildExternalChargesPresets` returns exactly 3 entries, in order Last 7 days, This month, Last month | Pins the count and the order (D-Order) — a regression that silently reorders or drops one is caught structurally, not just by the label text. |

### Group D — `/supercharger-stats` is untouched (regression guard)

| ID | Assertion | What it proves |
|---|---|---|
| **D1** | `buildSuperchargerPresets` still returns exactly 2 entries | RD8 — this tier adds no preset to the OTHER page. Cheap to assert; expensive to silently regress. |

---

## Risks

1. **The checkbox's checked state on an edit row is a derived read, not a stored fact (D-Echo).**
   A future reader who expects `PriceConfirmed` to round-trip through `Reader` will not find it —
   this is intentional and matches tier 1's own documented Risk 2. Recorded here again so a
   gateway-side agent does not "fix" this by adding a field.
2. **A checked box on a `price > 0` submission is silently ignored, with no error and no visible
   feedback** — RD6's own accepted cost, inherited unchanged from the roadmap. Not a new risk this
   tier introduces; recorded because this is the file that wires the checkbox to that behavior.
