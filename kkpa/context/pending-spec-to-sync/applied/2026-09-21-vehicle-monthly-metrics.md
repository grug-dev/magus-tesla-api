# Sync proposal — vehicle-monthly-metrics

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `workflows/vehicle-monthly-metrics.md`
Source spec:  `openspec/specs/vehicle-monthly-metrics/spec.md`
Generated:    `2026-09-21`
Status: APPLIED 2026-09-21

---

<!--
Derived from the three requirements RM67 tier 3 added to this capability:
  - A Month's Summary Carries That Month's External Charging Totals, Split By Type
  - A Month's Summary Carries That Month's Supercharger Totals
  - A Month's Summary States The Currency Its Charging Totals Are Expressed In
No `## Component map` block: spec.md carries behavior, not file paths, so the live
Component map stays untouched.
-->

## [guide] ## Glossary — REPLACE

- **Known as:** `vehicle monthly metrics`, `monthly metrics`, `monthly effective capacity`,
  `effective pack capacity`, `measured pack capacity`, `monthly capacity`, `pack capacity`,
  `capacity backfill`, `monthly distance`, `monthly efficiency`, `weekday weekend split`,
  `monthly charging totals`, `ending battery distribution`
- **Internal names — TWO tables in TWO modules. Read this before you grep:**
  - `charging.monthly_effective_capacity` — the measured pack capacity. Job
    `charging.MonthlyCapacityCalculator.Calculate`; **two** read sides — `packCapacityKWh`
    (`internal/charging/capacity.go`, module-internal) and the public
    `charging.MonthlyCapacityReader` port (`internal/charging/monthly_capacity_reader.go`).
  - `analytics.vehicle_monthly_metrics` — the wider per-vehicle per-month rollup. Written by
    `analytics.MonthlySyncer.SyncMonth` (`internal/analytics/monthly_sync.go`).

## [guide] ## Conventions & gotchas — APPEND

- **The month's charging totals come from two sources, kept apart.** External charges split
  into two groups by charging type, AC and DC. Supercharger sessions are a third group. Each
  group carries its own energy, cost and record count.
  _Source: spec vehicle-monthly-metrics — Requirement: A Month's Summary Carries That Month's External Charging Totals, Split By Type._

- **An external charge with no charging type is dropped whole.** It feeds neither group: no
  energy, no cost, no count, no bucket. There is no third column for it.
  _Source: spec vehicle-monthly-metrics — Requirement: A Month's Summary Carries That Month's External Charging Totals, Split By Type._

- **Every external charge counts, whatever its lifecycle status.** There is no filter on an
  incomplete record. Filtering would drop most of a typical month.
  _Source: spec vehicle-monthly-metrics — Requirement: A Month's Summary Carries That Month's External Charging Totals, Split By Type._

- **A missing energy or cost adds zero but still counts.** The record raises the group's count
  and leaves the sum alone. A count that exceeds its distribution total is normal, not a bug.
  _Source: spec vehicle-monthly-metrics — Requirement: A Month's Summary Carries That Month's External Charging Totals, Split By Type._

- **The five ending-battery ranges are half-open except the last.** They are 0-20, 20-40,
  40-60, 60-80 and 80-100, where only 80-100 includes its upper edge. Closed edges everywhere
  would count 20, 40, 60 and 80 twice. A record with no ending battery percentage counts but
  enters no range, so a distribution can sum to less than its group's count.
  _Source: spec vehicle-monthly-metrics — Requirement: A Month's Summary Carries That Month's External Charging Totals, Split By Type._

- **A Supercharger session's month is decided by the platform's own time zone.** It is the
  session's ending time, read in that zone — never UTC, never another zone. The two sources are
  dated differently on purpose: an external charge keeps its own charge date.
  _Source: spec vehicle-monthly-metrics — Requirement: A Month's Summary Carries That Month's Supercharger Totals._

- **One currency is recorded for the whole month.** Every record's cost is summed into its
  group, whatever currency that single record recorded. A month mixing currencies gets a mixed
  total, and the summary still names one currency.
  _Source: spec vehicle-monthly-metrics — Requirement: A Month's Summary States The Currency Its Charging Totals Are Expressed In._

## [index] ## Workflows — ADD ROWS

| `monthly charging totals` (the month's external AC/DC and Supercharger energy, cost and counts) | `workflows/vehicle-monthly-metrics.md` |
| `ending battery distribution` (five ranges 0-20/20-40/40-60/60-80/80-100, last edge closed) | `workflows/vehicle-monthly-metrics.md` |
| `ext_ac` / `ext_dc` / `sc_` columns | the rollup's charging columns → `workflows/vehicle-monthly-metrics.md` |
