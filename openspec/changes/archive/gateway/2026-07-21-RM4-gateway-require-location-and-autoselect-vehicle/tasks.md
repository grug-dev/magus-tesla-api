# Tasks — RM4-gateway-require-location-and-autoselect-vehicle

> Keep this file updated live as tasks are applied (check the box and update
> `progress.json` for the matching task). Any agent picking up this change MUST
> read `design.md` (D1–D6) and `internal/gateway/AGENTS.md` before implementing
> any task.
>
> Generated files (`*_templ.go`) are produced by `make templ` (pinned
> `go tool templ generate ./...`). After any `.templ` edit, the agent MUST run
> `make templ`. NEVER hand-edit `*_templ.go` files.
>
> Notation: `depends_on` lists task IDs that must be complete before this task
> starts. Tasks with no or non-overlapping `depends_on` may run in parallel when
> their file sets are disjoint.

---

## Sub-task A — Required location validation in `parseChargeForm` + Templ `<select>` changes

**ID:** A
**depends_on:** (none — touches disjoint files from B and C)
**Parallel-safe with:** B (disjoint: A touches `charges.go` + `charge_create_form.templ` +
`charge_row_edit.templ`; B touches `charges_vm.go` + `charges.go` auto-select section +
`charge_create_form.templ` auto-select section)
**Note:** A and B BOTH touch `charges.go` (different sections) and
`charge_create_form.templ` (different sections), so they are NOT fully parallel. A must
complete before B applies its changes to those same files, OR they must be implemented
together by the same agent.

- [x] A1. In `internal/gateway/handlers/charges.go`, `parseChargeForm`: add a
  required-field validation block for `location_kind`.
  - Read `v := c.PostForm("location_kind")`.
  - If `v` is not in `{"HOME","WORK","OTHER"}`, set
    `errs["location_kind"] = "Location is required."` — do NOT set `entry.LocationKind`.
  - If valid, set `entry.LocationKind = &copy` where `copy := v` (local copy to avoid any
    loop-variable alias; though no loop here, keep consistent with project convention).
  - This block joins the required-fields section (after `currency`, before the optional
    fields), so it participates in the `if len(errs) > 0 { return …, errs, false }` early
    exit already in place.
  - The optional-fields section's existing `if v := c.PostForm("location_kind"); ...`
    block is REMOVED (the required block above replaces it entirely).
- [x] A2. In `internal/gateway/templates/fragments/charge_create_form.templ`:
  - Move the `location_kind` `<select>` block from inside the `<details>` expander to
    the always-visible required fields section (after `currency`, before the vehicle picker).
  - Remove `<option value="">-</option>` from the `location_kind` `<select>`.
  - Add `required` attribute to the `<select>`.
  - Add validation error display:
    ```templ
    if validationErrors["location_kind"] != "" {
        <span class="error">{ validationErrors["location_kind"] }</span>
    }
    ```
