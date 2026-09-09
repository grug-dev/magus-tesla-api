# Proposal — RM51-gateway-add-free-charge-and-month-preset

Source: MAG-58 — https://linear.app/magus-monitor/issue/MAG-58/chargin-external-charges-v2-done-status
Roadmap: `openspec/roadmaps/RM51-external-charges-completion.md` — **tier 2 of 2**, module `gateway`,
implementing roadmap decisions **RD6–RD8** verbatim. Depends on tier 1
(`RM51-charging-derive-status-and-price-source`, archived), which added
`charging.Entry.PriceSource` (module-computed) and `charging.Entry.PriceConfirmed`
(caller-supplied intent).

Design gate: **NOT tripped.** This change adds no table, column, index, or migration — it is a
form field plus a date-math preset, both inside `internal/gateway`. `openspec/config.yaml` §design
only requires the gate for a database-touching change.

Unit tests: **included** — `internal/gateway`'s standing testing convention applies: offline tests
for `resolvePriceConfirmedRaw`-shaped parsing and for the "last month" date math, `httptest`
handler tests for the checkbox echo and the third preset. Expected values are fixed in design.md
§Test Contract **before** implementation.

---

## Why

Two small gaps left after tier 1 gave the `charging` module a way to record a confirmed real zero
price:

1. **Nothing on the page lets a user confirm one.** `charging.Entry.PriceConfirmed` exists, but no
   form sends it — every zero price still resolves to `UNCONFIRMED`, same as before tier 1 shipped.
2. **The date filter has no "last month" preset**, so checking last month's charges means manually
   typing `?start=&end=`.

RD6 closes the first gap with a plain, always-visible "this charge was free" checkbox next to the
price input — not a blocking prompt, a deliberate divergence from the ticket text, confirmed with
the user. RD7/RD8 close the second with a third preset, previous-calendar-month, on
`/external-charges` only.

## What Changes

