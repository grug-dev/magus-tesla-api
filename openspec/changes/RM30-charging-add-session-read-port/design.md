# Design — RM30-charging-add-session-read-port

> Numbering note: this document's decisions are **D1, D2, …**, scoped to this change only —
> distinct from the roadmap's own **D1–D4** in
> `openspec/roadmaps/RM30-supercharger-stats-read-from-charging.progress.json` (cited as
> **roadmap D1**, …) and from RM29 tier 6's archived decisions (cited as **RM29 D1**, …).
> **D1–D3 transcribe binding instructions from the leader's dispatch** (which itself
> transcribes the roadmap's tier-1 proposal prompt and roadmap D1/D3); each says so in its
> heading. **D4–D7 are decisions this artifacts pass had to make** to turn those into a
> buildable change.

## Context

Four facts about the existing schema and code shape everything below.

1. **`charge_sessions` and its index already exist; only the reader is missing.** RM29
   tier 6 shipped the table, `SessionWriter`/`SessionMirror` (write-only), and
   `idx_charge_sessions_vehicle_stop (account_id, tesla_id, charge_stop_date_time)` —
   built and left unused on purpose, "prepared, not built" for exactly the read this
   change adds (RM29 proposal.md §"Read paths affected"). RM29 design.md D9 declined to
   build a reader because it would have had no caller; this tier has one — tier 2's
   gateway swap (roadmap tiers table).

2. **The sibling port to mirror already exists in this same module.**
   `Reader.ListEntriesByVehicleBetween` (`charging.go`) is the established shape for a
   bounded-window, per-vehicle, no-limit read in this module: inclusive on both bounds,
   ordered by its date column, always a non-nil empty slice on no match. This change's
   dispatch is explicit that `SessionReader`'s new method must mirror that contract
   "exactly" — this design's job is to say precisely where it does and does not (D3, D5).

3. **`tesla_id` is nullable on `charge_sessions`, and this project already has a resolved
   precedent for filtering it by equality.**
   `telemetry.SuperchargerSessionsByVehicleBetweenParams.TeslaID` is `pgtype.Int8` even
   though that method's Go signature takes a plain `teslaID int64` — sqlc generates a
   nullable param type because the underlying `supercharger_sessions.tesla_id` column is
   nullable, regardless of the query's equality shape. `charge_sessions.tesla_id` is
   nullable for the identical reason (RM29's mirror inherits it from telemetry, RM29
   design.md D3), so `chargingdb.ListSessionsByVehicleBetweenParams.TeslaID` will be
   `pgtype.Int8` too — not the plain `int64` a first glance at `MirrorChargeSessionParams`
   (which has no such filter) might suggest. `internal/telemetry/reader.go`'s
   `teslaIDToPgInt8` helper is the exact precedent this change's mapping reuses the name
   and shape of (D6).

4. **`SessionMirror` and any future reader type are deliberately different types.** RM29
   design.md D6 made the write port percentage-free by construction: `SessionMirror` has
   no field for the five battery-percentage columns, so the nightly sync cannot bind one
   even if a future edit tried — "a compile error rather than a comment a reviewer has to
   notice." The dispatch for this change is explicit that `Session` must not be built by
   widening `SessionMirror`; this design confirms why and makes the type boundary
   explicit (D4).

## Goals / Non-Goals

**Goals**

- `internal/charging` exposes a read port over `charge_sessions` shaped for a bounded,
  per-vehicle window — the same shape `Reader.ListEntriesByVehicleBetween` already
  established for `manual_charge_entries` (**D3**).
- The read is served entirely by `idx_charge_sessions_vehicle_stop`, the index RM29
  tier 6 already built for it — no migration, no new index (**D1**, **D2**).
- The returned domain type carries every column, including the charging-owned
  battery-percentage verification fields — the whole point of reading `charging` instead
  of `telemetry` (**D4**).
- The write path's percentage-free compile-time protection (RM29 D6) is untouched: this
  change adds no field to `SessionMirror` and no path from `Session` back into the mirror
  (**D4**).

**Non-Goals**

- Any database migration, index, column, or constraint. Roadmap D1 already decided the
  page drops `CountryCode`/`BillingType` rather than adding them to `charge_sessions`,
  which is what keeps this tier schema-free (**D2**).
- Wiring anything into `cmd/web` or `internal/gateway`. Tier 2 is the consumer; this tier
  has no caller of its own new port.
- Any change to `SessionWriter`, `SessionMirror`, or `MirrorChargeSession`. The write path
  is not touched.
