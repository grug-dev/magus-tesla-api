# Proposal — RM33-gateway-update-charge-form

Source: MAG-18 — https://linear.app/magus-monitor/issue/MAG-18/adding-status-to-manual-records
Roadmap: `openspec/roadmaps/RM33-manual-record-status.md` — **tier 2 of 3**, module `gateway`,
implementing roadmap decisions **D7, D9, D12, D15, D16** verbatim, plus decisions confirmed at the
2026-08-29 tier-2 interview: **D-RM33-5** (both the create form and the inline edit row are in
scope), **D-RM33-6** (client-side required-toggle on the status select), **D-RM33-7** (odometer
lives in "More details"), **D-RM33-8** (the Currency input is removed, replaced by a COP suffix).
Depends on: `RM33-charging-add-entry-status` (tier 1, **archived**) — `internal/charging` already
exports `Status`, `EnergySource`, `Field`, `RequiredFieldsFor(Status) []Field`, and
`Entry.EnergyAddedKWh *float64` / `Entry.OdometerKm *int` / `Entry.EnergySource` (always
module-computed).

Design gate: **NOT tripped.** This change touches no database object — no migration, no column,
no index. `internal/charging`'s tier-1 schema is consumed as-is through the existing `Reader` /
`Writer` ports. `CLAUDE.md` §Pipeline config → `Design-Gates: database` therefore does not apply;
design.md is still written up front (mandatory for this change per the dispatch's artifact rules)
because it fixes the Test Contract before implementation, not because a schema changed.

Unit tests: **included** — the roadmap's standing decision (MAG-18/RM33, confirmed at the Step 2
interview), unchanged for this tier. Both offline unit tests (form parsing, the JS behavior
documented as a manual/owner-verified affordance — see design.md §Test Contract) and the
existing `httptest`-based handler test style (`internal/gateway/handlers/charges_test.go`).

---

## Why

