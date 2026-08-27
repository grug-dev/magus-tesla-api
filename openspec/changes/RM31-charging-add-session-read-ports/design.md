# Design — RM31-charging-add-session-read-ports

> Numbering note: this document's decisions are **D1, D2, …**, scoped to this change only —
> distinct from the roadmap's own **Decision 1, Decision 2, …** in
> `openspec/roadmaps/RM31-supercharger-session-verification.md` (cited as **roadmap Decision
> N**), from RM29 tier 6's archived decisions (cited as **RM29 D1**, …), and from RM30 tier
> 1's archived decisions (cited as **RM30 D1**, …). **D1 and D2 transcribe binding
> instructions from the leader's dispatch** (which itself transcribes the roadmap's tier-2
> proposal prompt and roadmap Decisions 8/9); each says so in its heading. **D3–D7 are
> decisions this artifacts pass had to make** to turn those into a buildable change.

## Context

Five facts about the existing schema and code shape everything below.

1. **`charge_sessions` and its one index already exist; only two read shapes are missing.**
   RM29 tier 6 shipped the table and `idx_charge_sessions_vehicle_stop (account_id,
   tesla_id, charge_stop_date_time)`. RM30 tier 1 shipped the first reader,
   `ListSessionsByVehicleBetween`, over that same index. This tier adds two more methods to
   the same `SessionReader` interface and the same `sessionReader` struct — no new table, no
   new index, no new file for the struct.

2. **The two sibling ports to mirror already exist, in two different modules, and they
   disagree on how to order an "updated since" read.** `Reader.ListEntriesByVehicleUpdatedSince`
   (this module, `charging.go`) orders by its **date** column (`charged_on DESC`) — the
   index's own order — treating `updated_at` purely as a residual filter. `telemetry.SuperchargerReader.SuperchargerSessionsByVehicleUpdatedSince`
   orders by **`updated_at`** itself, ascending. The dispatch is explicit that method 1 in
   this change mirrors `ListEntriesByVehicleUpdatedSince` specifically — its index reasoning,
   not `telemetry`'s ordering choice — so D1 below follows the sibling in this module, not
   the one in `telemetry`.

3. **`ListSessionsByVehicleBetween`'s own doc comment (RM30 D3) already warns future readers
   that `Session` reads on this table do not share a sort-direction convention** — sort
   direction is a property of what each query needs from the shared index, not a
   port-family rule. This design's D3 is the concrete instance that warning exists for.

4. **`idx_charge_sessions_vehicle_stop` was built ASC, not DESC** (RM29 tier 6's migration,
   confirmed unchanged in `20260823000001_add_charge_sessions.sql`), specifically so
   `ListSessionsByVehicleBetween`'s ascending bounded-window read gets a forward scan with
   no sort step (RM30 D1). Any query on this table that wants **descending** order — as
   method 2 below does, for its "most recent N" semantics — must therefore rely on
   Postgres's ability to walk a B-tree index **backward**, not on the index's own build
   order. This is standard, well-documented Postgres behavior (a B-tree is doubly
   traversable at identical cost in either direction), not something this change is
   inventing — see D3's index proof.

5. **`analytics` consumes both new shapes today, from `telemetry`, and their consumer call
   sites tolerate the exact contracts these methods provide.**
   `internal/analytics/recalculate.go`'s `Reconcile` calls
   `r.supercharger.SuperchargerSessionsByVehicleUpdatedSince(ctx, accountID, teslaID,
   scsCursor.Add(-recalcOverlap))` and only reads each returned session's
   `ChargeStopDateTime`/`UpdatedAt` to widen a date range and advance a watermark — it does
   not depend on any particular sort order of the slice. `internal/analytics/reader.go`'s
   `RecentEfficiency` calls `r.supercharger.SuperchargerSessionsByVehicle(ctx, accountID,
   teslaID, chargingSourceLimit)` and only sums `EnergyKWh` across sessions at or after a
   cutoff via `sumSuperchargerKWh` — it iterates every returned row exactly once and also
   does not depend on order. Both facts are recorded here so a future reader does not
   over-index on "the caller needs DESC/ASC" as the reason for D1/D3 below — the caller
   tolerates either; the index does not.

## Goals / Non-Goals

**Goals**

- `internal/charging`'s `SessionReader` gains the two read shapes `analytics` needs to move
  its Supercharger source off `telemetry` (roadmap Decision 9): an `updated_at`-cursor read
  (**D1**) and a limit-bounded "most recent N" read (**D3**).
- Both reads are served entirely by the existing `idx_charge_sessions_vehicle_stop` — no
  migration, no new index (**D2**).
- Neither method ever returns a row whose `TeslaID` is `nil` (**D4**, restating RM29 D6 /
  RM30 D6 for these two new call shapes).
