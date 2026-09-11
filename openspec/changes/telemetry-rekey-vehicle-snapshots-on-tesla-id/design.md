# Design — telemetry-rekey-vehicle-snapshots-on-tesla-id

Required because this change touches the database (`openspec/config.yaml` design gate).

## Overview

Two changes, in a fixed order, because the second is only correct once the
first has landed:

1. **Poll election** — pure Go, no schema change. Pick one account to poll
   each car, so `vehicle_snapshots` stops receiving two writers for the same
   `(tesla_id, captured_date)`.
2. **Re-key `vehicle_snapshots`** from `(account_id, tesla_id, captured_date)`
   to `(tesla_id, captured_date)`, and rename `poll_attempts.account_id` to
   `poll_attempts.polled_by_account_id`.

Four pieces after that, in dependency order: migration, queries + `sqlc
generate`, ports + implementation, callers and their tests, then docs.

## Part 1 — Poll election (D-ELECT-1 / D-ELECT-2, both binding, decided in
the interview — recorded here, not re-opened)

### D-ELECT-1 — Election rule: prefer OWNER, fall back to any, never skip

**Decision:** for every distinct `tesla_id` in `account.AllRegisteredVehicles`'s
result, elect exactly one `(account_id, tesla_id)` pair to poll:

1. Prefer a candidate whose `AccessType` is `"OWNER"`.
2. If no candidate is `OWNER`, use any candidate that registered the vehicle.
3. Tie-break (two candidates at the same preference level — two `OWNER`s, or
   no `OWNER` and multiple candidates) by **lowest `account_id`**, compared as
   raw UUID bytes.
4. **A vehicle is never skipped**, regardless of `AccessType`.

**Why never skip.** Tesla's `vehicle_data` takes no date filter (see
`internal/telemetry/AGENTS.md`'s "Why nightly collection exists at all"). A
missed night is a **permanent** gap — no later call can recover it. The dev
database proves an `OWNER`-only filter would create exactly that gap today:

```
tesla_id           access_type  display_name       snapshots
3744325961659064   DRIVER       Palomito Nachito   56
3744327027802250   OWNER        Magus              56
```

`Palomito Nachito` has 56 nights of real history and no `OWNER` account
anywhere. An `access_type = 'OWNER'` filter stops polling it starting the
next run. `AccessType` is also `*string` (nullable) on `account.OwnedVehicle`
— a NULL would be silently skipped by an `OWNER`-only filter too. This is
exactly the failure mode `account`'s own `resolveSelectedVehicle` already
guards against with the same OWNER-then-fallback shape, for the same reason.

**Why lowest `account_id`, not oldest registration.** `account.OwnedVehicle`
carries no timestamp (`AccountID`, `TeslaID`, `VIN`, `DisplayName`,
`AccessType`, `ExteriorColor`, `CarType` — verified,
`internal/account/account.go`). Adding one would be an `account`-module
schema change this ticket does not need and was not asked for. `account_id`
is a `uuid.UUID` (v4, random) — comparing it gives a stable, deterministic
tie-break with no extra data, at the cost of being arbitrary rather than
meaningful. That trade is fine here: the tie-break only fires when two
candidates are otherwise equal, and its only job is to stop the elected
account from flapping between runs for no reason.

### D-ELECT-2 — No token liveness check at election time (accepted limitation)

**Decision:** election reads only the in-memory vehicle list —
`AccessType` and `AccountID`. It makes **no** DB call and **no** token probe.

**Why.** `AccessTokenFor` (`internal/account/service.go:108`) is not a cheap
read: it opens a transaction, locks the row `FOR UPDATE`, and **rotates the
single-use refresh token**. The `account` port exposes no cheap "has a live
connection" read. Probing every candidate at election time would rotate
tokens for accounts that then poll nothing that night — a real, avoidable
cost for a check this design does not need to make (see `AllRegisteredVehicles`'s
consumer-facing decision below).