- [x] A3. In `internal/gateway/templates/fragments/charge_row_edit.templ`:
  - Remove `<option value="">-</option>` from the `location_kind` `<select>` inside
    `<details>`.
  - Add `required` attribute to that `<select>`.
  - Ensure at least one option is always selected: the existing `selected?=` pattern
    already guarantees this for existing entries (all will have HOME/WORK/OTHER after the
    tier-3 migration's defensive backfill). No change needed to the `selected?=` logic.
- [x] A4. Run `make templ` to regenerate `charge_create_form_templ.go` and
  `charge_row_edit_templ.go`. Confirm no compile errors.
- [x] A5. Run `go build ./...` to confirm the handler change compiles.

**Files touched:** `handlers/charges.go`, `templates/fragments/charge_create_form.templ`,
`templates/fragments/charge_row_edit.templ`, their `*_templ.go` regenerated files.

**Acceptance:** `parseChargeForm` returns a validation error for missing/invalid
`location_kind` on both create and edit paths; the create form's `location_kind` select has
no blank option and carries `required`; the edit form's `location_kind` select has no blank
option and carries `required`; `go build ./...` passes.

---

## Sub-task B — Vehicle auto-select view model + template changes

**ID:** B
**depends_on:** A (shares `charges.go` and `charge_create_form.templ`)
**Parallel-safe with:** C (C touches `handlers.go`; B touches `charges.go` and templates)

- [x] B1. In `internal/gateway/templates/fragments/charges_vm.go`:
  - Add `Selected bool` field to `VehicleOptionVM`:
    ```go
    Selected bool // pre-computed; true for exactly one option (the auto-selected vehicle)
    ```
  - Add `SingleVehicle bool` field to `ChargesPageData`:
    ```go
    SingleVehicle bool // true when len(VehicleOptions)==1; drives disabled+hidden-input branch
    ```
- [x] B2. In `internal/gateway/handlers/charges.go`, replace `buildVehicleOptions` with a
  version that applies the auto-select rule (RD4) and computes both `SingleVehicle` and
  `Selected`. The function signature changes to also return `singleVehicle bool`:
  - If `len(vehicles) == 0`: return empty slice, `false`.
  - If `len(vehicles) == 1`: set `Selected = true` on the sole option; return, `true`.
  - If `len(vehicles) >= 2`:
    - Find the first vehicle where `v.AccessType != nil && *v.AccessType == "OWNER"`.
    - If found, set `Selected = true` on that option.
    - Else set `Selected = true` on the first option.
    - Return, `false`.
  - Update `buildChargesPage` to use the new return value and set
    `ChargesPageData.SingleVehicle`.
- [x] B3. In `internal/gateway/templates/fragments/charge_create_form.templ`, replace the
  vehicle `<select>` block with the auto-select-aware markup:
  Note: in the `SingleVehicle=false` branch the `<select>` also gains `required`.
- [x] B4. Run `make templ` to regenerate `charge_create_form_templ.go`.
- [x] B5. Run `go build ./...` to confirm the changes compile.

**Files touched:** `templates/fragments/charges_vm.go`, `handlers/charges.go` (auto-select
logic in `buildVehicleOptions`), `templates/fragments/charge_create_form.templ`,
`charge_create_form_templ.go` (regenerated).

**Acceptance:** `VehicleOptionVM` has `Selected bool`; `ChargesPageData` has
`SingleVehicle bool`; `buildVehicleOptions` applies the RD4 auto-select rule and returns the
correct selection for 1-vehicle, multi-vehicle-with-OWNER, and multi-vehicle-no-OWNER cases;
the create form template renders `disabled` + hidden input for `SingleVehicle=true` and a
normal `<select>` with the selected option for `SingleVehicle=false`; `go build ./...` passes.

---

## Sub-task C — Seed access_type mapping in `handlers.go`

**ID:** C
**depends_on:** (none — touches only `handlers.go`, disjoint from A and B)
**Parallel-safe with:** A, B

- [x] C1. In `internal/gateway/handlers/handlers.go`, in `buildVehicleData`, locate the seed
  build loop (~line 165–172):
  ```go
  seed = append(seed, account.SeedVehicle{
      TeslaID:     v.ID,
      VIN:         v.VIN,
      DisplayName: v.DisplayName,
  })
  ```
  Replace with:
  ```go
  var accessType *string
  if v.AccessType != "" {
      at := v.AccessType // local copy — nil convention: empty string → nil *string
      accessType = &at
  }
  seed = append(seed, account.SeedVehicle{
      TeslaID:     v.ID,
      VIN:         v.VIN,
      DisplayName: v.DisplayName,
      AccessType:  accessType,
  })
  ```
- [x] C2. Run `go build ./...` to confirm no compile errors.

**Files touched:** `handlers/handlers.go`.

**Acceptance:** The seed loop correctly maps non-empty `VehicleTesla.AccessType` to a
non-nil `*string` on `SeedVehicle.AccessType`, and maps empty string to `nil`.
`go build ./...` passes.

---

## Sub-task D — Tests

**ID:** D
**depends_on:** A, B, C

- [x] D1. In `internal/gateway/handlers/charges_test.go`, add tests for
  required location validation (sub-task A):
  - `TestChargeCreate_MissingLocationKind`: POST with valid required fields but no
    `location_kind` → 422, response contains `location_kind` error, `Writer.Create` NOT
    called.
  - `TestChargeCreate_InvalidLocationKind`: POST with `location_kind=INVALID` → 422,
    `Writer.Create` NOT called.
  - `TestChargeRowUpdate_MissingLocationKind`: PUT with missing `location_kind` → 422,
    `Writer.Update` NOT called.
  - `TestChargeCreate_ValidLocationKind`: valid `location_kind=HOME` → 200, Writer.Create
    called with LocationKind=HOME.
- [x] D2. In `internal/gateway/handlers/charges_test.go`, add tests for vehicle auto-select
  (sub-task B):
  - `TestBuildVehicleOptions_SingleVehicle`: one vehicle → `SingleVehicle = true`, that
    vehicle `Selected = true`.
  - `TestBuildVehicleOptions_MultiVehicleOwnerFirst`: two vehicles, second has
    `AccessType = ptr("OWNER")` → second option `Selected = true`, `SingleVehicle = false`.
  - `TestBuildVehicleOptions_MultiVehicleNoOwner`: two vehicles, neither has OWNER →
    first option `Selected = true`, `SingleVehicle = false`.
  - `TestBuildVehicleOptions_MultiVehicleFirstOwner`: two vehicles, first has
    `AccessType = ptr("OWNER")` → first option `Selected = true`.
  - `TestBuildVehicleOptions_NilAccessType`: vehicle with `AccessType = nil` → treated as
    non-OWNER; if sole vehicle, still selected with `SingleVehicle = true`.
- [x] D3. In `internal/gateway/handlers/handlers_test.go`, add a test for seed mapping
  (sub-task C):
  - `TestSeedAccessTypeMapping_NonEmpty`: non-empty AccessType → non-nil *string.
  - `TestSeedAccessTypeMapping_Empty`: empty AccessType → nil.
  - Tested via `vehiclesFor` + `lastSeedVehicles` capture on `fakeAccount`.
- [x] D4. Run `go test ./internal/gateway/...` and confirm all tests pass.

**Files touched:** `handlers/charges_test.go`, `handlers/handlers_test.go`.

**Acceptance:** `go test ./internal/gateway/...` passes with new tests covering required
location validation (422 on missing/invalid), auto-select logic (single/multi-OWNER/multi-
no-OWNER/nil access type), and seed access_type mapping (empty→nil, non-empty→pointer).
No existing tests broken.

---

## Parallelism summary

| Sub-task | depends_on | Parallel-safe with |
|----------|-----------|-------------------|
| A | none | C only (B shares files with A) |
| B | A | C |
| C | none | A, B |
| D | A, B, C | (terminal) |

**Recommended dispatch:** C can run in parallel with A. B runs after A completes. D runs
last after A+B+C complete.

If dispatching a single serial worker: A → B → C → D (all in one pass, the same agent can
implement all four sub-tasks without handoff overhead, since the files are small and the
changes are coherent).
