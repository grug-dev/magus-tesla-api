# Sync proposal — manual-charge-log

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `workflows/manual-charge-crud.md`
Source spec:  `openspec/specs/manual-charge-log/spec.md`
Generated:    2026-08-29
Status: PENDING REVIEW

> **Routing note (for the reviewer).** `manual charge` already resolves to a `workflows/` file
> that predates the use-case/workflow boundary rule, so per the skill's "existing `workflows/`
> files are left alone" rule this proposal **extends that guide in place** rather than creating a
> `use-case/` entry or moving the file. Raised here rather than decided silently — say so if you
> would rather migrate it.

---

## [guide] ## Glossary — REPLACE

- **Known as:** `manual charge`, `charges form`, `charge row`, `editing a manual charge`, `Manual Records page`, `/charges`, `/ui/charges`, `inferred capacity`, `inferred pack capacity`
- **Internal name:** `ChargeCreate` / `ChargeRowUpdate` / `ChargeRowDelete` (gateway handlers) → `charging.Writer` port → `analytics.Recalculator.Recalculate` hook — table `manual_charge_entries`, downstream table `vehicle_metrics`. The inferred pack capacity is `charging.Entry.InferredCapacityKWhCalc` (`*float64`), backed by the database column `manual_charge_entries.inferred_capacity_kwh_calc`.

## [guide] ## Conventions & gotchas — APPEND

- **The inferred pack capacity is derived by the database, never by Go.** It is a
  `GENERATED ALWAYS AS (…) STORED` column, so it is correct on every write path — including
  write paths added in future — with no caller action. Do not add a Go-side computation, and do
  not name the column in any `INSERT`/`UPDATE` column list.
  _Source: spec manual-charge-log — Requirement: Inferred pack capacity is recorded on every entry._
- **The column is unwritable, and that is enforced by the engine.** A caller cannot set, override,
  or corrupt it; a direct write fails with `column "inferred_capacity_kwh_calc" can only be
  updated to DEFAULT` (SQLSTATE `428C9`). Setting the field on the struct passed to
  `Writer.Create` / `Writer.Update` is silently ignored, exactly as `ID` / `CreatedAt` /
  `UpdatedAt` already are.
  _Source: spec manual-charge-log — Requirement: Inferred pack capacity is recorded on every entry._
- **A recorded absence is normal, not an error.** The value exists only when the entry has all
  three inputs (energy added, start %, end %) **and** the end percentage is strictly greater than
  the start. Otherwise it is `NULL` / `nil`, and the entry still creates and edits successfully.
  The strict `>` matters: an equal delta would be a division by zero that would otherwise *reject*
  an ordinary row, and a negative delta would store a negative "capacity", which is not a physical
  quantity.
  _Source: spec manual-charge-log — Requirement: Inferred pack capacity is recorded on every entry._
- **Editing a battery percentage silently changes the recorded capacity.** Changing the end
  percentage from 74 to 84 on a 7.04 kWh entry moves the recorded value from `70.400` to `35.200`
  with no caller involvement. Any read model or cache keyed on this value must be refreshed by the
  same hook that already handles the entry edit.
  _Source: spec manual-charge-log — Requirement: Inferred pack capacity is recorded on every entry._
- **This value is stored, unlike the capability's other derived values.** Cost per kWh, battery
  delta and session duration remain computed on read as value-receiver methods on `charging.Entry`
  and are not persisted. Do not follow their pattern when touching the inferred capacity, or the
  reverse.
  _Source: spec manual-charge-log — Requirement: Inferred pack capacity is recorded on every entry._
- **Small battery deltas produce mathematically valid but practically worthless figures.** A
  1-point delta divides by `0.01`, so a ±0.5% reading error becomes a ±50% capacity error. This is
  accepted deliberately: no minimum-delta floor exists in the column, because filtering is a
  presentation decision. Any consumer that aggregates this value should apply its own floor, or
  prefer a median over a mean.
  _Source: spec manual-charge-log — Requirement: Inferred pack capacity is recorded on every entry._
- **Naming rule for any future stored derived column: `<what>_<unit>_calc`.** That is the shape
  `internal/analytics`' `vehicle_metrics` established and this column follows; it satisfies the
  project's mandatory unit suffix while marking the column as engine-derived.
  _Source: spec manual-charge-log — Requirement: Inferred pack capacity is recorded on every entry._

## [index] ## Glossary & routing — entities — ADD ROWS

| `inferred capacity` | `charging.Entry.InferredCapacityKWhCalc` / `manual_charge_entries.inferred_capacity_kwh_calc` (DB-generated) | entity | `workflows/manual-charge-crud.md` |
| `inferred pack capacity` | synonym of `inferred capacity` | entity | `workflows/manual-charge-crud.md` |