- Both methods return a non-nil empty slice on no match, matching every existing method on
  this interface (**D1**, **D3**).

**Non-Goals**

- Any database migration, index, column, or constraint. If this design's index proof had
  concluded one was needed, that conclusion would be reported as blocked rather than
  implemented (dispatch instruction, **D2**).
- Wiring `analytics` onto these two methods, or any change to
  `internal/analytics/reader.go` / `recalculate.go`. Tier 3 is the consumer; this tier has
  no caller of its own two new methods, exactly as RM30 tier 1 shipped
  `ListSessionsByVehicleBetween` with none.
- Any change to `SessionWriter`, `SessionMirror`, `SessionVerifier`, `VerifyChargeSession`,
  or `ListSessionsByVehicleBetween`. Nothing about the write path or the existing bounded
  read changes.
- A new file for the struct implementation. Both methods land on the existing
  `sessionReader` type in the existing `session_reader.go` (**D6**).
- Any offline/pure-function unit test. Neither method has separable pure logic beyond the
  two-line `limit <= 0` clamp, which mirrors an existing, already-integration-only-tested
  pattern (**D7**).

---

## Decisions

### D1 — `ListSessionsByVehicleUpdatedSince` mirrors `ListEntriesByVehicleUpdatedSince`'s index reasoning exactly: `updated_at` as a residual filter, ordered by the index's own trailing column (binding — dispatch/roadmap Decision 9)

`ListSessionsByVehicleUpdatedSince(ctx, accountID, teslaID, since) ([]Session, error)`
returns every session for the vehicle whose `updated_at >= since`, using
`idx_charge_sessions_vehicle_stop (account_id, tesla_id, charge_stop_date_time)` as a single
ascending range scan: `account_id`/`tesla_id` are the leading equality predicates the index
already serves for every other vehicle-scoped query on this table, and `updated_at >=
@since` is evaluated as a **residual filter** on each row the scan visits — exactly
`ListEntriesByVehicleUpdatedSince`'s own documented reasoning
(`internal/charging/db/query.sql:118`) transplanted onto this table's index. No new
`updated_at`-leading or `updated_at`-including index is added: this table receives one row
per Supercharger session per account, written nightly by the mirror plus occasional
human `VerifySession` calls — the same "small, write-driven table" profile that already
justified skipping a dedicated `updated_at` index for `manual_charge_entries`.

**Ordering:** results are ordered **ascending by `ChargeStopDateTime`** — the index's own
build order — not by `updated_at` (Context fact 2 distinguishes this from `telemetry`'s
sibling, which orders by `updated_at` itself). This choice is deliberate for two reasons:
(a) it is a pure forward index scan with no sort step, identical in shape to
`ListSessionsByVehicleBetween`'s own scan; (b) `ListEntriesByVehicleUpdatedSince` — the
method the dispatch names as the mirror target — already established that convention for
this exact "updated since" shape in this exact module, and Context fact 5 confirms
`analytics`'s consumer does not depend on any particular order, so there is no caller
pressure to order by `updated_at` instead.

**Why this is the mechanism that makes RM31 work at all.** `SessionVerifier.VerifySession`
(tier 1, already shipped) sets `updated_at = now()` in the same `UPDATE` that writes
`start_battery_pct`/`end_battery_pct`, and touches no other timestamp column
(`chargingdb.VerifyChargeSession`'s `SET` clause). `charge_start_date_time` and
`charge_stop_date_time` are write-once (RM29 D1) and therefore never move when a human
verifies a session. The *only* signal that a session changed after it was first mirrored is
`updated_at` — so `ListSessionsByVehicleUpdatedSince` is the sole path by which a
verification reaches `analytics.Recalculator.Reconcile`'s cursor-driven re-derivation once
tier 3 rewires the source port. No other method on this interface, and no method on
`telemetry.SuperchargerReader`, can carry that signal for `charge_sessions` — `telemetry`
never sees the human edit at all (roadmap Decision 2).

**No `LIMIT`:** `since` itself bounds the result, matching
`ListEntriesByVehicleUpdatedSince`'s and `ListSessionsByVehicleBetween`'s own precedent —
an unbounded cursor read is the caller's chosen contract, not a gap.

### D2 — No database object is created or altered (binding — dispatch/roadmap tier-2 prompt)

This change ships zero migrations. `internal/charging/db/migrations/` gains no new file,
and `20260823000001_add_charge_sessions.sql` is not touched. `idx_charge_sessions_vehicle_stop`
already serves both new queries (proven in §"Database Changes" below). Per the dispatch's
explicit instruction: **if this design had concluded a new index or any other database
object were needed, that conclusion would be reported as blocked rather than implemented**,
because it would trip the project's `database` design gate (`CLAUDE.md` §Pipeline config →
Design-Gates), which requires the owner's explicit sign-off before Apply. That conclusion
was not reached — see the index proofs below — so this change proceeds without a gate
confirmation step.

