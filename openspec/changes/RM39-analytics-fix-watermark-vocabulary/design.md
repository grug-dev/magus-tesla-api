# Design — RM39-analytics-fix-watermark-vocabulary (roadmap tier 3b)

**Design gate: TRIPPED.** This change alters `vehicle_metric_watermarks`'s CHECK
constraint and deletes rows. Per `CLAUDE.md`'s Design-Gates (`database`), this design +
its rationale + its index plan must be shown to the owner and confirmed before Apply.

## 1. Context — what precedent this change copies

`internal/analytics/db/migrations/20260828000001_migrate_vehicle_metric_watermarks_source.sql`
performed this exact class of operation once already (RM31 tier 3, MAG-19): the
`vehicle_metric_watermarks.source` vocabulary is a closed, free-standing `TEXT` label (no
FK) naming the physical table each of `Recalculator.Reconcile`'s three independent cursors
tracks. When the table a cursor tracks gets renamed, the label must follow, and the
cleanest way to do that — established by that migration, not invented here — is:

1. DROP the CHECK constraint (removes vocabulary enforcement so the DELETE below can never
   fail on it, though a DELETE cannot itself violate a CHECK — kept uniform with every
   other step, matching `20260828000001`'s own stated reasoning).
2. DELETE every row whose `source` holds the retiring value.
3. ADD the CHECK constraint back with the new vocabulary, now that every remaining row
   already satisfies it, so the ADD cannot fail.
4. Refresh the table and column `COMMENT ON` text so the catalog's own documentation
   matches the live vocabulary.
5. `-- +goose Down` reverses all four steps, deleting the rows written under the *new*
   vocabulary before restoring the old CHECK — `20260828000001`'s own Down comment
   explains why this is load-bearing, not tidying (see §4 below); this change's Down needs
   the identical property.

This design does not re-derive that shape from scratch — roadmap decision D8 says
explicitly "copy `20260828000001`; do not invent it," and that is what §3 below does,
adapted only for (a) the specific values being retired/readmitted and (b) schema
qualification, which `20260828000001` does not need (it predates tier 2's schema move and
correctly names the table bare for its own, still-`public`-schema, moment in history —
D9/D1: historic migrations are never edited to "fix" this).

## 2. Why this is analytics-owned work (roadmap D15, restated for this artifact)

`vehicle_metric_watermarks` lives in the `analytics` Postgres schema (moved there by tier
2, `RM39-analytics-move-to-own-schema`, archived). A migration script that runs
`ALTER TABLE`/`DELETE FROM` against another module's schema is a cross-module database
write whether it executes as deploy-time DDL or as application-runtime SQL —
`ai/architecture.md` §2's "no cross-module database leaks" does not exempt migration
files. Putting this migration in `internal/charging/db/migrations/` (the module whose
rename triggered the vocabulary drift) would repeat exactly the class of violation
roadmap D6 already blocks tier 4 on. `20260828000001` is the direct precedent for the
alternative taken here: it too was triggered by a charging-side change
(`RM31-analytics-read-sessions-from-charging`) and still lives in
`internal/analytics/db/migrations/`, because the table it touches is analytics'.

`internal/analytics/recalculate.go`'s `sourceChargeSessions` constant is analytics' own
Go source file for the identical reason — no other module reads or writes it.

## 3. The exact migration

New file:
`internal/analytics/db/migrations/20260902000004_migrate_vehicle_metric_watermarks_source_supercharger.sql`.

Version `20260902000004` is a fresh, globally-unique 14-digit timestamp — verified against
every `*_test.go`-adjacent migration file in the repository (`find internal -path
'*/db/migrations/*.sql'`, deduplicated); the highest in use at the time this design was
written is `20260902000003` (`internal/charging`'s tier-3 migration). `make
migration-guard` enforces global uniqueness across every module's directory (they share
one `goose_db_version` table) — this is not a per-module namespace. Because
`MIGRATIONS_DIRS` applies `account → telemetry → charging → analytics` **per directory, to
completion**, not merged by timestamp across directories (roadmap's own "Replay is
coherent" section, and D8's point 3), this migration is guaranteed to run after every
`charging` migration — including tier 3's rename — regardless of how close or far its own
timestamp sits from tier 3's. The dependency is satisfied by directory order, not by
numeric proximity.

```sql
-- +goose Up
-- internal/analytics — RM39 tier 3b (RM39-analytics-fix-watermark-vocabulary, MAG-31).
--
-- vehicle_metric_watermarks.source is a closed-vocabulary label naming the physical
-- table each of Reconcile's three independent cursors tracks (see this table's own
-- migration, 20260821000002_add_vehicle_metric_watermarks.sql, and the prior rewrite,
-- 20260828000001_migrate_vehicle_metric_watermarks_source.sql). RM39 tier 3
-- (RM39-charging-move-to-own-schema, archived) renamed internal/charging's Supercharger
-- table from charge_sessions to supercharger_sessions (roadmap D5b). Leaving this
-- column's vocabulary at 'charge_sessions' would let it keep naming a table that no
-- longer exists under that name -- defeating the column's own documented purpose.
--
-- The 'charge_sessions' cursor rows are RESET TO EPOCH (DELETEd), not renamed in place --
-- roadmap D8/D15, mirroring 20260828000001's own precedent exactly. An absent watermark
-- row is DEFINED as the epoch by Recalculator.Reconcile's own watermark() method (design
-- D7 of RM29-analytics-add-vehicle-metrics), so the next nightly Reconcile for each
-- affected vehicle backfills that source's entire history from charging.supercharger_
-- sessions in one pass -- no separate backfill migration or one-off binary, and no
-- vehicle_metrics/charge_gaps row is touched, only this bookkeeping cursor.
--
-- DELETE, not UPDATE: carrying the cursor value forward would make the migration's
-- correctness depend on charging's own mirror pass having run without a gap between the
-- rename landing and this migration applying -- an operational fact this migration cannot
-- verify. A single redundant backfill pass on the next Reconcile is the cheaper,
-- self-correcting failure mode (exact precedent: 20260828000001's own reasoning).
--
-- Reused-string note (design.md "Reused-String Ambiguity"): 'supercharger_sessions' is
-- the SAME literal this column held before 20260828000001, when it named
-- internal/telemetry's table instead. No row currently holds that value -- the CHECK
-- constraint has forbidden it since 20260828000001 landed -- so this is not a data
-- collision. It IS a live documentation concern until roadmap tier 4 renames telemetry's
-- table to supercharger_history: until then, two different tables in this database
-- (public.supercharger_sessions and charging.supercharger_sessions) share this bare name,
-- and this column's own value cannot schema-qualify itself (tier 2 decision: these are
-- data, not table references). See this file's COMMENT ON column.source below, and
-- design.md, for the disambiguation a reader must rely on instead.
--
-- Only vehicle_metric_watermarks.charge_sessions rows are affected; vehicle_snapshots and
-- manual_charge_entries rows are untouched.
--
-- Ordering (mirrors 20260828000001 exactly): DROP the constraint before the DELETE
-- (uniform with every other step, though a DELETE cannot itself violate a CHECK
-- constraint); ADD the new constraint only once every remaining row already satisfies it,
-- so the ADD cannot fail. Every reference below is schema-qualified
-- (analytics.vehicle_metric_watermarks) -- unlike 20260828000001, which predates tier 2's
-- schema move and correctly stays bare for its own historical moment (D1/D9: historic
-- migrations are never edited).
ALTER TABLE analytics.vehicle_metric_watermarks
    DROP CONSTRAINT IF EXISTS vehicle_metric_watermarks_source_check;

