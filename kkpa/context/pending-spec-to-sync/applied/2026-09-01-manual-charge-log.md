# Sync proposal — manual-charge-log

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `workflows/manual-charge-crud.md`
Source spec:  `openspec/specs/manual-charge-log/spec.md`
Generated:    2026-08-29
Status: APPLIED 2026-09-01

> **Routing note (for the reviewer).** `manual charge` already resolves to a `workflows/` file
> that predates the use-case/workflow boundary rule, so per the skill's "existing `workflows/`
> files are left alone" rule this proposal **extends that guide in place** rather than creating a
> `use-case/` entry or moving the file. Raised here rather than decided silently — say so if you
> would rather migrate it.
>
> **This draft REPLACES the earlier pending one (MAG-25, inferred capacity), which was never
> applied.** It is a superset, not a narrowing: the main spec now carries MAG-25's and MAG-18's
> requirements merged, so every block from the earlier draft is carried forward here verbatim and
> the MAG-18 material is added alongside it. Applying this one applies both changes. Nothing from
> the MAG-25 draft was dropped.
>
> **On what got an INDEX row and what did not.** The skill forbids a row per attribute of a
> concept. `entry status` and `energy source` are proposed as rows anyway, on the same footing the
> already-pending `inferred capacity` rows claim: each names a concept somebody asks about by name
> without thinking "field of a manual charge", and `energy source` in particular exists only to let
> a future capacity average exclude its own output. `odometer` is deliberately **not** proposed —
> it is a plain attribute and routes to this guide through `manual charge` anyway. Trim or extend
> that block as you see fit; this is the judgment most worth your two minutes.

---

## [guide] ## Glossary — REPLACE

- **Known as:** `manual charge`, `charges form`, `charge row`, `editing a manual charge`, `Manual Records page`, `/charges`, `/ui/charges`, `inferred capacity`, `inferred pack capacity`, `entry status`, `charge status`, `in progress charge`, `energy source`, `energy provenance`, `odometer reading`
- **Internal name:** `ChargeCreate` / `ChargeRowUpdate` / `ChargeRowDelete` (gateway handlers) → `charging.Writer` port → `analytics.Recalculator.Recalculate` hook — table `manual_charge_entries`, downstream table `vehicle_metrics`. The inferred pack capacity is `charging.Entry.InferredCapacityKWhCalc` (`*float64`), backed by the database column `manual_charge_entries.inferred_capacity_kwh_calc`. The lifecycle status is `charging.Status` (`StatusInProgress` / `StatusDone`) on `charging.Entry.Status`, its required-field rule is `charging.RequiredFieldsFor`, and the energy provenance is `charging.EnergySource` (`EnergySourceUser` / `EnergySourceEstimated`) on `charging.Entry.EnergySource`.

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
- **Which fields an entry must carry is a function of its status, and that rule lives in exactly
  one place.** `RequiredFieldsFor` is the sole source of truth: the capability enforces exactly it
  on every create and every edit, for every caller, and any presentation layer deciding which
  inputs to mark required must READ it rather than restate it. Adding a field to the rule must
  stay a one-place change — if you find yourself writing a second check, you have just given the
  single source of truth a second source.
  _Source: spec manual-charge-log — Requirement: A charge entry has a recorded status that governs its required fields._
- **The rule is deliberately NOT a database CHECK.** Enforcing it in the schema would turn every
  future change to the required-field set into a migration, which is precisely what the rule is
  shaped to avoid. Do not "harden" it by adding a constraint.
  _Source: spec manual-charge-log — Requirement: A charge entry has a recorded status that governs its required fields._
- **An absent status means in progress; an unrecognized one is rejected before any write.** These
  are different outcomes for different inputs, and the distinction is what lets a caller that does
  not yet send a status keep working while a typo still fails loudly.
  _Source: spec manual-charge-log — Requirement: A charge entry has a recorded status that governs its required fields._
