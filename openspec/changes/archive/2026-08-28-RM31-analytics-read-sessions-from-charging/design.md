# Design — RM31-analytics-read-sessions-from-charging

**Design gate: TRIPPED.** This change alters `vehicle_metric_watermarks`'s CHECK constraint
and every existing row it constrains. Per `openspec/config.yaml` §`rules.design`, this file
carries the full schema change, the rationale (including a rejected alternative), and an index
plan justified against the project's read patterns — and per `CLAUDE.md`'s `Design-Gates`, the
change may not proceed to Apply without the owner's explicit confirmation of this document.

## 1. Scope recap and what was already verified

Two blockers stood between RM31 and this tier; both were resolved before this tier could be
scoped, and both are restated here (not re-derived) because they bound the retype plan below.

### 1a. Field parity — PASSES, no workaround needed

The three Supercharger-consuming functions in `internal/analytics` read exactly these fields:

| Function | File | Fields read |
|---|---|---|
| `sumSuperchargerPctBetween` | `consumed.go` | `ChargeStopDateTime`, `StartBatteryPct`, `EndBatteryPct` |
| `inferMissingChargingType` | `consumed.go` | `ChargeStopDateTime`, `StartBatteryPct`, `EndBatteryPct` |
| `sumSuperchargerKWh` | `reader.go` | `ChargeStartDateTime`, `EnergyKWh` |

All five fields exist on `charging.Session` with identical Go types (`time.Time`, `*int`,
`*float64`). The five fields `charge_sessions` deliberately omits — `CountryCode`,
`BillingType`, `UnlatchDateTime`, `VehicleMakeType`, `RawData` (RM29 design D1 — a closed
exclusion list) — feed nothing in `analytics`. **There is no workaround to design and nothing
to report as blocked here.**

### 1b. Port parity — PASSES as of tier 2, was the actual blocker

`analytics` consumes exactly three methods, on two different sibling ports today:

| Method today (`telemetry.SuperchargerReader`) | Method after this tier (`charging.SuperchargerSessionAnalyticsReader`) |
|---|---|
| `SuperchargerSessionsByVehicleBetween` | `ListSessionsByVehicleBetween` (inherited via the embedded `SessionReader`) |
| `SuperchargerSessionsByVehicleUpdatedSince` | `ListSessionsByVehicleUpdatedSince` |
| `SuperchargerSessionsByVehicle(limit)` | `ListSessionsByVehicle(limit)` |

Tier 2 (`RM31-charging-add-session-read-ports`, archived) added the two missing methods on a
**new** interface, `charging.SuperchargerSessionAnalyticsReader`, which embeds the pre-existing
`SessionReader` rather than widening it (tier 2 design D8 — `SessionReader` stays a
single-method interface because `internal/gateway` depends on it and must not be forced to
carry two methods it never calls). This tier depends on `charging.NewSuperchargerSessionAnalyticsReader`
existing, which it now does. **This module depends on `SuperchargerSessionAnalyticsReader`,
mirroring how it depends on `telemetry.SuperchargerReader` today** — the provider's exported
port, not a locally re-declared narrower interface, because all three of that port's methods
are used (two by `Recalculator`, one by `Reader`; no single caller uses all three, but the
module as a whole does, and the provider already ships the exact shape needed).

### 1c. The `charging.Session.TeslaID` nil case

