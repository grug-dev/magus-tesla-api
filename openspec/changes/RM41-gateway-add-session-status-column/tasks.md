# Tasks — RM41-gateway-add-session-status-column

Ownership legend: **[module: gateway worker]** — inside `internal/gateway/`, this
tier's own sandbox. **[doc: gateway worker, granted path]** — the single KB file
this worker is explicitly granted outside `internal/gateway/`
(`kkpa/context/workflows/supercharger-stats-read.md`). See design.md's "Confirmed
inputs," "Badge Kind mapping," "Template changes," "Docs," and "Local decisions"
sections for the rationale and exact before/after behind each task — this file
assigns work, design.md is the source of truth for content.

**Hard ordering constraints:**
- 2.1 (`supercharger_row.templ`), 2.2 (`supercharger_row_edit.templ`), and 2.3
  (`supercharger_stats.templ`) each reference the new i18n keys (1.1) — none can
  compile/render correctly before 1.1 lands. 2.1 additionally references
  `vm.RawStatus`, so it also depends on 1.2.
- 2.4 (`handlers/supercharger.go`) depends on 1.2 only (`RawStatus` field must
  exist on the VM struct before the handler can assign to it) — it does NOT
  depend on 1.1 or on any `.templ` edit, and can run in parallel with 2.1–2.3.
- 3.1 (regenerate `*_templ.go`) depends on ALL of 2.1, 2.2, 2.3 having landed — it
  is one repo-wide command, not per-file, so it cannot be split or run early.
- 4.1 (test verification/repair) depends on 1.1, 1.2, 2.4, AND 3.1 — several
  Test Contract items check actual rendered output, so it needs the regenerated
  `*_templ.go` files, not just the source edits.
- 5.1 (the KB doc fix) has no code dependency and may run at any point in the
  wave — listed in Wave 1 only for scheduling convenience, not because it blocks
  or is blocked by anything.
- 6.1/6.2 (verification) run after every other task.

## Wave 1 — independent, file-scoped edits (module: gateway worker)

- [x] **1.1** `internal/gateway/i18n/catalog.go` — add the five new keys and
  their catalogue entries exactly as given in design.md "i18n keys" (constant
  block insertion points: after `KeySuperchargerDate` for the header key, a new
  comment-headed block after `KeySuperchargerRowCancel` for the three badge
  labels, and after `KeySuperchargerBatteryPctHelp`'s existing entry for the
  status-help key). Copy the ES/EN strings verbatim — do not paraphrase.
  Acceptance: `grep -c "KeySuperchargerStatus \|KeySuperchargerBadgeInProgress\|KeySuperchargerBadgeDoneCalculated\|KeySuperchargerBadgeDone \|KeySuperchargerStatusHelp" internal/gateway/i18n/catalog.go`
  returns `10` (5 constants + 5 entries — note the trailing space on
  `KeySuperchargerStatus `/`KeySuperchargerBadgeDone ` in the grep pattern
  disambiguates them from `KeySuperchargerStatusHelp`/`KeySuperchargerBadgeDoneCalculated`).
  `depends_on`: — · `parallel_ok`: with 1.2, 5.1

- [x] **1.2** `internal/gateway/templates/fragments/supercharger_vm.go` — add the
  `RawStatus string` field (with its doc comment) to `SuperchargerRowVM`, per
  design.md "View model change". Acceptance:
  `grep -c "RawStatus string" internal/gateway/templates/fragments/supercharger_vm.go`
  returns `1`.
  `depends_on`: — · `parallel_ok`: with 1.1, 5.1

## Wave 2 — dependent template/handler edits (module: gateway worker)

