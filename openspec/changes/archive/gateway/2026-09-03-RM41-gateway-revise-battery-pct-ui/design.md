# Design — RM41-gateway-revise-battery-pct-ui

## Context

`RM41-supercharger-battery-pct-cleanup` (the roadmap, read in full before this
design) closes out MAG-36. Its findings table established, by reading the real
code, that `internal/gateway/handlers/supercharger.go`'s
`superchargerRowVMFromSession` is the ONLY place outside `internal/charging` that
reads `charging.Session.StartBatteryPctEst`/`EndBatteryPctEst` — it maps them into
`fragments.SuperchargerRowVM.StartBatteryPctEstLabel`/`EndBatteryPctEstLabel`,
which two `.templ` files then render as two of the sessions table's nine columns
and two of the edit row's read-only fields. Both fields are permanently NULL on
every row (nothing in the repo writes them — `charging/db/query.sql` and
`telemetry/db/query.sql` both carry comments guarding that exclusion), so both
columns have rendered `"—"` on every row since they shipped. The estimator that was
originally meant to fill them (`RM27` D11 descoped it; the `AGENTS.md` reservation
called it "kept rather than dropped so the estimator can land later without a
migration") shipped on 2026-09-01 as `charging-add-derived-start-battery-pct`,
writing the real `start_battery_pct` column instead of a separate estimate
snapshot — so the reservation is now obsolete (roadmap D8) and the two columns are
permanently dead weight (roadmap D1).

This tier does two things: adds the bilingual guidance alert MAG-36 asked for
(roadmap D4/D5), and removes every trace of `BatteryPctEst`/`StartEstimate`/
`EndEstimate` from `internal/gateway/` (roadmap D9), which is what unblocks tier 2
(`RM41-charging-drop-estimate-columns`) — `charging` cannot drop a column the
gateway still selects through its own struct field.

## Goals / Non-Goals

**Goals:**
- Add `i18n.KeySuperchargerBatteryPctHelp` (`supercharger.battery_pct_help`) with
  the owner's exact corrected copy (roadmap D5) in both `ES`/`EN`.
- Render it as one `ui.Alert{Kind: "info"}` on `/supercharger-stats`, mirroring
  `dashboard.templ`'s existing page-level `ui.Alert` usage exactly (roadmap D4) —
  no new `ui/` kit component.
- Remove `KeySuperchargerStartEstimate`/`KeySuperchargerEndEstimate` (both the
  constants and their catalogue entries), the two table headers, the two `<td>`s in
  `SuperchargerRow`, the two `ui.Field` blocks in `SuperchargerRowEdit`, the two VM
  label fields, and their two assignments in `superchargerRowVMFromSession`
  (roadmap D9).
- Repair `handlers/supercharger_test.go` so it compiles and asserts only against
  the new rendered/mapped shape (roadmap D7) — see Test Contract below.
- Correct `kkpa/context/workflows/supercharger-stats-read.md`'s two bullets that
  describe the gateway's OLD four-column table (`CLAUDE.md` "docs track structural
  change").
- Leave `grep -rn "BatteryPctEst\|StartEstimate\|EndEstimate" internal/gateway/`
  returning nothing — this tier's own definition of done.

**Non-Goals (explicitly deferred, do not implement here):**
- Dropping `start_battery_pct_est`/`end_battery_pct_est` from
  `charging.supercharger_sessions` or `telemetry.supercharger_history` — tiers 2
  and 3.
- Any change to `charging.Session`'s Go struct, `charging/db/query.sql`, or
  `internal/telemetry` — none of those are edited by this worker.
- Correcting `kkpa/context/use-case/charging/verify-session-battery.md`'s
  "estimator that does not exist yet" gotcha — that is a `charging`/write-path
  fact, and the roadmap explicitly assigns its correction to tier 2.
- Adding new test coverage of the alert's rendered text — roadmap D7 excludes new
  unit tests; only repair of what breaks is in scope.

## Decisions

### D1 — The alert renders unconditionally, as the first element of the region

Roadmap D4 fixes that the alert is ONE page-level `ui.Alert{Kind:"info"}` "above
the sessions table," but leaves open whether it also renders when the window has
zero sessions (`v.Empty`) or when the request is malformed (`v.Presets == nil`,
`400`). This design resolves that open question: the alert renders **unconditionally**, as
the very first line inside `templ SuperchargerStatsContent`, before the
`if v.Presets != nil` selector block and before the `if v.Empty { … } else { … }`
branch — so it is present on every render of the region: normal, empty, and
malformed alike.

```templ
templ SuperchargerStatsContent(v SuperchargerStatsView) {
	@ui.Alert(ui.AlertProps{Kind: "info", Class: "mb-4"}) {
		{ i18n.T(ctx, i18n.KeySuperchargerBatteryPctHelp) }
	}
	if v.Presets != nil {
		@superchargerMonthsSelector(v.Presets)
	}
	if v.Empty {
		...
```

**Rationale:** the message is guidance about how the FEATURE behaves ("Tesla
doesn't give us these values; try to remember them; we'll derive the start from
the end") — it is data-independent, so there is no principled reason to hide it
merely because the current window happens to have zero sessions or a malformed
query string. A single unconditional render site is also the cheapest
implementation: no new `SuperchargerStatsView` field, no branching to keep in sync
across future edits to the empty/malformed paths. This reading is consistent with
"above the sessions table" — unconditionally-first is trivially above wherever the
table would be.

**Rejected:** gating the alert on `!v.Empty` (mirroring where `dashboard.templ`
gates its own `ui.Alert` on `d.TelemetryUnavailable`). Rejected because the
dashboard's alert is a data-dependent NOTICE (something went wrong with telemetry
data), whereas this alert is a data-independent instruction — gating it on session
presence would hide the "please remember your percentages next time" guidance
exactly when a user has zero sessions in the window and might be about to
Supercharge for the first time this window.

### D2 — Exact strings (carries roadmap D5 verbatim; restated here as the binding literal)

New key `supercharger.battery_pct_help`:

```go
KeySuperchargerBatteryPctHelp Key = "supercharger.battery_pct_help"
```

```go
KeySuperchargerBatteryPctHelp: {
	ES: "Tesla no nos provee los porcentajes de batería inicial y final. Te aconsejamos que siempre intentes recordarlos al usar un Supercharger, para tener mejor precisión en los análisis. Sin embargo, conociendo solo el porcentaje final, el sistema calculará el porcentaje inicial aproximado que tenía el vehículo.",
	EN: "Tesla does not give us the start and end battery percentages. We recommend you always try to remember them when you use a Supercharger, so the analysis is more accurate. Still, if you only know the end percentage, the system will calculate the approximate start percentage the vehicle had.",
},
```

Place both the constant and the catalogue entry immediately after
`KeySuperchargerEndBattery`/its entry (replacing the two removed
`KeySuperchargerStartEstimate`/`KeySuperchargerEndEstimate` lines in the same
spot) — `internal/gateway/i18n/catalog.go` groups Supercharger keys together under
one `// --- supercharger stats ---` comment block; keep them there rather than
appending to the end of the file.

Do not fix, shorten, or retranslate this copy (roadmap D5) — it is final.

## Removed surface — exact edits

Restated here as a single checklist so the Test Contract below can reference each
by name; tasks.md assigns these to concrete sub-tasks.

| File | Removed |
|---|---|
| `i18n/catalog.go` | `KeySuperchargerStartEstimate`/`KeySuperchargerEndEstimate` constants + catalogue entries |
| `templates/fragments/supercharger_stats.templ` | Two `i18n.T(ctx, i18n.KeySuperchargerStartEstimate/EndEstimate)` header entries in `superchargerTable`'s `ui.Table` call |
| `templates/fragments/supercharger_row.templ` | Two `<td>{ vm.StartBatteryPctEstLabel }</td>`/`<td>{ vm.EndBatteryPctEstLabel }</td>` in `SuperchargerRow`; `SuperchargerRowError`'s `colspan="9"` → `colspan="7"` (its doc comment's "8 data cells + Actions" → "6 data cells + Actions") |
| `templates/fragments/supercharger_row_edit.templ` | Two `ui.Field` blocks (StartEstimate/EndEstimate) in `SuperchargerRowEdit`; its `<td colspan="9">` → `<td colspan="7">` |
| `templates/fragments/supercharger_vm.go` | `StartBatteryPctEstLabel`/`EndBatteryPctEstLabel` fields (+ their doc comments) on `SuperchargerRowVM` |
| `handlers/supercharger.go` | Two assignments in `superchargerRowVMFromSession`: `StartBatteryPctEstLabel: formatBatteryPct(s.StartBatteryPctEst)`, `EndBatteryPctEstLabel: formatBatteryPct(s.EndBatteryPctEst)` |

