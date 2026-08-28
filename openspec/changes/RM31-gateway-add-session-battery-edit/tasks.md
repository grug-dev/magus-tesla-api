# Tasks — RM31-gateway-add-session-battery-edit

Implementation stays inside `internal/gateway/` plus the explicitly granted `kkpa/context/`
path. No task writes `progress.json`; the leader owns that state. Checkboxes are updated live
only when their acceptance is actually met. `depends_on` names task IDs; `parallel_ok` states
whether the task's files are disjoint from its concurrent siblings.

## Wave 1 — independent vocabulary (parallel_ok: yes; disjoint files)

- [x] **1.1** **[module: gateway worker]** `internal/gateway/templates/fragments/supercharger_vm.go`
  — add `ID string` (session UUID), `RawStartBatteryPct string`, `RawEndBatteryPct string` to
  `SuperchargerRowVM` (empty string when the corresponding percentage is nil, matching
  `ChargeEntryVM.RawStartBatteryPct`'s convention exactly); add `CSRFToken string`,
  `WindowStartStr string`, `WindowEndStr string` to `SuperchargerStatsView`. Acceptance: no
  `charging.Session` type or pointer leaks into the VM; doc comments state the raw-string
  emptiness convention. `depends_on`: — · `parallel_ok`: yes, with 1.2 and 1.3.

- [x] **1.2** **[module: gateway worker]** `internal/gateway/i18n/catalog.go` — append the 10 new
  bilingual ES/EN keys per design.md D3/D8/D9/D10 (namespaces `supercharger.actions`,
  `supercharger_row.*` for Edit/Save/Cancel, `supercharger_error.*` for invalid-id,
  session-not-found, malformed-body, start-range, end-range, could-not-save). Acceptance: every
  new key has non-empty `ES` and `EN` on the same map line, following the existing
  constant/map ordering convention; no existing key is modified. `depends_on`: — · `parallel_ok`:
  yes, with 1.1 and 1.3.

- [x] **1.3** **[module: gateway worker]** `internal/gateway/AGENTS.md` — add the
  `Deps.SuperchargerVerifier charging.SessionVerifier` bullet under "Public interface" (mirroring
  the existing `Deps.SuperchargerReader`/`Deps.ChargingWriter` bullets: what calls it, what it may
  never do — no `chargingdb` import); add a new "Exception: Supercharger session battery
  verification" subsection under "Read-only at request time," alongside the existing D4
  (`charging.Writer`) and D-lang (`SetLanguage`) amendments, per design.md D8 — same-account
  scoping via `VerifySession`'s own `WHERE` clause (no separate `RegisteredVehicles` ownership
  check, a deliberate divergence from D4), CSRF via the new `csrf_supercharger` key, and
  `charging.SessionVerifier.VerifySession` as the sole permitted call under this aperture.
  Acceptance: the new subsection follows the D4/D-lang numbered-constraint format; it states the
  divergence from D4's ownership check explicitly rather than silently omitting it. `depends_on`:
  — · `parallel_ok`: yes, with 1.1 and 1.2.

## Wave 2 — templates (depends on the VM shape)

- [x] **2.1** **[module: gateway worker]** NEW `internal/gateway/templates/fragments/supercharger_row.templ`
  — `SuperchargerRow(vm fragments.SuperchargerRowVM, csrfToken string)`: the addressable static
  row, `id="supercharger-row-{vm.ID}"`, all eight existing cells plus a new Actions cell holding
  ONE `ui.Button` Edit control (`hx-get` to the edit route carrying `?start={vm's window}&end=...`
  via the view's `WindowStartStr`/`WindowEndStr` — see 3.1's `superchargerTable` signature change,
  `hx-target="#supercharger-row-{vm.ID}"`, `hx-swap="outerHTML"`). Also add
  `SuperchargerRowError(id, msg string)` mirroring `ChargeRowError`. NO delete button, no
  `hx-confirm`. Acceptance: matches `charge_row.templ`'s structure; no raw DaisyUI class
  (composes `ui.Button` only); no hardcoded user-facing text (every label via `i18n.T`).
  `depends_on`: 1.1, 1.2 · `parallel_ok`: yes, with 2.2 (disjoint files).