This mirrors an existing, deliberate decision already recorded on
`ListAllVehicles` (`internal/account/db/query.sql:158`): *"No join to
tesla_tokens: enumeration is decoupled from connection liveness (that is the
caller's job via AccessTokenFor)."* Election stays on the enumeration side of
that line; liveness is still resolved later, once per account, inside
`collectAccount`'s existing `AccessTokenFor` call — unchanged by this ticket.

**Accepted limitation, to revisit when vehicle sharing lands:** if the
elected account's token turns out to be dead, `collectAccount`'s existing
whole-account short-circuit fires and every one of that account's vehicles —
including this one — is recorded `unauthorized` for the night. A *different*
account that also registered the same car is **not** tried as a fallback.
This is unreachable today (no car has two accounts in the live database), and
it is exactly today's behavior for a single-account car — election does not
make it worse, it just makes the failure mode reachable in a two-account
case for the first time. Fixing it needs a rescue path across accounts,
which is out of scope here and belongs with vehicle sharing.

### Where election lives

A new pure function in `internal/telemetry/service.go`, called at the top of
`CollectAll` before `groupByAccount`:

```go
// electPollingVehicles picks exactly one owning account to poll each distinct
// tesla_id, so a car registered to more than one account is fetched once per
// night, not once per registering account (D-ELECT-1). Prefers OWNER; falls
// back to any candidate; a vehicle is NEVER dropped regardless of AccessType
// (a missed night is a permanent gap — the Fleet API has no date filter).
// Ties (two OWNERs, or no OWNER with several candidates) break on the lowest
// AccountID, compared as raw bytes — arbitrary but deterministic, so the
// elected account does not flap between runs with no real change.
//
// Pure Go, no DB call: it reads only what AllRegisteredVehicles already
// returned. It does NOT check whether the elected account's token is usable
// — that stays collectAccount's job via the existing AccessTokenFor call,
// so election never pays AccessTokenFor's cost (a FOR UPDATE row lock plus a
// single-use refresh-token rotation) for a candidate it might not even use.
//
// Sorts its own input explicitly rather than trusting the caller's order:
// ListAllVehicles happens to return rows ordered (account_id, tesla_id), but
// that is an account-module implementation detail telemetry must not
// silently depend on.
func electPollingVehicles(vehicles []account.OwnedVehicle) []account.OwnedVehicle {
	sorted := make([]account.OwnedVehicle, len(vehicles))
	copy(sorted, vehicles)
	sort.Slice(sorted, func(i, j int) bool {
		return bytes.Compare(sorted[i].AccountID[:], sorted[j].AccountID[:]) < 0
	})

	elected := make(map[int64]account.OwnedVehicle, len(sorted))
	for _, v := range sorted {
		current, ok := elected[v.TeslaID]
		if !ok {
			elected[v.TeslaID] = v
			continue
		}
		if !isOwner(current) && isOwner(v) {
			elected[v.TeslaID] = v
		}
		// Otherwise keep current: it already has the lower AccountID (sorted
		// ascending above) at the same or better preference level.
	}

	result := make([]account.OwnedVehicle, 0, len(elected))
	for _, v := range elected {
		result = append(result, v)
	}
	return result
}

func isOwner(v account.OwnedVehicle) bool {
	return v.AccessType != nil && *v.AccessType == "OWNER"
}
```

`CollectAll` changes from:

```go
vehicles, err := s.acct.AllRegisteredVehicles(ctx)
...
byAccount := groupByAccount(vehicles)
```

to:

```go
vehicles, err := s.acct.AllRegisteredVehicles(ctx)
...
elected := electPollingVehicles(vehicles)
byAccount := groupByAccount(elected)
```

`groupByAccount` and `collectAccount` are otherwise **unchanged**: the
existing one-`AccessTokenFor`/one-`ListVehicles`-per-account batching
(design D3, `internal/telemetry/service.go:174-255`) still runs, just over
the elected subset. `report.AccountsAttempted` now counts elected accounts,
not every account that has ever registered a vehicle — a behavior change
worth stating plainly, not a bug: an account that registers only vehicles
lost to another account's `OWNER` election is no longer attempted at all,
because it has nothing left to poll.

**Result order is unspecified** (`result` is built from a `map` range). No
existing caller of `CollectAll` depends on vehicle order — `groupByAccount`
re-buckets by account regardless, and `report.AccountsAttempted` is a count.
No new determinism requirement is introduced here; the only thing that must
be deterministic is *which* account wins a given `tesla_id`, which the sort
+ single-pass comparison above already guarantees.

## Part 2 — Re-key `vehicle_snapshots`

### D-INDEX — no replacement index beyond the new UNIQUE constraint

**Decision:** the migration drops `idx_vehicle_snapshots_vehicle_time
(account_id, tesla_id, captured_at)` and replaces it with **only** the index
the new `UNIQUE (tesla_id, captured_date)` constraint itself creates. No
second, explicit `(tesla_id, captured_at)` index is added.

**Rationale — every existing query is still served:**

| Query | Access pattern | Served by `(tesla_id, captured_date)` how |
|---|---|---|
| `InsertVehicleSnapshot` (`ON CONFLICT`) | `(tesla_id, captured_date)` | the conflict target IS this index |
| `SnapshotsByVehicleSince` | `tesla_id = $1 AND captured_at >= $2 ORDER BY captured_at ASC` | `tesla_id` pinned by equality; `captured_date` is monotonic non-decreasing in `captured_at` for one vehicle (at most one row per calendar day), so the index still prunes to this vehicle's rows in one range scan before the `captured_at` residual filter/sort — same shape the old three-column index gave, minus the redundant `account_id` prefix |
| `SnapshotsByVehicleBetween` | `tesla_id = $1 AND captured_at ∈ [start_bound, end_bound)` | same as above |
| `SnapshotsByVehicleUpdatedSince` | `tesla_id = $1 AND updated_at >= $2` | `tesla_id` prunes the scan; `updated_at` is already a residual filter on the OLD index too (it is not `captured_date`-ordered) — no regression |
| `SnapshotPrecedingDay` | `tesla_id = $1 AND captured_date < $2 ORDER BY captured_at DESC LIMIT 1` | `(tesla_id, captured_date)` **is** the exact predicate pair, in index order — this query improves from a residual filter to a direct index bound |
| `LatestSnapshotsByVehicles` | `tesla_id = ANY($1) ORDER BY tesla_id, captured_at DESC` (`DISTINCT ON`) | `tesla_id` is the leading column; the old index needed `account_id` first only because callers filtered on it — gone now |

No fifth query exists on this table beyond these six.

**Why not keep a `(tesla_id, captured_at)` index too, alongside the UNIQUE
`(tesla_id, captured_date)` one:** `captured_date` and `captured_at` are
different columns (a `DATE` derived from a `TIMESTAMPTZ`, computed once in
Go — `snapshotFrom`/`clock.CalendarDay`), so the UNIQUE index does not
literally serve a `captured_at`-ordered scan the way the old
`(account_id, tesla_id, captured_at)` index did. But every query above that
filters on `captured_at` (`Since`, `Between`) still gets `tesla_id`-scoped
pruning from the UNIQUE index — Postgres does not require the ordered
column to lead every predicate before it can use an index to eliminate
non-matching rows. The residual `captured_at` filter and sort happen over a
row set already narrowed to one vehicle, which at this platform's
one-row-per-vehicle-per-day cadence (`vehicle_snapshots_account_tesla_date_unique`,
soon `vehicle_snapshots_tesla_date_unique`) is at most one row per day — the
same bound `SnapshotPrecedingDay`'s own doc comment already relies on. A
dedicated second index would only remove that residual filter step, at the
cost of a second btree the nightly upsert must also maintain, for a query
population this small. This project's read-heavy Performance-Profile calls
for indexing aggressively **for reads that exist and are expensive** — not
for shaving a residual filter off an already-tiny per-vehicle row set. If a
vehicle's history ever grows large enough that this residual filter shows up
in a slow-query log, add the second index then, against that measurement —
the same "add it when a real read pattern appears" rule the pilot's D-INDEX
already established for `charge_gaps`.

**Rejected: keeping `account_id` in the index "for reads that might come
back."** Nothing reads `vehicle_snapshots` by `account_id` alone once the
column is dropped — there is no such column to filter on. This is not a
choice between two designs; it is a consequence of D-elect/D-INDEX's parent
decision to drop the column at all.

### D-MIGRATION — duplicate collapse before the new constraint

**Decision:** before dropping the old constraint and adding the new one,
delete any row that would violate `UNIQUE (tesla_id, captured_date)`, keeping
the row with the latest `captured_at` (ties broken by `id`) — the exact rule
`20260805000001_dedupe_vehicle_snapshots_daily.sql` already applies one
column narrower (`account_id, tesla_id, captured_date` → `tesla_id,
captured_date`).

**Why latest `captured_at` wins (not earliest, unlike the pilot's
`charge_gaps` collapse):** this is the table's own existing rule, not a new
one this change invents — `20260805000001`'s design D1 already established
"the newest capture for a calendar day wins" as `vehicle_snapshots`'s
write-path semantics (`InsertVehicleSnapshot`'s `ON CONFLICT DO UPDATE`
follows the same rule today, one column narrower). A snapshot is Tesla's
current vehicle state at time of capture; a later same-day capture is
strictly fresher information about that same day, so keeping it — and
discarding the earlier, staler capture — is consistent with what the write
path already does for a single account's own repeat captures. This is
different from `charge_gaps`'s pilot decision (earliest `created_at` wins),
because that table's `created_at` answers "how long has this been
outstanding" — an entirely different question with the opposite right
answer. Re-key migrations do not share one dedupe rule; each inherits the
rule its own table's write path already established.

**Expected real-world impact: at most a handful of rows, all synthetic.**
Measured on the dev database 2026-09-10: 39 duplicate `(tesla_id,
captured_date)` groups, every one traced to an integration-test fixture, not
real capture data — both real cars have exactly one registering account
today. MAG-72 has since cleaned those 78 orphan rows and fixed the fixture
that created them (Done as of this design). So this migration is expected to
delete **zero** rows on the current database. That expectation does NOT
change how the migration is written: it is authored as if real duplicates
existed, per the ticket's explicit instruction, so it is safe to run against
any future state where poll election has not yet suppressed every
duplicate-writer case (e.g. a database migrated before Part 1's Go change is
deployed).

### Migration SQL

New file: `internal/telemetry/db/migrations/20260911000001_rekey_vehicle_snapshots_on_tesla_id.sql`.

```sql
-- +goose Up
-- Collapse any (tesla_id, captured_date) duplicates before the new UNIQUE
-- constraint can be added. Keeps the row with the latest captured_at, ties
-- broken by id -- the same "newest capture wins" rule the write path
-- already applies one column narrower (20260805000001's own dedupe, design
-- D1: a same-day re-capture REPLACES the existing row). On a database where
-- poll election (Part 1 of this change) has already suppressed every
-- duplicate-writer case, this deletes zero rows; it exists so the migration
-- is still safe against a database migrated before that Go change deploys.
DELETE FROM telemetry.vehicle_snapshots a
USING telemetry.vehicle_snapshots b
WHERE a.tesla_id = b.tesla_id
  AND a.captured_date = b.captured_date
  AND (a.captured_at < b.captured_at
       OR (a.captured_at = b.captured_at AND a.id < b.id));

ALTER TABLE telemetry.vehicle_snapshots
    DROP CONSTRAINT vehicle_snapshots_account_tesla_date_unique;

-- Schema-qualified: an index name is resolved through search_path, and
-- goose does not guarantee telemetry is on it.
DROP INDEX telemetry.idx_vehicle_snapshots_vehicle_time;

ALTER TABLE telemetry.vehicle_snapshots
    DROP COLUMN account_id;

ALTER TABLE telemetry.vehicle_snapshots
    ADD CONSTRAINT vehicle_snapshots_tesla_date_unique UNIQUE (tesla_id, captured_date);

-- poll_attempts.account_id is NOT dropped -- it is renamed. It has never
-- been a co-identity column (the row's identity is (vehicle, run)); it
-- records which account's token paid for this Fleet API call. That fact
-- only becomes reliable once poll election (Part 1) removes the possibility
-- of two accounts both attempting the same vehicle in one cycle.
ALTER TABLE telemetry.poll_attempts
    RENAME COLUMN account_id TO polled_by_account_id;

-- +goose Down
-- NOT a full rollback of history: rows deleted by the Up migration's
-- collapse step are NOT recoverable (their raw_data is gone with them).
-- Down only reverses this migration's own schema changes.
ALTER TABLE telemetry.poll_attempts
    RENAME COLUMN polled_by_account_id TO account_id;

ALTER TABLE telemetry.vehicle_snapshots
    DROP CONSTRAINT vehicle_snapshots_tesla_date_unique;

-- account_id comes back NULLABLE, not NOT NULL: the DROP COLUMN in Up threw
-- the values away, so there is nothing to backfill a NOT NULL constraint
-- with on a populated table -- the same limitation every DROP COLUMN in
-- this codebase's migrations has on Down (mirrors the pilot's own Down).
ALTER TABLE telemetry.vehicle_snapshots
    ADD COLUMN account_id UUID;

CREATE INDEX idx_vehicle_snapshots_vehicle_time
    ON telemetry.vehicle_snapshots (account_id, tesla_id, captured_at);

ALTER TABLE telemetry.vehicle_snapshots
    ADD CONSTRAINT vehicle_snapshots_account_tesla_date_unique
    UNIQUE (account_id, tesla_id, captured_date);
```

The table's `COMMENT ON TABLE`/`COMMENT ON COLUMN` text (there is none on
`vehicle_snapshots` beyond what earlier migrations set) and every historic
migration file are left untouched — `ai/go-conventions.md`'s "historic
migrations are never edited" precedent.

