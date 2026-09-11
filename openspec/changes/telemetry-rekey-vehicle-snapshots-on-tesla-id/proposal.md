# telemetry-rekey-vehicle-snapshots-on-tesla-id

> Source: MAG-65 — https://linear.app/magus-monitor/issue/MAG-65/2-re-key-telemetryvehicle-snapshots-on-tesla-id-demote-poll
> Step 2 of the MAG-63 re-key decision record. `vehicle_snapshots` is the root
> of all car data and the largest table in the platform. It has no gateway
> consumer — the gateway must never import `internal/telemetry` at all
> (`make boundary-guard`), so this lands before any authorization work.

## Why

`vehicle_snapshots` is keyed on `(account_id, tesla_id, captured_date)`. But
today's real duplicates do not come from the key being wrong in isolation —
they come from **how vehicles are polled**. `telemetry.CollectAll` groups the
account module's full vehicle list by account and polls every row, with no
`access_type` filter. So a car registered to two accounts (one `OWNER`, one
`DRIVER`, or two `DRIVER`s) is fetched twice a night, and each account writes
its own snapshot row for the same car-day. Re-keying the table without fixing
the poll would just move the duplicate-writer problem one column over.

So this change does two things, in a fixed order: first it stops the
double-write at its source (poll election), then it re-keys the table
`(account_id, tesla_id, captured_date)` → `(tesla_id, captured_date)`,
matching the parent re-key roadmap's plan for every table it touches, and
matching the pilot (`analytics-rekey-charge-gaps-on-tesla-id`).

`poll_attempts.account_id` is not dropped — it is renamed to
`polled_by_account_id`. It never was a co-identity column (the row's identity
is `(vehicle, run)`); it records which account's token paid for the Fleet API
call. That fact only becomes reliable after poll election: today two accounts
can both attempt the same car in one cycle, so the column names one of two
attempts, not "the" account. After election there is exactly one attempt, one
account, per vehicle per cycle — the rename makes the column's meaning match
what it has always tried to record.

## What changes

- **Poll election** (`internal/telemetry/service.go`, no schema change): a
  new pure-Go step runs before `groupByAccount`, picking exactly one
  `(account_id, tesla_id)` pair per distinct `tesla_id` from
  `account.AllRegisteredVehicles`'s full result. Prefer `OWNER`; otherwise any
  candidate account; tie-break by lowest `account_id`; **never skip a
  vehicle**. `collectAccount`'s existing one-`AccessTokenFor`/one-`ListVehicles`
  -per-account batching (design D3) is unchanged — election only changes
  *which* rows reach `groupByAccount`, not how the account loop works.
- **Migration** (`internal/telemetry/db/migrations/`): collapse any
  duplicate `(tesla_id, captured_date)` rows in `vehicle_snapshots` (latest
  `captured_at` wins, ties by `id` — the same rule
  `20260805000001_dedupe_vehicle_snapshots_daily.sql` already uses one column
  narrower), replace `UNIQUE (account_id, tesla_id, captured_date)` with
  `UNIQUE (tesla_id, captured_date)`, replace
  `idx_vehicle_snapshots_vehicle_time` with `(tesla_id, captured_at)`, and
  `DROP COLUMN account_id`. Separately, `RENAME COLUMN account_id TO
  polled_by_account_id` on `poll_attempts` — no other change to that table.
- **Queries** (`internal/telemetry/db/query.sql`): drop `account_id` from
  `InsertVehicleSnapshot`, `SnapshotsByVehicleSince`,
  `SnapshotsByVehicleBetween`, `SnapshotsByVehicleUpdatedSince`,
  `SnapshotPrecedingDay`; rename `InsertPollAttempt`'s column reference;
  rename `LatestSnapshotsByAccount` to `LatestSnapshotsByVehicles`, taking
  `tesla_id = ANY(@tesla_ids::bigint[])` instead of one `account_id`.
