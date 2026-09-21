# Sync proposal — monthly-effective-capacity

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `workflows/vehicle-monthly-metrics.md`
Source spec:  `openspec/specs/monthly-effective-capacity/spec.md`
Generated:    2026-09-18
Status: APPLIED 2026-09-18

---

## [guide] ## Conventions & gotchas — APPEND

- **The table has two reads, and they answer different questions.** One asks "what capacity should
  today's maths use" and takes the newest measured month. The other asks "what did this vehicle
  measure in this exact month" and never looks at another month. Never make one serve both: the
  first is allowed to substitute an older month, which is exactly what the second must not do.
  _Source: spec monthly-effective-capacity — Requirement: A Vehicle's Monthly Measurement Is Retrievable For An Exact Month._
- **The exact-month read reports three outcomes, not two.** No measurement recorded; a measurement
  recorded with no capacity; a measurement recorded with a capacity. The first two must stay
  distinguishable. Both carry no capacity value, so a caller that only checks "is the capacity
  absent" cannot tell an unmeasured month from a month nobody ever processed.
  _Source: spec monthly-effective-capacity — Requirement: A Vehicle's Monthly Measurement Is Retrievable For An Exact Month._
- **The exact-month read accepts any day inside the month.** The 1st, the 15th and the last day all
  return the same result. The caller never has to build the first day of the month itself — which
  matters, because building one in Go means a hand-rolled midnight outside `internal/clock`.
  _Source: spec monthly-effective-capacity — Requirement: A Vehicle's Monthly Measurement Is Retrievable For An Exact Month._
- **The exact-month read never leaks across a vehicle or a month.** Another vehicle's row for the
  same month, and the same vehicle's row for another month, are both excluded — even when the
  adjacent month has its own measurement.
  _Source: spec monthly-effective-capacity — Requirement: A Vehicle's Monthly Measurement Is Retrievable For An Exact Month._
- **Reading never writes.** The exact-month read alters no stored measurement and starts no new
  measurement. Computing a month stays the job's work, never a read's side effect.
  _Source: spec monthly-effective-capacity — Requirement: A Vehicle's Monthly Measurement Is Retrievable For An Exact Month._

## [index] ## Workflows — ADD ROWS

| `capacity for an exact month` | `workflows/vehicle-monthly-metrics.md` |
| `monthly capacity read port` | synonym of `capacity for an exact month` → `workflows/vehicle-monthly-metrics.md` |
