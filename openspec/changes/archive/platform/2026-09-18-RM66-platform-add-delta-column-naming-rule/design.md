## Context

`platform` is tier 1 of 3 of `RM66-travel-progress-trends`. It owns no
`internal/` module — it is cross-cutting, per `openspec/config.yaml`'s convention
(`platform` is the canonical domain for changes not owned by one module). Its
sandbox is explicit: `Makefile`, `README.md`, `CLAUDE.md`, `ai/go-conventions.md`,
this change's own OpenSpec folder, and the `unit-of-measure` spec.

Performance profile: **not engaged**. `make delta-guard` is a static grep over
source text, run at `make check` time, never at runtime. It touches no read path
(`ai/architecture.md` §7).

**No database object** is created, changed, or referenced by this tier. It owns no
table, runs no query, adds no migration.

## Repo-wide measurement — verified, not trusted blind

The dispatch's own claim ("the baseline holds exactly the four
`tpms_pressure_*_psi_calc` names ... must shrink to empty when tier 2 lands") was
checked against the current tree before any pattern was finalized, the same way
`RM35-platform-add-tz-guard`'s design verified its own dispatch's counts. The claim
does not hold.

**SQL columns ending in `_calc`, not `_delta_calc`, found in the migrations that
define them** (`grep -rnoE '\b[a-z][a-z0-9_]*_calc\b' --include='*.sql' internal`,
comment lines and `COMMENT ON` prose excluded):

| # | Column | Module | Is it a day-over-day delta? |
|---|---|---|---|
| 1 | `distance_traveled_km_calc` | analytics | Yes — today's odometer minus the predecessor row's. Not renamed by tier 2. |
| 2 | `battery_used_pct_calc` | analytics | Yes — same shape. Not renamed by tier 2. |
| 3 | `days_spanned_calc` | analytics | Yes — a delta of dates. Not renamed by tier 2. |
| 4 | `km_per_pct_calc` | analytics | **No** — `distance / battery_used_pct` for one day, a rate, not a subtraction across days. |
| 5 | `estimated_range_km_calc` | analytics | No — a same-day estimate, not a difference across days. |
| 6 | `tpms_pressure_fl_psi_calc` | analytics | Yes — renamed by tier 2. |
| 7 | `tpms_pressure_fr_psi_calc` | analytics | Yes — renamed by tier 2. |
| 8 | `tpms_pressure_rl_psi_calc` | analytics | Yes — renamed by tier 2. |
| 9 | `tpms_pressure_rr_psi_calc` | analytics | Yes — renamed by tier 2. |
| 10 | `inferred_capacity_kwh_calc` | charging | **No** — `energy_added_kwh / battery_pct_delta` for one row, a same-row ratio (`GENERATED ALWAYS AS`), not a cross-day subtraction. |

**Ten**, not four, and one module the roadmap never mentions (`charging`).

**Go identifiers ending in `Calc`, not `DeltaCalc`**, found the same way
(`grep -rnoE '\b[A-Z][A-Za-z0-9]*Calc\b' --include='*.go' internal cmd`, `_test.go`
excluded):

`DistanceTraveledKmCalc`, `BatteryUsedPctCalc`, `DaysSpannedCalc`,
`KmPerPctCalc`, `EstimatedRangeKmCalc`, `TpmsPressureFLPSICalc`,
`TpmsPressureFRPSICalc`, `TpmsPressureRLPSICalc`, `TpmsPressureRRPSICalc`,
`InferredCapacityKWhCalc` (9 hand-written domain names) **plus**
`TpmsPressureFlPsiCalc`, `TpmsPressureFrPsiCalc`, `TpmsPressureRlPsiCalc`,
`TpmsPressureRrPsiCalc`, `InferredCapacityKwhCalc` (5 sqlc-generated names, under
`internal/<module>/db/models.go` and `query.sql.go` — sqlc does not know `FL`,
`PSI`, or `KWh` as initialisms, so it title-cases each underscore segment on its
own: `fl_psi_calc` → `FlPsiCalc`, `kwh_calc` → `KwhCalc`). **Fifteen**, not the
implied four (or eight, counting both casings of the tpms names).

`km_per_pct_calc` and `inferred_capacity_kwh_calc` matter beyond a bigger count:
they show that "ends in `_calc`, not `_delta_calc`" is not, by itself, proof of a
bad name. Both are legitimately non-delta today, and roadmap tier 2 plans to add a
**new**, separately-named `km_per_pct_delta_calc` column *alongside* the unchanged
`km_per_pct_calc` — two columns, two different meanings, coexisting on purpose.
This is why the guard needs an escape hatch (D-plat-3), not just a baseline: a
baseline only forgives *existing* names, and a future engineer must be able to add
a legitimate new non-delta `_calc` name without the guard treating it as owed a
`_delta_calc` rename.

