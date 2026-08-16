> **Additive change to `internal/gateway` only — read design.md before starting.** This adds
> a third chart panel ("Battery consumed") to the existing `/ui/dashboard/history` fragment,
> wires a new `battery.Reader` dependency into the gateway's `Deps`, and changes
> `parseHistoryRange`'s default window and validation cap from "browser-today" to
> "browser-yesterday" (D11 — a real behavior change on an existing endpoint, not purely
> additive; see proposal.md's "Breaking" section). **No database change** — the `database`
> design gate does not apply (`internal/gateway` owns no schema, and `battery.Reader` is a
> recompute-on-read port with no cache, tier 3).
>
> **T7 is LEADER-OWNED and out of this dispatch's sandbox** — `cmd/web` is not a child of
> `internal/`, so no worker touches it. It is listed here only so the dependency chain and
> the exact contract (design.md's "`cmd/web` wiring" section) are visible in one place.
>
> **T6's `buildConsumedChart` marker/tooltip behavior for a day that is both `Flagged` AND
> `DaysSpanned > 1` follows roadmap D21 (owner ruling, 2026-08-16), NOT an open question.**
> D21 settles what this dispatch's first revision had flagged as an ambiguity (formerly
> called out as design.md D-G4): such a day carries BOTH markers, with both facts in the
> tooltip — see design.md D-G4 (rewritten under D21) for the full four-state table. Implement
> it as specified; there is nothing left to escalate on this point.
>
> **Dependencies:**
>
> - T1 (`history_vm.go`: `HistoryBar.MarkerFlagged`/`MarkerSpan` booleans,
>   `HistoryView.Consumed`) has no dependencies — start immediately.
> - T2 (`catalog.go`: seven new i18n keys — three composable clauses + title + no-data +
>   two charge-type nouns, design.md D-G7) has no dependencies — **parallel-safe with T1**
>   (disjoint files).
> - T3 (`history.templ`: two independent marker-chip blocks, third `ui.Card`) depends on T1
>   (needs `MarkerFlagged`/`MarkerSpan`/`HistoryView.Consumed` to exist to compile against)
>   and T2 (needs the new `i18n.Key*` constants for the card title).
> - T4 (`format.go`: `formatPctRaw`) has no dependencies — **parallel-safe with T1–T3**
>   (disjoint file).
> - T5 (`gateway.go` + `handlers.go`: `Deps.BatteryReader` wiring) has no dependencies —
>   **parallel-safe with T1–T4** (disjoint files; only needs `internal/battery` to already
>   exist, which it does — tier 3 archived).
> - T6 (`history.go`: `parseHistoryRange` D11 change, `buildConsumedChart`,
>   `buildHistoryView` extension) depends on T1 (types), T2 (catalogue keys), T4
>   (`formatPctRaw`), T5 (`batteryReader` field on `Handler`) all landing first.
> - T7 (`cmd/web/main.go` wiring, LEADER-OWNED) depends on T5 (`Deps.BatteryReader` must
>   exist) — run only after this module's own build/vet signals pass, independently of T6/T8
>   (the leader can wire `cmd/web` while this module's worker is still writing T6's tests).
> - T8 (`history_test.go`: new consumed-chart tests + updated D11 tests) depends on T6.
> - T9 (`AGENTS.md` update) depends on T5 (final `Deps` shape) — **parallel-safe with T6, T8**
>   (disjoint file).
> - Verification (V) depends on T1–T6, T8, T9 (not T7 — a separate module's build).

---

## T1. `internal/gateway/templates/fragments/history_vm.go` — `HistoryBar.MarkerFlagged`/`MarkerSpan`, `HistoryView.Consumed`

**No enum type in this task** — D21 (owner ruling) means a bar can carry both markers at
once, so there is no single-valued type to add; see design.md D-G5.

- [x] T1.1 Add `MarkerFlagged bool` and `MarkerSpan bool` to `HistoryBar`, with the doc
      comments from design.md's "Go-Level Surface" section (each states which roadmap
      decision it renders — D10 for `MarkerFlagged`, D20 for `MarkerSpan` — and that BOTH
      can be true on the same bar per D21). Existing odometer/battery bar construction
      sites (`buildOdometerChart`, `buildBatteryChart`) are UNCHANGED — they leave both new
      fields at their zero value (`false`); confirm no existing test asserts a struct
      literal that would now need these fields added (Go zero-value struct literals
      compile unchanged).
      Acceptance: `go build ./...` succeeds; `gofmt -l internal/gateway/templates/
      fragments/history_vm.go` reports no issues.
- [x] T1.2 Add `Consumed HistoryChart` to `HistoryView`, with the doc comment from
      design.md stating the D18/D18a bucketing note, the D19 relative-scale note (a single
      `math.Max(0, ConsumedPct)` clamp, no per-state branch — design.md D-G1), and that a
      bar's two marker fields are independent and can both be true (D21).
      Acceptance (all of T1): `go build ./...` succeeds; `go vet ./...` reports no issues.

## T2. `internal/gateway/i18n/catalog.go` — seven new keys, both ES/EN

**Three of the seven keys are composable CLAUSES, not whole tooltips** — see design.md D-G7
(revised under D21). Do not add a fourth "span+flagged" whole-sentence key — the composition
in T6 covers that combination by joining the span clause and the flagged clause.

- [x] T2.1 Add a new `// --- consumed chart (templates/fragments/history.templ,
      handlers/history.go) ---` section immediately after the existing `// --- history
      chart ...` section (after `KeyHistoryNoSnapshotTooltip`, before `// --- supercharger
      stats ...`), with the seven `Key` constants: `KeyHistoryConsumedTitle`,
      `KeyHistoryConsumedPctClause`, `KeyHistoryConsumedSpanClause`,
      `KeyHistoryConsumedFlaggedClause`, `KeyHistoryConsumedNoDataTooltip`,
      `KeyHistoryChargeTypeManual`, `KeyHistoryChargeTypeSupercharger` — string values per
      design.md D-G6/D-G7 (e.g. `"history.consumed_title"`, `"history.consumed_pct_clause"`,
      ...).
- [x] T2.2 Add the matching `catalog` map entries in the SAME section position (mirroring
      the existing history-chart entries' placement), each with non-empty `ES` and `EN`
      values exactly as specified in design.md D-G6/D-G7:
      - `KeyHistoryConsumedTitle`: `{ES: "Batería consumida", EN: "Battery consumed"}`
      - `KeyHistoryConsumedPctClause`: `{ES: "%s%% consumida", EN: "%s%% consumed"}` — a
        CLAUSE, no label, no leading separator (the handler prepends `<label> · `).
      - `KeyHistoryConsumedSpanClause`: `{ES: "%s%% · abarca %d días", EN: "%s%% · covers
        %d days"}` — a CLAUSE (value + day-count as one fact).
      - `KeyHistoryConsumedFlaggedClause`: `{ES: "posible registro de carga faltante (%s)",
        EN: "possible missing charge record (%s)"}` — a CLAUSE, appended whenever the day
        is flagged, span or not.
      - `KeyHistoryConsumedNoDataTooltip`: `{ES: "%s · sin dato", EN: "%s · no data"}` —
        stays a WHOLE tooltip (label included) — the one key that does NOT compose.
      - `KeyHistoryChargeTypeManual`: `{ES: "manual", EN: "manual"}`
      - `KeyHistoryChargeTypeSupercharger`: `{ES: "Supercharger", EN: "Supercharger"}`
      Acceptance: `go build ./...` succeeds; `go test -run TestCatalog_AllKeysHaveBothLanguages
      ./internal/gateway/i18n/...` is NOT run by this worker (Test-Execution-Policy) but the
      worker visually confirms every new key has both fields non-empty before moving on;
      `make i18n-guard` run by this worker (allowed signal) reports no new violations.

## T3. `internal/gateway/templates/fragments/history.templ` — two independent marker-chip blocks, third chart card — depends on T1, T2

- [x] T3.1 Add TWO independent per-bar marker blocks to `historyBarChart` (NOT a
      switch/else-if — a bar can hit both per D21), appended after the existing bar-rect
      loop inside the same `<svg>`, exactly as specified in design.md's "Go-Level Surface"
      section — literal `fill-warning`/`fill-info` classes written in the `.templ` source
      (design.md D-G5), each at its OWN fixed geometry so both remain independently visible
      when a bar carries both: `if bar.MarkerFlagged` renders its chip at `y="1"
      height="4"`; `if bar.MarkerSpan` renders its chip at `y="6" height="4"` — same
      positions whether the marker appears alone or alongside the other. Each chip wrapped
      in its own `<title>{ bar.Tooltip }</title>`.
- [x] T3.2 Add the third `ui.Card` to `DashboardHistoryContent`, after the existing
      Battery card: `@ui.Card(ui.CardProps{Title: i18n.T(ctx,
      i18n.KeyHistoryConsumedTitle)}) { @historyBarChart(v.Consumed, "fill-accent") }`.
      `"fill-accent"` is a literal at this call site.
- [x] T3.3 Run `make templ` (regenerates `history_templ.go`) and `make css` (the new
      literal classes `fill-accent`/`fill-warning`/`fill-info` must appear in the
      regenerated `static/app.css` — confirm with `grep -o 'fill-accent\|fill-warning\|
      fill-info' internal/gateway/static/app.css` after running `make css`; an empty grep
      result means the Tailwind content scan missed the new literals and the marker chips
      will ship unstyled — do not proceed to T8 until this passes).
      Acceptance (all of T3): `go build ./...` succeeds; the three new fill-* classes are
      present in `static/app.css`; `git diff --stat internal/gateway/static/` shows
      `app.css` changed and is included in this task's commit (per `AGENTS.md`'s
      "stale CSS silently ships unstyled markup" CI guard).

## T4. `internal/gateway/handlers/format.go` — `formatPctRaw`

- [x] T4.1 Add `formatPctRaw(pct float64) string` exactly as specified in design.md D-G8,
      placed after `formatKmRaw` in the existing file (no new file).
      Acceptance: `go build ./...` succeeds; `gofmt -l internal/gateway/handlers/
      format.go` reports no issues. A quick manual sanity check (not a committed test,
      just worker verification): `formatPctRaw(11.0) == "11.0"`,
      `formatPctRaw(-3.4) == "-3.4"`.

## T5. `internal/gateway/gateway.go` + `internal/gateway/handlers/handlers.go` — `Deps.BatteryReader` wiring

- [x] T5.1 In `handlers/handlers.go`: add `BatteryReader battery.Reader` to `Deps` (with
      the doc comment from design.md D-G11, mirroring `SuperchargerReader`'s existing doc
      comment shape), add `batteryReader battery.Reader` to `Handler`, add
      `batteryReader: d.BatteryReader` to `New()`. Add the new import
      `"github.com/cristianpena/magus-tesla-api/internal/battery"`.
- [x] T5.2 In `gateway.go`: add `BatteryReader battery.Reader` to `gateway.Deps` (same doc
      comment pattern as `SuperchargerReader`'s existing field there), forward it into the
      `handlers.Deps{...}` literal at `NewEngine`'s handler-construction call site. Add the
      matching import.
      Acceptance (all of T5): `go build ./...` succeeds (both `Deps` structs' new field is
      unused by any caller yet — that's expected, `cmd/web` supplies it in T7); `go vet
      ./...` reports no issues.

## T6. `internal/gateway/handlers/history.go` — D11 change, `buildConsumedChart`, `buildHistoryView` extension — depends on T1, T2, T4, T5

- [x] T6.1 Modify `parseHistoryRange` exactly as specified in design.md D-G9: compute
      `yesterday := today.AddDate(0, 0, -1)` once at the top of the function, use it (not
      `today`) for the both-absent default's `end` and for the `calendarDateAfter(e, ...)`
      cap check. Update the function's doc comment's numbered validation steps (1 and 4)
      to say "yesterday" instead of "today" — do not leave the prose stale.
- [x] T6.2 Add `buildConsumedChart(ctx context.Context, days []battery.DayConsumption,
      start, end time.Time) fragments.HistoryChart` exactly as specified in design.md's
      "Go-Level Surface" section — the two-phase collect/convert structure; every bar's
      `displayVal = math.Max(0, day.ConsumedPct)`, ONE clamp, no per-state branch
      (design.md D-G1); `MarkerFlagged := day.Flagged` and `MarkerSpan := day.DaysSpanned >
      1` set INDEPENDENTLY — both can be true on the same bar (D21, design.md D-G4); the
      tooltip composed from independently-translated clauses joined by `" · "` per
      design.md D-G7 (span clause when `MarkerSpan`, else plain-value clause when
      `!MarkerFlagged`, PLUS the flagged clause appended whenever `MarkerFlagged`); the
      `KeyHistoryConsumedNoDataTooltip` no-data bar; `Empty` firing only when `len(days) ==
      0`. Add the `chargeTypeLabel` helper in the same file.
      Add the new import `"github.com/cristianpena/magus-tesla-api/internal/battery"`
      (`"math"` and the `i18n`/`telemetry` imports already exist in this file).
- [x] T6.3 Extend `buildHistoryView`: after the existing `v.Battery = buildBatteryChart(...)`
      line, add the independent `h.batteryReader.ConsumedByDay(ctx, uid, teslaID, start,
      end)` call with its OWN error branch that sets only `v.Consumed =
      fragments.HistoryChart{Empty: true}` and returns — it must NOT affect
      `v.Odometer`/`v.Battery`, which already succeeded (design.md D-G10). On success,
      `v.Consumed = buildConsumedChart(ctx, days, start, end)`.
      Acceptance (all of T6): `go build ./...` succeeds; `go vet ./...` reports no
      issues; `gofmt -l internal/gateway/handlers/history.go` reports no issues.

## T7. `cmd/web/main.go` wiring — LEADER-OWNED, outside `internal/gateway`'s sandbox — depends on T5

- [x] T7.1 Add the import `"github.com/cristianpena/magus-tesla-api/internal/battery"`.
- [x] T7.2 Construct `battery.NewReader(telemetry.NewReader(pool),
      telemetry.NewSuperchargerReader(pool), manualcharge.NewReader(pool), acct,
      battery.DefaultWindow)` and pass it as `gateway.Deps.BatteryReader` in the existing
      `gateway.NewEngine(gateway.Deps{...})` call site, alongside the existing
      `TelemetryReader`/`SuperchargerReader`/`ManualChargeReader` fields — exact form in
      design.md's "`cmd/web` wiring" section (mirrors `cmd/poller/main.go`'s existing
      identical construction verbatim).
      Acceptance: `go build ./...` succeeds for the whole repo; `go vet ./...` reports no
      issues. This task is NOT part of this dispatch's deliverable — recorded here for the
      leader to pick up after T5 lands.

## T8. `internal/gateway/handlers/history_test.go` — new consumed-chart tests + updated D11 tests — depends on T6

- [ ] T8.1 `TestBuildConsumedChart_NormalDay_RelativeScale` — design.md Test Contract (a).
- [ ] T8.2 `TestBuildConsumedChart_FlaggedNonSpan_Manual_HidesValue` — Test Contract (b):
      assert the rendered tooltip string does NOT contain `"-5"` anywhere.
- [ ] T8.3 `TestBuildConsumedChart_FlaggedNonSpan_Supercharger_ZeroWithDistance` — Test
      Contract (c).
- [ ] T8.4 `TestBuildConsumedChart_MultiDaySpan_NotFlagged_ShowsRealValue` — Test Contract
      (d).
- [ ] T8.5 `TestBuildConsumedChart_MultiDaySpanAndFlagged_BothMarkers_ValueShown` — Test
      Contract (e), roadmap D21: assert BOTH `MarkerFlagged == true` AND `MarkerSpan ==
      true` (not one or the other), and the tooltip DOES contain `"-3.0"` AND states both
      the span-day-count fact and the missing-charge-record fact.
- [ ] T8.6 `TestBuildConsumedChart_MultiDaySpanAndFlagged_HeightClampIsolatedFromMax` — Test
      Contract (f): two-entry fixture isolating the height clamp from the window max.
- [ ] T8.7 `TestBuildConsumedChart_NoDataDay_DistinctFromNoSnapshotWording` — Test Contract
      (g): assert the tooltip text differs from what `buildBatteryChart`'s missing-day
      tooltip would produce for the same label.
- [ ] T8.8 `TestBuildConsumedChart_EmptyWhenZeroDays` — Test Contract (h).
- [ ] T8.9 `TestBuildConsumedChart_BucketsOnDateVerbatim_NoEffectiveDayUTC` — Test Contract
      (i), the D-G2 regression guard.
- [ ] T8.9a `TestBuildConsumedChart_FlaggedDayNeverDistortsScale_SingleClamp` — Test
      Contract (i2), the D-G1 dead-branch regression guard: a flagged (non-span) day and a
      normal day in the same window; assert the flagged day's `math.Max(0, ConsumedPct)`
      alone keeps it out of the max — do NOT special-case this in the implementation under
      test; the point of this test is that the ONE clamp already suffices.
- [ ] T8.10 Update `TestParseHistoryRange_BothAbsent_DefaultSixDayWindow` to assert
      `end == today.AddDate(0,0,-1)` (Test Contract (j)) — was `end == today`.
- [ ] T8.11 Update `TestParseHistoryRange_EndCapUsesBrowserToday` (and rename it to
      `TestParseHistoryRange_EndCapUsesBrowserYesterday`, updating its doc comment) to
      assert `end == today` now returns `ok=false` (Test Contract (k)) and `end ==
      today.AddDate(0,0,-1)` returns `ok=true` (Test Contract (l)). Apply the equivalent
      update to `TestParseHistoryRange_EndCapUsesBrowserToday_AcrossOffsets`.
- [ ] T8.12 Review `TestParseHistoryRange_EndAfterToday` and every entry in
      `TestDashboardHistoryFragment_400_Cases`'s table per design.md Test Contract (m): a
      fixture already dated strictly after `today` (e.g. `2099-12-31`) needs no change; a
      fixture that used `end == today` expecting acceptance must move to expecting
      rejection.
- [ ] T8.13 Update `TestDashboardHistoryFragment_DefaultWindowPassedToReader`,
      `TestDashboardHistoryFragment_DaysParamIsIgnored`, and
      `TestDashboardHistoryFragment_DefaultWindowActivatesSixDayPreset` to compute their
      expected windows from `yesterday := today.AddDate(0,0,-1)` (Test Contract (n)).
      `DefaultWindowActivatesSixDayPreset`'s doc comment currently states the API default
      and the preset window diverge — rewrite it to state they now match.
- [ ] T8.14 `TestHandler_BatteryReaderDepsForwarding` (naming per whatever pattern the
      existing `SuperchargerReader`/`ManualChargeReader` forwarding test already uses —
      grep it first, mirror its shape, do not invent a new one) — Test Contract (o).
      Acceptance (all of T8): every listed test compiles (`go vet ./...`); this worker
      does NOT run `go test ./...` (Test-Execution-Policy) — report all tests as
      `awaiting-user-verification` with the exact command to run.

## T9. `internal/gateway/AGENTS.md` — document the new `battery.Reader` dependency — depends on T5

- [x] T9.1 Add a `Deps.BatteryReader battery.Reader` bullet to the "Public interface"
      section, mirroring the existing `Deps.SuperchargerReader`/`Deps.ManualChargeReader`
      bullets' shape: what it is, who calls it (`buildHistoryView`, once per history
      fragment render), the "NEVER import a battery database package" reminder (note there
      isn't one — `internal/battery` owns no DB), and which change added it
      (`RM28-gateway-add-consumed-graph`, tier 4).

---

## V. Verification — depends on T1–T6, T8, T9

- [ ] V.1 `go build ./...` succeeds for the whole repo (excluding T7's `cmd/web` change,
      which is the leader's separate build check).
- [ ] V.2 `go vet ./...` reports no issues.
- [ ] V.3 `gofmt -l` reports no files needing formatting under `internal/gateway/`.
- [ ] V.4 `make ui-guard` reports no violations (no raw DaisyUI component class inlined
      outside `templates/ui/`).
- [ ] V.5 `make i18n-guard` reports no violations (every new consumed-chart string routes
      through `i18n.T`).
- [ ] V.6 `make css` was run after the `.templ` change and `static/app.css` contains the
      three new literal classes (T3.3's grep check) — committed alongside the `.templ`
      change, not as a follow-up.
- [ ] V.7 `openspec validate RM28-gateway-add-consumed-graph --strict` passes.
- [ ] V.8 Hand back to the owner: `go test ./internal/gateway/...` (or the project's
      `make test`) — every test T8 added is written but not run by this worker
      (Test-Execution-Policy); status is `awaiting-user-verification` until the owner runs
      it and reports back.