Tier 1 gave `internal/charging` a `Status`-conditional required-field rule, optional energy,
and an `odometer_km` column — but the gateway's two charge forms (`charge_create_form.templ`,
`charge_row_edit.templ`) still render the *old* contract: `energy_added_kwh` and `price` marked
`Required`, `start_battery_pct`/`end_battery_pct` unconditionally required, no `status` control at
all (so the module's normalize-empty-to-`IN_PROGRESS` path is the only thing exercised today), a
disabled `Currency` input nobody can act on, plain `AC`/`DC` option text, and no `odometer_km`
input anywhere. MAG-18's headline flow — save a charge as **IN PROGRESS** with only what is known
at plug-in time, then complete it to **DONE** later from the inline edit row — is impossible until
the gateway grows a status control and lets the required-field set follow it.

Two more defects ride along because they live in the same two files this tier is already touching:

- **D15 — the 4xx/5xx re-render blanks the form.** `ChargeCreate` re-renders
  `fragments.ChargeCreateForm(d, validationErrors)` at HTTP 422 with a **freshly built**
  `ChargesPageData` that carries only the day-default fields (`DefaultChargedOn`/
  `DefaultStartedAt`/`DefaultEndedAt`). Every other input — energy, price, the battery
  percentages, `location_kind`'s selection, charging type, location label, notes — has no
  mechanism to echo what the user actually typed, so a single invalid field (e.g. an out-of-range
  battery percentage) wipes the entire form the user just filled in. htmx already swaps the 4xx
  body here (`renderError` sets `HX-Error-Fragment`), so the fix is purely about what gets
  rendered, not how it gets swapped.
- **D16 — AC/DC is a cryptic two-letter choice.** The ticket asks the option text itself to
  explain what each charging speed means, in both languages.

## What Changes

- **ADDED** — a `status` `<select>` (`IN PROGRESS` / `DONE`, i18n, values `IN_PROGRESS`/`DONE`) at
  the **top** of the main field grid on both `ChargeCreateForm` and `ChargeRowEdit`, defaulting to
  `IN_PROGRESS` on the create form and to the entry's **persisted** status on the edit row
  (pre-selected, not defaulted).
- **CHANGED** — `energy_added_kwh` and `price` drop `Required` on both forms. `price` gains a
  `COP` suffix rendered **inside** the input (a new `ui.InputProps.Suffix` capability, DaisyUI v5's
  `<label class="input"><input .../><span class="label">COP</span></label>` idiom, verified via
  Context7 against `/saadeghi/daisyui`); the standalone Currency field is **removed** from both
  forms (**D-RM33-8**). `parseChargeForm` accepts an empty `price` as `0` (never negative) and an
  empty `energy_added_kwh` as `nil` (module derivation on write is untouched, tier-1 territory).
- **CHANGED** — `end_battery_pct` and `ended_at` are required **only when the submitted `status`
  resolves to `DONE`** (`charging.RequiredFieldsFor`, imported by the gateway — no reimplemented
  rule, **D-Fields**). `start_battery_pct`, `charged_on`, and `location_kind` stay unconditionally
  required (outside `RequiredFieldsFor`'s skip set / not covered by it at all).
- **ADDED** — a client-side listener (in `static/app.js`, two new sanctioned zero-JS exceptions —
  **RD12**, **RD13** — recorded in `internal/gateway/AGENTS.md`) that (a) rewrites the
  `YYYY-MM-DD` part of `started_at`/`ended_at` when `charged_on` changes, preserving `HH:MM` and
  never auto-filling an empty datetime (**D12**), and (b) toggles the `required` attribute on
  `ended_at`/`end_battery_pct` when the `status` select changes, running once on load and on every
  change, scoped per-form so multiple open inline edit rows behave independently (**D-RM33-6**).
  The server still renders the *initial* `required` state from `RequiredFieldsFor` — the JS is a
  UX affordance on top of a real server-side default, not the only source of it.
- **ADDED** — an optional `odometer_km` integer input inside the "More details" `<details>` section
  of both forms, alongside charging type / location label / notes (**D-RM33-7**).
- **CHANGED** — the `AC`/`DC` `<option>` text on both forms to the descriptive labels from
  roadmap **D16** (values unchanged: `AC`/`DC`).
- **FIXED (D15)** — a new `fragments.ChargeFormValues` struct carries the **raw submitted POST
  values** (not the parsed/typed `charging.Entry`) back into the 4xx/5xx re-render of both the
  create form and the inline edit row, so every field the user typed — valid or not — survives a
  validation failure. `parseChargeForm`'s signature grows a return value carrying this struct.
- **REMOVED** — `KeyChargesFormCurrency`, `KeyChargesErrorEnergyRequired`,
  `KeyChargesErrorPriceRequired` from the i18n catalogue (dead after the field/validation changes
  above — verified with `grep` before deletion, see tasks.md).
- **ADDED** — i18n catalogue keys for: the status control and its two option labels, the odometer
  label, the (revised) AC/DC option text, and the new/changed validation messages (ended_at
  required, odometer invalid, status invalid). Every new/edited key carries both `ES` and `EN`
  (`internal/gateway/AGENTS.md` §i18n).

**Out of scope, deliberately:** the Status *column* in the entries table (completeness dot +
badge), the battery-range column, removing the Vehicle column/Refresh action, the date filter, and
the aggregation tiles — all tier 3 (`RM33-gateway-add-entries-dashboard`). This change touches
`charge_create_form.templ` and `charge_row_edit.templ` only among the manual-charge templates;
`charges_list.templ` and `charge_row.templ` are tier 3's. `charge_row_edit.templ`'s
`<td colspan="8">` is **not** adjusted here even though tier 3 will change the table's column
count — flagged for tier 3, not fixed in this tier (see design.md).

## Breaking?

**NO** — this change is additive/corrective at the gateway's own surface and does not change any
Go interface `internal/gateway` exposes to other modules (the gateway exposes none consumed by
siblings; it is the leaf of the dependency graph). `parseChargeForm`'s signature change is a
**private, package-internal** function signature (lowercase `h *Handler) parseChargeForm`, called
only from within `internal/gateway/handlers`) — no external caller exists to break. Every
`.templ`/`_templ.go` change is presentation-only.

## Modules affected

- **`gateway`** — owner of every file this tier touches: `templates/fragments/charge_create_form.templ`,
  `templates/fragments/charge_row_edit.templ`, `templates/fragments/charges_vm.go`,
  `templates/ui/input.templ`, `handlers/charges.go`, `i18n/catalog.go`, `static/app.js`,
  `AGENTS.md`.
- **`charging`** — consumed read-only through its existing public surface
  (`charging.Status`, `charging.StatusInProgress`, `charging.StatusDone`, `charging.Field`,
  `charging.RequiredFieldsFor`). No change to `internal/charging` in this tier.
- No other module. No database object of any kind is touched — no migration belongs to this
  change.

## Read paths affected

None. This tier changes form rendering and form parsing only; it adds no query, no new call to
`charging.Reader`, and does not change `buildChargesPage`'s existing single `ListEntriesByVehicle`
/ `ListEntriesByAccount` read per render. `charging.Writer.Create`/`Update` are called exactly as
often as before — once per successful submission — with a request payload that now legitimately
carries `nil` energy/price-as-zero/`nil` `EndedAt`/`EndBatteryPct` for an `IN_PROGRESS` entry.

## Impact

- **Affected spec:** `gateway` — two **MODIFIED** requirements ("Create Charge Entry", "Inline Row
  Editing" — both currently state `start_battery_pct`/`end_battery_pct` are unconditionally
  required and that `energy_added_kwh`/`price` are required) and three **ADDED** requirements
  (status-conditional required fields on the form; value-preserving validation re-render; the
  COP-suffixed, Currency-free price/odometer/AC-DC-label presentation). `manual-charge-log`
  (the `charging` capability spec) is **untouched** — no domain rule changes in this tier.
- **Affected code:** `internal/gateway/templates/fragments/charge_create_form.templ`,
  `internal/gateway/templates/fragments/charge_row_edit.templ`,
  `internal/gateway/templates/fragments/charges_vm.go`,
  `internal/gateway/templates/ui/input.templ`, `internal/gateway/handlers/charges.go`,
  `internal/gateway/i18n/catalog.go`, `internal/gateway/static/app.js`,
  `internal/gateway/AGENTS.md` (two new RD entries, §i18n, §Public interface untouched).
- **Deferred, explicitly NOT in scope:** every tier-3 table/dashboard change (see roadmap); the
  `charge_row_edit.templ` colspan fix (flagged for tier 3); any change to
  `internal/charging`'s domain rules, schema, or `packCapacityKWh` (tier 1's territory,
  archived); the DESIGN.md → custom-theme translation of any new visual pattern beyond the
  `ui.InputProps.Suffix` addition.
