# Sync proposal — analytics (RM39 tier 3b)

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `entities/vehicle-metrics/guide.md`
Source spec:  `openspec/specs/analytics/spec.md`
Generated:    2026-09-03
Status: APPLIED 2026-09-03

> **Curator's routing note.** Unlike the other RM39 proposals, this one does NOT target
> `architecture/schema-per-module.md` — tier 3b moves no schema. It changes the watermark
> vocabulary, which is `vehicle-metrics`' own concept, so it belongs in that entity's guide.
>
> Wave 3 of the change already corrected the *present-tense* vocabulary in the live guides
> (`entities/vehicle-metrics/guide.md`, `architecture/nightly-cycle.md`,
> `architecture/telemetry-ingest-only.md`, `INDEX.md`). What is left, and what this proposal
> adds, is the durable *rule* a future agent needs — not the value, but why the value moved and
> what to do when it moves again.

---

## [guide] ## Conventions & gotchas — APPEND

- **`vehicle_metric_watermarks.source` is a closed vocabulary of table names stored AS DATA, and
  a table rename in another module invalidates it.** The column is never schema-qualified (the
  values are data, not SQL table references), a CHECK constraint pins the legal set, and
  `Recalculator.Reconcile` keys its per-source cursor on the string. So when a module renames a
  table this vocabulary names, the fix is an analytics-owned migration — not an edit in the
  module that did the renaming. Precedent twice over: `20260828000001` (RM31) and
  `20260902000004` (RM39 tier 3b).
  _Source: spec analytics — Requirement: Incremental Recompute Via An Analytics-Owned Watermark._
- **Retire a vocabulary value by DELETing its rows, never by UPDATEing them.** An absent watermark
  row is DEFINED as the epoch, so the next nightly `Reconcile` backfills that source's whole
  history in one pass — self-healing. Carrying the cursor value forward would make correctness
  depend on the other module's mirror pass never having gapped, which the migration cannot
  verify, and a stalled mirror would strand a carried cursor with nothing able to detect it.
  _Source: spec analytics — Requirement: Incremental Recompute Via An Analytics-Owned Watermark;
  RM39 roadmap decisions D8/D21._
- **The migration's Down DELETE is load-bearing, not tidying.** Restoring the old vocabulary while
  a row still holds the new value makes `ADD CONSTRAINT` fail with SQLSTATE 23514 and leaves the
  table with NO constraint at all. Each direction must clear the rows written under the
  vocabulary the other direction retires. Found by the round-trip test, which is why the test
  asserts the round trip rather than only the forward migration.
  _Source: migration `20260828000001`'s own Down block, re-confirmed by `20260902000004`._
- **`sqlc` mirrors the database's `COMMENT ON` text into `models.go` doc comments, so a migration
  that rewrites a comment REQUIRES `make sqlc`.** Easy to miss, because the change alters no
  column type and the build stays green either way. It has now been missed twice on this exact
  table — fixed by commit `3882a53` after RM31, and caught again in RM39 tier 3b's review round 1.
  _Source: RM39 tier 3b review finding F1._
- **The value `'supercharger_sessions'` means two different tables depending on era.** Before
  RM31 it named `internal/telemetry`'s table; since RM39 tier 3b it names `internal/charging`'s.
  No live row is ambiguous (the CHECK forbade the string in between, so the eras cannot coexist
  in data), but old backups, archived specs and `git log` are. A test asserting mid-migration
  state must pin the literal of the era it runs in, not the current Go constant.
  _Source: spec analytics; RM39 tier 3b design.md §6 and review finding F2._

## [index] ## Entities — ADD ROWS

| `watermark vocabulary` | the closed set of table names `vehicle_metric_watermarks.source` may hold, and how it is migrated → `entities/vehicle-metrics/guide.md` |
| `vocabulary migration` | retiring a watermark source value when another module renames its table → `entities/vehicle-metrics/guide.md` |