`charging.Session.StartBatteryPctEst`/`EndBatteryPctEst` themselves are NOT
touched — they still exist on the struct until tier 2. This tier only removes the
gateway's own read of them.

## Test Contract

Author's note: `handlers/supercharger_test.go` currently has SEVEN places naming
`BatteryPctEst`/`StartEstimate`/`EndEstimate` (confirmed by
`grep -n "BatteryPctEst\|StartEstimate\|EndEstimate" internal/gateway/handlers/supercharger_test.go`
against the tip of `main` at the time this design was written). All seven are
listed below with their exact repair. **This tier's own acceptance grep
(`grep -rn "BatteryPctEst\|StartEstimate\|EndEstimate" internal/gateway/`) scans
test files too** — a fixture like `charging.Session{StartBatteryPctEst:
ptrInt(42)}` must be repaired even though `charging.Session` itself keeps that
field until tier 2, because the NAME still appears inside `internal/gateway/`.

1. **`TestBuildSuperchargerRows_NilEnergyAndCostRenderDash`** — the loop
   `for _, label := range []string{rows[0].StartBatteryPctLabel,
   rows[0].EndBatteryPctLabel, rows[0].StartBatteryPctEstLabel,
   rows[0].EndBatteryPctEstLabel}` drops its last two elements:
   `for _, label := range []string{rows[0].StartBatteryPctLabel,
   rows[0].EndBatteryPctLabel}`. Expected value UNCHANGED: both remaining labels
   are `"—"`.

