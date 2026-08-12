Source: MAG-5 — https://linear.app/magus-monitor/issue/MAG-5/new-records-improvements
Module: gateway
Breaking: no

## Why

The Charge log page (`internal/gateway/`, `GET /charges` + the `/ui/charges*` htmx
fragment routes) is the user's path for entering manual charge records. MAG-5 reports
six concrete annoyances and bugs users hit on that page today:

1. **Deleting a manual record is broken and shows an alert.** The Delete button on a
   charge row (`fragments.ChargeRow` → `hx-delete="/ui/charges/row/:id"`,
   `internal/gateway/handlers/charges.go:294` `ChargeRowDelete`) is not removing the
   row in the UI and instead surfaces an alert to the user. A manual charge record
   cannot currently be deleted through the page.
2. **The Vehicle input field is redundant.** The create form renders a Vehicle
   picker (`fragments.ChargeCreateForm`, the `@ui.Field(ui.FieldProps{Label: "Vehicle"…})`
   block in `charge_create_form.templ`). Its value is already driven by the sidebar
   vehicle selector — the menu-level switcher is the single source of truth for "which
   vehicle am I working on" on every other vehicle-scoped page (see
   `internal/gateway/AGENTS.md` §"Vehicle-scoped reads"). Asking the user to also pick
   it on the form is double-entry.
3. **Currency should always be COP.** The form shows a free-text Currency input
   (`@ui.Field(ui.FieldProps{Label: "Currency"…})`, `Value: "COP"`, `Required: true`).
   The platform only operates in COP today, so the field is noise the user can fill
   wrong; it should be fixed to COP and removed from the editable surface.
4. **Start and end battery % should be required, and the start field should suggest
   the latest known battery level.** Today `start_battery_pct` and `end_battery_pct`
   are optional, so a charge entry can be saved with no battery delta data, which makes
   the battery-delta derived metric (`manualcharge.Entry.BatteryDelta()`) silently
   nil. The start field should additionally carry a **suggestion label**
   (placeholder/helper text) showing the active vehicle's latest telemetry battery %,
   when one exists — so the user does not have to leave the form to look it up. When
   no telemetry snapshot exists for the active vehicle, no suggestion is rendered
   (graceful empty — no fabricated number).
5. **The optional `started_at` / `ended_at` date fields are buried in "More details"
   and don't default to today.** They live inside the `<details>` "More details"
   disclosure in `charge_create_form.templ`, so a user completing the main "Log a
   charge" form never sees them unless they expand the section. The author wants them
   pulled up into the main "Log a charge" section with today's date (`YYYY-MM-DD`)
   auto-filled as the default — still optional, but visible and pre-populated.
6. **"Energy added (kWh)" rejects 3-decimal precision.** The `energy_added_kwh`
   input is declared `step="0.01"` today, so the browser's native validation blocks a
   3-decimal value (e.g. `7.345`) and shows the nearest-valid-range helper label
   ("please use the nearest valid value: X.XX"). The existing gritty validation is
   good behavior to keep — but it should apply after **3** decimals, not 2.

### Decisions settled upstream (grill-me output — bake in, do not re-litigate)

- **D1 (item 5)** — MOVE the `started_at` / `ended_at` fields out of "More details"
  up into the main "Log a charge" section, auto-fill today's date (`YYYY-MM-DD`) as
  the default, and keep both fields OPTIONAL. Author's lean, user-confirmed.
- **D2 (item 4)** — The `start_battery_pct` field gets a **label suggestion**
  (placeholder / helper label, NOT a forced value) showing the active vehicle's
  **latest telemetry snapshot battery %**. Source: the gateway's already-injected
  `telemetry.Reader` port (`LatestSnapshotsByAccount`). When no telemetry snapshot
  exists for the active vehicle, render no suggestion (graceful empty). Do NOT add a
  new `telemetry.Reader` method, and do NOT touch the telemetry module's database or
  queries.

## What Changes

Primary module: **`internal/gateway/` only.** No other `internal/` module is touched.