DELETE FROM analytics.vehicle_metric_watermarks
WHERE source = 'charge_sessions';

ALTER TABLE analytics.vehicle_metric_watermarks
    ADD CONSTRAINT vehicle_metric_watermarks_source_check
    CHECK (source IN ('vehicle_snapshots', 'supercharger_sessions', 'manual_charge_entries'));

COMMENT ON TABLE analytics.vehicle_metric_watermarks IS
    'One recompute cursor per (account_id, tesla_id, source) for internal/analytics.Recalculator.'
    'Reconcile (RM29-analytics-add-vehicle-metrics, MAG-26 tier 3). Three independent sources '
    '(design D3): vehicle_snapshots, supercharger_sessions, manual_charge_entries -- each advances '
    'on its own row, never coupled to the others'' clocks. No watermark row yet for a '
    '(account_id, tesla_id, source) means "epoch" (design D7): Reconcile backfills the vehicle''s '
    'full history in one pass. Owned by internal/analytics; no other module reads this table '
    'directly. source''s vocabulary was migrated vehicle_snapshots/supercharger_sessions/manual_'
    'charge_entries -> vehicle_snapshots/charge_sessions/manual_charge_entries by '
    '20260828000001 (RM31-analytics-read-sessions-from-charging), then back to '
    'vehicle_snapshots/supercharger_sessions/manual_charge_entries by THIS migration '
    '(RM39-analytics-fix-watermark-vocabulary, tier 3b) once internal/charging renamed its own '
    'table to supercharger_sessions (RM39 tier 3, D5b). The reused string now names a DIFFERENT '
    'physical table (charging.supercharger_sessions) than it did before 20260828000001 '
    '(telemetry.supercharger_sessions) -- see COMMENT ON COLUMN .source for the disambiguation.';

