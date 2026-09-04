# Design — RM41-charging-add-session-status

## Context

`charging.supercharger_sessions` mirrors each Tesla Supercharger session and carries a
human-write verification channel — `start_battery_pct`, `end_battery_pct`,
`battery_pct_source` — written only through `SessionVerifier.VerifySession`
(`internal/charging/session_verifier.go`). Tesla supplies neither percentage; a human
fills them in, and since MAG-36 (`charging-add-derived-start-battery-pct`) the start
percentage can also be derived from the end percentage and the session's `energy_kwh`
via `derivedStartBatteryPct` (`internal/charging/capacity.go`).

Nothing today tells a caller whether a session's battery data is *complete* without
reading both percentage columns and re-deriving the three-state logic itself. MAG-45
(this roadmap's tier 4/5) adds a stored, auto-set `status` column so every future reader
gets one flat value. Tier 5 (`gateway`, depends on this tier) renders it as a
`ui.Badge`, second column, mirroring `/charges`' own status badge (roadmap D14) — not
this tier's job.

This design is written against the roadmap's binding decisions D10/D11/D13 and two
further decisions the leader resolved with the owner today, closing gaps the roadmap
left open (see proposal.md "Resolved decisions" for both — CALC MARKER and BACKFILL).
Everything else below is this design's own local decision, made because the roadmap
explicitly deferred it to the implementer.

## Goals / Non-Goals

**Goals:**
- A stored `status` column on `charging.supercharger_sessions`, recomputed by
  `SessionVerifier.VerifySession` on every call — never computed on read, never
  user-settable.
- A complete state truth table covering every combination of the two percentage
  columns, including the start-set/end-NULL corner the roadmap did not spell out.
- A migration that ships the column AND backfills every pre-existing row to
  `DONE_CALCULATED`, per the owner's explicit, twice-confirmed decision.
- `charging.Session` and every `SessionReader`/`SuperchargerSessionAnalyticsReader`
  method expose the new field with no SQL change (all three reads already use
  `SELECT *`).

**Non-Goals (explicitly out of scope for this tier):**
- Rendering the status anywhere (tier 5, `gateway`, depends on this tier).
- Any change to `battery_pct_source`'s value set or CHECK (CALC MARKER decision:
  no `derived` value is added).
- Any change to `telemetry.supercharger_history` — it has no status column and none
  is proposed; this is charging-owned verification-channel metadata, not a mirrored
  fact.
- Any new query predicate, index, or read path filtering/counting by status — no
  consumer in this tier's scope needs one (see "Index plan" below).

## Database Design (design gate — `database`, per `openspec/config.yaml` and `CLAUDE.md` Pipeline config)

### State truth table

**One-sentence rule:** a session's status is `IN_PROGRESS` unless BOTH percentage
columns end up non-`NULL` after the write; when both are present, it is
`DONE_CALCULATED` if THIS write derived the start percentage rather than storing a
caller-supplied one, and `DONE` otherwise.

| `start_battery_pct` (final, after this write) | `end_battery_pct` (final) | This write derived `start`? | `status` |
|---|---|---|---|
| NULL | NULL | — | `IN_PROGRESS` |
| NULL | non-NULL | derivation not attempted or failed (no `energy_kwh`, or out of `[0,100]`) | `IN_PROGRESS` |
| non-NULL | NULL | — | `IN_PROGRESS` |
| non-NULL (derived this call) | non-NULL | yes | `DONE_CALCULATED` |
| non-NULL (caller-supplied) | non-NULL | no | `DONE` |

**The start-set/end-NULL corner, explicitly** (the leader's own instruction singled
this out): a session carrying a typed `start_battery_pct` but no `end_battery_pct` is
`IN_PROGRESS` — the same status as a session with nothing recorded at all. This is
deliberate, not an oversight: the three-state design exists so tier 5's "fill in the
percentages of sessions still in progress" recommendation catches every session whose
record is incomplete, and a start-only session is exactly as incomplete as an empty
one — it cannot produce `InferredCapacityKWhCalc` (which needs both percentages plus
`energy_kwh`), it cannot be told apart from "nobody has looked at this yet" by anyone
who only reads `status`, and there is no product reason to invent a fourth state for
"half-done" when the existing recommendation already tells the user what to do next
regardless of which half is missing. Adopted as given in the dispatch, no
counter-evidence found.

**Why status is computed from the write's OWN behavior, not recomputed by re-reading
the row** (this is what makes `DONE` vs `DONE_CALCULATED` possible at all):
`battery_pct_source` already cannot tell a derived percentage from a typed one once
stored — both are `"user_verified"` (MAG-36 design.md D1, restated in
`internal/charging/AGENTS.md`). If `status` were computed purely from the two stored
percentage values, it could never be more informative than `battery_pct_source`
already is. `VerifySession` is the ONLY place in the codebase that knows, at the
instant of writing, whether the value it is about to store for `start_battery_pct`
came from the caller or from `derivedStartBatteryPct` — so it is the only place that
can compute `status` at all. This is why the CALC MARKER decision assigns the
computation to `VerifySession` rather than to a database trigger or a read-time
function: no trigger or read-time computation has access to that fact once the row is
written.

### Schema change

`internal/charging/db/migrations/20260903000004_add_session_status.sql` (sorts after
the newest existing migration, `20260903000003_drop_supercharger_est_columns.sql` in
`internal/telemetry/db/migrations`, and after this module's own
`20260903000002_drop_supercharger_est_columns.sql` — confirmed no other module's
migration directory uses `20260903000004`, per `make migration-guard`'s own
duplicate-version check across `MIGRATIONS_DIRS`):

