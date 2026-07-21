## Context

Tier 4 (final) of the RM4-charge-form-required-location-and-vehicle-autoselect roadmap.
This change wires the UI consequences of tiers 1–3 into `internal/gateway/`: required
`location_kind` on forms, vehicle auto-select on the create form, and `access_type`
propagation at seed time.

**This change adds NO database object.** Tiers 1–3 landed the column (`vehicles.access_type`),
the domain fields (`Vehicle.AccessType`, `SeedVehicle.AccessType`), and the DB constraint
(`location_kind NOT NULL`). This tier only modifies handler logic and Templ templates.
No migration, no sqlc changes, no new tables, columns, or indexes.

The database design gate is **not triggered** for this tier. This design.md is provided
per-change for implementation clarity, not as a DB-gate artifact.

All binding decisions are from the RM4 grill-me session (RD1–RD4); they are not re-
litigated here.

---

## D1 — Required Location: `parseChargeForm` validation change

### Current behavior (to be replaced)

`handlers/charges.go` ~line 535:

```go
// optional
if v := c.PostForm("location_kind"); v == "HOME" || v == "WORK" || v == "OTHER" {
    entry.LocationKind = &v
}
```

Missing or invalid values are silently ignored — `entry.LocationKind` stays nil and the
call proceeds. With the tier-3 migration `location_kind` is now `NOT NULL`, so the service
returns an error, but the gateway gives no field-level feedback.

### New behavior

`location_kind` validation joins the **required-fields block** (after `currency`, before
optional fields begin). The handler validates:

1. Read `v := c.PostForm("location_kind")`.
2. If `v` is not in `{"HOME","WORK","OTHER"}` (including empty string): set
   `errs["location_kind"] = "Location is required."` and do NOT set `entry.LocationKind`.
3. If valid: set `entry.LocationKind = &v` (pointer to a local copy — not the range var).

The `if len(errs) > 0 { return …, errs, false }` early exit already exists; adding
`location_kind` to the required-fields block means a missing location returns the form
with the field-level error, the same as a missing `charged_on` or `energy_added_kwh`.

This change applies to BOTH create and edit paths — `parseChargeForm` is shared.

---

## D2 — Required Location: Templ `<select>` changes

### Create form (`charge_create_form.templ`)

**Current:** `location_kind` is in the `<details>` expander with a blank `-` first option:

```templ
<select name="location_kind">
    <option value="">-</option>
    <option value="HOME">Home</option>
    ...
</select>
```

**New:**

1. Move the `location_kind` field **out of** the `<details>` expander and into the always-
   visible required fields section (alongside `charged_on`, `energy_added_kwh`, `price`,
   `currency`). This matches the "required fields always visible" rule from design.md D3
   of the RM3 change.
2. Remove the `<option value="">-</option>` blank option.
3. Add `required` attribute to the `<select>`.
4. Add a validation error display: `if validationErrors["location_kind"] != "" { ... }`.

The `<details>` expander retains the remaining optional fields (`started_at`, `ended_at`,
`start_battery_pct`, `end_battery_pct`, `charging_type`, `location_label`, `notes`).

### Edit row (`charge_row_edit.templ`)

**Current:** same structure — `location_kind` is in `<details>` with a blank option.

**New:**

1. Keep `location_kind` **in the `<details>` expander** for the edit form (consistent with
   the existing edit-row layout where required fields still live inside `<details>`). The
   edit row is already bound to an existing entry — in the current data model, pre-existing
   entries may have a non-null `location_kind` after the tier-3 migration (the defensive
   backfill set `'OTHER'` for any NULL rows). The select will always show one of the valid
   options pre-selected because `vm.LocationKind` will always be one of `HOME/WORK/OTHER`.
2. Remove the `<option value="">-</option>` blank option from the `location_kind` select.
3. Add `required` attribute to the `<select>`.
4. Ensure one option is selected via `selected?=` (already present for existing options).

**Rationale for keeping location_kind in `<details>` in the edit row:** The edit row has a
fixed, compact layout — required fields are not split into a separate visible section.
Removing the blank option and adding `required` ensures the browser enforces the constraint
on submit without changing the row layout. The pre-existing `selected?={ vm.LocationKind
== "..." }` means the `<select>` always renders with a value from the stored entry.

---

## D3 — Vehicle Auto-Select: View Model Extension

### `VehicleOptionVM` — new `Selected bool` field

```go
type VehicleOptionVM struct {
    TeslaID     int64
    VIN         string
    DisplayName string
    Value       string // "{TeslaID}:{VIN}"
    Selected    bool   // pre-computed; true for exactly one option (the auto-selected vehicle)
}
```

`Selected` is set by the handler and checked in the template via `selected?={ opt.Selected }`.
Templates remain logic-free (no AccessType comparisons or len checks in Templ).

### `ChargesPageData` — new `SingleVehicle bool` field

