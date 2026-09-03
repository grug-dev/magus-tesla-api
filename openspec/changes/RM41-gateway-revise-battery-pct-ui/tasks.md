# Tasks — RM41-gateway-revise-battery-pct-ui

Ownership legend: **[module: gateway worker]** — inside `internal/gateway/`, this
tier's own sandbox. **[doc: gateway worker, granted path]** — the single KB file
this worker is explicitly granted outside `internal/gateway/`
(`kkpa/context/workflows/supercharger-stats-read.md`). See design.md's Decisions,
"Removed surface," and "Test Contract" sections for the rationale and exact
before/after behind each task below — this file assigns work, design.md is the
source of truth for content.

**Hard ordering constraints:**
- Every `.templ` edit (2.1–2.3) depends on BOTH 1.1 (`i18n/catalog.go`) and 1.2
  (`supercharger_vm.go`) having landed — a template still referencing
  `i18n.KeySuperchargerStartEstimate` or `vm.StartBatteryPctEstLabel` after either
  is removed fails to compile.
- 2.4 (`handlers/supercharger.go`) depends on 1.2 only (it references
  `vm.StartBatteryPctEstLabel`/`EndBatteryPctEstLabel` via the VM struct literal,
  not any i18n key) — it does NOT need to wait on 1.1 or on any `.templ` edit, and
  can run in parallel with 2.1–2.3.
- 3.1 (regenerate `*_templ.go`) depends on ALL of 2.1, 2.2, 2.3 having landed — it
  is one repo-wide command, not per-file, so it cannot be split or run early.
- 4.1 (test repair) depends on 1.1, 1.2, 2.4, AND 3.1 — several of its assertions
  render actual templ output (e.g. checking rendered header text), so it needs the
  regenerated `*_templ.go` files, not just the source edits.
- 5.1 (the KB doc fix) has no code dependency and may run at any point in the
  wave — listed in Wave 1 only for scheduling convenience, not because it blocks or
  is blocked by anything.
- 6.1 (verification) runs after every other task.

## Wave 1 — independent, file-scoped edits (module: gateway worker)

- [x] **1.1** `internal/gateway/i18n/catalog.go` —
  - Add `KeySuperchargerBatteryPctHelp Key = "supercharger.battery_pct_help"`
    immediately after `KeySuperchargerEndBattery`'s constant line, and its
    catalogue entry (design.md D2 — exact ES/EN strings given there, copied
    verbatim, no correction) immediately after `KeySuperchargerEndBattery`'s
    catalogue entry.
  - Remove `KeySuperchargerStartEstimate`/`KeySuperchargerEndEstimate` — both
    constants and both catalogue entries.
  - Acceptance: `grep -c "KeySuperchargerStartEstimate\|KeySuperchargerEndEstimate" internal/gateway/i18n/catalog.go`
    returns `0`; `grep -c KeySuperchargerBatteryPctHelp internal/gateway/i18n/catalog.go`
    returns `2` (constant + entry).
  `depends_on`: — · `parallel_ok`: with 1.2, 1.3

- [x] **1.2** `internal/gateway/templates/fragments/supercharger_vm.go` — remove
  the `StartBatteryPctEstLabel`/`EndBatteryPctEstLabel` fields (and their doc
  comments) from `SuperchargerRowVM`. Acceptance:
  `grep -c "BatteryPctEst" internal/gateway/templates/fragments/supercharger_vm.go`
  returns `0`.
  `depends_on`: — · `parallel_ok`: with 1.1, 1.3

- [x] **1.3** `kkpa/context/workflows/supercharger-stats-read.md` — replace the
  three bullets under "Requirement: Supercharger Stats session table displays
  battery percentages" describing the old four-column table with design.md
  "Docs"' exact replacement text (three bullets, the third and fourth new) — copy
  it verbatim, do not paraphrase. Acceptance: the file contains no remaining
  mention of "start estimate, end estimate" as two of "the four battery columns."
  `depends_on`: — · `parallel_ok`: with 1.1, 1.2

