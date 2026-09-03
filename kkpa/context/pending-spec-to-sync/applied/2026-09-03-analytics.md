# Sync proposal — analytics

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `architecture/schema-per-module.md`
Source spec:  `openspec/specs/analytics/spec.md`
Generated:    2026-09-02
Status: APPLIED 2026-09-03

> **Curator's routing note.** Same cross-cutting invariant as the `account` proposal, so it
> appends to the SAME guide. **Apply `account.md` first** — that is the proposal that creates the
> guide. The blocks here add only what is specific to analytics, and update the per-module
> migration-status table.

---

## [guide] ## How maintenance works — APPEND

Migration status update (RM39 tier 2): `analytics` has moved. Replace the `analytics` row of the
status table with:

| `analytics` | `analytics` | `vehicle_metrics`, `vehicle_metric_watermarks`, `charge_gaps` | moved (RM39 tier 2) |

## [guide] ## Conventions & gotchas — APPEND

- **`vehicle_metric_watermarks.source` holds table names as DATA, and they are never
  schema-qualified.** The column stores the literal strings `'vehicle_snapshots'`,
  `'supercharger_sessions'` and `'manual_charge_entries'`, a CHECK constraint depends on them, and
  `analytics.Recalculator` keys its recompute cursor on them. Qualifying one would silently break
  the cursor. The rule: a string inside a column is data; only a table reference in a `FROM` /
  `INTO` / `UPDATE` / `JOIN` clause gets a schema.
  _Source: spec analytics — Requirement: Module-Scoped Database Schema._
- **A test that replays a historic migration out of chronological order needs `search_path` on
  its own connection, never a migration edit.** `db_watermark_migration_integration_test.go`
  drives `goose.Provider.ApplyVersion` after the schema move has already applied; the historic
  file names its table bare, correctly, so it must be given
  `?search_path=public,<module>` on that one connection. Keep `public` first, so the test still
  passes against a database that has not applied the move.
  _Source: RM39 roadmap decision D12 (found during tier 2)._
- **Hunt raw test SQL by TABLE NAME, never by an opening quote.** A quote-anchored grep misses
  backtick-delimited multi-line SQL entirely — on tier 2 it predicted 5 statements and the real
  number was 18, in a file the pattern never surfaced. Use
  `grep -rnE '(FROM|INTO|UPDATE|JOIN)[[:space:]]+(<table>|<table>)\b' --include='*_test.go' internal/<module>`.
  _Source: RM39 roadmap decision D13 (found during tier 2)._
- **Only qualify the module's OWN tables.** Analytics' tests seed other modules' tables directly
  (`vehicle_snapshots`, `supercharger_sessions`); those stay bare until their own tier moves them.
  Over-qualifying a table that has not moved breaks the test just as surely as under-qualifying
  one that has. _Source: spec analytics — Requirement: Module-Scoped Database Schema._

## [index] ## Architecture — ADD ROWS

| `analytics schema` | the `analytics` schema holding `vehicle_metrics`, `vehicle_metric_watermarks`, `charge_gaps` → `architecture/schema-per-module.md` |
| `watermark source values` | why `vehicle_metric_watermarks.source` strings are never schema-qualified → `architecture/schema-per-module.md` |