## Queries (`internal/telemetry/db/query.sql`)

- `InsertVehicleSnapshot`: drop `account_id` from the column list, the
  `VALUES` list, and the `ON CONFLICT` target (becomes
  `(tesla_id, captured_date)`); drop it from the `DO UPDATE SET` clause too
  (it was never in `SET` — the column list already omits it, nothing to
  change there beyond the conflict target).
- `SnapshotsByVehicleSince`, `SnapshotsByVehicleBetween`,
  `SnapshotsByVehicleUpdatedSince`, `SnapshotPrecedingDay`: drop
  `account_id` from the `SELECT` column list and the `WHERE` clause; keep
  `tesla_id` as the sole vehicle-identity predicate.
- `InsertPollAttempt`: rename the bound column from `account_id` to
  `polled_by_account_id`, and the parameter from `@account_id` to
  `@polled_by_account_id`. No other change — this query does not drop a
  parameter, it renames one.
- `LatestSnapshotsByAccount` → `LatestSnapshotsByVehicles`: drop the
  `WHERE account_id = @account_id` predicate; add
  `WHERE tesla_id = ANY(@tesla_ids::bigint[])`. `DISTINCT ON (tesla_id) ...
  ORDER BY tesla_id, captured_at DESC` is unchanged — it already produces one
  row per `tesla_id`, which is what a batch caller wants regardless of how
  many accounts those vehicles came from.

