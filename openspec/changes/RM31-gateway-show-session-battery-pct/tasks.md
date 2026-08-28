# Tasks — RM31-gateway-show-session-battery-pct

Implementation stays inside `internal/gateway/`. No task writes `progress.json`; the leader owns
that state. Checkboxes are updated live only when their acceptance is actually met.

## Wave 1 — independent view vocabulary (parallel_ok: yes; disjoint files)

- [x] **1.1** **[module: gateway worker]** `internal/gateway/templates/fragments/supercharger_vm.go` — add the four display-ready string fields `StartBatteryPctLabel`, `EndBatteryPctLabel`, `StartBatteryPctEstLabel`, and `EndBatteryPctEstLabel` to `SuperchargerRowVM`, with comments documenting `"<n>%"` or `"—"`. Acceptance: the VM carries no `charging.Session` pointer/type and has no Country/Billing field. `depends_on`: — · `parallel_ok`: yes, with 1.2 and 1.3.

- [x] **1.2** **[module: gateway worker]** `internal/gateway/i18n/catalog.go` — append four semantic Supercharger battery-header keys and same-line non-empty ES/EN catalogue entries, following the existing constant/map ordering. Acceptance: every new table header has exactly one catalogue key and both translations are non-empty; do not add Country. `depends_on`: — · `parallel_ok`: yes, with 1.1 and 1.3.

- [x] **1.3** **[module: gateway worker]** `internal/gateway/templates/fragments/supercharger_stats.templ` — extend the existing `ui.Table` headers/cells with the four i18n-backed battery columns and the four VM labels, preserving Date/Site/Energy/Cost and delegation to `historyBarChart`. Acceptance: no hardcoded user-facing text, no HTML comments, no raw DaisyUI table class, no Country column, and header/cell order is identical. `depends_on`: 1.1, 1.2 · `parallel_ok`: yes after 1.1/1.2 land; otherwise wait for their shapes.

## Wave 2 — presentation mapping and shared chart reuse

- [x] **2.1** **[module: gateway worker]** `internal/gateway/handlers/supercharger.go` — in `buildSuperchargerChart`, set every bucket bar label to `YYYY-MM`, use the existing `buildYAxisTicks(maxKWh, kWh formatter)`, and set `LabelVertical: true` for empty and non-empty results. Keep existing bucket count, heights, tooltip semantics, and shared renderer. Acceptance: no second tick helper/chart renderer/chart library; zero max produces nil/empty ticks. `depends_on`: — · `parallel_ok`: yes, with 2.2 (disjoint functions in one file require conflict-free coordination; default serial worker is preferred).

- [x] **2.2** **[module: gateway worker]** `internal/gateway/handlers/supercharger.go` — in `buildSuperchargerRows`, map all four existing `charging.Session` battery fields to the new VM labels as `<integer>%` or exactly `"—"` when nil. Acceptance: estimates remain a direct nil-safe display mapping only; no estimator, write, extra read, Country field, or template formatting is introduced. `depends_on`: 1.1 · `parallel_ok`: yes with 2.1 only if the same-file edit is coordinated; otherwise serial.

## Wave 3 — deterministic tests and generated template

- [x] **3.1** **[module: gateway worker]** `internal/gateway/handlers/supercharger_test.go` — add pure/offline tests for chart `YYYY-MM` labels, five kWh ticks at positive max, no ticks at zero max, and `LabelVertical=true`; add row-mapping tests for populated four-field percentages and all-nil em-dash values. Acceptance: fixtures are `charging.Session`; expected values meet design.md T1–T5; existing currency/nil cost behavior remains asserted. `depends_on`: 2.1, 2.2 · `parallel_ok`: no (shared test file with 3.2).

- [x] **3.2** **[module: gateway worker]** `internal/gateway/handlers/supercharger_test.go` — add/update an existing page/fragment `httptest` assertion covering both-language header resolution, populated battery cells, nil em-dash cells, and Country absence. Acceptance: meets design.md T6–T7 using the existing language-context/fake-reader patterns; no database or network dependency. `depends_on`: 1.2, 1.3, 2.2 · `parallel_ok`: no (shared test file with 3.1).

- [x] **3.3** **[module: gateway worker — codegen]** Run `make templ` after the `.templ` change and include the regenerated `internal/gateway/templates/fragments/supercharger_stats_templ.go`. Acceptance: generated artifact reflects the four header/cell additions and is not hand-edited. Do not run build or test commands. `depends_on`: 1.3 · `parallel_ok`: no (must run after all template edits).

## Wave 4 — artifact and owner verification handoff

- [x] **4.1** **[module: gateway worker]** Re-read the implemented diff against design.md D1–D6 and specs/gateway/spec.md, then update only the completed task checkboxes in this file. Acceptance: no code outside `internal/gateway/`, no database object, write path, estimator, Country restoration, or new chart implementation has entered the change. `depends_on`: 3.1, 3.2, 3.3 · `parallel_ok`: no.

- [x] **4.2** **[owner verification]** Run `go test ./internal/gateway/...`. Acceptance: report the result to the leader; until then the work remains awaiting-user-verification, not done. `depends_on`: 4.1 · `parallel_ok`: no.

- [x] **4.3** **[owner re-verification]** Re-run `go test ./internal/gateway/...` after review finding R1's test-coverage correction. Acceptance: report the result to the leader; until then R1 cannot be closed or re-reviewed. `depends_on`: 3.2 · `parallel_ok`: no.

- [ ] **4.4** **[owner verification]** Re-run `go test ./internal/gateway/...` after review finding R2's missing zero-max y-axis ticks test. Acceptance: report the result to the leader; until then R2 cannot be closed or re-reviewed. `depends_on`: 3.1 · `parallel_ok`: no.
