# Tasks — RM3-gateway-add-manual-charge-ui

> Keep this file updated live as tasks are applied (check the box and update
> `progress.json` for the matching task). Any agent picking up this change MUST
> read `design.md` (especially D1–D10) before implementing any task.
>
> Generated files (`*_templ.go`) are produced by `templ generate` — NEVER
> hand-edit them. After any `.templ` edit, the user must run `templ generate`.
> The implementation agent should note which `.templ` files were edited and
> prompt the user to regenerate.
>
> Notation: `depends_on` lists task IDs that must be complete before this task
> starts. Tasks with no `depends_on` (or only `[done-tier1]`) may begin
> immediately. Tasks sharing no files may run in parallel.

---

## Sub-task A — Wire ports into Deps / Handler / cmd/web

**ID:** A  
**depends_on:** (none — first task; no code touches the same files as B/C/D/E/F/G/H/I)

- [x] A1. Add `ManualChargeWriter manualcharge.Writer` and
  `ManualChargeReader manualcharge.Reader` to `internal/gateway/gateway.go:Deps`.
- [x] A2. Pass both new fields from `gateway.Deps` through to `handlers.Deps` in
  `gateway.NewEngine` (the `h := handlers.New(handlers.Deps{...})` call).
- [x] A3. Add `ManualChargeWriter manualcharge.Writer` and
  `ManualChargeReader manualcharge.Reader` to `internal/gateway/handlers/handlers.go:Deps`.
- [x] A4. Add unexported fields `manualChargeWriter manualcharge.Writer` and
  `manualChargeReader manualcharge.Reader` to `handlers.Handler`.
- [x] A5. Wire them in `handlers.New(d Deps)`.
- [x] A6. In `cmd/web/main.go` (or equivalent), construct
  `manualcharge.NewWriter(pool)` and `manualcharge.NewReader(pool)` and inject them into
  `gateway.Deps`. No new config keys; reuse the existing `*pgxpool.Pool`.
- [x] A7. Run `go build ./...` to confirm the wiring compiles (no test run required here).

**Acceptance:** `go build ./...` passes; `gateway.Deps`, `handlers.Deps`, and
`handlers.Handler` all carry the two new port fields; `cmd/web` injects them.

---

## Sub-task B — View models and helper structs

**ID:** B  
**depends_on:** A

- [x] B1. Create `internal/gateway/handlers/charges_vm.go` (or add to handlers.go) with:
  - `ChargeEntryVM` struct (all display-ready string fields + raw edit-form fields, as
    specified in design.md D6).
  - `VehicleOptionVM` struct (TeslaID, VIN, DisplayName, Value — combined form value
    `"{TeslaID}:{VIN}"`).
  - `ChargesPageData` struct (Entries, VehicleOptions, ActiveTeslaID, CSRFToken,
    EmptyState, Error fields).
- [x] B2. Implement `mapToChargeEntryVM(e manualcharge.Entry, vehicles []account.Vehicle) ChargeEntryVM`
  in the same file. This helper calls `e.CostPerKWh()`, `e.BatteryDelta()`,
  `e.SessionDuration()` and formats them into display strings. No Gin, no HTTP. Unit-testable.
- [x] B3. Implement `dataForCharges(ctx, h *Handler, uid uuid.UUID, teslaID int64, limit int) (ChargesPageData, error)`
  as a package-level function (gin-free so it is mockable in tests). It:
  - Calls `h.acct.RegisteredVehicles(ctx, uid)` → builds `[]VehicleOptionVM`.
  - Calls `h.manualChargeReader.ListEntriesByVehicle` or `ListEntriesByAccount` depending
    on whether `teslaID != 0`.
  - Maps the results via `mapToChargeEntryVM`.
  - Generates or passes through the CSRF token (token is generated in the page handler,
    passed here as a parameter when called from the list fragment handler).
- [x] B4. Implement `parseVehicleValue(v string) (teslaID int64, vin string, err error)` to
  parse the combined `"{teslaID}:{vin}"` form field (split on first `:`).

**Acceptance:** The view model types exist; `mapToChargeEntryVM` maps all fields including
derived values and nil-safe optionals; `dataForCharges` can be called in a unit test with
fake port implementations.

---

## Sub-task C — Templ templates: page, fragments, layouts