Every one of the five edited queries' doc comments currently cites
`design D1`/`D2`/`D3`/`D4`/`D5` from the now-archived, frozen
`telemetry-dedupe-daily-snapshots`/`RM8`/`RM29` design docs, or names the
constraint/index this migration retires. Replace each such citation with the
reason itself (`ai/go-conventions.md`'s code-comment rule; do not cite this
change's own decision IDs either — see `tasks.md` T2 for the exact wording
each comment needs).

Then run `sqlc generate` (`make sqlc`) so `internal/telemetry/db` regenerates
`InsertVehicleSnapshotParams`, `SnapshotsByVehicleSinceParams`,
`SnapshotsByVehicleBetweenParams`, `SnapshotsByVehicleUpdatedSinceParams`,
`SnapshotPrecedingDayParams`, `InsertPollAttemptParams`, and
`LatestSnapshotsByVehiclesParams` (new name) reflecting all of the above, and
`VehicleSnapshot`/`PollAttempt` (the generated row structs) reflecting the
dropped/renamed columns.

## Ports and implementation

### D-SNAPSHOT-FIELD — `Snapshot` drops its `AccountID` field

**Decision:** `Snapshot` (the domain type, `telemetry.go`) loses its
`AccountID uuid.UUID` field.

**Rationale — this is a compile requirement, not a style choice.** Once
`vehicle_snapshots.account_id` is dropped, sqlc's generated
`telemetrydb.VehicleSnapshot` row struct has no `AccountID` field to map
`rowToSnapshot` from, and no query supplies one on write. A `Snapshot` field
that can never be populated by any read and is never read by any write is
dead weight a future caller could mistake for live data — the same reasoning
the pilot's D-CHARGEGAP-FIELD used for `ChargeGap.AccountID`. The ticket's
own "Done when" gate ("`account_id` is gone from `vehicle_snapshots`")
requires this by plain reading, even though the dispatch's "Ports" section
named only the `Reader` methods explicitly.

