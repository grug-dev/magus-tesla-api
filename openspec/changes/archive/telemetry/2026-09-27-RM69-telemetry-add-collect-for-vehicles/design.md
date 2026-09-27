# Design — RM69-telemetry-add-collect-for-vehicles

## Overview

Today `Collector.CollectAll` is the only way to run step 1 (fetch + store a
snapshot, fetch Supercharger history) — it always covers every registered
vehicle. This tier adds `CollectVehicles`, a sibling method that runs the
identical flow for a caller-supplied set of `tesla_id`s. It exists so tier 2
(`internal/app`) can retry only the cars that did not finish the 03:30 run,
without waking a car that already succeeded.

The two methods must never drift apart — a bug fixed in one and not the
other would quietly reintroduce the exact class of gap this roadmap closes.
So this design shares code, not just intent: `enumerateElected`,
`collectAccount`, and `collectChargingHistory` are the same three functions
both methods call.

## Decisions

### D1 — One new method on the existing `Collector` port, not a new port

`CollectVehicles(ctx context.Context, run RunContext, teslaIDs []int64) (CycleReport, error)`
joins `CollectAll` on `Collector`. It is not a separate `RetryCollector` port:
both methods do the same kind of work (step 1, keyed by `tesla_id`), and
splitting them into two ports would force tier 2 to hold two interfaces to
express one "run step 1" concept. `CollectAll`'s signature is untouched.

### D2 — The car set is a plain `[]int64`, not `vehicleref.Ref`

