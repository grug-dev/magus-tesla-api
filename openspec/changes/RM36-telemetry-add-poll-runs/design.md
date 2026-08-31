# Design — RM36-telemetry-add-poll-runs

## Context

`openspec/roadmaps/RM36-poll-run-tracking.md` settles seven decisions (D1–D7) with
the user before any artifact existed. This design.md does not re-litigate them; it
resolves the concrete, buildable shape they leave open, and flags every place this
pass had to make a call the roadmap did not dictate. Two constraints shaped almost
every decision below:

1. **This tier is sandboxed to `internal/telemetry`.** Tier 2
   (`RM36-app-record-poll-run`) is sandboxed to `internal/app`. Any design that would
   require editing a file in the other module's sandbox to keep the build green is
   invalid, however faithfully it reads the roadmap text — see D6 and D8.
2. **`poll_attempts` (grain: one row per vehicle per run) is untouched.** Every
   decision below is scoped to the new `poll_runs` table and to `CycleReport`; no
   `poll_attempts` column, query, or Go type changes.

## Goals / Non-Goals

**Goals:** a `poll_runs` row for every `app.ProcessVehicleData` invocation, including
one that fails before touching a single vehicle; an accurate Tesla-API-call count;
account-level attempt/outcome counts; a disambiguated log line; zero edits outside
`internal/telemetry`.

**Non-goals:** anything that reads `poll_runs` (no `Reader`-style port, no gateway
page — backlog); per-vehicle duration (roadmap D3, backlog); any change to
`internal/app`, `cmd/poller`, or `internal/tesla` (tier 2 / never, per roadmap D2).

## Decisions

### D1 — `run_id` is the PRIMARY KEY; no surrogate `id` column

`RunContext.RunID` is generated once per invocation by `internal/app` (`uuid.New()`)
**before** any row in `poll_attempts` or `poll_runs` exists for that run — it is
already the row's natural, immutable identity by the time `RecordRun` is called.
Adding a surrogate `id UUID DEFAULT gen_random_uuid()` alongside it would be a second
column meaning the same thing, at a table where — unlike `vehicle_snapshots` — the
natural key is never mutated after insert (`RecordRun` is a plain, single `INSERT`;
there is no dedupe-upsert here, see D3). Rejected: a surrogate `id` "for
consistency" with `vehicle_snapshots`/`poll_attempts` — those tables need a surrogate
because their natural keys are either non-unique per se (`poll_attempts`: many rows
share an `(account_id, tesla_id)` pair over time) or mutable under upsert
(`vehicle_snapshots`: `(account_id, tesla_id, captured_date)` is replaced in place).
Neither condition holds here.

### D2 — No foreign key between `poll_runs` and `poll_attempts`

Both tables carry `run_id`, but a FK in either direction is unsatisfiable by this
tier's own write order: `poll_attempts` rows are written throughout the cycle
(`service.go`'s `record`, once per vehicle), while the `poll_runs` row for that same
`run_id` is written **once, at the very end**, after tier 2 measures the whole run's
duration (roadmap D6). A `poll_attempts.run_id → poll_runs.run_id` FK would reject
every attempt row the instant it was written, since the parent row does not exist
yet. The reverse FK is equally wrong: a run that fails before writing any
`poll_attempts` row (the exact case D1 of the roadmap exists to fix) must still
record a `poll_runs` row with zero attempts — nothing to reference. The two tables
correlate only through the application-known `run_id` value, mirroring this
project's standing no-cross-module-FK convention (`ai/architecture.md` §2,
originally about `account_id`/`tesla_id`) applied here to two tables in the **same**
module for the identical reason: referential integrity is upheld by write order the
code already guarantees, not by a constraint.

### D3 — `finished_at` and `duration_seconds` are `NOT NULL`; one `INSERT`, never an `UPDATE`

Resolves the dispatch's open question directly. `RunWriter.RecordRun` is called
**exactly once** per invocation, by tier 2, **after** the run's end is measured — on
every code path, including the step-1 whole-cycle-failure short-circuit (roadmap
D1/D6: "measures the run (start before step 1, end after step 3) and calls
`telemetry.RunWriter.RecordRun` ... including on the step-1 whole-cycle-failure
path"). Every row this port ever writes therefore already knows its own end time and
duration at `INSERT` time — there is no legitimate "started but not yet finished"
state for the schema to represent, so nullability would only invite a state the code
never produces.

The only way a run leaves **no row at all** is a hard process crash between
`ProcessVehicleData`'s start and its `RecordRun` call (power loss, OOM kill,
`kill -9`) — a pre-existing, accepted gap: `poll_attempts` already has the identical
property (a crash mid-cycle leaves whatever attempt rows were written and no more).
This table adds the same honesty at run grain, not a stronger guarantee. **A failed
run still leaves a trace** (roadmap D1's whole justification) because "failed" here
means "step 1 returned an error," which is a value `ProcessVehicleData` catches and
still measures the end time for — it is a normal, complete row with small counts,
not a partial one. See the Test Contract, Fixture 5b, for exactly what that row's
values are.

Rejected — a nullable `finished_at`/`duration_seconds` pair with a two-phase
`INSERT`-then-`UPDATE` (record start immediately, fill in the end later): this would
let an in-flight run's row be visible mid-cycle, which nothing in this roadmap reads
or needs, at the cost of a second write per run and a nullable pair whose NULL state
the single-call design (roadmap D6) never actually produces. Deferred, not
discarded: if a future "is a run currently in progress" read ever needs this, it is
an additive follow-up, not a breaking change to this schema.

### D4 — No `CHECK` constraint on `triggered_by`

Mirrors `poll_attempts.triggered_by`'s own documented reasoning (migration
`20260823000002`): the typed Go constant `telemetry.TriggeredBy` guards every value
this codebase can ever write, and nothing in this project writes SQL by hand outside
Go. A DB `CHECK` would only guard a hand-written statement that does not exist.