- **There is no transition rule — done may be reopened.** The capability does not restrict which
  status an entry moves to. Do not add a guard against done → in progress; it is explicitly
  permitted.
  _Source: spec manual-charge-log — Requirement: A charge entry has a recorded status that governs its required fields._
- **Entries that pre-date the status are recorded as in progress, on purpose.** Historical entries
  surface as unreviewed rather than being silently asserted complete. This was chosen over the
  more flattering default; it is not an oversight to "correct".
  _Source: spec manual-charge-log — Requirement: A charge entry has a recorded status that governs its required fields._
- **Energy added is optional, but zero and negative are still rejected.** Only the *absence* of a
  value became permissible. Never substitute a fabricated `0` for an unknown energy — it is both a
  lie about the charge and a constraint violation.
  _Source: spec manual-charge-log — Requirement: Energy added is optional, may be derived on write, and records its provenance._
- **Energy is derived ON WRITE and never recomputed on read.** When the caller supplies none and
  the entry has both percentages with a strictly positive difference, the capability derives the
  value from the pack capacity and stores it. In every other case it stores exactly what the
  caller gave, including nothing. Do not add a read-time fallback — that would make the same entry
  report different energy as the capacity constant changes.
  _Source: spec manual-charge-log — Requirement: Energy added is optional, may be derived on write, and records its provenance._
- **Provenance is the capability's to assert, never the caller's.** A caller-supplied energy
  source is ignored, and provenance is re-determined on every write — so replacing a derived value
  with a typed one flips it back to "from the person". Read it on the way out; never trust it on
  the way in.
  _Source: spec manual-charge-log — Requirement: Energy added is optional, may be derived on write, and records its provenance._
- **The pack capacity comes from ONE named place, and that is the point.** Replacing today's fixed
  figure with a real per-vehicle value must stay a one-place change. **Any future averaging of
  inferred capacities MUST exclude derived rows** — on a derived entry the recorded inferred
  capacity is arithmetically equal to the capacity the derivation used, so including those rows
  averages the seed value back into itself and never converges. That is the entire reason
  provenance is stored.
  _Source: spec manual-charge-log — Requirement: Energy added is optional, may be derived on write, and records its provenance._
- **An entry with no energy records no inferred capacity, and that is not an error.** The
  pre-existing inferred-capacity behaviour is unchanged by energy becoming optional; a missing
  energy simply lands in the same "no recorded capacity" case a missing percentage already did.
  _Source: spec manual-charge-log — Requirement: Energy added is optional, may be derived on write, and records its provenance._
- **The odometer reading belongs to the charge EVENT, not to the vehicle.** Two entries for the
  same vehicle carry two independent readings, which is why it lives on the entry and not on a
  vehicle record. It is optional, in whole kilometres, and negative is rejected.
  _Source: spec manual-charge-log — Requirement: An entry records the odometer reading taken at the charge event._

## [index] ## Glossary & routing — entities — ADD ROWS

| `inferred capacity` | `charging.Entry.InferredCapacityKWhCalc` / `manual_charge_entries.inferred_capacity_kwh_calc` (DB-generated) | entity | `workflows/manual-charge-crud.md` |
| `inferred pack capacity` | synonym of `inferred capacity` | entity | `workflows/manual-charge-crud.md` |
| `entry status` | `charging.Status` (`IN_PROGRESS` / `DONE`) / `charging.RequiredFieldsFor` — the status-to-required-fields rule | entity | `workflows/manual-charge-crud.md` |
| `charge status` | synonym of `entry status` | entity | `workflows/manual-charge-crud.md` |
| `energy source` | `charging.EnergySource` (`USER` / `ESTIMATED`) / `manual_charge_entries.energy_source` — module-computed, never caller-supplied | entity | `workflows/manual-charge-crud.md` |
| `energy provenance` | synonym of `energy source` | entity | `workflows/manual-charge-crud.md` |