## Goals / Non-Goals

**Goals:**
- Add `make delta-guard`, wired into `.PHONY` and `check` (this tier does not
  implement the wiring — that is tasks.md's job in a later dispatch — but this
  design specifies its exact shape so implementation needs no further judgment
  call).
- Guard exactly roadmap D-D's invariant: a day-over-day delta column or Go field
  is named `_delta_calc`/`DeltaCalc`, never a bare `_calc`/`Calc`.
- Seed the baseline with **every** name the repo-wide grep finds today (10 SQL,
  15 Go — D-plat-6), not only the four tpms names, so `make check` stays green on
  the current tree once the guard lands.
- Resolve the `unit-of-measure` "Unit Suffix Naming" conflict (below) so the spec
  stops contradicting the schema it describes.
- Document the new target where `CLAUDE.md`'s workflow-documentation rule
  requires it.

**Non-Goals:**
- Renaming any column or Go field. That is tier 2's job (the four tpms names) or
  nobody's job (the other six SQL / seven Go names — see the table above).
- Deciding whether `km_per_pct_calc` or `inferred_capacity_kwh_calc` should ever
  gain an inline `delta:allow` marker. Left to whoever next edits those lines.
- A semantic ("is this really a delta") check. A grep cannot know that; see
  D-plat-3.
- Any database object — this tier owns none and needs none.

## Spec conflict — `unit-of-measure` "Unit Suffix Naming"

The live requirement says a unit-bearing column's name "ends with" its unit
suffix. That is already false: `distance_traveled_km_calc` ends in `_calc`, not
`_km`. It was already false before this roadmap — `_delta_calc` only adds a second
trailing marker to a contradiction that already existed.