**Consequence:** `snapshotFrom` (`service.go`) drops its `accountID
uuid.UUID` parameter — it becomes
`snapshotFrom(teslaID int64, capturedAt time.Time, loc *time.Location, data *tesla.VehicleDataTesla, raw []byte) Snapshot`.
`rowToSnapshot` (`mapping.go`) drops its `AccountID: r.AccountID,` line.
`dbStore.insertSnapshot` drops `AccountID: s.AccountID,` from the
`InsertVehicleSnapshotParams` literal it builds. `collectVehicle`/
`attemptVehicle`'s call to `snapshotFrom` drops the `v.AccountID` argument.

### D-ATTEMPT-FIELD — `Attempt.AccountID` renames to `Attempt.PolledByAccountID`

**Decision:** `Attempt`'s `AccountID uuid.UUID` field renames to
`PolledByAccountID uuid.UUID`, matching the renamed column.

**Rationale.** The ticket only asks for the SQL-level rename ("rename its
column only"), but leaving the Go field named `AccountID` after the column
becomes `polled_by_account_id` would make the domain type's name say
something the schema no longer does — exactly the kind of drift
`ai/go-conventions.md`'s AI-efficiency principle (self-describing names an
agent can trust without re-deriving) exists to prevent. This is a pure
rename, not a semantic change: `Attempt.AccountID`'s value has always been
"the account whose credentials made this attempt," which is precisely what
`PolledByAccountID` says.

**Consequence:** `record()` (`service.go`) builds
`Attempt{PolledByAccountID: accountID, ...}` instead of
`Attempt{AccountID: accountID, ...}`. `dbStore.insertPollAttempt` maps
`PolledByAccountID: a.PolledByAccountID,` (sqlc will generate
`InsertPollAttemptParams.PolledByAccountID` once the query parameter is
renamed). `query_log.go`'s `insertSnapshot`/`insertPollAttempt` log lines
that read `a.AccountID` switch to `a.PolledByAccountID`; the log line's text
label may keep saying `account=%s` (it is a free-text log label, not a
schema reference) or change to `polled_by_account=%s` — either is
acceptable, `tasks.md` leaves the exact string to the implementer.

### `Reader` interface changes (`telemetry.go`)

```go
SnapshotsByVehicleSince(ctx context.Context, teslaID int64, since time.Time) ([]Snapshot, error)
SnapshotsByVehicleBetween(ctx context.Context, teslaID int64, start, end time.Time) ([]Snapshot, error)
SnapshotsByVehicleUpdatedSince(ctx context.Context, teslaID int64, since time.Time) ([]Snapshot, error)
SnapshotPrecedingDay(ctx context.Context, teslaID int64, day time.Time) (*Snapshot, error)
LatestSnapshotsByVehicles(ctx context.Context, teslaIDs []int64) ([]Snapshot, error)
```