- Value-receiver derived methods on `Session` (e.g. a `Session`-side `BatteryDelta`
  mirroring `Entry`'s). Not asked for by the dispatch or by tier 2's declared read
  pattern; can be added later without touching the port's signature.

---

## Decisions

### D1 — Window on `charge_stop_date_time`; no new index (binding — dispatch/roadmap D1)

`ListSessionsByVehicleBetween` filters and orders on `charge_stop_date_time`, **not**
`charge_start_date_time`. This is not a fresh call — RM29 roadmap D12 already established
that a session belongs to the window containing its stop instant (energy is fully
delivered at session stop, which is what `end_battery_pct` corresponds to), and RM29 tier
6 already built `idx_charge_sessions_vehicle_stop` on `(account_id, tesla_id,
charge_stop_date_time)` — leading with stop-time **specifically and only** so a future read
like this one gets a pure index range scan with no sort step. Filtering on
`charge_start_date_time` instead would make that index useless for this query (its
trailing column would no longer match the range predicate) and would force either a new
index or an in-memory sort — exactly the situation `telemetry`'s own
`idx_supercharger_sessions_vehicle_time` is in today (start-time-leading, and its own doc
comment records that it does not fully serve a stop-time query). See §"Database Changes"
for the concrete `EXPLAIN`-level justification.

**Consequence for tier 2:** the gateway's month-window read shifts from filtering
`ChargeStartDateTime >= since` (its current in-memory behavior over
`telemetry.SuperchargerReader`) to filtering on stop-time via this port. Roadmap D3
already named this consequence: "minor behavioural shift for sessions that straddle a
month boundary... flagged for review rather than escalated." This design does not
re-decide it — it is transcribed here so tier 2's implementer does not have to
re-derive it from the roadmap file.

### D2 — No database object is created or altered (binding — dispatch/roadmap D1)

This change ships zero migrations. `internal/charging/db/migrations/` gains no new file,
and `20260823000001_add_charge_sessions.sql` is not touched. The existing
`idx_charge_sessions_vehicle_stop` is sufficient for the query this tier adds (proven in
§"Database Changes"). Per the dispatch's explicit instruction: **if this design had
concluded a new index or any other database object were needed, that conclusion would be
reported as blocked rather than implemented**, because it would trip the project's
`database` design gate (`CLAUDE.md` §Pipeline config → Design-Gates), which requires the
owner's explicit sign-off before Apply. That conclusion was not reached — see the index
proof below — so this change proceeds without a gate confirmation step.

### D3 — Ascending order, distinct from `Reader`'s descending order, matching `telemetry`'s dated-read convention (binding — dispatch; strengthened on revision)

`ListSessionsByVehicleBetween` returns rows **ascending** by `charge_stop_date_time` (ASC,
oldest-first) — matching `idx_charge_sessions_vehicle_stop`'s own creation order (see the
migration file: "ASC (no DESC): the read is an ascending, oldest-first bounded window, so
an ascending index satisfies both the range predicate and the ORDER BY with no sort step
and no backward scan"). This **differs** from `Reader.ListEntriesByVehicleBetween`, which
returns `charged_on DESC` (newest first), because `idx_manual_charge_entries_vehicle_time`
was built DESC-leading for that table's own dashboard-newest-first access pattern (RM29's
sibling design). The two ports share every other contract element — inclusive bounds, no
limit, non-nil empty slice — but not sort direction, because sort direction is not a
port-family convention here, it is a property of whichever index each table happens to
carry. **This divergence is deliberate and must not be "fixed" into DESC** by a future
reader who assumes the two `…Between` methods on the same module should match exactly;
doing so would force a sort step on every call. The port's own doc comment states this
explicitly (task list, Wave 2).

**ASC is not a local oddity — it matches `telemetry.SuperchargerSessionsByVehicleBetween`
exactly**, which is also oldest-first ascending by `charge_stop_date_time`, over the
identically-shaped `idx_supercharger_sessions_vehicle_time`-adjacent access pattern for a
bounded dated read. With D5's revision below (this port now taking the same whole-day,
half-open-range `?start=&end=` contract telemetry's method already serves), the two
methods are not just independently ASC — they are the **same shape twice**: same
column filtered, same bound semantics, same sort direction. ASC is therefore the
platform's convention for a bounded dated-window read on a stop-time-indexed table, not
an accident of this table's particular index. A future third such read (any module,
any table) should default to ASC/half-open/stop-time unless it has a documented reason
not to.

### D4 — `Session` is a new type, not a widened `SessionMirror`

