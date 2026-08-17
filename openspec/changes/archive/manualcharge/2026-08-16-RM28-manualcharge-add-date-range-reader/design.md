## Context

Tier 3 of RM28 (`RM28-battery-derive-consumed-per-day`, `internal/battery`) computes, per
vehicle-day, how much battery the car actually consumed by correcting the raw
day-over-day delta for everything a charge put back in that day (roadmap D13):

```
battery_consumed_pct = battery_used_pct_calc + Σ(end_battery_pct − start_battery_pct)
```

The Σ sums across **every** charge event in the window, from **both** places the
platform stores charge records — `supercharger_sessions` (`internal/telemetry`) and
`manual_charge_entries` (`internal/manualcharge`, this module). Tier 1 already added
`telemetry.SuperchargerReader.SuperchargerSessionsByVehicleBetween` for the telemetry
side (archived `2026-08-16-RM28-telemetry-add-charge-gap-storage`). This tier adds the
symmetric method for `manualcharge.Reader`: `ListEntriesByVehicleBetween`.

The module's existing `Reader` has two methods, both limit-based ("most recent N"):
`ListEntriesByVehicle` and `ListEntriesByAccount`. Neither can safely serve tier 3.
Tier 3 needs *every* entry whose `charged_on` falls in a caller-chosen window (typically
~90 days per roadmap D2); a limit-based call has no safe number to guess — pick too low
and a heavy month silently truncates, understating consumption without any error or
signal. A date-range method with no limit is the honest contract: the window itself is
the bound (roadmap D9).

## Goals / Non-Goals

**Goals**
- Add `ListEntriesByVehicleBetween(ctx, accountID, teslaID, from, to time.Time) ([]Entry, error)`
  to `manualcharge.Reader`, filtering on `charged_on` inclusive of both `from` and `to`
  (roadmap D9, D12).
- Reuse the existing `idx_manual_charge_entries_vehicle_time` index as-is — no schema
  change, no new index.
- Keep the new method's shape (ordering, empty-slice convention, error wrapping)
  consistent with the module's two existing Reader methods, so a future reader of
  `service.go` has one convention to learn, not three.

**Non-Goals**
- No change to `ListEntriesByVehicle`, `ListEntriesByAccount`, or the `Writer` port.
- No change to `manual_charge_entries`' schema, columns, or existing indexes.
- No derivation, gap-detection, or matching logic — that is `internal/battery`, tier 3.
- No `cmd/poller` wiring — tier 3.
- No gateway change — tier 4.
- No caller-side lookback/window-shifting logic (e.g. the telemetry-side `[start−1,
  end]` lookback in roadmap D9a). D9a is specific to `internal/battery`'s own snapshot
  fetch and belongs entirely to tier 3; this module's contract is the plain, literal
  `[from, to]` the caller supplies, both bounds inclusive.

## Design Decisions

### D1 (roadmap D9) — No `limit` parameter on the new method

`ListEntriesByVehicleBetween` takes no `limit`. The existing two Reader methods are
limit-based by design (bounded "give me the most recent page" pattern for a dashboard
list); this method is date-bounded instead — the caller-supplied `[from, to]` window
*is* the safety bound. Adding a `limit` on top would either be redundant (large enough
to never trigger) or dangerous (small enough to silently truncate a real window, exactly
the failure mode this method exists to avoid). Matches the sibling telemetry method
`SuperchargerSessionsByVehicleBetween`, which is also limit-free (tier 1, already
shipped).

### D2 (leader decision) — Ordering: `charged_on DESC`

The new method returns rows ordered `charged_on DESC`, identical to
`ListEntriesByVehicle`. Two reasons, in order of weight:

1. **The covering index is already sorted that way.** `idx_manual_charge_entries_vehicle_time
   (account_id, tesla_id, charged_on DESC)` means `ORDER BY charged_on DESC` is free —
   the index scan itself returns rows in that order, no separate sort step. Reversing
   to ASC would force a sort (or require flipping the index, which nothing else needs).
2. **Consistency over convenience for one caller.** Tier 3 (`internal/battery`) builds a
   keyed map (`charged_on` → summed delta) from the result, not an ordered list — it does
   not care about the order it receives rows in. Optimizing for that consumer's
   convenience would mean the module's three Reader methods disagree with each other for
   no one's actual benefit. `ai/go-conventions.md`'s AI-efficiency principle favors the
   closed, small vocabulary: one ordering convention across the module beats a
   caller-specific exception a future reader has to notice and remember.

### D3 — No schema or index change; the existing index already covers the query

**Explicit statement (required by the project's design rule for any DB-touching
change, even when the `database` design gate does not trip): this change adds no
migration, no table, no column, and no index.**

