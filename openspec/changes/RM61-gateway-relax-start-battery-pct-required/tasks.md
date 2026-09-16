# Tasks — RM61-gateway-relax-start-battery-pct-required

Ownership legend: **[module: gateway worker]** — inside `internal/gateway/` only.
**[leader]** — outside the module sandbox, so a module worker may not do it without an
explicit grant. **[owner]** — the human. See design.md **D1–D7** for the rationale behind
each group.

No design gate applies here (design.md §"No Database Changes") — implementation may start
immediately, no owner confirmation needed first.

## Ordering constraints

- **Wave 1 is small and has no internal dependency** — the two files it touches
  (`templates/ui/field.templ`, `i18n/catalog.go`) are disjoint, so 1.1 and 1.2 run in
  parallel.
- **Wave 2 touches `external_charges_vm.go` and `external_charges.go`.** 2.2 reads the
  struct field 2.1 declares, so 2.2 depends on 2.1 even though they are different files.
- **Wave 3 (the two `.templ` forms) needs Wave 1's `Help` prop and catalogue key, and 3.2
  additionally needs Wave 2's `VM.StartBatterySource` field.** 3.1 and 3.2 touch disjoint
  files and run in parallel once their dependencies land.
- **Wave 4 (`make templ`) is a serialization point** — it must run after Wave 3 and before
  any test that renders these templates, because `go vet`/`go test` compile against the
  generated `*_templ.go`, not the `.templ` source.
- **Wave 5's tests cannot compile before Wave 4.** Their expected values are already fixed
  in design.md §Test Contract — implement against that contract, not against whatever the
  code happens to produce. 5.1 covers `internal/gateway/handlers/external_charges_test.go`
  (one file, so its sub-items are sequential within the task, not parallel); 5.2 covers a
  new `internal/gateway/templates/ui/field_test.go` (a different file, so 5.2 runs in
  parallel with 5.1).
- **No comment in any file this tier touches may cite `design.md`, a decision id (`D1`…`D7`),
  or `RM61`/`RD3`/`RD5`/`MAG-40`.** Write the reason itself, the same way the code snippets
  in design.md already do. This binds every wave below.
- **This tier has no cross-module compile-fix task**: everything is additive or a
  restriction lifted, never a signature change on an exported type another module reads
  (proposal.md §Breaking). `go build ./...` / `go vet ./...` should stay green outside
  `internal/gateway` throughout.

---

## Wave 1 — kit + catalogue (parallel)

- [x] **1.1** **[module: gateway worker]** `internal/gateway/templates/ui/field.templ`:
  add `Help string` to `FieldProps` with the doc comment from design.md **D1**, and render
  it right after `{ children... }` and before the existing `Error` paragraph:
  ```templ
  if p.Help != "" {
      <p class="fieldset-label">{ p.Help }</p>
  }
  ```
  Do not touch `Error`, `Optional`, or the `fieldset`/`fieldset-legend` markup around them.
  `fieldset-label` is a DaisyUI class living inside `templates/ui/` — this is the sanctioned
  place for it (`ui-guard` never scans this directory), not an exception to the no-inline
  rule.
  `depends_on`: — · `parallel_ok`: with 1.2

- [x] **1.2** **[module: gateway worker]** `internal/gateway/i18n/catalog.go`:
  - Add `KeyChargesFormStartBatteryPctHelp Key = "charges_form.start_battery_pct_help"` and
    its catalogue entry, both `ES`/`EN` on the same line, exact wording from design.md
    **D2**.
  - Correct `KeyChargesFormCreateHint`'s catalogue entry: replace only the second sentence
    per design.md **D7.1** (ES: `"Una carga En progreso solo requiere Fecha y Ubicación."`,
    EN: `"An In progress charge only requires Date and Location."`). The first sentence
    (the energy-from-battery-delta estimate) is untouched — do not reword it.
  - Do not touch `KeyChargesFormEditHint` — it states the DONE-required set
    (`ended_at`/`end_battery_pct`), which this change does not alter.
  `depends_on`: — · `parallel_ok`: with 1.1

---