```go
// Session is the full domain representation of one charge_sessions row: identity, the
// session's time window, the session facts internal/telemetry collects, and the five
// charging-owned battery-percentage verification/estimate columns. Read-only counterpart
// to SessionMirror — NOT built by adding fields to it.
//
// SessionMirror stays deliberately percentage-free (RM29 design.md D6): a nightly sync
// that took a Session instead of a SessionMirror would have a field to bind a
// human-verified percentage to, defeating the compile-time protection that is RM29 tier
// 6's central invariant. Session and SessionMirror are separate types for exactly that
// reason, even though Session's first thirteen fields duplicate SessionMirror's eleven.
type Session struct {
	ID        uuid.UUID
	AccountID uuid.UUID
	VIN       string
	TeslaID   *int64 // nil when the VIN is not a currently-registered vehicle
	SessionID int64

	ChargeStartDateTime time.Time
	ChargeStopDateTime  time.Time

	SiteLocationName string
	EnergyKWh        *float64 // nil when the session had no kWh fee
	TotalCost        *float64 // nil when the session had no fees
	Currency         *string  // nil when the session had no fees
	IsPaid           *bool    // nil when the session had no fees

	// Charging-owned verification channel (RM29 design.md D1/D5/D6). Never written by
	// the nightly sync — SessionWriter has no field for any of these five.
	StartBatteryPct    *int    // 0-100 inclusive; nil = nothing recorded
	EndBatteryPct      *int    // 0-100 inclusive; nil = nothing recorded
	BatteryPctSource   *string // "user_verified" or "polled"; nil iff both percentages are nil
	StartBatteryPctEst *int    // frozen snapshot at verification time; nil = nothing recorded
	EndBatteryPctEst   *int    // frozen snapshot at verification time; nil = nothing recorded

	CreatedAt time.Time
	UpdatedAt time.Time
}
```

