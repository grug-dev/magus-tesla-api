# Sync proposal — charge-session-log

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `workflows/supercharger-stats-read.md`
Source spec:  `openspec/specs/charge-session-log/spec.md`
Generated:    2026-09-06
Status: PENDING REVIEW

Derived from the 3 requirements modified by `RM44-charging-add-change-detecting-mirror`
(MAG-48, roadmap RM44 tier 3). The capability already routes to an existing `workflows/`
guide, so this proposal extends that guide instead of creating a second entry.

No `## Component map` block is proposed — `spec.md` carries behaviour, not file paths, so the
live Component map is preserved untouched. `--with-filemap` was not passed.

---

## [guide] ## Conventions & gotchas — APPEND

- **`updated_at` on a charge session record means "this row's data changed", not "the last
  sync touched this row".** A synchronization pass that writes the same energy, cost,
  currency, payment status and registered vehicle identifier the record already holds does
  NOT advance it. Before MAG-48 every pass advanced it, which made `internal/analytics`
  recalculate the whole history every night.
  _Source: spec charge-session-log — Requirement: Charge Sessions Are Retrievable For A Vehicle By Recency Of Update._

- **A human battery-% correction and a sync pass can never be mistaken for each other, in
  either direction.** The comparison neither reads nor is influenced by the record's verified
  percentages or its lifecycle status. So a human correction is never read as a sync change,
  and a sync pass never looks like a human correction.
  _Source: spec charge-session-log — Requirement: The Charge Session Log Is Synchronized From The Source._

- **The registered vehicle identifier going from absent to present DOES count as a change.**
  That is how a session becomes visible again once its vehicle is re-registered. If you ever
  exclude this field from the comparison, the orphan-recovery path breaks silently.
  _Source: spec charge-session-log — Requirement: The Registered Vehicle Identifier Is Refreshed On Every Synchronization._

- **A source disagreement on the charging site is NOT a change — on any night, not just the
  first.** The record keeps its original site name, and the disagreement is not treated as
  new information. This matters because the site name is never updated after first recording:
  if it were compared, the mismatch could never resolve, and the record would look modified
  every single night forever.
  _Source: spec charge-session-log — Requirement: The Charging Site Is Fixed On Record._

- **Rule to keep this correct when you change the sync:** the change comparison covers exactly
  the columns the synchronization writes, and nothing else. Add a column to what the sync
  writes → include it in the comparison. Add a column the sync does not write → exclude it.
  `internal/charging/db_mirror_schema_selfcheck_integration_test.go` fails the moment a live
  column belongs to neither list, and names the column plus which list to fix.
  _Source: spec charge-session-log — Requirement: The Charge Session Log Is Synchronized From The Source._

## [index] ## Glossary & routing — entities — ADD ROWS

| `session change detection` | when a nightly sync counts as a modification of a charge session record (`updated_at`) | entity | `workflows/supercharger-stats-read.md` |
| `why did every session recalculate` | the MAG-48 symptom — `updated_at` used to advance on every sync pass | entity | `workflows/supercharger-stats-read.md` |