**ID:** C  
**depends_on:** B  
(Note: touches only `.templ` files in `templates/`; does not overlap with D/E/F/G which
touch `handlers.go`.)

- [x] C1. Create `internal/gateway/templates/pages/charges.templ` for the full Charge log page:
  - Uses `@layouts.BaseAuth("Charge log — Magus")` (auth shell with nav).
  - Contains `@templ.Fragment("charges-create-form") { @fragments.ChargeCreateForm(d, nil) }`.
  - Contains `@templ.Fragment("charges-list") { @fragments.ChargesList(d) }`.
  - Includes an htmx `hx-get="/ui/charges/list"` refresh button targeting `#charges-list`.
- [x] C2. Create `internal/gateway/templates/fragments/charges_list.templ` with:
  - `ChargesList(d ChargesPageData)` component: the `<div id="charges-list">` table wrapper.
  - Renders `fragments.ChargeRow(vm, d.CSRFToken)` for each entry in `d.Entries`.
  - Renders empty-state message when `d.EmptyState == true`.
  - Renders a user-facing error message when `d.Error != ""`.
- [x] C3. Create `internal/gateway/templates/fragments/charge_row.templ` with:
  - `ChargeRow(vm ChargeEntryVM, csrfToken string)` — the static `<tr id="charge-row-{vm.ID}">`.
  - Shows: date, energy, price+currency, cost/kWh, vehicle, optional battery delta, duration.
  - "Edit" button: `hx-get="/ui/charges/row/{vm.ID}/edit"` targeting `#charge-row-{vm.ID}` with
    `hx-swap="outerHTML"`.
  - "Delete" button: `hx-delete="/ui/charges/row/{vm.ID}"` targeting `#charge-row-{vm.ID}`
    with `hx-swap="outerHTML"`, `hx-confirm="Delete this entry?"`, and hidden csrf input via
    `hx-include`.
- [x] C4. Create `internal/gateway/templates/fragments/charge_row_edit.templ` with:
  - `ChargeRowEdit(vm ChargeEntryVM, csrfToken string, validationErrors map[string]string)` —
    the inline edit `<tr id="charge-row-{vm.ID}">` with pre-populated inputs.
  - Required fields always visible; optional fields under `<details>` expander.
  - "Save" button: `hx-put="/ui/charges/row/{vm.ID}"` targeting `#charge-row-{vm.ID}` with
    `hx-swap="outerHTML"`.
  - "Cancel" button: `hx-get="/ui/charges/row/{vm.ID}"` targeting `#charge-row-{vm.ID}` with
    `hx-swap="outerHTML"`.
  - Hidden `<input name="csrf_token" value="{csrfToken}">`.
- [x] C5. Create `internal/gateway/templates/fragments/charge_create_form.templ` with:
  - `ChargeCreateForm(d ChargesPageData, validationErrors map[string]string)` component
    inside `<div id="charges-create-form">`.
  - Required fields: `charged_on`, `energy_added_kwh`, `price`, `currency` (all visible).
  - Vehicle picker `<select name="vehicle">` populated from `d.VehicleOptions`.
  - `<details>` expander "More details" with optional fields.
  - Hidden `<input name="csrf_token" value="{d.CSRFToken}">`.
  - `ChargeCreateSuccessOOB` component uses htmx hx-swap-oob to update both create form
    (reset) and charges-list in one response.
- [x] C6. Added `layouts.BaseAuth` to `base.templ` with auth-conditional nav links
  ("Dashboard", "Charge log"). `charges.templ` uses `BaseAuth`.
- [x] C7. `templ generate` run successfully (6 updates). `*_templ.go` files generated.

**Acceptance:** All `.templ` files compile after `templ generate`; `go build ./...` passes;
templates accept only `ChargeEntryVM` / `ChargesPageData` — no `manualcharge.*` types, no
`pgtype.*` types in template files.

---

## Sub-task D — Read handlers and routes (GET /charges, GET /ui/charges/list, GET /ui/charges/row/{id})

**ID:** D  
**depends_on:** B, C