### D5 — Index Plan: no index beyond the primary key's automatic B-tree on `run_id`

This is the "no index beyond the PK" answer the dispatch explicitly names as a
legitimate outcome, justified here against both sides of the read-heavy
Performance-Profile's own test — write volume and the stated future read:

- **Write pattern:** exactly one `INSERT` per `app.ProcessVehicleData` invocation —
  the nightly scheduler (once per night) plus the occasional `cmd/poller --once`.
  Call it 1–3 rows/day. Already fully served by the PK's own B-tree (`INSERT`
  performs an equality-uniqueness check on `run_id`, which the PK index answers in
  O(log n) on a table that will hold on the order of 10³ rows after a decade).
- **Nothing reads this table in this roadmap** (see "Out of scope" in proposal.md;
  the roadmap's own "Future work" section names the gateway read surface as
  backlog). There is no query to index for today.
- **The one named future read** ("list recent runs, newest first" — an ops/dashboard
  view) would want `ORDER BY started_at DESC LIMIT N`. At this table's write volume,
  that is a sequential scan over at most a few thousand rows even a decade out —
  materially cheaper than the write-side cost of maintaining a second index on every
  `INSERT` for a benefit nothing exercises yet. Postgres' planner will pick a
  sequential scan over a partial/unused index at this row count regardless.

Rejected — adding `idx_poll_runs_started_at (started_at DESC)` speculatively now:
this is the over-abstraction `CLAUDE.md`'s AI-efficiency principle warns against —
indexing a read path that does not exist yet, on a surface explicitly deferred to a
future tier. Precedent: migration `20260823000002` made the identical call for
`poll_attempts.run_id`/`.triggered_by` ("no index... nothing reads either column in
this tier... costs nothing lost by waiting"). The future gateway-read tier can add
this index at zero migration risk (a new index on an existing, small table) the
moment a real reader needs it.

### D6 — `CycleReport` gains a `Duration time.Duration` field, left zero by `CollectAll`, set by the caller

`CollectAll` cannot measure the run-level duration roadmap D3 wants (steps 1–3):
it only ever runs step 1. Only `internal/app`'s `ProcessVehicleData` (tier 2) spans
all three steps and can measure the true start-to-finish duration. Two ways to get
that number into `LogCycle`'s printed line (roadmap D7):

- **(a) Change `LogCycle`'s signature** to accept a duration parameter. Rejected:
  `LogCycle` is called from two files outside this tier's sandbox
  (`internal/app/scheduler.go:74`, `cmd/poller/main.go:163`), and tier 2 is itself
  sandboxed to `internal/app` — neither this tier nor tier 2 can touch both
  `internal/telemetry/report.go`'s signature and its two call sites in the same
  dispatch without a leader-granted cross-module exception. A signature change is
  therefore unbuildable without either breaking `cmd/poller/main.go` (a file no
  tier here touches) or requiring one worker to reach outside its module.
- **(b) Add a field the caller populates before calling the unchanged `LogCycle`
  function** — chosen. `CycleReport` is already a plain exported struct passed by
  value into `LogCycle`; tier 2 already measures `start`/`end` around the whole
  `ProcessVehicleData` call to build `PollRun.StartedAt`/`.FinishedAt`/
  `.DurationSeconds` (D3 above). Tier 2 sets `report.Duration = end.Sub(start)`
  on the same `CycleReport` value it already holds, then calls
  `telemetry.LogCycle(report, err)` exactly as today — **no signature change, no
  edit to `internal/telemetry` from tier 2, no edit to `internal/app` from this
  tier.** `CollectAll` itself never sets this field; its zero value is a
  documented, intentional "not measured by this call" state.

This field is additive beyond the roadmap tier table's literal "New `CycleReport`
fields" list (`TeslaAPICalls`, `AccountsAttempted`, `AccountsSucceeded`,
`AccountsFailed`) — flagged here explicitly for the design gate, since it is this
design's own resolution of a build-ability problem the roadmap's tier split did not
anticipate, not a roadmap-authored requirement.

