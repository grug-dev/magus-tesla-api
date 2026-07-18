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

- [ ] A1. Add `ManualChargeWriter manualcharge.Writer` and
  `ManualChargeReader manualcharge.Reader` to `internal/gateway/gateway.go:Deps`.
- [ ] A2. Pass both new fields from `gateway.Deps` through to `handlers.Deps` in
  `gateway.NewEngine` (the `h := handlers.New(handlers.Deps{...})` call).
- [ ] A3. Add `ManualChargeWriter manualcharge.Writer` and
  `ManualChargeReader manualcharge.Reader` to `internal/gateway/handlers/handlers.go:Deps`.
- [ ] A4. Add unexported fields `manualChargeWriter manualcharge.Writer` and
  `manualChargeReader manualcharge.Reader` to `handlers.Handler`.
- [ ] A5. Wire them in `handlers.New(d Deps)`.
- [ ] A6. In `cmd/web/main.go` (or equivalent), construct
  `manualcharge.NewWriter(pool)` and `manualcharge.NewReader(pool)` and inject them into
  `gateway.Deps`. No new config keys; reuse the existing `*pgxpool.Pool`.
- [ ] A7. Run `go build ./...` to confirm the wiring compiles (no test run required here).

**Acceptance:** `go build ./...` passes; `gateway.Deps`, `handlers.Deps`, and
`handlers.Handler` all carry the two new port fields; `cmd/web` injects them.

---

## Sub-task B — View models and helper structs

**ID:** B  
**depends_on:** A

- [ ] B1. Create `internal/gateway/handlers/charges_vm.go` (or add to handlers.go) with:
  - `ChargeEntryVM` struct (all display-ready string fields + raw edit-form fields, as
    specified in design.md D6).
  - `VehicleOptionVM` struct (TeslaID, VIN, DisplayName, Value — combined form value
    `"{TeslaID}:{VIN}"`).
  - `ChargesPageData` struct (Entries, VehicleOptions, ActiveTeslaID, CSRFToken,
    EmptyState, Error fields).
- [ ] B2. Implement `mapToChargeEntryVM(e manualcharge.Entry, vehicles []account.Vehicle) ChargeEntryVM`
  in the same file. This helper calls `e.CostPerKWh()`, `e.BatteryDelta()`,
  `e.SessionDuration()` and formats them into display strings. No Gin, no HTTP. Unit-testable.
- [ ] B3. Implement `dataForCharges(ctx, h *Handler, uid uuid.UUID, teslaID int64, limit int) (ChargesPageData, error)`
  as a package-level function (gin-free so it is mockable in tests). It:
  - Calls `h.acct.RegisteredVehicles(ctx, uid)` → builds `[]VehicleOptionVM`.
  - Calls `h.manualChargeReader.ListEntriesByVehicle` or `ListEntriesByAccount` depending
    on whether `teslaID != 0`.
  - Maps the results via `mapToChargeEntryVM`.
  - Generates or passes through the CSRF token (token is generated in the page handler,
    passed here as a parameter when called from the list fragment handler).
- [ ] B4. Implement `parseVehicleValue(v string) (teslaID int64, vin string, err error)` to
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

- [ ] C1. Create `internal/gateway/templates/pages/charges.templ` for the full Charge log page:
  - Uses `@layouts.Base("Charge log")`.
  - Contains `@templ.Fragment("charges-create-form") { @fragments.ChargeCreateForm(d) }`.
  - Contains `@templ.Fragment("charges-list") { @fragments.ChargesList(d) }`.
  - Includes an htmx `hx-get="/ui/charges/list"` refresh button targeting `#charges-list`.
- [ ] C2. Create `internal/gateway/templates/fragments/charges_list.templ` with:
  - `ChargesList(d ChargesPageData)` component: the `<div id="charges-list">` table wrapper.
  - Renders `fragments.ChargeRow(vm, d.CSRFToken)` for each entry in `d.Entries`.
  - Renders `ChargesEmptyState()` when `d.EmptyState == true`.
  - Renders a user-facing error message when `d.Error != ""`.