```sql
-- +goose Up
-- RM41 tier 4 (charging-add-session-status, MAG-45): a Supercharger session gains a
-- stored, auto-set lifecycle status, mirroring manual_charge_entries.status's shape
-- (20260829000002_add_entry_status.sql) with three codes instead of two.
--
-- WHY TEXT + CHECK, not a Postgres ENUM -- identical rationale to 20260829000002's own:
-- consistency with every other lifecycle/provenance column on these two tables
-- (manual_charge_entries.status, manual_charge_entries.energy_source,
-- supercharger_sessions.battery_pct_source), and altering an enum's value set later is
-- painful (ALTER TYPE ... ADD VALUE cannot run inside a transaction block before PG12,
-- and a value can never be removed).
--
-- WHY THE BACKFILL IS A SEPARATE STATEMENT FROM THE COLUMN DEFAULT (owner's decision,
-- confirmed twice -- see this change's design.md "Rationale" for the recompute-mismatch
-- evidence the leader showed the owner before this was confirmed). The column's own
-- DEFAULT must be 'IN_PROGRESS': every FUTURE session mirrored by
-- SessionWriter.MirrorSessions starts with no battery-percentage data at all
-- (SessionMirror carries no percentage field, structurally -- RM29 design.md D6), and
-- IN_PROGRESS is the only honest state for a session nobody has looked at yet. But the
-- owner wants every PRE-EXISTING row -- data the sync could never have supplied, entirely
-- human-reconstructed -- recorded as DONE_CALCULATED, regardless of what a recompute
-- against its actual start_battery_pct/end_battery_pct would say. Because the intended
-- DEFAULT (IN_PROGRESS, for future rows) and the intended backfill value
-- (DONE_CALCULATED, for existing rows) differ, ADD COLUMN ... DEFAULT alone cannot do
-- both jobs -- unlike 20260829000002's status/energy_source columns, whose backfill
-- value WAS their intended default. This migration needs the two-step shape
-- 20260720000001 used (add, then a separate backfill statement), not 20260829000002's
-- one-step shape.
--
-- The bare UPDATE below (no WHERE clause) runs inside this migration's own transaction,
-- immediately after the ADD COLUMN, so it touches every row that existed when this
-- migration started and none that could not yet exist (there is no concurrent writer
-- inside one transaction). It is NOT metadata-only -- unlike the ADD COLUMN ... DEFAULT
-- above it, which is catalog-only on PostgreSQL 11+, this UPDATE performs a real
-- row-by-row rewrite. Acceptable per the Performance-Profile (writes may be slower;
-- this table holds one row per Supercharger session per account -- low volume -- and
-- this runs once, at deploy time, never on a read path).
ALTER TABLE charging.supercharger_sessions
    ADD COLUMN status TEXT NOT NULL DEFAULT 'IN_PROGRESS'
        CHECK (status IN ('IN_PROGRESS','DONE_CALCULATED','DONE'));

UPDATE charging.supercharger_sessions SET status = 'DONE_CALCULATED';

COMMENT ON COLUMN charging.supercharger_sessions.status IS
    'Lifecycle status of this session''s battery-percentage data, auto-computed by '
    'SessionVerifier.VerifySession on every call, never accepted from a caller: '
    'IN_PROGRESS when either start_battery_pct or end_battery_pct is NULL, '
    'DONE_CALCULATED when both are present and this call derived start_battery_pct via '
    'derivedStartBatteryPct rather than storing a caller-supplied value, DONE when both '
    'are present and start_battery_pct was supplied directly. Every row that existed '
    'before this migration was backfilled to DONE_CALCULATED unconditionally -- an '
    'owner decision about data provenance the stored percentages themselves cannot show, '
    'not a recompute of this rule against their actual values (see '
    'RM41-charging-add-session-status design.md "Rationale"). Not indexed: no read '
    'query in this tier filters, orders, or joins by it.';

-- +goose Down
-- Non-guarded, unlike 20260829000002's Down: dropping this column has no NOT NULL
-- restoration to protect against -- nothing else in the schema depends on its presence,
-- and there is no invalid intermediate state a rollback could produce. It IS lossy: the
-- DONE vs DONE_CALCULATED distinction (which start percentage was typed vs derived) is
-- not reconstructable from start_battery_pct/end_battery_pct alone -- the identical
-- limitation battery_pct_source already has for "was this pair typed or derived at
-- all" (MAG-36 design.md D1) -- but that loss needs no guard because it simply forgets
-- a fact rather than leaving the table in a state some other constraint would reject.
ALTER TABLE charging.supercharger_sessions DROP COLUMN IF EXISTS status;
```

### Rationale — why this design is right

- **Two-step add-then-backfill, not one-step `DEFAULT`** (see the migration's own
  comment above for the full reasoning). Rejected: a single
  `ADD COLUMN ... DEFAULT 'DONE_CALCULATED'` — that would also default every FUTURE
  freshly-mirrored session to `DONE_CALCULATED`, which is wrong (a session with no
  percentages recorded yet must read `IN_PROGRESS`).
