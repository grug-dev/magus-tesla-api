## Why

Tiers 1–3 of RM4 shipped the plumbing: `VehicleTesla.AccessType` is decoded by the tesla
adapter (tier 1), the `vehicles.access_type` column and `Vehicle.AccessType *string` domain
field exist in the account module (tier 2), and `manualcharge.Create`/`Update` now reject
`nil`/empty `location_kind` at the service layer (tier 3). The UI has not yet been updated:

- The `location_kind` picker in both the create form (`charge_create_form.templ`) and the
  inline-edit row (`charge_row_edit.templ`) still shows a blank `-` default option and carries
  no `required` attribute. A user can submit the form without choosing a location, which passes
  the browser silently and is now rejected at the service layer — producing a confusing error
  with no field-level guidance.
- `parseChargeForm` in `handlers/charges.go` silently ignores any `location_kind` value that
  is not in `{HOME,WORK,OTHER}` (including an empty string); it never sets a validation error
  for a missing location, so the required-field constraint added in tier 3 can only be surfaced
  as a generic service error.
- The vehicle `<select>` in the create form always starts with a blank option (`Select a
  vehicle`). A single-vehicle user is forced to choose the only vehicle they own on every form
  submission. Multi-vehicle users get no preference ordering — the vehicle most likely to be
  correct (the one they own) is not pre-selected.
- The one-time Tesla vehicle seed call in `handlers/handlers.go` builds `account.SeedVehicle`
  structs without mapping `VehicleTesla.AccessType` into `SeedVehicle.AccessType`, so even
  though the column and domain field exist, every seeded vehicle has `access_type = NULL`.

This tier closes all four gaps within the gateway module.

The raw user request (2026-07-19, grill-me session):

> "I want some improvements on the manualcharge module … make `location_kind` a required
> field on the manual charge form, front-end and back-end … auto-select the vehicle in the
> create form — if the tenant has one vehicle, auto-select it and disable the control; if
> more than one, auto-select the first vehicle whose Tesla `access_type` is `OWNER`."

All binding design decisions were captured in the RM4 roadmap grill-me session (RD1–RD4).
No new interview is needed for this tier.

## What Changes

**Module: `internal/gateway/`** — the only module that produces HTML.

### (a) `parseChargeForm` — required location validation

`internal/gateway/handlers/charges.go` (~line 535): the existing optional parse block

```go
if v := c.PostForm("location_kind"); v == "HOME" || v == "WORK" || v == "OTHER" {
    entry.LocationKind = &v
}
```

becomes a **required-field validation** that populates `errs["location_kind"]` when the
submitted value is missing or not in `{HOME,WORK,OTHER}`, and only sets `entry.LocationKind`
on a valid value. The check joins the existing required-field block (before the optional-fields
section) — the form cannot successfully submit without a recognized location kind.

### (b) Templ `<select name="location_kind">` — remove blank option + add `required`

Both templates move `location_kind` out of the `<details>` expander and into the required
fields section (or, if keeping the expander, add `required` and remove the `<option value="">-
</option>` blank option) so the browser's native form validation fires before an htmx POST
is even sent:

- `internal/gateway/templates/fragments/charge_create_form.templ` (~line 108): the
  `location_kind` `<select>` block loses `<option value="">-</option>` and gains
  `required`.
- `internal/gateway/templates/fragments/charge_row_edit.templ` (~line 122): same change.

### (c) Vehicle auto-select in the create form (RD4)

`ChargesPageData.VehicleOptions` now carries a new computed field to support pre-selection,
**or** the `ChargesPageData` struct grows a `DefaultVehicleValue string` field that the
handler pre-computes. The auto-select rule (RD4):

1. If the account has exactly **one** registered vehicle → pre-select it + mark `disabled` on
   the `<select>` + add a sibling `<input type="hidden" name="vehicle">` carrying the value
   (disabled selects post nothing).
2. Else find the first vehicle whose `Vehicle.AccessType` is `"OWNER"` (nil-safe pointer
   deref) → pre-select it in the `<select>` (not disabled — user can change).
3. Else pre-select the **first vehicle in the list** (guaranteed first element, never empty
   because the create form only renders when `VehicleOptions` is non-empty).