## Wave 2 — view model + handler

- [x] **2.1** **[module: gateway worker]** `internal/gateway/templates/fragments/external_charges_vm.go`:
  add `StartBatterySource string` to `ExternalChargeEntryVM`, placed next to
  `RawStartBatteryPct`, with the doc comment from design.md **D3**. Do not import
  `internal/charging` in this file — the VM stays a plain string, per its own package doc
  comment ("no charging.\*").
  `depends_on`: — · `parallel_ok`: with 1.1/1.2 (different file), but must land before 2.2

- [x] **2.2** **[module: gateway worker]** `internal/gateway/handlers/external_charges.go`:
  - In `externalChargeEntryVMFromEntry`, map `e.StartBatterySource *charging.StartBatterySource`
    to the new `StartBatterySource string` field exactly as design.md **D3** shows (`nil` →
    `""`, else `string(*e.StartBatterySource)`), and add it to the returned struct literal
    next to `RawStartBatteryPct`.
  - Do **not** set `StartBatterySource` in `externalChargeEntryVMFromRawValues` — its zero
    value (`""`) is the correct behavior there (design.md D3's own explanation: no persisted
    entry to read a provenance from, so the field always renders as a plain echoed value on
    a 4xx/5xx re-render).
  - In `parseExternalChargeForm`, replace the block whose own comment currently says
    `start_battery_pct` is unconditionally required (starts around line 1403 as read on
    2026-09-16 — re-locate by the comment text, not the line number, since nearby edits may
    have shifted it) with the exact block from design.md **D6**: `startPct` becomes `*int`,
    empty input means `nil` with no error, a non-empty value still gets the same 0–100 range
    check as today. Update the `charging.Entry{...}` literal a few lines below from
    `StartBatteryPct: &startPct,` to `StartBatteryPct: startPct,`.
  - Do not touch the `end_battery_pct` block, `location_kind`, `ended_at`, or any other
    field's validation in this function. `i18n.KeyChargesErrorBatteryPctRequired` stays in
    the catalogue — it is still used for `end_battery_pct`.
  `depends_on`: 2.1 · `parallel_ok`: no

---

## Wave 3 — the two forms (parallel once Wave 1 + 2.1 land)

- [x] **3.1** **[module: gateway worker]**
  `internal/gateway/templates/fragments/external_charge_create_form.templ`: on the
  `start_battery_pct` `ui.Field`, remove `Required: true` from the `ui.Input`, and change
  the `ui.Field` call to add `Optional: true` and
  `Help: i18n.T(ctx, i18n.KeyChargesFormStartBatteryPctHelp)`, exactly as design.md **D5**
  shows. Do not touch `d.StartBatteryPctSuggestion`'s existing placeholder wiring — it is
  unrelated to this change. Do not touch any other field in this file.
  `depends_on`: 1.1, 1.2 · `parallel_ok`: with 3.2

- [x] **3.2** **[module: gateway worker]**
  `internal/gateway/templates/fragments/external_charge_row_edit.templ`: replace the
  `start_battery_pct` `ui.Field`/`ui.Input` pair with the exact `if vm.StartBatterySource ==
  "ESTIMATED" { ... } else { ... }` branch from design.md **D4** — placeholder-only when
  `"ESTIMATED"`, value-only (as today, minus `Required`) otherwise. Add `Optional: true` and
  the same `Help` key as 3.1 to the `ui.Field` wrapper. Do not touch any other field in this
  file, and do not add a similar branch to any field other than `start_battery_pct`.
  `depends_on`: 1.1, 1.2, 2.1 · `parallel_ok`: with 3.1

---

## Wave 4 — regenerate (serialization point)