```go
type ChargesPageData struct {
    // ... existing fields ...
    SingleVehicle bool // true when len(VehicleOptions) == 1; drives disabled+hidden-input branch
}
```

When `SingleVehicle == true`, the template renders a `<select disabled>` plus a sibling
`<input type="hidden" name="vehicle" value="...">` carrying the auto-selected option's
`Value`. A disabled `<select>` does not POST; the hidden input carries the value instead
(RD4 decision).

### Auto-select rule (implemented in `buildVehicleOptions`)

Rename `buildVehicleOptions` to make room for auto-select logic, or add an in-place helper.
The rule (RD4):

1. **Exactly one vehicle** → select it. Set `SingleVehicle = true`. The template renders
   `<select disabled>` + `<input type="hidden" name="vehicle" value="...">`.
2. **Multiple vehicles, first OWNER** → find the first option where `Vehicle.AccessType`
   points to `"OWNER"`. Set `Selected = true` on that option. `SingleVehicle = false`.
   `<select>` is enabled; user can change.
3. **Multiple vehicles, no OWNER** → set `Selected = true` on the first option.
   `SingleVehicle = false`.

**Nil-safety:** `Vehicle.AccessType` is `*string`. The check is:

```go
v.AccessType != nil && *v.AccessType == "OWNER"
```

never dereferences a nil pointer.

**"Never unselected" invariant:** step 1 guarantees `len == 1`; steps 2–3 guarantee exactly
one `Selected = true` when `len >= 2`. A vehicle list is only passed to the template when
it is non-empty (the create form renders only when the user has vehicles registered; an empty
vehicle list shows the connect prompt instead, in the dashboard flow — the charge page
currently degrades gracefully if vehicles is empty; this change does not alter that path).

---

## D4 — Seed Mapping: `VehicleTesla.AccessType → SeedVehicle.AccessType`

`handlers/handlers.go` ~line 165–172: the existing seed build loop:

```go
seed = append(seed, account.SeedVehicle{
    TeslaID:     v.ID,
    VIN:         v.VIN,
    DisplayName: v.DisplayName,
})
```

becomes:

```go
var accessType *string
if v.AccessType != "" {
    at := v.AccessType // local copy — avoids loop-variable alias
    accessType = &at
}
seed = append(seed, account.SeedVehicle{
    TeslaID:     v.ID,
    VIN:         v.VIN,
    DisplayName: v.DisplayName,
    AccessType:  accessType,
})
```

**Nil convention:** `VehicleTesla.AccessType` is a `string` (not a pointer). An empty string
(`""`) — which the Fleet API returns when the field is absent from the JSON — maps to `nil`
on the `*string` domain field (the boundary-nil convention used throughout this project,
documented in the account module). A non-empty value (`"OWNER"` or `"DRIVER"`) maps to a
`*string` pointing to a local copy.

**No new imports:** `internal/tesla` is already imported in `handlers.go` (used for
`tesla.Credentials` and `tesla.ListVehicles`). `account.SeedVehicle.AccessType *string`
is already in the `account` package (tier 2).

---

## D5 — File Map (authoritative — AGENTS.md recipe)

| File | Change | Sub-task |
|------|--------|----------|
| `internal/gateway/handlers/charges.go` | Add `location_kind` required validation in `parseChargeForm`; add `buildVehicleOptionsWithAutoSelect` (or extend `buildVehicleOptions`) computing `Selected` and returning `SingleVehicle` | A, B |
| `internal/gateway/templates/fragments/charges_vm.go` | Add `Selected bool` to `VehicleOptionVM`; add `SingleVehicle bool` to `ChargesPageData` | B |
| `internal/gateway/templates/fragments/charge_create_form.templ` | Move `location_kind` to required section; remove blank option; add `required`; add error display; add vehicle auto-select markup (disabled+hidden vs normal) | A, B |
| `internal/gateway/templates/fragments/charge_row_edit.templ` | Remove blank `location_kind` option; add `required` to `<select>` | A |
| `internal/gateway/handlers/handlers.go` | Map `v.AccessType` → `SeedVehicle.AccessType` in seed loop | C |
| `internal/gateway/handlers/charges_test.go` | New test cases for required location, auto-select, and seed mapping | D |
| `internal/gateway/handlers/handlers_test.go` | New test case for seed AccessType mapping | D |
| `*_templ.go` (generated) | Re-run `make templ` after any `.templ` edit | A, B |

---

## D6 — Performance Profile (read-heavy)

The only hot path affected is the charge create page render, which calls
`account.RegisteredVehicles` once per load. The auto-select computation (a linear scan of
the returned vehicle list, typically 1–5 elements) is O(n) and adds no additional database
queries. No new indexes, summary tables, or caching are needed. Read performance is
unchanged.

The seed call is a one-time, off-critical-path operation (first Tesla connect only).
Adding one field assignment per vehicle is negligible.