### D7 — `poll_runs` also carries `charging_sessions_upserted`, `charging_fetch_failures`, `config_capture_failures`

Roadmap D5's own text states the goal plainly: *"One `poll_runs` row therefore
reproduces the whole log line without a join."* The log line (`LogCycle`, both
before and after this tier's D7 update) already prints
`charging_upserted`/`charging_failures`/`config_capture_failures` — all three
already exist as `CycleReport` fields (added by earlier tiers, unchanged by this
one). Omitting them from `poll_runs` would leave the row **not** reproducing the
whole log line, contradicting D5's stated purpose. No new `CycleReport` field is
needed for any of the three — `RecordRun`'s caller (tier 2) reads them straight off
the `CycleReport` it already has.

Flagged explicitly, like D6, because the roadmap tier table's column enumeration for
`poll_runs` (D5's own text) named only the vehicle counts and the `Reason` columns —
this design reads D5's stated *purpose* as authoritative over its illustrative
column list, but the design gate is exactly the point to overrule that reading if
the user disagrees; dropping the three columns is a one-line schema edit, not a
structural change, if so.

### D8 — The log-line vehicle-counter rename (roadmap D7) changes only the *printed text*, not the `CycleReport` Go field names

Roadmap D7 says `LogCycle` "renames its vehicle-grain counters (`attempted` →
`vehicles_attempted`, `succeeded` → `vehicles_succeeded`)". Read as a Go field
rename (`CycleReport.Attempted` → `.VehiclesAttempted`), this breaks
`internal/app/scheduler_test.go:139`
(`telemetry.CycleReport{Attempted: 3, Succeeded: 2, ...}`) — a file in a module
neither this tier nor tier 2 is allowed to edit as a *side effect* of a telemetry
schema-naming change (tier 2 touches `internal/app` deliberately for its own
scope; fixing a rename it did not ask for is a different kind of edit). This design
resolves the ambiguity in favor of the buildable reading: `LogCycle`'s **printed**
line uses the labels `vehicles_attempted=%d vehicles_succeeded=%d` (satisfying the
disambiguation the ticket actually complained about — an operator reading logs), while
`CycleReport.Attempted`/`.Succeeded` keep their existing Go names, populated exactly
as today. `poll_runs.vehicles_attempted`/`.vehicles_succeeded` (D5, roadmap) are
still named that way in the schema — `RecordRun`'s caller maps
`PollRun.VehiclesAttempted = report.Attempted` by **field**, not by name, same as it
already must for every other `CycleReport` → `PollRun` field. If the user wants the
Go field genuinely renamed, that is a separate, tiny follow-up change touching
`internal/telemetry` **and** `internal/app/scheduler_test.go` together, sequenced as
its own atomic unit — not a silent side effect of this tier.

### D9 — The counting decorator implements every `tesla.VehicleService` method explicitly; it does not embed the interface

```go
type callCounter struct {
    inner tesla.VehicleService
    calls int
}
```