Nineteen fields, one per `charge_sessions` column (the table has 19 columns including
`id`). Field names and types follow this module's existing conventions exactly:
`*T` for every nullable column (matching `Entry`'s pattern), `time.Time` for every
`TIMESTAMPTZ`, `int64`/`*int64` for `BIGINT`/nullable `BIGINT`, `*int` for nullable
`SMALLINT` (matching `Entry.StartBatteryPct`'s identical type), `*string` for nullable
`TEXT`. No `pgtype` anywhere in this type (`ai/architecture.md` §2, RM29 D6's own rule
applied to the read side).

**Rejected — reuse `SessionMirror` by adding the five percentage fields to it.** This is
exactly what RM29 tier 6 built `SessionMirror` to prevent. Widening it would mean the
*write* port (`SessionWriter.MirrorSessions`, unchanged by this tier) suddenly has fields
it must not bind — the compiler no longer catches a future mis-edit that tries to write a
percentage from the sync path, which was the entire point of RM29 D6. The two types
sharing thirteen field names is an acceptable duplication cost against that guarantee,
exactly as the dispatch instructs.

### D5 — Inclusive of the whole end calendar day, translated in Go, mirroring `telemetry.SuperchargerSessionsByVehicleBetween` exactly (revised — see revision note below)

> **Revised after Apply, before implementation.** The original version of this decision
> assumed tier 2's caller would pass exact instants and rejected calendar-day
> translation on that basis. The owner subsequently brought tier 2's endpoint into
> compliance with the project's existing HTTP date-filter convention
> (`internal/gateway/AGENTS.md` §"HTTP date-filter convention", `ai/go-conventions.md`
> §"Read optimization") instead of the ad hoc `since`/`now` shape this design first
> assumed — so the caller now passes `?start=YYYY-MM-DD&end=YYYY-MM-DD`, both whole UTC
> calendar days, `end` inclusive. That makes the original premise false, and the
> decision is replaced below rather than left standing. No other decision in this
> document changes.

`from` and `to` are two whole UTC calendar days, `to` inclusive. `to` is translated to a
half-open upper bound **in Go**, exactly as `telemetry.SuperchargerSessionsByVehicleBetween`
already does — the case this design originally cited only as a contrast is now the
mechanism to copy:

```go
endBound := to.AddDate(0, 0, 1)
```

The SQL becomes half-open, not `BETWEEN`:

```sql
charge_stop_date_time >= @from_time AND charge_stop_date_time < @end_bound
```

**Why day-semantics is correct here now.** The caller is an HTTP endpoint bound to the
documented `?start=&end=` contract: `end` parses to UTC midnight of the given calendar
day, so a plain `BETWEEN @from_time AND @to_time` would silently drop every session that
stopped later that same day (e.g. 14:00) — exactly the off-by-a-day trap the convention's
`end`-inclusive rule exists to prevent, and exactly what a plain `BETWEEN` on a
`TIMESTAMPTZ` bound to a midnight value produces. Translating `end` into `end+1 day` and
comparing with `<` closes that gap deterministically: every instant of the `to` calendar
day is included, and the boundary is expressed once, in Go, at the one place a caller's
raw date input becomes a timestamp — not left for every caller to remember to add a day
itself.

**This makes `charging`'s and `telemetry`'s dated session reads behave identically — one
convention across both modules, not two.** Before this revision, this port would have been
the *second* dated-window shape in the codebase (instant-`BETWEEN`, next to telemetry's
day-half-open); after it, both modules' `SuperchargerSessionsByVehicleBetween` and
`ListSessionsByVehicleBetween` methods share the same column (`charge_stop_date_time`),
the same bound semantics (whole UTC calendar days, `end` inclusive, translated to a
half-open range in Go), and the same sort direction (D3). A future third dated-session
read has exactly one pattern to copy, not two to choose between — the closed-vocabulary
instance the AI-efficiency rule asks for.

**`Reader.ListEntriesByVehicleBetween` is unaffected and stays a plain `BETWEEN`** — its
`charged_on` column is `DATE` (day-granularity by construction, no midnight ambiguity to
translate away), and it predates the HTTP date-filter convention's gateway-endpoint
contract. This port does not retroactively change that method; the two ports' bound
mechanics differ because their columns' granularities differ, not because one is more
"correct" than the other.

**Parameter names are unchanged: `from, to`.** Renaming them (e.g. to `start, end`) was
considered and rejected, to keep this method's Go signature consistent with
`ListEntriesByVehicleBetween`'s `from, to time.Time` within the same module — intra-module
naming consistency outweighs mirroring the HTTP query-param names of one particular
caller. The day-inclusive, half-open-translated semantics are documented in the method's
doc comment instead of being implied by a param rename.

### D6 — `tesla_id` filtering: nullable equality, reused conversion pattern

`ListSessionsByVehicleBetweenParams.TeslaID` is `pgtype.Int8` (sqlc infers this from
`charge_sessions.tesla_id BIGINT`'s nullability, not from the query's equality shape — see
Context fact 3, and `SuperchargerSessionsByVehicleBetweenParams.TeslaID` as the
already-verified precedent in this codebase for the identical situation). The mapping
adds one new forward helper, named and shaped after telemetry's own:

```go
// teslaIDToPgInt8 maps a plain int64 vehicle id to a valid pgtype.Int8 filter param.
// Named and shaped after telemetry.teslaIDToPgInt8 (internal/telemetry/reader.go) — the
// identical situation: a NOT NULL Go parameter filtering a nullable BIGINT column.
func teslaIDToPgInt8(teslaID int64) pgtype.Int8 {
	return pgtype.Int8{Int64: teslaID, Valid: true}
}
```

**Consequence, worth stating so no test "fixes" it:** because SQL `NULL = value` is
neither true nor false (it is unknown, hence excluded by `WHERE`), a session whose
`tesla_id` is `NULL` — the VIN is not a currently-registered vehicle of the account — is
**never** returned by this method, for any `teslaID`. This is correct, not a gap: a
vehicle-scoped read is, definitionally, about a currently-resolvable vehicle id, and an
orphaned session (RM29's own contract: retained forever, never deleted) is simply outside
the scope a `teslaID`-keyed read can name. This is the exact same behavior
`SuperchargerSessionsByVehicleBetween` already has over the analogous nullable column in
`supercharger_sessions`, so it introduces no new class of behavior into the codebase.

The read side also needs the reverse of the three pointer→pgtype helpers RM29 tier 6 added
to `session_writer.go`, which today each end with "no reverse pair exists — this module
exposes no reader for `charge_sessions`" — a sentence this change makes false. The
reverses are added **next to their forward pairs in `session_writer.go`** (not a new
file), following this module's own established round-trip-pair convention
(`intPtrToPgInt2` / `pgInt2ToIntPtr` in `service.go`), and each stale sentence is deleted
in the same edit:

```go
// pgInt8ToInt64Ptr converts a nullable pgtype.Int8 to *int64. Reverse of
// int64PtrToPgInt8 — added by RM30-charging-add-session-read-port, this module's first
// reader for charge_sessions.
func pgInt8ToInt64Ptr(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}

// pgFloat8ToFloat64Ptr converts a nullable pgtype.Float8 to *float64. Reverse of
// float64PtrToPgFloat8.
func pgFloat8ToFloat64Ptr(v pgtype.Float8) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}

// pgBoolToBoolPtr converts a nullable pgtype.Bool to *bool. Reverse of boolPtrToPgBool.
func pgBoolToBoolPtr(v pgtype.Bool) *bool {
	if !v.Valid {
		return nil
	}
	b := v.Bool
	return &b
}
```

`Currency` (`pgtype.Text → *string`) and the four `SMALLINT` percentage columns
(`pgtype.Int2 → *int`) need no new helper: `service.go`'s existing `pgTextToPtr` and
`pgInt2ToIntPtr` are package-level (unexported but visible throughout `package charging`)
and already do exactly this — `Entry.StartBatteryPct`/`EndBatteryPct` already round-trip
through `pgInt2ToIntPtr`. Reusing them here rather than adding `session_reader.go`-local
duplicates is the same "closed, small vocabulary" instance `CLAUDE.md`'s AI-efficiency
rule names explicitly for conversion helpers.

### D7 — Implementation split mirrors `SessionWriter`'s, not `Reader`'s

`SessionReader`'s implementation lives in a **new** `session_reader.go`, not inside
`service.go` alongside `readerService`. This is a deliberate asymmetry the dispatch
specifies, and it is consistent with how RM29 tier 6 already split `SessionWriter` out of
`service.go` into `session_writer.go`: `service.go`'s `store` interface (and its
fake-backed offline unit tests) exists for `Writer`/`Reader`'s CRUD-shaped,
narrowly-testable methods; `SessionWriter` and now `SessionReader` are both tested only
via real `DATABASE_URL`-gated integration tests (no fake), so — mirroring
`sessionWriter`'s exact shape — `sessionReader` is a small unexported struct holding
`*pgxpool.Pool` + `*chargingdb.Queries` directly, with its own unexported constructor and
compile-time assertion:

```go
type sessionReader struct {
	pool *pgxpool.Pool
	q    *chargingdb.Queries
}

func newSessionReader(pool *pgxpool.Pool) *sessionReader {
	return &sessionReader{pool: pool, q: chargingdb.New(pool)}
}

var _ SessionReader = (*sessionReader)(nil)
```

(`pool` is retained for shape-parity with `sessionWriter` and future methods that might
need a transaction; this tier's single method uses only `q`.)

---

## Database Changes

**None.** No migration file is added or edited. This section exists — per
`openspec/config.yaml`'s blanket rule that "design.md is REQUIRED for any DB-touching
change" — to prove that the existing schema already serves the new query, not to record a
schema change.

### The query (`internal/charging/db/query.sql`, appended)

```sql
-- name: ListSessionsByVehicleBetween :many
-- Return charge sessions for a specific vehicle within an account whose
-- charge_stop_date_time falls within the whole UTC calendar-day window
-- [@from_time, @to_time], @to_time inclusive of its entire day, ordered oldest-first
-- (ascending charge_stop_date_time, design.md D3 — deliberately UNLIKE
-- ListEntriesByVehicleBetween's charged_on DESC, but matching
-- telemetry.SuperchargerSessionsByVehicleBetween's ordering exactly).
--
-- @end_bound is @to_time + 1 calendar day, COMPUTED IN GO (design.md D5), exactly
-- mirroring telemetry.SuperchargerSessionsByVehicleBetween's own end-bound translation
-- — do NOT compute it in SQL. The predicate below is therefore half-open
-- (>= ... AND < ...), not BETWEEN: a plain BETWEEN against @to_time's UTC-midnight
-- value would silently drop every session that stopped later that same calendar day,
-- which is exactly the trap the project's ?start=&end= HTTP date-filter convention
-- (internal/gateway/AGENTS.md) exists to prevent.
--
-- Uses idx_charge_sessions_vehicle_stop (account_id, tesla_id, charge_stop_date_time)
-- as a single ascending index range scan: account_id and tesla_id prune to the tenant
-- and vehicle as leading equality predicates, the half-open charge_stop_date_time
-- range walks the trailing column, and the index's own ASC order satisfies ORDER BY
-- with no separate sort step and no backward scan (design.md D1). A half-open range is
-- exactly as scannable as a closed BETWEEN on a B-tree index — both are a single
-- contiguous leaf-page walk bounded on two sides; only the boundary comparison
-- operator differs (design.md §"Index proof"). No LIMIT: the caller-supplied
-- [from_time, end_bound) window is the safety bound, matching
-- ListEntriesByVehicleBetween's precedent.
--
-- tesla_id = @tesla_id against a nullable column excludes every row where tesla_id IS
-- NULL (SQL's NULL = value is neither true nor false) — an orphaned session (VIN no
-- longer a currently-registered vehicle) is correctly outside a teslaID-keyed read
-- (design.md D6). This is the same behavior telemetry's own
-- SuperchargerSessionsByVehicleBetween already has over the identical column shape.
SELECT * FROM charge_sessions
WHERE account_id = @account_id
  AND tesla_id = @tesla_id
  AND charge_stop_date_time >= @from_time
  AND charge_stop_date_time <  @end_bound
ORDER BY charge_stop_date_time ASC;
```

**Go-side call shape** (mirroring `superchargerReader.SuperchargerSessionsByVehicleBetween`,
`internal/telemetry/reader.go`, verbatim):

```go
func (r *sessionReader) ListSessionsByVehicleBetween(ctx context.Context, accountID uuid.UUID, teslaID int64, from, to time.Time) ([]Session, error) {
	endBound := to.AddDate(0, 0, 1)
	rows, err := r.q.ListSessionsByVehicleBetween(ctx, chargingdb.ListSessionsByVehicleBetweenParams{
		AccountID: accountID,
		TeslaID:   teslaIDToPgInt8(teslaID),
		FromTime:  pgtype.Timestamptz{Time: from, Valid: true},
		EndBound:  pgtype.Timestamptz{Time: endBound, Valid: true},
	})
	// ... error handling + rowToSession mapping, as any other Reader method in this file
}
```

### Index proof — why `idx_charge_sessions_vehicle_stop` already serves this query

The index, created by RM29 tier 6's migration and unchanged by this tier:

```sql
CREATE INDEX idx_charge_sessions_vehicle_stop
    ON charge_sessions (account_id, tesla_id, charge_stop_date_time);
```

| Query clause | Index column | Role |
|---|---|---|
| `WHERE account_id = @account_id` | `account_id` (1st) | Leading equality predicate — prunes to the tenant, per the platform's account-id-leads convention (`ai/go-conventions.md` §"Read optimization"). |
| `WHERE tesla_id = @tesla_id` | `tesla_id` (2nd) | Second equality predicate in the same range scan — prunes to the vehicle. |
| `WHERE charge_stop_date_time >= @from_time AND charge_stop_date_time < @end_bound` | `charge_stop_date_time` (3rd, trailing) | Half-open range predicate on the trailing column — satisfied as a single contiguous index range scan following the two leading equalities. |
| `ORDER BY charge_stop_date_time ASC` | same trailing column, index built ASC | The index's natural order already matches the requested order — **no sort step**. |

**A half-open range is not a weaker fit than the `BETWEEN` this proof originally assumed
— confirmed, not assumed.** A B-tree index range scan is bounded by two comparisons
against the leading key of the scanned range; whether those comparisons are
`(>=, <=)` (as `BETWEEN` compiles to) or `(>=, <)` (this query's half-open form) changes
only which comparison operator bounds the top of the scan, not the scan's shape. Postgres
walks the same contiguous run of index leaf entries either way — it does not fall back to
a full scan, a bitmap scan, or an in-memory filter for a half-open bound on an indexed
column. `telemetry.SuperchargerSessionsByVehicleBetween` is the existing, already-running
proof of exactly this: it has used `(account_id, tesla_id, charge_start_date_time DESC)`'s
analogous three-column index with this same `>= start AND < endBound` half-open shape
since RM28, over the same table family, with no reported degradation. The conclusion is
unchanged from the original proof: this is still a pure index range scan with no sort
step and no bookmark lookup beyond the index's own leaf pages (Postgres serves
`SELECT *` from a non-covering index via a plain index scan + heap fetch per matching
row; no covering/INCLUDE index is warranted at this table's current 4-row volume — the
same call RM29 tier 6's own Index Plan already made explicit for the general case, see
that design's "Write cost accepted"). This is the query RM29 tier 6's Index Plan named as
the exact reason this index was built ahead of any caller ("the deferred re-point... and
any future verification-UI or charge-history listing"). **No new index, column, or
constraint is required by the half-open form either — D2's conclusion stands unchanged.**

**Deliberately NOT added, restating RM29 tier 6's own Index Plan (unchanged by this
tier):** an account-wide `(account_id, charge_stop_date_time)` index (no such read is
declared — `ListSessionsByVehicleBetween` is always vehicle-scoped), a `vin`-keyed index
(every declared read is by `tesla_id`), and a partial index on "has verified percentages"
(the verification UI, backlog item 11, still does not exist). Nothing in this tier changes
any of those conclusions.

---

## Test Contract (expected values authored before implementation, per `ai/go-conventions.md`)

All tests are `DATABASE_URL`-gated integration tests in `internal/charging`'s existing
external test package (`package charging_test`), seeding `charge_sessions` directly via
SQL or through `SessionWriter.MirrorSessions` (whichever is more direct per case) and
asserting against `charging.Session` domain fields only — **no `pgtype` in any assertion**
(`internal/charging/AGENTS.md` §Testing Notes). Fixtures use fresh `uuid.New()` account
ids and `session_id`s in the **940001–940099** range (disjoint from RM29 tier 6's
920001–920099 and from the real backfilled `734860294`).

Baseline fixture **S1**, seeded via one `SessionWriter.MirrorSessions` call under account
`acctA`, `TeslaID = 940001`, all four rows queried with **`from = 2026-08-01, to =
2026-08-31`** (whole calendar dates — `from_time = 2026-08-01T00:00:00Z`,
`end_bound = to.AddDate(0,0,1) = 2026-09-01T00:00:00Z`, per D5):

| SessionID | ChargeStartDateTime | ChargeStopDateTime | In `[2026-08-01, 2026-08-31]`? |
|---|---|---|---|
| 940001 | 2026-08-01T00:00:00Z | 2026-08-01T00:00:00Z | **included** — stops exactly at `from_time`, the lower bound |
| 940002 | 2026-07-31T23:50:00Z | 2026-08-01T00:10:00Z | **included** — starts *before* the window but stops within it (the straddle case D1 is about) |
| 940003 | 2026-08-31T23:00:00Z | 2026-08-31T23:59:59.999999Z | **included** — stops at the last representable instant of the `to` calendar day |
| 940004 | 2026-08-31T23:50:00Z | 2026-09-01T00:00:00Z | **excluded** — stops exactly at `end_bound`, the first instant of the day *after* `to`; its start is inside the window but its stop is not |

Battery percentages are then written directly by SQL onto 940002 only
(`start_battery_pct = 20, end_battery_pct = 80, battery_pct_source = 'user_verified',
start_battery_pct_est = 22, end_battery_pct_est = 78`), so the returned `Session` for that
row is asserted to carry them and 940001/940003 are asserted to carry all five as `nil`.

**T1. The lower bound is inclusive: a session stopping exactly at `from`'s midnight is
included.** Call with `from = 2026-08-01, to = 2026-08-31`. Expected: 940001 is present in
the result (not excluded by an off-by-one on `>= from_time`).

**T2. A session stopping at the very last instant of the `to` calendar day is included**
— the case that motivates translating `to` into a half-open upper bound rather than
comparing against `to`'s own midnight value. Same call as T1. Expected: 940003 is present
(`23:59:59.999999Z` on `2026-08-31` is `< end_bound` = `2026-09-01T00:00:00Z`).

**T3. A session stopping exactly at the first instant of the day *after* `to` is
excluded** — the boundary case that motivated this design's D5 revision (a plain
`BETWEEN` against `to`'s raw midnight value would have wrongly excluded 940003 above while
correctly excluding this one; the half-open `< end_bound` form gets both right from one
mechanism). Same call as T1. Expected: 940004 is **absent** from the result, even though
its `ChargeStartDateTime` (`2026-08-31T23:50:00Z`) falls inside the window — a session's
membership is decided by its stop instant alone (D1).

**T4. A session whose start precedes the window but whose stop falls inside it is
included (the boundary case D1 is about).** Same call as T1 (940002 already satisfies
this: start `2026-07-31T23:50:00Z` is before `from_time`, stop
`2026-08-01T00:10:00Z` is inside `[from_time, end_bound)`). Expected: 940002 is present,
with `ChargeStartDateTime` still reported as `2026-07-31T23:50:00Z` — the field is
returned as recorded, even though it precedes the window.

**T5. No match returns a non-nil empty slice.** Call for a `teslaID` with no sessions
stopping inside `[from_time, end_bound)` for any `from, to`. Expected: `len(result) == 0`
and `result != nil`.

**T6. Multi-tenant isolation: another account's session with the same `tesla_id` and
overlapping window does not leak.** Seed an identical session under account `acctB` with
the same `TeslaID` (940001) and a `ChargeStopDateTime` inside `acctA`'s query window.
Call `ListSessionsByVehicleBetween(ctx, acctA, 940001, from, to)`. Expected: only
`acctA`'s sessions are returned; `acctB`'s session is absent regardless of window overlap.

**T7. A different vehicle within the same account does not leak.** Seed a session under
`acctA` with `TeslaID = 940099` inside the same window. Call with `teslaID = 940001`.
Expected: the 940099 session is absent.

**T8. A session whose `tesla_id` is `NULL` is never returned, for any `teslaID`.** Mirror
a session with `TeslaID: nil` (simulating a deregistered vehicle) inside the window.
Expected: not returned by any `teslaID` value — this is D6's documented consequence, not a
bug to "fix" by special-casing `NULL`.

**T9. Returned rows carry the charging-owned battery-percentage fields.** From S1's
fixture: 940002's `Session.StartBatteryPct == 20`, `EndBatteryPct == 80`,
`BatteryPctSource == "user_verified"`, `StartBatteryPctEst == 22`,
`EndBatteryPctEst == 78`; 940001 and 940003 all five `nil`. This is the scenario that
proves the point of reading `charging` instead of `telemetry` at all.

**T10. Ordering is ascending by `ChargeStopDateTime`**, not insertion order and not
descending. Asserted directly by T1–T4's shared three-row result (940004 excluded, D5):
`result[0].SessionID == 940001`, `result[1].SessionID == 940002`, `result[2].SessionID ==
940003` — in stop-time order, not `SessionID` order (which happens to coincide here; the
fixture in a real re-run should not rely on that coincidence to catch an ordering bug).

**T11. Nullable fee fields round-trip as `nil` through the reverse helpers.** Mirror a
session with `EnergyKWh: nil, TotalCost: nil, Currency: nil, IsPaid: nil` (D6's reverse
helpers exercised directly). Expected: all four fields `nil` on the returned `Session`,
`SiteLocationName` still non-empty.

### What must NOT change

- RM29 tier 6's existing `db_session_integration_test.go` (Groups B and C) and
  `db_backfill_integration_test.go` (Group A): not one assertion, fixture or name.
- `internal/charging`'s `manual_charge_entries` tests: unaffected — this change adds no
  file that touches them and no shared helper is renamed.

---

## Risks / Trade-offs

- **`Session` and `SessionMirror` duplicate thirteen field names.** Accepted deliberately
  (D4) to preserve `SessionMirror`'s compile-time percentage-free guarantee; the
  alternative (one shared type) reopens the exact risk RM29 D6 closed.
- **`ListSessionsByVehicleBetween` ships with zero callers this tier.** Standard for a
  roadmap's first tier (RM29 tier 6 shipped `SessionWriter` the same way, one tier ahead
  of `cmd/poller`'s wiring). The port is exercised only by its own integration tests until
  tier 2 lands; `go vet` and `make build` still cover it structurally.
- **`from`/`to` are whole-day semantics on a `TIMESTAMPTZ` column, which is easy to
  misuse if a future caller passes an exact instant expecting `to` to be honored as given.**
  `to` is always translated to `to.AddDate(0, 0, 1)` and compared with `<` (D5); a caller
  that passes `time.Now()` as `to` intending "up to right now" instead gets "up to the end
  of today" — a wider window than asked for, silently. This is the correct contract for
  this port's actual caller (tier 2's `?start=&end=` endpoint, which only ever supplies
  whole calendar days), but it means `ListSessionsByVehicleBetween` is **not** a safe
  drop-in for a future caller that wants sub-day precision; such a caller needs its own
  port or an explicit exception documented here, not a silent reinterpretation of `to`.
  The port's doc comment states the whole-day contract explicitly (Wave 2) so this is
  discoverable before a future misuse, not after.
- **`session_writer.go` gains three reverse helpers it does not itself call** — read-only
  helpers living in a file whose existing content is entirely write-path. Accepted over a
  new `session_reader.go`-local trio to keep every `pgtype.IntN`/`Float8`/`Bool`
  round-trip pair in one place (D6); the alternative (duplicate helpers, one file each)
  is a smaller diff per file but a "closed vocabulary in two places" the AI-efficiency
  rule warns against directly.

## Verification signals

Per the binding `Test-Execution-Policy`, the assistant runs and reports: `go build ./...`,
`go vet ./...`, `gofmt -l .`, `make build`, `make vet`, `make bins`, `make sqlc` (after the
new query is appended), and the three standalone guards — `make ui-guard` (no-op: no
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