Each method's doc comment currently justifies its `account_id AND tesla_id`
filter as "defense-in-depth tenant isolation." That paragraph is removed from
all five (there is no `account_id` left to filter on); the rest of each
comment (window semantics, ordering, empty-result contract, index reuse) is
updated to match the new predicate but is otherwise unchanged in substance.
`LatestSnapshotsByVehicles`'s doc comment additionally states its new
contract: given a batch of `tesla_id`s (typically every vehicle one caller
cares about, not necessarily one account's), it returns the latest snapshot
per `tesla_id` present in the argument, in one query — the same
`DISTINCT ON` shape as before, just no longer scoped to a single account's
enumeration.

`store` (the unexported persistence seam, `service.go`) mirrors the same
five signature changes plus the `latestSnapshotsByAccount` →
`latestSnapshotsByVehicles(ctx, teslaIDs []int64)` rename.

### `reader.go` / `service.go` implementation

`reader.go`'s five methods become thin pass-throughs to the renamed/
resigned `store` methods (unchanged shape, just fewer/renamed parameters).
`dbStore`'s five methods (`service.go`) drop `accountID` from their
`telemetrydb.*Params` literals and from their own parameter lists;
`latestSnapshotsByVehicles` binds `TeslaIds: teslaIDs` (sqlc's
`ANY(@tesla_ids::bigint[])` binding — an `int64` slice — via whatever
sqlc/pgx type it generates for a bigint array parameter; confirm the exact
generated field name after `sqlc generate`, do not guess it here).

### `query_log.go` decorators

`loggingStore`'s three write methods (`insertSnapshot`, `insertPollAttempt`,
`upsertSuperchargerHistory`) and two silent pass-throughs among the four
renamed `store` read methods update their signatures to match; the
`insertSnapshot` log line drops `account=%s` (no longer has an `AccountID` to
log — it may log `tesla_id=%d` alone, already present). `insertPollAttempt`'s
log line switches to `a.PolledByAccountID` per D-ATTEMPT-FIELD.
`loggingReader`'s five methods update their signatures and log lines to
match the renamed/resigned `Reader` methods — `LatestSnapshotsByVehicles`'s
log line reports `tesla_ids=%v` (or similar) instead of `account=%s`.

## D-SCOPE — cross-module touches are mechanical, not a roadmap

