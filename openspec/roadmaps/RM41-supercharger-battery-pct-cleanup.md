# RM41 — Supercharger Battery-Percentage Guidance & Estimate-Column Cleanup

Source ticket: MAG-36 — https://linear.app/magus-monitor/issue/MAG-36/supercharger-session-battery-start-calculated

## Intention

Finish the two parts of MAG-36 that remain after the start-percentage derivation shipped.

1. **Tell the user what the system does.** `/supercharger-stats` gains one bilingual info
   alert explaining that Tesla does not supply the battery percentages, that the user
   should record them, and that supplying only the end percentage makes the system derive
   the start.
2. **Delete the four dead `*_pct_est` columns.** `start_battery_pct_est` and
   `end_battery_pct_est` exist on two tables, are written by nothing in the repo, always
   render `—`, and are now permanently unnecessary — the derivation that shipped writes
   the real `start_battery_pct`, not a snapshot column.

When this roadmap completes, `grep -rn "battery_pct_est\|BatteryPctEst" internal/` returns
only migration history.

## What is already done (do NOT re-implement)

MAG-36's first and largest part — deriving `start_battery_pct` from `energy_kwh`, the end
percentage, and `packCapacityKWh` — **shipped on 2026-09-01** as
`charging-add-derived-start-battery-pct` (branch `ft/CH37-MAG-36-…`, PR #39, archived at
`openspec/changes/archive/2026-09-01-charging-add-derived-start-battery-pct`).

`derivedStartBatteryPct` lives in `internal/charging/capacity.go`. Its formula, rounding
rule, out-of-range behavior, and the "never overwrite a user-supplied start" rule are
settled and binding. This roadmap does not touch it.

## Findings that shaped this roadmap (read before touching anything)

Established by reading the real code, not by reasoning about it.

| Finding | Evidence | Consequence |
|---|---|---|
| The est columns exist on **two** tables in **two** modules | `charging/db/migrations/20260823000001` (as `charge_sessions`, renamed by `20260902000003`); `telemetry/db/migrations/20260815000001` | Two migrations, two tiers — **D2** |
| **Nothing** writes them | Absent from both UPSERT/SET clauses by explicit design; `charging/db/query.sql` and `telemetry/db/query.sql` both carry comments guarding that exclusion | The user's premise is confirmed; the drop loses no data — **D1** |
| The gateway **reads** them from `charging.Session` | `handlers/supercharger.go` → `StartBatteryPctEstLabel: formatBatteryPct(s.StartBatteryPctEst)` | Gateway must stop reading before charging drops the field — **D3** (tier order) |
| `telemetry`'s copies have **no** cross-module consumer | Only `telemetry.go`, `mapping.go`, `service.go` (a comment) and its own integration test | The telemetry tier is independent of the other two — **D3** |
| `charging` and `telemetry` do not read each other's est columns | `charging.go`'s only `SuperchargerHistory` mentions are doc comments about mirroring | No ordering constraint between tiers 2 and 3 — **D3** |
| An **`analytics`** test seeds the columns directly | `analytics/db_integration_test.go` INSERTs into `charging.supercharger_sessions` listing both est columns | One granted path on tier 2, not a fourth tier — **D6** |
| `ui.Alert` already exists and is already used page-level | `templates/ui/alert.templ`; `pages/dashboard.templ` uses `ui.Alert{Kind:"warning", Class:"mb-4"}` | No new kit component — **D4** |
| `ui.Field` has **no** hint slot | `FieldProps` is `Label`/`Error`/`Class`/`Optional` only | An in-form hint would need a shared-kit change; the alert avoids it — **D4** |
| The catalogue treats "Supercharger" as a proper noun in ES | `KeySuperchargerEmpty` = "No hay sesiones de **Supercharger**…" | New ES copy says "Supercharger", never "supercargador" — **D5** |
| 7 test files reference the columns | `charging` ×4, `telemetry` ×1, `gateway/handlers` ×1, `analytics` ×1 | Repair is in scope; new coverage is not — **D7** |