- **The backfill is unconditional, not a recompute-and-compare `CASE`** — this is the
  owner's decision (BACKFILL, proposal.md "Resolved decisions"), confirmed twice after
  the leader ran the alternative and showed the owner the result: recomputing the
  general truth table above against the 10 real pre-existing rows' actual
  `start_battery_pct`/`end_battery_pct`/`energy_kwh` values matches `DONE_CALCULATED`
  for only 3 of them (the other 7 would recompute to `DONE`, since the leader's
  recompute cannot tell whether a stored start percentage was originally typed or
  derived — the same `battery_pct_source` limitation this whole column exists to
  work around, applied retroactively to data from before the column existed). 4 of
  the 10 rows carry a `NULL energy_kwh` and so could never have been derived by
  `derivedStartBatteryPct` even in principle. The owner's position, accepted as
  final: every one of these 10 rows' percentages was manually reconstructed by the
  owner from memory and external records, not read from the car — a provenance fact
  the stored columns cannot show either way — and the owner would rather every
  historic row read `DONE_CALCULATED` (a hint "double-check this one, it was a
  reconstruction") than have some of them read a plain `DONE` that looks
  indistinguishable from a session verified normally going forward. Rejected: a
  `CASE` backfill recomputing the general rule — it would silently mark 7 rows
  `DONE` on provenance grounds the owner explicitly does not want attributed to them.
- **`TEXT` + `CHECK`, not a Postgres `ENUM`** — identical rationale to
  `20260829000002_add_entry_status.sql`'s own choice: consistency with every other
  lifecycle/provenance column on these two tables, and enum value sets are painful to
  extend later (a real risk here: a `POLLED`-sourced fourth status is a plausible
  future addition once `battery_pct_source`'s own `"polled"` value gets a real writer).
- **No `derived` value added to `battery_pct_source`** — the CALC MARKER decision,
  confirmed by the owner today. Rejected: extending `battery_pct_source`'s CHECK with
  a `derived` value instead of adding a new column — that would let `status` be
  computed as a pure function of the three EXISTING columns with no new column at
  all. The owner rejected this explicitly: `battery_pct_source`'s two-value contract
  (`user_verified` / `polled`) is referenced by name in three other places
  (`internal/charging/AGENTS.md`, the `charge-session-log` spec, and the
  `supercharger_sessions_pct_source_required` CHECK's own semantics), and widening it
  to carry lifecycle information conflates two concerns the roadmap deliberately kept
  separate (D11: "the stored value is a code" for `status` specifically, not a
  repurposing of provenance).
- **Not computed by a database trigger** — `VerifySession` already has the
  `wasCalculated` fact in hand in Go, for free, as a side effect of computing
  `battery_pct_source`; a trigger would need to re-derive "was this call's start
  value the result of a derivation" from data a trigger cannot see (Go call
  arguments), so it could only ever implement the weaker "both non-NULL → DONE"
  rule and would need the `battery_pct_source`-widening rejected above to go further.
- **Not computed on read** — rejected explicitly by the roadmap (D10): a read-time
  computation cannot distinguish `DONE` from `DONE_CALCULATED` any more than a
  trigger can, for the identical reason, and would recompute the same three-state
  logic on every dashboard row on every page load instead of once, at write time —
  against a read-heavy Performance-Profile that says the opposite trade should be
  made wherever possible.

### Index plan

**No index is added.** Justification against the Performance-Profile
(`CLAUDE.md`: "read performance is mandatory... never at the cost of... module data
ownership" — but also "denormalizing, indexing aggressively... is acceptable" where a
real read pattern needs it):

- All three existing reads (`ListSessionsByVehicleBetween`,
  `ListSessionsByVehicleUpdatedSince`, `ListSessionsByVehicle`) use `SELECT *` against
  `idx_supercharger_sessions_vehicle_stop` (`account_id, tesla_id,
  charge_stop_date_time`). None filters, orders, or joins by `status` — it is
  returned as a passenger column on the same index scan those queries already
  perform, the exact way `battery_pct_source`/`inferred_capacity_kwh_calc` already
  ride along for free.
- Tier 5 (`gateway`, out of scope here) renders `status` per already-fetched row and
  computes its "fill in sessions still in progress" recommendation by counting over
  the SAME already-fetched slice in Go, per the roadmap's own tier 5 scope
  description — it does not need, and this design does not add, a
  `WHERE status = 'IN_PROGRESS'` query.
- This mirrors `manual_charge_entries.status`'s own, identical precedent exactly
  (`20260829000002_add_entry_status.sql`'s own design.md D9): "No index was added on
  any of the three new columns — none is predicated on by any read query in this
  tier; an index on a column nothing filters/orders/joins by is pure write and
  storage cost for no read benefit."
- **Revisit trigger** (stated explicitly, not left silent): if a future change needs
  a server-side count or list of in-progress sessions (e.g. an account-wide "N
  sessions need attention" badge outside the already-fetched page), add a
  **partial**, `account_id`-leading index —
  `CREATE INDEX ... ON charging.supercharger_sessions (account_id, tesla_id)
  WHERE status = 'IN_PROGRESS'` — at that time, mirroring `manual_charge_entries`'s
  own documented revisit trigger. Not now: no such query exists in this tier or
  tier 5's stated scope.

### What happens to existing rows / data-loss statement

**No data loss on Up.** `ADD COLUMN ... DEFAULT` is additive; the backfill `UPDATE`
overwrites only the new column, on rows this migration itself just created the column
for. No existing column is read, altered, or dropped.

**Data loss on Down is real but narrow.** `DROP COLUMN status` permanently discards:
the `IN_PROGRESS`/`DONE_CALCULATED`/`DONE` distinction for every row (recoverable in
part — the `IN_PROGRESS` vs "both present" split IS reconstructable by re-reading the
two percentage columns — but the `DONE` vs `DONE_CALCULATED` split, i.e. which stored
start percentage was typed vs derived, is NOT reconstructable, exactly like
`battery_pct_source` already cannot reconstruct "was this pair typed or derived at
all"). No `NOT NULL` restoration hazard exists on Down (unlike
`20260829000002_add_entry_status.sql`'s `energy_added_kwh` case) because nothing else
in the schema references this column, so the Down needs no guard.

## Go-side call shape

`SessionVerifier.VerifySession` (`internal/charging/session_verifier.go`) already
computes, before this change: `startToStore` (the caller's `startBatteryPct`, or a
derived value, or `nil`) and whether `needsDerivedStartBatteryPct(startBatteryPct,
endBatteryPct)` was `true` for this call. This design adds exactly one new local fact,
`calculated`, and one new pure function:

```go
// sessionStatusFor computes the lifecycle SessionStatus for a supercharger_sessions
// row from the FINAL values VerifySession is about to store, plus whether THIS call
// derived startPct rather than storing a caller-supplied value
// (RM41-charging-add-session-status, MAG-45, design.md "Go-side call shape"). It is
// the single place this rule is written, mirroring needsDerivedStartBatteryPct's own
// "one small named function" shape.
//
// startPct/endPct here are the FINAL values about to be written -- startPct is
// VerifySession's own startToStore (the caller's value, the derived one, or nil),
// never the caller's raw startBatteryPct parameter. derived is true only when THIS
// call both attempted and succeeded at deriving startPct
// (needsDerivedStartBatteryPct(...) was true AND the derivation produced a non-nil
// result) -- a failed derivation (energy_kwh SQL NULL, or an out-of-range result)
// leaves startPct nil and therefore falls through to SessionStatusInProgress via the
// first rule below, never SessionStatusDoneCalculated.
//
// Rule, in priority order (design.md "State truth table"):
//  1. Either percentage absent (nil) -> SessionStatusInProgress. Covers "nothing
//     recorded", "only a start percentage recorded" (deliberately IN_PROGRESS, not a
//     fourth state), and "only an end percentage recorded and no derivation was
//     possible or successful".
//  2. Both present AND derived -> SessionStatusDoneCalculated.
//  3. Both present, not derived -> SessionStatusDone.
func sessionStatusFor(startPct, endPct *int, derived bool) SessionStatus {
	if startPct == nil || endPct == nil {
		return SessionStatusInProgress
	}
	if derived {
		return SessionStatusDoneCalculated
	}
	return SessionStatusDone
}
```

`VerifySession`'s body changes from:

```go
	startToStore := startBatteryPct
	q := v.q
	var tx pgx.Tx

	if needsDerivedStartBatteryPct(startBatteryPct, endBatteryPct) {
		// ... (tx/lock/packCapacityKWh, unchanged) ...
		startToStore = derivedStartBatteryPct(capacityKWh, pgFloat8ToFloat64Ptr(row.EnergyKwh), endBatteryPct)
	}

	var source *string
	if startToStore != nil || endBatteryPct != nil {
		s := batteryPctSourceUserVerified
		source = &s
	}

	row, err := q.VerifySuperchargerSession(ctx, chargingdb.VerifySuperchargerSessionParams{
		ID:               id,
		AccountID:        accountID,
		StartBatteryPct:  intPtrToPgInt2(startToStore),
		EndBatteryPct:    intPtrToPgInt2(endBatteryPct),
		BatteryPctSource: stringPtrToPgText(source),
	})
```

to:

```go
	startToStore := startBatteryPct
	calculated := false
	q := v.q
	var tx pgx.Tx

	if needsDerivedStartBatteryPct(startBatteryPct, endBatteryPct) {
		// ... (tx/lock/packCapacityKWh, unchanged) ...
		startToStore = derivedStartBatteryPct(capacityKWh, pgFloat8ToFloat64Ptr(row.EnergyKwh), endBatteryPct)
		calculated = startToStore != nil
	}

	status := sessionStatusFor(startToStore, endBatteryPct, calculated)

	var source *string
	if startToStore != nil || endBatteryPct != nil {
		s := batteryPctSourceUserVerified
		source = &s
	}

	row, err := q.VerifySuperchargerSession(ctx, chargingdb.VerifySuperchargerSessionParams{
		ID:               id,
		AccountID:        accountID,
		StartBatteryPct:  intPtrToPgInt2(startToStore),
		EndBatteryPct:    intPtrToPgInt2(endBatteryPct),
		BatteryPctSource: stringPtrToPgText(source),
		Status:           string(status),
	})
```

No new database round-trip: `status` is computed from values already in hand (or
already read, inside the existing transaction, when a derivation is attempted) before
the single existing `UPDATE` fires. `sessionStatusFor` is unexported, like
`needsDerivedStartBatteryPct`/`derivedStartBatteryPct`/`packCapacityKWh` — an
implementation detail of `VerifySession`'s caller, not part of this module's public
surface. Per this tier's "no new unit-test files" hard rule, `sessionStatusFor` has no
dedicated offline test file; its four branches are exercised entirely through the
DB-gated `VerifySession` integration tests below (Test Contract Group S), which is
also the ONLY way to reach the `calculated=true` branch honestly (it depends on the
real derivation path, not a value a pure-function test could fabricate without also
faking `packCapacityKWh`/`derivedStartBatteryPct`).

## Code changes — exact edits

### `internal/charging/charging.go`

1. **New type + constants**, placed immediately before the `Session` struct (after
   `SessionWriter`'s constructor, matching this file's existing type-then-constants
   ordering for `EnergySource`/`Status` in the manual-entry section above):

   ```go
   // SessionStatus is the lifecycle status of one supercharger_sessions row's
   // battery-percentage data (TEXT + CHECK in the DB, RM41-charging-add-session-status,
   // MAG-45). ALWAYS COMPUTED by SessionVerifier.VerifySession on every call -- no port
   // accepts a Session as input for this field, so there is no way for a caller to set
   // it directly.
   type SessionStatus string

   const (
       // SessionStatusInProgress: at least one of start_battery_pct/end_battery_pct is
       // NULL. This is the status of a session nobody has recorded anything for, AND of
       // a session with only a start percentage recorded and no end percentage --
       // deliberately not a fourth state (design.md "State truth table"): this is what
       // keeps "sessions still in progress" a meaningful worklist for the gateway's own
       // follow-up recommendation (RM41 tier 5) even when a session is half-recorded.
       SessionStatusInProgress SessionStatus = "IN_PROGRESS"
       // SessionStatusDoneCalculated: both percentages are present, AND the
       // VerifySession call that produced this row's CURRENT start_battery_pct derived
       // it via derivedStartBatteryPct (capacity.go) rather than storing a
       // caller-supplied value. This is the one place in the schema that keeps a typed
       // percentage apart from a derived one -- battery_pct_source itself cannot
       // (MAG-36 design.md D1).
       SessionStatusDoneCalculated SessionStatus = "DONE_CALCULATED"
       // SessionStatusDone: both percentages are present, and the CURRENT
       // start_battery_pct was supplied directly by VerifySession's caller.
       SessionStatusDone SessionStatus = "DONE"
   )
   ```

2. **`Session` struct** gains one field, placed after `BatteryPctSource` and before
   `InferredCapacityKWhCalc` (grouped with the verification channel it is derived
   from, ahead of the engine-generated column):

   ```go
       // Status is this session's lifecycle status -- ALWAYS COMPUTED by
       // SessionVerifier.VerifySession, never settable through any port
       // (RM41-charging-add-session-status, MAG-45). See SessionStatus's own doc
       // comment for the three values and the rule that produces each.
       Status SessionStatus
   ```

   The struct's own doc comment ("Eighteen fields, one per supercharger_sessions
   column...") becomes "Nineteen fields..." and its "the three charging-owned
   battery-percentage verification columns" clause becomes "the three charging-owned
   battery-percentage verification columns, plus (since RM41 tier 4) a fourth
   charging-owned column, `Status`, computed from them".

3. **`SessionVerifier` interface doc comment** — the sentence "VerifySession updates
   exactly three columns on one account-scoped supercharger_sessions row —
   start_battery_pct, end_battery_pct, battery_pct_source — plus updated_at. No other
   column is reachable through this method: the underlying query's SET clause names
   only these three plus updated_at" becomes:

   > VerifySession updates exactly four columns on one account-scoped
   > supercharger_sessions row — start_battery_pct, end_battery_pct,
   > battery_pct_source, status — plus updated_at (RM41-charging-add-session-status,
   > MAG-45, added the fourth). No other column is reachable through this method: the
   > underlying query's SET clause names only these four plus updated_at (RM31 design.md
   > D1, extended by RM41 tier 4). status is ALWAYS COMPUTED by this method from the
   > same startToStore/endBatteryPct/derivation-outcome values used to compute
   > battery_pct_source — never accepted as a parameter; VerifySession's own signature
   > is unchanged by this addition.

   The trailing sentence about the two dropped `_est` columns is unchanged (still
   accurate, unrelated to this edit).

### `internal/charging/session_verifier.go`

Add the `sessionStatusFor` function (full body under "Go-side call shape" above) and
apply the `VerifySession` body edit shown there verbatim. Update `VerifySession`'s own
doc comment's numbered implementation-shape list to add a step between the existing
steps 3 and 4: "3.5. Compute status via sessionStatusFor from startToStore, the
caller's endBatteryPct, and whether step 2 actually derived a value
(RM41-charging-add-session-status design.md 'Go-side call shape')."

### `internal/charging/session_reader.go`

`rowToSession` gains one line, placed after `BatteryPctSource` and before
`CreatedAt`:

```go
		Status: SessionStatus(r.Status),
```

The mapping-rules doc comment above `rowToSession` gains one bullet: "Status:
`string` (NOT NULL `TEXT`) → `SessionStatus` via a direct type conversion — no pgtype
involved, the same shape `Vin`/`SiteLocationName` already use for their own NOT NULL
columns."

### `internal/charging/db/query.sql`

**`VerifySuperchargerSession`** — `SET` clause gains one line:

```sql
UPDATE charging.supercharger_sessions
SET
    start_battery_pct  = @start_battery_pct,
    end_battery_pct    = @end_battery_pct,
    battery_pct_source = @battery_pct_source,
    status             = @status,
    updated_at         = now()
WHERE id = @id
  AND account_id = @account_id
RETURNING *;
```

Its doc comment's opening sentence ("Update the human-owned verification channel on
one account-scoped charge session: start_battery_pct, end_battery_pct, and
battery_pct_source — plus updated_at. No other column is in this SET clause...")
becomes "...start_battery_pct, end_battery_pct, battery_pct_source, and status — plus
updated_at. No other column is in this SET clause..." and gains one sentence:
"`@status` is COMPUTED IN GO by `sessionStatusFor`
(RM41-charging-add-session-status), never accepted from an external caller — the
identical shape `@battery_pct_source` already uses."

**`MirrorSuperchargerSession`** — no SQL statement changes (its INSERT/ON CONFLICT
lists already exclude every verification column, and `status` is naturally excluded
the same way for the same structural reason: `SessionMirror` has no field for it).
Its existing "LOAD-BEARING" guarding comment gains one paragraph:

> The session's lifecycle status (added by RM41-charging-add-session-status, MAG-45)
> is ALSO absent from both the INSERT column list and the ON CONFLICT DO UPDATE SET
> clause — a freshly-mirrored session has no battery-percentage data yet, so it must
> start IN_PROGRESS, which is exactly what the column's own DEFAULT provides with no
> explicit value here. Only SessionVerifier.VerifySession ever writes this column
> (see VerifySuperchargerSession's own doc comment).

**`ListSessionsByVehicleBetween`, `ListSessionsByVehicleUpdatedSince`,
`ListSessionsByVehicle`, `LockSessionForVerification`** — no change. The first three
already `SELECT *`; the fourth reads only `vin, energy_kwh` and has no reason to
change.

### Regenerated files

Run `make sqlc` (or `sqlc generate`) after the migration and `query.sql` edits land.
`internal/charging/db/models.go` gains `Status string` on the generated
`SuperchargerSession` struct (appended at the end, matching `ALTER TABLE ADD COLUMN`'s
physical column order — this does not affect any positional access anywhere in this
module, which only ever names fields). `internal/charging/db/query.sql.go` gains
`Status string` on `VerifySuperchargerSessionParams`. Never hand-edit either file.

## Test Contract

Per `ai/go-conventions.md`'s "author expected values up front" rule and this
dispatch's own TEST CONTRACT mandate. All new cases extend the EXISTING
`internal/charging/db_session_verifier_integration_test.go` (package `charging_test`)
— no new file, no new offline/unit test, per this tier's hard rule. New fixtures use
session IDs `950010`-`950017` — continuing within THIS FILE's own already-claimed
`950001`-`950099` block (its header comment's range, only `950001`-`950009` used so
far by the RM31 tier 1 tests), not a new registry entry. `960001`-`960099` was
considered and rejected: it is already claimed by three OTHER charging test files
(`db_inferred_capacity_sessions_integration_test.go`,
`db_session_reader_updated_since_integration_test.go`,
`db_session_reader_by_vehicle_integration_test.go`) — reusing it here would not cause
a real database collision (each test seeds its own fresh `uuid.New()` account, and
the table's uniqueness is `(account_id, session_id)`), but would violate the
module's own documentation convention of one numeric block per file. Full registry
for reference: RM29 tier 6: `920001`-`920099`; RM30 tier 1: `940001`-`940099`; RM31
tier 1 (this same file): `950001`-`950099`; three other files: `960001`-`960099`;
the real backfilled session: `734860294`.

### New helper

```go
// seedVerifierSessionEnergy seeds one baseline supercharger_sessions row via
// SessionWriter.MirrorSessions with a caller-chosen EnergyKWh (nil included) and all
// battery-percentage/status columns at their defaults -- generalizes
// seedVerifierSession for Group S, which needs an energy_kwh SQL NULL fixture (S3)
// and an unusually large one (S8) seedVerifierSession's hardcoded 30.5 cannot
// produce.
func seedVerifierSessionEnergy(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, sessionID int64, energyKWh *float64) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)
	m := charging.SessionMirror{
		AccountID:           accountID,
		VIN:                 "VVERIFY",
		TeslaID:             ptrInt64(sessionID),
		SessionID:           sessionID,
		ChargeStartDateTime: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC),
		SiteLocationName:    "Verifier Test Site",
		EnergyKWh:           energyKWh,
		TotalCost:           ptrFloat64(9.99),
		Currency:            ptrString("USD"),
		IsPaid:              ptrBool(true),
	}
	if err := w.MirrorSessions(ctx, accountID, []charging.SessionMirror{m}); err != nil {
		t.Fatalf("seedVerifierSessionEnergy: MirrorSessions: %v", err)
	}
	return fetchSuperchargerSessionID(t, pool, accountID, sessionID)
}
```

### Group S — `sessionStatusFor` via `VerifySession` (state truth table, S1-S8)

**S1 — fresh mirror, never verified → `IN_PROGRESS` (the column `DEFAULT`, exercised
through the real INSERT path).**

```go
func TestVerifySession_S1_FreshMirrorIsInProgress(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargingSuperchargerSessions(t, pool, acctA)

	id := seedVerifierSession(t, pool, acctA, 950010)

	var status string
	if err := pool.QueryRow(context.Background(),
		"SELECT status FROM charging.supercharger_sessions WHERE id = $1", id,
	).Scan(&status); err != nil {
		t.Fatalf("reading status: %v", err)
	}
	if status != string(charging.SessionStatusInProgress) {
		t.Errorf("status = %q, want %q", status, charging.SessionStatusInProgress)
	}
}
```

**S2 — start only, end nil → `IN_PROGRESS` (truth table row 3).**

```go
func TestVerifySession_S2_StartOnlyIsInProgress(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargingSuperchargerSessions(t, pool, acctA)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	id := seedVerifierSession(t, pool, acctA, 950011)

	got, err := v.VerifySession(ctx, acctA, id, ptrIntV(30), nil)
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if got.Status != charging.SessionStatusInProgress {
		t.Errorf("Status = %q, want %q", got.Status, charging.SessionStatusInProgress)
	}
	if got.StartBatteryPct == nil || *got.StartBatteryPct != 30 {
		t.Errorf("StartBatteryPct = %v, want 30", got.StartBatteryPct)
	}
	if got.EndBatteryPct != nil {
		t.Errorf("EndBatteryPct = %v, want nil", got.EndBatteryPct)
	}
}
```

**S3 — end only, `energy_kwh` SQL NULL → derivation impossible, `IN_PROGRESS` (truth
table row 2, no-energy sub-case).**

```go
func TestVerifySession_S3_EndOnlyNoEnergyStaysInProgress(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargingSuperchargerSessions(t, pool, acctA)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	id := seedVerifierSessionEnergy(t, pool, acctA, 950012, nil)

	got, err := v.VerifySession(ctx, acctA, id, nil, ptrIntV(80))
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if got.Status != charging.SessionStatusInProgress {
		t.Errorf("Status = %q, want %q", got.Status, charging.SessionStatusInProgress)
	}
	if got.StartBatteryPct != nil {
		t.Errorf("StartBatteryPct = %v, want nil (no energy to derive from)", got.StartBatteryPct)
	}
	if got.EndBatteryPct == nil || *got.EndBatteryPct != 80 {
		t.Errorf("EndBatteryPct = %v, want 80", got.EndBatteryPct)
	}
}
```

**S4 — end only, `energy_kwh` present, derivation succeeds → `DONE_CALCULATED`
(truth table row 4). Arithmetic: `90 - 30.5/62*100 = 40.8` → rounds to `41`, the
identical fixture shape `TestVerifySession_PartialEndOnlyStillSetsSource` already
pins.**

```go
func TestVerifySession_S4_EndOnlyDerivedIsDoneCalculated(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargingSuperchargerSessions(t, pool, acctA)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	id := seedVerifierSession(t, pool, acctA, 950013)

	got, err := v.VerifySession(ctx, acctA, id, nil, ptrIntV(90))
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if got.Status != charging.SessionStatusDoneCalculated {
		t.Errorf("Status = %q, want %q", got.Status, charging.SessionStatusDoneCalculated)
	}
	if got.StartBatteryPct == nil || *got.StartBatteryPct != 41 {
		t.Errorf("StartBatteryPct = %v, want 41 (derived)", got.StartBatteryPct)
	}
}
```

**S5 — both supplied directly → `DONE` (truth table row 5).**

```go
func TestVerifySession_S5_BothSuppliedIsDone(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargingSuperchargerSessions(t, pool, acctA)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	id := seedVerifierSession(t, pool, acctA, 950014)

	got, err := v.VerifySession(ctx, acctA, id, ptrIntV(20), ptrIntV(80))
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if got.Status != charging.SessionStatusDone {
		t.Errorf("Status = %q, want %q", got.Status, charging.SessionStatusDone)
	}
}
```

**S6 — re-verification flips a status: `DONE_CALCULATED` → `DONE` when a later call
supplies a caller-typed start (this is the dispatch's required "re-verification that
flips a status" case, and it is also the concrete proof that `status` is recomputed
on every write, per roadmap D10, not fixed at first-write).**

```go
func TestVerifySession_S6_ReverificationFlipsCalculatedToDone(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargingSuperchargerSessions(t, pool, acctA)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	id := seedVerifierSession(t, pool, acctA, 950015)

	first, err := v.VerifySession(ctx, acctA, id, nil, ptrIntV(90))
	if err != nil {
		t.Fatalf("VerifySession (first): %v", err)
	}
	if first.Status != charging.SessionStatusDoneCalculated {
		t.Fatalf("first Status = %q, want %q", first.Status, charging.SessionStatusDoneCalculated)
	}

	second, err := v.VerifySession(ctx, acctA, id, ptrIntV(45), ptrIntV(90))
	if err != nil {
		t.Fatalf("VerifySession (second): %v", err)
	}
	if second.Status != charging.SessionStatusDone {
		t.Errorf("second Status = %q, want %q (a caller-supplied start must flip DONE_CALCULATED to DONE)", second.Status, charging.SessionStatusDone)
	}
	if second.StartBatteryPct == nil || *second.StartBatteryPct != 45 {
		t.Errorf("second StartBatteryPct = %v, want 45 (the caller's own value, never the earlier derived 41)", second.StartBatteryPct)
	}
}
```

**S7 — clearing both percentages resets a `DONE` session to `IN_PROGRESS`.**

```go
func TestVerifySession_S7_ClearingBothResetsToInProgress(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargingSuperchargerSessions(t, pool, acctA)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	id := seedVerifierSession(t, pool, acctA, 950016)

	if _, err := v.VerifySession(ctx, acctA, id, ptrIntV(20), ptrIntV(80)); err != nil {
		t.Fatalf("VerifySession (set): %v", err)
	}

	got, err := v.VerifySession(ctx, acctA, id, nil, nil)
	if err != nil {
		t.Fatalf("VerifySession (clear): %v", err)
	}
	if got.Status != charging.SessionStatusInProgress {
		t.Errorf("Status = %q, want %q", got.Status, charging.SessionStatusInProgress)
	}
	if got.StartBatteryPct != nil || got.EndBatteryPct != nil {
		t.Errorf("percentages not cleared: start=%v end=%v", got.StartBatteryPct, got.EndBatteryPct)
	}
}
```

**S8 — end only, `energy_kwh` present, derivation out of range → `IN_PROGRESS` (truth
table row 2, out-of-range sub-case — a different path to the same status as S3).
Arithmetic: `10 - 100/62*100 = -151.29` → rounds to `-151`, outside `[0,100]`.**

```go
func TestVerifySession_S8_DerivedOutOfRangeStaysInProgress(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargingSuperchargerSessions(t, pool, acctA)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	id := seedVerifierSessionEnergy(t, pool, acctA, 950017, ptrFloat64(100.0))

	got, err := v.VerifySession(ctx, acctA, id, nil, ptrIntV(10))
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if got.Status != charging.SessionStatusInProgress {
		t.Errorf("Status = %q, want %q", got.Status, charging.SessionStatusInProgress)
	}
	if got.StartBatteryPct != nil {
		t.Errorf("StartBatteryPct = %v, want nil (derivation out of range)", got.StartBatteryPct)
	}
}
```

### Repair: `TestVerifySession_OnlyTargetColumnsChange` (T8)

This test currently discards `VerifySession`'s return value
(`if _, err := v.VerifySession(ctx, acctA, v8ID, ptrIntV(15), ptrIntV(95)); err != nil`).
`status` is now a fourth column the query's SET clause legitimately changes (from the
`IN_PROGRESS` default to `DONE`, since both percentages are supplied directly here),
so it must move from "unasserted" to "asserted as a column that DOES change, on
purpose" — it must NOT be added to `assertVerifierColumnsUnchanged`, which exists
specifically for columns that must NOT change. Edit:

```go
	got, err := v.VerifySession(ctx, acctA, v8ID, ptrIntV(15), ptrIntV(95))
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if got.Status != charging.SessionStatusDone {
		t.Errorf("Status = %q, want %q (both percentages supplied directly)", got.Status, charging.SessionStatusDone)
	}
```

Every other assertion in this test (the bit-identical-column checks) is UNCHANGED.

### Migration backfill — NOT integration-tested (identical precedent to MAG-25/RM33)

Per `internal/charging/AGENTS.md`'s own established pattern (design.md D10 of both
`charging-add-inferred-capacity` and `RM33-charging-add-entry-status`): this
package's test database is provisioned fresh with every migration applied before any
row exists (`testdb.ProvisionDirs`, `internal/charging/AGENTS.md` §Testing Notes), so
the backfill `UPDATE` always runs against zero rows in that environment — there is
nothing to backfill in a test, only the column `DEFAULT` mechanism is exercisable
there (covered by S1 above). The real backfill outcome is verified by the owner
against the live database after `make migrate-up`:

```sql
SELECT status, count(*) FROM charging.supercharger_sessions GROUP BY status;
```

Expected: every row that existed before this migration shows `DONE_CALCULATED`; any
session mirrored for the first time AFTER this migration shows `IN_PROGRESS`. See
tasks.md's final wave.

## Docs

### `internal/charging/AGENTS.md` (module doc — `CLAUDE.md` docs-track-change rule)

1. **§Public Interface** — the `SessionStatus` type block and the `Session` struct's
   new `Status` field are added to the reproduced code, exactly as in charging.go
   (§"Code changes" above). The `Session` struct's own surrounding prose ("the full
   domain representation...") gains one clause noting the new field.
2. **`SessionVerifier`'s reproduced doc comment** is updated to the four-column
   version (§"Code changes" above).
3. **New subsection**, placed immediately after "### Derived start battery
   percentage (MAG-36, charging-add-derived-start-battery-pct)", titled "### Session
   lifecycle status (RM41 tier 4, MAG-45)": reproduces the state truth table and its
   one-sentence rule (design.md "State truth table" above), states that `status` is
   computed in `VerifySession` alongside `battery_pct_source` and shares its "never
   accepted from a caller" property, and records the BACKFILL decision (every
   pre-existing row reads `DONE_CALCULATED` unconditionally, an owner decision about
   provenance the columns themselves cannot show — not a recomputed value).
4. **§Data Ownership → `supercharger_sessions` → column-by-column list** — the
   `start_battery_pct, end_battery_pct, battery_pct_source` bullet gains a sentence:
   "Since RM41 tier 4 (MAG-45), a fourth charging-owned column, `status`, is computed
   from these two on every `VerifySession` call — see §Public Interface above and the
   new 'Session lifecycle status' subsection for the full rule; `status` is not
   itself mirrored, refreshed, or engine-generated, it is Go-computed."
5. **§Testing Notes** — the existing paragraph about
   `db_session_verifier_integration_test.go` gains one sentence naming this tier's
   addition: "Since RM41 tier 4 (MAG-45), the same file also covers the `status`
   column's full state truth table (Test Contract Group S1-S8,
   `RM41-charging-add-session-status` design.md) and the backfill's identical
   'DEFAULT mechanism only, not the real backfill outcome' exemption already
   established for MAG-25/RM33's own backfilled columns."

### Root `README.md` — database-tables table, `charging.supercharger_sessions` row

Current text (line 287, the `supercharger_sessions` row's "What it stores" cell)
reads, among other things: "...plus the five **human-verified** battery-percentage
columns, which the sync structurally cannot touch...". This is ALREADY stale — tier 2
(`RM41-charging-drop-estimate-columns`) dropped the two `_est` columns, leaving three,
not five, and that correction was missed at the time (this tier did not cause the
staleness, but touches this exact sentence to add its own fact, so both are fixed in
one edit per `CLAUDE.md`'s docs-track-change rule). New text:

> ...plus the three **human-verified** battery-percentage columns, which the sync
> structurally cannot touch (`charging.SessionMirror` has no field for them), and
> (since RM41 tier 4, MAG-45) a fourth charging-owned **`status`** column — auto-computed
> from those three's presence on every human correction, describing whether the
> session's battery data is `IN_PROGRESS`, `DONE_CALCULATED` (end percentage recorded,
> start percentage derived), or `DONE`...

(the rest of the cell — the source-of-record sentence, the naming-collision history,
the `inferred_capacity_kwh_calc` sentence — is unchanged).

### `kkpa/context/use-case/charging/verify-session-battery.md`

The "Database" table's row 1 currently reads:

> WRITE | `supercharger_sessions` | `VerifySuperchargerSession` — SETs
> `start_battery_pct`, `end_battery_pct`, `battery_pct_source`, `updated_at` and
> nothing else; Postgres recomputes `inferred_capacity_kwh_calc` in the same
> statement

becomes:

> WRITE | `supercharger_sessions` | `VerifySuperchargerSession` — SETs
> `start_battery_pct`, `end_battery_pct`, `battery_pct_source`, `status`,
> `updated_at` and nothing else (the fourth SET target, `status`, added by
> `RM41-charging-add-session-status`); Postgres recomputes
> `inferred_capacity_kwh_calc` in the same statement

The "Conventions & gotchas" bullet beginning "**`VerifySuperchargerSession` and
`MirrorSuperchargerSession` are deliberate mirror images.**" gains one clause noting
that `status` joined the human-owned side of that mirror image in RM41 tier 4, still
excluded from `MirrorSuperchargerSession`'s SET clause the same way the three
percentage columns are.

## Spec delta — `openspec/specs/charge-session-log/spec.md`

One NEW Requirement, two MODIFIED Requirements. Full requirement/scenario text is in
this change's `specs/charge-session-log/spec.md`; summarized here:

- **NEW: "A Charge Session Carries A Lifecycle Status"** — every charge session
  record carries a status computed from whether its two verified battery percentages
  are both present, and (when both are present) whether the start percentage was
  supplied directly or derived. States the same state truth table as an
  implementation-neutral SHALL, plus the start-set/end-NULL corner explicitly.
- **MODIFIED: "Charge Sessions Are Retrievable For A Vehicle Within A Time Window"**
  — the "every fact the capability holds" list gains "its lifecycle status"; the
  "retrieved record carries its full session detail" scenario gains a status
  assertion.
- **MODIFIED: "A Charge Session's Battery Percentages Are Correctable By A Human"** —
  a new paragraph states that a correction's outcome includes updating the record's
  lifecycle status as a DIRECT EFFECT of the percentages it changes (not added to the
  "other facts a correction never touches" list, which stays about facts the
  correction has no business touching at all — status is not one of those, it is a
  computed consequence of exactly what the correction does touch). Three new
  scenarios restate S4/S6/S7 above at the capability-behavior level (derivation
  producing a "calculated" status; a later correction with a directly supplied start
  changing that status; clearing both percentages resetting the status).

## Risks

- **A future writer of `start_battery_pct`/`end_battery_pct` that is not
  `SessionVerifier.VerifySession` would silently skip the status recompute.** Today
  there is exactly one such writer (`VerifySession`) and it is a compile-time fact,
  not a convention, that `SessionMirror`/`MirrorSuperchargerSession` cannot touch
  either percentage column (RM29 design.md D6) — so this risk has no live surface
  today. If a future change adds a second way to write these columns, it MUST also
  call (or duplicate) `sessionStatusFor`; flagged here so that change's design.md
  finds this warning by reading this module's history, the same way this change
  found MAG-36's own D1 limitation.
- **The backfill's `DONE_CALCULATED` label on rows that were never actually
  derived is a deliberate, accepted inaccuracy**, not a defect — restated from
  "Rationale" so a future reader auditing "why does this row say
  DONE_CALCULATED when its start percentage matches what the user would have typed"
  finds the answer here rather than filing a bug.