- [x] **2.1** `internal/gateway/templates/fragments/supercharger_row.templ` —
  - In `SuperchargerRow`, insert the Status `<td>` as the row's 2nd cell (right
    after the Date `<td>`, before Site), using the exact `if/else if/else`
    three-way badge block from design.md "Template changes" §`supercharger_row.templ`.
  - In `SuperchargerRowError`, change `<td colspan="7">` to `<td colspan="8">`
    and update its doc comment ("6 data cells + Actions" → "7 data cells +
    Actions").
  - Acceptance: `grep -c 'colspan="7"' internal/gateway/templates/fragments/supercharger_row.templ`
    returns `0`; `grep -c "vm.RawStatus" internal/gateway/templates/fragments/supercharger_row.templ`
    returns `3` (one per branch).
  `depends_on`: 1.1, 1.2 · `parallel_ok`: with 2.2, 2.3, 2.4

- [x] **2.2** `internal/gateway/templates/fragments/supercharger_row_edit.templ` —
  change `<td colspan="7">` to `<td colspan="8">` (design.md "Finding: a second
  colspan" — the edit row's full-width cell, missed by the dispatch's own
  colspan callout). No other line in this file changes — the edit row itself
  gains no Status field or input. Acceptance:
  `grep -c 'colspan="7"' internal/gateway/templates/fragments/supercharger_row_edit.templ`
  returns `0`; `grep -c 'colspan="8"' internal/gateway/templates/fragments/supercharger_row_edit.templ`
  returns `1`.
  `depends_on`: 1.1, 1.2 · `parallel_ok`: with 2.1, 2.3, 2.4

- [x] **2.3** `internal/gateway/templates/fragments/supercharger_stats.templ` —
  - In `superchargerTable`'s `ui.Table` call, insert
    `i18n.T(ctx, i18n.KeySuperchargerStatus)` as the Headers slice's 2nd entry
    (right after `KeySuperchargerDate`, before `KeySuperchargerSite`).
  - In `SuperchargerStatsContent`, insert a second
    `ui.Alert(ui.AlertProps{Kind: "info", Class: "mb-4"})` rendering
    `i18n.T(ctx, i18n.KeySuperchargerStatusHelp)` immediately below the existing
    `KeySuperchargerBatteryPctHelp` alert and above the
    `if v.Presets != nil { ... }` block — both alerts unconditional, per
    design.md D3.
  - Acceptance: `grep -c "KeySuperchargerStatus\b" internal/gateway/templates/fragments/supercharger_stats.templ`
    returns `1` (the header call); `grep -c "KeySuperchargerStatusHelp" internal/gateway/templates/fragments/supercharger_stats.templ`
    returns `1`; the file contains exactly two `ui.Alert(ui.AlertProps{Kind: "info"`
    occurrences.
  `depends_on`: 1.1, 1.2 · `parallel_ok`: with 2.1, 2.2, 2.4

- [x] **2.4** `internal/gateway/handlers/supercharger.go` — add
  `RawStatus: string(s.Status),` to the `fragments.SuperchargerRowVM{...}` literal
  returned by `superchargerRowVMFromSession`. Do NOT touch `charging.Session`,
  any other field mapping in this function, or any other handler in the file.
  Acceptance: `grep -c "RawStatus: string(s.Status)" internal/gateway/handlers/supercharger.go`
  returns `1`.
  `depends_on`: 1.2 · `parallel_ok`: with 2.1, 2.2, 2.3

## Wave 3 — codegen (module: gateway worker)

- [x] **3.1** Run `make templ` (pinned `go tool templ generate`) to regenerate
  `supercharger_row_templ.go`, `supercharger_row_edit_templ.go`, and
  `supercharger_stats_templ.go` from the three edited `.templ` files. Never
  hand-edit a `*_templ.go` file. `make css` is NOT needed — `ui.Badge`/`ui.Alert`
  and every Kind/class this change uses (`badge-primary`, `badge-neutral`,
  `badge-ghost`, `alert-info`, `mb-4`) already exist in the compiled stylesheet,
  used identically elsewhere (`charge_row.templ`, this page's existing alert).
  Acceptance: `grep -c 'colspan="7"' internal/gateway/templates/fragments/supercharger_row_templ.go internal/gateway/templates/fragments/supercharger_row_edit_templ.go`
  returns `0` across both files; the regenerated
  `supercharger_stats_templ.go`/`supercharger_row_templ.go` contain the new key
  constants' resolved string literals are NOT expected here (i18n resolves at
  render time via `i18n.T`, not codegen time) — instead confirm
  `grep -c "KeySuperchargerStatus" internal/gateway/templates/fragments/supercharger_stats_templ.go internal/gateway/templates/fragments/supercharger_row_templ.go`
  is `>= 1` across the two files combined.
  `depends_on`: 2.1, 2.2, 2.3 · `parallel_ok`: no

## Wave 4 — test verification / repair (module: gateway worker)

- [x] **4.1** `internal/gateway/handlers/supercharger_test.go` — verify against
  design.md's Test Contract (items 1–12):
  - Confirm the file still compiles as-is (`go vet ./internal/gateway/...` is the
    cheap signal — see 6.1). Because every existing assertion in this file is
    field-by-field (no `reflect.DeepEqual`/struct-literal comparison against
    `SuperchargerRowVM` or `charging.Session` — confirmed in design.md's
    "Confirmed inputs"), adding `RawStatus` to the VM and reading
    `charging.Session.Status` (already present since tier 4) is NOT expected to
    break any existing test. If it does, that is a genuine finding — STOP and
    report it rather than silently loosening an assertion.
  - Do NOT add new test functions asserting the badge's rendered Kind/label
    (roadmap D7 — no new unit tests). The Test Contract in design.md stands as
    the authored expected-values record for this surface; a future change that
    DOES add coverage here reads that section rather than re-deriving it.
  - Spot-check (read, do not edit unless broken):
    `TestSuperchargerStatsFragment_RendersBatteryHeadersAndValues` and
    `TestSuperchargerStatsContent_RendersEnglishHeadersAndNilBatteryValues` still
    pass their existing assertions unmodified (Test Contract item 12) — both use
    `strings.Contains`/count-based checks that a new column cannot break.
  Acceptance: `go vet ./internal/gateway/...` compiles the whole package with no
  error (vet compiles `_test.go` files — the cheap signal that would catch any
  break here).
  `depends_on`: 1.1, 1.2, 2.4, 3.1 · `parallel_ok`: no

## Wave 5 — docs (doc: gateway worker, granted path)

- [x] **5.1** `kkpa/context/workflows/supercharger-stats-read.md` — apply
  design.md "Docs" findings 1–3 verbatim:
  1. Append `, and `RawStatus` (RM41 tier 5)` to the `SuperchargerRowVM` clause
     in the Component map row for `supercharger_vm.go`.
  2. Append the "Extended by RM41 tier 5..." sentence to the glossary intro line
     (currently ending "...the session table carries the four `charging.Session`
     battery-percentage columns.").
  3. Add the new "The session table's 2nd column is a Status badge..." bullet
     under "Conventions & gotchas", immediately after the existing "There is no
     longer an estimate column in the gateway" bullet.
  Acceptance: the file contains the string `RM41-gateway-add-session-status-column`
  at least once, and `grep -c "Status badge" kkpa/context/workflows/supercharger-stats-read.md`
  returns `>= 1`.
  `depends_on`: — · `parallel_ok`: with 1.1, 1.2 (may run any time in the wave)

## Wave 6 — verification (assistant-run signals, then owner-run suite)

- [ ] **6.1** Run and report: `go build ./internal/gateway/...`, `go vet
  ./internal/gateway/...`, `gofmt -l internal/gateway`, `make i18n-guard`, `make
  ui-guard`. This tier's own definition of done: every Test Contract item in
  design.md holds (verified by reading the rendered output, since no new test
  asserts them per D7), `grep -rn 'colspan="7"' internal/gateway/templates/fragments/supercharger_row*.templ`
  returns nothing, and `grep -c "KeySuperchargerStatus\|KeySuperchargerBadge" internal/gateway/i18n/catalog.go`
  is non-zero. Per the Test-Execution-Policy, never run `go test ./...`, `make
  test`, `make test-with-db`, or `make check`.
  `depends_on`: 1.1, 1.2, 2.1, 2.2, 2.3, 2.4, 3.1, 4.1, 5.1 · `parallel_ok`: no

- [ ] **6.2** Hand off to the owner the exact command to run and report:
  `go test ./internal/gateway/...`. Until the owner reports a pass, this tier's
  implementation status is **awaiting-user-verification**, never "done."
  `depends_on`: 6.1 · `parallel_ok`: no