with `ListVehicles`/`WakeUp`/`VehicleData`/`ChargingHistory` each explicitly
forwarding to `inner` and incrementing `calls` first. Rejected — embedding
(`struct{ tesla.VehicleService }` plus overriding only the methods that exist
today): if a future Fleet API call is added to `tesla.VehicleService` without a
matching override here, an embedded field would satisfy the interface *silently* via
promotion, and that call would never be counted — exactly the "known accepted risk"
roadmap D2 names ("counting outside the HTTP layer can drift if a future call path
bypasses the decorator"). Explicit, no-embedding implementation turns that risk into
a **compile error** the moment `tesla.VehicleService` gains a method this file does
not also gain — the reviewer's checklist item ("every `tesla` call in `telemetry`
goes through it") becomes something the compiler partly enforces, not something that
depends entirely on a human noticing.

The counter increments **before** delegating to `inner`, unconditionally — a call
that returns an error still counted. This matches roadmap D2's framing exactly:
"how many Tesla Fleet API requests it spent" — a rejected or failed request still
consumes a request against Tesla's API and its rate limit; only a call that never
reaches `inner` at all (there is none — every method here is a thin wrapper) would
be uncounted.

### D10 — The decorator is constructed fresh inside `CollectAll`, never stored on `*service`, and threaded as an explicit parameter

```go
func (s *service) CollectAll(ctx context.Context, run RunContext) (CycleReport, error) {
    counted := newCallCounter(s.tsla)
    report := CycleReport{FailuresByReason: map[Reason]int{}}
    ...
    for accountID, owned := range byAccount {
        s.collectAccount(ctx, run, counted, accountID, owned, &report)
    }
    report.TeslaAPICalls = counted.calls
    ...
}
```

`*service` is a long-lived object built once in `cmd/poller` and reused across every
scheduled cycle (the same fact `CollectAll`'s own doc comment already states about
`run RunContext`). A counter held as a `*service` field would need resetting at the
top of every `CollectAll` call, and a concurrent or future overlapping invocation
would silently share (and corrupt) the count — the same failure class `RunContext`
being a parameter, never a field, already exists to prevent. Constructing `counted`
fresh on `CollectAll`'s stack and threading it down through
`collectAccount`/`collectVehicle`/`attemptVehicle`/`listStates`/
`collectChargingHistory` as an explicit `tsla tesla.VehicleService` parameter
(replacing their internal reads of `s.tsla`) makes leakage between runs structurally
impossible, mirroring the exact pattern `RunContext` already established one call
chain over. `waitUntilOnline`/`isOnline` need no change — both already take
`svc tesla.VehicleService` as an explicit parameter, so `attemptVehicle` simply
passes `counted` through unchanged.

No `sync`/atomic needed: `CollectAll`'s account loop is a plain sequential `for`
(no goroutines), so `calls int` is never accessed concurrently within one run.
Flagged for future readers: **if `CollectAll` is ever parallelized across accounts,
`callCounter.calls` must gain synchronization at that time** — not added
speculatively here (AI-efficiency: don't guard against concurrency the code does not
have).

Every one of `collectAccount`/`collectChargingHistory`/`listStates`/
`collectVehicle`/`attemptVehicle`'s signatures gains this one parameter
(`tsla tesla.VehicleService`, placed immediately after `ctx`/`run`); none of their
call sites live in test files (grep confirms every existing test calls only the
public `CollectAll` — see Test Contract preamble), so this is a zero-impact internal
refactor from the test suite's point of view.

### D11 — A duplicate `RecordRun` call for the same `run_id` fails loudly; no upsert

`RecordRun` is a plain `INSERT`, not `INSERT ... ON CONFLICT`. Roadmap D6 states
`RecordRun` is called exactly once per invocation; a second call for the same
`run_id` indicates a bug in the caller (a retry loop calling it twice, or two
invocations sharing a `RunID` by mistake), and the PK's uniqueness violation surfaces
that immediately as an error tier 2 can log — rather than an `ON CONFLICT DO NOTHING`
silently hiding a caller bug, or `ON CONFLICT DO UPDATE` silently overwriting a
run's true first-recorded facts with a second, possibly different set. This mirrors
`vehicle_snapshots`' own contrast: that table upserts because a same-day recapture
is an *expected, named* event (the dedupe rule); nothing in this design names a
legitimate "record this run twice" event, so nothing shields the caller from a
mistake that produces one.

### D12 — `RunWriter`'s implementation talks to `telemetrydb.Queries` directly; it is not routed through the offline-fakeable `store` interface

Mirrors the precedent `internal/analytics`' `gapWriter` and this module's own
`SuperchargerReader` already establish (see `internal/analytics/gap_writer.go`'s own
doc comment): a port whose correctness is proven by a `DATABASE_URL`-gated
integration test, not by an offline fake, gets its own small concrete type
(`runWriter{ pool *pgxpool.Pool; q *telemetrydb.Queries }`) rather than a new method
added to the `store` interface `service.go`/`reader.go` share. Adding it to `store`
would force every existing `fakeStore`/`fakeReadStore`-style test double in the
package to grow a stub method it never calls — pure churn, no coverage gained,
exactly the reasoning `gapWriter`'s doc comment already gives for the identical
choice.

```go
// telemetry.go
type PollRun struct {
    RunID       uuid.UUID
    TriggeredBy TriggeredBy
    StartedAt   time.Time
    FinishedAt  time.Time
    DurationSeconds float64

    AccountsAttempted int
    AccountsSucceeded int
    AccountsFailed    int

    VehiclesAttempted int
    VehiclesSucceeded int
    FailuresAsleepTimeout int
    FailuresUnauthorized  int
    FailuresAPIError      int

    TeslaAPICalls int

    ChargingSessionsUpserted int
    ChargingFetchFailures    int
    ConfigCaptureFailures    int
}

type RunWriter interface {
    // RecordRun persists one poll_runs row. Called exactly once per
    // app.ProcessVehicleData invocation (roadmap D6), after the run's end is
    // measured — on every path, including the step-1 whole-cycle-failure
    // short-circuit, so a failed run still leaves a row (roadmap D1). A second
    // call for the same run.RunID is a caller bug and fails on the PRIMARY
    // KEY (design D11) rather than silently upserting.
    RecordRun(ctx context.Context, run PollRun) error
}

func NewRunWriter(pool *pgxpool.Pool) RunWriter {
    return &runWriter{pool: pool, q: telemetrydb.New(pool)}
}
```

## Database Changes (design gate — full schema, rationale, index plan)

### Migration — `internal/telemetry/db/migrations/20260830000001_add_poll_runs.sql`

```sql
-- +goose Up
-- poll_runs: one row per run_id, written ONCE per app.ProcessVehicleData
-- invocation by internal/app through the new telemetry.RunWriter port
-- (RM36-telemetry-add-poll-runs design D1–D5, D11). This reopens migration
-- 20260823000002's "no separate run-level table" call, but only for facts
-- that were never per-vehicle to begin with: duration, Tesla API-call spend,
-- and the account-level breakdown. poll_attempts (one row per vehicle per
-- run) is UNCHANGED by this migration.
--
-- run_id is the PRIMARY KEY, not a surrogate id: RunContext.RunID is already
-- a globally unique value generated once per invocation (uuid.New() in
-- internal/app) before any row in this table or in poll_attempts exists, so
-- it is already this row's natural, immutable identity. A surrogate
-- `id UUID DEFAULT gen_random_uuid()` alongside it would be a second column
-- meaning the same thing (design D1). Unlike vehicle_snapshots (whose
-- natural key is mutable under a dedupe UPSERT) or poll_attempts (whose
-- natural key is not unique at all — many rows share one vehicle), nothing
-- ever rewrites a poll_runs row: RecordRun is a plain, single INSERT.
--
-- No FK to/from poll_attempts.run_id: poll_attempts rows for a run are
-- written throughout the cycle, while this table's row for the same run_id
-- is written once, at the very end, after the whole run is measured — so a
-- poll_attempts row for a run always exists BEFORE that run's poll_runs row
-- does, and a run that fails before writing any poll_attempts row must
-- still be able to write a poll_runs row referencing nothing. A FK in
-- either direction would reject legitimate writes on both counts (design
-- D2). The two tables correlate by the application-known run_id value
-- alone, mirroring this project's standing no-cross-module-FK convention
-- (ai/architecture.md §2) applied here within one module for the same
-- reason: write order, not a constraint, upholds the correlation.
--
-- finished_at / duration_seconds are NOT NULL: RecordRun is called exactly
-- once per invocation, AFTER the run's end is measured, on every code path
-- including the step-1 whole-cycle-failure short-circuit (roadmap D1/D6) —
-- so every row this port ever writes already knows its own end time and
-- duration at INSERT time. There is no "started but not yet finished" state
-- for this schema to represent (design D3). The only way a run leaves NO
-- row at all is a hard process crash between start and the RecordRun call —
-- an accepted, pre-existing gap matching poll_attempts' own append-only
-- design.
--
-- No CHECK on triggered_by: guarded by the typed Go constant
-- telemetry.TriggeredBy, mirroring poll_attempts.triggered_by's own
-- documented reasoning in migration 20260823000002 (design D4) — nothing in
-- this project writes SQL by hand outside Go.
--
-- No index beyond the PRIMARY KEY's automatic B-tree on run_id. See
-- design.md's "Index Plan" (D5) for the full justification: the only write
-- pattern is INSERT-by-run_id (already served by the PK); nothing in this
-- roadmap reads this table at all (backlog: a future gateway read surface);
-- and even that future "list recent runs" read stays a trivial sequential
-- scan at this table's write volume (roughly one row per poller invocation
-- — nightly, plus the rare --once run) for years to come.
CREATE TABLE poll_runs (
    run_id                      UUID PRIMARY KEY,
    triggered_by                TEXT NOT NULL,
    started_at                  TIMESTAMPTZ NOT NULL,
    finished_at                 TIMESTAMPTZ NOT NULL,
    duration_seconds            DOUBLE PRECISION NOT NULL,

    accounts_attempted          INTEGER NOT NULL,
    accounts_succeeded          INTEGER NOT NULL,
    accounts_failed             INTEGER NOT NULL,

    vehicles_attempted          INTEGER NOT NULL,
    vehicles_succeeded          INTEGER NOT NULL,
    failures_asleep_timeout     INTEGER NOT NULL,
    failures_unauthorized       INTEGER NOT NULL,
    failures_api_error          INTEGER NOT NULL,

    tesla_api_calls             INTEGER NOT NULL,

    charging_sessions_upserted  INTEGER NOT NULL,
    charging_fetch_failures     INTEGER NOT NULL,
    config_capture_failures     INTEGER NOT NULL
);

COMMENT ON TABLE poll_runs IS
    'One row per app.ProcessVehicleData invocation (nightly scheduler or '
    'cmd/poller --once), written once by telemetry.RunWriter.RecordRun '
    'after the whole run completes -- success or the step-1 '
    'whole-cycle-failure path alike (RM36-telemetry-add-poll-runs design '
    'D1/D3/D6). Reproduces the poller''s per-cycle log line '
    '(telemetry.LogCycle) as a queryable row (design D5/D7).';

COMMENT ON COLUMN poll_runs.run_id IS
    'The invocation''s identity, generated once by internal/app '
    '(uuid.New()) and shared with every poll_attempts row that invocation '
    'wrote via RunContext.RunID. No FK to poll_attempts -- see this '
    'file''s header (design D2).';

COMMENT ON COLUMN poll_runs.triggered_by IS
    'scheduler (the nightly poller, including cmd/poller --once) or api '
    '(the parked manual-rerun API, RM29 tier 8) -- same domain and same '
    'no-CHECK reasoning as poll_attempts.triggered_by (design D4).';

COMMENT ON COLUMN poll_runs.accounts_failed IS
    'Accounts that hit one of the two whole-account short-circuits in '
    'collectAccount: AccessTokenFor failure, or the up-front ListVehicles '
    'call returning tesla.ErrUnauthorized (roadmap D4). No other failure '
    'mode counts here; accounts_succeeded = accounts_attempted - '
    'accounts_failed.';

COMMENT ON COLUMN poll_runs.tesla_api_calls IS
    'Every call telemetry made to tesla.VehicleService during this run '
    '(ListVehicles, WakeUp, VehicleData, ChargingHistory), counted by an '
    'internal counting decorator regardless of whether the call succeeded '
    'or failed (roadmap D2, design D9/D10) -- a rejected request still '
    'spends a request against Tesla''s API.';

-- +goose Down
DROP TABLE IF EXISTS poll_runs;
```

### New sqlc query — `internal/telemetry/db/query.sql`

```sql
-- name: InsertPollRun :exec
-- Inserts one poll_runs row. Called exactly once per app.ProcessVehicleData
-- invocation via telemetry.RunWriter.RecordRun (design D3/D11/D12). Never an
-- upsert: a duplicate run_id is a caller bug and must fail loudly on the
-- PRIMARY KEY, not be silently absorbed.
INSERT INTO poll_runs (
    run_id, triggered_by, started_at, finished_at, duration_seconds,
    accounts_attempted, accounts_succeeded, accounts_failed,
    vehicles_attempted, vehicles_succeeded,
    failures_asleep_timeout, failures_unauthorized, failures_api_error,
    tesla_api_calls,
    charging_sessions_upserted, charging_fetch_failures, config_capture_failures
) VALUES (
    @run_id, @triggered_by, @started_at, @finished_at, @duration_seconds,
    @accounts_attempted, @accounts_succeeded, @accounts_failed,
    @vehicles_attempted, @vehicles_succeeded,
    @failures_asleep_timeout, @failures_unauthorized, @failures_api_error,
    @tesla_api_calls,
    @charging_sessions_upserted, @charging_fetch_failures, @config_capture_failures
);
```

No `:many`/`:one` read query is added — nothing reads `poll_runs` in this roadmap
(D5).

### Index Plan (summary — full rationale in D5 above)

| Query pattern | Index used | Rationale |
|---|---|---|
| `INSERT` by `run_id` (the only write) | Automatic PK B-tree on `run_id` | Already optimal; no new index needed. |
| *(none — no read exists yet)* | — | Nothing in this roadmap reads `poll_runs`. |
| *(future, backlog)* "list recent runs, newest first" | none added now | Sequential scan is cheap at this table's write volume (~1–3 rows/day); the future gateway-read tier adds `idx_poll_runs_started_at (started_at DESC)` at zero migration risk when a real reader exists. |

## Go-Level Seam Summary (what the implementation tasks build — not written by this pass)

- `internal/telemetry/telemetry.go` — add `PollRun`, `RunWriter`, `NewRunWriter`
  (forward declaration); add `CycleReport.TeslaAPICalls`, `.AccountsAttempted`,
  `.AccountsSucceeded`, `.AccountsFailed`, `.Duration` (D6/D7 above).
- `internal/telemetry/run_writer.go` (new file) — `runWriter` concrete type +
  `RecordRun` implementation (D12), mapping `PollRun` ↔
  `telemetrydb.InsertPollRunParams` at the DB boundary (plain non-nullable binds for
  every column, since every `PollRun` field is always populated by contract, D3).

  **Correction, recorded during implementation (wave A).** An earlier draft of this
  bullet said `run_id` binds as `pgtype.UUID` "reusing the existing `runIDToPgUUID`
  helper in `service.go`". That is wrong, and the difference is caused by this
  design's own D3: because `poll_runs.run_id` is `NOT NULL`, sqlc's `uuid` override
  generates `InsertPollRunParams.RunID` as a plain `uuid.UUID`. `runIDToPgUUID`
  exists for `poll_attempts.run_id`, which is **nullable** and therefore generates
  `pgtype.UUID`. `RecordRun` passes `run.RunID` directly and does not call the
  helper. The gated DDL is unaffected — this is a generated-Go-type detail only.
- `internal/telemetry/call_counter.go` (new file) — `callCounter` (D9), `newCallCounter`.
- `internal/telemetry/service.go` — `CollectAll` constructs `counted` and threads it
  (D10); `collectAccount`/`listStates`/`collectVehicle`/`attemptVehicle`/
  `collectChargingHistory` gain a `tsla tesla.VehicleService` parameter each,
  replacing their internal `s.tsla` reads; `collectAccount`'s two short-circuit
  branches increment `report.AccountsFailed`; `CollectAll` sets
  `report.AccountsAttempted = len(byAccount)` and
  `report.AccountsSucceeded = report.AccountsAttempted - report.AccountsFailed`
  once, after the account loop (roadmap D4's own formula — no per-account "success"
  increment needed).
- `internal/telemetry/report.go` — `LogCycle`'s `Printf` format string updated per
  D7/D8: vehicle counters printed as `vehicles_attempted`/`vehicles_succeeded`
  (reading the unchanged `report.Attempted`/`.Succeeded` fields), plus
  `accounts_attempted`/`accounts_succeeded`/`accounts_failed`, `tesla_api_calls`,
  and `duration_s` (reading `report.Duration.Seconds()`).
- `internal/telemetry/AGENTS.md` — Data Ownership section gains `poll_runs` (a
  fifth owned table).
- Root `README.md` — "Database tables by module" table gains a `poll_runs` row
  under the existing `internal/telemetry` block.

## Test Contract (authored before implementation, per `ai/go-conventions.md`)

All fixtures below extend the package's existing `fakeAccount`/`fakeTesla`/
`fakeStore` infrastructure in `service_test.go` (no new fake framework). `fakeTesla`
already tracks `listCalls`, `wakeCalls[id]`, `dataCalls[id]` independently of the new
`callCounter` — every offline fixture below cross-checks `report.TeslaAPICalls`
against the sum of those independently-tracked counts, so a bug in the decorator
that under- or over-counts would fail two different ways, not one.

### Fixture 1 — one account, two vehicles (one already online, one needs waking), charging history succeeds

**Setup:** `fakeTesla` with vehicle 1 (`state: "online"`, `wakesOnline: false`),
vehicle 2 (`state: "asleep"`, `wakesOnline: true`); `chargingHistory` returns a
history with 0 sessions (default). One account owning both vehicles.

**Expected `CycleReport`:**
- `Attempted = 2`, `Succeeded = 2`, `FailuresByReason` empty.
- `AccountsAttempted = 1`, `AccountsFailed = 0`, `AccountsSucceeded = 1`.
- `TeslaAPICalls = 6`: `listStates` (1 `ListVehicles`) + vehicle 1's `VehicleData`
  (1) + vehicle 2's `WakeUp` (1) + vehicle 2's `isOnline` check (1 `ListVehicles`,
  returns online immediately since `wakesOnline` flips state synchronously in the
  fake — no poll-loop iteration) + vehicle 2's `VehicleData` (1) +
  `collectChargingHistory`'s `ChargingHistory` (1, once per account, after the
  vehicle loop) = 6.