- [ ] C3. Create `internal/gateway/templates/fragments/charge_row.templ` with:
  - `ChargeRow(vm ChargeEntryVM, csrfToken string)` — the static `<tr id="charge-row-{vm.ID}">`.
  - Shows: date, energy, price+currency, cost/kWh, vehicle, optional battery delta, duration.
  - "Edit" button: `hx-get="/ui/charges/row/{vm.ID}/edit"` targeting `#charge-row-{vm.ID}` with
    `hx-swap="outerHTML"`.
  - "Delete" button: `hx-delete="/ui/charges/row/{vm.ID}"` targeting `#charge-row-{vm.ID}`
    with `hx-swap="outerHTML"`, `hx-confirm="Delete this entry?"`, and a hidden
    `<input name="csrf_token" value="{csrfToken}">` inside the form or via `hx-headers`.
- [ ] C4. Create `internal/gateway/templates/fragments/charge_row_edit.templ` with:
  - `ChargeRowEdit(vm ChargeEntryVM, csrfToken string, validationErrors map[string]string)` —
    the inline edit `<tr id="charge-row-{vm.ID}">` with pre-populated inputs.
  - Required fields always visible; optional fields under `<details>` expander.
  - "Save" button: `hx-put="/ui/charges/row/{vm.ID}"` targeting `#charge-row-{vm.ID}` with
    `hx-swap="outerHTML"`.
  - "Cancel" button: `hx-get="/ui/charges/row/{vm.ID}"` targeting `#charge-row-{vm.ID}` with
    `hx-swap="outerHTML"`.
  - Hidden `<input name="csrf_token" value="{csrfToken}">`.
- [ ] C5. Create `internal/gateway/templates/fragments/charge_create_form.templ` with:
  - `ChargeCreateForm(d ChargesPageData, validationErrors map[string]string)` component
    inside `<div id="charges-create-form">`.
  - Required fields: `charged_on`, `energy_added_kwh`, `price`, `currency` (all visible).
  - Vehicle picker `<select name="vehicle">` populated from `d.VehicleOptions`.
  - `<details>` expander "More details" with optional fields.
  - Hidden `<input name="csrf_token" value="{d.CSRFToken}">`.
  - "Log charge" submit: `hx-post="/ui/charges/create"` targeting `#charges-create-form`
    with `hx-swap="outerHTML"` (success response from the handler resets the form and
    triggers an htmx OOB swap to refresh the list — or uses `hx-trigger` to fire the list
    refresh).
- [ ] C6. Add "Charge log" nav link to the base layout template
  (`templates/layouts/base.templ` or equivalent): auth-conditional (visible only to signed-in
  users, same pattern as the Dashboard link).
- [ ] C7. After all `.templ` edits, note in task comments that the user must run
  `templ generate` to produce the `*_templ.go` files. Do NOT hand-edit `*_templ.go`.

**Acceptance:** All `.templ` files compile after `templ generate`; `go build ./...` passes;
templates accept only `ChargeEntryVM` / `ChargesPageData` — no `manualcharge.*` types, no
`pgtype.*` types in template files.

---

## Sub-task D — Read handlers and routes (GET /charges, GET /ui/charges/list, GET /ui/charges/row/{id})

**ID:** D  
**depends_on:** B, C

- [ ] D1. Implement `(h *Handler) ChargePage(c *gin.Context)` in
  `internal/gateway/handlers/` (may be a new `charges.go` file):
  - Auth guard: `currentUID(c)` → redirect to `/login` if no session.
  - Generate CSRF token (16 bytes from `crypto/rand`, hex-encoded), store in session under
    `"csrf_manualcharge"`.
  - Call `dataForCharges(ctx, h, uid, 0, 0)` (account-wide list, default limit).
  - `render(c, http.StatusOK, pages.ChargePage(d))`.
- [ ] D2. Implement `(h *Handler) ChargesListFragment(c *gin.Context)`:
  - Auth guard.
  - Read CSRF token from session (already generated by ChargePage).
  - Call `dataForCharges`.
  - `renderFragment(c, http.StatusOK, pages.ChargePage(d), "charges-list")`.