### D3 — `ListSessionsByVehicle` orders **DESC** by `ChargeStopDateTime`, served by a backward scan of the same ASC index — deliberately unlike `ListSessionsByVehicleBetween`'s ASC (this pass's own decision, per the dispatch's explicit instruction to justify sort direction against the index rather than copy a sibling)

`ListSessionsByVehicle(ctx, accountID, teslaID, limit) ([]Session, error)` returns the
`limit` most recent sessions for the vehicle, ordered **newest-first** (descending
`ChargeStopDateTime`).

**Why DESC is the right semantics for this method, independent of any index question.** This
is a limit-bounded "most recent N" read — the same access pattern
`Reader.ListEntriesByVehicle` already serves for `manual_charge_entries` (`charged_on
DESC LIMIT`) and `telemetry.SuperchargerReader.SuperchargerSessionsByVehicle` already serves
for `supercharger_sessions` (`charge_start_date_time DESC`). A "most recent N" read that
returned the *oldest* N rows under a `LIMIT` would silently return the wrong N rows whenever
a vehicle has more than `limit` sessions — not a style choice, a correctness requirement.

**Why this does NOT reuse `ListSessionsByVehicleBetween`'s ASC, and why that is not a
problem for the shared index.** `idx_charge_sessions_vehicle_stop` was built
`(account_id, tesla_id, charge_stop_date_time)` **ascending**, specifically because
`ListSessionsByVehicleBetween`'s bounded-window read needed ASC (RM30 D1's own stated
reason: "so a future read like this one gets a pure index range scan with no sort step" —
that "future read" was write-once at the time, this method is the read it anticipated
sharing the index with, not necessarily its sort direction). A B-tree index is traversable
in **either** direction at identical cost — Postgres does not need a second, DESC-built
index to serve `ORDER BY charge_stop_date_time DESC LIMIT N` efficiently; it walks the same
leaf-page chain backward instead of forward. This is exactly the situation
`ListEntriesByVehicle`'s own index (`idx_manual_charge_entries_vehicle_time`, built
**DESC**-leading) avoids needing to prove, because that index already matches its query's
direction — `charge_sessions`' single index does not match both of its methods' directions,
so this method is the one that needs the backward-scan proof, not the other. See
§"Database Changes" for the concrete `EXPLAIN`-level argument and the integration test that
confirms it (Test Contract T-Order2).

**Consequence, stated explicitly per the dispatch's instruction:** `ListSessionsByVehicleBetween`
and `ListSessionsByVehicle` are the two `Session` reads that "deliberately do NOT share a
sort-direction rule" (the warning already present in `ListSessionsByVehicleBetween`'s own
doc comment, RM30 D3). This design is the concrete case that warning was written for — a
future reader must not "fix" one to match the other; each direction is chosen against this
query's own access pattern, not a module-wide convention.

**`limit <= 0` uses the module's existing `defaultLimit` (100).** `defaultLimit` is already
a package-level constant in `service.go`, already reused by `readerService.ListEntriesByVehicle`
and `ListEntriesByAccount` with the identical `if limit <= 0 { limit = defaultLimit }` guard.
`sessionReader.ListSessionsByVehicle` reuses the same constant rather than defining its own —
the "closed, small vocabulary" instance `CLAUDE.md`'s AI-efficiency rule names for exactly
this kind of cross-file constant. The clamp runs in Go, before the query is issued — the SQL
`LIMIT @limit_count` parameter is always a positive `int32` by the time it reaches the
database.

### D4 — `tesla_id` filtering: reuse the existing nullable-equality pattern for both new methods (restates RM29 D6 / RM30 D6)

Both new queries filter `tesla_id = @tesla_id` against the same nullable `BIGINT` column
`ListSessionsByVehicleBetween` already filters this way. No new mapping code: both reuse the
existing `teslaIDToPgInt8` helper (`session_reader.go`, added by RM30 tier 1) unchanged.
Consequence, restated so no test "fixes" it: because SQL `NULL = value` is neither true nor
false, a session whose `tesla_id` is `NULL` (the VIN is not a currently-registered vehicle)
is **never** returned by either new method, for any `teslaID` — the identical, already-tested
behavior `ListSessionsByVehicleBetween` has (RM30 D6, RM29 D6), extended to two more call
shapes over the same column.

### D5 — Row mapping reuses the existing `rowToSession` helper unchanged

Both new queries return the same `chargingdb.ChargeSession` row shape
`ListSessionsByVehicleBetween` already returns (same `SELECT *` from the same table, no new
column). Both new methods call the existing `rowToSession` (`session_reader.go`, RM30 tier
1) with no modification — no new mapping code, no new pgtype helper.