Counts above come from
`grep -rn "battery_pct_est\|BatteryPctEst\|StartEstimate\|EndEstimate" --include=... internal/ cmd/`,
run on the tip of `main` at `5aeade3`.

## Decisions (binding — settled with the owner before any artifact was written)

**D1 — Drop the columns directly; no verify-then-drop step.** The owner chose this over a
migration that first counts non-NULL values. The evidence is already conclusive: both
tables exclude the columns from every write path *by design*, and both exclusions are
guarded by integration tests that assert the columns stay untouched. Rejected: keeping the
columns and marking them deprecated — that preserves exactly the dead schema the ticket
asked to remove.

**D2 — One migration per module, each in its own module's `migrations/` directory.** This
is forced by the module boundary, not chosen: each module owns its schema. `DROP COLUMN`
also drops the column's `CHECK` constraint, so the constraints need no separate statement.
Migration timestamps must sort after `20260903000001` (the newest existing migration).

**D3 — Tier order is `gateway` → `charging` → `telemetry`, and only the first edge is a
real dependency.** The gateway reads `charging.Session.StartBatteryPctEst`, so charging
cannot delete that field while the gateway still names it — tier 2 depends on tier 1.
Telemetry has no consumer outside itself and depends on nothing; it is ordered last only
for readability.

**D4 — The help text is ONE page-level `ui.Alert{Kind:"info"}` above the sessions table.**
Rejected: putting it inside the row edit form, which hides the "record your percentages"
half of the message from anyone who is only reading the table; and a tooltip, which would
need a kit component that does not exist. The alert mirrors `dashboard.templ`'s existing
page-level alert exactly, so no new UI vocabulary is introduced.

**D5 — The copy is the owner's own draft, with its typos and grammar corrected, in both
languages.** Corrections applied: `porcenta`→`porcentaje`, `siempre intenta`→`siempre
intentes`, `precision`→`precisión`, `analisis`→`análisis`. The wording reuses the existing
column vocabulary ("batería inicial"/"batería final") and keeps "Supercharger" as a proper
noun. Final strings, binding, one new key `supercharger.battery_pct_help`:

- **ES:** "Tesla no nos provee los porcentajes de batería inicial y final. Te aconsejamos
  que siempre intentes recordarlos al usar un Supercharger, para tener mejor precisión en
  los análisis. Sin embargo, conociendo solo el porcentaje final, el sistema calculará el
  porcentaje inicial aproximado que tenía el vehículo."
- **EN:** "Tesla does not give us the start and end battery percentages. We recommend you
  always try to remember them when you use a Supercharger, so the analysis is more
  accurate. Still, if you only know the end percentage, the system will calculate the
  approximate start percentage the vehicle had."

**D6 — `analytics/db_integration_test.go` is a granted path on tier 2, not a fourth tier.**
It is a test seed helper that INSERTs into `charging.supercharger_sessions` naming both est
columns, so it stops compiling the moment tier 2 lands. The fix is deleting two arguments
from one INSERT. Creating an `analytics` tier for that would cost a whole dispatch to edit
two lines. The worker gets `internal/analytics/db_integration_test.go` as an explicitly
granted path and may touch **nothing else** under `internal/analytics/`.

**D7 — Unit tests: NEW tests excluded; EXISTING tests repaired.** The owner's standing
default is no new unit tests, and it holds here. But seven test files stop compiling once
the fields and columns go, so reworking them is in scope — that is repair, not new
coverage. The two `telemetry` tests that assert the columns survive a re-UPSERT are
**deleted**, not rewritten: they guard a column that no longer exists.

**D8 — This reverses RM27's design D6, deliberately.** `telemetry.go` currently documents
the columns as *"Kept rather than dropped so the estimator can land later without a
migration."* That reservation is now obsolete: the estimator landed as
`derivedStartBatteryPct` and writes the real `start_battery_pct` column instead. The
reversal is recorded here so a future reader does not treat the drop as an oversight. The
D6 comment block is deleted with the fields it describes.