The query this method issues is:

```sql
SELECT * FROM manual_charge_entries
WHERE account_id = @account_id
  AND tesla_id = @tesla_id
  AND charged_on BETWEEN @from_date AND @to_date
ORDER BY charged_on DESC;
```

`idx_manual_charge_entries_vehicle_time (account_id, tesla_id, charged_on DESC)`
(`internal/manualcharge/db/migrations/20260718000001_add_manual_charge_entries.sql:64-65`)
already covers this exactly:

- `account_id = @account_id` — equality on the leading column.
- `AND tesla_id = @tesla_id` — equality on the second column, still within a single
  index range.
- `AND charged_on BETWEEN @from_date AND @to_date` — a range predicate on the third
  (and final) index column. Postgres executes this as one `Index Scan` (or
  `Index Only Scan` if `RETURNING *` is not needed elsewhere — it is here, since the
  query selects `*`, so it is a plain `Index Scan` with a heap fetch) that seeks
  directly to `(account_id, tesla_id, from_date)` and walks forward to
  `(account_id, tesla_id, to_date)`.
- `ORDER BY charged_on DESC` — satisfied by the index's own `DESC` column order within
  the walked range; no separate sort node.

This is the textbook case an `(a, b, c DESC)` composite index exists for: two leading
equality predicates plus a range/order on the trailing column, in one scan, no sort. The
same index already serves `ListEntriesByVehicle`'s `WHERE account_id = ? AND tesla_id =
? ORDER BY charged_on DESC LIMIT ?` today — this method is the same access pattern with
the unbounded scan replaced by a bounded range, which only makes the scan cheaper, never
more expensive. No index reshaping, no second index, nothing to add.

**Why this matters for the design gate:** the project's `Design-Gates: database`
requires the user's explicit confirmation before Apply *when* a change's artifacts
propose a schema or index change. This design concludes none is needed — the gate does
not trip. Per the leader's dispatch instructions, if implementation later revealed this
conclusion was wrong, the correct move is to stop and report back for the user's
sign-off, not to write a migration unilaterally. No such reversal occurred during this
design pass.

### D4 — Non-nil empty slice on no match

Matches both existing Reader methods and the module's `AGENTS.md` contract ("Both
methods return a non-nil empty slice when no entries exist"). `ListEntriesByVehicleBetween`
extends that guarantee rather than introducing a third convention: implemented via
`make([]Entry, 0, len(rows))`, identical to the existing two methods' implementation
pattern in `service.go`.

### D5 — `charged_on BETWEEN` is exact for a DATE column; no half-open bound trick needed

The sibling telemetry method (`SuperchargerSessionsByVehicleBetween`) filters on a
`TIMESTAMPTZ` column (`charge_stop_date_time`) and therefore has to translate its
inclusive end-day bound into a half-open `>= start AND < end+1day` predicate in Go, to
avoid excluding stop times after `end`'s first instant. `manual_charge_entries.charged_on`
is a plain `DATE` column — it carries no time-of-day component to lose. A direct SQL
`BETWEEN @from_date AND @to_date` is already exactly inclusive of both calendar days,
with no off-by-one risk and no Go-side bound arithmetic required. This is simpler than
the telemetry precedent by construction of the column type, not by a different design
choice — see roadmap D12: "Manual entries match on `charged_on` (date)" is precisely the
reason this column is a `DATE`, not a `TIMESTAMPTZ`.

`from`/`to` are accepted as `time.Time` (matching the `Reader` port's existing
`ChargedOn time.Time` field and the sibling telemetry signature) and converted at the DB
boundary via the module's existing `dateFromTime` helper (`service.go`) — the same
helper `Create`/`Update` already use to build `pgtype.Date` params. No new conversion
helper is needed.

### D6 — sqlc param naming: `FromDate` / `ToDate`

The generated `ListEntriesByVehicleBetweenParams` struct fields are named `FromDate` and
`ToDate` (from `@from_date` / `@to_date` in the SQL), not `From`/`To` — sqlc titles Go
field names directly from the named parameter, and `_date` on both makes the generated
struct self-documenting at the call site (`params.FromDate`, not the more ambiguous
`params.From`) without opening `query.sql` to check. Small, one-time naming choice with
no behavioral effect; recorded so implementers don't bikeshed it mid-task.

## Schema

**No migration in this change.** For reference, the existing schema object this method
depends on (unchanged, already live since `20260718000001_add_manual_charge_entries.sql`):

```sql
CREATE INDEX idx_manual_charge_entries_vehicle_time
    ON manual_charge_entries (account_id, tesla_id, charged_on DESC);
