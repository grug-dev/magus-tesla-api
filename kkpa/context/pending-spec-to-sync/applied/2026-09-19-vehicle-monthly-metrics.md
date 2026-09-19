# Sync proposal — vehicle-monthly-metrics

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `workflows/vehicle-monthly-metrics.md`
Source spec:  `openspec/specs/vehicle-monthly-metrics/spec.md`
Generated:    2026-09-19
Status: APPLIED 2026-09-19

---

## Curator's note — read before applying

**No `## Glossary` block is proposed, on purpose.** The live guide's glossary was rewritten by
hand during RM67 tier 2. It now names both tables and warns that their names are close enough to
send a reader to the wrong module. A spec-derived `REPLACE` would lose that warning and say less.

**No `## Component map` block**, per the mode's own rule: a spec carries behavior, not file paths.
The live Component map is already correct and stays untouched.

What is genuinely new here is the **rules** below. They come from the spec's `SHALL` statements,
which no guide section carried before.

## [guide] ## Conventions & gotchas — APPEND

- **A monthly summary is keyed by vehicle and month alone.** No account is part of the key. The
  row describes a car, not a user.
  _Source: spec vehicle-monthly-metrics — Requirement: A Vehicle's Month Is Summarised Into One Row._
- **Every figure is reported three times: all days, weekdays, weekends.** A weekend day is a
  Saturday or a Sunday. Every other day is a weekday.
  _Source: spec vehicle-monthly-metrics — Requirement: A Vehicle's Month Is Summarised Into One Row._
- **A day with no computable predecessor contributes to nothing — not even the day count.** It is
  excluded from distance, from battery used, and from every count.
  _Source: spec vehicle-monthly-metrics — Requirement: A Vehicle's Month Is Summarised Into One Row._
- **Efficiency is a ratio of sums, never an average of daily ratios.** It is the month's total
  distance divided by the month's total battery used. Only days with a positive battery-used
  figure feed that division.
  _Source: spec vehicle-monthly-metrics — Requirement: A Vehicle's Month Is Summarised Into One Row._
- **A day whose battery use is zero or negative still counts as a tracked day.** It raises the day
  count but stays out of the efficiency division. The two rules pull in opposite directions on
  purpose; do not "fix" one to match the other.
  _Source: spec vehicle-monthly-metrics — Requirement: A Vehicle's Month Is Summarised Into One Row._
- **The day count per figure set is what separates a real zero from no data.** Every column is
  `NOT NULL DEFAULT 0`, so a zero figure alone cannot carry "unknown". Never drop a count column.
  _Source: spec vehicle-monthly-metrics — Requirement: A Vehicle's Month Is Summarised Into One Row._
- **A month with no computable day at all still gets a row, with every figure and count at zero.**
  Absence of input is not absence of a row.
  _Source: spec vehicle-monthly-metrics — Requirement: A Vehicle's Month Is Summarised Into One Row._
- **Summarising a month again replaces the summary; it never adds one.** Exactly one row per
  vehicle and month survives, holding whatever the data said at the second run. This is what makes
  a re-sync safe to call at any time.
  _Source: spec vehicle-monthly-metrics — Requirement: A Vehicle's Month Is Summarised Into One Row._
- **The pack capacity is copied in for that exact month, never joined at read time.** The copy is
  taken from the charging capability's measurement for the same vehicle and month.
  _Source: spec vehicle-monthly-metrics — Requirement: A Month's Summary Carries That Month's Measured Pack Capacity._
- **The summary records whether a capacity was found, and collapses two "no" reasons into one.**
  "Charging recorded no evidence" and "charging recorded evidence too thin to measure" are stored
  identically. A reader of this table cannot tell them apart, and is not meant to.
  _Source: spec vehicle-monthly-metrics — Requirement: A Month's Summary Carries That Month's Measured Pack Capacity._

## [index] ## Workflows — ADD ROWS

| `vehicle-monthly-metrics` (the capability name, as spelled in `openspec/specs/`) | `workflows/vehicle-monthly-metrics.md` |
| `monthly summary` | `workflows/vehicle-monthly-metrics.md` |
| `weekday figures` | `workflows/vehicle-monthly-metrics.md` |
| `weekend figures` | `workflows/vehicle-monthly-metrics.md` |