**D9 — Removing the two visible table columns is confirmed, not a side effect.** The
sessions table goes from 9 columns to 7. The `supercharger.start_estimate` /
`supercharger.end_estimate` catalogue keys and both VM label fields are deleted with them.
This is forced by D1 — once the DB columns are gone there is nothing to render — and the
owner confirmed the narrower table is wanted.

## Tiers

Status legend: `[ ]` pending (change not created) · `[~]` in progress (change created, not
archived) · `[x]` done (archived).

| Status | Change | Module | Scope | depends_on | Proposal prompt |
|---|---|---|---|---|---|
| `[~]` | `RM41-gateway-revise-battery-pct-ui` | `gateway` | Add the `supercharger.battery_pct_help` key (ES+EN, D5) and render it as a `ui.Alert{Kind:"info"}` above the sessions table in `supercharger_stats.templ` (D4). Delete the `supercharger.start_estimate` / `supercharger.end_estimate` keys, the two table headers, the two `<td>`s in `supercharger_row.templ`, the two `ui.Field` blocks in `supercharger_row_edit.templ`, the `StartBatteryPctEstLabel` / `EndBatteryPctEstLabel` VM fields, and their two assignments in `handlers/supercharger.go` (D9). Repair `handlers/supercharger_test.go` (D7). Re-run `templ generate`. The gateway stops naming `BatteryPctEst` entirely — that is what unblocks tier 2. | — | *(artifacts produced in this run)* |
| `[ ]` | `RM41-charging-drop-estimate-columns` | `charging` | New migration dropping `start_battery_pct_est` / `end_battery_pct_est` from `charging.supercharger_sessions` (D1, D2). Remove the two `Session` fields in `charging.go`, the two mappings in `session_reader.go`, and the guarding comments in `db/query.sql`; re-run `sqlc generate`. Repair the four `charging` integration tests plus the granted path `internal/analytics/db_integration_test.go` (D6, D7). | 1 | Generate the OpenSpec proposal for dropping `start_battery_pct_est` and `end_battery_pct_est` from `charging.supercharger_sessions`. Binding decisions: D1, D2, D6, D7 of this roadmap. The gateway already stopped reading these fields in tier 1. |
| `[ ]` | `RM41-telemetry-drop-estimate-columns` | `telemetry` | New migration dropping `start_battery_pct_est` / `end_battery_pct_est` from `telemetry.supercharger_history` (D1, D2). Remove the two struct fields and the RM27-D6 comment block in `telemetry.go` (D8), the two mappings in `mapping.go`, and the stale comment in `service.go`; re-run `sqlc generate`. Delete the assertions in `db_supercharger_battery_pct_integration_test.go` that guard the dropped columns (D7). | — | Generate the OpenSpec proposal for dropping `start_battery_pct_est` and `end_battery_pct_est` from `telemetry.supercharger_history`. Binding decisions: D1, D2, D7, D8 of this roadmap. These columns have no consumer outside `internal/telemetry`. |

All three tiers commit to the single shared branch
`ft/RM41-MAG-36-supercharger-battery-pct-cleanup`.

## Future work

None descoped from MAG-36 — this roadmap closes the ticket's remaining scope.

One pre-existing observation recorded but explicitly **not** actioned here: the KB guide
`kkpa/context/use-case/charging/verify-session-battery.md` carries a gotcha stating the est
columns "await an estimator that does not exist yet". That line becomes wrong when tier 2
lands and must be corrected in tier 2's own change, per `CLAUDE.md`'s docs-track-change
rule — it is a doc fix inside the tier, not separate future work.

A second, genuinely separate observation: `analytics/db_integration_test.go` writing
directly into `charging.supercharger_sessions` crosses a module boundary in test code.
Tier 2 only removes two arguments from that INSERT; it does **not** fix the boundary
crossing. Whether test-only cross-module seeding is acceptable is a question for
`ai/architecture.md`, not for this ticket.