```

### Index plan

| Query | Index used | Scan type | New index? |
|---|---|---|---|
| `ListEntriesByVehicleBetween` (new) | `idx_manual_charge_entries_vehicle_time (account_id, tesla_id, charged_on DESC)` | Index Scan, range on `charged_on` within the `(account_id, tesla_id)` equality prefix; `ORDER BY` free | No |

No other query in this module is touched, so no other row in an index plan changes.

## Go-Level Surface

### `Reader` port addition (`manualcharge.go`)

```go
// Reader is the read port shaped for dashboard access patterns.
// Both methods return a non-nil empty slice when no entries exist.
// limit = 0 uses a server default (100).
type Reader interface {
	ListEntriesByVehicle(ctx context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]Entry, error)
	ListEntriesByAccount(ctx context.Context, accountID uuid.UUID, limit int) ([]Entry, error)

	// ListEntriesByVehicleBetween returns entries for a specific vehicle within an
	// account whose charged_on falls within [from, to], inclusive of both bounds
	// (design D5, roadmap D9/D12). Ordered charged_on DESC, matching
	// ListEntriesByVehicle (design D2). Always returns a non-nil empty slice when no
	// rows match (design D4). No limit parameter (design D1, roadmap D9) — the
	// [from, to] window itself bounds the result.
	ListEntriesByVehicleBetween(ctx context.Context, accountID uuid.UUID, teslaID int64, from, to time.Time) ([]Entry, error)
}
```

### `service.go` additions

`store` interface gains one method:

```go
type store interface {
	// ... existing five methods unchanged ...
	listEntriesByVehicleBetween(ctx context.Context, params manualchargedb.ListEntriesByVehicleBetweenParams) ([]manualchargedb.ManualChargeEntry, error)
}
```

`dbStore` gains the corresponding one-line delegation (mirrors the five existing
`dbStore` methods exactly):

```go
func (d *dbStore) listEntriesByVehicleBetween(ctx context.Context, params manualchargedb.ListEntriesByVehicleBetweenParams) ([]manualchargedb.ManualChargeEntry, error) {
	return d.q.ListEntriesByVehicleBetween(ctx, params)
}
```

`readerService` gains the port implementation (mirrors `ListEntriesByVehicle`'s shape
exactly, minus the limit default):

```go
func (r *readerService) ListEntriesByVehicleBetween(ctx context.Context, accountID uuid.UUID, teslaID int64, from, to time.Time) ([]Entry, error) {
	params := manualchargedb.ListEntriesByVehicleBetweenParams{
		AccountID: accountID,
		TeslaID:   teslaID,
		FromDate:  dateFromTime(from),
		ToDate:    dateFromTime(to),
	}

	rows, err := r.store.listEntriesByVehicleBetween(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("manualcharge: list entries by vehicle between: %w", err)
	}

	entries := make([]Entry, 0, len(rows))
	for _, row := range rows {
		e, err := rowToEntry(row)
		if err != nil {
			return nil, fmt.Errorf("manualcharge: mapping entry row: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, nil
}
```

No change to `rowToEntry`, `dateFromTime`, or any other mapping helper — all reused
as-is. `writerService`, `Writer`, and every Create/Update/Delete path are untouched.

### `db/query.sql` addition

```sql
-- name: ListEntriesByVehicleBetween :many
-- Return entries for a specific vehicle within an account whose charged_on falls
-- within [@from_date, @to_date], inclusive of both bounds, ordered newest charged
-- day first. Uses idx_manual_charge_entries_vehicle_time (account_id, tesla_id,
-- charged_on DESC) as a single index range scan: account_id and tesla_id prune to
-- the tenant and vehicle, charged_on BETWEEN walks the range, and the DESC column
-- order satisfies ORDER BY with no separate sort step (design D3). No LIMIT: the
-- caller-supplied [from, to] window is the safety bound, not a row count
-- (design D1, roadmap D9).
SELECT * FROM manual_charge_entries
WHERE account_id = @account_id
  AND tesla_id = @tesla_id
  AND charged_on BETWEEN @from_date AND @to_date
ORDER BY charged_on DESC;
```

sqlc infers `@from_date` and `@to_date` as `pgtype.Date` from the `charged_on` column's
type (`DATE`), matching `dateFromTime`'s return type — no override needed in
`sqlc.yaml`.

## Test Contract (authored before implementation, binding)

All cases below are DB-integration tests (`db_integration_test.go`,
`DATABASE_URL`-gated / testcontainers-provisioned per the module's existing `TestMain`).
Naming follows the module's existing `TestListByVehicle_*` convention:
`TestListByVehicleBetween_*`.

### (a) Both bounds are inclusive

- **Setup:** one vehicle `V`, account `A`. Create three entries with `charged_on` =
  `2026-07-10` (the intended `from`), `2026-07-15` (mid-window), `2026-07-20` (the
  intended `to`).
- **Call:** `ListEntriesByVehicleBetween(ctx, A, V, 2026-07-10, 2026-07-20)`.
- **Expected:** all three entries are returned. The entry dated exactly `2026-07-10`
  (`from`) is present. The entry dated exactly `2026-07-20` (`to`) is present.

### (b) An entry one day outside each bound is excluded

- **Setup:** same vehicle/account as (a), plus two more entries: `charged_on =
  2026-07-09` (one day before `from`) and `charged_on = 2026-07-21` (one day after
  `to`).
- **Call:** `ListEntriesByVehicleBetween(ctx, A, V, 2026-07-10, 2026-07-20)`.
- **Expected:** exactly the same 3 entries as (a) — length 3. Neither the `07-09` nor
  the `07-21` entry appears in the result.

### (c) Ordering: `charged_on DESC`

- **Setup:** same 3-entry window as (a) (`07-10`, `07-15`, `07-20`), inserted in
  non-sorted creation order.
- **Call:** `ListEntriesByVehicleBetween(ctx, A, V, 2026-07-10, 2026-07-20)`.
- **Expected:** `result[0].ChargedOn = 2026-07-20`, `result[1].ChargedOn = 2026-07-15`,
  `result[2].ChargedOn = 2026-07-10` — strictly non-increasing by `ChargedOn` across the
  slice (same assertion style as `TestListByVehicle_NewestFirst`).

### (d) Empty-result shape

- **Setup:** account `A`, vehicle `V` with zero entries in the queried window (either no
  entries at all, or entries only outside `[from, to]`).
- **Call:** `ListEntriesByVehicleBetween(ctx, A, V, from, to)` for a window containing no
  matching rows.
- **Expected:** `err == nil`; `result != nil` (non-nil); `len(result) == 0`.

### (e) Multi-tenant isolation

- **Setup:** two accounts `A` and `B`, same `teslaID = V` value used under both (Tesla
  IDs are not globally unique across accounts in this schema — the isolation must come
  from `account_id`, not from `tesla_id` alone). Account `A` has an entry at
  `charged_on = 2026-07-15`; account `B` has an entry at the same `teslaID = V` and the
  same `charged_on = 2026-07-15`.
- **Call:** `ListEntriesByVehicleBetween(ctx, A, V, 2026-07-01, 2026-07-31)`.
- **Expected:** exactly 1 entry returned, and it is account `A`'s entry. Account `B`'s
  entry — despite matching `teslaID` and falling inside the same date window — never
  appears.

### (f) Vehicle isolation

- **Setup:** one account `A` with two vehicles `V1` and `V2`. Both have an entry at
  `charged_on = 2026-07-15`, inside the queried window.
- **Call:** `ListEntriesByVehicleBetween(ctx, A, V1, 2026-07-01, 2026-07-31)`.
- **Expected:** exactly 1 entry returned, with `TeslaID == V1`. `V2`'s entry — despite
  belonging to the same account and falling inside the same date window — never appears.

## Migration Plan (implementation order for the worker)

No dependency ordering complexity — this is a single-file-cluster additive change with
no schema step:

1. `manualcharge.go` — add the `ListEntriesByVehicleBetween` method to `Reader` (D1–D2,
   D4–D5).
2. `db/query.sql` — add the `ListEntriesByVehicleBetween` query (D3, D5, D6). Regenerate
   with `sqlc generate` (or `make sqlc`) to produce
   `manualchargedb.ListEntriesByVehicleBetweenParams` and the query method.
3. `service.go` — add the `store` interface method, the `dbStore` delegation, and the
   `readerService` implementation (depends on step 2's generated types existing).
4. `db_integration_test.go` — add the six test-contract cases above (depends on steps
   1–3 compiling).
5. `AGENTS.md` — update the module's `## Public Interface` code block to include the new
   method (docs-track-change rule, `CLAUDE.md`).

Steps 1 and 2 have no dependency on each other and are parallel-safe (disjoint files).
Step 3 depends on both. Step 4 depends on 1–3. Step 5 depends on 1 (the interface must be
final before the doc snippet is copied).

## Risks / Trade-offs

- **None identified beyond the standard additive-method risk profile.** No existing
  behavior changes, no schema changes, no new index to validate against production data
  volume. The only genuinely new code path is the query itself, and its plan is a
  reuse of an index and access pattern already proven by `ListEntriesByVehicle` in
  production use.
- **Deferred, not a risk of this change:** the "charging data split across two modules"
  concern the roadmap raised (every consumer of "how was this car charged" composes two
  ports) is explicitly out of scope here and tracked as a backlog entry — this tier does
  not attempt to unify the two read surfaces.