Never leave the vehicle picker unselected. The edit row is unaffected (it already binds to
the entry's vehicle via a hidden `<input name="vehicle">`).

**Implementation changes:**
- `charges_vm.go`: `VehicleOptionVM` gains a `Selected bool` field (pre-computed by the
  handler; template checks it). `ChargesPageData` gains a `SingleVehicle bool` field (true
  when `len(VehicleOptions) == 1`) so the template knows whether to disable and add a hidden
  input.
- `handlers/charges.go`: `buildVehicleOptions` or a new `buildVehicleOptionsWithAutoSelect`
  helper computes `Selected` per option and detects single-vehicle via `len`.
- `charge_create_form.templ`: if `d.SingleVehicle`, render the `<select disabled>` plus a
  `<input type="hidden" name="vehicle" value="...">` carrying the selected value; otherwise
  render the `<select>` normally with `selected?={ opt.Selected }` on each option.

### (d) Seed mapping — `VehicleTesla.AccessType → SeedVehicle.AccessType`

`internal/gateway/handlers/handlers.go` (~line 165–172): the seed-build loop

```go
seed = append(seed, account.SeedVehicle{
    TeslaID:     v.ID,
    VIN:         v.VIN,
    DisplayName: v.DisplayName,
})
```

gains an `AccessType` field mapping:

- `v.AccessType == ""` → `nil` (boundary-nil convention: empty string from the adapter is
  treated as no value at the domain boundary).
- `v.AccessType != ""` → `&v.AccessType` (copy to avoid loop-variable alias; or take address
  of a local copy).

This is a pure Go change: no new imports, no Templ edits, no DB.

### Scope note — no other modules touched

- **No `internal/manualcharge` change** — tier 3 already validates `location_kind` required.
- **No `internal/account` change** — `Vehicle.AccessType`, `SeedVehicle.AccessType` already
  exist from tier 2.
- **No `internal/tesla` change** — `VehicleTesla.AccessType` already exists from tier 1.
- **No DB migration** — this tier introduces zero new/changed database objects.

### Companion file

`cmd/web` is not changed in this tier (no new port fields added to `gateway.Deps`).

## Breaking

No. Existing routes, handlers, and Templ templates are modified in-place (required-field
stricter validation is backward-compatible from the perspective of callers submitting well-
formed data). The `VehicleOptionVM.Selected` and `ChargesPageData.SingleVehicle` additions
are additive zero-value fields. No interface changes.

## Modules affected

- `internal/gateway/` — primary target (all four change areas above).
- No other `internal/` module.

## No Database Changes

This tier introduces **no** new database object — no table, no column, no index, no
constraint, no migration, no sqlc change. The `access_type` column (tier 2) and the
`location_kind NOT NULL` migration (tier 3) are already in place. The database design gate
is **not triggered** for this tier.

## Read Paths Affected

- **Charge create page render / vehicle list** — `account.Service.RegisteredVehicles(ctx,
  uid)` is called once per `GET /charges` render (and on the OOB success swap) to populate
  the vehicle picker. This is the affected read path: it now drives the auto-select logic
  in the handler. No new DB query — the same existing indexed call. Read performance is
  unchanged.

- **One-time Tesla connect seed** — `buildVehicleData` in `handlers/handlers.go` calls
  `tesla.ListVehicles` + `account.SeedVehicles` on first connect only. The seed-mapping
  change adds one field assignment per vehicle — O(n vehicles), always n≤10 for any Tesla
  account. Not a hot path.

No new indexes or summary tables needed.

## Capabilities

### Modified Capabilities

- `gateway` — the existing capability gains required-field enforcement for `location_kind`
  on the create and edit forms, vehicle auto-select on the create form, and correct
  `access_type` propagation during the first-connect seed. No new routes, no new Deps fields,
  no new module imports.

### Consumed Capabilities (no change to their specs)

- `account` — consumed via `RegisteredVehicles` (read) and `SeedVehicles` (write, first-
  connect only). Both interfaces already carry `AccessType *string`. No spec change.
- `manual-charge-log` — consumed via `Writer`/`Reader` ports. Required-location enforcement
  is already in the tier-3 spec. No spec change to the manualcharge module.