2. **`TestBuildSuperchargerRows_PopulatedFields`** — the `charging.Session{}`
   fixture drops `StartBatteryPctEst: ptrInt(42)` and `EndBatteryPctEst:
   ptrInt(78)`. The final assertion
   `if rows[0].StartBatteryPctLabel != "40%" || rows[0].EndBatteryPctLabel != "80%"
   || rows[0].StartBatteryPctEstLabel != "42%" || rows[0].EndBatteryPctEstLabel !=
   "78%"` becomes `if rows[0].StartBatteryPctLabel != "40%" ||
   rows[0].EndBatteryPctLabel != "80%"`. Expected value UNCHANGED for the two
   remaining assertions: `"40%"`/`"80%"`.

3. **`TestSuperchargerStatsFragment_RendersBatteryHeadersAndValues`** — the
   `charging.Session{}` fixture drops `StartBatteryPctEst: ptrInt(42)` and
   `EndBatteryPctEst: ptrInt(78)`. The `want` slice
   `[]string{"Batería inicial", "Batería final", "Estimación inicial",
   "Estimación final", "40%", "80%", "42%", "78%"}` becomes
   `[]string{"Batería inicial", "Batería final", "40%", "80%"}`. Expected value
   UNCHANGED for the four remaining strings: all four still render (headers +
   values for start/end battery). The trailing `if strings.Contains(body,
   "País")` guard is untouched.

4. **`TestSuperchargerStatsContent_RendersEnglishHeadersAndNilBatteryValues`** —
   the `fragments.SuperchargerRowVM{}` literal drops
   `StartBatteryPctEstLabel: "—"` and `EndBatteryPctEstLabel: "—"`. The `want`
   slice `[]string{"Start battery", "End battery", "Start estimate", "End
   estimate"}` becomes `[]string{"Start battery", "End battery"}`. Expected value
   UNCHANGED for the two remaining strings.

5. **`TestSuperchargerRowUpdate_BothEmptyClearsBothPercentages`** — the
   `fakeSessionVerifier{result: charging.Session{...}}` fixture drops
   `StartBatteryPctEst: ptrInt(42)` and `EndBatteryPctEst: ptrInt(78)`. The block
   ```go
   if !strings.Contains(body, "42%") || !strings.Contains(body, "78%") {
       t.Errorf("want the (unchanged) estimate cells still rendered, body=%q", body)
   }
   ```
   is deleted outright (there is no longer an estimate cell to assert on). The
   following em-dash count assertion is UNCHANGED and stays correct:
   `if got := strings.Count(body, "—"); got != 2` still expects exactly 2 —
   `EnergyKWh`/`TotalCost`/`Currency` are all non-nil in this fixture, so only the
   two cleared battery-percentage cells render `"—"`; removing the two estimate
   columns removes zero additional em dashes because they were never nil-rendered
   in this fixture's assertions to begin with (the fixture set them to non-nil
   `42`/`78`, and now doesn't set them at all — a field that no longer exists on
   the VM contributes no dash either way).

