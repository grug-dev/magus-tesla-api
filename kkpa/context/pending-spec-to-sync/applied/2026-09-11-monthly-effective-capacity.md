# Sync proposal — monthly-effective-capacity

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `workflows/vehicle-monthly-metrics.md`
Source spec:  `openspec/specs/monthly-effective-capacity/spec.md`
Generated:    2026-09-10
Status: APPLIED 2026-09-11

**Routing note (read before applying).** The concept resolved to **nothing** in `INDEX.md`, so
this creates a new guide. Two naming choices, both deliberate:

- The guide is named `vehicle-monthly-metrics`, **not** `monthly-effective-capacity`. Roadmap
  RM52 decision **RD10** fixed that name, because the guide is meant to document the whole
  monthly story, including where a future monthly metric goes. The capability is only the first
  metric.
- It is filed under `workflows/`, not `use-case/`. The job has **no single external trigger** —
  it runs inside the nightly cycle on the first day of the month. That is this skill's own
  use-case/workflow boundary.

**Scope warning.** This spec is tier 1 of RM52. Tier 2 (`app`) adds the monthly step to the
nightly cycle and tier 3 (`platform`) adds the `cmd/monthly-capacity` re-run tool. Neither exists
yet. The blocks below deliberately describe only what tier 1 shipped. **Do not apply this after
tiers 2 and 3 land without re-reading it** — a staged proposal goes stale.

**No `## Component map` block is proposed.** A spec carries behavior, not file paths, and
`--with-filemap` was not passed. The live Component map stays untouched at apply time.

---

## [guide] ## Glossary — REPLACE

- **Known as:** `vehicle monthly metrics`, `monthly effective capacity`, `effective pack capacity`,
  `measured pack capacity`, `monthly capacity`
- **Internal name:** `monthly-effective-capacity` (capability) / `charging.monthly_effective_capacity` (table)

## [guide] ## Conventions & gotchas — APPEND

- **A measured capacity is never a guess.** Below the minimum number of reliable records, the
  measured capacity is left absent and only the record count is stored. An absent value means
  "not enough evidence", never "we estimated it".
  _Source: spec monthly-effective-capacity — Requirement: A Vehicle's Effective Pack Capacity Is Measured Once Per Month._
- **Only non-derived records count as evidence.** A user-logged charge counts only when the person
  supplied the energy. A Supercharger session counts only when it is complete and both battery
  percentages were supplied directly. A record whose figures came from an assumed capacity would
  feed that assumption back into the result.
  _Source: spec monthly-effective-capacity — Requirement: A Vehicle's Effective Pack Capacity Is Measured Once Per Month._
- **Small battery changes are dropped, not corrected.** A record whose battery-percentage change
  is below the reliability threshold contributes to neither the measured capacity nor the sample
  count.
  _Source: spec monthly-effective-capacity — Requirement: A Vehicle's Effective Pack Capacity Is Measured Once Per Month._
- **The median is used, not the average.** It is computed after the unreliable records are
  discarded, so one unusual record cannot dominate the month.
  _Source: spec monthly-effective-capacity — Requirement: A Vehicle's Effective Pack Capacity Is Measured Once Per Month._
- **The vehicle is the key, not the account.** Records logged under different accounts for the
  same vehicle pool into one monthly measurement. A Supercharger session that cannot be attributed
  to a registered vehicle is excluded from every month.
  _Source: spec monthly-effective-capacity — Requirement: A Vehicle's Effective Pack Capacity Is Measured Once Per Month._
- **Computing a month never rewrites history.** No existing charge record's stored energy or
  capacity figures change. A monthly measurement is a new, separate fact.
  _Source: spec monthly-effective-capacity — Requirement: A Vehicle's Effective Pack Capacity Is Measured Once Per Month._
- **Reads take the newest measured month and skip unmeasured ones.** A more recent month with no
  measured capacity does not hide an older measured one.
  _Source: spec monthly-effective-capacity — Requirement: The Newest Measured Capacity Is Used, Skipping Unmeasured Months._
- **Nothing changes until a real measurement exists.** With no measured month for a vehicle, the
  capability falls back to a fixed default capacity, so every earlier calculation behaves exactly
  as it did before.
  _Source: spec monthly-effective-capacity — Requirement: The Newest Measured Capacity Is Used, Skipping Unmeasured Months._

## [index] ## Workflows — ADD ROWS

| `vehicle monthly metrics` (the monthly per-vehicle measurement story; today one metric, effective pack capacity) | `workflows/vehicle-monthly-metrics.md` |
| `monthly effective capacity` (measured pack capacity per vehicle per month; median of reliable charge records, absent when evidence is thin) | `workflows/vehicle-monthly-metrics.md` |
| `effective pack capacity` (synonym of `monthly effective capacity`) | `workflows/vehicle-monthly-metrics.md` |
| `measured pack capacity` (synonym of `monthly effective capacity`) | `workflows/vehicle-monthly-metrics.md` |