- [x] **2.2** **[module: gateway worker]** NEW `internal/gateway/templates/fragments/supercharger_row_edit.templ`
  — `SuperchargerRowEdit(vm fragments.SuperchargerRowVM, csrfToken string, validationErrors
  map[string]string)` per design.md D10: `<tr id="supercharger-row-{vm.ID}"><td colspan="9">`
  containing one `<form hx-patch="/ui/supercharger-stats/row/{vm.ID}?start=...&end=...">` (the
  `hx-patch` verb, on the form, not the Save button — mirrors `ChargeRowEdit`'s documented
  HTML5-validation rationale); a hidden `csrf_token` input; DateLabel/SiteLabel/EnergyLabel/
  CostLabel/StartBatteryPctEstLabel/EndBatteryPctEstLabel as read-only text; TWO
  `ui.Field`+`ui.Input type="number" min="0" max="100" step="1"` controls for
  `RawStartBatteryPct`/`RawEndBatteryPct` (NOT `Required` — empty is valid, D7), each showing its
  `validationErrors[...]` field key; a top-of-form `ui.Alert` for `validationErrors["_top"]`;
  Save (`Type: "submit"`) and Cancel (`hx-get` to the static route with the same window,
  `hx-swap="outerHTML"`) buttons. Acceptance: exactly two editable inputs; no ordering validation
  in markup; column count (`colspan="9"`) matches the static row's total header count after 3.2.
  `depends_on`: 1.1, 1.2 · `parallel_ok`: yes, with 2.1 (disjoint files).

## Wave 3 — presentation wiring and one refactor (single file, coordinate or serialize)

> **Leader note (appended after wave 2 — clarification, no criterion is changed or relaxed).**
> Wave 2 shipped the two components with an extra pair of trailing string parameters, because
> `SuperchargerRowVM` carries no window field (design.md keeps the window on
> `SuperchargerStatsView`, one value per page, not per row):
> `SuperchargerRow(vm, csrfToken, windowStartStr, windowEndStr string)` and
> `SuperchargerRowEdit(vm, csrfToken, windowStartStr, windowEndStr string, validationErrors map[string]string)`.
> Task 3.2's `@SuperchargerRow(s, csrfToken)` shorthand is therefore a **4-argument** call in practice —
> `@SuperchargerRow(s, csrfToken, windowStartStr, windowEndStr)` — which is exactly the "built here or
> inside `SuperchargerRow` — implementer's choice" latitude 3.2 already grants. Same for 3.4's
> `SuperchargerRowEdit(...)` / `SuperchargerRow(...)` render calls. Every other acceptance criterion stands.


- [x] **3.1** **[module: gateway worker]** `internal/gateway/handlers/supercharger.go` — extract
  `superchargerRowVMFromSession(s charging.Session) fragments.SuperchargerRowVM` out of
  `buildSuperchargerRows`'s existing per-session body (design.md D11), now also setting `ID`,
  `RawStartBatteryPct`, `RawEndBatteryPct`; `buildSuperchargerRows` calls it per element with NO
  behavior change to its existing four `"—"`-formatted labels; `buildSuperchargerStatsView` sets
  `v.WindowStartStr`/`v.WindowEndStr` from the already-resolved `start`/`end`
  (`Format("2006-01-02")`) and `v.CSRFToken` from a new `csrfToken string` parameter threaded in
  from `superchargerStatsViewFor` (itself threaded from `SuperchargerStatsPage`/
  `SuperchargerStatsFragment`, wave 4). Acceptance: `superchargerRowVMFromSession` is the ONLY
  place that formats these five fields; existing chart/tile tests remain green under `go vet`.
  `depends_on`: 1.1 · `parallel_ok`: no (same file as 3.2-3.4; serialize within this wave).