`charging.Session.TeslaID` is `*int64` (nil when the VIN is not a currently-registered
vehicle), where `telemetry.SuperchargerSession.TeslaID` is also `*int64` with the identical
nullability contract. Neither of the three retyped functions above (§1a's table) reads a
`TeslaID` field at all — they operate purely on the time window and the two percentages/kWh
value. More importantly, `SuperchargerSessionAnalyticsReader`'s three methods each guarantee,
in their own doc comments (tier 2 design D4/D6), that **a session whose `TeslaID` is nil is
never returned by any vehicle-scoped read, for any `teslaID`** — `SQL NULL = value` is neither
true nor false, so an orphaned session is definitionally outside the result set before it ever
reaches Go code. The retyped functions are therefore structurally never handed a nil-`TeslaID`
row; see Test Contract T4 for the non-regression test that confirms this rather than adding new
filtering logic (there is none to add).

## 2. Database Changes

### 2a. What changes and why

`vehicle_metric_watermarks.source` is a closed-vocabulary label (no FK — see its own migration
comment) naming the physical table each of `Reconcile`'s three independent cursors tracks. As
of this tier, `analytics` no longer reads `telemetry.supercharger_sessions` for the Supercharger
path — it reads `charging.charge_sessions`. Leaving the CHECK constraint and existing row
values as `'supercharger_sessions'` would let this label lie about the table it names, which
is exactly the drift the constraint exists to prevent (its own migration's rationale: "a
free-standing string label" whose entire value is being self-describing and closed).

The fix is two-part, applied in the SAME migration:

1. The CHECK constraint's vocabulary changes from
   `('vehicle_snapshots', 'supercharger_sessions', 'manual_charge_entries')` to
   `('vehicle_snapshots', 'charge_sessions', 'manual_charge_entries')`.
2. Every existing row with `source = 'supercharger_sessions'` is **DELETED** — resetting that
   source's cursor to "no watermark," which `Reconcile`'s own `watermark` method already treats
   as the epoch (design D7). This is the roadmap document's Decision 10, unchanged by this
   revision — see §2c.

### 2b. Why DELETE is nearly free, and why that — not cursor preservation — is the deciding factor

This section previously argued for carrying the cursor value forward under an UPDATE. That
path is **not** what ships (see §2c) — Decision 10 calls for DELETE, and the reasoning below is
restated for its correct purpose: showing that DELETE's cost is bounded, not arguing that an
UPDATE would also have been safe.

- `charging.MirrorSessions` (the nightly mirror's write path into `charge_sessions`) sets
  `updated_at = now()` **unconditionally** on every upsert — no `WHERE` predicate limits it.
  Its own doc comment states `updated_at` means "the last mirror pass touched this row," which
  is **exactly** what `telemetry.supercharger_sessions.updated_at` already meant before this
  tier.
- Every session's `updated_at` is therefore bumped on **both** tables, every night, by the same
  mirror cycle (`internal/app`'s `processChargingData` reads `telemetry.supercharger_sessions`
  and writes `charge_sessions` through `charging.MirrorSessions`, per roadmap Decision 8 — the
  one legitimate reader of the telemetry table after this tier). **This is what makes the DELETE
  nearly free**, not a reason to prefer an UPDATE: with no stored watermark, the first
  post-migration `Reconcile` for a vehicle queries `ListSessionsByVehicleUpdatedSince(epoch)`
  against `charge_sessions` and gets back the vehicle's entire Supercharger session history —
  but because every row's `updated_at` was set by the SAME nightly mirror cycle that (until this
  migration) drove the old telemetry-sourced cursor, that full re-read touches nothing the
  vehicle's data hasn't already been reconciled against in substance. It is one redundant,
  UPSERT-idempotent backfill pass (`RM29-analytics-add-vehicle-metrics` D4 precedent), not a
  correctness gap and not an unbounded cost.
- **Why DELETE, not UPDATE, is still the right call despite that mechanism existing:**
  Decision 10's own rationale is the deciding factor, and it does not rest on the mechanism
  above being wrong — it rests on **not making the migration's correctness depend on an
  operational fact this migration cannot verify**. Carrying the cursor value forward is only as
  safe as the claim "the nightly mirror actually ran, every night, with no gap" — a claim this
  migration has no way to check at apply time. A stalled or skipped mirror cycle would strand a
  carried-over cursor ahead of rows it never actually reconciled under the new table, with
  **nothing able to detect it**: no error, no flag, just a silently under-covered vehicle. A
  single redundant backfill pass (the DELETE's cost, bounded and self-correcting) is the cheaper
  failure mode than a silent, undetectable one.

**No SEPARATE watermark-reset migration is needed** — the reset is folded into this same
migration's DELETE statement, exactly as `20260822000002_reset_vehicle_metric_watermarks.sql`
did for the `vehicle_snapshots` source during RM29 tier 4. Only the `supercharger_sessions` →
`charge_sessions` source is reset here; `vehicle_snapshots` and `manual_charge_entries` rows for
the same vehicles are untouched, because those two sources' underlying data and derivation are
unaffected by this tier.

### 2c. The roadmap document is internally inconsistent; Decision 10 is authoritative

`openspec/roadmaps/RM31-supercharger-session-verification.md` contains two statements that
contradict each other: its **tier-3 table cell** (the "Proposal prompt" column, in the row for
this change) describes an in-place value migration ("existing `'supercharger_sessions'` rows
are UPDATEd to `'charge_sessions'` in the same migration"), while its own **Decision 10**,
written in the same document, states the opposite: "Existing `vehicle_metric_watermarks` rows
with `source = 'supercharger_sessions'` are DELETEd, not UPDATEd to `'charge_sessions'`... An
absent watermark row is *defined* as the epoch... so the next nightly `Reconcile` rebuilds each
vehicle's history once from the new table."

This design initially followed the stale table-cell wording (via the dispatch that scoped this
worker, which had inherited it). **That was an error, corrected at the design gate on
2026-08-28: the owner reconfirmed Decision 10 as written — DELETE, not UPDATE.** There was no
separate 2026-08-28 owner decision overriding Decision 10; the only 2026-08-28 owner decision
in scope was a different question entirely — whether to migrate the `source` label's vocabulary
at all, versus leaving `'supercharger_sessions'` as a stale-but-opaque string (the owner chose
to migrate the label; see §1's "why this is right" framing, unaffected by this correction). That
label-migration choice is orthogonal to the UPDATE-vs-DELETE question this section resolves.

**Decision 10, as written in the roadmap document, is authoritative and is what §2e implements.**
The leader is responsible for correcting the roadmap document's stale tier-3 table cell
separately, so it no longer contradicts Decision 10 lower in the same file. This design.md is
not the record of a supersession — there was none — and no future reader of this file should
conclude Decision 10 was ever overridden.

### 2d. Migration ordering and safety

The three-step order below is what makes the migration constraint-safe — a naive
"update-then-drop-then-add" or a single unconditional `ADD CONSTRAINT` before the data is
migrated would reject rows mid-migration:

1. **DROP** the existing CHECK constraint first. This removes all vocabulary enforcement
   temporarily — deliberately, so a concurrent write during the migration window cannot be
   rejected by a constraint mid-transition. (The DELETE below needs no such protection — deleting
   rows can never violate a CHECK constraint — but the drop-first ordering is kept uniform with
   every other step of this migration and costs nothing.)
2. **DELETE** every `source = 'supercharger_sessions'` row, with no other row touched — the
   `vehicle_snapshots` and `manual_charge_entries` rows for the same vehicles are left exactly as
   they are.
3. **ADD** the new CHECK constraint, now that every remaining row already satisfies its
   (narrower) vocabulary — this step cannot fail, because step 2 already guaranteed no row holds
   `'supercharger_sessions'` any more, and no other value was ever legal under the OLD
   constraint that is not also legal under the new one (`vehicle_snapshots` and
   `manual_charge_entries` are unchanged members of both lists).

Postgres auto-names an inline, unnamed `CHECK` column constraint as
`<table>_<column>_check`, so the existing constraint on `vehicle_metric_watermarks.source` is
named `vehicle_metric_watermarks_source_check` — confirmed by inspecting
`20260821000002_add_vehicle_metric_watermarks.sql`, which declares the CHECK inline with no
explicit `CONSTRAINT` name.

### 2e. Full migration SQL

New file: `internal/analytics/db/migrations/20260828000001_migrate_vehicle_metric_watermarks_source.sql`
(naming convention: `<YYYYMMDDHHMMSS>_<name>.sql`, matching every existing file in this
directory — timestamp is later than `20260822000002`, the most recent migration in this
module's directory).

```sql
-- +goose Up
-- internal/analytics — RM31-analytics-read-sessions-from-charging (MAG-19 tier 3).
--
-- vehicle_metric_watermarks.source is a closed-vocabulary label naming the physical
-- table each of Reconcile's three independent cursors tracks (see this table's own
-- migration, 20260821000002_add_vehicle_metric_watermarks.sql). As of this change,
-- internal/analytics no longer reads internal/telemetry.supercharger_sessions for the
-- Supercharger path -- it reads internal/charging.charge_sessions instead (roadmap
-- Decision 1/8). Leaving the label as 'supercharger_sessions' would let it name a
-- table this module no longer reads, defeating the column's own purpose.
--
-- The 'supercharger_sessions' cursor rows are RESET TO EPOCH (DELETEd), not renamed
-- in place -- roadmap Decision 10, reconfirmed by the owner at the design gate on
-- 2026-08-28. An absent watermark row is DEFINED as the epoch by
-- Recalculator.Reconcile's own watermark() method (tier 3 design D7 of
-- RM29-analytics-add-vehicle-metrics), so the next nightly Reconcile for each
-- affected vehicle backfills that source's entire history from charge_sessions in
-- one pass -- no separate backfill migration or one-off binary, exactly the
-- 20260822000002_reset_vehicle_metric_watermarks.sql precedent.
--
-- This is nearly free, not merely correct: charging.MirrorSessions sets
-- updated_at = now() UNCONDITIONALLY on every nightly upsert into charge_sessions,
-- the same "last mirror pass touched this row" meaning
-- telemetry.supercharger_sessions.updated_at already carried -- so the resulting
-- full re-read touches nothing the vehicle's data hasn't already been reconciled
-- against in substance (design.md Sec 2b). DELETE, not UPDATE, is still the right
-- call: carrying the cursor value forward would make the migration's correctness
-- depend on the nightly mirror having actually run without a gap -- an operational
-- fact this migration cannot verify, and a stalled mirror would strand a
-- carried-over cursor with nothing able to detect it. A single redundant backfill
-- pass is the cheaper, self-correcting failure mode (design.md Sec 2b/2c).
--
-- Only the supercharger_sessions -> charge_sessions source is reset here;
-- vehicle_snapshots and manual_charge_entries rows are untouched.
--
-- Ordering (design.md Sec 2d): DROP the constraint before the DELETE (uniform with
-- every other step of this migration, though a DELETE cannot itself violate a CHECK
-- constraint); ADD the new constraint only once every remaining row already
-- satisfies it, so the ADD cannot fail.
ALTER TABLE vehicle_metric_watermarks
    DROP CONSTRAINT IF EXISTS vehicle_metric_watermarks_source_check;

DELETE FROM vehicle_metric_watermarks
WHERE source = 'supercharger_sessions';

ALTER TABLE vehicle_metric_watermarks
    ADD CONSTRAINT vehicle_metric_watermarks_source_check
    CHECK (source IN ('vehicle_snapshots', 'charge_sessions', 'manual_charge_entries'));

COMMENT ON TABLE vehicle_metric_watermarks IS
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

COMMENT ON COLUMN vehicle_metric_watermarks.source IS
    'Closed 3-value vocabulary naming the physical table this cursor tracks (design D3): '
    '''vehicle_snapshots'' (internal/telemetry), ''charge_sessions'' (internal/charging, as '
    'of RM31-analytics-read-sessions-from-charging -- previously ''supercharger_sessions'' in '
    'internal/telemetry), or ''manual_charge_entries'' (internal/charging). No FK -- a '
    'free-standing string label, mirroring charge_gaps.missing_charging_type''s identical '
    'convention.';

-- +goose Down
-- IRREVERSIBLE for the deleted rows, and HARMLESS -- exact precedent:
-- 20260822000002_reset_vehicle_metric_watermarks.sql's own Down. A DELETE cannot be
-- undone by an UPDATE (there is no row left to update, and no source_updated_at
-- value recorded anywhere to restore even if there were). The only consequence of
-- rolling back is that the next Reconcile for an affected vehicle backfills the
-- supercharger_sessions/charge_sessions source once more from epoch -- an absent
-- watermark row is DEFINED as the epoch (tier 3 design D7), so there is no state to
-- lose and nothing to actually undo, only a redundant recompute pass.
--
-- What Down DOES restore: the CHECK constraint's old vocabulary and the original
-- table/column COMMENTs, so a rollback leaves the schema exactly as it was before Up
-- -- only the deleted rows themselves are unrecoverable, and their absence degrades
-- to "epoch," never to an error.
ALTER TABLE vehicle_metric_watermarks
    DROP CONSTRAINT IF EXISTS vehicle_metric_watermarks_source_check;

-- CORRECTION (2026-08-28, owner-confirmed at a re-opened design gate). This DELETE was
-- absent from the design as first approved, and its absence was a real defect: restoring
-- the old vocabulary while any source = 'charge_sessions' row exists fails with SQLSTATE
-- 23514 and leaves the table with NO constraint. Reconcile writes such rows on every pass
-- (recalculate.go's advanceWatermark), so the Down was unrunnable in production from the
-- first nightly run after Up. Caught by this change's own T1 round-trip test.
DELETE FROM vehicle_metric_watermarks
WHERE source = 'charge_sessions';

ALTER TABLE vehicle_metric_watermarks
    ADD CONSTRAINT vehicle_metric_watermarks_source_check
    CHECK (source IN ('vehicle_snapshots', 'supercharger_sessions', 'manual_charge_entries'));

COMMENT ON TABLE vehicle_metric_watermarks IS
    'One recompute cursor per (account_id, tesla_id, source) for internal/analytics.Recalculator.'
    'Reconcile (RM29-analytics-add-vehicle-metrics, MAG-26 tier 3). Three independent sources '
    '(design D3): vehicle_snapshots, supercharger_sessions, manual_charge_entries -- each advances '
    'on its own row, never coupled to the others'' clocks. No watermark row yet for a '
    '(account_id, tesla_id, source) means "epoch" (design D7): Reconcile backfills the vehicle''s '
    'full history in one pass. Owned by internal/analytics; no other module reads this table '
    'directly.';

COMMENT ON COLUMN vehicle_metric_watermarks.source IS
    'Closed 3-value vocabulary naming the physical table this cursor tracks (design D3): '
    '''vehicle_snapshots'' (internal/telemetry), ''supercharger_sessions'' (internal/telemetry), '
    'or ''manual_charge_entries'' (internal/charging). No FK -- a free-standing string label, '
    'mirroring charge_gaps.missing_charging_type''s identical convention.';
```

### 2f. Index plan

**No new index is created, and none is needed.** The sole read pattern this table serves —
`Reconcile`'s single-row watermark lookup (`recalculate.go`'s `watermark` method), issuing
`WHERE account_id = $1 AND tesla_id = $2 AND source = $3` — is already served in full by
`vehicle_metric_watermarks_account_tesla_source_unique`, the table's own `UNIQUE (account_id,
tesla_id, source)` constraint index, declared in
`20260821000002_add_vehicle_metric_watermarks.sql` and untouched by this migration. This
migration changes only a CHECK constraint's vocabulary and existing rows' `source` values —
neither adds, removes, nor reshapes a column the existing index covers, and neither the WHERE
clause nor the index definition changes. This mirrors the "Index Plan" note already present in
that table's own original migration file, restated here because `openspec/config.yaml` requires
every DB-touching design to state its index plan explicitly rather than leave it implied.

The `DELETE` statement in §2e (`WHERE source = 'supercharger_sessions'`) is a one-time,
low-cardinality data migration — this table holds one row per `(account_id, tesla_id, source)`,
i.e. three rows per vehicle at most — and does not need its own index; the delete runs once, at
migration time, not on a hot path.

## 3. Go retype plan

All three files below are edited together for the package to compile; they touch disjoint
files so are safe to implement in parallel (see tasks.md), but the package will not `go build`
until all three land.

### `consumed.go`

- `sumSuperchargerPctBetween(sessions []telemetry.SuperchargerSession, from, to time.Time) float64`
  → `sumSuperchargerPctBetween(sessions []charging.Session, from, to time.Time) float64`. Body
  unchanged — same three fields, same comparisons.
- `inferMissingChargingType(sessions []telemetry.SuperchargerSession, from, to time.Time) MissingChargingType`
  → `inferMissingChargingType(sessions []charging.Session, from, to time.Time) MissingChargingType`.
  Body unchanged.
- `deriveVehicleMetrics(preceding *telemetry.Snapshot, snapshots []telemetry.Snapshot, sessions []telemetry.SuperchargerSession, entries []charging.Entry, start, end time.Time) []vehicleMetricRow`
  → `sessions []charging.Session` in the signature; `preceding`/`snapshots` stay
  `telemetry.Snapshot` (snapshots are unaffected by this tier). Body unchanged — it only calls
  `sumSuperchargerPctBetween`/`inferMissingChargingType`, both retyped above.
- The `charging` import is already present in this file (for `charging.Entry`); no import list
  change beyond what the retyped signatures need. The `telemetry` import remains required
  (`telemetry.Snapshot`).
- Doc comments referencing `telemetry.SuperchargerSession` (the file header, and each retyped
  function's own comment) are updated to `charging.Session`.

### `reader.go`

- `reader.supercharger` field: `telemetry.SuperchargerReader` → `charging.SuperchargerSessionAnalyticsReader`.
- `sumSuperchargerKWh(sessions []telemetry.SuperchargerSession, since time.Time) float64` →
  `sumSuperchargerKWh(sessions []charging.Session, since time.Time) float64`. Body unchanged.
- `RecentEfficiency`'s call site: `r.supercharger.SuperchargerSessionsByVehicle(ctx, accountID, teslaID, chargingSourceLimit)`
  → `r.supercharger.ListSessionsByVehicle(ctx, accountID, teslaID, chargingSourceLimit)`.
- `NewReader(pool *pgxpool.Pool, telemetryReader telemetry.Reader, supercharger telemetry.SuperchargerReader, manual charging.Reader, acct vehicleLookup, window time.Duration) Reader`
  → `supercharger charging.SuperchargerSessionAnalyticsReader` in the signature. Parameter name
  unchanged (`supercharger` describes the role, not the package).
- `charging` import is already present (for `charging.Reader`); `telemetry` import remains
  required (`telemetry.Reader`, `telemetry.Snapshot` via `SnapshotsByVehicleSince`'s return
  type).

### `recalculate.go`

- `recalculator.supercharger` field: `telemetry.SuperchargerReader` → `charging.SuperchargerSessionAnalyticsReader`.
- `Recalculate`'s call site: `r.supercharger.SuperchargerSessionsByVehicleBetween(ctx, accountID, teslaID, chargeStart, end.AddDate(0, 0, 2))`
  → `r.supercharger.ListSessionsByVehicleBetween(...)` (same arguments).
- `Reconcile`'s call site: `r.supercharger.SuperchargerSessionsByVehicleUpdatedSince(ctx, accountID, teslaID, scsCursor.Add(-recalcOverlap))`
  → `r.supercharger.ListSessionsByVehicleUpdatedSince(...)` (same arguments).
- `NewRecalculator(pool *pgxpool.Pool, telemetryReader telemetry.Reader, supercharger telemetry.SuperchargerReader, manual charging.Reader) Recalculator`
  → `supercharger charging.SuperchargerSessionAnalyticsReader` in the signature.
- **The `sourceSuperchargerSessions` constant's identifier IS renamed**, to
  `sourceChargeSessions`; its value changes from `"supercharger_sessions"` to
  `"charge_sessions"`. Rationale: this module's own established convention (stated verbatim in
  `20260821000002_add_vehicle_metric_watermarks.sql`'s comment, restated in this module's own
  `AGENTS.md`) is that each source constant is "named after the physical table each source's
  data lives in." Keeping the Go identifier `sourceSuperchargerSessions` while its value points
  at `charge_sessions` would violate that self-imposed convention on the very line that most
  needs to stay trustworthy — a future reader grepping for `"charge_sessions"` usage in this
  module would miss this constant entirely. All four usage sites in `recalculate.go`
  (`Reconcile`'s three watermark calls plus the constant declaration) rename together; no other
  file references this identifier by name (test files reference it as
  `analytics.sourceChargeSessions` is not applicable — it's unexported; test files in
  `package analytics` reference it directly and are covered by the retype tasks below).
- `charging` and `telemetry` imports are both already present; no import list change.

### Test files

- `consumed_test.go`: every fixture constructing `telemetry.SuperchargerSession{...}` for a
  `sessions` argument to `deriveVehicleMetrics`/`sumSuperchargerPctBetween`/
  `inferMissingChargingType` becomes `charging.Session{...}` — a literal field-for-field swap
  (same field names, same types, per §1a). The `telemetry` import stays for `Snapshot`
  fixtures.
- `reader_test.go`: `fakeSuperchargerReader` (implements `telemetry.SuperchargerReader` today)
  is retyped to implement `charging.SuperchargerSessionAnalyticsReader` — same three methods
  used by this file (`ListSessionsByVehicle` replaces `SuperchargerSessionsByVehicle`,
  `ListSessionsByVehicleBetween`/`ListSessionsByVehicleUpdatedSince` replace their
  `Supercharger...` equivalents, with the same "must not be called from this path" panics
  preserved on whichever methods `Reader` never calls). Every `sessions: []telemetry.SuperchargerSession{...}`
  fixture becomes `[]charging.Session{...}`.
- `recalculate_test.go`: `fakeSuperchargerReader` retypes identically to `reader_test.go`'s
  (implements `charging.SuperchargerSessionAnalyticsReader`); every fixture retypes identically.
- `db_integration_test.go`: the direct-SQL seeding helper that currently `INSERT`s into
  `supercharger_sessions` (this module's own D19 test-seeding convention — no public writer
  exists for a bare Supercharger session, and this module may not import `internal/tesla`)
  retargets to `charge_sessions`, using that table's actual column set (§2's migration file,
  `internal/charging/db/migrations/20260823000001_add_charge_sessions.sql`, is the schema
  reference). `sourceSuperchargerSessions` references become `sourceChargeSessions`. See
  tasks.md for the exact scope split.

### `internal/analytics/AGENTS.md`

"Allowed / forbidden imports" §"May import" updates:
`internal/telemetry` — drops `SuperchargerReader (SuperchargerSessionsByVehicle)` and
`telemetry.SuperchargerSession` from its bullet, keeping `telemetry.Reader`'s methods and
`telemetry.Snapshot` unchanged. `internal/charging` — gains
`SuperchargerSessionAnalyticsReader` (`ListSessionsByVehicleBetween`,
`ListSessionsByVehicleUpdatedSince`, `ListSessionsByVehicle`) alongside the existing
`Reader`/`Entry`. "Must NOT import" is unaffected (still forbids `telemetrydb`/`chargingdb`/
`accountdb`; nothing there names `SuperchargerReader` specifically). The "Public interface"
and "Data ownership" sections' prose referencing "the two charging-cost sources
(`telemetry.SuperchargerReader` and `internal/charging`)" is updated to name
`charging.SuperchargerSessionAnalyticsReader` and `charging.Reader` respectively.

## 4. `cmd/web` wiring (leader-owned, outside this tier's sandbox)

`cmd/web/main.go` constructs `telemetry.NewSuperchargerReader(pool)` and passes the result into
both `analytics.NewRecalculator(...)` and `analytics.NewReader(...)`. After this tier's retype,
both constructors' `supercharger` parameter requires a
`charging.SuperchargerSessionAnalyticsReader`, which `charging.NewSuperchargerSessionAnalyticsReader(pool)`
(added by tier 2, already merged) provides. The leader must change both call sites from
`telemetry.NewSuperchargerReader(pool)` to `charging.NewSuperchargerSessionAnalyticsReader(pool)`
— this is a one-line swap at each of the two (or more, if `cmd/poller` independently
constructs a `Recalculator`/`Reader`) call sites; grep for `NewSuperchargerReader` in `cmd/` to
find every site. `telemetry.NewSuperchargerReader` itself is not removed — `telemetry`'s own
`SuperchargerReader` port is unrelated code, still valid, still backing nothing analytics
depends on after this tier.

## 5. Test Contract (authored before implementation exists)

Fixtures are pinned so tests written against them, later, confirm the design rather than the
implementation. `day(y,m,d)` denotes a UTC-midnight `time.Time` per `consumed_test.go`'s
existing helper.

### T1 — the migration DELETEs only the `supercharger_sessions` watermark, leaving its siblings untouched

**Given** (pre-migration state — migrations applied through `20260822000002` only, then three
rows inserted directly for the SAME vehicle, one per source):
```sql
INSERT INTO vehicle_metric_watermarks (account_id, tesla_id, source, source_updated_at) VALUES
  ('11111111-1111-1111-1111-111111111111', 555, 'supercharger_sessions', '2026-08-20T10:00:00Z'),
  ('11111111-1111-1111-1111-111111111111', 555, 'vehicle_snapshots',     '2026-08-21T03:30:00Z'),
  ('11111111-1111-1111-1111-111111111111', 555, 'manual_charge_entries','2026-08-19T18:00:00Z');
```
**When** migration `20260828000001_migrate_vehicle_metric_watermarks_source.sql` is applied.

**Then:**
- `SELECT COUNT(*) FROM vehicle_metric_watermarks WHERE account_id = '11111111-1111-1111-1111-111111111111' AND tesla_id = 555 AND source = 'supercharger_sessions'`
  = 0 — the row is **gone**, not renamed (this is the assertion that distinguishes DELETE from
  the rejected UPDATE alternative in §6: a renamed row would still satisfy a `source =
  'charge_sessions'` count of 1 here, but there is no `charge_sessions` row for this vehicle at
  all post-migration — its cursor is simply absent, i.e. epoch).
- `SELECT source_updated_at FROM vehicle_metric_watermarks WHERE account_id = '11111111-1111-1111-1111-111111111111' AND tesla_id = 555 AND source = 'vehicle_snapshots'`
  = `'2026-08-21T03:30:00Z'`, **unchanged** — this source is untouched by this migration.
- `SELECT source_updated_at FROM vehicle_metric_watermarks WHERE account_id = '11111111-1111-1111-1111-111111111111' AND tesla_id = 555 AND source = 'manual_charge_entries'`
  = `'2026-08-19T18:00:00Z'`, **unchanged** — this source is untouched by this migration.
- `SELECT COUNT(*) FROM vehicle_metric_watermarks WHERE account_id = '11111111-1111-1111-1111-111111111111' AND tesla_id = 555`
  = 2 (down from 3 — exactly the `vehicle_snapshots` and `manual_charge_entries` rows survive).
- `INSERT INTO vehicle_metric_watermarks (account_id, tesla_id, source, source_updated_at) VALUES ('22222222-2222-2222-2222-222222222222', 1, 'supercharger_sessions', now())`
  fails with a CHECK-constraint violation on `vehicle_metric_watermarks_source_check`.
- The identical INSERT with `source = 'charge_sessions'` succeeds.
- **Semantic consequence, asserted via `Reconcile`'s own `watermark` method (not a raw SQL
  check):** calling `r.watermark(ctx, accountID, teslaID, sourceChargeSessions)` for this
  vehicle after the migration returns the zero-value epoch (`time.Time{}`, no error) — the
  absence of a row IS the epoch signal (design D7), exactly as it is for a vehicle that has
  never been reconciled at all.
- **Down round-trip:** applying `-- +goose Down` restores the old CHECK constraint
  (`source IN ('vehicle_snapshots', 'supercharger_sessions', 'manual_charge_entries')`) and the
  original table/column `COMMENT`s, but does **NOT** restore the deleted
  `supercharger_sessions` row for `(account_id='1111...', tesla_id=555)` — it does not exist
  after Down either, by design (§2e's Down comment: the DELETE is irreversible for the row
  itself, only the schema — constraint and comments — rolls back). Asserting this negative (row
  still absent post-Down) is part of this test: a Down that resurrected the row would be a bug,
  not a feature.

### T2 — `Reconcile` reads sessions through the `charging` port, not `telemetry`

**Given:** `accountID = A` (a fixed UUID), `teslaID = 42`. No prior watermark rows for any
source (epoch). Telemetry snapshots seeded: `prev` at `day(2026,8,13)`, `BatteryLevelPct = 80`,
`OdometerKm = 100`; `cur` at `day(2026,8,14)`, `BatteryLevelPct = 75`, `OdometerKm = 140`. A
`charging.Session` fixture (via a `charging.SuperchargerSessionAnalyticsReader` test double,
offline test; or a seeded `charge_sessions` row, DB-integration test):
```
Session{
  SessionID:           900,
  TeslaID:             ptr(int64(42)),
  ChargeStartDateTime: time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC),
  ChargeStopDateTime:  time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC),
  StartBatteryPct:     ptr(20),
  EndBatteryPct:       ptr(80),
  EnergyKWh:           ptr(30.0),
}
```

**When** `Reconcile(ctx, A, 42)` is called.

**Then:**
- The fake/test double's `ListSessionsByVehicleUpdatedSince` is invoked (recorded call log
  entry) — its `SuperchargerSessionsByVehicleUpdatedSince`-shaped sibling method (were the fake
  to accidentally still implement the OLD interface) is never invoked. This is the direct
  assertion that the retype actually changed which port is consulted, not just which package
  compiles.
- `deriveConsumption(prev, cur)` yields `BatteryUsedPctCalc = 80 - 75 = 5`.
- `sumSuperchargerPctBetween` over the one session yields `80 - 20 = 60` (the session's stop
  instant `08:30` falls within `[prev.CapturedAt, cur.CapturedAt)`).
- The resulting `vehicle_metrics` row for `metric_date = day(2026,8,14)`:
  `battery_used_pct_calc = 5`, `consumed_pct = 5 + 60 = 65`, `flagged = false` (65 is neither
  negative nor zero).
- A `vehicle_metric_watermarks` row for `(A, 42, 'charge_sessions')` is created (no prior row —
  epoch), advanced to the session's `updated_at` observed on this run.

### T3 — a verified percentage that differs from telemetry's copy lands the charging-sourced value

This is the direct proof that the switch — not merely a type change — has the intended effect:
the SAME underlying event, differently recorded on each side of the migration, produces
different `vehicle_metrics` output depending on which table is read.

**Given:** `accountID = A`, `teslaID = 42`, `SessionID = 901`. Two representations of the SAME
Supercharger session exist simultaneously (as they would mid-migration, or in any test that
seeds both tables to prove the read moved):
- `telemetry.supercharger_sessions` row for session 901: `StartBatteryPct = 30`,
  `EndBatteryPct = 70` (delta = 40 — a stale/unverified reading, or simply what telemetry's own
  copy happens to hold).
- `charging.charge_sessions` row for session 901 (mirrored, then human-verified via tier 1's
  `SessionVerifier.VerifySession`): `StartBatteryPct = 20`, `EndBatteryPct = 90` (delta = 70 —
  the human-corrected value).

Snapshots: `prev` at `day(2026,8,20)`, `BatteryLevelPct = 90`; `cur` at `day(2026,8,21)`,
`BatteryLevelPct = 85` (`BatteryUsedPctCalc = 90 - 85 = 5`). Session 901's `ChargeStopDateTime`
falls within `[prev.CapturedAt, cur.CapturedAt)`.

**When** `Recalculate`/`Reconcile` runs for this vehicle-day, reading through
`charging.SuperchargerSessionAnalyticsReader` (this tier's retyped path).

**Then:**
- `vehicle_metrics.consumed_pct` for `day(2026,8,21)` = `5 + 70 = 75` — the **charging-sourced**
  delta.
- `vehicle_metrics.consumed_pct` is explicitly asserted **NOT** `5 + 40 = 45`, which is what
  this same day would have computed under the pre-tier code path (reading
  `telemetry.supercharger_sessions`). Asserting the negative case is the point of this test: it
  is the one assertion in this Test Contract that would fail silently — i.e. still pass a
  weaker test — if the retype compiled correctly but a stray call site were left pointed at the
  old port.

### T4 — the nil-`TeslaID` case (non-regression, not new filtering)

**Given:** a `charging.Session` fixture with `TeslaID = nil` (the VIN is not a
currently-registered vehicle) and a `ChargeStopDateTime` that would otherwise fall inside the
window under test for `teslaID = 42`.

**When** `Reconcile(ctx, A, 42)` is called with a `charging.SuperchargerSessionAnalyticsReader`
test double that (correctly, per tier 2's own contract) never returns this row for
`teslaID = 42` from any of its three methods — the double enforces the same
`tesla_id = @tesla_id` filtering the real SQL implementation does, since `SQL NULL = value` is
never true.

**Then:**
- No `vehicle_metrics` row is created or altered for this session's day on account of this
  session — the day's `consumed_pct` (if a row exists for other reasons) does not include this
  session's `(EndBatteryPct - StartBatteryPct)` delta.
- `sumSuperchargerPctBetween`/`inferMissingChargingType` are never even invoked with this row
  present, because the port itself already excluded it before the slice reached these
  functions — this test asserts the ABSENCE of the row from what `Reconcile` receives, not a
  new nil-check inside `consumed.go` (there is none to add — see §1c).
- Compiles and runs without a nil-pointer dereference regardless: neither
  `sumSuperchargerPctBetween` nor `inferMissingChargingType` ever reads `TeslaID`, so even a
  hypothetical future port that failed to filter would not crash these two functions — this is
  a documented property, not the primary thing under test here.

## 6. Rejected alternatives

- **Keeping the watermark label as an opaque string, value unchanged** (i.e. no migration at
  all — `'supercharger_sessions'` stays valid vocabulary, still describing what is now the
  wrong table). Rejected by the owner (2026-08-28): the label and its CHECK-enforced vocabulary
  exist specifically to be self-describing and closed; leaving it stale would mean the column's
  own purpose no longer holds for one of its three values, defeating the reason the column
  documents its vocabulary in a `COMMENT` at all. See §2c.
- **Carrying the cursor value forward via an in-place UPDATE**, instead of the DELETE/epoch-reset
  Decision 10 actually specifies. This was this design's OWN first draft, corrected at the
  design gate on 2026-08-28 (§2c) — it is not a path the owner ever chose over DELETE; it was a
  stale reading of the roadmap document's tier-3 table cell, which contradicts Decision 10
  lower in the same file. Rejected once the mistake was caught: an UPDATE would make this
  migration's correctness depend on the nightly mirror having run without a gap — an
  operational fact the migration cannot verify — where a DELETE's redundant-backfill cost is
  bounded and self-correcting regardless (§2b).
- **Adding `charge_sessions` as a FOURTH watermark source, alongside the retained
  `supercharger_sessions`** — considered and rejected at the roadmap level (Decision 1), not
  re-litigated here: it would require a conflict rule for what happens when both sources
  disagree on the same session's percentages, which the "move, don't duplicate" approach avoids
  entirely by construction (exactly one table ever carries a session's percentages after this
  tier).
- **Narrowing the dependency to a locally-declared interface covering only the methods each of
  `Reader`/`Recalculator` individually calls**, instead of depending on the full
  `charging.SuperchargerSessionAnalyticsReader`. Rejected: the provider (`charging`) already
  ships the exact three-method shape the module as a whole needs (tier 2 built it for this
  purpose), and `analytics` already follows this same "depend on the provider's exported port"
  pattern for `telemetry.SuperchargerReader` today — introducing a second, narrower,
  locally-declared interface here would be inconsistent with that existing convention for no
  benefit (`ai/architecture.md`'s consumer-side-interface guidance exists for cycle-breaking,
  which does not apply here — `charging` does not depend on `analytics`).