COMMENT ON COLUMN analytics.vehicle_metric_watermarks.source IS
    'Closed 3-value vocabulary naming the physical table this cursor tracks (design D3): '
    '''vehicle_snapshots'' (internal/telemetry), ''supercharger_sessions'' (internal/charging, '
    'AS OF RM39-analytics-fix-watermark-vocabulary -- this is a REUSED string; before RM31 '
    '(20260828000001) the same literal named internal/telemetry''s table instead, and until '
    'RM39 tier 4 renames that table to supercharger_history, a DIFFERENT, still-live table '
    '(public.supercharger_sessions) shares this bare name in this same database. This column ' 
    'never schema-qualifies its own value (RM39 tier 2 decision -- these are data, not table '
    'references), so a reader relies on this comment, internal/analytics/AGENTS.md, and '
    'recalculate.go''s sourceSuperchargerSessions constant to know which table is meant: always '
    'internal/charging''s, never internal/telemetry''s, for this column, in every era after RM31.), '
    'or ''manual_charge_entries'' (internal/charging). No FK -- a free-standing string label, '
    'mirroring charge_gaps.missing_charging_type''s identical convention.';

-- +goose Down
-- IRREVERSIBLE for the deleted rows, and HARMLESS -- exact precedent: 20260828000001's own
-- Down, and before it 20260822000002_reset_vehicle_metric_watermarks.sql's. A DELETE
-- cannot be undone by an UPDATE (there is no row left to update, and no source_updated_at
-- value recorded anywhere to restore even if there were). The only consequence of rolling
-- back is that the next Reconcile for an affected vehicle backfills the charging-sourced
-- watermark once more from epoch -- an absent watermark row is DEFINED as the epoch
-- (design D7), so there is no state to lose and nothing to actually undo, only a
-- redundant recompute pass.
--
-- What Down DOES restore: the CHECK constraint's old vocabulary and the original
-- table/column COMMENTs, so a rollback leaves the schema exactly as it was before Up --
-- only the deleted rows themselves are unrecoverable, and their absence degrades to
-- "epoch," never to an error.
--
-- THE DELETE BELOW IS LOAD-BEARING, NOT TIDYING -- identical reasoning to
-- 20260828000001's own Down. Restoring the old vocabulary while a single
-- source = 'supercharger_sessions' row exists makes the ADD CONSTRAINT fail with
-- SQLSTATE 23514, leaving the table with NO constraint at all. Such rows are the normal
-- case, not an edge case: Reconcile calls advanceWatermark(..., sourceSuperchargerSessions,
-- ...) on every pass (recalculate.go), so the first nightly run after Up creates them.
-- Without this DELETE the Down is unrunnable in production from that moment on. It
-- mirrors Up's own DELETE exactly -- each direction clears the rows written under the
-- vocabulary the other direction retires. Verified by this change's own T1 round-trip
-- test (design.md §5), exactly as 20260828000001's own T1 test first found this.
ALTER TABLE analytics.vehicle_metric_watermarks
    DROP CONSTRAINT IF EXISTS vehicle_metric_watermarks_source_check;

DELETE FROM analytics.vehicle_metric_watermarks
WHERE source = 'supercharger_sessions';

ALTER TABLE analytics.vehicle_metric_watermarks
    ADD CONSTRAINT vehicle_metric_watermarks_source_check
    CHECK (source IN ('vehicle_snapshots', 'charge_sessions', 'manual_charge_entries'));

COMMENT ON TABLE analytics.vehicle_metric_watermarks IS
    'One recompute cursor per (account_id, tesla_id, source) for internal/analytics.Recalculator.'
    'Reconcile (RM29-analytics-add-vehicle-metrics, MAG-26 tier 3). Three independent sources '
    '(design D3): vehicle_snapshots, charge_sessions, manual_charge_entries -- each advances '
    'on its own row, never coupled to the others'' clocks. No watermark row yet for a '
    '(account_id, tesla_id, source) means "epoch" (design D7): Reconcile backfills the vehicle''s '
    'full history in one pass. Owned by internal/analytics; no other module reads this table '
    'directly. source''s vocabulary was migrated from supercharger_sessions to charge_sessions '
    'by RM31-analytics-read-sessions-from-charging (MAG-19 tier 3) when the Supercharger read '
    'moved from internal/telemetry to internal/charging; existing supercharger_sessions cursor '
    'rows were reset to epoch (DELETEd), not renamed in place (roadmap Decision 10; design.md '
    'Sec 2b/2c of that change).';

COMMENT ON COLUMN analytics.vehicle_metric_watermarks.source IS
    'Closed 3-value vocabulary naming the physical table this cursor tracks (design D3): '
    '''vehicle_snapshots'' (internal/telemetry), ''charge_sessions'' (internal/charging, as '
    'of RM31-analytics-read-sessions-from-charging -- previously ''supercharger_sessions'' in '
    'internal/telemetry), or ''manual_charge_entries'' (internal/charging). No FK -- a '
    'free-standing string label, mirroring charge_gaps.missing_charging_type''s identical '
    'convention.';
```

Note the Up/Down COMMENT text above is copied byte-for-byte from `20260828000001`'s own
Up/Down comments where it is restoring that migration's prior state — this is intentional:
Down's comments must match what was live immediately before this migration's Up ran, which
is exactly what `20260828000001`'s Up already established and left in place.

## 4. Rejected alternatives

**(a) Rename in place (`UPDATE ... SET source = 'supercharger_sessions' WHERE source =
'charge_sessions'`).** Rejected for the same reason `20260828000001` rejected it: the
value being written is *also* the historically-reused string, so an in-place rewrite would
silently claim continuity with the pre-RM31 cursor's progress — a cursor value
(`source_updated_at`) that was watermarking `telemetry.supercharger_sessions`'s
`updated_at` column, carried forward as if it now watermarked
`charging.supercharger_sessions`'s `updated_at` column. Those are two different tables
with two different update histories; there is no guarantee `charging`'s mirror pass has
ever written a row with `updated_at` anywhere near the old cursor value, so a carried-over
cursor could silently skip real Supercharger session data that predates it in
`charging`'s own table but postdates the stale cursor. DELETE avoids this by making the
next `Reconcile` re-derive the cursor from a real, current read of `charging`'s table
rather than trusting an inherited number.

**(b) Do nothing — leave the vocabulary and the Go constant at `charge_sessions`.**
Rejected: `charging.charge_sessions` no longer exists (tier 3 renamed it), so this is not
a stable equilibrium — it is stale metadata masquerading as current, with the actual
runtime behavior (`Recalculator` reads and writes `charging.supercharger_sessions`
correctly, because `internal/charging`'s public port already changed under RM31/tier-3)
silently diverging from what the watermark label claims to track. `Reconcile` itself keeps
working (the label is a free-standing string, not a live reference — nothing crashes), but
every future reader of this column — human or AI — is handed a lie: a comment, a doc, or
an `AGENTS.md` entry that still says `'charge_sessions'` when the table has been
`supercharger_sessions` since tier 3. This is exactly the "docs track structural change"
failure mode `CLAUDE.md` calls out, applied to a database CHECK constraint instead of a
markdown file.

**(c) A one-off backfill binary/manual `UPDATE` run outside a migration.** Rejected for
the same reason `20260828000001` rejected it: this project's convention is that a schema
or vocabulary change ships as a goose migration, reviewable and replayable, not an
undocumented one-time script. The DELETE-and-let-`Reconcile`-backfill approach costs one
redundant nightly pass per affected vehicle and needs no separate tooling at all.

## 5. Index plan

**No index changes — none needed.** This migration touches only the table's `CHECK`
constraint and its `COMMENT ON` text. `Recalculator.watermark`'s sole read pattern against
this table, `SELECT source_updated_at FROM analytics.vehicle_metric_watermarks WHERE
account_id = $1 AND tesla_id = $2 AND source = $3` (a single-row lookup), is served
entirely by the table's existing `vehicle_metric_watermarks_account_tesla_source_unique`
UNIQUE constraint on `(account_id, tesla_id, source)` — untouched by this migration, and
already justified against this exact read pattern in
`20260821000002_add_vehicle_metric_watermarks.sql`'s own Index Plan comment. `advanceWatermark`'s
write path is an `ON CONFLICT` upsert against the same unique index. Neither the DELETE
(which removes rows, never adds a query pattern) nor the CHECK rewrite (enforced on
`INSERT`/`UPDATE`, not read) changes the shape of any query this table serves. Per the
project's read-heavy performance profile: this table's read path was already optimal
before this change and stays optimal after it.

## 6. Reused-String Ambiguity — the substantive design question

**What a reader of a `vehicle_metric_watermarks` row cannot tell from the row alone.** The
row's columns are `id`, `account_id`, `tesla_id`, `source`, `source_updated_at`,
`created_at`, `updated_at`. Nothing on the row itself carries an "era" marker. A row with
`source = 'supercharger_sessions'` and, say, `created_at = 2026-07-15` (before RM31 ever
landed, if such a row still existed) would be indistinguishable, by inspecting the row
alone, from a row with the same `source` value and `created_at = 2026-09-10` (after this
migration). Both say the identical three-byte-different string. The column is deliberately
*not* schema-qualified — tier 2's own migration comment states this explicitly: "these are
data, not table references, and this schema move does not touch them... schema-qualifying
data would corrupt it, not namespace it." So there is no mechanical way to make the value
self-describing without abandoning that tier-2 decision, which this change does not
propose to do.

**Does the DELETE this migration performs make the ambiguity harmless in practice? Yes,
for data — no, for documentation, and the two need to be kept separate.**

- *For live data:* yes, harmless, and provably so rather than merely likely. The value
  `'supercharger_sessions'` has been **impossible** to insert into this table since
  `20260828000001` applied (the CHECK constraint has rejected it for the entire interval
  between that migration and this one) — the table's own `TestMigration_
  WatermarkSourceVocabulary` test asserts this directly (`insertWatermarkSource(...,
  "supercharger_sessions")` returns a CHECK violation, post-`20260828000001`). So on the
  moment this migration's Up runs, the DELETE `WHERE source = 'charge_sessions'` clears
  every row that could possibly exist under the retiring label, and the vocabulary space
  for `'supercharger_sessions'` is provably empty going in — there is no pre-existing row
  this migration could accidentally reinterpret. Every row written under this label from
  this migration forward means exactly one thing: `charging.supercharger_sessions`. There
  is no live-data collision to resolve, because there is no live row old enough to collide.
- *For documentation, backups, and history:* no, not harmless, and this migration does not
  claim otherwise. A database backup taken before `20260828000001`, an old dashboard
  export, an archived OpenSpec spec (the roadmap itself names "133 doc files [that] still
  use `supercharger_sessions` to mean *telemetry's* table" as an accepted, known
  consequence of tier 3's own rename), or simply a person reading `git log` on this
  migration file, will genuinely encounter the string meaning two different tables
  depending on which side of `20260828000001` they are looking at. This migration cannot
  and does not retroactively resolve that — it only guarantees the *live, queryable*
  table is unambiguous from this point forward. The mitigation is documentation, not code:
  this migration's own `COMMENT ON COLUMN`, `internal/analytics/recalculate.go`'s renamed
  `sourceSuperchargerSessions` constant and its doc comment, and
  `internal/analytics/AGENTS.md` all state explicitly, in the same breath as the value,
  which table it means and that the string was reused.

**What changes when roadmap tier 4 (`RM39-telemetry-move-to-own-schema`) renames
`internal/telemetry`'s table to `supercharger_history` (roadmap D5a).** Two distinct kinds
of ambiguity are in play, and tier 4 only closes one of them:

- *Lexical ambiguity* — right now, and until tier 4 lands, **two different tables in the
  same live database share the bare name `supercharger_sessions`**:
  `public.supercharger_sessions` (`internal/telemetry`, untouched until tier 4) and
  `charging.supercharger_sessions` (`internal/charging`, renamed by tier 3). They are
  schema-qualified and therefore never collide as SQL identifiers — `ai/architecture.md`'s
  own §D2 finding is that sqlc forces exactly this qualification — but the *watermark's*
  `source` value is plain text, not a SQL identifier, so schema-qualification does nothing
  to disambiguate it for a reader. Once tier 4 renames telemetry's copy to
  `supercharger_history`, the bare word `supercharger_sessions` becomes globally unique in
  this database's catalog — permanently, not just for this one column. That is the
  ambiguity tier 4 actually closes.
- *Semantic ambiguity* — already closed, by RM31, independently of both tier 3 and tier 4.
  `internal/analytics` has not read `internal/telemetry`'s Supercharger data since RM31
  moved that read onto `internal/charging`'s `SuperchargerSessionAnalyticsReader`
  (`internal/analytics/AGENTS.md`'s "Allowed / forbidden imports" section: telemetry's own
  Supercharger port/type is explicitly no longer imported here). So even today, before
  tier 4, the watermark's `'supercharger_sessions'` label has exactly one operational
  meaning inside this module's own code path: `charging`'s table. The risk tier 4 removes
  is a *human or AI reader* mistaking the label for telemetry's still-identically-named
  table because both are visible in the same database at once — not a runtime behavior
  risk, because `Reconcile` never queries telemetry for this source regardless of what the
  label says.

**Conclusion driving this migration's design.** Because the semantic half of the ambiguity
was already closed by RM31 and the data half is provably closed by this migration's own
DELETE, this change proceeds with reusing `'supercharger_sessions'` rather than choosing a
disambiguated alternative (e.g. `'charging_supercharger_sessions'`). The remaining,
real risk — a reader's *documentation-level* confusion until tier 4 lands — is mitigated
by stating the reused-string fact explicitly everywhere the value is named: this
migration's comments, the Go constant's doc comment, and `AGENTS.md`. A permanent rename
of the *label itself* (rather than reusing history's string) was considered and rejected:
it would abandon the precedent `20260828000001` set (label mirrors the table's own name)
for a problem tier 4 already fully resolves on its own timeline, at the cost of yet
another watermark vocabulary migration this roadmap does not otherwise need.

## 7. Go constant rename

`internal/analytics/recalculate.go` (currently, before this change):

```go
sourceVehicleSnapshots    = "vehicle_snapshots"
sourceChargeSessions      = "charge_sessions"
sourceManualChargeEntries = "manual_charge_entries"
```

becomes:

```go
sourceVehicleSnapshots    = "vehicle_snapshots"
sourceSuperchargerSessions = "supercharger_sessions"
sourceManualChargeEntries = "manual_charge_entries"
```

**Both the name and the value change.** Charging's own tier-3 design.md left this as "an
analytics-side style call, not specified further" while noting the rename "would match the
vocabulary and avoid a stale identifier." This design makes that call: rename it. A
constant named `sourceChargeSessions` holding the value `"supercharger_sessions"` would be
a self-contradicting identifier — exactly the kind of drift this whole change exists to
eliminate from the database side; leaving it on the Go side would just move the same
problem one layer up. Every call site (`r.watermark(ctx, accountID, teslaID,
sourceChargeSessions)`, `r.advanceWatermark(ctx, accountID, teslaID, sourceChargeSessions,
...)`, the two `fmt.Errorf` format calls) is a mechanical rename — the constant is used
purely as a value, never pattern-matched by name elsewhere in production code (confirmed:
`grep -rn sourceChargeSessions internal/` finds only `recalculate.go` itself and this
module's own `_test.go` files — see §8).

## 8. Test file impact — a real interaction this migration introduces, not a pre-existing bug

**Offline tests.** `derive_test.go`, `reader_test.go`, `recalculate_test.go`: none
reference `vehicle_metric_watermarks`, the CHECK constraint, or the renamed constant by
its old name in a way that fails to compile after the rename — `go vet` will catch any
compile-time miss (renaming a Go identifier, if any call site is missed, is a build
error, not a silent pass). No offline test needs new assertions for this change; the
public `Reader`/`Recalculator` contract is unchanged.

**`db_integration_test.go`.** Three call sites (`fetchWatermark(..., sourceChargeSessions)`
at what are today lines 1062, 1233, 1379) use the Go **symbol**, not a hand-typed literal
— once the symbol is renamed, these calls automatically query
`source = 'supercharger_sessions'` instead of `source = 'charge_sessions'`, which is
correct: these tests exercise `Reconcile` against the *current* (post-migration)
vocabulary, and `Reconcile` will be writing/reading `sourceSuperchargerSessions` after this
change lands, exactly as these tests already expect symbolically. **No assertion logic
changes.** Only the surrounding prose comments need correcting — e.g. line ~1059's "RM31
tier 3: this source's label is charge_sessions now (sourceChargeSessions), not
supercharger_sessions" becomes stale and should read "this source's label is
supercharger_sessions again (sourceSuperchargerSessions), as of RM39 tier 3b — see
recalculate.go" (or equivalent); lines referring to "charge_sessions watermark" in error
messages/comments near lines 1233 and 1379 likewise. These are comment-only fixes with no
behavioral stake — `go vet`/`gofmt` cannot catch stale prose, so this is a manual sweep
(tasks.md).

**`db_watermark_migration_integration_test.go` — the one file needing a real logic fix,
not just renaming.** This file drives `goose.Provider.ApplyVersion` directly against
`20260828000001`'s own version, replaying that ONE historic migration's Up/Down in
isolation, independent of the rest of the migration chain (see the file's own header
comment for why `ApplyVersion`, not `DownTo`/`Up`, is used). Its hardcoded string literals
(`"charge_sessions"`, `"supercharger_sessions"` at lines ~250–316) are correctly pinned to
*that migration's own* vocabulary transition and must **not** change — they test
`20260828000001`'s Up/Down SQL exactly as it was written, per D1 (historic migrations are
never edited, and neither are the tests proving them).

The bug this change must fix is different: **this file's shared `t.Cleanup` leaves the
table at the WRONG final state once this change's migration also exists.** Traced
precisely:

1. Before this test runs, `TestMain` has applied every migration in the package's test
   database, including THIS change's new migration (version `20260902000004`) — so the
   live CHECK constraint accepts `('vehicle_snapshots', 'supercharger_sessions',
   'manual_charge_entries')` (this change's target vocabulary) when the test starts.
2. The test's own body rolls `20260828000001` back and forward multiple times via
   `ApplyVersion`, which executes that migration's raw Up/Down SQL directly — it does not
   know or care that a *later* migration (this change's) has also modified the same
   constraint object. Because `20260828000001`'s Down installs
   `CHECK (... 'supercharger_sessions' ...)` — the SAME three-value set this change's
   migration also installs — and `20260828000001`'s Up installs
   `CHECK (... 'charge_sessions' ...)` — a set THIS change's migration was never asked to
   restore — the test's internal round-trip is unaffected (it only ever asserts against
   `20260828000001`'s own two vocabularies). But the file's existing `t.Cleanup`
   (registered once, at the top of the test) ends by calling
   `provider.ApplyVersion(cleanupCtx, watermarkSourceMigrationVersion, true)` — i.e.
   **re-applying `20260828000001`'s Up** — which leaves the live constraint at
   `('vehicle_snapshots', 'charge_sessions', 'manual_charge_entries')`: the vocabulary this
   change's migration was supposed to have permanently retired.
3. `goose_db_version`'s bookkeeping row for THIS change's migration (`20260902000004`) is
   never touched by any of this — only `20260828000001`'s row is toggled — so goose's own
   records still claim `20260902000004` is "applied," even though its physical DDL effect
   (the CHECK constraint it installed) has just been silently overwritten by
   `20260828000001`'s replayed Up. Every other DB-backed test that runs afterward in this
   package now sees the *wrong* constraint (`'charge_sessions'` accepted, not
   `'supercharger_sessions'`) despite `TestMain` having applied every migration, including
   this change's, at package start.

**The fix (task in tasks.md):** extend this file's `t.Cleanup` to also re-toggle THIS
change's migration version after `20260828000001`'s own cleanup step, restoring the
cumulative final state every other test in the package expects:

```go
t.Cleanup(func() {
    cleanupCtx := context.Background()
    _, _ = pool.Exec(cleanupCtx, `DELETE FROM analytics.vehicle_metric_watermarks WHERE account_id = $1 AND tesla_id = $2`, accountID, teslaID)
    _, _ = pool.Exec(cleanupCtx, `DELETE FROM analytics.vehicle_metric_watermarks WHERE account_id = $1 AND tesla_id = $2`, otherAccountID, otherTeslaID)
    // Leave 20260828000001 in its own Up state first (existing behavior)...
    if _, err := provider.ApplyVersion(cleanupCtx, watermarkSourceMigrationVersion, true); err != nil && !errors.Is(err, goose.ErrAlreadyApplied) {
        t.Errorf("cleanup: re-applying watermark-source migration: %v", err)
    }
    // ...then re-apply THIS change's migration on top of it, since goose_db_version's
    // bookkeeping for that version was never toggled during this test (only
    // 20260828000001's row was) and its physical DDL effect (the CHECK constraint) was
    // just overwritten by the line above. A bare ApplyVersion(..., true) would return
    // goose.ErrAlreadyApplied and skip execution -- force a real down/up cycle instead,
    // which the bookkeeping (still "applied" throughout) legitimately allows.
    if _, err := provider.ApplyVersion(cleanupCtx, superchargerVocabMigrationVersion, false); err != nil {
        t.Errorf("cleanup: rolling back the RM39 tier 3b migration to force re-application: %v", err)
    }
    if _, err := provider.ApplyVersion(cleanupCtx, superchargerVocabMigrationVersion, true); err != nil {
        t.Errorf("cleanup: re-applying the RM39 tier 3b migration: %v", err)
    }
})
```

where `superchargerVocabMigrationVersion int64 = 20260902000004` is declared alongside the
existing `watermarkSourceMigrationVersion` constant, and `newAnalyticsMigrationProvider`'s
existing `goose.Provider` (already scoped to this module's whole `db/migrations` directory
via `os.DirFS("db/migrations")`) can drive both versions without a second provider — it
already has this migration's Up/Down SQL loaded, since `ApplyVersion` looks up a version
from the same filesystem-backed provider regardless of which one is requested.

**Why this matters and is not optional:** without this fix, this file's own test would
leave every subsequent DB-backed test in the `analytics` package running against the
*wrong*, pre-this-change constraint for the remainder of that `go test` process — including
this change's own new round-trip test (§9, T1), if it happens to run after this file's
test in the same binary. `go test` does not guarantee file-level ordering by default beyond
alphabetical-ish source ordering per package, so this is not a hypothetical ordering
accident — it must be fixed unconditionally as part of this change, not treated as a
follow-up.

**D9/D17 sweep — no other file affected.** `grep -rnE '(FROM|INTO|UPDATE|JOIN)[[:space:]]+
(vehicle_metric_watermarks|analytics\.vehicle_metric_watermarks)\b' --include='*_test.go'
internal/` returns hits only in `internal/analytics/db_watermark_migration_integration_test.go`
and `internal/analytics/db_integration_test.go` — both already covered above; no other
module's test hand-writes SQL against this table. `grep -rn "'charge_sessions'\"charge_
sessions\"" --include='*.go' internal/` (outside migration files, which are exempt by D9)
returns hits only in `internal/analytics/recalculate.go` (the constant itself, renamed
above) and `internal/analytics/db_watermark_migration_integration_test.go` (the pinned
historic-migration literals, left alone per D1). No other module's Go code holds this
string as a watermark source value — confirming D17's cross-module escape concern does not
apply here.

## 9. Test Contract (authored up front, per `ai/go-conventions.md` §Testing)

### T1 — migration round-trip (DB-integration, final wave)

New test, either a new function in a new file (e.g.
`db_watermark_supercharger_migration_integration_test.go`) or a second `Test...` function
appended to the existing `db_watermark_migration_integration_test.go` (implementer's
choice — tasks.md leaves this open since both are equally valid file organizations; the
existing file's helpers — `seedWatermarkRow`, `fetchWatermarkUpdatedAt`, `countWatermarkRows`,
`insertWatermarkSource`, `isCheckViolation`, `withSearchPath` — are all reusable as-is,
since they take `source string` and a `*goose.Provider`/`*pgxpool.Pool`, not anything
migration-specific).

**Given:** migrations applied through `20260828000001` only (this change's migration
rolled back via `ApplyVersion(ctx, superchargerVocabMigrationVersion, false)`), so the live
CHECK is `('vehicle_snapshots', 'charge_sessions', 'manual_charge_entries')`. Seed three
rows for one vehicle (`accountID = uuid.MustParse("33333333-3333-3333-3333-333333333333")`,
`teslaID = int64(777)`), one per source, using the pre-this-migration vocabulary:
  - `source = "charge_sessions"`, `source_updated_at = 2026-08-30T10:00:00Z`
  - `source = "vehicle_snapshots"`, `source_updated_at = 2026-08-31T03:30:00Z`
  - `source = "manual_charge_entries"`, `source_updated_at = 2026-08-29T18:00:00Z`

**When:** the migration (`superchargerVocabMigrationVersion`) is applied
(`ApplyVersion(ctx, superchargerVocabMigrationVersion, true)`).

**Then:**
- `countWatermarkRows(t, pool, accountID, teslaID, "charge_sessions")` == `0` (the row is
  GONE, not renamed).
- `fetchWatermarkUpdatedAt(t, pool, accountID, teslaID, "vehicle_snapshots")` equals
  `2026-08-31T03:30:00Z`, unchanged.
- `fetchWatermarkUpdatedAt(t, pool, accountID, teslaID, "manual_charge_entries")` equals
  `2026-08-29T18:00:00Z`, unchanged.
- `countWatermarkRows(t, pool, accountID, teslaID, "")` == `2` (down from 3).
- `insertWatermarkSource(t, pool, otherAccountID, otherTeslaID, "charge_sessions")` returns
  a CHECK violation on `vehicle_metric_watermarks_source_check`
  (`isCheckViolation(err, "vehicle_metric_watermarks_source_check")` is `true`).
- `insertWatermarkSource(t, pool, otherAccountID, otherTeslaID, "supercharger_sessions")`
  succeeds (`err == nil`).
- Semantic consequence via `Recalculator.watermark` directly (not raw SQL): a
  `*recalculator` built via `newRealRecalculator(pool).(*recalculator)`, calling
  `rec.watermark(ctx, accountID, teslaID, sourceSuperchargerSessions)` (the renamed
  constant — this assertion runs in the FULLY POST-migration state, unlike
  `db_watermark_migration_integration_test.go`'s own analogous assertion at its line 288,
  which correctly uses the pre-rename literal for ITS migration's mid-state — see §8)
  returns the zero-value epoch (`time.Time{}`), `err == nil` — no row exists yet for this
  vehicle under the new label, and absence IS epoch (design D7).

**Down round-trip:** `ApplyVersion(ctx, superchargerVocabMigrationVersion, false)`.
- `insertWatermarkSource(t, pool, otherAccountID, otherTeslaID, "charge_sessions")`
  succeeds (old vocabulary restored).
- `countWatermarkRows(t, pool, accountID, teslaID, "supercharger_sessions")` == `0` still
  (Down does not resurrect the row deleted by Up — the negative assertion IS the point,
  mirroring `20260828000001`'s own T1 test).
- `countWatermarkRows(t, pool, otherAccountID, otherTeslaID, "supercharger_sessions")` ==
  `0` after Down (Down's own DELETE clears the row this test's Up-phase assertion above
  inserted under the new vocabulary — this is what makes the restored ADD CONSTRAINT
  possible at all; without it, Down fails with SQLSTATE 23514, exactly the failure mode
  `20260828000001`'s T1 test first caught for that migration).

**Cleanup:** delete both vehicles' rows; re-apply `superchargerVocabMigrationVersion`
forward so the package's fully-migrated state is restored for every other test —
`ApplyVersion(..., true)` here is a *direct* re-application (no prior toggle needed),
because this test's own final action left the migration in the "not applied" bookkeeping
state (the Down round-trip above), unlike `db_watermark_migration_integration_test.go`'s
cross-migration interaction described in §8 (which requires the explicit false-then-true
force sequence precisely because that test never itself un-applies THIS migration's
version).

### T2 — `sourceSuperchargerSessions` constant sanity (offline, compiled by `go vet`)

Not a new test function — covered by the compiler. `go build ./... && go vet ./...`
failing to compile after the rename is itself the signal that every call site was updated;
no call site should remain referencing the old identifier `sourceChargeSessions`. Verified
manually during implementation via `grep -rn sourceChargeSessions internal/` returning
zero hits outside `db_watermark_migration_integration_test.go`'s pinned historic literals
(which are string literals, not the Go identifier, and are correctly left alone per §8).

## 10. What this change explicitly does NOT do

- No schema move (tier 2 already did this).
- No `sqlc.yaml` change and no `sqlc generate` — a CHECK constraint carries no column type
  sqlc reflects into generated Go structs; `VehicleMetricWatermark`'s shape is unaffected.
- No change to `Recalculator`'s or `Reader`'s public method signatures.
- No change to `vehicle_metrics` or `charge_gaps` — only `vehicle_metric_watermarks` is
  touched.
- No permanent rename of the reused string to something disambiguated (see §6's
  "Conclusion").