## Wave 2 — dependent template/handler edits (module: gateway worker)

- [x] **2.1** `internal/gateway/templates/fragments/supercharger_stats.templ` —
  - In `SuperchargerStatsContent`, add
    `@ui.Alert(ui.AlertProps{Kind: "info", Class: "mb-4"}) { { i18n.T(ctx,
    i18n.KeySuperchargerBatteryPctHelp) } }` as the FIRST element inside the
    template body, before the `if v.Presets != nil` block (design.md D1 — exact
    placement and rationale given there).
  - In `superchargerTable`'s `ui.Table` call, remove the two
    `i18n.T(ctx, i18n.KeySuperchargerStartEstimate)` /
    `i18n.T(ctx, i18n.KeySuperchargerEndEstimate)` header entries.
  - Acceptance: `grep -c "KeySuperchargerStartEstimate\|KeySuperchargerEndEstimate" internal/gateway/templates/fragments/supercharger_stats.templ`
    returns `0`; `grep -c KeySuperchargerBatteryPctHelp internal/gateway/templates/fragments/supercharger_stats.templ`
    returns `1`.
  `depends_on`: 1.1, 1.2 · `parallel_ok`: with 2.2, 2.3, 2.4

- [x] **2.2** `internal/gateway/templates/fragments/supercharger_row.templ` —
  remove the two `<td>{ vm.StartBatteryPctEstLabel }</td>` /
  `<td>{ vm.EndBatteryPctEstLabel }</td>` lines from `SuperchargerRow`. In
  `SuperchargerRowError`, change `<td colspan="9">` to `<td colspan="7">` and
  update its doc comment ("8 data cells + Actions" → "6 data cells + Actions").
  Acceptance: `grep -c "BatteryPctEst" internal/gateway/templates/fragments/supercharger_row.templ`
  returns `0`; `grep -c 'colspan="9"' internal/gateway/templates/fragments/supercharger_row.templ`
  returns `0`.
  `depends_on`: 1.1, 1.2 · `parallel_ok`: with 2.1, 2.3, 2.4

- [x] **2.3** `internal/gateway/templates/fragments/supercharger_row_edit.templ` —
  remove the two `ui.Field(ui.FieldProps{Label: i18n.T(ctx,
  i18n.KeySuperchargerStartEstimate/EndEstimate)})` blocks (each wrapping a
  `<span>{ vm.Start/EndBatteryPctEstLabel }</span>`) from `SuperchargerRowEdit`.
  Change `<td colspan="9">` to `<td colspan="7">`. Acceptance:
  `grep -c "BatteryPctEst\|StartEstimate\|EndEstimate" internal/gateway/templates/fragments/supercharger_row_edit.templ`
  returns `0`; `grep -c 'colspan="9"' internal/gateway/templates/fragments/supercharger_row_edit.templ`
  returns `0`.
  `depends_on`: 1.1, 1.2 · `parallel_ok`: with 2.1, 2.2, 2.4

- [x] **2.4** `internal/gateway/handlers/supercharger.go` — remove the two
  `StartBatteryPctEstLabel: formatBatteryPct(s.StartBatteryPctEst)` /
  `EndBatteryPctEstLabel: formatBatteryPct(s.EndBatteryPctEst)` assignments from
  `superchargerRowVMFromSession`. Do NOT touch `charging.Session` itself or any
  other field mapping in this function. Acceptance:
  `grep -c "BatteryPctEst" internal/gateway/handlers/supercharger.go` returns `0`.
  `depends_on`: 1.2 · `parallel_ok`: with 2.1, 2.2, 2.3

## Wave 3 — codegen (module: gateway worker)