- [x] **4.1** **[module: gateway worker]** Run `make templ` (allowed by `CLAUDE.md` §"Builds
  & local checks"). Confirm both `external_charge_create_form_templ.go` and
  `external_charge_row_edit_templ.go` regenerated with no unrelated diff. Run `make css` only
  if a new Tailwind/DaisyUI class was introduced — `fieldset-label` already ships in the
  vendored `daisyui.mjs` (verified 2026-09-16), so `make css` should be a no-op; run it
  anyway and confirm `git diff internal/gateway/static/app.css` is empty or trivial.
  `depends_on`: 3.1, 3.2 · `parallel_ok`: no (blocks Wave 5)

---

## Wave 5 — tests (unit tests included, per the roadmap header)

- [x] **5.1** **[module: gateway worker]** `internal/gateway/handlers/external_charges_test.go`
  — implement design.md §Test Contract Groups **B, C, D, E, F** exactly:
  - **Group B**: extend `TestExternalChargeEntryVMFromEntry` (or add a sibling test) with
    the three `StartBatterySource` mapping cases (nil / `USER` / `ESTIMATED`).
  - **Group C**: new test(s) using the existing `renderEditRow` helper, asserting the
    placeholder-vs-value choice for `"ESTIMATED"`, `"USER"`, and `""` (via `tagAttrsFor`,
    the same helper `TestExternalChargeForms_C1_...` already uses).
  - **Group D**: extend the create-form suggestion tests to assert `start_battery_pct`
    carries no `required` attribute, and that the field's legend/help both render.
  - **Group E** (behavior fixes):
    - `TestExternalChargeCreate_MissingBatteryPct_Rejected` → rewrite per **E1**: now
      expects HTTP 200 and `writer.createEntry.StartBatteryPct == nil`. Rename if the new
      assertion no longer matches "Rejected" (e.g.
      `TestExternalChargeCreate_MissingStartBatteryPct_Accepted`).
    - `TestExternalChargeCreate_A8_StartBatteryPctRequired_BothStatuses` → rewrite per
      **E2** and rename to `TestExternalChargeCreate_StartBatteryPctOptional_BothStatuses`.
    - `TestExternalChargeCreate_OutOfRangeBatteryPct_Rejected` → **no change** (design.md
      E3); confirm it still passes.
    - `TestExternalChargeForms_C1_OptionalFieldsCarryNoRequired_UnconditionalFieldsDo` →
      move `"start_battery_pct"` from the unconditional-required slice to the
      optional-no-required slice (**E4**).
    - `TestExternalChargeRowUpdate_D3b_ValidationFailureKeepsThePostedWindow` → change the
      422-triggering field from an omitted `start_battery_pct` to
      `"start_battery_pct": {"150"}` (out of range), per **E5**; update the inline comment
      to say so.
  - **Group F** (comment/name hygiene, listed in design.md — apply all four):
    `TestExternalChargePage_BatterySuggestionFromTelemetry`'s stale "must be Required (D6)"
    comment; `TestExternalChargeCreate_MissingRequiredField`'s field list (drop
    `start_battery_pct`); `TestExternalChargeCreate_ValidInput` and
    `TestExternalChargeRowUpdate_ValidInput`'s `// D6: required battery fields...` comments
    (reword to "supplied", not "required"). None of these four need an assertion change —
    comment-only.
  `depends_on`: 4.1 · `parallel_ok`: with 5.2

- [x] **5.2** **[module: gateway worker]** Create
  `internal/gateway/templates/ui/field_test.go`, package `ui`, mirroring
  `theme_switcher_test.go`'s render-to-`bytes.Buffer` pattern. Implement design.md §Test
  Contract Group **A** (A1–A3): `Help` renders a `fieldset-label` paragraph when non-empty,
  renders nothing when empty, and — when both `Help` and `Error` are set — both render, with
  `Help` appearing before `Error` in the output.
  `depends_on`: 1.1 · `parallel_ok`: with 5.1

---

## Wave 6 — documentation (`CLAUDE.md` §Non-negotiables: docs track change)

- [x] **6.1** **[module: gateway worker]** `internal/gateway/AGENTS.md`, "UI stack (styling)"
  section: add one short bullet for `ui.FieldProps.Help`, mirroring the existing
  `ui.FieldProps.Optional` bullet's shape and length — what it renders, and that it is
  independent of `Optional` (a field may carry either, both, or neither). Do not restate
  design.md's full D1 rationale; point at the two call sites (`start_battery_pct` on both
  charge forms) as the worked example.
  `depends_on`: 5.2 · `parallel_ok`: with 6.2

