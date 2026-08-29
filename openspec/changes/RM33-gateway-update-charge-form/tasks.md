# Tasks — RM33-gateway-update-charge-form

Ownership legend: **[module: gateway worker]** — inside `internal/gateway/` only. **[owner]** —
the human. See design.md for the rationale behind each group.

> No design gate applies (no database object is touched — `CLAUDE.md` §Pipeline config →
> `Design-Gates: database` is scoped to DB-touching changes only). Implementation may start once
> this change is proposed; there is no owner-confirmation checkpoint to wait on before Wave 1.

## Ordering constraints

- **Wave 1 is foundational and has no dependency** — the `ui.Input` suffix capability and the
  i18n catalogue changes are both additive/self-contained. **1.1 and 1.2 touch disjoint files**
  and may run in parallel.
- **Wave 2 (`charges_vm.go`) depends on nothing but may as well follow Wave 1** since its new
  `Status`/`RequiredEndedAt`/`RequiredEndBatteryPct` fields reference the same names Wave 1's
  catalogue keys and Wave 3's handler logic use — sequencing it right after Wave 1 avoids
  redundant re-reads. It has no compile dependency on Wave 1's files, though.
- **Wave 3 (`handlers/charges.go`) depends on 2.1** (`ChargeFormValues` must exist to extend
  `parseChargeForm`'s return signature). **3.1, 3.2, 3.3 are all in the same file** — do them in
  one pass, not as separately dispatchable parallel tasks, even though they touch different
  functions.
- **Wave 4 (the two `.templ` files) depends on 1.1, 1.2, 2.1, and 3.2/3.3** (the templates
  reference `ui.InputProps.Suffix`, the new i18n keys, `ChargesPageData.FormValues`/
  `RequiredEndedAt`/`RequiredEndBatteryPct`, and `ChargeEntryVM.Status`/`RawStatus`/
  `RequiredEndedAt`/`RequiredEndBatteryPct`). **4.1 and 4.2 touch disjoint files
  (`charge_create_form.templ` vs `charge_row_edit.templ`) and may run in parallel.**
- **Wave 5 (codegen) depends on Wave 4** — `make templ` then `make css` (new DaisyUI `label`/`grow`
  classes must exist in the compiled stylesheet before the app ships them; `make generate` runs
  both).
- **Wave 6 (`static/app.js`) has no compile dependency on Waves 2-5** — the two listeners only
  need to know the (unchanged) form input `name` attributes, which are fixed by design.md. It MAY
  run any time after Wave 1, in parallel with Waves 2-5.
- **Wave 7 (`AGENTS.md` RD12/RD13 entries) depends on Wave 6** — document the JS as it was
  actually written, not as planned.
- **Wave 8 (tests) depends on Waves 3, 4, and 6** — every offline `httptest` in this tier is
  pure/no-DB (`ai/go-conventions.md` §Testing authoring order calls these "write early, TDD-style"
  in general, but here they assert against `parseChargeForm`'s new signature and the templates'
  new markup, both of which must compile/exist first — there is no DB-integration wave in this
  tier at all, so Wave 8 is both the earliest *and* the only test wave, and it is last because its
  subject matter (Waves 3+4+6) is not ready before then).
- **Wave 9 (signals) depends on Wave 8.**

---

## Wave 1 — foundation (ui kit + i18n catalogue)

- [x] **1.1** **[module: gateway worker]** `internal/gateway/templates/ui/input.templ` — add
  `Suffix string` to `InputProps` and the conditional `label`-wrapped render path from design.md
  §D-Suffix, verbatim (DaisyUI v5's `<label class="input"><input class="grow".../><span
  class="label">{Suffix}</span></label>` idiom, confirmed via Context7 against
  `/saadeghi/daisyui`). `Suffix == ""` must render **byte-identical** markup to today's plain
  `<input class="input font-mono w-full">` — verify against the current `_templ.go` diff after
  `make templ` in Wave 5 that every other `ui.Input` call site (search the whole
  `internal/gateway` tree, not just the charges templates) is untouched.
  `depends_on`: — · `parallel_ok`: with 1.2

- [x] **1.2** **[module: gateway worker]** `internal/gateway/i18n/catalog.go`:
  - **Add** keys (both `ES`/`EN` on the catalogue map line, per `internal/gateway/AGENTS.md` §i18n
    D1 convention): status field label, `IN_PROGRESS` option text, `DONE` option text, odometer
    field label, and validation messages for: `ended_at` required (when status is DONE), status
    invalid/unrecognized, odometer invalid (non-integer or negative). Suggested key names (adjust
    to match the existing `charges_form.*` / `charges_error.*` naming convention exactly):
    `KeyChargesFormStatus`, `KeyChargesFormStatusInProgress`, `KeyChargesFormStatusDone`,
    `KeyChargesFormOdometer`, `KeyChargesErrorEndedAtRequired`, `KeyChargesErrorStatusInvalid`,
    `KeyChargesErrorOdometerInvalid`.
  - **Change** the `ES`/`EN` values (not the key names — `KeyChargesFormAC`/`KeyChargesFormDC`
    already exist and are referenced by both templates) to roadmap **D16**'s exact strings: ES
    `AC — Carga lenta (casa/destino)` / `DC — Carga rápida (Supercargador)`; EN `AC — Slow charging
    (home/destination)` / `DC — Fast charging (Supercharger)`.
  - **Remove** `KeyChargesFormCurrency`, `KeyChargesErrorEnergyRequired`,
    `KeyChargesErrorPriceRequired` (both the `Key` constant and the catalogue map entry) — verify
    with `grep -rn "KeyChargesFormCurrency\|KeyChargesErrorEnergyRequired\|KeyChargesErrorPriceRequired" internal/gateway`
    (excluding `_templ.go`, `catalog.go` itself) returns **zero** matches before deleting; if any
    remain, do not delete that key and report it instead.
  `depends_on`: — · `parallel_ok`: with 1.1

---

## Wave 2 — view-model surface

- [x] **2.1** **[module: gateway worker]** `internal/gateway/templates/fragments/charges_vm.go`:
  - Add the `ChargeFormValues` struct from design.md §D-Values, verbatim (all ten fields, doc
    comment included).
  - Add `FormValues ChargeFormValues` to `ChargesPageData`, with a doc comment cross-referencing
    roadmap D15.
  - Add `RequiredEndedAt bool` and `RequiredEndBatteryPct bool` to **both** `ChargesPageData` and
    `ChargeEntryVM` (design.md §D-Fields — the handler-computed, template-consumed required-state
    pair). Doc comment: computed from `charging.RequiredFieldsFor`, never computed in the
    template.
  - Add `Status string` and `RawStatus string` to `ChargeEntryVM` — `Status` for a possible future
    display label (unused by this tier's markup, kept for symmetry with every other VM field
    pair), `RawStatus` for the edit form's `<option selected>` binding, holding the persisted
    entry's status string.
  - Add `RawOdometerKm string` to `ChargeEntryVM` (leader addition, 2026-08-29) — the edit row's
    odometer input binds to it to render the persisted reading, exactly as `RawEnergyKWh` /
    `RawPrice` already do. design.md §D-Values covers the create form's
    `ChargeFormValues.OdometerKm` but omitted the edit row's VM counterpart; task 4.2 flagged the
    gap and it is closed here rather than left to a worker's judgement.
  `depends_on`: — · `parallel_ok`: yes (new file additions only; do this any time, though Wave 1
  first avoids rework if a key name changes)

---

## Wave 3 — handler logic (single file, sequential)

- [x] **3.1** **[module: gateway worker]** `internal/gateway/handlers/charges.go` —
  `parseChargeForm`:
  - Change the signature to return `(charging.Entry, fragments.ChargeFormValues,
    map[string]string, bool)` per design.md §D-Values. Build the `ChargeFormValues` from
    `c.PostForm(...)` calls **before** any other parsing, so it is populated on every return path
    (success and every failure).
  - Parse `status`: reject anything other than `"IN_PROGRESS"`/`"DONE"` with
    `errs["status"] = i18n.T(ctx, i18n.KeyChargesErrorStatusInvalid)`; on success set
    `entry.Status = charging.Status(statusStr)`.
  - Compute `required := charging.RequiredFieldsFor(entry.Status)` (only reachable once `status`
    parsed successfully — an invalid status short-circuits before this call, matching the "reject
    before any data is written" contract the charging module itself follows) and build a
    `map[charging.Field]bool`. Use it to decide whether `ended_at`/`end_battery_pct` are validated
    as required (design.md §D-Fields table). **Do not** change `start_battery_pct`,
    `charged_on`, or `location_kind`'s validation — they stay unconditionally required exactly as
    today.
  - `energy_added_kwh`: empty string → `entry.EnergyAddedKWh = nil`, no error. Non-empty → parse
    and validate `> 0` as today, on the same field key.
  - `price`: empty string → `price = 0`, no error (roadmap **D7**). Non-empty → parse and validate
    `>= 0` as today.
  - `odometer_km` (new, always optional): empty → `entry.OdometerKm = nil`. Non-empty → parse as
    integer, validate `>= 0`, else `errs["odometer_km"] = i18n.T(ctx, i18n.KeyChargesErrorOdometerInvalid)`.
  - Remove the two now-dead `errs["energy_added_kwh"] = ...Required` / `errs["price"] =
    ...Required` branches entirely (their catalogue keys were removed in 1.2).
  `depends_on`: 2.1 · `parallel_ok`: no (same file as 3.2/3.3)

- [x] **3.2** **[module: gateway worker]** `internal/gateway/handlers/charges.go` —
  `buildChargesPage` and `chargeEntryVMFromEntry`:
  - `buildChargesPage`: set `d.FormValues.Status = string(charging.StatusInProgress)` on the
    fresh-page-load path (design.md §D-Values — "fresh-load fields keep rendering blank"). Compute
    `d.RequiredEndedAt` / `d.RequiredEndBatteryPct` from `charging.RequiredFieldsFor(charging.StatusInProgress)`
    (the create form's status is always `IN_PROGRESS` on a fresh render, since `FormValues.Status`
    is set to it above — both must agree).
  - `chargeEntryVMFromEntry`: map `e.Status` into `vm.Status` and `vm.RawStatus` (both the same
    string value — see 2.1's doc comment for why two fields exist), map `e.OdometerKm` into
    `vm.RawOdometerKm` (nil → `""`, mirroring how the other `Raw*` pointer-backed fields render),
    and compute `vm.RequiredEndedAt` / `vm.RequiredEndBatteryPct` from
    `charging.RequiredFieldsFor(e.Status)`.
  `depends_on`: 3.1 · `parallel_ok`: no (same file)

- [x] **3.3** **[module: gateway worker]** `internal/gateway/handlers/charges.go` — wire the D15
  fix into the two error-render paths:
  - `ChargeCreate`'s 422 and 500 branches: after `d := h.buildChargesPage(...)`, overwrite
    `d.DefaultChargedOn`/`d.DefaultStartedAt`/`d.DefaultEndedAt` with the submitted raw
    `charged_on`/`started_at`/`ended_at` strings (from the `raw ChargeFormValues` `parseChargeForm`
    now returns) and set `d.FormValues = raw`.
  - `ChargeRowUpdate`'s 422 and 500 branches: build the `ChargeEntryVM` passed to
    `fragments.ChargeRowEdit` from `raw` instead of `chargeEntryVMFromEntry(entry, vehicles)` — a
    new small helper (e.g. `chargeEntryVMFromRawValues(id string, raw fragments.ChargeFormValues,
    vehicleLabel string) fragments.ChargeEntryVM`) mapping every `Raw*` field 1:1 from `raw`, plus
    the untouched `ID`/`VehicleLabel` the handler already has independent of parsing. Also set
    `RequiredEndedAt`/`RequiredEndBatteryPct` on this VM from `charging.RequiredFieldsFor(charging.Status(raw.Status))`
    (falling back to the strictest set if `raw.Status` fails to parse as a `charging.Status` — the
    same fail-closed posture `charging.RequiredFieldsFor` itself documents for an unrecognized
    status).
  - Every other call site of `parseChargeForm` in this file updates to the new 4-return signature
    (Go's compiler enforces this — `go vet` will not compile until every call site is fixed).
  `depends_on`: 3.1, 3.2 · `parallel_ok`: no (same file)

---

## Wave 4 — templates

- [x] **4.1** **[module: gateway worker]** `internal/gateway/templates/fragments/charge_create_form.templ`:
  - Add the `status` `<select>` (`@ui.Select`) as the **first** field in the main grid, before
    `charged_on`, with options `IN_PROGRESS`/`DONE` using the new i18n keys, `selected?={
    d.FormValues.Status == "IN_PROGRESS" }` / `"DONE"`.
  - Drop `Required: true` from `energy_added_kwh` and `price`; bind `Value: d.FormValues.EnergyAddedKWh`
    / `d.FormValues.Price`.
  - Replace the `price` `@ui.Input` call with `Suffix: "COP"` (marking the literal `"COP"` string
    with `// i18n:allow: ISO currency code` on the line above, per `internal/gateway/AGENTS.md`
    §i18n placement rule for `.templ` files).
  - **Remove** the Currency `@ui.Field`/`@ui.Input` block entirely.
  - Add `selected?={ d.FormValues.LocationKind == "HOME" }` etc. to the `location_kind` options
    (today's create form has none — this tier fixes that as part of D15).
  - Bind `Value: d.FormValues.StartBatteryPct` / `EndBatteryPct` on the two battery inputs (today's
    create form has neither).
  - Bind `ended_at`'s and `end_battery_pct`'s `Required` to `d.RequiredEndedAt` /
    `d.RequiredEndBatteryPct` instead of the current hardcoded `true` for the pct field and
    hardcoded absence for `ended_at`.
  - Update the AC/DC `<option>` text to use the (now-changed) `KeyChargesFormAC`/`KeyChargesFormDC`
    values (no template change needed beyond what's already there — the i18n values changed in
    1.2, not the call sites — confirm this and do not duplicate the change).
  - Add `selected?={ d.FormValues.ChargingType == "AC" }` / `"DC"` (today's create form has
    neither).
  - Add the `odometer_km` `@ui.Input` (`Type: "number"`, `min="0"`, `step="1"`) inside the
    existing `<details>`/"More details" block, alongside `charging_type`/`location_label`/`notes`
    (roadmap **D-RM33-7**), bound to `Value: d.FormValues.OdometerKm`.
  - Bind `location_label`'s `Value: d.FormValues.LocationLabel` and `notes`'s body to
    `{ d.FormValues.Notes }` (today's create form has neither).
  `depends_on`: 1.1, 1.2, 2.1, 3.2, 3.3 · `parallel_ok`: with 4.2

- [x] **4.2** **[module: gateway worker]** `internal/gateway/templates/fragments/charge_row_edit.templ`:
  - Same `status` `<select>` as 4.1, as the first field, `selected?={ vm.RawStatus == "IN_PROGRESS"
    }` / `"DONE"` (the persisted value — "persisted and loaded" per the ticket).
  - Drop `Required: true` from `energy_added_kwh` and `price` (values stay bound to
    `vm.RawEnergyKWh`/`vm.RawPrice` as today — those are unaffected by D15 since the edit row's
    non-error render already sources from the persisted `Entry`).
  - `price` gains `Suffix: "COP"` (same `i18n:allow` marker), and the Currency `@ui.Field` block is
    **removed**.
  - Bind `ended_at`'s and `end_battery_pct`'s `Required` to `vm.RequiredEndedAt` /
    `vm.RequiredEndBatteryPct`.
  - Update the AC/DC option text (same non-change as 4.1 — the i18n values already changed).
  - Add the `odometer_km` `@ui.Input` (`Type: "number"`, `min="0"`, `step="1"`) inside the
    existing "More details" block, bound to `Value: vm.RawOdometerKm` — the field is added by task
    2.1, so this is a plain binding with no conditional. `chargeEntryVMFromEntry` (task 3.2) must
    populate it from `e.OdometerKm` (nil → `""`).
  - **Do not touch `<td colspan="8">`** — design.md §D-Colspan, tier 3's territory.
  `depends_on`: 1.1, 1.2, 2.1, 3.2, 3.3 · `parallel_ok`: with 4.1


- [x] **4.3** **[module: gateway worker]** `internal/gateway/i18n/catalog.go` — **deferred deletion
  from task 1.2.** Wave 1 could not remove `KeyChargesFormCurrency`,
  `KeyChargesErrorEnergyRequired` and `KeyChargesErrorPriceRequired` because 1.2's mandated grep
  still found live references (`charge_create_form.templ:58`, `charge_row_edit.templ:54`,
  `handlers/charges.go:734,746`) — tasks 3.1/4.1/4.2 remove those call sites. Re-run the grep from
  1.2 now; when it returns **zero** matches outside `catalog.go` and `_templ.go`, delete all three
  (`Key` constant + catalogue map entry). If any reference still remains, do NOT delete that key —
  report which one and where, because that means 3.1/4.1/4.2 left a call site behind.
  `depends_on`: 3.3, 4.1, 4.2 · `parallel_ok`: no

---

## Wave 5 — codegen

- [x] **5.1** **[module: gateway worker]** Run `make templ` (regenerates `*_templ.go` for both
  edited fragments and `ui/input_templ.go`), then `make css` (the new DaisyUI `label`/`grow`
  classes must be present in the committed `static/app.css` — check
  `git diff --stat internal/gateway/static/app.css` shows a change; if it shows none, the classes
  were already covered by an existing scan and that is fine, but confirm rather than assume).
  `make generate` runs both in one step and is an acceptable substitute.
  `depends_on`: 4.1, 4.2 · `parallel_ok`: no

---

## Wave 6 — client-side JS (RD12 + RD13)

- [x] **6.1** **[module: gateway worker]** `internal/gateway/static/app.js` — add the two listeners
  from design.md §D-JS:
  - RD12: a `change` listener (delegated on `document.body`) matching
    `input[name="charged_on"]`, rewriting the date portion of `started_at`/`ended_at` within
    `evt.target.closest("form")` when each is non-empty, leaving an empty one untouched.
  - RD13: a `change` listener matching `select[name="status"]`, plus an `htmx:load` listener that
    re-applies the same toggle for every `select[name="status"]` present in the loaded/swapped
    content — both setting `ended_at.required` / `end_battery_pct.required` to
    `select.value === "DONE"` within the same form. **Verify the `htmx:load` event name and firing
    semantics (initial load + every swap) against current htmx docs via Context7
    (`/context7/htmx_org` or the project's existing `/a-h/templ`-adjacent htmx source) before
    relying on it** — do not assume from memory.
  - Keep both additions in the file's existing style: plain functions, no library, `document.body`
    delegation, defensive `querySelector`/`closest` null-checks mirroring RD9/RD10's guard style.
  `depends_on`: — (no compile dependency; sequenced after Wave 1 for report-ordering convenience
  only) · `parallel_ok`: with Waves 2-5

---

## Wave 7 — documentation (RD8: record every client-side-JS decision in the same change)

- [ ] **7.1** **[module: gateway worker]** `internal/gateway/AGENTS.md`:
  - Add **RD12 — date→time-preserving sync** and **RD13 — status-driven required toggle** as new
    sections mirroring RD9/RD10/RD11's exact shape (What / Why / Rejected alternative / Boundary —
    this is NOT an opening for general client-side JS), using design.md §D-JS's content as the
    source, updated to match what 6.1 actually implemented if anything diverged.
  - Update §"Client-side JS exception" cross-references if the file's structure numbers them
    sequentially.
  `depends_on`: 6.1 · `parallel_ok`: yes

---

## Wave 8 — tests (offline `httptest`, no DB — this tier's only test wave)

> **Fixture convention (established while repairing wave 2's fallout, 2026-08-29 — read before
> writing any new fixture in `charges_test.go`).** design.md Test Contract **A4** makes a *missing*
> `status` a validation error, so every `url.Values` fixture that reaches `parseChargeForm` must
> carry an explicit `"status"`. Of the file's 19 charge-form fixtures, 15 now do. The **four that
> must NOT** are `TestChargeCreate_CSRFMismatch`, `TestChargeRowUpdate_CSRFMismatch`,
> `TestChargeCreate_NoSessionToken` and `TestChargeRowUpdate_NoSessionToken` — both handlers check
> CSRF and the session *before* calling `parseChargeForm`, so a status there would imply a
> dependency the code does not have.
>
> **The trap:** a validation test that asserts only `422` + writer-not-called passes off the
> spurious missing-status error and stays green even if the check it names is deleted
> (`TestChargeCreate_NonPositiveEnergy_Rejected` and `TestChargeCreate_MissingRequiredField` both
> did, until fixed). A new negative test must therefore supply a valid `status` **and** assert the
> specific i18n message for the field under test, not just the status code.

- [ ] **8.1** **[module: gateway worker]** `internal/gateway/handlers/charges_test.go` — Group A
  (design.md Test Contract A1–A8): status-conditional required validation, optional
  energy/price, odometer parsing, unconditional `start_battery_pct` requirement across both
  statuses. Mirror the existing fake-`Writer`/fake-`Reader` fixture style already in this file
  (`TestChargeCreate_MissingBatteryPct_Rejected` etc. are the closest existing precedent).
  `depends_on`: 3.3 · `parallel_ok`: with 8.2, 8.3

- [ ] **8.2** **[module: gateway worker]** `internal/gateway/handlers/charges_test.go` — Group B
  (design.md Test Contract B1–B4): the D15 value-preservation assertions for both the create form
  and the inline edit row, plus the B4 no-regression check on a fresh (non-error) render.
  `depends_on`: 3.3, 4.1, 4.2 · `parallel_ok`: with 8.1, 8.3

- [ ] **8.3** **[module: gateway worker]** `internal/gateway/handlers/charges_test.go` — Group C
  (design.md Test Contract C1–C7): rendered-markup assertions for required attributes, the
  status-select default/persisted-selection, the removed Currency field / COP suffix, the AC/DC
  option text (both languages), and the odometer input's placement inside "More details".
  `depends_on`: 4.1, 4.2, 5.1 · `parallel_ok`: with 8.1, 8.2

- [ ] **8.4** **[owner]** Manually verify the three JS behaviors design.md's Test Contract
  "Owner-verified, not automatable here" section lists (date-sync preserving time; live required
  toggle with no network request; independent behavior across two simultaneously-open inline edit
  rows). Record pass/fail against each of the three in the session/report — this is not
  automatable by `httptest` and is not skippable.
  `depends_on`: 6.1, 5.1 · `parallel_ok`: with 8.1-8.3

---

## Wave 9 — signals

- [ ] **9.1** **[module: gateway worker]** Run the cheap deterministic signals the
  `Test-Execution-Policy` allows: `gofmt -l ./internal/gateway`, `go build ./...`,
  `go vet ./...`, `make ui-guard`, `make i18n-guard`. Confirm
  `TestCatalog_AllKeysHaveBothLanguages`-relevant catalogue entries are present for every key
  added in 1.2 (that specific test itself is part of `go test`/`make check` and is owner-run, but
  `make i18n-guard`'s static scan is Claude's to run and should already catch a missed key).
  `depends_on`: 8.1, 8.2, 8.3 · `parallel_ok`: no

---

## Owner verification (`Test-Execution-Policy`: the owner runs the suite)

- [ ] **O1** **[owner]** Run the suite. Nothing above may be reported as `done` on the assistant's
  say-so; work that is complete but unexecuted is **`awaiting-user-verification`**.
  ```bash
  make check
  ```
  (`make check` = `build vet ui-guard i18n-guard money-guard test`.)

- [ ] **O2** **[owner]** Perform the manual JS verification from task **8.4** if it was not already
  completed and recorded during implementation (`make dev` for the hot-reload loop).