- **ADDED** — a `ui.Checkbox` component in `internal/gateway/templates/ui/checkbox.templ`
  (`CheckboxProps{Name, Label, Checked, Class, Attrs}`), the DaisyUI `checkbox` class's adapter.
  No wrapper for a checkbox exists in the kit yet (`ai/htmx-conventions.md` §"Compose the `ui/`
  kit" — a repeated element with no wrapper gets one added to `ui/`, never inlined).
- **ADDED** — a "this charge was free" checkbox (`name="price_confirmed"`), rendered inside the
  same `ui.Field` as the price input, on both `ExternalChargeCreateForm` and
  `ExternalChargeRowEdit`. Always visible, never `disabled`, never blocking the save. **No new
  JavaScript** — the sanctioned zero-JS exception list (`internal/gateway/AGENTS.md` RD9/RD10/
  RD12/RD13/RD14/RD15) stays closed at six; this checkbox needs no client reactivity because
  `charging` (tier 1) already ignores the intent whenever `price > 0`.
- **CHANGED** — `fragments.ExternalChargeFormValues` gains `PriceConfirmed bool` (the raw submitted
  checkbox state, echoed on a 4xx/5xx re-render). `fragments.ExternalChargeEntryVM` gains
  `RawPriceConfirmed bool` (the edit row's checkbox state — the persisted entry's
  `Price == 0 && PriceSource == USER` on a normal render, or the raw submitted value on an error
  re-render).
- **CHANGED** — `parseExternalChargeForm` (`internal/gateway/handlers/external_charges.go`) reads
  `price_confirmed` from the POST body (`c.PostForm("price_confirmed") != ""`, the standard HTML
  checkbox absent-when-unchecked shape) into `raw.PriceConfirmed`, and sets
  `entry.PriceConfirmed = raw.PriceConfirmed` on the parsed `charging.Entry`. No new validation —
  a checkbox has no invalid state. `externalChargeEntryVMFromEntry` and
  `externalChargeEntryVMFromRawValues` (same file) are updated to populate
  `RawPriceConfirmed` from the two respective sources.
- **ADDED** — the RD7 "last month" preset (previous calendar month) as a third entry in
  `buildExternalChargesPresets` (`internal/gateway/handlers/external_charges_range.go`), built from
  the existing `startOfMonth` (`supercharger.go`) and `endOfMonth` (same file) helpers — no new
  date-math primitive. `Active` computed by the same exact-match rule the other two presets already
  use. `/external-charges` only (RD8) — `buildSuperchargerStatsView`'s own presets are untouched.
- **ADDED** — two new i18n catalogue keys in `internal/gateway/i18n/catalog.go`, both `ES` and `EN`
  non-empty: `charges_form.price_confirmed` (the checkbox label) and `charges_range.last_month`
  (the third preset's button label).
- **CHANGED** — `internal/gateway/templates/fragments/external_charge_create_form.templ` and
  `external_charge_row_edit.templ`: the checkbox added beside the price field in both.
- **CHANGED** — `kkpa/context/input-port/charging/external-charges.md` (the form field set and the
  date presets both go stale with this tier — roadmap "Verified codebase findings").

**Out of scope, deliberately (see design.md for the full list):** re-opening RD1–RD5 (tier 1,
already archived); a client-side toggle that shows the checkbox only when `price == 0` (RD6
explicitly rejects the JS this would need); a rolling last-30-days preset (RD7 rejects it);
touching `/supercharger-stats`'s own presets (RD8).

## Breaking?

**NO.** Every change is additive: one new `ui/` component, two new struct fields (both bool,
zero-value `false` matches today's implicit "not confirmed" / "not the persisted free charge"
state), one new preset entry appended to a slice nothing indexes by position
(`buildExternalChargesPresets`'s existing comment already anticipates a third: "no third preset
type" — meaning no new *type*, a third *value* was always expected). No handler signature changes,
no route changes, no interface gains or loses a method.

## Modules affected

- **`gateway`** — owner. Templates, view models, handler parsing, i18n, `AGENTS.md`'s RD-list is
  unaffected (no seventh zero-JS exception is opened).
- **`charging`** — **not touched.** `Entry.PriceConfirmed`/`Entry.PriceSource` and
  `resolvePriceSource` already exist and already apply the RD3 rule table (tier 1). This tier only
  starts sending a real `PriceConfirmed` value instead of the implicit `false` every caller has
  sent since tier 1 shipped.
- No other module reads `internal/gateway`'s templates or handlers.

## Read paths affected

Per `openspec/config.yaml` §proposal. This tier adds no new read — `buildExternalChargesPresets` is
pure date arithmetic over `today` and adds one more `fragments.RangePreset` value to an
already-built slice; it calls no reader. The checkbox is form-only; parsing it costs one extra
`c.PostForm` call per submission, no I/O. `charging.Reader.ListEntriesByVehicle`'s row now includes
`PriceSource` since tier 1 (already `SELECT *`) — this tier is the first caller that *reads* the
field into the response (`externalChargeEntryVMFromEntry`), not the first one to receive it.

## Impact

- **Affected spec:** `gateway` — three **MODIFIED** requirements ("Create Charge Entry", "Inline
  Row Editing" gain the checkbox; "Charge List Date Filter" gains the third preset). No requirement
  removed.
- **Affected code:** `internal/gateway/templates/ui/checkbox.templ` (new),
  `internal/gateway/templates/fragments/external_charge_create_form.templ`,
  `external_charge_row_edit.templ`, `external_charges_vm.go`,
  `internal/gateway/handlers/external_charges.go`, `external_charges_range.go`,
  `internal/gateway/i18n/catalog.go`, new/extended `_test.go` files, `internal/gateway/AGENTS.md`
  (only if a doc claim it makes goes stale — see design.md) — plus
  `kkpa/context/input-port/charging/external-charges.md`.
- **Design gate: not tripped** — no DB object of any kind changes.
- **`MIGRATIONS_DIRS` order check:** not applicable — this tier adds no migration.
- **Deferred, explicitly NOT in scope:** everything tier 1 already settled (RD1–RD5); a JS-driven
  conditional checkbox; a rolling-30-days preset; touching Supercharger Stats' presets.

## Modules affected — summary table

| Module | Change |
|---|---|
| `gateway` | Owner. Checkbox component + wiring, third date preset, i18n, tests, docs. |
| `charging` | None in this tier — consumes the fields tier 1 already added. |
