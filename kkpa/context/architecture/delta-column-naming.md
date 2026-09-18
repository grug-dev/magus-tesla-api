# Delta column naming — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.
> All KB links are relative to `kkpa/context/`.

## Glossary

- **Known as:** `delta column naming`, `_delta_calc`, `delta-guard`, `delta column suffix`
- **Internal name:** `make delta-guard` (`Makefile`), the rule prose in `ai/go-conventions.md`

## Component map

Files involved, grouped by layer. Each row: the file's role in this concept.

### Rule and enforcement

| File | Role |
|---|---|
| `ai/go-conventions.md` | The rule itself, beside the unit-suffix rule. What an agent reads before naming a column. |
| `Makefile` | The `delta-guard` target — two grep legs, the baseline lists, the `check` wiring. |
| `README.md` | Lists `delta-guard` in the `make check` composition comment. |
| `CLAUDE.md` | Lists `make delta-guard` as a command Claude may run, and in the Test-Execution-Policy block. |
| `openspec/specs/platform/spec.md` | Requirement `Delta Column Naming Guard` — the behaviour the guard must have. |
| `openspec/specs/unit-of-measure/spec.md` | Requirement `Unit Suffix Naming` — where the marker sits relative to the unit suffix. |

### Columns the rule governs

| File | Role |
|---|---|
| `internal/analytics/db/migrations/` | Defines 9 of the 10 baselined SQL `_calc` names. |
| `internal/charging/db/migrations/` | Defines `inferred_capacity_kwh_calc` — a same-row ratio, not a delta. |

## How maintenance works

The rule: a column or Go field that stores a **day-over-day change** — today's value of a metric
minus yesterday's value of the **same** metric — is named `_delta_calc` (Go: `DeltaCalc`). A bare
`_calc` / `Calc` stays correct for a derived value that is **not** a day-over-day difference: a
same-day rate (`km_per_pct_calc`) or a same-row ratio (`inferred_capacity_kwh_calc`).

`make delta-guard` enforces it, and runs inside `make check`. It has two grep legs:

1. **SQL leg** — scans column **definition** lines only (`CREATE TABLE` / `ADD COLUMN`) under
   `internal/*/db/migrations/`. It never scans `query.sql` or `COMMENT ON` prose. A column is
   named once, at its definition; everywhere else only uses that name. A guard that read prose
   would flag its own English documentation.
2. **Go leg** — scans field-declaration-shaped lines (a tab, then a capitalized `...Calc`
   identifier) under `internal/` and `cmd/`, excluding `_test.go`. A struct literal field
   assignment has the same shape and also matches. That is accepted: it lands in the same bucket
   the real declaration already would, so it never causes a wrong failure.

Each leg splits its hits against a **baseline** of names that existed before the rule:

- a baselined name **warns** — not fatal, so `make check` stays green;
- any other unmarked name **fails** with exit 1, and the guard names the file and line.

**The baseline only ever shrinks.** When a change renames a baselined name to `_delta_calc` /
`DeltaCalc`, delete its entry from the `delta-guard` target. Never add a name to the baseline.

**Escape hatch:** a trailing `-- delta:allow: <reason>` (SQL) or `// delta:allow: <reason>` (Go)
comment on the same line. Use it for a genuinely new non-delta `_calc` name. Never widen a
pattern to silence a true positive.

- **Add a delta column:** name it `<what>_<unit>_delta_calc` → write the migration under
  `internal/<module>/db/migrations/` → run `sqlc generate`, so the Go name becomes `...DeltaCalc`
  → run `make delta-guard` and confirm no new failure.
- **Rename a baselined column to a delta name:** write the migration → `sqlc generate` → delete
  that name from the **Go** baseline list in the `Makefile` → run `make delta-guard`. The printed
  Go baseline size must drop. **The SQL baseline usually cannot shrink** — see the gotcha below.
- **Add a new derived column that is NOT a delta:** name it `<what>_<unit>_calc` and mark the
  definition line `-- delta:allow: <reason>`. Do not add it to the baseline.

## Conventions & gotchas

- **A day-over-day delta is named `_delta_calc` / `DeltaCalc`, never a bare `_calc` / `Calc`.**
  A bare `_calc` says the value is derived, but not that it compares two days. A reader can
  mistake it for the day's own figure.
  _Source: spec platform — Requirement: Delta Column Naming Guard._
- **A bare `_calc` is not automatically wrong.** `km_per_pct_calc` is a same-day rate and
  `inferred_capacity_kwh_calc` is a same-row ratio. Neither is a delta. This is why the guard
  needs the `delta:allow` escape hatch and not only a baseline.
  _Source: spec platform — Requirement: Delta Column Naming Guard._
- **The unit suffix comes before the marker.** `tpms_pressure_fl_psi_delta_calc`, not
  `tpms_pressure_fl_delta_calc_psi`. The unit suffix is the last unit-bearing segment; exactly
  one derivation marker may follow it, and nothing may follow that.
  _Source: spec unit-of-measure — Requirement: Unit Suffix Naming._
- **The guard scans definitions, never uses.** `query.sql`, `COMMENT ON` bodies and `_test.go`
  are out of scope on purpose. Do not "fix" the guard to scan them — it would flag its own
  documentation.
  _Source: spec platform — Requirement: Delta Column Naming Guard._
- **The baseline only shrinks, never grows.** A rename deletes its baseline entry. A new name
  that needs an exemption gets `delta:allow`, not a new baseline entry.
  _Source: spec platform — Requirement: Delta Column Naming Guard._
- **The success line counts distinct names, not matched lines.** The Go leg matches struct
  literal fields too, so one baselined name matches many times. `10 SQL / 7 Go` is the real
  baseline size today.
  _Source: spec platform — Requirement: Delta Column Naming Guard._
- **A rename shrinks the Go baseline, almost never the SQL one.** The SQL leg reads column
  definitions, and a column is defined once, in the module's frozen baseline migration. A rename
  lives in a later migration, so the old definition line stays in the tree for good. Deleting its
  SQL baseline entry turns that line into a hard failure. The four `tpms_pressure_*_psi_calc`
  names are renamed and still baselined for exactly this reason.
  _Source: `Makefile` — the `delta-guard` target's `sqlbaseline` list._
- **The delta-exclusion patterns run against `grep -rn` output.** Each line carries a
  `path:line:` prefix, so an exclusion pattern anchored with a bare `^` can never match, and a
  correctly named `_delta_calc` column is reported as a violation. Both patterns accept the
  prefix. Do not re-anchor them.
  _Source: `Makefile` — the `delta-guard` target's `sqldeltapattern` / `godeltapattern`._

## Related KB

- Architecture: `architecture/charging-tables.md` — every charging-specific column decision,
  including `inferred_capacity_kwh_calc`.
- Architecture: `architecture/telemetry-tables.md` — the units history on the telemetry tables.
- Entities: `entities/vehicle-metrics/guide.md` — the `_calc` columns of `vehicle_metrics`,
  including the tyre-pressure deltas RM66 tier 2 renames.