Mirrors the pilot's own D-SCOPE exactly. The owning module is
`internal/telemetry`. `internal/analytics/recalculate.go` (three call
sites: `:104` `SnapshotsByVehicleBetween`, `:123` `SnapshotPrecedingDay`,
`:269` `SnapshotsByVehicleUpdatedSince`) and `internal/analytics/reader.go`
(`:136` `SnapshotsByVehicleSince`) change only because the `Reader` port's
signature changed — each call site drops the `accountID` argument it
currently passes as `telemetry`'s first real parameter, keeping its own
`teslaID` argument unchanged. `internal/analytics` makes no decision of its
own here and its own `Recalculate`/`Reconcile` keep their `accountID`
parameter (they still need it for the charge sources, unchanged by this
ticket — see `proposal.md`'s Non-goals). This does not make the change
cross-module in the roadmap sense.

Three analytics test files need the matching mechanical fixup (not new
tests): `internal/analytics/reader_test.go`,
`internal/analytics/recalculate_test.go`,
`internal/analytics/consumption_test.go` (per the dispatch's verified
facts) — plus one the dispatch did not name, found by this design (see
below).

### Correction to the dispatch: a fourth analytics file needs a fixup

`internal/analytics/reader_test.go:53` defines `fakeTelemetryReader`, a test
double assigned to a `telemetry.Reader`-typed field
(`internal/analytics/reader.go:50`, `telemetry telemetry.Reader`). Because
Go interface satisfaction requires every method, this fake must gain a
`LatestSnapshotsByVehicles(ctx context.Context, teslaIDs []int64)
([]telemetry.Snapshot, error)` method (mirroring its existing
`LatestSnapshotsByAccount` stub's `panic(...)` — it is never actually called,
same as today) or the analytics package fails to compile. This is the same
file the dispatch already lists for its `SnapshotsByVehicleSince` fixup, so
it is one file gaining a second, distinct edit — not a new file added to the
list.

## Test-fixup scope inside `internal/telemetry` (D-TESTFIX)

**Unit tests are excluded for this change** (owner's decision, per the
ticket). No new test file and no new test case testing new behavior is
written. But `go vet ./...` compiles every `_test.go` file, and the "Done
when" gate requires the build/vet stay clean — so every existing test
referencing a changed signature, a changed field, or the unexported `store`
interface must update to compile and keep asserting what it already
asserted, against the new shape.

**Correction to the dispatch: the real file list is nine files, not three.**
The dispatch's "Known affected test files" named `db_read_integration_test.go`,
`query_log_test.go`, `reader_test.go`. Verified by reading every telemetry
test file, the real list of files needing a fixup (all inside this module's
sandbox) is:

| File | What needs to change |
|---|---|
| `db_read_integration_test.go` | Builds `Snapshot{AccountID: ...}` (multiple sites); asserts `.AccountID` on returned snapshots; calls `SnapshotsByVehicleSince`/`Between`/`UpdatedSince`/`SnapshotPrecedingDay`/`LatestSnapshotsByAccount` with an `accountID` argument |
| `db_integration_test.go` | Builds `Snapshot{AccountID: ...}`; calls `insertSnapshot`/`insertPollAttempt` with account-scoped `Attempt{AccountID: ...}` |
| `db_preceding_snapshot_integration_test.go` | Builds `Snapshot{AccountID: ...}`; calls `SnapshotPrecedingDay`/`reader.SnapshotPrecedingDay` with an `accountID` argument |
| `db_sourcea_integration_test.go` | Builds `Snapshot{AccountID: ...}` |
| `db_tpms_integration_test.go` | Builds `Snapshot{AccountID: ...}` |
| `query_log_test.go` | Builds `Snapshot{AccountID: ...}` and `Attempt{AccountID: ...}`; `fakeQueryLogStore` implements all renamed/resigned `store` methods; calls `LatestSnapshotsByAccount` |
| `reader_test.go` | Builds `Snapshot{AccountID: ...}`; `fakeReadStore`, `fakeHistoryStore`, `fakeBetweenStore` each implement all five renamed/resigned `store` methods; calls `LatestSnapshotsByAccount`/`SnapshotsByVehicleSince`/etc. with an `accountID` argument |
| `service_test.go` | `fakeStore` implements all five renamed/resigned `store` methods; line ~404 asserts `got.AccountID != acctID` on a returned `Snapshot` — this assertion is deleted (the field no longer exists), not replaced, since `got.TeslaID` already covers vehicle identity in the same check |
| `snapshot_from_test.go` | Four `snapshotFrom(uuid.New(), ...)` calls drop the leading `uuid.New()` argument (D-SNAPSHOT-FIELD's `snapshotFrom` signature change) |

**Not affected** (verified, not assumed): `db_change_detection_integration_test.go`,
`db_change_detection_schema_test.go`, `db_supercharger_*_test.go` (four
files), `db_poll_run_integration_test.go`, `call_counter_test.go`,
`report_test.go`, `wake_test.go`, `testdb_test.go`, `dedupe_test.go` — every
`AccountID` in these files belongs to `SuperchargerHistory` (unaffected
table) or `account.OwnedVehicle` (a different module's type, unaffected by
this change).

This is still test-fixup, not new-behavior authoring: every remaining
assertion already existed and already passed before this change; only the
key used to express "which vehicle" narrows from `(account, vehicle)` to a
bare `tesla_id`, and `store`'s five interface methods change shape.

**Poll election gets no new test either — say why.** Unit tests are excluded
project-wide for this ticket, and `electPollingVehicles` is pure Go with no
DB dependency, so it would otherwise be the cheapest, most natural function
in this change to unit-test. It is deliberately not tested here because the
ticket's test-exclusion decision applies to the whole change, not only the
schema half — the implementer MAY still exercise it manually (a
`go run`/REPL check is not a checked-in test) before relying on it, but no
`_test.go` addition is planned or required by this design.

## Test contract (authored before implementation, per this project's
testing convention)

Concrete expected values for the migration's duplicate-collapse step:

**Given** two `vehicle_snapshots` rows both with `tesla_id = 3744325961659064`,
`captured_date = 2026-08-04`:
- Row X: `captured_at = 2026-08-04T03:31:00Z`, some `raw_data` payload A.
- Row Y: `captured_at = 2026-08-04T03:45:00Z`, some `raw_data` payload B.

**When** the migration's `DELETE` step runs.

**Then** Row X is deleted (earlier `captured_at`); Row Y survives with its
original `raw_data`, `id`, and every extracted column unchanged. After the
migration, `SELECT COUNT(*) FROM telemetry.vehicle_snapshots WHERE tesla_id =
3744325961659064 AND captured_date = '2026-08-04'` returns exactly 1, and
its `raw_data` equals payload B.

**Given** a database with no duplicate `(tesla_id, captured_date)` pairs (the
expected real-world state post-MAG-72).

**When** the migration runs.

**Then** the `DELETE` affects 0 rows, and every existing row's `id`,
`tesla_id`, `captured_at`, every extracted typed column, and `raw_data` are
byte-identical to before the migration — only `account_id` is gone and the
constraint/index names changed.

**Given** two `account.OwnedVehicle` candidates for the same `tesla_id`: one
with `AccessType = nil`, one with `AccessType = ptr("OWNER")`.

**When** `electPollingVehicles` runs.

**Then** the `OWNER` candidate is the one present in the result for that
`tesla_id`, regardless of the two candidates' relative `AccountID` order.

**Given** two `account.OwnedVehicle` candidates for the same `tesla_id`,
neither `OWNER` (`AccessType = ptr("DRIVER")` or `nil` on both), with
`AccountID`s `A` and `B` where `A < B` as raw bytes.

**When** `electPollingVehicles` runs.

**Then** the candidate with `AccountID = A` is the one present in the result.

**Given** a single `account.OwnedVehicle` candidate for a `tesla_id`, with
`AccessType = ptr("DRIVER")` (no `OWNER` candidate exists for this vehicle at
all).

**When** `electPollingVehicles` runs.

**Then** that `DRIVER` candidate is still present in the result — the
vehicle is never dropped from the elected set.

`Reader`'s own behavioral contract (window semantics, ordering, empty-result
handling) is unchanged by this ticket — only the identity key each method
filters on changes from `(account_id, tesla_id)` to `tesla_id` alone. The
test-fixup table above lists exactly which existing scenarios need updating
to the new key shape, not new scenarios.