### D6 — Both methods land on the existing `sessionReader` struct, in the existing `session_reader.go` — no new file

`ListSessionsByVehicleUpdatedSince` and `ListSessionsByVehicle` are added as two more
methods on the existing unexported `sessionReader` struct (`pool *pgxpool.Pool`, `q
*chargingdb.Queries`), in the existing `internal/charging/session_reader.go`, next to
`ListSessionsByVehicleBetween`. This is not a fresh design choice so much as the only shape
consistent with the module's own pattern: one struct/one file per port
(`sessionWriter`/`session_writer.go`, `sessionVerifier`/`session_verifier.go`,
`sessionReader`/`session_reader.go`), not one file per method. The compile-time assertion
`var _ SessionReader = (*sessionReader)(nil)` already present in the file continues to prove
both new methods satisfy the widened interface.

### D7 — Integration-only testing; no offline unit test for the `limit` clamp (restates RM30 D7 / RM31-tier-1's own precedent)

Neither new method is added to `service.go`'s `store` interface — that interface, and
`charging_test.go`'s fake-backed offline tests, exist for `Writer`/`Reader`'s CRUD-shaped
methods over `manual_charge_entries`. `sessionReader` (like `sessionWriter` and
`sessionVerifier` before it) is tested only via the real `DATABASE_URL`-gated integration
suite. The `limit <= 0` clamp inside `ListSessionsByVehicle` is a two-line conditional,
structurally identical to `readerService.ListEntriesByVehicle`'s own clamp — which itself has
never had a dedicated offline unit test in this codebase (confirmed: `charging_test.go`
covers only `Entry`'s three value-receiver methods). Extracting the clamp into a
separately-unit-tested pure function would be new-vocabulary-for-its-own-sake with no
existing precedent asking for it; it stays inline and is covered by the integration Test
Contract's T-Limit cases instead.

---

## Database Changes

**None.** No migration file is added or edited. This section exists — per
`openspec/config.yaml`'s blanket rule that "design.md is REQUIRED for any DB-touching
change" — to prove the existing schema already serves both new queries, not to record a
schema change.

### Query 1 — `ListSessionsByVehicleUpdatedSince` (`internal/charging/db/query.sql`, appended)

```sql
-- name: ListSessionsByVehicleUpdatedSince :many
-- Return charge sessions for a specific vehicle within an account whose updated_at is at
-- or after @since, ordered oldest-first by charge_stop_date_time (design.md D1) — NOT by
-- updated_at itself, and NOT ListEntriesByVehicleUpdatedSince's DESC: this table's index
-- is built ASC (RM30 D1), so ascending on the index's own trailing column is the order
-- that needs no sort step. Reuses idx_charge_sessions_vehicle_stop (account_id, tesla_id,
-- charge_stop_date_time) as a single ascending index range scan: account_id and tesla_id
-- prune to the tenant and vehicle as leading equality predicates in the same scan every
-- other vehicle-scoped query on this table already uses; updated_at >= @since is a
-- RESIDUAL filter evaluated per matching row within that scan, not a separately-indexed
-- predicate (design.md D1) -- the identical reasoning
-- ListEntriesByVehicleUpdatedSince (query.sql:118) already documents for
-- manual_charge_entries's own index. No new index: this table receives roughly one row
-- per Supercharger session per account, written nightly, the same low-volume,
-- write-driven profile that already justified skipping a dedicated updated_at index
-- there.
--
-- THIS QUERY IS THE ONLY MECHANISM (design.md D1) that carries a
-- SessionVerifier.VerifySession edit into analytics.Recalculator.Reconcile once tier 3
-- repoints the source port: VerifySession sets updated_at = now() and touches no other
-- timestamp column, and charge_start_date_time/charge_stop_date_time are write-once
-- (RM29 D1), so updated_at is the only column that moves when a human verifies a
-- session.
--
-- No LIMIT: @since itself bounds the result, matching ListEntriesByVehicleUpdatedSince's
-- and ListSessionsByVehicleBetween's own precedent.
--
-- tesla_id = @tesla_id against a nullable column excludes every row where tesla_id IS
-- NULL (SQL's NULL = value is neither true nor false) -- an orphaned session is
-- correctly outside a teslaID-keyed read (design.md D4, restating RM29 D6/RM30 D6).
SELECT * FROM charge_sessions
WHERE account_id = @account_id
  AND tesla_id = @tesla_id
  AND updated_at >= @since
ORDER BY charge_stop_date_time ASC;
```

**Go-side call shape** (mirroring `ListSessionsByVehicleBetween`'s existing shape in
`session_reader.go`, no `endBound` translation needed — `since` is used as-is):

```go
func (r *sessionReader) ListSessionsByVehicleUpdatedSince(ctx context.Context, accountID uuid.UUID, teslaID int64, since time.Time) ([]Session, error) {
	rows, err := r.q.ListSessionsByVehicleUpdatedSince(ctx, chargingdb.ListSessionsByVehicleUpdatedSinceParams{
		AccountID: accountID,
		TeslaID:   teslaIDToPgInt8(teslaID),
		Since:     pgtype.Timestamptz{Time: since, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("charging: list sessions by vehicle updated since: %w", err)
	}
	sessions := make([]Session, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, rowToSession(row))
	}
	return sessions, nil
}
```

### Query 2 — `ListSessionsByVehicle` (`internal/charging/db/query.sql`, appended)

```sql
-- name: ListSessionsByVehicle :many
-- Return the limit_count most recent charge sessions for a specific vehicle within an
-- account, ordered newest-first (descending charge_stop_date_time), limited to
-- @limit_count rows.
--
-- Sort direction is DESC here, DELIBERATELY UNLIKE ListSessionsByVehicleBetween's ASC
-- (design.md D3 of this change -- ListSessionsByVehicleBetween's own doc comment already
-- warns these two Session reads do not share a sort-direction rule). A "most recent N"
-- limit-bounded read needs newest-first by construction, the same reasoning
-- Reader.ListEntriesByVehicle already applies to manual_charge_entries and
-- telemetry.SuperchargerSessionsByVehicle already applies to supercharger_sessions.
--
-- idx_charge_sessions_vehicle_stop (account_id, tesla_id, charge_stop_date_time) was
-- built ASC, not DESC (RM30 D1, for ListSessionsByVehicleBetween's own bounded-window
-- read). This query still needs NO new index: Postgres serves
-- ORDER BY charge_stop_date_time DESC LIMIT @limit_count from the SAME ascending btree
-- via a backward index scan -- a B-tree index is traversable in either direction at
-- identical cost, so account_id/tesla_id still prune the scan to a single contiguous
-- leaf-page range and only the walk direction (and hence the row order handed up)
-- differs (design.md D3, "Index proof" below). Confirmed by EXPLAIN in the integration
-- test (Test Contract T-Order2), not merely asserted.
--
-- limit_count is always a positive int32 by the time this query runs: the Go caller
-- clamps a non-positive limit to the module's existing defaultLimit (100) before
-- calling (design.md D3), mirroring ListEntriesByVehicle's identical clamp -- this
-- query itself has no default-handling logic, exactly like ListEntriesByVehicle's own
-- :many query.
--
-- tesla_id = @tesla_id against a nullable column excludes every row where tesla_id IS
-- NULL, same as every other vehicle-scoped query on this table (design.md D4).
SELECT * FROM charge_sessions
WHERE account_id = @account_id
  AND tesla_id = @tesla_id
ORDER BY charge_stop_date_time DESC
LIMIT @limit_count;
```

**Go-side call shape:**

```go
func (r *sessionReader) ListSessionsByVehicle(ctx context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]Session, error) {
	if limit <= 0 {
		limit = defaultLimit
	}
	rows, err := r.q.ListSessionsByVehicle(ctx, chargingdb.ListSessionsByVehicleParams{
		AccountID:  accountID,
		TeslaID:    teslaIDToPgInt8(teslaID),
		LimitCount: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("charging: list sessions by vehicle: %w", err)
	}
	sessions := make([]Session, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, rowToSession(row))
	}
	return sessions, nil
}
```

### Index proof — why `idx_charge_sessions_vehicle_stop` already serves both queries

The index, created by RM29 tier 6's migration and unchanged by this or any prior tier:

```sql
CREATE INDEX idx_charge_sessions_vehicle_stop
    ON charge_sessions (account_id, tesla_id, charge_stop_date_time);
```

**Query 1 (`ListSessionsByVehicleUpdatedSince`) — forward scan, residual filter:**

| Query clause | Index column | Role |
|---|---|---|
| `WHERE account_id = @account_id` | `account_id` (1st) | Leading equality predicate — prunes to the tenant. |
| `WHERE tesla_id = @tesla_id` | `tesla_id` (2nd) | Second equality predicate in the same range scan — prunes to the vehicle. |
| `WHERE updated_at >= @since` | *not indexed* — `updated_at` is not a column of this index | Evaluated as a **residual filter** on every row the `(account_id, tesla_id)` scan visits (a Filter, not an Index Cond, in `EXPLAIN` terms) — cheap because that scan is already narrowed to one vehicle's rows, typically a handful to a few dozen. |
| `ORDER BY charge_stop_date_time ASC` | `charge_stop_date_time` (3rd, trailing), index built ASC | The index's natural order already matches the requested order — **no sort step**, same as `ListSessionsByVehicleBetween`'s existing proof. |

This is structurally identical to `ListEntriesByVehicleUpdatedSince`'s own already-shipped
proof over `idx_manual_charge_entries_vehicle_time` — a residual filter on a non-indexed
column, evaluated within an index scan already narrowed by two leading equality predicates.

**Query 2 (`ListSessionsByVehicle`) — backward scan, no residual filter:**

| Query clause | Index column | Role |
|---|---|---|
| `WHERE account_id = @account_id` | `account_id` (1st) | Leading equality predicate — prunes to the tenant. |
| `WHERE tesla_id = @tesla_id` | `tesla_id` (2nd) | Second equality predicate in the same range scan — prunes to the vehicle. |
| `ORDER BY charge_stop_date_time DESC` | `charge_stop_date_time` (3rd, trailing), index built ASC | Postgres satisfies a `DESC` request on an `ASC`-built index by walking the same leaf-page chain **backward** ("Index Scan Backward" in `EXPLAIN`) — still **no sort step**, because the scan direction, not the index's declared direction, determines the row order handed up. |
| `LIMIT @limit_count` | — | Bounds the backward scan to the first `limit_count` rows encountered — Postgres stops walking as soon as it has enough rows, exactly as it would for a forward-scanned `LIMIT`. |

**A backward index scan is not a degraded fallback — it is the same cost class as a forward
scan, confirmed, not assumed.** A B-tree leaf level is a doubly-linked list of pages
precisely so it can be walked in either direction without re-deriving position from the
root; Postgres has supported backward index scans natively since long before this project's
Postgres 13+ baseline. The planner will choose "Index Scan Backward using
idx_charge_sessions_vehicle_stop" for this query whenever the leading two predicates are
selective enough to prefer an index scan at all (true here — every declared read on this
table is vehicle-scoped, never account-wide) — it does not fall back to a sequential scan
plus in-memory sort merely because the query's `ORDER BY` direction is the opposite of the
index's build direction. The integration test's `T-Order2` case runs `EXPLAIN` against this
exact query to confirm "Index Scan Backward" (not "Sort") appears in the plan, so this proof
is verified against the real planner, not asserted from documentation alone.

**Deliberately NOT added, restating RM29 tier 6's and RM30 tier 1's own Index Plans
(unchanged by this tier):** a second, `DESC`-built copy of `idx_charge_sessions_vehicle_stop`
(the backward-scan proof above is exactly what makes a duplicate index unnecessary — it
would only save Postgres a scan-direction flag, at the cost of doubling this table's index
write overhead on every nightly mirror pass), an `updated_at`-leading or
`updated_at`-including index (Query 1's residual-filter proof above is exactly what makes
one unnecessary at this table's low, write-driven row volume), an account-wide
`(account_id, charge_stop_date_time)` index (no such read is declared — both new methods are
always vehicle-scoped), and a `vin`-keyed index (every declared read is by `tesla_id`).
Nothing in this tier changes any of those conclusions.

---

## Test Contract (expected values authored before implementation, per `ai/go-conventions.md`)

All tests are `DATABASE_URL`-gated integration tests in `internal/charging`'s existing
external test package (`package charging_test`), seeding `charge_sessions` via
`SessionWriter.MirrorSessions` and, where a verified percentage or a specific `updated_at`
is needed, direct SQL — mirroring `db_session_reader_integration_test.go`'s existing pattern
— and asserting against `charging.Session` domain fields only, **no `pgtype` in any
assertion** (`internal/charging/AGENTS.md` §Testing Notes). Fixtures use fresh `uuid.New()`
account ids and `session_id`s in the **960001–960099** range — disjoint from RM29 tier 6's
920001–920099, RM30 tier 1's 940001–940099, RM31 tier 1's 950001–950099, and the real
backfilled `734860294`.

### Method 1 — `ListSessionsByVehicleUpdatedSince`

Baseline fixture **U1**, seeded under a fresh `acctA` / `TeslaID = 960001`, via
`SessionWriter.MirrorSessions` (which sets `updated_at` to "now" as of the mirror call),
followed by a direct-SQL `UPDATE ... SET updated_at = @t` per row to pin exact values:

| SessionID | updated_at (pinned) | ChargeStopDateTime |
|---|---|---|
| 960001 | `2026-08-01T00:00:00Z` | `2026-07-30T12:00:00Z` |
| 960002 | `2026-08-05T00:00:00Z` | `2026-08-04T09:00:00Z` |
| 960003 | `2026-08-10T00:00:00Z` | `2026-08-09T18:00:00Z` |

**T1. A session updated by `VerifySession` after the mirror pass becomes visible at a
`since` after that pass.** Simulate the RM31 end-to-end scenario directly: seed 960001 via
`MirrorSessions` with `updated_at` pinned to `2026-08-01T00:00:00Z` (the mirror pass), then
call `SessionVerifier.VerifySession(ctx, acctA, 960001's id, ptr(50), ptr(90))` — which the
implementation already sets `updated_at = now()` for. Call
`ListSessionsByVehicleUpdatedSince(ctx, acctA, 960001teslaID, since =
2026-08-01T00:00:01Z)` (one second after the mirror pass, before the verification's `now()`).
Expected: the session **is present**, and its `StartBatteryPct == 50`,
`EndBatteryPct == 90` — proving the exact mechanism design.md D1 describes: the verification
is invisible to a `since` cursor taken before it, and visible to one taken after the mirror
pass but before the verification's own timestamp, precisely because `updated_at` (not
`charge_start_date_time`/`charge_stop_date_time`) is what moved.

**T2. The boundary case `updated_at == since` is included (inclusive lower bound).** Using
**U1**, call with `since = 2026-08-05T00:00:00Z` (960002's exact pinned `updated_at`).
Expected: 960002 is present (not excluded by an off-by-one on `>= @since`); 960001
(`updated_at` before `since`) is absent.

**T3. A session with `updated_at` strictly before `since` is excluded.** Same call as T2.
Expected: 960001 is absent.

**T4. No match returns a non-nil empty slice.** Call `ListSessionsByVehicleUpdatedSince` for
a `teslaID` with no sessions updated at or after a `since` later than every fixture's
`updated_at` (e.g. `2027-01-01T00:00:00Z`). Expected: `len(result) == 0` and
`result != nil`.

**T5. A session whose `tesla_id` is `NULL` is never returned, for any `teslaID` or
`since`.** Mirror a session with `TeslaID: nil` (simulating a deregistered vehicle), pin its
`updated_at` well after `since`. Expected: not returned by any `teslaID` value passed to
`ListSessionsByVehicleUpdatedSince` — design.md D4's documented consequence, not a bug.

**T6. Multi-tenant isolation.** Seed an identical `updated_at`-matching session under a
distinct `acctB` with the same `TeslaID`. Call scoped to `acctA`. Expected: only `acctA`'s
sessions are returned.

**T7. Ordering is ascending by `ChargeStopDateTime`, not by `updated_at` and not insertion
order.** Using **U1** with a `since` that matches all three rows (e.g. epoch), expected
order: `960001` (`ChargeStopDateTime = 2026-07-30T12:00:00Z`), then `960002`
(`2026-08-04T09:00:00Z`), then `960003` (`2026-08-09T18:00:00Z`) — this is the **stop-time**
order, which happens to coincide with `updated_at` order in this fixture on purpose (both
increase together across 960001→960003); the assertion checks `SessionID` sequence against
`ChargeStopDateTime`, not against the pinned `updated_at` values, so a future accidental
switch to `ORDER BY updated_at` would not silently pass this test if a re-run reordered the
`updated_at` pins independently of stop-time.

### Method 2 — `ListSessionsByVehicle`

Baseline fixture **L1**, seeded under a fresh `acctA` / `TeslaID = 960010`, via
`SessionWriter.MirrorSessions`, four sessions with strictly increasing
`ChargeStopDateTime`:

| SessionID | ChargeStopDateTime |
|---|---|
| 960011 | `2026-08-01T00:00:00Z` (oldest) |
| 960012 | `2026-08-10T00:00:00Z` |
| 960013 | `2026-08-20T00:00:00Z` |
| 960014 | `2026-08-30T00:00:00Z` (newest) |

**T8. `limit` returns exactly the newest `limit` sessions, newest-first.** Call
`ListSessionsByVehicle(ctx, acctA, 960010teslaID, 2)`. Expected: `len(result) == 2`,
`result[0].SessionID == 960014`, `result[1].SessionID == 960013` — the two newest by
`ChargeStopDateTime`, in descending order, **not** 960011/960012 (which a mistaken ASC
`LIMIT` would return).

**T9. `limit` larger than the available row count returns every row, still newest-first.**
Call with `limit = 100`. Expected: all four rows, ordered `960014, 960013, 960012, 960011`.

**T10. `limit == 0` uses the server default (`defaultLimit = 100`) — same contract as
`Reader.ListEntriesByVehicle`.** Call with `limit = 0`. Expected: all four rows returned
(fewer than `defaultLimit`), ordered `960014, 960013, 960012, 960011` — proving the clamp
fires rather than returning zero rows.

**T11. A negative `limit` also uses the server default, exactly like `limit == 0`.** Call
with `limit = -5`. Expected: identical result to T10 — `limit <= 0` is the documented guard
(design.md D3), not `limit == 0` alone.

**T12. No match returns a non-nil empty slice.** Call for a `teslaID` with zero sessions.
Expected: `len(result) == 0` and `result != nil`.

**T13. A session whose `tesla_id` is `NULL` is never returned, for any `teslaID` or
`limit`.** Mirror a session with `TeslaID: nil`. Expected: absent from every `limit` value's
result (design.md D4).

**T14. Multi-tenant isolation and cross-vehicle isolation**, mirroring
`ListSessionsByVehicleBetween`'s existing T6/T7: a same-`tesla_id` session under a distinct
`acctB`, and a different-`tesla_id` session under `acctA`, are both absent from
`ListSessionsByVehicle(ctx, acctA, 960010teslaID, ...)`'s result.

**T-Order2. `EXPLAIN` confirms a backward index scan, not a sort step, for the `DESC`
query.** Inside a transaction, issue `SET LOCAL enable_seqscan = off`, then run
`EXPLAIN (FORMAT TEXT) SELECT ...` (the literal `ListSessionsByVehicle` SQL, with the
fixture's real parameter values substituted) and assert the plan text contains
`"Index Scan Backward using idx_charge_sessions_vehicle_stop"` and does **not** contain a
`"Sort"` node. This is the concrete verification for design.md D3's index proof — a
planning-level assertion, not just a result-correctness one.

**`SET LOCAL enable_seqscan = off` is load-bearing, not a workaround — do not delete it.**
The fixture holds four rows. At that size Postgres's cost model prefers a **Seq Scan** over
any index scan, so the unguarded assertion would fail against a design that is entirely
correct — it would be measuring the planner's row-count arithmetic, not the property under
test. The property under test is *"can this ASC-built index serve a `DESC` order without a
`Sort` step?"*, and disabling seq scans is what forces the planner to answer that exact
question. The `Sort`-absence half of the assertion still has full force under the setting:
`enable_seqscan = off` does not suppress a `Sort` node, so if the index genuinely could not
serve the `DESC` order backward, the plan would come back as an index scan **plus** a
`Sort`, and the test would fail as intended. `SET LOCAL` scopes the setting to the
transaction, so no other test in the package is affected.

### What must NOT change

- `ListSessionsByVehicleBetween`'s existing `db_session_reader_integration_test.go`
  assertions (T1–T11 from RM30 tier 1): not one assertion, fixture, or name.
- `SessionWriter`'s, `SessionVerifier`'s, and `Reader`'s/`Writer`'s existing test files: not
  one assertion, fixture, or name.

---

## Risks / Trade-offs

- **A single index now serves two queries whose `ORDER BY` directions disagree.** Accepted
  deliberately (D3): a second, DESC-built duplicate index would eliminate the (already
  negligible) planner reliance on backward-scan support in exchange for doubling this
  table's index-maintenance cost on every nightly mirror write. Read-heavy profile
  (`ai/architecture.md` §7) favors the existing single index; the `EXPLAIN`-verified proof
  (Test Contract T-Order2) removes the remaining uncertainty about whether the backward scan
  actually fires.
- **`ListSessionsByVehicleUpdatedSince` and `ListSessionsByVehicle` both ship with zero
  callers this tier.** Standard for a roadmap's middle tier (RM30 tier 1 shipped
  `ListSessionsByVehicleBetween` the same way, one tier ahead of its gateway consumer). Both
  ports are exercised only by their own integration tests until tier 3 lands; `go vet` and
  `make build` still cover them structurally.
- **The `limit <= 0` clamp is untested by any offline unit test**, by design (D7) — covered
  only by the integration suite's T10/T11. Accepted because the clamp has no existing
  offline-test precedent in this module and extracting it into a separately-tested pure
  function would be new vocabulary with no caller asking for the extra indirection.
- **A residual, non-indexed filter (`updated_at >= @since`) inside an otherwise-indexed
  scan is slightly more expensive per visited row than a fully-indexed predicate.**
  Accepted deliberately (D1), for the same reason `ListEntriesByVehicleUpdatedSince` already
  accepted it: the leading two-column equality prefix already narrows the scan to one
  vehicle's rows (a handful to a few dozen at this table's current and projected volume),
  so the residual filter's cost is a few in-memory comparisons, not a table-wide scan.

## Verification signals

Per the binding `Test-Execution-Policy`, the assistant runs and reports: `go build ./...`,
`go vet ./...`, `gofmt -l .`, `make build`, `make vet`, `make bins`, `make sqlc` (after both
new queries are appended), and the three standalone guards — `make ui-guard` (no-op: no
gateway markup), `make i18n-guard` (no-op: no user-facing string), `make money-guard`
(no-op: gateway-only grep, this change touches no gateway file). It also runs
`openspec validate --changes --strict`.

The owner alone runs the suite:

```
go test ./internal/charging/...
```

or the full suite:

```
go test ./...
```

Until the owner runs these and reports the results, this tier's implementation status is
**awaiting-user-verification**, never "done".
