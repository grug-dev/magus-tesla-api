> **Gateway-only validation + UI convenience change** (tier 2 of roadmap
> `RM49-analysis-start-date`, depends on the already-archived tier 1). Adds a
> server-side rejection of a `charged_on` before the account's analysis start date to
> `parseExternalChargeForm` (shared by create and edit), a `min` attribute convenience on
> the date input, and two i18n catalogue keys. No `Deps` change, no new module
> dependency, no database object. **No unit tests** (roadmap D10) — `design.md`'s
> "Rejection Contract" table authors the expected values for the owner to check by hand.
>
> **Dependencies / parallelism:**
> - T1 (i18n catalogue keys) has no dependencies. Independent of every other task —
>   `i18n/catalog.go` is touched by nothing else in this tier.
> - T2 (handler logic: the validation check + `MinChargedOn` computation, both in
>   `internal/gateway/handlers/external_charges.go`, plus the new `MinChargedOn` field on
>   `internal/gateway/templates/fragments/external_charges_vm.go`) depends on T1 — it
>   references the new `i18n.Key` constants, which must exist for the package to compile.
> - T3 (template wiring: the `min` attribute on both `.templ` date inputs, and the new
>   `ExternalChargeRowEdit` parameter) depends on T2 — the generated `*_templ.go` code
>   references `d.MinChargedOn` (from T2's VM field) and the new call-site argument (from
>   T2's handler edits), so it must compile against what T2 adds. T3 does NOT touch any
>   file T2 touches (disjoint: three `.templ` files vs. two `.go` files), so a second agent
>   MAY start T3 as soon as T2's exact field/parameter names are fixed (they are pinned
>   verbatim in `design.md` D4/D5) — sequencing here is about correctness of the generated
>   code, not a file conflict.
> - T4 (docs: the KB page-detail guide + INDEX.md check) depends on T2 and T3 — it
>   describes the finished behavior and the finished template shape.
> - T5 (verification) depends on T1–T4.
>
> **Leader-integrated step:** run `make templ && make css` after T3 lands, to regenerate
> `*_templ.go` and `app.css` from the edited `.templ` files — required before `go build`
> can succeed and before `make ui-guard`/`make i18n-guard` can see the final markup.

## T1. i18n catalogue keys (`internal/gateway/i18n/catalog.go`) — no dependencies

- [x] T1.1 Add `KeyChargesErrorDateBeforeAnalysisStart` (`"charges_error.
      date_before_analysis_start"`) to the `Key` const block, in the `charges_error`
      group, near `KeyChargesErrorInProgressExists` (same "%s date" shape — see its own
      doc comment for the pattern to mirror). Add its ES/EN entry to the catalogue map,
      both non-empty, with exactly one `%s` verb for the formatted analysis start date.
      See `design.md` D6 for a starting wording.
- [x] T1.2 Add `KeyChargesErrorCouldNotValidateAnalysisStartDate` (`"charges_error.
      could_not_validate_analysis_start_date"`), near
      `KeyChargesErrorCouldNotValidateVehicleOwnership` (same "internal lookup failed"
      shape). Add its ES/EN entry, both non-empty, no `%s` verb.
      Acceptance: `go build ./internal/gateway/i18n/...` compiles; both new keys appear in
      both the const block and the catalogue map (a key present in one but not the other
      fails `TestCatalog_AllKeysHaveBothLanguages`, though that test itself is owner-run
      per Test-Execution-Policy — visually confirm the pairing here).

## T2. Handler logic (`internal/gateway/handlers/external_charges.go`, `internal/gateway/templates/fragments/external_charges_vm.go`) — depends on T1

- [x] T2.1 Add `MinChargedOn string` to `ExternalChargesPageData`
      (`external_charges_vm.go`), with the doc comment from `design.md` D4.
- [x] T2.2 In `parseExternalChargeForm`, add the analysis-start-date check exactly as
      `design.md` D1 specifies: inside the existing `charged_on` parse block, only reached
      when `chargedOnStr` parsed successfully. Call `h.acct.AnalysisStartDateFor(c.Request.
      Context(), uid)`. On error, log it and set `errs["_top"]` to the new
      `KeyChargesErrorCouldNotValidateAnalysisStartDate` message (D3, fail closed). On
      success, if `chargedOn.Before(minDate)`, set `errs["charged_on"]` to
      `fmt.Sprintf(i18n.T(...KeyChargesErrorDateBeforeAnalysisStart), minDate.Format("2006-01-02"))`
      (D2 — the exact comparison and the accept-on-boundary case).
- [x] T2.3 In `buildExternalChargesPage`, compute `minChargedOn` once, at the top of the
      function, BEFORE the `teslaIDFilter == 0` early return (D4 — the create form needs a
      `min` even with no vehicle resolved). Set it on both `fragments.
      ExternalChargesPageData{}` return literals (the early-return one and the final one).