- [ ] D3. Implement `(h *Handler) ChargeRowStatic(c *gin.Context)` (for cancel-edit):
  - Auth guard.
  - Parse `id` from path param → `uuid.Parse`.
  - Fetch the single entry: call `h.manualChargeReader.ListEntriesByAccount(ctx, uid, 0)`
    and find the entry by ID (or add a `GetEntry` convenience via the existing list — note:
    if this pattern forces a full list scan, the task should call `ListEntriesByAccount` and
    filter; this is acceptable since the "cancel" path is not the hot path, and adding a
    new `GetEntry` method to the Reader port is a follow-up, not a blocker).
  - Render `fragments.ChargeRow(vm, csrfToken)` directly (no page wrapper needed — this
    handler returns the `<tr>` only).
- [ ] D4. Register routes in `internal/gateway/gateway.go`:
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

- [ ] E1. Implement `(h *Handler) ChargeCreate(c *gin.Context)`:
  - Auth guard.
  - CSRF validation: read `csrf_token` from form body; compare to session
    `"csrf_manualcharge"` via `subtle.ConstantTimeCompare`; 403 on mismatch.
  - Parse and validate required form fields (charged_on, energy_added_kwh, price, currency);
    parse optional fields when non-empty.
  - Parse and validate the vehicle value (`parseVehicleValue`); confirm the resolved
    `(teslaID, vin)` is in `h.acct.RegisteredVehicles(ctx, uid)`; 403 on mismatch.
  - Build `manualcharge.Entry{AccountID: uid, TeslaID: teslaID, VIN: vin, ...}`.
  - Call `h.manualChargeWriter.Create(ctx, entry)`.
  - On success: re-render the create form (reset) and trigger an OOB swap to refresh
    `#charges-list` (using htmx OOB swap response headers or a combined fragment response).
  - On validation error: re-render the create form at HTTP 422 with errors map.
  - On write error: re-render with a top-level error message.
- [ ] E2. Register route: `r.POST("/ui/charges/create", h.ChargeCreate)`.

**Acceptance:** Valid POST creates an entry and the list updates. Invalid POST re-renders the
form with errors at 422. CSRF mismatch returns 403 with no write. Vehicle not owned by user
returns 403 with no write. All without a full page reload.

---

## Sub-task F — Inline-edit handlers and routes

**ID:** F  
**depends_on:** B, C, D

- [ ] F1. Implement `(h *Handler) ChargeRowEditFragment(c *gin.Context)`:
  - Auth guard.
  - Parse `id` path param.
  - Fetch the entry (same pattern as D3 — list and filter, or await a `GetEntry` follow-up).
  - Read CSRF token from session.
  - Render `fragments.ChargeRowEdit(vm, csrfToken, nil)` (no errors on first load).
- [ ] F2. Implement `(h *Handler) ChargeRowUpdate(c *gin.Context)`:
  - Auth guard.
  - CSRF validation (same as E1 pattern).
  - Parse `id` path param → `uuid.Parse`.
  - Parse and validate form fields (same validation as create; charged_on, energy_added_kwh,
    price, currency required; optionals parsed when non-empty).
  - Parse and validate vehicle; confirm tenant ownership.
  - Build updated `manualcharge.Entry{ID: id, AccountID: uid, ...}`.
  - Call `h.manualChargeWriter.Update(ctx, entry)`.
  - On success: render the static `<tr>` for that entry (re-use ChargeRowStatic logic).
  - On validation error: re-render the edit form row at HTTP 422 with errors.
  - On write error: re-render with error message.
- [ ] F3. Register routes:
  - `r.GET("/ui/charges/row/:id/edit", h.ChargeRowEditFragment)`
  - `r.PUT("/ui/charges/row/:id", h.ChargeRowUpdate)`

**Acceptance:** `GET /ui/charges/row/{id}/edit` swaps the row to an edit form pre-populated
with the entry's current values. `PUT /ui/charges/row/{id}` updates the entry and swaps back
to the static row on success, or shows inline errors at 422. CSRF mismatch returns 403.

---

## Sub-task G — Delete handler and route

**ID:** G  
**depends_on:** B, C, D

