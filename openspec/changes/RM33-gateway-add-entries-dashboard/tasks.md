# Tasks — RM33-gateway-add-entries-dashboard

Ownership legend: **[module: gateway worker]** — inside `internal/gateway/` only. **[owner]** —
the human. See design.md for the rationale behind each decision; this file only decomposes it
into dispatchable, dependency-ordered work.

> No design gate applies (no database object is touched — `CLAUDE.md` §Pipeline config →
> `Design-Gates: database` is scoped to DB-touching changes only). Implementation may start once
> this change is proposed; there is no owner-confirmation checkpoint to wait on before Wave 1.

## Two interface contracts frozen by design.md — read before dispatching Wave 4/5 in parallel

design.md gives VERBATIM signatures/bodies for `parseChargesRange`, `endOfMonth`,
`buildChargesPresets`, `entryComplete`, `buildChargeTiles`, `ui.DotProps`/`templ Dot`, and
`ChargeRowUpdateSuccessOOB`. Because these are fixed in the artifact rather than left to a
worker's judgement, **Wave 4 (templates) and Wave 5 (handlers) both consume the SAME frozen
signatures independently** — this is why Wave 5 does not need to depend on Wave 4 function-by-
function, only on Wave 4 being *complete* (see Wave 5's `depends_on`). Any worker who finds a
frozen signature insufficient must flag it rather than silently diverging rather than guessing.
Three such flags were raised while this file was written and are already resolved — see "Leader
resolutions" at the end of this file before touching `buildChargesPage` or the window helpers.

## Ordering constraints (summary — see each wave for detail)

- **Wave 1 is foundational, three disjoint files, no dependencies** — `ui/dot.templ`,
  `i18n/catalog.go` (additions only), `fragments/charges_vm.go`. All three may run in parallel.
- **Wave 2 (two new pure-function files) depends on the specific Wave 1 piece each one
  references** — `charges_range.go` needs the i18n keys `buildChargesPresets` calls;
  `charges_tiles.go` needs the `ChargeTiles` struct `buildChargeTiles` returns. The two files are
  disjoint and may run in parallel.
- **Wave 3 (TDD-style tests for Wave 2, Test Contract Groups A & B) depends on its own Wave 2
  file only** — two disjoint test files, parallel.
- **Wave 4 (the four `.templ` files) depends on Wave 1 only** (the VM struct + i18n keys are the
  only compile-time inputs a template needs — it never calls a handler-side Go function). All
  four files are disjoint and may run in parallel.
- **Wave 5 (`handlers/charges.go`) depends on ALL of Wave 4 being complete, plus Wave 2** —
  unlike the templates, the handler file both calls INTO the new/changed template functions
  (`fragments.ChargeRow` with its new 4-arg signature, the brand-new
  `fragments.ChargeRowUpdateSuccessOOB`, `fragments.ChargeRowEdit` with its new window params)
  AND supplies the `ChargesPageData`/`ChargeEntryVM` those templates render — a true two-way
  compile dependency once signatures change, unlike tier 2 where template signatures stayed
  fixed. Sub-tasks 5.1–5.7 are all in the SAME file — do them in one sequential pass, not as
  separately dispatchable parallel tasks.
- **Wave 6 (dead-code + i18n-key cleanup) depends on Wave 4 (charges_list.templ /
  charge_row.templ having actually stopped referencing the removed keys/functions) AND Wave 5
  (charges.go having stopped calling `ChargeRowEmpty`/`ChargeRowError`)** — mirrors tier 2's
  deferred-deletion pattern (its task 4.3) exactly: delete only after the grep comes back empty.
- **Wave 7 (AGENTS.md doc update for the new `ui.Dot` primitive) depends on Wave 1 (`ui.Dot`
  existing) only** — may run any time after Wave 1, in parallel with Waves 2–6.
- **Wave 8 (codegen) depends on every wave that touches a `.templ` file — Wave 4 AND Wave 6**
  (Wave 6 edits `charge_row.templ` again, after Wave 4 already generated once; codegen must run
  AFTER the dead code is actually removed, not before).
- **Wave 9 (tests, Test Contract Groups C/D/E + the four REWRITE items) depends on Wave 8
  (compiled templates) and Wave 5 (finished handlers)** — this is the tier's only
  `httptest`/rendered-HTML wave, per the dispatch's placement rule. All sub-tasks land in the
  SAME file (`charges_test.go`) — sequence carefully or merge by hand; marked `parallel_ok` only
  in the same sense tier 2's Wave 8 was (additive functions, same file, real merge risk accepted
  as tier 2's precedent already did).
- **Wave 10 (signals) depends on Wave 9.**

---

## Wave 1 — foundation (ui kit primitive + i18n additions + VM struct)

- [ ] **1.1** **[module: gateway worker]** `internal/gateway/templates/ui/dot.templ` (new file) —
  add `DotProps{Variant, Tooltip, Class string}` and `templ Dot(p DotProps)` **verbatim** from
  design.md §D-Dot:
  ```go
  type DotProps struct {
      Variant string // "success"|"warning"|"error"|"neutral"
      Tooltip string // native title attribute; empty renders no tooltip
      Class   string
  }

  templ Dot(p DotProps) {
      <span
          class={ "inline-block w-2.5 h-2.5 rounded-full", dotClass(p.Variant), p.Class }
          if p.Tooltip != "" {
              title={ p.Tooltip }
          }
      ></span>
  }
  ```
  Add a `dotClass(variant string) string` helper in a companion `dot.go` (same package,
  `templates/ui`), mirroring `badgeClass`'s exact lookup shape in `ui/badge.templ`/its companion
  `.go` file (`bg-success`/`bg-warning`/`bg-error`/`bg-neutral` DaisyUI semantic tokens — never
  hex). This is a NEW `ui/` primitive per `CLAUDE.md`'s closed-vocabulary rule — see Wave 7 for
  the required `AGENTS.md` documentation update.
  `depends_on`: none · `parallel_ok`: with 1.2, 1.3

- [ ] **1.2** **[module: gateway worker]** `internal/gateway/i18n/catalog.go` — **ADD** keys only
  (deletions are deferred to Wave 6, mirroring tier 2's task 1.2/4.3 pattern — do not delete
  `KeyChargesListRefresh`/`KeyChargesListHeaderVehicle` here, their call sites are still live
  until Wave 4 lands):
  - Two filter-preset labels (D-Presets): suggested `KeyChargesRangeLast7Days`
    (`charges_range.last_7_days`), `KeyChargesRangeThisMonth` (`charges_range.this_month`). ES
    "Últimos 7 días" / "Este mes"; EN "Last 7 days" / "This month".
  - Four tile labels (D-Tiles), following the "table/tile header is its own semantic role, not a
    reuse" precedent already documented above `KeyChargesListHeaderDate` — do NOT reuse
    `KeySupercharger{Sessions,Energy,Cost,AvgKWhSession}`: suggested `KeyChargesTileSessions`,
    `KeyChargesTileEnergy`, `KeyChargesTileCost`, `KeyChargesTileAvgKWh`
    (`charges_tile.{sessions,energy,cost,avg_kwh_session}`). ES "Sesiones"/"Energía"/"Costo"/"kWh
    prom. / sesión"; EN "Sessions"/"Energy"/"Cost"/"Avg kWh / session".
  - Two new table headers (D9/D11): suggested `KeyChargesListHeaderStatus`
    (`charges_list.header_status`, ES "Estado" / EN "Status") and
    `KeyChargesListHeaderBatteryRange` (`charges_list.header_battery_range`, ES "Rango de
    batería" / EN "Battery range").
  - Two status-badge labels (D9), distinct from the FORM's existing
    `KeyChargesFormStatusInProgress`/`KeyChargesFormStatusDone` per the same table/form
    semantic-role precedent: suggested `KeyChargesBadgeInProgress`
    (`charges_badge.in_progress`), `KeyChargesBadgeDone` (`charges_badge.done`). Same English
    words as the form option text are fine — the precedent is about the KEY, not the copy.
  - Two completeness-dot tooltips (D-Dot): suggested `KeyChargesDotCompleteTooltip`
    (`charges_dot.complete_tooltip`, ES "Completo" / EN "Complete") and
    `KeyChargesDotIncompleteTooltip` (`charges_dot.incomplete_tooltip`, ES "Incompleto" / EN
    "Incomplete").
  - **CHANGE** `KeyChargesListHeaderBattery`'s `ES`/`EN` values (key unchanged, still used for
    the DELTA column) from the current generic "Carga de batería"/"Battery Charge" to a
    delta-specific label now that a separate Range header exists alongside it — suggested ES "Δ
    Batería" / EN "Battery Δ".
  Every new/changed entry keeps `ES`/`EN` on the SAME catalogue-map line (D1 convention).
  `depends_on`: none · `parallel_ok`: with 1.1, 1.3

- [ ] **1.3** **[module: gateway worker]** `internal/gateway/templates/fragments/charges_vm.go`:
  - Add `Complete bool` and `BatteryRange string` to `ChargeEntryVM`, each with a doc comment
    cross-referencing design.md §D-Dot / the battery-range requirement — `Complete` is
    handler-computed via `entryComplete(e)` (Wave 2), never computed in the template;
    `BatteryRange` is the pre-formatted `"22% → 70%"` string, or `"—"` when either battery
    percentage is absent (D14).
  - Add a new `ChargeTiles` struct (design.md §D-Tiles) — `Sessions`, `Energy`, `Cost`, `AvgKWh`,
    all pre-formatted strings (mirror `SuperchargerTiles`'s doc-comment style in
    `supercharger_vm.go`, e.g. `Sessions string // e.g. "14"`).
  - Add to `ChargesPageData`: `Presets []RangePreset` (nil on a malformed window or no resolved
    vehicle — no selector rendered, mirrors `SuperchargerStatsView.Presets`'s exact doc comment
    shape), `Tiles ChargeTiles`, `WindowStartStr string`, `WindowEndStr string` (pre-formatted
    `YYYY-MM-DD`, rendered into the `#charges-window-start`/`#charges-window-end` hidden inputs
    per design.md §D-Include), and `NoFilterChrome bool` (design.md §D-Empty state 1 — no
    vehicle resolved OR malformed window: hide presets, tiles, AND table, render only
    `ChargesEmptyState()`).
  - Update the doc comments on `CostPerKWhLabel`, `BatteryDelta`, `DurationLabel` to say `"—"` on
    empty, not `""` (D14 extends the em-dash convention already applied to `EnergyKWh`).
  `depends_on`: none · `parallel_ok`: with 1.1, 1.2

---

## Wave 2 — pure handler functions (new files, offline, no gin/DB dependency beyond `*gin.Context`)

- [ ] **2.1** **[module: gateway worker]** `internal/gateway/handlers/charges_range.go` (new
  file) — add, **verbatim from design.md §D-Range/§D-Presets**:
  - `const chargesRangeDefaultDays = 7`, `const chargesRangeMaxDays = 400`.
  - `func parseChargesRange(c *gin.Context, today time.Time) (start, end time.Time, ok bool)` —
    both absent → 7-day default ending `today`; either alone → reject; `end.Before(start)` →
    reject; window wider than `chargesRangeMaxDays` → reject; **no future-`end` rejection** (the
    deliberate divergence from `parseHistoryRange`/`parseSuperchargerRange` — do not add one).
  - `func endOfMonth(t time.Time) time.Time` — the one new date-math primitive this tier adds.
    Reuse the EXISTING unexported `startOfMonth` from `supercharger.go:57` (same `package
    handlers`) — do not duplicate it.
  - `func buildChargesPresets(ctx context.Context, start, end, today time.Time)
    []fragments.RangePreset` — two presets ("last 7 days", "this month"), `Active` computed by
    exact-match against each preset's own recomputed window. Reuses `fragments.RangePreset`
    unchanged (no third preset type).
  `depends_on`: 1.2 (i18n keys `KeyChargesRangeLast7Days`/`KeyChargesRangeThisMonth`) ·
  `parallel_ok`: with 2.2

- [ ] **2.2** **[module: gateway worker]** `internal/gateway/handlers/charges_tiles.go` (new
  file) — add, **verbatim from design.md §D-Dot/§D-Tiles**:
  - `func entryComplete(e charging.Entry) bool` — `EndedAt != nil && EndBatteryPct != nil &&
    EnergyAddedKWh != nil && Price > 0`. Evaluated identically regardless of `e.Status`; do NOT
    special-case `IN_PROGRESS`.
  - `func buildChargeTiles(entries []charging.Entry) fragments.ChargeTiles` — sums `Price`
    unconditionally (COP by construction, D13 — no per-currency map like Supercharger's
    `CostLines`), sums `EnergyAddedKWh` only over non-nil entries, `AvgKWh` division-guarded to
    `"—"` on zero count. Reuses `formatMoney` (`handlers/format.go:39`) for the Cost string — do
    not hand-format currency.
  `depends_on`: 1.3 (`fragments.ChargeTiles` struct) · `parallel_ok`: with 2.1

---

## Wave 3 — offline tests for Wave 2 (Test Contract Groups A & B, TDD-style)

- [ ] **3.1** **[module: gateway worker]** `internal/gateway/handlers/charges_range_test.go`
  (new file) — Test Contract **A1–A8** verbatim (design.md §Test Contract Group A):
  - A1: both absent, `today = 2026-08-29` → `start = 2026-08-23`, `end = 2026-08-29`, `ok`.
  - A2: an explicit non-default valid window → `ok`.
  - A3: `?end=2026-09-15` with `today = 2026-08-29` (future `end`) → `ok == true` — the explicit
    divergence-from-history/supercharger assertion; do not skip this one.
  - A4: partial pair (`start` only) → `ok == false`.
  - A5: `end` before `start` → `ok == false`.
  - A6: window wider than 400 days → `ok == false`; the same window narrowed to exactly 400 days
    → `ok == true`.
  - A7: `buildChargesPresets` with `today = 2026-08-15` → "This month" `EndStr == "2026-08-31"`
    (the LAST day, not `"2026-08-15"`), `StartStr == "2026-08-01"`.
  - A8: `buildChargesPresets` called with the "last 7 days" window → that preset `Active ==
    true`, "this month" `Active == false`; called with a custom window → both `Active == false`.
  `depends_on`: 2.1 · `parallel_ok`: with 3.2

- [ ] **3.2** **[module: gateway worker]** `internal/gateway/handlers/charges_tiles_test.go`
  (new file) — Test Contract **B1–B5** verbatim (design.md §Test Contract Group B):
  - B1: all four completeness inputs present, `Price > 0` → `entryComplete == true`.
  - B2: four variants, each missing exactly ONE of the four inputs → `entryComplete == false` in
    all four cases.
  - B3: a normally-shaped `IN_PROGRESS` entry (`EndedAt == nil`) → `entryComplete == false` — the
    "predicate does not special-case status" assertion.
  - B4: `buildChargeTiles` over `[]charging.Entry{}` → `Sessions == "0"`, `Energy == "0.0 kWh"`,
    `Cost == "0.00 COP"`, `AvgKWh == "—"` (assert per-field, not just non-panic).
  - B5: three entries (one nil-energy) → `Sessions == "3"`, `Energy == "15.0 kWh"` (nil-skip),
    `Cost == "1500.00 COP"` (summed regardless of nil energy), `AvgKWh == "7.5 kWh"` (divided by
    the non-nil-energy COUNT, not the entry count).
  `depends_on`: 2.2 · `parallel_ok`: with 3.1

---

## Wave 4 — templates (four disjoint files, parallel; all depend on Wave 1 only)

- [ ] **4.1** **[module: gateway worker]** `internal/gateway/templates/fragments/charges_list.templ`:
  - **Remove** the Refresh button entirely (D9 proposal — subsumed by any filter click).
  - **Add** a `chargesRangeSelector(presets []RangePreset)` component, structurally identical to
    `superchargerMonthsSelector` (`supercharger_stats.templ`) — `ui.Join` wrapping one
    `ui.Button` per preset, `hx-get` built from `p.StartStr`/`p.EndStr` against
    `/ui/charges/list`, `hx-target="#charges-list"`, `hx-swap="outerHTML"`, `Variant: "primary"`
    when `p.Active` else `"ghost"`. Render it only `if d.Presets != nil` (mirrors
    `SuperchargerStatsContent`'s `if v.Presets != nil` guard — nil on `NoFilterChrome`).
  - **Add** a `chargeTiles(t ChargeTiles)` component, structurally identical to
    `superchargerTiles` — four `ui.StatTile`s in a `grid grid-cols-2 md:grid-cols-4 gap-3`, using
    the four new i18n tile-label keys from 1.2. Render inside its own `ui.Card`, same layout
    pattern as `SuperchargerStatsContent`'s tiles card.
  - **Add** the two stable-id hidden inputs from design.md §D-Include, rendered ONCE inside
    `#charges-list` (so they are always fresh when the filter buttons refresh this region):
    ```templ
    <input type="hidden" id="charges-window-start" value={ d.WindowStartStr }/>
    <input type="hidden" id="charges-window-end" value={ d.WindowEndStr }/>
    ```
  - **Implement the three-state D-Empty branching**: `if d.NoFilterChrome` → render ONLY
    `ChargesEmptyState()`, no selector, no tiles, no table. Else → render the selector + tiles
    always, then `if d.Error != ""` show the existing `ui.Alert`; `if d.EmptyState` (existing
    field, zero rows) → table body replaced by `ChargesEmptyState()`; else → the table.
  - **Update `ui.Table` Headers** to the 9-column order from the proposal — `Date, Status,
    Energy, Price, Cost/kWh, Battery Range, Battery Δ, Duration, Actions` — using
    `KeyChargesListHeaderStatus`/`KeyChargesListHeaderBatteryRange` (new) and dropping
    `KeyChargesListHeaderVehicle` (its removal from the catalogue is deferred to Wave 6 — just
    stop referencing it here).
  - **Update every `ChargeRow(vm, d.CSRFToken)` call site** to the new 4-arg signature Wave 4.2
    defines: `ChargeRow(vm, d.CSRFToken, d.WindowStartStr, d.WindowEndStr)`.
  - Run `make templ` deferred to Wave 8 — do not run it per-task.
  `depends_on`: 1.2, 1.3 · `parallel_ok`: with 4.2, 4.3, 4.4

- [ ] **4.2** **[module: gateway worker]** `internal/gateway/templates/fragments/charge_row.templ`:
  - Change `ChargeRow`'s signature to `templ ChargeRow(vm ChargeEntryVM, csrfToken,
    windowStartStr, windowEndStr string)`.
  - **Remove** the Vehicle `<td>{ vm.VehicleLabel }</td>` cell.
  - **Add** a Status `<td>` (2nd column, right after the date) rendering BOTH signals (D9): the
    completeness dot —
    `if vm.Complete { @ui.Dot(ui.DotProps{Variant: "success", Tooltip:
    i18n.T(ctx, i18n.KeyChargesDotCompleteTooltip)}) } else { @ui.Dot(ui.DotProps{Variant:
    "warning", Tooltip: i18n.T(ctx, i18n.KeyChargesDotIncompleteTooltip)}) }` — and the status
    badge, keyed off `vm.RawStatus`: `ui.Badge{Kind: "primary", Text:
    i18n.T(ctx, i18n.KeyChargesBadgeDone)}` for `"DONE"`, `ui.Badge{Kind: "ghost", Text:
    i18n.T(ctx, i18n.KeyChargesBadgeInProgress)}` for `"IN_PROGRESS"` — deliberately NOT
    `"success"`/`"warning"` Kinds on the badge (design.md §D-Dot: the badge's colour vocabulary
    must never collide with the dot's).
  - **Add** a Battery Range `<td>{ vm.BatteryRange }</td>` immediately before the existing
    Battery Delta `<td>{ vm.BatteryDelta }</td>` (D11 — both columns, range then delta, per the
    proposal's stated column order).
  - **Thread the window** into both action buttons' URLs: Edit's `hx-get` becomes
    `"/ui/charges/row/" + vm.ID + "/edit?start=" + windowStartStr + "&end=" + windowEndStr`;
    Delete's `hx-delete` becomes `"/ui/charges/row/" + vm.ID + "?start=" + windowStartStr +
    "&end=" + windowEndStr` (design.md §D-Refresh — "on the row's own Delete URL").
  - **Change Delete's `hx-target`** from `"#charge-row-" + vm.ID` to `"#charges-list"` (D-Refresh
    — whole-region re-render, matching the filter buttons' own target).
  - **Add** `templ ChargeRowUpdateSuccessOOB(vm ChargeEntryVM, csrfToken, windowStartStr,
    windowEndStr string, list ChargesPageData)` in this same file, **verbatim from design.md
    §D-Refresh**:
    ```templ
    templ ChargeRowUpdateSuccessOOB(vm ChargeEntryVM, csrfToken, windowStartStr, windowEndStr string, list ChargesPageData) {
        @ChargeRow(vm, csrfToken, windowStartStr, windowEndStr)
        <div id="charges-list" hx-swap-oob="outerHTML:#charges-list">
            @ChargesList(list)
        </div>
    }
    ```
  - **Do NOT delete `ChargeRowEmpty`/`ChargeRowError` in this task** — `charges.go`'s Wave 5
    still calls them until Wave 5.6 rewrites `ChargeRowDelete`; deletion is Wave 6.1.
  `depends_on`: 1.1, 1.2, 1.3 · `parallel_ok`: with 4.1, 4.3, 4.4

- [ ] **4.3** **[module: gateway worker]** `internal/gateway/templates/fragments/charge_row_edit.templ`:
  - **`<td colspan="8">` → `<td colspan="9">`** (design.md §D-Colspan — the deferred fix from
    tier 2's own design.md, now that the table is 9 columns: Vehicle removed, Status + Battery
    Range added, net +1).
  - Change `ChargeRowEdit`'s signature to accept `windowStartStr, windowEndStr string` (in
    addition to its existing params) and render them as two hidden inputs inside the row's edit
    `<form>` — `<input type="hidden" name="start" value={ windowStartStr }/>` /
    `name="end"` — mirroring how `csrf_token` is already a hidden input in this same form
    (design.md §D-Refresh: "plain hidden inputs for the edit row" — the PUT method DOES parse
    request bodies, unlike DELETE, so this is a valid wire path here, unlike the DELETE
    button's CSRF-header workaround).
  `depends_on`: 1.3 · `parallel_ok`: with 4.1, 4.2, 4.4

- [ ] **4.4** **[module: gateway worker]** `internal/gateway/templates/fragments/charge_create_form.templ`:
  - Add `hx-include="#charges-window-start, #charges-window-end"` to the `<form>` element
    (design.md §D-Include). This is a plain htmx attribute — it does NOT touch the module's
    "no client-side JS" rule (`ai/htmx-conventions.md` §Styling) and needs NO new RD entry in
    `AGENTS.md` (design.md is explicit on this point — do not add one).
  - No other change to this file in this tier (the two charge FORMS are tier 2's territory,
    already archived — this is the one addition the roadmap grants tier 3).
  `depends_on`: none (the referenced element ids are a Wave 4.1 runtime/DOM dependency, not a Go
  compile dependency) · `parallel_ok`: with 4.1, 4.2, 4.3

---

## Wave 5 — handler rewire (`internal/gateway/handlers/charges.go`, single file, sequential)

- [ ] **5.1** **[module: gateway worker]** `buildChargesPage` — the frozen signature is
  **7 args**: `(ctx, uid, csrfToken string, teslaIDFilter int64, today, start, end time.Time)`.
  `today` is RETAINED alongside the new `start, end` — they are different concepts and neither
  substitutes for the other: `today` is the BROWSER's calendar day and drives the create form's
  D1 date defaults (`day`/`todayDate`) and the D2 battery suggestion, while `start/end` is the
  LIST's filter window. Deriving the form's default date from `end` would make the "This month"
  preset pre-fill the create form with the last day of the month. Per design.md
  §D-Range/§D-RM33-9/§D-Empty:
  - Switch the vehicle-scoped read from `ListEntriesByVehicle(ctx, uid, teslaIDFilter,
    defaultChargeLimit)` to `ListEntriesByVehicleBetween(ctx, uid, teslaIDFilter, start, end)` —
    no limit parameter, the window itself bounds the result.
  - **D-RM33-9**: when `teslaIDFilter == 0` (no vehicle resolved), do NOT call
    `ListEntriesByAccount` at all — set `NoFilterChrome = true` and return early with just
    `ChargesPageData{CSRFToken: csrfToken, EmptyState: true, NoFilterChrome: true}` (no read of
    any kind against `charging.Reader`).
  - Compute `d.Presets = buildChargesPresets(ctx, start, end, today)` and `d.WindowStartStr =
    start.Format("2006-01-02")` / `d.WindowEndStr = end.Format(...)` on every non-`NoFilterChrome`
    path.
  - Compute `d.Tiles = buildChargeTiles(entries)` over the SAME `entries` slice the VM-mapping
    loop below it consumes — before that loop, so both are provably the same data (D13).
  - Remove the now-unused `defaultChargeLimit` constant if nothing else references it after this
    change (grep first — `fetchEntryVM`/`fetchEntryTeslaIDAndChargedOn` may still use
    `ListEntriesByAccount` with a `0`/different limit; do not remove if still referenced).
  `depends_on`: 4.1, 4.2, 4.3, 4.4 (Wave 4 complete), 2.1, 2.2 · `parallel_ok`: no (same file as
  5.2–5.7)

- [ ] **5.2** **[module: gateway worker]** `chargeEntryVMFromEntry` — add:
  - `vm.Complete = entryComplete(e)`.
  - `vm.BatteryRange`: `"22% → 70%"`-shaped when both `StartBatteryPct`/`EndBatteryPct` are
    present, else `"—"` (D11/D14).
  - Extend the `"—"` empty-placeholder convention (already applied to `EnergyKWh`) to
    `CostPerKWhLabel`, `BatteryDelta`, `DurationLabel` — every currently-blank-string fallback in
    this function becomes `"—"` (D14).
  Also add the three window helpers **verbatim from design.md §D-Include**: the shared
  `bestEffortWindow(rawStart, rawEnd string, today time.Time) (start, end time.Time)` and its two
  one-line callers `windowFromForm` (`c.PostForm`) and `windowFromQuery` (`c.Query`). Both are
  best-effort and NEVER a validation gate — a malformed window must never turn a successful write
  into an error. Do NOT reach for `parseChargesRange` here: that one deliberately REJECTS a
  malformed window (D-Range), which is right for the user-driven filter route and wrong for a
  post-write refresh.
  `depends_on`: 5.1 · `parallel_ok`: no

- [ ] **5.3** **[module: gateway worker]** `ChargePage` / `ChargesListFragment`:
  - `ChargePage` (full-page `GET /charges`) keeps using the DEFAULT 7-day window only — it does
    NOT parse `?start=&end=` itself (spec.md's date-filter contract is scoped to `GET
    /ui/charges/list`; a full page reload has no reason to remember a prior filter click). Call
    `buildChargesPage(ctx, uid, csrfToken, filterTeslaID, today, start, end)` with the default
    window (`start = today.AddDate(0,0,-(chargesRangeDefaultDays-1))`, `end = today`) — obtained
    from a small local default-window helper, not by duplicating the `chargesRangeDefaultDays`
    math inline. Note the argument ORDER: `today` stays in its existing 5th position and the
    window follows it (5.1).
  - `ChargesListFragment` (`GET /ui/charges/list`) calls `parseChargesRange(c, today)`; on `ok ==
    false`, render the SAME `NoFilterChrome` empty-state fragment at HTTP 400 (via
    `renderFragmentError`, `"charges-list"`) with NO read against `charging.Reader` — mirrors the
    platform's own "on 400, render the empty-state placeholder... and return no preset selector"
    convention (`internal/gateway/AGENTS.md` §"HTTP date-filter convention").
  `depends_on`: 5.2 · `parallel_ok`: no

- [ ] **5.4** **[module: gateway worker]** `ChargeCreate`:
  - **Success path only**: resolve the OOB refresh window via `windowFromForm(c, browserToday(c))`
    (design.md §D-Include) and pass it into the `buildChargesPage` call feeding
    `fragments.ChargeCreateSuccessOOB(d)`.
  - **422/500 error paths**: unchanged in spirit — they never render `#charges-list` (only
    `ChargeCreateForm`), so keep them on the plain default window; do NOT thread
    `windowFromForm` into these branches (no observable effect, avoids needless complexity).
  - Every `buildChargesPage(...)` call site in this function updates to the new 7-arg signature
    from 5.1 (Go's compiler enforces every call site is fixed).
  `depends_on`: 5.2 · `parallel_ok`: no

- [ ] **5.5** **[module: gateway worker]** `ChargeRowUpdate`:
  - Parse the hidden `start`/`end` inputs (added in 4.3) via `c.PostForm("start")`/`"end"` —
    reuse `windowFromForm`'s exact fallback shape (best-effort, default on malformed/absent).
  - On success, render `fragments.ChargeRowUpdateSuccessOOB(vm, csrfToken, windowStartStr,
    windowEndStr, list)` where `list = h.buildChargesPage(ctx, uid, csrfToken, updated.TeslaID,
    today, start, end)` — the SAME `buildChargesPage` path every other list render uses (design.md
    §D-Refresh: "no new render function").
  - The validation-ERROR path (422/500) is UNCHANGED — still `fragments.ChargeRowEdit(...)` only,
    still no `#charges-list` touch (a rejected save does not change the tiles). Update its
    `ChargeRowEdit` call site for the new `windowStartStr, windowEndStr` params (echo back
    whatever was posted, via the same best-effort parse).
  `depends_on`: 5.2 · `parallel_ok`: no

- [ ] **5.6** **[module: gateway worker]** `ChargeRowDelete` — rewrite per design.md §D-Refresh
  **verbatim structure**:
  ```go
  func (h *Handler) ChargeRowDelete(c *gin.Context) {
      // ...auth, CSRF, id-parse unchanged...
      today := browserToday(c)
      start, end := windowFromQuery(c, today) // ?start=&end= on the row's own Delete URL

      entryTeslaID, entryChargedOn, hadEntry := h.fetchEntryTeslaIDAndChargedOn(...)
      err := h.chargingWriter.Delete(c.Request.Context(), uid, id)
      d := h.buildChargesPage(ctx, uid, csrfToken, teslaIDFilter, today, start, end)
      if err != nil {
          log.Printf(...)
          d.Error = i18n.T(ctx, i18n.KeyChargesErrorCouldNotDeleteEntry)
          renderFragmentError(c, http.StatusInternalServerError, pages.ChargePage(d), "charges-list")
          return
      }
      if hadEntry {
          h.recalculateAfterChargeWrite(...)
      }
      renderFragment(c, http.StatusOK, pages.ChargePage(d), "charges-list")
  }
  ```
  The snippet above is the CORRECTED one (leader resolution L1, "Leader resolutions" below):
  design.md's original draft carried a stray trailing `true` on this call that named no
  parameter anywhere in the document; it is a snippet typo and design.md has been corrected.
  The signature is 5.1's frozen 7 args — no trailing bool. Resolve `teslaIDFilter` for this call
  the same way `ChargePage`/`ChargesListFragment` do (`resolveSelectedVehicle`).
  Use `pages.ChargePage(d)` + `renderFragment(..., "charges-list")` / `renderFragmentError(...,
  "charges-list")` exactly as shown — this reuses the SAME render machinery every other list
  render already uses (no new render function).
  `depends_on`: 5.2 · `parallel_ok`: no

- [ ] **5.7** **[module: gateway worker]** `ChargeRowStatic` / `ChargeRowEditFragment` — thread
  the window through the two remaining row routes so `ChargeRow`'s new required
  `windowStartStr`/`windowEndStr` params and `ChargeRowEdit`'s new hidden-input params always
  have a real value, not an empty string that would silently reset a user's filter on Cancel:
  - Both handlers read `?start=&end=` from `c.Query(...)` via `windowFromQuery(c,
    browserToday(c))` (best-effort, defaults on absent/malformed). Leader resolution L3
    ("Leader resolutions" below): this is IN scope and is compile-forced — `ChargeRow`'s new
    mandatory `windowStartStr`/`windowEndStr` params make every call site supply a window, and
    passing `""` here would silently reset the user's filter on Cancel, the exact failure
    D-RM33-11 exists to prevent.
  - `ChargeRowStatic`'s `fragments.ChargeRow(vm, csrfToken)` call becomes `fragments.ChargeRow(vm,
    csrfToken, windowStartStr, windowEndStr)`.
  - `ChargeRowEditFragment`'s `fragments.ChargeRowEdit(vm, csrfToken, nil)` call becomes
    `fragments.ChargeRowEdit(vm, csrfToken, nil, windowStartStr, windowEndStr)`.
  `depends_on`: 5.2 · `parallel_ok`: no

---

## Wave 6 — deferred dead-code + i18n-key cleanup

- [ ] **6.1** **[module: gateway worker]** `internal/gateway/templates/fragments/charge_row.templ`
  — run `grep -rn "ChargeRowEmpty\|ChargeRowError" internal/gateway` (excluding `_templ.go`);
  once it returns matches ONLY inside `charge_row.templ` itself (i.e. `charges.go` no longer
  calls either after Wave 5.6), delete both `templ ChargeRowEmpty(...)` and `templ
  ChargeRowError(...)`. If the grep still shows a live caller, do NOT delete — report which one.
  `depends_on`: 4.2, 5.6 · `parallel_ok`: with 6.2

- [ ] **6.2** **[module: gateway worker]** `internal/gateway/i18n/catalog.go` — run `grep -rn
  "KeyChargesListRefresh\|KeyChargesListHeaderVehicle" internal/gateway` (excluding
  `catalog.go`, `_templ.go`); once it returns ZERO matches, delete both `Key` constants and their
  catalogue map entries. If any reference remains, do NOT delete that key — report which one and
  where.
  `depends_on`: 4.1, 5.1 · `parallel_ok`: with 6.1

---

## Wave 7 — documentation (new `ui/` primitive, `CLAUDE.md` docs-track-change rule)

- [ ] **7.1** **[module: gateway worker]** `internal/gateway/AGENTS.md` — add `Dot` to the
  enumerated `ui/` kit list in the "UI stack (styling)" section's "Compose the `ui/` kit" bullet
  (currently `Card, StatTile, Button, Alert, Badge, Table, PageHeader, NavShell, ConfirmDialog,
  and the form set Field/Input/Select/Textarea`), with a short parenthetical noting its purpose
  (small colour-only completeness indicator with a native tooltip, distinct from `Badge`'s
  mandatory text). This is a component addition, not a client-side-JS exception — it does NOT
  need its own RD-numbered section (RD9–RD13 are reserved for JS decisions; `ui.Dot` ships zero
  JS).
  `depends_on`: 1.1 · `parallel_ok`: yes (independent file; may run any time after Wave 1)

---

## Wave 8 — codegen

- [ ] **8.1** **[module: gateway worker]** Run `make templ` (regenerates `*_templ.go` for all
  four Wave 4 files, `ui/dot_templ.go`, and `charge_row.templ` again after Wave 6.1's deletion),
  then `make css` (the new `ui.Dot`/`ui.StatTile`/`ui.Join`/`ui.Badge` usage may introduce classes
  not yet in the committed `static/app.css` — check `git diff --stat
  internal/gateway/static/app.css`; confirm rather than assume if it shows no change).
  `make generate` runs both and is an acceptable substitute.
  `depends_on`: 4.1, 4.2, 4.3, 4.4, 6.1 · `parallel_ok`: no

---

## Wave 9 — tests (Test Contract Groups C, D, E + the four REWRITE items — this tier's only
`httptest`/rendered-HTML wave; all sub-tasks land in `internal/gateway/handlers/charges_test.go`,
same real merge-risk caveat tier 2's Wave 8 accepted for its own same-file test tasks)

- [ ] **9.1** **[module: gateway worker]** Group **C1–C3** (design.md §Test Contract Group C) —
  rendered-HTML assertions on the completeness dot / status badge:
  - C1: a `DONE`, fully-complete entry's row renders `ui.Dot` with a class containing
    `bg-success` and the status badge text matches the DONE label.
  - C2: a `DONE` entry missing `EndBatteryPct` renders the WARNING dot class, never `bg-error`
    (D-RM33-12 — assert the class is never the error variant).
  - C3: a normally-shaped `IN_PROGRESS` entry renders the warning dot AND the IN_PROGRESS badge.
  `depends_on`: 8.1, 5.2 · `parallel_ok`: with 9.2, 9.3, 9.4

- [ ] **9.2** **[module: gateway worker]** Group **D1–D4** (design.md §Test Contract Group D) —
  window-preservation `httptest` assertions:
  - D1: `POST /ui/charges/create` with a valid submission plus `start=2026-08-01&end=2026-08-31`
    in the form body → the OOB `#charges-list` reflects that window, not the default.
  - D2: same but `start`/`end` absent → OOB falls back to the default 7-day window, success
    status unaffected (D-Include's "cosmetic only, never a validation gate").
  - D3: `PUT /ui/charges/row/{id}` with hidden `start`/`end` set to a non-default window → the
    OOB `#charges-list` reflects that window.
  - D4: `DELETE /ui/charges/row/{id}?start=2026-08-01&end=2026-08-31` → the response is a full
    `#charges-list` fragment (not a bare `<tr>`) reflecting the post-delete state within THAT
    window, and the deleted row's id is absent from it.
  `depends_on`: 8.1, 5.4, 5.5, 5.6 · `parallel_ok`: with 9.1, 9.3, 9.4

- [ ] **9.3** **[module: gateway worker]** Group **E1–E4** (design.md §Test Contract Group E) —
  no-vehicle / malformed-window / reader-error empty states:
  - E1: zero registered vehicles → `GET /charges` contains `ChargesEmptyState()`'s message and
    contains NEITHER a preset button NOR any `ui.StatTile` NOR a `<table>` (assert absence).
  - E2: `GET /ui/charges/list?start=not-a-date&end=2026-08-31` → HTTP 400, same no-chrome
    assertions as E1.
  - E3: valid vehicle + valid window, fake `Reader` error → response contains preset buttons AND
    four `ui.StatTile`s (zero/`—`) AND a `ui.Alert` — NOT the same render as E1/E2.
  - E4: valid vehicle + valid window, fake `Reader` returns `[]charging.Entry{}` (no error) →
    preset buttons shown, tiles shown at `0`/`—` (not hidden), `ChargesEmptyState()` in place of
    table rows.
  `depends_on`: 8.1, 5.1, 5.3 · `parallel_ok`: with 9.1, 9.2, 9.4

- [ ] **9.4** **[module: gateway worker]** The four existing-test REWRITE items design.md
  enumerates so nothing silently breaks (§Test Contract "Existing tests requiring REWRITE"):
  - `TestChargesListFragment_WithEntries`, `TestChargesListFragment_ReaderError`,
    `TestChargesListFragment_EmptyState` — account for the default-window `?start=&end=`
    resolution and the new tiles/presets in the response.
  - `TestChargeRowDelete_ValidInput_RendersEmptyRow` — rename/rewrite: the response is no longer
    a bare empty `<tr>`; assert the full `#charges-list` fragment shape (same assertion as D4).
  - `TestChargeRowDelete_ThenListReflectsRemoval` — likely still valid in INTENT, re-point at the
    new response shape.
  - The comment at the old `charges_test.go:853` referencing `ChargeRowError`'s `<td>` shape —
    remove or update once that template is deleted (Wave 6.1).
  `depends_on`: 8.1, 5.6 · `parallel_ok`: with 9.1, 9.2, 9.3

---

## Wave 10 — signals

- [ ] **10.1** **[module: gateway worker]** Run the cheap deterministic signals the
  `Test-Execution-Policy` allows: `gofmt -l ./internal/gateway`, `go build ./...`, `go vet
  ./...`, `make ui-guard`, `make i18n-guard`. Confirm every i18n key added in 1.2 and every key
  removed in 6.2 is consistent (`make i18n-guard`'s static scan catches a missed
  `i18n.T`-bypassing literal; `TestCatalog_AllKeysHaveBothLanguages` itself is owner-run as part
  of `go test`/`make check`).
  `depends_on`: 9.1, 9.2, 9.3, 9.4 · `parallel_ok`: no

---

## Owner verification (`Test-Execution-Policy`: the owner runs the suite)

- [ ] **O1** **[owner]** Run the suite. Nothing above may be reported as `done` on the
  assistant's say-so; work that is complete but unexecuted is **`awaiting-user-verification`**.
  ```bash
  make check
  ```
  (`make check` = `build vet ui-guard i18n-guard money-guard test`.)

---

## Leader resolutions (the three ambiguities the artifact worker flagged — all resolved here)

The worker that decomposed design.md correctly refused to guess on three points and flagged them
instead of silently diverging. The leader resolved all three at the Step 3 gate, BEFORE any
implementation wave, and corrected design.md where design.md was the thing at fault. No open
question remains; a worker hitting one of these follows the resolution below.

- **L1 — `buildChargesPage`'s trailing `true`: a snippet typo. design.md corrected.**
  §D-Refresh's `ChargeRowDelete` snippet passed an 8th positional argument that named no
  parameter anywhere in the document. Every other reference to the function in design.md uses
  seven. The `true` has been removed from design.md; the frozen signature is 5.1's seven args.
  **`today` is retained and is NOT redundant with `start`/`end`** — the worker's first reading
  dropped it, which would have been a real defect: `today` is the browser's calendar day feeding
  the create form's D1 date defaults and the D2 battery suggestion, while `start`/`end` is the
  list's filter window. Deriving the form's default date from `end` would pre-fill the create
  form with the last day of the month whenever "This month" is the active preset.

- **L2 — `windowFromQuery` is now defined in design.md, not extrapolated.** §D-Include gains the
  shared `bestEffortWindow` helper plus its two one-line callers `windowFromForm` (`c.PostForm`)
  and `windowFromQuery` (`c.Query`), so the "never a validation gate" behaviour is written once
  and cannot drift between the POST and GET sides. The section also states explicitly why this is
  NOT `parseChargesRange`: that one rejects a malformed window and the caller 400s (right for the
  user-driven filter route, wrong for a post-write refresh, where a malformed window must never
  turn a successful delete into an error).

- **L3 — threading the window through `ChargeRowStatic` / `ChargeRowEditFragment` is IN scope.**
  Not over-reach: it is compile-forced. `ChargeRow` gains mandatory `windowStartStr`/
  `windowEndStr` params (4.2), so every call site must supply a window, and the only alternative
  to threading the real one is passing `""` — which would silently reset the user's filter on
  Cancel, the exact failure D-RM33-11 exists to prevent. Task 5.7 stands as written.

## Decision coverage

| Decision (design.md §Decisions) | Carried by |
|---|---|
| **D-Range** | 2.1 (`parseChargesRange`), 3.1 (A1–A6 tests), 5.3 (`ChargesListFragment` wiring) |
| **D-Presets** | 2.1 (`buildChargesPresets`, `endOfMonth`), 3.1 (A7–A8 tests), 4.1 (`chargesRangeSelector`) |
| **D-Dot** | 1.1 (`ui.Dot`/`DotProps`), 1.2 (tooltip + badge i18n keys), 1.3 (`ChargeEntryVM.Complete`), 2.2 (`entryComplete`), 3.2 (B1–B3 tests), 4.2 (dot + badge render), 9.1 (C1–C3 tests), 7.1 (AGENTS.md doc) |
| **D-Tiles** | 1.3 (`ChargeTiles` struct), 2.2 (`buildChargeTiles`), 3.2 (B4–B5 tests), 4.1 (`chargeTiles` component), 5.1 (wiring into `buildChargesPage`) |
| **D-Empty** | 1.3 (`NoFilterChrome` field), 4.1 (three-state template branching), 5.1 (D-RM33-9 no-account-wide-fallback + state computation), 5.3 (malformed-window 400 branch), 9.3 (E1–E4 tests) |
| **D-Include** | 4.1 (hidden `#charges-window-start`/`#charges-window-end` inputs), 4.4 (`hx-include` attribute), 5.2 (`windowFromForm`), 5.4 (`ChargeCreate` success-path wiring), 9.2 (D1–D2 tests) |
| **D-Refresh** | 4.2 (`ChargeRowUpdateSuccessOOB`, Delete target retarget), 4.3 (edit-row hidden window inputs), 5.5 (`ChargeRowUpdate`), 5.6 (`ChargeRowDelete` rewrite), 6.1 (dead-code removal), 9.2 (D3–D4 tests), 9.4 (delete-test rewrites) |
| **D-Colspan** | 4.3 (`charge_row_edit.templ` `colspan="8"` → `"9"`) |

Roadmap decisions D9/D10/D11/D13/D14 and D-RM33-9..12 are covered transitively through the
design.md decisions above exactly as design.md's own "Roadmap-decision mapping" table states —
not re-derived here.