- **Delete-row bug fix (D3).** Root-cause and fix the `ChargeRowDelete` failure that
  surfaces an alert instead of removing the row. Today the handler returns
  `fragments.ChargeRowEmpty(id)` on success (an empty `<tr id="charge-row-<id>"></tr>`
  swapped in by `hx-swap="outerHTML"` targeting `#charge-row-<id>`, per
  `charge_row.templ`). The likely cause is one of: a stale CSRF token, a wrong htmx
  target/swap, or a non-2xx path being hit (e.g. `checkCSRF` failing silently, or the
  Writer returning an error rendered via `fragments.ChargeRowError`). The fix is
  gateway-only — no `manualcharge` service change.
- **Remove the Vehicle input field (D4).** Drop the `@ui.Field(ui.FieldProps{Label:
  "Vehicle"…})` block from the create form. Source the vehicle instead from the
  already-resolved active menu vehicle (`h.resolveSelectedVehicle`, the same call
  `ChargePage`/`ChargesListFragment` already make to scope the entry list). The form
  no longer submits a `vehicle` field; the handler derives `(tesla_id, vin)` from the
  session-selected vehicle and runs the same tenant-ownership check against
  `account.RegisteredVehicles` that it runs today.
- **Disable the Currency field + always send COP (D5).** Render Currency as a
  **disabled** input pre-filled with `COP` (kept as a visible, read-only field for
  user transparency), and have `parseChargeForm` hardcode `Currency: "COP"` on the
  built `manualcharge.Entry` regardless of the submitted form value. The
  `manualcharge.Entry.Currency` column and the `manualcharge.Writer` interface are
  unchanged — the gateway always hands `"COP"` down.
- **Make start and end battery % required + add start-battery suggestion label (D6 +
  D2).** `start_battery_pct` and `end_battery_pct` become required form fields
  (validation error when empty or out of 0–100). The start field additionally renders
  a suggestion label built from the active vehicle's latest telemetry snapshot
  `BatteryLevelPct` (read via `telemetry.Reader.LatestSnapshotsByAccount`, picking the
  snapshot whose `TeslaID == selectedTeslaID`). When no snapshot exists for the
  active vehicle, no suggestion text is rendered. The suggestion is a **placeholder /
  helper label**, not a forced value — the user may still type any 0–100 integer.
- **Move date fields up + default today (D1).** The `started_at` and `ended_at`
  fields are moved out of the `<details>` "More details" block up into the main "Log a
  charge" card, alongside Date / Energy / Price. Both are auto-filled with today's
  date in `YYYY-MM-DD` form (a default value, not a required value) and remain
  OPTIONAL. (Note: these are `datetime-local` inputs in the current form; the today's-
  date default applies to the date portion. Whether the field stays `datetime-local`
  or simplifies to `date` is an implementer choice scoped by design.md, not a behavior
  change this proposal gates.)
- **Allow 3 decimals on energy + keep the nearest-valid-range helper, applied after 3
  decimals (D7).** Change `energy_added_kwh`'s `step` from `"0.01"` to `"0.001"` so
  the browser's native validation permits up to three decimals. The existing
  nearest-valid-range helper label behavior (which nudges the user toward the nearest
  valid step value when they type more precision than allowed) is preserved — it now
  applies after 3 decimals instead of after 2.

No new database object, no new `manualcharge.Reader`/`Writer` method, no new
`telemetry.Reader` method, no new gateway route. The existing `GET /charges` page,
its `/ui/charges*` fragment routes, and the `manualcharge.Entry` shape carried by the
existing `Writer.Create` / `Writer.Update` / `Writer.Delete` interface are all
unchanged — only the gateway's form markup, validation, and the way it sources
`TeslaID`/`VIN`/`Currency` change.

## Capabilities

### Modified Capabilities

- **`gateway`** — the "Manual charge log" UI capability (the create form, the row
  delete behavior, and the form→handler→Writer path on the charges page) changes per
  the six items above: delete reliably removes a row, the vehicle picker is removed
  from the form (sourced from the menu selection instead), currency is fixed to COP,
  battery % is required with a telemetry-sourced start suggestion, the date fields
  move up with today's default, and energy allows 3 decimals. No `manualcharge`
  capability behavior changes — the gateway just hands a fully populated,
  gateway-validated `manualcharge.Entry` to the same `Writer` port.