- **Domain types and ports** (`internal/telemetry/telemetry.go`,
  `service.go`, `reader.go`, `query_log.go`): drop `accountID` from every
  `Reader` method and from `Snapshot.AccountID` (the field has nothing left
  to map from once the column is gone — see `design.md` D-SNAPSHOT-FIELD).
  Rename `Attempt.AccountID` to `Attempt.PolledByAccountID` to match the
  renamed column (`design.md` D-ATTEMPT-FIELD). Rename
  `Reader.LatestSnapshotsByAccount` to `Reader.LatestSnapshotsByVehicles`.
- **Test fixups** (not new tests — keeping build/vet green after the
  signature and schema changes): every telemetry test file that constructs a
  `Snapshot{AccountID: ...}` or an `Attempt{AccountID: ...}`, or implements
  the unexported `store` interface. See `tasks.md` for the full, verified
  file list — it is larger than the dispatch's original three-file estimate.
- **Docs**: `internal/telemetry/AGENTS.md`, the affected requirements in
  `openspec/specs/telemetry/spec.md` (this change's delta), and the
  `kkpa/context/architecture/telemetry-ingest-only.md` KB guide (its
  "Same-day captures dedupe" bullet names the old constraint literally — see
  `design.md`'s "Ticket-vs-reality correction" note for exactly where).

## Breaking?

Internal only — no external API, no HTTP surface on this module. The
`Reader` port's signature changes on five methods (drop `accountID`) and one
method renames (`LatestSnapshotsByAccount` → `LatestSnapshotsByVehicles`,
different parameter shape). `Snapshot` loses `AccountID`; `Attempt` renames
`AccountID` to `PolledByAccountID`. Every caller and every test that builds
these types or implements `store`/`Reader` must update to compile — see
`tasks.md`.

## Modules affected

- `internal/telemetry` — owns the tables, the ports, the poll-election logic,
  and every test fixup inside this module (this worker's sandbox).
- `internal/analytics` — two production call sites
  (`internal/analytics/recalculate.go:104,123,269`,
  `internal/analytics/reader.go:136`) drop the `accountID` argument they pass
  into the four `telemetry.Reader` methods they call — a mechanical
  consequence of the port signature change, not an analytics design decision
  (`design.md` D-SCOPE, mirroring the pilot's own D-SCOPE). `analytics`
  itself **keeps** its own `accountID` parameter on `Recalculate`/
  `Reconcile` — it still needs it to reach the charge sources, which re-key
  in a later child ticket. Three analytics test files also need mechanical
  fixups: `internal/analytics/reader_test.go`,
  `internal/analytics/recalculate_test.go`,
  `internal/analytics/consumption_test.go` — plus the `fakeTelemetryReader`
  in `reader_test.go`, which must add `LatestSnapshotsByVehicles` to keep
  satisfying `telemetry.Reader` (found by this design, not in the original
  dispatch — see `design.md`). All of this is **leader-owned integration
  work**, listed as its own task group; this worker does not touch
  `internal/analytics`.

## Read paths affected

None directly widened or narrowed. `vehicle_snapshots`'s only readers are
`internal/analytics` (`SnapshotsByVehicleSince`, `SnapshotsByVehicleBetween`,
`SnapshotsByVehicleUpdatedSince`, `SnapshotPrecedingDay`) and, after this
change, nothing calls `LatestSnapshotsByVehicles` in production either — same
as today's `LatestSnapshotsByAccount`, which has no real caller outside this
module (verified: the only other reference is a test fake). Every one of
those four analytics-called methods keeps the same per-vehicle access
pattern; only the identity key they filter on narrows from
`(account_id, tesla_id)` to `tesla_id`, which the new `UNIQUE (tesla_id,
captured_date)` index alone still serves — see `design.md`'s Index Plan.

## Non-goals

- No change to `supercharger_history` or `poll_runs` — separate tickets in
  the MAG-63 roadmap (`supercharger_history` still needs `account_id`: it is
  account-level Tesla billing data, not a per-vehicle table keyed the same
  way).
- No change to `analytics.Recalculate`/`Reconcile`'s own `accountID`
  parameter — deliberate, see "Modules affected" above.
- No gateway change — the gateway has zero real telemetry imports today and
  none are added.