Every other test in the file (`TestBuildSuperchargerRows_CostLabelCommaGrouped`,
every `TestParseSuperchargerRange_*`, every `TestBuildSuperchargerChart_*`, every
`TestBuildSuperchargerTiles_*`, every `TestSuperchargerRowUpdate_*` other than #5
above, `TestSuperchargerRowEditFragment_NotInWindowIs404`, etc.) is untouched —
none names `BatteryPctEst`/`StartEstimate`/`EndEstimate` and none of their expected
values change.

## Docs

`kkpa/context/workflows/supercharger-stats-read.md` currently carries two bullets
(under "Requirement: Supercharger Stats session table displays battery
percentages") that describe the OLD four-column table. Replace them — and add one
new bullet for the guidance alert — with this exact text, in place:

Replace:
```
- **The four battery columns are start %, end %, start estimate, end estimate — all from `charging.Session`, all pre-formatted in the handler.** A present value renders as its integer percentage plus `%`; a nil value renders **exactly** `"—"`. The template presents strings only. _Source: spec gateway — Requirement: Supercharger Stats session table displays battery percentages._
- **The battery columns add NO read.** They are served from the existing `charging.SessionReader` result — no additional read, write, Tesla API call, or database query may be introduced to display them. _Source: spec gateway — Requirement: Supercharger Stats session table displays battery percentages._
- **The two `_est` columns render `"—"` today and the gateway must never compute one.** Both estimate fields are NULL for every row (no SOC estimator exists — RM27 D11 descoped it), so they must degrade visibly rather than look broken. The gateway calculates and persists nothing. _Source: spec gateway — Requirement: Supercharger Stats session table displays battery percentages._
```

With:
```
- **The two battery columns are start % and end % — both from `charging.Session`, pre-formatted in the handler.** A present value renders as its integer percentage plus `%`; a nil value renders **exactly** `"—"`. The template presents strings only. _Source: spec gateway — Requirement: Supercharger Stats session table displays battery percentages._
- **The battery columns add NO read.** They are served from the existing `charging.SessionReader` result — no additional read, write, Tesla API call, or database query may be introduced to display them. _Source: spec gateway — Requirement: Supercharger Stats session table displays battery percentages._
- **There is no longer an estimate column in the gateway.** `start_battery_pct_est`/`end_battery_pct_est` were removed from the rendered table by `RM41-gateway-revise-battery-pct-ui` (2026-09-03) — they rendered `"—"` on every row since they shipped (no writer ever populated them) and are now gone from the sessions table, the row VM, and the i18n catalogue. `charging.Session` still carries the two fields until tier 2 of `RM41-supercharger-battery-pct-cleanup` drops the columns; the gateway simply stopped naming them. _Source: spec gateway — Requirement: Supercharger Stats session table displays battery percentages._
- **A page-level info alert explains the battery-percentage gap.** `/supercharger-stats` renders one bilingual `ui.Alert{Kind:"info"}` (key `supercharger.battery_pct_help`), unconditionally, above the tiles/chart/table — telling the user Tesla does not supply the percentages and that the system derives the start percentage from the end when only the end is known. _Source: spec gateway — Requirement: Supercharger Stats battery percentage guidance._
```

Do NOT touch `kkpa/context/use-case/charging/verify-session-battery.md`'s "await an
estimator that does not exist yet" gotcha (lines ~104-105) — the roadmap assigns
that correction to tier 2 explicitly, and duplicating the fix here would let tier 2
"fix" an already-fixed line against a stale assumption about what tier 1 left
behind.

## Risks

- **Test-file repair is mechanical but touches five separate test functions** — the
  Test Contract above gives each one's exact before/after so no assertion is
  invented or guessed; `go vet ./internal/gateway/...` (compiles `_test.go` files)
  is the cheap signal that every occurrence was actually caught, not just the ones
  enumerated.
- **The alert's unconditional placement (D1) is this design's own call, not a
  roadmap-settled fact** — flagged explicitly above so a reviewer can see the
  reasoning and reject it if the owner disagrees; the fallback (gate on
  `!v.Empty`) is a one-line `if` wrap around the same `ui.Alert` call if reversed
  later.

**No database design gate applies to this tier.** This change touches no database
object at all — no table, column, index, constraint, view, or migration.
