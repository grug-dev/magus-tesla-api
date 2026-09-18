# Sync proposal — analytics

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `entities/vehicle-metrics/guide.md`
Source spec:  `openspec/specs/analytics/spec.md`
Generated:    2026-09-18
Status: APPLIED 2026-09-18

Delta scope: the spec gained one requirement (`Precomputed Travel-Progress Day-Over-Day
Deltas`) and one was modified (`Latest Vehicle Status Per Account`). The guide's column lists,
field lists and history line were already brought up to date by hand in the same change, so
this proposal carries only what the spec adds beyond that: three aliases and two rules.

---

## [guide] ## Glossary — APPEND

- **Known as (added):** `travel progress delta`, `travel progress trend`, `day-over-day delta`

## [guide] ## Conventions & gotchas — APPEND

- **A delta absent because of the pass boundary is not permanent — a later pass that includes both days produces it normally.** The first day a recalculation pass considers has no earlier day in that pass, so its three travel-progress deltas are absent. That is a property of the pass, not of the stored history. A `Reconcile` over the whole history, or any later pass that contains the day before it, fills them. Never treat the absence as a permanent fact about that day.
  _Source: spec analytics — Requirement: Precomputed Travel-Progress Day-Over-Day Deltas._
- **A latest row whose day predates delta tracking reports the three travel-progress deltas as absent, never as `0`.** This is the same rule the eight status observations and the four tyre-pressure columns already follow. The row keeps its own battery, range, odometer, status, tyre-pressure and travel-progress figures; only the deltas are absent. A fabricated `0` would be read as "no change", which is a different fact.
  _Source: spec analytics — Requirement: Latest Vehicle Status Per Account._

## [index] ## Entities — ADD ROWS

| `travel progress delta` | the three `*_delta_calc` columns of `vehicle_metrics` — each day's travel-progress figure minus the previous day's, absent without a predecessor in the same pass | entity | `entities/vehicle-metrics/guide.md` |
| `travel progress trend` | synonym of `travel progress delta` | entity | `entities/vehicle-metrics/guide.md` |
| `day-over-day delta` | synonym of `travel progress delta`; see also `tyre pressure delta` | entity | `entities/vehicle-metrics/guide.md` |

---

## Note for the applier — one row this proposal CANNOT fix

`INDEX.md`'s existing `tyre pressure delta` row still names the old column spelling
`tpms_pressure_*_psi_calc`. `ADD ROWS` skips any row whose first cell already exists, so it
cannot correct that row. Fix it by hand to `tpms_pressure_*_psi_delta_calc`.