- Cross-check: `fakeTesla.listCalls == 2`, `fakeTesla.wakeCalls[2] == 1`,
  `fakeTesla.dataCalls[1] == 1`, `fakeTesla.dataCalls[2] == 1`; `2 + 1 + 1 + 1 + 1
  (ChargingHistory) == 6 == report.TeslaAPICalls`.
- `ChargingSessionsUpserted = 0`, `ChargingFetchFailures = 0`.

### Fixture 2 — two accounts, one fails at `AccessTokenFor`

**Setup:** `fakeAccount` returns an error from `AccessTokenFor` for account X (2
registered vehicles); account Y has 1 vehicle, already online, charging history
succeeds with 0 sessions.

**Expected `CycleReport`:**
- `Attempted = 3` (2 from X + 1 from Y), `Succeeded = 1` (only Y's vehicle),
  `FailuresByReason = {unauthorized: 2}` (X's two vehicles, via the existing
  `AccessTokenFor`-failure short-circuit).
- `AccountsAttempted = 2`, `AccountsFailed = 1` (X), `AccountsSucceeded = 1`
  (`2 - 1`).
- `TeslaAPICalls = 3`: **zero** calls for account X (the short-circuit fires before
  any Tesla call is made — `AccessTokenFor` is an `account` port call, not a
  `tesla` one) + Y's `listStates` (1) + Y's `VehicleData` (1) + Y's
  `ChargingHistory` (1) = 3.

### Fixture 3 — one account, `ListVehicles` returns `tesla.ErrUnauthorized`

**Setup:** `fakeTesla.listErr = tesla.ErrUnauthorized`; one account, 2 registered
vehicles.

**Expected `CycleReport`:**
- `Attempted = 2`, `Succeeded = 0`, `FailuresByReason = {unauthorized: 2}`.
- `AccountsAttempted = 1`, `AccountsFailed = 1`, `AccountsSucceeded = 0`.
- `TeslaAPICalls = 1`: the single failed `ListVehicles` call **counts** (design D9 —
  the counter increments before checking the error) even though it failed;
  `collectAccount` returns immediately on this branch, so `ChargingHistory` is
  never called (0 additional calls).

### Fixture 4 — whole-cycle failure: `AllRegisteredVehicles` errors

**Setup:** `fakeAccount.AllRegisteredVehicles` returns an error.

**Expected:** `CollectAll` returns `(report, err)` with `err != nil` and `report`
all-zero (`Attempted = 0`, `AccountsAttempted = 0`, `TeslaAPICalls = 0`, empty
`FailuresByReason` map still non-nil). This is the pre-existing behavior
(`CollectAll`'s early-return branch); the fixture exists to pin the exact
zero-valued shape a `PollRun` built from this report would carry (Fixture 5b below),
not to test new logic.

### Fixture 5 — `RunWriter.RecordRun` (`DATABASE_URL`-gated integration test, authored last)

**5a — a normal successful run:**

```go
run := telemetry.PollRun{
    RunID:                    uuid.New(),
    TriggeredBy:              telemetry.TriggeredByScheduler,
    StartedAt:                fixedT0,
    FinishedAt:               fixedT0.Add(42 * time.Second),
    DurationSeconds:          42.0,
    AccountsAttempted:        2,
    AccountsSucceeded:        1,
    AccountsFailed:           1,
    VehiclesAttempted:        3,
    VehiclesSucceeded:        1,
    FailuresAsleepTimeout:    0,
    FailuresUnauthorized:     2,
    FailuresAPIError:         0,
    TeslaAPICalls:            3,
    ChargingSessionsUpserted: 5,
    ChargingFetchFailures:    0,
    ConfigCaptureFailures:    0,
}
```

**Expected:** `RecordRun(ctx, run)` returns `nil`. A direct SQL `SELECT * FROM
poll_runs WHERE run_id = $1` (there is no `Reader` method yet, D5/backlog) returns
exactly one row whose 17 columns equal the 17 fields above, byte-for-byte
(`triggered_by = 'scheduler'`, `finished_at - started_at` consistent with
`duration_seconds` to within floating-point tolerance).

**5b — the step-1 whole-cycle-failure trace (the case roadmap D1 exists for):**

```go
run := telemetry.PollRun{
    RunID:             uuid.New(),
    TriggeredBy:       telemetry.TriggeredByScheduler,
    StartedAt:         fixedT0,
    FinishedAt:        fixedT0.Add(150 * time.Millisecond),
    DurationSeconds:   0.15,
    AccountsAttempted: 0,
    AccountsSucceeded: 0,
    AccountsFailed:    0,
    VehiclesAttempted: 0,
    VehiclesSucceeded: 0,
    TeslaAPICalls:     0,
    // every other field zero
}
```

**Expected:** `RecordRun(ctx, run)` returns `nil` — the `NOT NULL` schema (D3) admits
this all-zero-counts row without complaint, because `finished_at`/`duration_seconds`
are still concretely known (150ms after start) even though nothing else happened.
This is the row that did not exist before this tier: a query for "did last night's
run even start" now has an answer where before there was nothing at all.

**5c — duplicate `run_id` fails loudly (design D11):**

Call `RecordRun(ctx, run)` twice with the same `run.RunID` (second call may vary
other fields). **Expected:** the first call returns `nil`; the second returns a
non-nil error (a Postgres unique-violation on the `run_id` primary key), and the
table still holds exactly one row — the first call's values, unmodified.

### `EXPLAIN` verification

Not applicable — no new index is added (D5). The only verified query pattern is a
PRIMARY KEY equality write, whose plan is Postgres' standard index-backed uniqueness
check and needs no `EXPLAIN` assertion beyond what the migration's own `CREATE TABLE
... PRIMARY KEY` guarantees.

## Risks / Trade-offs

- **The counting decorator's accuracy depends on every future `tesla` call inside
  `internal/telemetry` going through it.** Mitigated structurally by D9 (no
  interface embedding — a missed override is a compile error), and procedurally by
  the reviewer explicitly checking every `s.tsla`/parameter-`tsla` call site in a
  future change. This is roadmap D2's own named "known accepted risk," not a new one
  introduced here.
- **D6/D7's two flagged extensions (the `Duration` field; the three charging/config
  columns) are this design's own calls, not roadmap-authored.** Both are cheap to
  reverse (drop a struct field; drop three columns) if the design gate disagrees —
  named explicitly so that disagreement is possible before Apply, not discovered
  after.
- **`poll_runs` has no read port in this tier**, so the only way to verify a row
  landed correctly (until the backlog's gateway tier) is direct SQL — acceptable
  since read is explicitly out of scope, but worth naming so nobody expects a
  `Reader.RecentPollRuns`-style method to already exist.

## Verification signals

Per the Test-Execution-Policy: this pass writes the tests above but does not run
them. Owner-run commands once implementation lands:

```
go build ./...
go vet ./...
gofmt -l .
make sqlc
go test ./internal/telemetry/...   # owner-run; DB-backed Fixture 5 self-skips
                                    # without DATABASE_URL/Docker, per
                                    # internal/telemetry/AGENTS.md "Testing notes"
```