**Resolution:** the requirement is corrected (MODIFIED, full text in
`specs/unit-of-measure/spec.md`) so the unit suffix must be the name's last
**unit-bearing** segment — the last segment that names a unit — and may be
followed by exactly one trailing derivation marker, `_calc` or `_delta_calc`,
neither of which is itself a unit. No other suffix may follow. This is not a
scope widening: it describes the schema that already exists
(`distance_traveled_km_calc`, `tpms_pressure_fl_psi_calc`) as compliant, which it
always was in spirit — the old wording just never said so. `_calc` and
`_delta_calc` are the only two markers used anywhere in the current schema
(D-plat-6's grep is exhaustive), so no third marker is invented here.

## Decisions

### D-plat-1 — Two grep legs (SQL, Go), each warn/fail-split by a name baseline

Mirrors `naming-guard`'s exact shape (`Makefile:892-937`): a single pattern finds
every candidate, a baseline regex splits the hits into WARN (a name already in the
baseline) and FAIL (everything else), and a trailing `delta:allow` comment removes
a line before either bucket sees it.

- **Leg 1 (SQL)** scans column **definitions only** — `CREATE TABLE` and
  `ALTER TABLE ... ADD COLUMN` lines inside `internal/*/db/migrations/*.sql` — not
  `query.sql` and not `COMMENT ON` bodies. A column is *named* once, at
  definition; every other file only *uses* the name it was given. Scoping to
  definitions also sidesteps a real hazard: `COMMENT ON` prose narrates other
  column names in English (e.g. "unlike the five `_calc` columns..."), and a
  guard that scanned prose would flag its own documentation.
- **Leg 2 (Go)** scans field-declaration/composite-literal-shaped lines — a tab,
  then a capitalized `...Calc` identifier — across `internal/**/*.go` and
  `cmd/**/*.go`, excluding `_test.go`. This also matches a composite-literal
  field assignment (`FieldCalc: value,`), which has the identical textual shape
  to a declaration. That is accepted, not fixed: it only produces an extra WARN
  row on an already-baselined name, never a wrong FAIL (D-plat-4).

### D-plat-2 — Exact patterns

**SQL leg** (run from the repo root):
```
pattern='^[[:space:]]*(ADD COLUMN[[:space:]]+)?[a-z][a-z0-9_]*_calc\b'
deltapattern='^[[:space:]]*(ADD COLUMN[[:space:]]+)?[a-z][a-z0-9_]*_delta_calc\b'
hits=$(grep -rnE "$pattern" --include='*.sql' internal/*/db/migrations \
  | grep -vE "$deltapattern" \
  | grep -v 'delta:allow')
```
Anchoring to line-start (with an optional `ADD COLUMN ` prefix) is what excludes
`COMMENT ON` lines (they start with `COMMENT`) and mid-string prose (never at
line-start) without a separate comment filter. Verified against the current tree:
this pattern matches all 10 baseline column-definition lines and zero
`COMMENT ON` lines.

**Go leg**:
```
gopattern='^\t+[A-Z][A-Za-z0-9]*Calc\b'
deltago='^\t+[A-Z][A-Za-z0-9]*DeltaCalc\b'
hits=$(grep -rnE "$gopattern" --include='*.go' internal cmd \
  | grep -v '_test.go' \
  | grep -vE "$deltago" \
  | grep -v 'delta:allow')
```

### D-plat-3 — Escape hatch, not just a baseline

A pre-existing name is forgiven by the **baseline** (D-plat-5). A **new** name
that is legitimately not a delta (a future rate or ratio, the same shape as
`km_per_pct_calc` or `inferred_capacity_kwh_calc`) is forgiven by the **escape
hatch**: a trailing `-- delta:allow: <reason>` (SQL) or `// delta:allow: <reason>`
(Go) comment on the same line, mirroring every other guard in this repo
(`tz:allow`, `naming:allow`, `money:allow`). This is the same trade-off
`naming-guard` already accepts for its own closed-list pattern: a guard that
cannot read intent still forces a human decision the moment a new ambiguous name
appears, which is strictly better than silent acceptance. Rejected: teaching the
guard "a ratio/rate is exempt" via a second pattern — there is no reliable textual
signal that distinguishes a rate from a delta; the two existing counterexamples
(`km_per_pct_calc`, `inferred_capacity_kwh_calc`) don't share any lexical marker
that a "not a delta" `_delta_calc` name wouldn't also need.

### D-plat-4 — Composite-literal false positives are accepted, not filtered

Leg 2's pattern also matches a struct literal field assignment, not only a
declaration (D-plat-1). Filtering it out would require distinguishing "inside a
`type ... struct { }` block" from "inside a function body" — a job a line-oriented
grep cannot do (the same class of limitation `RM35-platform-add-tz-guard`'s design
documents for its own guard, R2 in its Known Blind Spots table). The cost is
bounded and non-fatal: every extra hit is the identical identifier text as its own
declaration, so it lands in the same WARN or FAIL bucket the declaration would —
never a false FAIL that a real declaration would not also trigger.

### D-plat-5 — Baseline: every name D-plat-6 finds, not only the tpms four

```
sql_baseline='(distance_traveled_km_calc|battery_used_pct_calc|days_spanned_calc|km_per_pct_calc|estimated_range_km_calc|tpms_pressure_fl_psi_calc|tpms_pressure_fr_psi_calc|tpms_pressure_rl_psi_calc|tpms_pressure_rr_psi_calc|inferred_capacity_kwh_calc)\b'

go_baseline='(DistanceTraveledKmCalc|BatteryUsedPctCalc|DaysSpannedCalc|KmPerPctCalc|EstimatedRangeKmCalc|TpmsPressureFLPSICalc|TpmsPressureFRPSICalc|TpmsPressureRLPSICalc|TpmsPressureRRPSICalc|InferredCapacityKWhCalc|TpmsPressureFlPsiCalc|TpmsPressureFrPsiCalc|TpmsPressureRlPsiCalc|TpmsPressureRrPsiCalc|InferredCapacityKwhCalc)\b'
```
10 SQL names, 15 Go names — the full D-plat-6 tables. **The baseline only ever
shrinks.** When tier 2 renames the four `tpms_pressure_*_psi_calc` columns, it
deletes those four from `sql_baseline` and the matching eight (four hand-written +
four generated) from `go_baseline`. That leaves **6 SQL / 7 Go names still
baselined after tier 2** — `distance_traveled_km_calc`, `battery_used_pct_calc`,
`days_spanned_calc`, `km_per_pct_calc`, `estimated_range_km_calc`,
`inferred_capacity_kwh_calc`, and their Go counterparts (`KmPerPctCalc` is
non-delta so it has no generated-casing sibling to also remove; the other four
DO get an eventual `_delta_calc` sibling added by tier 2, under a *different*
column name — `distance_traveled_km_delta_calc` etc. — but that is a new column,
not a rename of the existing one, so the existing baseline entry is untouched).
This directly corrects the roadmap's "must shrink to empty" claim — it shrinks by
4/8, not to zero, because six of the ten names were never day-over-day deltas of
their own column, or are deltas this roadmap does not touch.

### D-plat-6 — Full baseline tables (restates the measurement above for
implementers who read only the Decisions section)