### Consumed Capabilities (no change to their specs)

- **`manualcharge`** — `Writer.Create` / `Writer.Update` / `Writer.Delete` and
  `Reader.ListEntriesByVehicle` / `Reader.ListEntriesByAccount` keep their exact
  current signatures. The `Entry` struct already carries `Currency string`,
  nullable `StartBatteryPct`/`EndBatteryPct`, nullable `StartedAt`/`EndedAt`, and
  `EnergyAddedKwh float64` — so making the gateway send these as required/COP/3-
  decimal-cleaned values needs no `manualcharge` change. No delta to the
  `manual-charge-log` spec.
- **`telemetry`** — `Reader.LatestSnapshotsByAccount(ctx, accountID)` is the existing
  port method the dashboard already calls once per render. The gateway reuses it to
  read the active vehicle's latest `BatteryLevelPct` for the start-battery
  suggestion label. No new `telemetry.Reader` method, no telemetry DB change.

## Impact

- **Module:** `internal/gateway` only.
- **Files (expected):** `internal/gateway/handlers/charges.go` (delete-flow root-cause
  fix in `ChargeRowDelete`; `parseChargeForm` source `TeslaID`/`VIN` from the resolved
  selected vehicle, hardcode `Currency = "COP"`, make `start_battery_pct` /
  `end_battery_pct` required, accept 3-decimal energy; `buildChargesPage` /
  `ChargeCreateForm` builder pull the latest telemetry snapshot for the suggestion
  label), `internal/gateway/templates/fragments/charge_create_form.templ` (remove
  vehicle field, disable currency field, move date fields up + today default, mark
  battery fields required + render the start-battery suggestion label, change energy
  `step` to `0.001`), `internal/gateway/templates/fragments/charges_vm.go`
  (`ChargesPageData` gains a `StartBatteryPctSuggestion` presenter string — empty when
  no telemetry snapshot exists, per D2's graceful-empty contract), and the existing
  handler/template test files. A `make templ` regen is required because `.templ`
  files change; `sqlc` is NOT required (no query change).
- **APIs:** `/ui/charges/create` and `/ui/charges/row/:id` (`DELETE`) — no route
  additions; the create POST stops expecting a `vehicle` form field and starts
  expecting `start_battery_pct` / `end_battery_pct` to be required. Non-breaking at
  the HTTP level for human users (the form is the only client).
- **Read paths:** none added or changed in shape. The single new read
  (`telemetry.Reader.LatestSnapshotsByAccount`) is the **same call** the dashboard
  already issues once per render; on the charges page it is one extra call per
  `buildChargesPage` to populate a single suggestion label. **This change does NOT
  touch the database or a hot read path** — it is a UI form/validation change, with
  one reuse of an existing batched read port. (Relevant design fact called out per
  the project's Performance-Profile: the read-heavy optimization mandate is not
  engaged by a form-validation change.)
- **Database:** **none.** No table, column, index, constraint, view, or migration is
  added, changed, or removed. The `database` design gate does not trigger. `Currency`
  stays a column the gateway simply always sends as `"COP"`; vehicle `TeslaID`/`VIN`
  are sourced from the active menu vehicle instead of a form input. If implementation
  discovers a genuine need for a DB object, design.md says to stop and flag it — not
  design one silently.
- **Breaking:** No. The `manualcharge` interface is unchanged; only the form the
  browser posts changes (no `vehicle` field, battery fields required, currency
  fixed). No external JSON consumer is affected — the `/ui/charges*` routes are
  htmx-only; no `/api/v1` route is involved.
- **Dependencies:** none added.

## Open Questions

None. The two design decisions with genuine user-facing alternatives (where to place
the date fields, and what to use as the battery-suggestion source) were resolved in
the leader's Step-2 interview with the user and are recorded as **D1** and **D2**
above — they are settled, not re-opened here. Implementation questions (e.g. whether
`started_at`/`ended_at` stay `datetime-local` or simplify to `date`) are scoped by
design.md, not gated as open questions in the proposal.