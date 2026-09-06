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

Derived from the RM44 requirements the main spec now carries (MAG-48): the 3 modified by
`RM44-charging-add-change-detecting-mirror` (tier 3), plus **Supercharger Mirror
Synchronization Is Bounded By An Account Watermark**, added by
`RM44-platform-add-mirror-watermark` (tier 4).

**This draft REPLACES the earlier tier-3-only proposal, which was staged but never applied.**
It is derived from the current merged main spec, so it covers both tiers.

The capability already routes to an existing `workflows/` guide, so this proposal extends that
guide instead of creating a second entry. Its sibling proposals are `telemetry.md` and
`charging.md`.

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

- **The mirror reads a BOUNDED window, not the whole history.** It asks the source only for
  sessions modified at or after this account's watermark, widened slightly to tolerate a source
  write that commits just after the previous run read. Before MAG-48 it re-read every session
  every night, which is what defeated every downstream cursor.
  _Source: spec charge-session-log — Requirement: Supercharger Mirror Synchronization Is Bounded By An Account Watermark._

- **THE most important rule in this capability: a run whose bounded read returns nothing leaves
  the watermark completely untouched.** Never advance it to "now". A session the source commits
  moments after the read would fall permanently behind the cursor and never be picked up again.
  The loss is silent and undetectable — nothing errors, nothing logs, the row simply never
  arrives. If you change this code, this is the line to protect.
  _Source: spec charge-session-log — Requirement: Supercharger Mirror Synchronization Is Bounded By An Account Watermark._

- **A run that DOES return sessions advances the watermark to the highest last-modified instant
  actually observed — never to the run's own instant.** Same reason: advancing to "now" skips
  anything the source commits between the read and the advance.
  _Source: spec charge-session-log — Requirement: Supercharger Mirror Synchronization Is Bounded By An Account Watermark._

- **The watermark advances only AFTER the mirroring step succeeds.** A failed run leaves it
  where it was, so the next run's bounded read still covers what the failed one did not write.
  That repeats work; it never loses a row. Prefer that trade every time.
  _Source: spec charge-session-log — Requirement: Supercharger Mirror Synchronization Is Bounded By An Account Watermark._

- **An account with no watermark backfills its whole history once**, then advances normally.
  So the bounded read costs nothing on first deploy and needs no migration or manual seeding.
  _Source: spec charge-session-log — Requirement: Supercharger Mirror Synchronization Is Bounded By An Account Watermark._

- **A session whose vehicle is not currently registered is still recovered under the bounded
  read.** This works only because the mirror uses telemetry's ACCOUNT-wide updated-since port,
  which applies no vehicle filter. Switching it to the per-vehicle port would drop those rows
  and break orphan recovery without any visible error.
  _Source: spec charge-session-log — Requirement: Supercharger Mirror Synchronization Is Bounded By An Account Watermark._

## [index] ## Glossary & routing — entities — ADD ROWS

| `session change detection` | when a nightly sync counts as a modification of a charge session record (`updated_at`) | entity | `workflows/supercharger-stats-read.md` |
| `why did every session recalculate` | the MAG-48 symptom — `updated_at` used to advance on every sync pass | entity | `workflows/supercharger-stats-read.md` |
| `bounded mirror read` | the watermark-bounded Supercharger sync (RM44 tier 4); replaced the full-history read | entity | `workflows/supercharger-stats-read.md` |
| `why is the mirror slow` | it used to read all history every night — MAG-48, fixed by the bounded read | entity | `workflows/supercharger-stats-read.md` |