- [ ] G1. Implement `(h *Handler) ChargeRowDelete(c *gin.Context)`:
  - Auth guard.
  - CSRF validation: read `csrf_token` from form body or htmx header.
  - Parse `id` path param → `uuid.Parse`.
  - Call `h.manualChargeWriter.Delete(ctx, uid, id)` (the Writer's scoped WHERE ensures
    cross-tenant deletes are no-ops).
  - On success: respond with an empty `<tr id="charge-row-{id}"></tr>` so htmx
    `hx-swap="outerHTML"` removes the row.
  - On error (DB error): respond with an error fragment in the row target.
  - CSRF mismatch: 403.
- [ ] G2. Register route: `r.DELETE("/ui/charges/row/:id", h.ChargeRowDelete)`.

**Acceptance:** `DELETE /ui/charges/row/{id}` removes the entry scoped to the session
account and the row disappears from the table. CSRF mismatch returns 403 with no delete.
Attempting to delete another user's entry silently deletes nothing (scoped WHERE in Writer).

---

## Sub-task H — httptest handler/gateway tests

**ID:** H  
**depends_on:** A, B, C, D, E, F, G

- [ ] H1. Add fake implementations of `manualcharge.Writer` and `manualcharge.Reader`
  in the test file (or a `testhelpers_test.go` alongside `handlers_test.go`). Fakes
  return configurable data and errors; no real database required.
- [ ] H2. Test `ChargePage` (GET /charges):
  - Auth guard: no session → 302 to `/login`.
  - With session: 200, page HTML contains `charges-list` region.
- [ ] H3. Test `ChargesListFragment` (GET /ui/charges/list):
  - Auth guard.
  - With entries: returns only the fragment (no page shell).
  - Reader error: 200 with error message, no 500.
  - Empty list: renders empty-state message.
- [ ] H4. Test `ChargeCreate` (POST /ui/charges/create):
  - CSRF mismatch → 403.
  - Unowned vehicle → 403.
  - Missing required field → 422 with error in response body.
  - Valid input + fake Writer.Create returning an entry → 200 with new entry in list / form reset.
- [ ] H5. Test `ChargeRowEditFragment` (GET /ui/charges/row/{id}/edit):
  - Auth guard.
  - Returns edit form `<tr>` pre-populated.
- [ ] H6. Test `ChargeRowUpdate` (PUT /ui/charges/row/{id}):
  - CSRF mismatch → 403.
  - Validation error → 422.
  - Valid → 200, static row rendered.
- [ ] H7. Test `ChargeRowDelete` (DELETE /ui/charges/row/{id}):
  - CSRF mismatch → 403.
  - Valid → 200, empty `<tr>` in response.
- [ ] H8. Run `go test ./internal/gateway/...` to confirm all tests pass.

**Acceptance:** `go test ./internal/gateway/...` passes with the new tests; auth guards, CSRF
checks, tenant ownership, error states, and fragment rendering are each covered by at least
one test case per handler.

---

## Sub-task I — Amend AGENTS.md

**ID:** I  
**depends_on:** D4 boundary-rule amendment is implemented and tested (depends_on: H)

- [ ] I1. Edit `internal/gateway/AGENTS.md`, "Read-only at request time" section:
  - Add a new sub-section "Exception: user-initiated writes" documenting the D4 amendment
    (from design.md D4): the gateway MAY call `manualcharge.Writer` on explicit form
    POSTs/PUTs/DELETEs, subject to (1) auth guard, (2) tenant ownership validation via
    `account.Service.RegisteredVehicles`, (3) CSRF token check, (4) this is the only
    permitted write port.
  - Update the `## Public interface` section to document the two new `Deps` fields
    (`ManualChargeWriter`, `ManualChargeReader`) with the same pattern as `TelemetryReader`.
  - Clarify that Reader-only remains the default for all non-form handlers.
- [ ] I2. Do NOT weaken or remove the existing "Read-only at request time" principle — the
  amendment is an additive exception, narrowly scoped.

**Acceptance:** `internal/gateway/AGENTS.md` accurately reflects the post-amendment boundary
rules; the amendment is constrained and does not imply general write access to the gateway.
