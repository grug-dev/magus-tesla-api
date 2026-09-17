# Sync proposal — monthly-effective-capacity

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `workflows/vehicle-monthly-metrics.md`
Source spec:  `openspec/specs/monthly-effective-capacity/spec.md`
Generated:    2026-09-16
Status: APPLIED 2026-09-16

---

**Why this one matters.** The spec's validity rule gained a second condition for manual entries.
Two places in the target guide state the old, one-condition rule and are now wrong: the
`### Source data` table row for `charging.manual_charge_entries` (inside `## Component map`), and
the "Only non-derived records count as evidence" bullet. A reader trusting either would conclude
a derived row still counts.

The `## Component map` block below is the one exception to the no-filemap rule, and it is not a
file map: it is a row of the same table stating **which rows count as evidence**, which is
exactly what this spec's requirement governs. It names no new file and no new symbol.

## [guide] ## Component map — REPLACE

Files involved, grouped by layer. Each row: the file's role in this concept.

PRESERVE the existing `## Component map` body exactly as it stands, with ONE line changed — the
`charging.manual_charge_entries` row of the `### Source data (read-only inputs — same module)`
table becomes:

| `charging.manual_charge_entries` | `energy_source = 'USER'` **and** `start_battery_source = 'USER'`, with a non-NULL `inferred_capacity_kwh_calc`. Two independent provenances; a row failing either one is not evidence. |

Change nothing else in this section.

## [guide] ## Conventions & gotchas — APPEND

- **A manual entry must pass BOTH provenance checks, not one.** The energy must have been typed
  by the person, and so must the starting percentage. The two are recorded and computed
  separately, so a row can fail either on its own. The bullet above that names only the energy is
  the older, one-condition rule and no longer holds.
  _Source: spec monthly-effective-capacity — Requirement: A Vehicle's Effective Pack Capacity Is Measured Once Per Month._
- **Why the starting percentage matters here at all.** A derived starting percentage is computed
  by dividing the energy by a pack capacity. The generated `inferred_capacity_kwh_calc` then
  comes back equal to that same capacity, by algebra. Counting such a row would feed the
  measurement back into itself, exactly like a derived energy does.
  _Source: spec monthly-effective-capacity — Requirement: A Vehicle's Effective Pack Capacity Is Measured Once Per Month._