- [x] **3.2** **[module: gateway worker]** `internal/gateway/templates/fragments/supercharger_stats.templ`
  — `superchargerTable` signature becomes `superchargerTable(sessions []SuperchargerRowVM,
  csrfToken, windowStartStr, windowEndStr string)`, replacing its inline `<tr>` loop body with a
  call to `@SuperchargerRow(s, csrfToken)` per session (2.1); each row's Edit URL is built here
  (or inside `SuperchargerRow` — implementer's choice, but built exactly once, not duplicated)
  using `windowStartStr`/`windowEndStr`; add the "Actions" header
  (`i18n.T(ctx, i18n.KeySuperchargerActions)`) as the ninth header;
  `SuperchargerStatsContent(v)` passes `v.CSRFToken, v.WindowStartStr, v.WindowEndStr` through.
  Acceptance: header count is 9; no Country column reappears; the table still delegates chart
  rendering to `historyBarChart` unchanged. `depends_on`: 2.1, 3.1 · `parallel_ok`: no (same file
  family as 3.1/3.3/3.4).

- [x] **3.3** **[module: gateway worker]** `internal/gateway/handlers/supercharger.go` — add
  `csrfSuperchargerKey = "csrf_supercharger"` constant; add `fetchSuperchargerRowVM(c
  *gin.Context, uid uuid.UUID, id uuid.UUID) (fragments.SuperchargerRowVM, bool)` per design.md
  D1/D2 (parse `?start=&end=` from the request → `resolveSelectedVehicle` →
  `ListSessionsByVehicleBetween` → match `s.ID == id` → `superchargerRowVMFromSession`); add
  `recalculateAfterSessionVerify(ctx, uid, teslaID int64, chargeStopDateTime time.Time)` exactly
  as specified in design.md D4/D5 (NOT calling or modifying `recalculateAfterChargeWrite`).
  Acceptance: every failure branch inside `fetchSuperchargerRowVM` collapses to `(zero, false)` —
  no error is returned or logged differently per branch (D1's documented non-distinction);
  `recalculateAfterSessionVerify` uses `startOfDay` (already in `history.go`) and
  `chargeStopDateTime.UTC()`, never `browserToday(c)`. `depends_on`: 1.1, 3.1 · `parallel_ok`: no
  (same file as 3.1/3.2/3.4).

- [x] **3.4** **[module: gateway worker]** `internal/gateway/handlers/supercharger.go` — add the
  three route handlers:
  - `SuperchargerRowStatic(c)` — auth guard → parse `id` (400 on bad UUID,
    `KeySuperchargerErrorInvalidID`) → read `csrf_supercharger` from session (do NOT reissue,
    D8) → `fetchSuperchargerRowVM` (404 `KeySuperchargerErrorSessionNotFound` on `false`) →
    `render(c, 200, fragments.SuperchargerRow(vm, csrfToken))`.
  - `SuperchargerRowEditFragment(c)` — identical guard/parse/fetch shape →
    `render(c, 200, fragments.SuperchargerRowEdit(vm, csrfToken, nil))`.
  - `SuperchargerRowUpdate(c)` — auth guard → `checkCSRFKey(c, csrfSuperchargerKey)` → parse `id`
    → `c.GetPostForm("start_battery_pct")`/`c.GetPostForm("end_battery_pct")` (400
    `KeySuperchargerErrorMalformedBody` if either key absent, D3) → per-field `[0,100]`
    integer-or-empty validation (422 with field errors + echoed raw values on failure, D7c/D9) →
    `h.superchargerVerifier.VerifySession(ctx, uid, id, startPct, endPct)` → on error, resolve via
    `fetchSuperchargerRowVM`; 404 if that also fails, else 500 with a `_top` error re-rendering
    the edit form (design.md D9) → on success, branch on `updated.TeslaID` (D6): non-nil calls
    `recalculateAfterSessionVerify`; nil logs and skips → `render(c, 200,
    fragments.SuperchargerRow(superchargerRowVMFromSession(updated), csrfToken))`.
  Acceptance: matches design.md's exact status-code/branch table for every case in the Test
  Contract (T1-T7); no handler calls `RegisteredVehicles` for ownership (D8's documented
  divergence from the `charges.go` D4 write path). `depends_on`: 1.1, 1.2, 3.1, 3.3 ·
  `parallel_ok`: no (same file as 3.1-3.3).

## Wave 4 — Deps/route wiring (depends on the handlers existing)

- [x] **4.1** **[module: gateway worker]** `internal/gateway/handlers/handlers.go` — add
  `SuperchargerVerifier charging.SessionVerifier` to `Deps`, `superchargerVerifier
  charging.SessionVerifier` to `Handler`, and thread it in `New()`, mirroring
  `SuperchargerReader`'s existing three-point wiring exactly; update `SuperchargerStatsPage`'s and
  `SuperchargerStatsFragment`'s bodies (in `supercharger.go`) to issue/read `csrfSuperchargerKey`
  per design.md D8 and thread the token into `superchargerStatsViewFor`/`buildSuperchargerStatsView`
  (3.1's new parameter). Acceptance: `Deps.SuperchargerVerifier` doc comment cross-references
  `internal/gateway/AGENTS.md`'s new amendment (1.3); no other `Deps` field is touched.
  `depends_on`: 1.3, 3.3, 3.4 · `parallel_ok`: no (same file as 4.2 in spirit — coordinate).

- [x] **4.2** **[module: gateway worker]** `internal/gateway/gateway.go` — register
  `r.GET("/ui/supercharger-stats/row/:id", h.SuperchargerRowStatic)`,
  `r.GET("/ui/supercharger-stats/row/:id/edit", h.SuperchargerRowEditFragment)`,
  `r.PATCH("/ui/supercharger-stats/row/:id", h.SuperchargerRowUpdate)`, immediately after the
  existing two Supercharger Stats routes. Acceptance: route order/grouping mirrors the
  `/ui/charges/row/:id[/edit]` block's existing shape; no other route is touched. `depends_on`:
  3.4 · `parallel_ok`: yes, with 4.1 once 3.4 lands (disjoint files: `gateway.go` vs
  `handlers.go`).

- [x] **4.3** **[LEADER TASK — outside `internal/`, NOT this worker]** `cmd/web/main.go` — wire
  `charging.NewSessionVerifier(pool)` into `gateway.Deps.SuperchargerVerifier`, alongside the
  existing `charging.NewSessionReader(pool)` → `Deps.SuperchargerReader` wiring. This sits outside
  the gateway module's sandbox; the leader (or a worker explicitly re-dispatched with a `cmd/`
  grant) performs it, never this module worker. Acceptance: `go build ./...` succeeds
  repo-wide with `Deps.SuperchargerVerifier` populated. `depends_on`: 4.1 · `parallel_ok`: no.
  - _Leader note (additive, RM31 D21):_ satisfying this acceptance criterion also required the
    `gateway.Deps` → `handlers.Deps` passthrough for `SuperchargerVerifier` in
    `internal/gateway/gateway.go`, which neither 4.1 (handlers.go only) nor 4.2 (routes only)
    named. `cmd/web` constructs `gateway.Deps`, so the build cannot go green without it. Done
    by the leader as part of 4.3; mirrors `SuperchargerReader`'s existing passthrough exactly.

## Wave 5 — tests (pure/offline first, per project test-authoring order)

- [ ] **5.1** **[module: gateway worker]** `internal/gateway/handlers/supercharger_test.go` — pure
  tests for `SuperchargerRowUpdate`'s body-parsing and validation branches: design.md T1 (absent
  key → 400, zero `VerifySession` calls), T2 (both present-empty → `VerifySession(nil, nil)`,
  "—" render), T5 (out-of-range → 422, field-specific error, echoed raw values, zero
  `VerifySession` calls), plus an accepted-decreasing-order case (`end < start` succeeds).
  Acceptance: exact status codes and call counts match design.md's Test Contract; fixtures are
  `charging.Session`/fake `SessionVerifier`, no real DB. `depends_on`: 3.4 · `parallel_ok`: yes,
  with 5.2 and 5.3 (same file — coordinate additions, no shared helper conflicts expected since
  each covers disjoint scenarios).

- [ ] **5.2** **[module: gateway worker]** `internal/gateway/handlers/supercharger_test.go` — pure
  tests for the recalculation window: design.md T3 (just-after-UTC-midnight session →
  `Recalculate(day-1, day+1)` computed from `ChargeStopDateTime` alone, ignoring a differently-
  dated `ChargeStartDateTime`) and T4 (nil `TeslaID` → zero `Recalculate` calls, save still
  succeeds, a log line is emitted). Acceptance: exact `start`/`end` values asserted per T3; fake
  `analytics.Recalculator` call count asserted per T4. `depends_on`: 3.3, 3.4 · `parallel_ok`:
  yes, with 5.1 and 5.3.

- [ ] **5.3** **[module: gateway worker]** `internal/gateway/handlers/supercharger_test.go` — pure
  tests for CSRF and not-found resolution: design.md T6 (no token ever issued → 403, zero
  `VerifySession` calls) and T7 (a `GET .../row/:id/edit` whose window excludes `id` → 404).
  Acceptance: matches design.md's Test Contract exactly; also assert a stale/mismatched token
  (distinct fixture from T6's never-issued case) is rejected the same way. `depends_on`: 3.3, 3.4
  · `parallel_ok`: yes, with 5.1 and 5.2.

- [ ] **5.4** **[module: gateway worker — codegen]** Run `make templ` after the wave-2/3 `.templ`
  changes and include the regenerated `supercharger_row_templ.go`,
  `supercharger_row_edit_templ.go`, and `supercharger_stats_templ.go`. Run `make css` only if a
  new Tailwind/DaisyUI class was introduced (none is designed here — confirm against design.md
  before running). Acceptance: generated artifacts are not hand-edited; `go build ./...` and
  `go vet ./...` succeed. `depends_on`: 2.1, 2.2, 3.2, 4.1, 4.2 · `parallel_ok`: no (must run
  after every template/handler edit in this change).

## Wave 6 — KB update (ticket item 6; granted `kkpa/context/` path, independent of waves 1-5)

- [ ] **6.1** **[module: gateway worker — KB]** `kkpa/context/workflows/supercharger-stats-read.md`
  — add the write path (the three new routes, `SessionVerifier`, the strict-body/clear/validation
  rules) and the recalculation hop (`recalculateAfterSessionVerify`, the `±1 day` window and why)
  to the "How maintenance works" and "Conventions & gotchas" sections; update the file's own
  "Create / Update / Delete a session: NO user-facing path exists" line, since a write path now
  exists for the two battery percentages (still no Create or Delete). Do NOT rename the file —
  state in the guide body that it now carries a write path (project rule). Acceptance: the guide
  accurately reflects the new routes/handlers/i18n keys added by this tier; no stale "NO
  user-facing path" claim remains. `depends_on`: — (documents the design, not the merged code;
  may be written any time after this design.md exists) · `parallel_ok`: yes, with 6.2.

- [ ] **6.2** **[module: gateway worker — KB]** `kkpa/context/workflows/manual-charge-crud.md` —
  correct its "Related KB" line that currently calls `supercharger-stats-read.md` "the contrast
  case: a read-only page with NO user write path" (no longer accurate); update the counterpart
  line in `supercharger-stats-read.md`'s own "Related KB" section similarly. Acceptance: neither
  file claims the other is write-path-free when it now has one. `depends_on`: — · `parallel_ok`:
  yes, with 6.1 (disjoint files, but keep the two "Related KB" edits consistent with each other).

- [ ] **6.3** **[module: gateway worker — KB]** `kkpa/context/INDEX.md` — add/update the rows for
  `supercharger-stats-read.md` and `manual-charge-crud.md` reflecting the corrected scope.
  Acceptance: INDEX rows resolve to the same glossary terms already used in each guide; no
  `kkpa/context/pending-spec-to-sync/` file is touched (owner's human-gated step, out of scope
  for this worker). `depends_on`: 6.1, 6.2 · `parallel_ok`: no.

## Wave 7 — artifact re-check and owner verification handoff

- [ ] **7.1** **[module: gateway worker]** Re-read the implemented diff against design.md D1-D11
  and `specs/gateway/spec.md`, then update only the completed task checkboxes in this file.
  Acceptance: no code outside `internal/gateway/` (except the KB paths in wave 6) has entered the
  change; no delete route, no ordering validation, no new `charging` port, no database object,
  no `pgx` import in the gateway, and `recalculateAfterChargeWrite` is byte-for-byte unchanged.
  `depends_on`: 5.1, 5.2, 5.3, 5.4, 6.3 · `parallel_ok`: no.

- [ ] **7.2** **[owner verification]** Run `go test ./internal/gateway/...`. Acceptance: report
  the result to the leader; until then the work remains awaiting-user-verification, not done.
  `depends_on`: 7.1 · `parallel_ok`: no.