- [ ] **6.2** **[leader — outside the gateway worker's normal doc-pack scope, but the file IS
  inside `internal/gateway/`, so no grant is needed]** Confirm 6.1 did not duplicate content
  already moved to the KB by MAG-39 — the per-field "Optional fields in the main grid" list
  lives in `kkpa/context/`, not here (see 6.3). `internal/gateway/AGENTS.md` should gain only
  the kit-level `Help` prop description, nothing page-specific.
  `depends_on`: 6.1 · `parallel_ok`: no

- [ ] **6.3** **[leader — outside `internal/gateway/`; grant the path or do it]**
  `kkpa/context/input-port/charging/external-charges.md`, "Form layout & field rules"
  section: add `start_battery_pct` to the `ui.FieldProps.Optional` list (design.md **D7.2**)
  — it now reads `energy_added_kwh`, `price`, `started_at`, `start_battery_pct`,
  `location_label`. Also add one short bullet under "Manual charge form helper copy" noting
  the new `KeyChargesFormStartBatteryPctHelp` field-help line and that
  `KeyChargesFormCreateHint`'s IN_PROGRESS required-set sentence was corrected in this
  change (state the new sentence, not just that it changed).
  `depends_on`: 3.1, 3.2 · `parallel_ok`: with 6.1

- [ ] **6.4** **[leader]** Sync this change's `specs/gateway/spec.md` delta into
  `openspec/specs/gateway/spec.md` via the project's normal spec-sync step, at the point the
  pipeline calls for it. Confirm the sync does not touch `openspec/changes/archive/`.
  `depends_on`: — (tracked here so it is not forgotten; timing follows the pipeline's own
  sync/archive step)

---

## Wave 7 — signals

- [x] **7.1** **[module: gateway worker]** Run the cheap deterministic signals the
  `Test-Execution-Policy` allows: `gofmt -l ./internal/gateway`, `go build ./...`,
  `go vet ./...`, `make i18n-guard`, `make ui-guard`. **All five should be clean** after
  this tier — `go build ./...`/`go vet ./...` repo-wide (proposal.md §Breaking: nothing is
  breaking), `i18n-guard` because the new copy goes through `i18n.T`/the catalogue, and
  `ui-guard` because `fieldset-label` lives inside `templates/ui/`, not a page/fragment. If
  any of the five fails, stop and report it rather than assuming it is expected.
  `depends_on`: 5.1, 5.2, 6.1 · `parallel_ok`: no

---

## Owner verification (`Test-Execution-Policy`: the owner runs the suite)

- [ ] **O1** **[owner]** Run the suite. Nothing above may be reported as `done` on the
  assistant's say-so; work that is complete but unexecuted is
  **`awaiting-user-verification`**.
  ```bash
  make check
  ```
  (`= build vet ui-guard i18n-guard money-guard tz-guard migration-guard boundary-guard
  theme-guard vehicleref-guard tenancy-guard archive-guard test`. No `make migrate-up` and
  no `make test-with-db` are needed for this tier — it touches no schema.)

- [ ] **O2** **[owner]** Manual check in the browser (this module's own testing convention
  — appearance and layout are verified by eye, never asserted in Go): open
  `/external-charges`, confirm the "Start battery %" field shows the new help line and the
  `(optional)` legend suffix, and — with an existing entry whose start percentage was
  derived — open its inline edit row and confirm the field shows the stored number as a
  greyed-out placeholder, not a filled value.

## Cross-module tasks the leader owns

- [ ] **L1** **[leader]** Confirm `go build ./...`/`go vet ./...` are green outside
  `internal/gateway` once Wave 7 lands. Proposal.md §Breaking states no cross-module fix
  should be needed — verify rather than assume.
- [ ] **L2** **[leader]** Confirm the root `README.md` and `cmd/README.md` need no edit.
  This change adds no module, no runnable, and no change to `internal/gateway`'s `Deps`
  surface (no new port, no new field on `Deps`).
  `depends_on`: 7.1 · `parallel_ok`: yes
