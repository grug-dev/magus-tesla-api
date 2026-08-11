# Tasks: gateway-battery-consumed-chart

> Follow-up to MAG-6 / RM7. Single module (`internal/gateway`), no DB, no new route. Sub-task A
> is the only code change of substance; B and C are independent verification/doc passes that can
> run in parallel with each other once A lands (both only *read* A's output, they don't edit the
> same lines). D is the final gate. Options 2/3 from design.md's Open Questions are explicitly
> **not** tasked here — they are follow-up work pending a grill-me with the user.

## A. Rewrite `buildBatteryChart`

- [ ] A.1 In `internal/gateway/handlers/history.go`, rewrite `buildBatteryChart(snaps
  []telemetry.Snapshot, days int) fragments.HistoryChart` to mirror `buildOdometerChart`'s
  structure (design.md D1–D4): take `want := days + 1` snapshots (same windowing as
  `buildOdometerChart`), guard `len(pts) < 2` → `fragments.HistoryChart{Empty: true}`, then for
  `i := 1; i < len(pts); i++` compute `d := pts[i-1].BatteryLevelPct - pts[i].BatteryLevelPct`
  (note the reversed operand order vs. odometer — call this out in a code comment, per design.md
  D1), clamp `d < 0` to `0`.
- [ ] A.2 Track `maxPct` (the largest single-day consumed delta in the loop, mirroring
  `buildOdometerChart`'s `maxKm`) and set each bar's `HeightPct = round(d / maxPct * 100)` when
  `maxPct > 0`, else `0` — do not leave `HeightPct` as the raw 0–100 absolute level (design.md D3).
- [ ] A.3 Build each bar's tooltip **unchanged in content and format** —
  `fmt.Sprintf("%s · %d%% · %s km range", label, pts[i].BatteryLevelPct,
  formatKmRaw(pts[i].BatteryRangeKm))` — sourced from the **current** snapshot `pts[i]`'s own
  absolute fields, not from the delta `d` (design.md D5). `label` continues to come from
  `pts[i].EffectiveDate.Format("01-02")`, matching the odometer builder and the existing RM7
  behavior.
- [ ] A.4 Set `LabelVertical: labelVerticalFor(days)` on the returned `fragments.HistoryChart`
  (unchanged helper, already used by `buildOdometerChart`) and `Empty: len(bars) == 0` as a
  defensive final check (mirrors `buildOdometerChart`'s return line).
- [ ] A.5 Do **not** touch `buildOdometerChart`, `buildHistoryView`, `DashboardHistoryFragment`,
  `clampHistoryDays`, `historyDayPresets`, `defaultHistoryDays`, `labelVerticalFor`, or
  `startOfDay` — none of these need to change for this scope. If, while implementing A.1–A.4, you
  find you *want* to touch one of them, stop and reconcile with this task list first (the odometer
  chart is explicitly required to stay byte-for-byte behaviorally unchanged).
- [ ] A.6 If extracting a shared delta-computation helper for A.1–A.2 (optional — not required),
  confirm `buildOdometerChart`'s own call site keeps its exact current arguments and produces
  identical output; add/keep a test that pins `buildOdometerChart`'s existing behavior unchanged
  (see B.4) so an accidental shared-helper regression is caught immediately.

_depends_on: none_

## B. Tests

- [ ] B.1 Table-driven test(s) for `buildBatteryChart` in `internal/gateway/handlers/history_test.go`
  (or the existing test file covering `history.go`) covering, at minimum:
  - Normal consumption day: consecutive snapshots 93% → 69% yields a bar valued 24 (before height
    scaling), per the design.md worked example.
  - First-bar baseline: given `days+1` snapshots for `days=N`, exactly `N` bars are returned and
    the first bar's value is a real delta against the extra oldest snapshot (not a zero stub, not
    omitted).
  - Charging (negative) day: a snapshot pair where level rose yields a bar with `HeightPct == 0`.
  - Fewer than 2 snapshots: `Empty: true`, no bars.
  - Height scaling: with multiple bars of varying delta, the largest-delta bar's `HeightPct == 100`
    and others scale proportionally (mirrors the equivalent existing `buildOdometerChart` test, if
    one exists — check `history_test.go` first and follow its exact assertion style).
  - Tooltip content unchanged: assert the tooltip string still reads
    `"<MM-DD> · <level>% · <range> km range"` using the **current** day's absolute level/range —
    not the delta — for at least one non-trivial (consumption ≠ level) case, so a regression that
    accidentally swaps in the delta is caught.
- [ ] B.2 Confirm (do not need to add — just verify no regression) that any existing
  `buildOdometerChart` test still passes unmodified — this change must not alter its behavior.
- [ ] B.3 If a render/fragment-level test exists asserting the "Battery history" `<svg>` output
  (e.g. bar count, `<title>` content), update its expected values to the new consumed-%
  semantics; otherwise add one alongside B.1's handler-level tests.
- [ ] B.4 Add or confirm a `buildOdometerChart`-pinning test (see A.6) if any shared helper was
  extracted; skip this if A was implemented with no shared extraction.

_depends_on: A_

## C. Docs sweep (change-locality check, not expected to find much)

- [ ] C.1 Check `internal/gateway/AGENTS.md`'s "Charts / data-viz" section: it documents the
  hand-rolled-SVG decision and the general chart pattern, not per-chart semantics, so it likely
  needs **no edit**. Confirm this instead of skipping it — if any sentence there specifically
  claims the battery chart shows "absolute level," correct it.
- [ ] C.2 Check whether the "Battery history" `ui.Card` title in
  `internal/gateway/templates/fragments/history.templ` (or wherever the card markup lives) should
  change to something like "Battery consumed" for clarity now that the bar's meaning changed. This
  is a judgment call, not a design requirement — the proposal explicitly left the exact wording to
  the implementer. If changed, it is a pure string edit (no `Props` field addition) and does not
  require `make templ` beyond the normal regenerate step.
- [ ] C.3 No root `README.md` "Project Structure"/"Architecture" table edit — no module was added,
  removed, renamed, or re-scoped, and no new route or `Deps` field was introduced.

_depends_on: A_ (reads the final chart semantics before writing/checking docs against them)

## D. Verification gate

- [ ] D.1 `make check` (build + vet + ui-guard + tests) passes.
- [ ] D.2 If any `.templ` file was touched (C.2), run `make templ` and `make css`, and commit the
  regenerated `internal/gateway/static/app.css` and `*_templ.go` files alongside the source edit
  (per `internal/gateway/AGENTS.md`'s "stale CSS" gotcha) — skip this sub-step entirely if C.2
  made no `.templ` edit.
- [ ] D.3 Manually confirm (via the existing `openspec/specs/gateway/spec.md` scenarios, once
  synced) that the odometer chart's rendered output is unchanged for a fixed fixture — a quick
  sanity check that A.5's "do not touch" boundary held.

_depends_on: A, B, C_