SQL (10): see the measurement table above.
Go (15): the 9 hand-written names + 5 generated (D-plat-2's list) + `KmPerPctCalc`
already counted among the 9 — total distinct: `DistanceTraveledKmCalc`,
`BatteryUsedPctCalc`, `DaysSpannedCalc`, `KmPerPctCalc`, `EstimatedRangeKmCalc`,
`TpmsPressureFLPSICalc`, `TpmsPressureFRPSICalc`, `TpmsPressureRLPSICalc`,
`TpmsPressureRRPSICalc`, `InferredCapacityKWhCalc`, `TpmsPressureFlPsiCalc`,
`TpmsPressureFrPsiCalc`, `TpmsPressureRlPsiCalc`, `TpmsPressureRrPsiCalc`,
`InferredCapacityKwhCalc` — 15.

### D-plat-7 — `.PHONY` and `check` wiring

`check: build vet lint ui-guard i18n-guard money-guard tz-guard logging-guard
migration-boundary-guard boundary-guard theme-guard vehicleref-guard
tenancy-guard naming-guard archive-guard logdir-guard delta-guard test` —
`delta-guard` inserted immediately before `test`, in the existing guard cluster,
mirroring every prior guard's placement. `.PHONY` gains `delta-guard` in the same
cluster.

### D-plat-8 — No database object (explicit per `openspec/config.yaml`)

This tier creates no table, column, index, constraint, view, or migration, and
modifies none. `openspec/config.yaml`'s design rule requiring a schema +
rationale + index plan for a DB-touching change does not apply — there is no
DB-touching change here.

### D-plat-9 — Does `make check` need to change

Checked: yes. `check:`'s dependency list (`Makefile:1022`) and its own `##` help
comment both enumerate every guard by name, and `README.md:93-94`'s manual
`make check` composition comment does too (though that comment is already stale —
it is missing `logging-guard`, `naming-guard`, and `logdir-guard`, a pre-existing
drift this tier does not own and does not fix beyond adding its own entry in the
right position). `delta-guard` is added to all three per D-plat-7; tasks.md T1
and T3 cover it.

## Test Contract

Authored before any Makefile text exists, per `ai/go-conventions.md` §Testing's
"author expected values up front" rule, restated here for a `make` guard the same
way `RM35-platform-add-tz-guard`'s design restated it for `tz-guard`.

**(a) The guard MUST pass — zero output, exit 0 — on the current tree**, once the
baseline in D-plat-5 is in place. No `delta:allow` marker is needed anywhere on
the current tree; the baseline alone covers every existing name.

**(b) The guard MUST WARN (not fail) on each of these 10 SQL / 15 Go names**,
listed in D-plat-5/D-plat-6, because each is a pre-existing name the baseline
covers.

**(c) The guard MUST FAIL on a genuinely new, unmarked violation**, verified by
temporarily introducing each of these into a scratch migration / scratch `.go`
file, running `make delta-guard`, confirming it fails and names the line, then
removing the scratch violation and confirming the guard is clean again:
1. A new SQL column definition line ending `_calc` that is not one of the 10
   baseline names and carries no `delta:allow` marker — e.g.
   `odometer_km_calc double precision,`.
2. A new Go field declaration ending `Calc` that is not one of the 15 baseline
   names and carries no `delta:allow` marker — e.g. `OdometerKmCalc *float64`.

**(d) The guard MUST NOT flag these inputs** (the negative contract):
- Any name already in the D-plat-5 baseline, anywhere it is declared.
- A new `_delta_calc`/`DeltaCalc` name (the compliant shape).
- A new bare `_calc`/`Calc` name carrying a trailing `delta:allow` marker.
- Any `COMMENT ON` line, any `query.sql` line, any English prose mentioning a
  `_calc` name — leg 1 never scans these files/shapes at all (D-plat-1).
- Any `_test.go` file — leg 2 excludes it unconditionally.

## Risks / Trade-offs

- **[Accepted]** Six SQL names and seven Go names stay baselined forever unless a
  future change either renames them or marks them `delta:allow` — this guard
  neither forces nor blocks that; it is explicitly not this tier's decision
  (Non-Goals).
- **[Accepted, mirrors naming-guard]** The escape hatch (D-plat-3) means a
  determined misuse — marking a genuine delta `delta:allow` instead of renaming
  it — is not caught. The guard stops carelessness, not deliberate evasion, the
  same honest limit every other guard in this repo accepts.
- **[Non-risk, verified]** Leg 1's line-start anchor was checked against every
  `COMMENT ON` line in `internal/analytics/db/migrations/20260917000001_baseline.sql`
  (the file with the densest `_calc` prose) and produced zero false positives.
- **[Non-risk]** No Go logic changes, no database object — this tier is Makefile
  + prose + doc edits only.

## Migration Plan

None — no database object, no code-behavior change. Rollback is a plain revert of
the `Makefile` target, the `.PHONY`/`check` wiring, and the doc edits.