## Docs

- `internal/telemetry/AGENTS.md` — the "Data ownership" table's
  `vehicle_snapshots` row currently reads "one row per (account, vehicle,
  `captured_date`)"; update to "one row per (vehicle, `captured_date`)".
  Update the "Rules that bind across all four" bullet naming
  `account_id`/`tesla_id` as "plain columns, no cross-module FK" to say the
  same about `tesla_id` alone for `vehicle_snapshots`
  (`supercharger_history` still carries `account_id` — do not touch that
  sentence). Add a short "Poll election" note under "Responsibility" or a
  new subsection: one account is elected to poll each vehicle before
  `CollectAll` groups by account (prefer OWNER, never skip) — point at
  `service.go`'s `electPollingVehicles` doc comment for the full rule rather
  than restating it (the AGENTS.md file is a map, not a copy).
- `openspec/specs/telemetry/spec.md` — this change's delta (see
  `specs/telemetry/spec.md` in this change folder) modifies: "Per-Vehicle
  Isolation And Attempt Recording" (an attempt's account id is no longer
  necessarily "the owning account" — it is the account that performed the
  poll), "Latest Snapshot Read Port" (method rename + scenarios),
  "Snapshot History Read Port" (drop account-scoping, both in the
  requirement text and its "Per-account and per-vehicle scoping" scenario),
  "Snapshot Updated-Since Read Port" and "Preceding-Snapshot Read Port"
  (drop "identify the vehicle by its account and its Tesla numeric id" →
  "by its Tesla numeric id alone"). It **adds** a new requirement, "Poll
  Account Election", carrying D-ELECT-1/D-ELECT-2's behavior as spec
  scenarios. It does **not** touch "Module-Scoped Database Schema" (a
  historical snapshot of an already-completed migration, per that
  requirement's own established precedent — same reasoning the pilot used
  for its own untouched historical scenario) or any Supercharger-session
  requirement (that table is unaffected by this change).
- **Ticket-vs-reality correction:** the ticket's own "Docs" section says
  `openspec/specs/telemetry/spec.md`'s "Same-day captures dedupe"
  requirement "names the old constraint." No such requirement exists in
  `openspec/specs/telemetry/spec.md` — grepped for `UNIQUE`, `account_id,
  tesla_id`, and `vehicle_snapshots_account_tesla_date_unique`, zero hits
  outside migration/query-comment files. The actual location is
  `kkpa/context/architecture/telemetry-ingest-only.md`'s "Conventions &
  gotchas" section, which carries a bullet literally titled **"Same-day
  captures dedupe"**: `` `UNIQUE (account_id, tesla_id, captured_date)`, latest
  wins... `` — update it to `` `UNIQUE (tesla_id, captured_date)` ``, keeping
  the rest of the sentence. This is the file the ticket meant; `tasks.md`
  targets the real location.

## Risks

- **Migration order matters only within this file** — the collapse `DELETE`
  must run before `DROP CONSTRAINT`/`ADD CONSTRAINT`, guaranteed by this
  migration's own statement order (goose runs one file's statements in the
  order written).
- **No cross-module migration ordering concern** — `vehicle_snapshots` and
  `poll_attempts` are read by no other module's migrations
  (`ai/go-conventions.md`'s only documented cross-module migration read is
  `telemetry` → `charging`, over `supercharger_history`, unrelated to these
  two tables).
- **Poll election deploys before the schema change reaches production** —
  the ticket's own non-negotiable order (elect → dedupe → swap constraint →
  drop column) means there is a window where election has already
  suppressed new duplicates but the old three-column constraint is still
  live. That is safe: the old `UNIQUE (account_id, tesla_id, captured_date)`
  constraint is a superset-permissive key (it would have allowed what the
  new key forbids, never the reverse), so election running under the old
  constraint never violates it — it just makes the later collapse migration
  find fewer or zero rows, exactly as intended.
- **`make migration-guard` / `make boundary-guard`** — unaffected by this
  change's shape (no new cross-module import, no schema file outside
  `internal/telemetry/db/migrations/`); run both as part of this change's
  own verification (`tasks.md`).