- [x] D1. Implement `(h *Handler) ChargePage(c *gin.Context)` in
  `internal/gateway/handlers/charges.go`:
  - Auth guard: `currentUID(c)` → redirect to `/login` if no session.
  - Generate CSRF token (16 bytes from `crypto/rand`, hex-encoded), store in session under
    `"csrf_manualcharge"`.
  - Call `buildChargesPage(ctx, uid, csrfToken, 0)` (account-wide list, default limit).
  - `render(c, http.StatusOK, pages.ChargePage(d))`.
- [x] D2. Implement `(h *Handler) ChargesListFragment(c *gin.Context)`:
  - Auth guard.
  - Read CSRF token from session (already generated by ChargePage).
  - Call `buildChargesPage`.
  - `renderFragment(c, http.StatusOK, pages.ChargePage(d), "charges-list")`.
- [x] D3. Implement `(h *Handler) ChargeRowStatic(c *gin.Context)` (for cancel-edit):
  - Auth guard.
  - Parse `id` from path param → `uuid.Parse`.
  - Fetch via `fetchEntryVM` (calls `ListEntriesByAccount` and filters in memory per D6).
  - Render `fragments.ChargeRow(vm, csrfToken)` directly.
- [x] D4. Routes registered in `internal/gateway/gateway.go`:
  - `r.GET("/charges", h.ChargePage)`
  - `r.GET("/ui/charges/list", h.ChargesListFragment)`
  - `r.GET("/ui/charges/row/:id", h.ChargeRowStatic)`

**Acceptance:** `GET /charges` renders the full page with auth guard; `GET /ui/charges/list`
returns only the list fragment; `GET /ui/charges/row/:id` returns a single static row.
All three redirect unauthenticated visitors to `/login`.

---

## Sub-task E — Create handler and route

**ID:** E  
**depends_on:** B, C, D

- [x] E1. Implement `(h *Handler) ChargeCreate(c *gin.Context)`:
  - Auth guard.
  - CSRF validation: read `csrf_token` from form body; compare to session
    `"csrf_manualcharge"` via `subtle.ConstantTimeCompare`; 403 on mismatch.
  - Parse and validate required form fields (charged_on, energy_added_kwh, price, currency);
    parse optional fields when non-empty.
  - Parse and validate the vehicle value (`parseVehicleValue`); confirm the resolved
    `(teslaID, vin)` is in `h.acct.RegisteredVehicles(ctx, uid)`; 403 on mismatch.
  - Build `manualcharge.Entry{AccountID: uid, TeslaID: teslaID, VIN: vin, ...}`.
  - Call `h.manualChargeWriter.Create(ctx, entry)`.
  - On success: renders `ChargeCreateSuccessOOB` — primary swap resets form, OOB swap
    refreshes `#charges-list` using htmx `hx-swap-oob="outerHTML:#charges-list"`.
  - On validation error: re-render the create form at HTTP 422 with errors map.
  - On write error: re-render with a top-level error message.
- [x] E2. Route registered: `r.POST("/ui/charges/create", h.ChargeCreate)`.

**Acceptance:** Valid POST creates an entry and the list updates. Invalid POST re-renders the
form with errors at 422. CSRF mismatch returns 403 with no write. Vehicle not owned by user
returns 403 with no write. All without a full page reload.

---

## Sub-task F — Inline-edit handlers and routes

**ID:** F  
**depends_on:** B, C, D

- [x] F1. Implement `(h *Handler) ChargeRowEditFragment(c *gin.Context)`:
  - Auth guard.
  - Parse `id` path param.
  - Fetch via `fetchEntryVM` (list+filter per D6).
  - Read CSRF token from session.
  - Render `fragments.ChargeRowEdit(vm, csrfToken, nil)` (no errors on first load).
- [x] F2. Implement `(h *Handler) ChargeRowUpdate(c *gin.Context)`:
  - Auth guard.
  - CSRF validation (same as E1 pattern).
  - Parse `id` path param → `uuid.Parse`.
  - Parse and validate form fields (same validation as create).
  - Parse and validate vehicle; confirm tenant ownership.
  - Build updated `manualcharge.Entry{ID: id, AccountID: uid, ...}`.
  - Call `h.manualChargeWriter.Update(ctx, entry)`.
  - On success: render static `<tr>` via `ChargeRow`.
  - On validation error: re-render edit form at HTTP 422 with errors.
  - On write error: re-render with error message.