- [x] T2.4 In `ExternalChargeRowUpdate`, compute `minChargedOn` once, near where
      `windowStartStr`/`windowEndStr` are already computed once and reused (mirror that
      existing pattern exactly — same function, same "computed once, reused by every
      branch" shape). Pass it as the new sixth argument to both of this function's
      `fragments.ExternalChargeRowEdit(...)` calls (the 422 branch and the 500 branch).
      Acceptance: this task alone does NOT compile yet — `fragments.ExternalChargeRowEdit`
      still has its old five-parameter signature until T3 lands. That failure is expected;
      confirm it is exactly a call-site arg-count mismatch on `ExternalChargeRowEdit`, no
      other unrelated error. `go vet ./internal/gateway/i18n/...` and `go vet ./internal/
      gateway/templates/fragments/...` (isolated packages that do compile at this point)
      may be run to sanity-check T1/T2.1 in isolation.

## T3. Template wiring (`internal/gateway/templates/fragments/external_charge_create_form.templ`, `external_charge_row_edit.templ`, `external_charges_list.templ`) — depends on T2

- [x] T3.1 In `external_charge_create_form.templ`, add `Attrs: templ.Attributes{"min":
      d.MinChargedOn}` to the existing `charged_on` `ui.Input(...)` call (design.md D5) —
      do not touch any other field on this form.
- [x] T3.2 In `external_charge_row_edit.templ`: add `minChargedOn string` as the sixth
      parameter of the `templ ExternalChargeRowEdit(...)` signature (after
      `windowStartStr, windowEndStr`), and add `Attrs: templ.Attributes{"min":
      minChargedOn}` to its `charged_on` `ui.Input(...)` call.
- [x] T3.3 In `external_charges_list.templ`, update its one call site of
      `ExternalChargeRowEdit(vm, d.CSRFToken, nil, d.WindowStartStr, d.WindowEndStr)` to
      pass `d.MinChargedOn` as the new sixth argument.
- [x] T3.4 Run `make templ` (regenerates the three `*_templ.go` files) then `make css`
      (no new class is introduced by this tier, but run it per the module's standing
      "always finish with make css" convention after any `.templ` edit).
      Acceptance: `go build ./...` now passes repo-wide (T2.4's expected failure from the
      previous task is resolved by this signature change). `go vet ./...` passes. `gofmt
      -l .` reports no diff.

## T4. Docs (`kkpa/context/input-port/charging/external-charges.md`, `kkpa/context/INDEX.md`) — depends on T2, T3

- [ ] T4.1 Add a new section to `kkpa/context/input-port/charging/external-charges.md`,
      titled to mirror its existing "Manual charge rule: one IN_PROGRESS entry per
      (vehicle, charged_on)" section (e.g. "Manual charge rule: charged_on cannot be
      before the account's analysis start date"). State: where the check runs
      (`parseExternalChargeForm`), what it calls (`account.Service.
      AnalysisStartDateFor`), the exact comparison and the accept-on-boundary case (D2),
      the `min`-attribute convenience (D5), and that `internal/charging` is untouched
      (roadmap D5). Link back to this change's `design.md` for the full contract, the same
      way the existing sections link to their own originating changes.
- [ ] T4.2 Read `kkpa/context/INDEX.md`'s existing "analysis start date" row (added by
      tier 1) and confirm whether it needs updating to name this tier as a consumer of
      `AnalysisStartDateFor`. Update it if so; report either way (`CLAUDE.md`'s
      docs-track-change / KB rule — grep `kkpa/context/` for the module and read every
      match before calling this done). Do not edit anything under
      `openspec/changes/archive/` — it is immutable.

## T5. Verification — depends on T1, T2, T3, T4

- [ ] T5.1 `go build ./...` and `go vet ./...` pass repo-wide.
- [ ] T5.2 `gofmt -l` reports no diff for any file this tier touched.
- [ ] T5.3 `make ui-guard` passes — the only new markup is an `Attrs` entry on an
      existing `ui.Input` call, the sanctioned use of that prop; no raw DaisyUI class was
      inlined.
- [ ] T5.4 `make i18n-guard` passes — both new strings resolve through `i18n.T(...)`, no
      hardcoded literal was introduced.
- [ ] T5.5 `make tz-guard` and `make boundary-guard` pass — reproduce `design.md`'s
      "Reverse-Direction Check" reasoning by hand (no raw `time.Now()`, no hardcoded zone,
      no new cross-module import) and confirm it holds; report the result rather than
      assuming it.
- [ ] T5.6 Confirm no file outside `internal/gateway/`, this change's own folder, or
      `kkpa/context/` was touched.
- [ ] T5.7 Report the exact test-suite commands the owner may run to exercise this change
      by hand (`design.md`'s "Test Contract commands" section) — this tier adds no new
      `_test.go` file (roadmap D10), so there is no new automated test to point at;
      `design.md`'s "Rejection Contract" table is what the owner checks manually against
      the running app.
- [ ] T5.8 `openspec validate RM49-gateway-restrict-external-charge-date --strict` passes
      and every `tasks.md` checkbox above reflects real completion.