`internal/vehicleref` exists to prove that ONE signed-in user's HTTP request
is asking for a vehicle that user actually owns (its own package doc: "many
module ports … will accept 'the vehicles this account may see' as an
argument"). `CollectVehicles` has no signed-in user and no single account —
tier 2 selects cars from `analytics.vehicle_metrics` across every account,
the same way `CollectAll` itself walks every account with no `vehicleref` in
sight. A `Ref` would have nothing to authorize against here: there is no
"this account's owned list" to check the id against. Every existing
server-side, non-user-scoped caller in this cycle (`AllRegisteredVehicles`,
`electPollingVehicles`, `groupByAccount`) already works in plain `int64`.
`CollectVehicles` follows the same convention.

### D3 — An unregistered or unelected `tesla_id` is skipped, with no `poll_attempts` row

`CollectVehicles` filters the *already-elected* per-account vehicle lists
down to the requested `tesla_id`s (see D5's `enumerateElected`). A requested
id that is not currently registered anywhere, or belongs to a vehicle that
was somehow not elected, simply never appears in any account's filtered
subset. `collectAccount` is never called for it, so `record` (which writes
the one `poll_attempts` row per vehicle) never runs for it either. No new
code path decides to "skip" it — it is skipped by construction, the same way
`CollectAll` never writes a row for a `tesla_id` that is not in
`AllRegisteredVehicles`'s result. Test contract: TR-2.

### D4 — `collectChargingHistory` sees the account's FULL elected car list, not only the retried ones

This is the one place `CollectVehicles` cannot simply narrow every input to
the retry subset. `collectChargingHistory` builds a VIN→`tesla_id` map from
the vehicle list it is given, and a session whose VIN is missing from that
map is **dropped** and counted as `ChargingSessionsSkippedUnregistered`
(`service.go`, existing behavior). If that list were narrowed to only the
retried cars, a session for the SAME account's other, already-done car would
falsely count as "unregistered" and would not be refreshed — even though the
car is registered and its history is exactly as fetchable as before. That
would corrupt an observability counter (a registered car misreported as
unregistered) and skip a real, cheap-to-do upsert (D2 of the roadmap already
commits to fetching the account's Supercharger history on every retry that
touches that account, regardless of how many of its cars are being retried —
the fetch is one HTTP call per account, not per vehicle).

So `collectAccount` gains a second vehicle-list parameter (D5's signature),
used ONLY for the Supercharger VIN map, while the first parameter (the
existing `owned`) keeps its current meaning: the vehicles this call's
snapshot loop wakes, fetches and records `poll_attempts` for. `CollectAll`
passes its own `owned` slice for both parameters — the two are identical
there, so its behavior does not change. `CollectVehicles` passes the retry
subset as the first (snapshot-loop) parameter and the account's full elected
list as the second (charging-history-scope) parameter. Test contract: TR-3.

### D5 — `enumerateElected` is the single shared enumeration step

A new private helper:

```go
// enumerateElected fetches every registered vehicle, elects one polling
// account per tesla_id (electPollingVehicles), and groups the elected
// vehicles by account (groupByAccount). Both CollectAll and CollectVehicles
// call this and nothing else to answer "which vehicle, polled by which
// account" — so a change to election or grouping can never apply to one
// method and not the other.
func (s *service) enumerateElected(ctx context.Context) (map[uuid.UUID][]account.OwnedVehicle, error) {
	vehicles, err := s.acct.AllRegisteredVehicles(ctx)
	if err != nil {
		return nil, err
	}
	return groupByAccount(electPollingVehicles(vehicles)), nil
}
```

`CollectAll` is rewritten to call it instead of inlining the two lines it
already had (behavior-preserving refactor — same two calls, same order,
same result). `CollectVehicles` calls it too, then filters. This is the
concrete mechanism behind the roadmap's "must share `electPollingVehicles`,
`collectAccount` and `collectChargingHistory`" instruction: sharing the
functions only pays off if the code that ELECTS the input to those functions
is shared as well, or the two methods could still see different `tesla_id →
account` assignments from a future change to one call site and not the
other.

### D6 — `TriggeredByRetry = "retry"`, no migration

```go
const TriggeredByRetry TriggeredBy = "retry"
```

added next to `TriggeredByScheduler` / `TriggeredByAPI` in `telemetry.go`.
Verified against the schema before writing this: `poll_attempts.triggered_by`
and `poll_runs.triggered_by` are both plain `text` columns. The baseline
migration's own comments say so explicitly —

- `poll_attempts.triggered_by`: *"Guarded by the typed Go constant
  telemetry.TriggeredBy — no DB CHECK (design.md D1/D7)."*
- `poll_runs.triggered_by`: *"same domain and same no-CHECK reasoning as
  poll_attempts.triggered_by (design D4)."*

(`internal/telemetry/db/migrations/20260917000001_baseline.sql`, lines
124-134.) A third string value needs no `ALTER TABLE`, no new `CHECK`, and no
migration file. This tier writes none.

### D7 — `report.AccountsAttempted` counts only accounts touched by the retry set

`CollectVehicles`'s `CycleReport.AccountsAttempted` is the number of DISTINCT
accounts that own at least one vehicle in the requested set — not every
account in the system, unlike `CollectAll`. This is the natural reading of
"attempted" for a call whose whole point is a narrower scope, and it is what
tier 2's own log line needs to say "attempted N of the accounts with a
not-done car" rather than "attempted every account in the platform" on a
run that touched almost none of them. `AccountsSucceeded` /`AccountsFailed`
keep their existing formula (`AccountsAttempted - AccountsFailed`), applied
to this narrower count.

### D8 — An empty `teslaIDs` input is a no-op, not an error

`CollectVehicles(ctx, run, nil)` (or an empty slice) returns a zero
`CycleReport{FailuresByReason: map[Reason]int{}}` and a `nil` error, making
**zero** Tesla API calls. An early return, before calling `enumerateElected`,
skips even the `AllRegisteredVehicles` call — letting the empty filter fall
through to an empty `byAccount` map would work too, but it would spend one
avoidable database read on a call the caller already knows is empty. Tier
2's scheduler is expected to check "is there a not-done car"
itself (roadmap D8: "a tick that finds no not-done car calls nothing and
writes nothing"), so this is a defensive port-level guarantee, not the
primary mechanism — but the port's own contract should not depend on every
caller getting that pre-check right. Test contract: TR-4.

### D9 — Poll election is unmodified and unre-tested at the election level

`electPollingVehicles` itself is untouched (same function, same tie-break
rule) and already has its own full test coverage and its own spec
requirement ("Poll Account Election", generic to "a collection cycle" —
not `CollectAll`-specific). `CollectVehicles` reaches it only through the
shared `enumerateElected` (D5), so D9 of the roadmap ("same account as the
nightly run") is satisfied by construction: there is only one election
function, called once per enumeration, regardless of which method asked for
it. This design adds no new election test — TR-1 below exercises
`CollectVehicles` choosing the correctly-elected account for a
multi-registered vehicle, but it is proving `enumerateElected`'s wiring, not
re-testing the tie-break rule itself.

## Schema

**No database object changes in this tier.** No new table, column, index,
constraint, or view. D6 above is the full migration analysis: the two
`triggered_by` columns accept an arbitrary `text` value already, so adding a
third named constant is a Go-only change. There is nothing to design an
index plan for.

## Signatures

`internal/telemetry/telemetry.go` — `Collector` gains one method:

```go
type Collector interface {
	CollectAll(ctx context.Context, run RunContext) (CycleReport, error)

	// CollectVehicles runs the same collection flow as CollectAll —
	// election, per-account grouping, WakeUp, snapshot fetch/store, and the
	// account's Supercharger history — but scopes the snapshot/poll_attempts
	// work to exactly the given tesla_id set. A tesla_id in teslaIDs that is
	// not currently registered to any account is silently absent from every
	// result: no Tesla call, no poll_attempts row (D3). An empty teslaIDs
	// makes zero Tesla calls and returns a zero CycleReport (D8).
	//
	// The Supercharger history fetch for an account touched by this call
	// still covers that account's FULL registered vehicle list, not only
	// the requested subset (D4) — a session for one of that account's other
	// cars is upserted normally rather than being misreported as
	// unregistered.
	//
	// run identifies this invocation exactly as it does for CollectAll — the
	// caller is expected to pass RunContext{TriggeredBy: TriggeredByRetry}
	// for a retry cycle, but this method does not itself inspect or require
	// any particular TriggeredBy value.
	CollectVehicles(ctx context.Context, run RunContext, teslaIDs []int64) (CycleReport, error)
}
```

`telemetry.go` — new constant next to the existing two:

```go
const (
	TriggeredByScheduler TriggeredBy = "scheduler"
	TriggeredByAPI       TriggeredBy = "api"
	// TriggeredByRetry — internal/app's 30-minute retry schedule (tier 2 of
	// RM69-nightly-retry-unfinished-vehicles), re-running step 1 for the
	// vehicles that did not finish the preceding nightly (or a preceding
	// retry) run.
	TriggeredByRetry TriggeredBy = "retry"
)
```

`service.go` — `collectAccount`'s signature gains one parameter (unexported,
no external caller):

```go
// collectAccount collects every vehicle in `toCollect` for one account …
// chargingScope is the account's vehicle list used ONLY to resolve
// Supercharger session VINs (D4) — it MAY differ from toCollect. CollectAll
// passes the same slice for both (its own scope is always the account's full
// elected list); CollectVehicles passes the retry subset as toCollect and
// the account's full elected list as chargingScope.
func (s *service) collectAccount(ctx context.Context, run RunContext, tsla tesla.VehicleService, accountID uuid.UUID, toCollect []account.OwnedVehicle, chargingScope []account.OwnedVehicle, report *CycleReport)
```

Every existing body line that reads `owned` for the snapshot loop now reads
`toCollect`; the one call `s.collectChargingHistory(ctx, tsla, owned, creds, report)`
becomes `s.collectChargingHistory(ctx, tsla, chargingScope, creds, report)`.
`collectChargingHistory` itself is unchanged — same signature, same body.

`CollectAll`'s only edit: it calls `enumerateElected` instead of inlining
`electPollingVehicles`/`groupByAccount`, and its one `collectAccount` call
site becomes `s.collectAccount(ctx, run, counted, accountID, owned, owned, &report)`.

`CollectVehicles`'s body:

```go
func (s *service) CollectVehicles(ctx context.Context, run RunContext, teslaIDs []int64) (CycleReport, error) {
	report := CycleReport{FailuresByReason: map[Reason]int{}}
	if len(teslaIDs) == 0 {
		return report, nil
	}

	counted := newCallCounter(s.tsla)

	byAccount, err := s.enumerateElected(ctx)
	if err != nil {
		return report, fmt.Errorf("telemetry: enumerating registered vehicles for retry: %w", err)
	}

	want := make(map[int64]bool, len(teslaIDs))
	for _, id := range teslaIDs {
		want[id] = true
	}

	for accountID, owned := range byAccount {
		var toCollect []account.OwnedVehicle
		for _, v := range owned {
			if want[v.TeslaID] {
				toCollect = append(toCollect, v)
			}
		}
		if len(toCollect) == 0 {
			continue
		}
		report.AccountsAttempted++
		s.collectAccount(ctx, run, counted, accountID, toCollect, owned, &report)
	}

	report.AccountsSucceeded = report.AccountsAttempted - report.AccountsFailed
	report.TeslaAPICalls = counted.calls

	return report, nil
}
```

## Docs this change invalidates

- `internal/telemetry/AGENTS.md` — the port table's `Collector` row currently
  lists only `CollectAll`; it must list both methods. Task in `tasks.md`.
- `kkpa/context/architecture/nightly-cycle.md` — its Step 1 file table and
  "Port map" section name `telemetry.Collector` → `CollectAll` as the only
  call. This tier adds no caller of `CollectVehicles` (tier 2 does), so the
  KB update that adds the retry row to the port map belongs to tier 2, not
  here — recorded as a non-goal in `proposal.md` and cross-referenced in
  `tasks.md` so it is not silently dropped between tiers.

## Test contract — expected values, authored before the implementation

### Offline (pure Go, no database) — write these first

All in `internal/telemetry/service_test.go`, reusing the existing
`fakeAccount` / `fakeTesla` / fake `store` doubles already in that file.

**TR-1 — A multi-registered vehicle is retried through its elected account.**
GIVEN vehicle 500 registered to account A (AccessType DRIVER) and account B
(AccessType OWNER), both accounts have a usable token, and vehicle 500 is
online.
WHEN `CollectVehicles(ctx, run, []int64{500})` is called.
THEN exactly one `poll_attempts` row is written for vehicle 500, with
`PolledByAccountID == B` (the OWNER account — the same account
`electPollingVehicles` would pick for `CollectAll`), `Reason == ReasonOK`.
Account A's fake `AccessTokenFor`/`ListVehicles` are never called.

**TR-2 — An unregistered `tesla_id` in the input is skipped, no attempt row.**
GIVEN one account with one registered vehicle, 501.
WHEN `CollectVehicles(ctx, run, []int64{501, 999})` is called (999 is not
registered anywhere).
THEN the fake store receives exactly one `insertPollAttempt` call, for 501.
`report.Attempted == 1`. No error is returned. Nothing distinguishes 999 in
the returned `CycleReport` — its absence from every count IS the "skipped"
outcome (D3).

**TR-3 — `collectChargingHistory` sees the account's full car list, not only the retried one.**
GIVEN one account owning two registered vehicles, 502 (VIN "AAA") and 503
(VIN "BBB"), both online. The fake `ChargingHistory` returns two sessions:
one for VIN "AAA", one for VIN "BBB".
WHEN `CollectVehicles(ctx, run, []int64{502})` is called (503 excluded from
the retry set).
THEN exactly one `poll_attempts` row is written (for 502 only — 503's
snapshot is NOT re-fetched, no WakeUp/VehicleData call for it). BOTH
Supercharger sessions are upserted: `report.ChargingSessionsUpserted == 2`,
`report.ChargingSessionsSkippedUnregistered == 0`. (If `chargingScope` were
narrowed to the retry set, the VIN "BBB" session would wrongly count as
skipped-unregistered — this test fails under that wrong implementation.)

**TR-4 — An empty `teslaIDs` makes zero Tesla calls.**
GIVEN a fake `tesla.VehicleService` that fails the test via `t.Fatal` if any
method is called, and one registered account/vehicle.
WHEN `CollectVehicles(ctx, run, nil)` and `CollectVehicles(ctx, run, []int64{})`
are each called.
THEN both return `CycleReport{FailuresByReason: map[Reason]int{}}` (every
count zero) and a `nil` error. `fakeAccount.AllRegisteredVehicles` is never
called either (D8's early return).

**TR-5 — `CollectAll` behavior is provably unchanged.**
No new test: every existing `CollectAll` test in `service_test.go` passes
unmodified after the `enumerateElected`/`collectAccount` refactor. This is
the regression check for D5/D4's refactor — `go vet` plus the existing suite
(owner-run) is the proof, not a new assertion.

**TR-6 — `report.AccountsAttempted` counts only touched accounts.**
GIVEN three accounts, each with one registered vehicle: 601, 602, 603.
WHEN `CollectVehicles(ctx, run, []int64{601, 603})` is called.
THEN `report.AccountsAttempted == 2` (accounts owning 601 and 603 — the
account owning 602 is never touched, never counted).

### Database-backed (`TEST_DATABASE_URL`-gated) — write these last

New file `internal/telemetry/db_collect_vehicles_integration_test.go`,
mirroring the shape of the existing `db_*_integration_test.go` files
(`testdb.Provision`, real `telemetrydb.Queries`).

**TR-7 — `triggered_by = 'retry'` round-trips through Postgres with no CHECK violation.**
GIVEN a real `telemetrydb.Queries` against a provisioned test database.
WHEN `InsertPollAttempt` is called directly with
`TriggeredBy: string(telemetry.TriggeredByRetry)` (and, separately,
`RunWriter.RecordRun` with the same `TriggeredBy` on a `PollRun`).
THEN both inserts succeed with no error, and a plain `SELECT triggered_by
FROM telemetry.poll_attempts WHERE …` (and the `poll_runs` equivalent)
returns exactly `"retry"`. This is the empirical confirmation of D6 — the
column genuinely accepts the new value with no migration.

**TR-8 — `CollectVehicles` end-to-end against a real store.**
GIVEN the `dbStore` (not the offline fake) wired to a provisioned test
database, one account with two registered vehicles, and a fake `tesla`
double (still fake — no live Fleet API call, per this module's standing
"never wake a car in a test" rule).
WHEN `CollectVehicles` is called for one of the two vehicles.
THEN exactly one row exists in `telemetry.poll_attempts` for that run, with
`triggered_by = 'retry'`, and exactly one row exists in
`telemetry.vehicle_snapshots` for the retried vehicle — none for the other.

## Risks

- **`collectAccount`'s new parameter is easy to pass backwards** (swapping
  `toCollect`/`chargingScope`) since both are `[]account.OwnedVehicle`. TR-3
  is written specifically so an implementation that passes the same slice
  for both, or swaps them, fails visibly (either 503 gets an unwanted
  snapshot fetch, or a real session gets misreported as skipped).
- **`enumerateElected`'s error wrapping text differs between the two
  callers** (`"enumerating registered vehicles"` vs `"… for retry"`) — this
  is intentional (clearer logs), not a shared-code violation; the shared
  code is the three functions named in the roadmap, not the error string.