- [x] F3. Routes registered:
  - `r.GET("/ui/charges/row/:id/edit", h.ChargeRowEditFragment)`
  - `r.PUT("/ui/charges/row/:id", h.ChargeRowUpdate)`

**Acceptance:** `GET /ui/charges/row/{id}/edit` swaps the row to an edit form pre-populated
with the entry's current values. `PUT /ui/charges/row/{id}` updates the entry and swaps back
to the static row on success, or shows inline errors at 422. CSRF mismatch returns 403.

---

## Sub-task G — Delete handler and route

**ID:** G  
**depends_on:** B, C, D

- [x] G1. Implement `(h *Handler) ChargeRowDelete(c *gin.Context)`:
  - Auth guard.
  - CSRF validation: read `csrf_token` from form body or X-CSRF-Token header.
  - Parse `id` path param → `uuid.Parse`.
  - Call `h.manualChargeWriter.Delete(ctx, uid, id)` (scoped WHERE in Writer).
  - On success: responds with `ChargeRowEmpty(id)` — empty `<tr>` that outerHTML removes row.
  - On error: renders `ChargeRowError` with user-facing message.
  - CSRF mismatch: 403.
- [x] G2. Route registered: `r.DELETE("/ui/charges/row/:id", h.ChargeRowDelete)`.

**Acceptance:** `DELETE /ui/charges/row/{id}` removes the entry scoped to the session
account and the row disappears from the table. CSRF mismatch returns 403 with no delete.
Attempting to delete another user's entry silently deletes nothing (scoped WHERE in Writer).

---

## Sub-task H — httptest handler/gateway tests

**ID:** H  
**depends_on:** A, B, C, D, E, F, G

- [x] H1. `fakeChargeWriter` and `fakeChargeReader` added to `handlers/charges_test.go`.
  No real database required — configurable data and errors.
- [x] H2. Test `ChargePage` (GET /charges):
  - Auth guard: no session → 302 to `/login`.
  - With session: 200, page HTML contains `charges-list` region.
- [x] H3. Test `ChargesListFragment` (GET /ui/charges/list):
  - Auth guard: no session → 302.
  - With entries: 200, charges-list div present.
  - Reader error: 200 with graceful degradation, no 500.
  - Empty list: 200, empty-state message.
- [x] H4. Test `ChargeCreate` (POST /ui/charges/create):
  - CSRF mismatch → 403.
  - Unowned vehicle → 403.
  - Missing required field → 422 with error message.
  - Valid input + fake Writer.Create → 200, entry persisted.
- [x] H5. Test `ChargeRowEditFragment` (GET /ui/charges/row/{id}/edit):
  - Auth guard: no session → 302.
  - Returns edit form `<tr>` with entry id.
- [x] H6. Test `ChargeRowUpdate` (PUT /ui/charges/row/{id}):
  - CSRF mismatch → 403.
  - Validation error → 422.
  - Valid → 200, static row with id rendered.
- [x] H7. Test `ChargeRowDelete` (DELETE /ui/charges/row/{id}):
  - CSRF mismatch → 403 (X-CSRF-Token header).
  - Valid → 200, empty `<tr>` with id in response.
- [x] H8. `go test ./internal/gateway/...` passes. All handler and gateway tests green.

**Acceptance:** `go test ./internal/gateway/...` passes with the new tests; auth guards, CSRF
checks, tenant ownership, error states, and fragment rendering are each covered by at least
one test case per handler.

---

## Sub-task I — Amend AGENTS.md

**ID:** I  
**depends_on:** D4 boundary-rule amendment is implemented and tested (depends_on: H)

- [x] I1. Edited `internal/gateway/AGENTS.md`, "Read-only at request time" section:
  - Added sub-section "Exception: user-initiated writes (D4 amendment)" documenting all
    four constraints (auth guard, tenant ownership, CSRF token, only manualcharge.Writer).
  - Updated `## Public interface` section with `ManualChargeWriter` and `ManualChargeReader`
    docs (same pattern as TelemetryReader).
  - Clarified Reader-only remains the default for all non-form handlers.
- [x] I2. Existing "Read-only at request time" principle preserved — amendment is additive
  and narrowly scoped. Original bullet points kept intact.

**Acceptance:** `internal/gateway/AGENTS.md` accurately reflects the post-amendment boundary
rules; the amendment is constrained and does not imply general write access to the gateway.