- [x] **3.1** Run `make templ` (pinned `go tool templ generate`) to regenerate
  `supercharger_stats_templ.go`, `supercharger_row_templ.go`, and
  `supercharger_row_edit_templ.go` from the three edited `.templ` files. Never
  hand-edit a `*_templ.go` file. `make css` is NOT needed for this change — no new
  DaisyUI/Tailwind class is introduced (`ui.Alert` and its `mb-4` class already
  exist in the compiled stylesheet, used identically by `dashboard.templ`).
  Acceptance: `grep -c "BatteryPctEst\|StartEstimate\|EndEstimate" internal/gateway/templates/fragments/supercharger_stats_templ.go internal/gateway/templates/fragments/supercharger_row_templ.go internal/gateway/templates/fragments/supercharger_row_edit_templ.go`
  returns `0` across all three files.
  `depends_on`: 2.1, 2.2, 2.3 · `parallel_ok`: no

## Wave 4 — test repair (module: gateway worker)

- [x] **4.1** `internal/gateway/handlers/supercharger_test.go` — apply design.md's
  Test Contract items 1–5 exactly (each names the test function, the exact
  before/after code, and the expected values that stay unchanged):
  1. `TestBuildSuperchargerRows_NilEnergyAndCostRenderDash` — drop the last two
     elements of the label-comparison loop.
  2. `TestBuildSuperchargerRows_PopulatedFields` — drop
     `StartBatteryPctEst`/`EndBatteryPctEst` from the fixture and the two
     `*EstLabel` clauses from the final assertion.
  3. `TestSuperchargerStatsFragment_RendersBatteryHeadersAndValues` — drop
     `StartBatteryPctEst`/`EndBatteryPctEst` from the fixture and
     `"Estimación inicial"`, `"Estimación final"`, `"42%"`, `"78%"` from the
     `want` slice.
  4. `TestSuperchargerStatsContent_RendersEnglishHeadersAndNilBatteryValues` —
     drop `StartBatteryPctEstLabel`/`EndBatteryPctEstLabel` from the VM literal
     and `"Start estimate"`, `"End estimate"` from the `want` slice.
  5. `TestSuperchargerRowUpdate_BothEmptyClearsBothPercentages` — drop
     `StartBatteryPctEst`/`EndBatteryPctEst` from the `fakeSessionVerifier`
     fixture and delete the `"42%"`/`"78%"` assertion block outright; leave the
     em-dash count assertion (`!= 2`) unchanged.
  Do NOT touch any other test in the file — none of the other ~40 test functions
  names `BatteryPctEst`/`StartEstimate`/`EndEstimate` and none of their expected
  values change (design.md Test Contract, closing paragraph). Do NOT add new
  assertions covering the guidance alert's rendered text (roadmap D7 — no new unit
  tests).
  Acceptance: `grep -c "BatteryPctEst\|StartEstimate\|EndEstimate\|Estimación\|estimate" internal/gateway/handlers/supercharger_test.go`
  returns `0`; `go vet ./internal/gateway/...` compiles the whole package with no
  error (vet compiles `_test.go` files — the cheap signal every occurrence was
  actually caught).
  `depends_on`: 1.1, 1.2, 2.4, 3.1 · `parallel_ok`: no

## Wave 5 — verification (assistant-run signals, then owner-run suite)

- [ ] **5.1** Run and report: `go build ./internal/gateway/...`, `go vet
  ./internal/gateway/...`, `gofmt -l internal/gateway`, `make i18n-guard`, `make
  ui-guard`. This tier's own definition of done, restated from proposal.md's
  acceptance bar: `grep -rn "BatteryPctEst\|StartEstimate\|EndEstimate"
  internal/gateway/` returns nothing at all (zero lines, across every file type —
  `.go`, `.templ`, `_templ.go`, `_test.go`). Per the Test-Execution-Policy, never
  run `go test ./...`, `make test`, `make test-with-db`, or `make check`.
  `depends_on`: 1.1, 1.2, 1.3, 2.1, 2.2, 2.3, 2.4, 3.1, 4.1 · `parallel_ok`: no

- [ ] **5.2** Hand off to the owner the exact command to run and report:
  `go test ./internal/gateway/...`. Until the owner reports a pass, this tier's
  implementation status is **awaiting-user-verification**, never "done."
  `depends_on`: 5.1 · `parallel_ok`: no
