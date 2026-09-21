# Design — RM67-app-add-monthly-metrics-step

Source ticket: MAG-73 · Roadmap: `openspec/roadmaps/RM67-vehicle-monthly-metrics-table.md`, tier 4
of 4. Roadmap decisions **RD1-RD10** are binding and were confirmed with the owner at the
2026-09-18 `grill-me` interview. This document does not re-open them. RD3, RD6 and RD10 are this
tier's own scope; RD1, RD2, RD4, RD5, RD7, RD8, RD9 were implemented and archived by tiers 1-3.

**No database design gate.** This change adds no table, column, index, constraint, or migration.
It reads no database directly. `openspec/config.yaml` §design's database gate
(`CLAUDE.md` §Pipeline config → `Design-Gates: database`) does not apply — recorded explicitly so
this is a finding, not an assumption.

---

## Context

Facts that constrain every decision here. Each was read out of the repository, not recalled.

1. **`ProcessVehicleData(ctx, triggeredBy)` runs once per cycle, not once per vehicle.** It takes
   no vehicle argument. The new step therefore enumerates vehicles itself, the same way
   `processChargingData` and `recalculateAnalytics` already do — never a caller-supplied list
   (`internal/app/processor.go`).
2. **Step 5 runs AFTER step 4 in the same cycle, and the order is load-bearing on the 1st of the
   month.** On that day, step 4 (`runMonthlyCapacityStep`) measures the PREVIOUS month's pack
   capacity and writes `charging.monthly_effective_capacity` for that period. Step 5's own
   previous-month sync (`analytics.MonthlySyncer.SyncMonth`) reads that same table through
   `charging.MonthlyCapacityReader.CapacityForMonth` and copies the figure into
   `vehicle_monthly_metrics`. If step 5 ran before step 4, the previous month's capacity sync
   would copy whatever was measured a month earlier (or nothing, on the very first month) instead
   of the figure step 4 just wrote — a full sync cycle of staleness. Placing step 5 immediately
   after step 4 removes that gap entirely, at zero extra cost, since both steps already run every
   night regardless of order.
3. **Its current shape is a short-circuit, not five independent calls.** Step 1
   (`p.collector.CollectAll`) returns `(report, err)`. Steps 2-4 run only inside
   `if err == nil { ... }`, and none of them return anything — they log and isolate their own
   failures internally (`processor.go:73-79`). The new step 5 joins that same block, as the last
   line.
4. **`internal/app` must not import `internal/analytics/db`.** It already imports
   `internal/analytics` for `Recalculator`, `Reader`, and `GapWriter` — all public ports, never the
   generated `analyticsdb` package (`internal/app/AGENTS.md` §Allowed/forbidden imports). This
   change adds a fourth `analytics` port, `MonthlySyncer`, to the same allow-listed import — no new
   import path.
5. **`NewProcessor`'s eleven parameters are grouped by owning module** — three `telemetry` ports,
   three `charging` ports, one `account` port, three `analytics` ports, then `loc`
   (`internal/app/app.go`). The natural slot for a twelfth, `analytics`-owned parameter is beside
   the other three `analytics` ports, not at the end.
6. **`clock.Now()` and `clock.Zone()` are the only sanctioned way to read "now" and the default
   zone** (`ai/go-conventions.md` §"platform's default time zone"; `make tz-guard` enforces it
   repo-wide). `clock.CalendarDay(t, loc)` normalizes a moment to its calendar day in `loc`,
   expressed at UTC midnight — the same representation `pgtype.Date` expects
   (`internal/clock/calendar_day.go`). This is the exact same pair `runMonthlyCapacityStep` already
   uses for step 4 (RM52 tier 2 D1) — RD3 says step 5 "uses the platform zone through
   `internal/clock`" too, the same knob, not the poller's configurable `p.loc`.
7. **`monthlyCapacityPeriod` (`processor.go`) is the existing precedent for a pure, clock-free date
   function taking `now`/`loc` as parameters rather than reading the clock itself.** This tier's new
   period function follows the same shape, with one structural difference: `monthlyCapacityPeriod`
   returns a `run bool` gate because step 4 only fires on the 1st of the month; step 5's function
   has no such gate, because RD3 makes it fire every night — it only ever decides WHICH two months,
   never WHETHER to sync.
8. **`analytics.MonthlySyncer.SyncMonth(ctx context.Context, teslaID int64, period time.Time)
   (analytics.VehicleMonthlyMetrics, error)`** is tiers 2/3's already-built, already-archived port
   (`internal/analytics/analytics.go`). Its own doc comment states: "only `period`'s year and
   calendar month matter; any day within that month gives the same result." Running it again for
   the same `(teslaID, period)` rewrites the row — it is a sync, not an append — so calling it every
   night for the same two months is exactly its intended usage, not a special case this tier must
   guard against.
9. **`analytics.NewMonthlySyncer(pool *pgxpool.Pool, capacity charging.MonthlyCapacityReader,
   charges charging.Reader, supercharger charging.SuperchargerSessionAnalyticsReader)
   analytics.MonthlySyncer`** is the only exported constructor for that port
   (`internal/analytics/analytics.go`). `cmd/poller/main.go` already builds `chargingReader`
   (`charging.NewReader(pool)`) and `sessionAnalyticsReader`
   (`charging.NewSuperchargerSessionAnalyticsReader(pool)`) locally for `analytics.NewRecalculator`
   — both are reusable here unchanged. Only `charging.NewMonthlyCapacityReader(pool)` is a new
   construction at that call site.
10. **Both step 2 and step 3 already enumerate vehicles the same way**: call
    `p.acct.AllRegisteredVehicles(ctx)`, then de-duplicate by `TeslaID` into a local slice, because
    a car registered to two accounts must be processed once, not twice
    (`processChargingData`, `recalculateAnalytics`). Step 5 is keyed on the car (`SyncMonth` takes
    `teslaID`, not an account), so it follows the identical dedup shape rather than inventing a
    third one.
11. **`internal/app`'s existing test fake roster** (`processor_test.go`) has one fake per
    `Processor` collaborator, wired through `newTestProcessor`. This tier adds one more fake,
    `fakeMonthlySyncer`, following the same shape as `fakeMonthlyCapacityCalculator`
    (RM52 tier 2) — except it must record a call PER `(teslaID, period)` pair, since this step
    calls the port multiple times per invocation (two periods × N vehicles), unlike step 4's single
    call.
12. **`internal/app`'s testing convention accepts a specific, named coverage gap for wall-clock
    wiring.** `runMonthlyCapacityStep`'s own `clock.Now()`/`clock.Zone()` read has no dedicated unit
    test — `internal/app/AGENTS.md` §Testing notes documents this as a deliberate, accepted gap.
    This tier's own `runMonthlyMetricsStep` reads the clock the same way, for the same reason, and
    inherits the same accepted-gap treatment (see D3).

## Goals / Non-Goals

**Goals**

- Step 5 of `ProcessVehicleData`: calls `analytics.MonthlySyncer.SyncMonth` for the current month
  and the previous month, for every distinct vehicle, every night — following the existing step-1
  short-circuit (RD3).
- The two-period computation is pure, unit-tested, and zone-correct via `internal/clock` — never a
  raw `time.Now()`, a hardcoded zone string, or a hand-rolled UTC-midnight truncation (RD6).
- Failures are isolated per `(vehicle, period)` pair: one vehicle's failure never blocks another
  vehicle, and a vehicle's current-month failure never blocks its own previous-month call.
- `NewProcessor`'s new parameter and `cmd/poller/main.go`'s updated call site are the only places
  outside `internal/app` this change touches (plus the docs listed in proposal.md).

**Non-Goals**

- Any change to `internal/analytics` or `internal/charging` — tiers 1-3 already built and archived
  every port this tier calls (done).
- Any gateway or read surface for `vehicle_monthly_metrics` — MAG-87, a separate future ticket.
- Backfilling more than the trailing two months automatically — RD3 is explicit: the window is
  bounded to current + previous, every night. An older month is fixed by a manual, out-of-band
  call to `SyncMonth` for that exact `(teslaID, period)` — no such tool exists yet, and building one
  is not this tier's job.
- A database object of any kind — this tier calls an existing port; it owns nothing.
- A completeness flag on the current month's row — RD3 explicitly rejected one: "a reader compares
  `period` against today, so a flag would be derived state."

---

## Decisions

### D1 — The step reads `clock.Zone()`, the platform default, not `p.loc`

**Not a roadmap decision to re-derive — a direct reading of RD3's own wording** ("the month gate
uses the platform zone through `internal/clock`"), made explicit because `p.loc` already exists on
`processor` and could look like the obvious choice, exactly as it did for step 4 (RM52 tier 2 D1).

```go
current, previous := monthlyMetricsPeriods(clock.Now(), clock.Zone())
```

`p.loc` is the poller's own **configurable** zone (`POLLER_TIMEZONE`), used today only by
`recalculateAnalytics`'s "yesterday" math (step 3) — a different, narrower thing than the
platform's fixed default. The two happen to agree in every deployment today, but nothing enforces
that, and RD3 names the platform default specifically.

| Alternative | Why rejected |
|---|---|
| Reuse `p.loc` | Contradicts RD3's literal wording. Would also silently change behavior if a deployment ever sets `POLLER_TIMEZONE` to something else, with no test able to catch it since RD3's own contract is about the fixed zone. |
| Add a new `*time.Location` parameter to `NewProcessor` just for this step | Rejected: `internal/app` needs no zone of its own beyond `clock.Zone()`, which is free to call from anywhere. An unnecessary parameter is exactly the over-abstraction `CLAUDE.md` §Non-negotiables warns against. |

### D2 — `monthlyMetricsPeriods` always returns both months; it has no "should I run" gate

**RD3's direct consequence.** Unlike `monthlyCapacityPeriod` (step 4), which gates on "is today the
1st", step 5 runs every night — there is nothing to gate. The function's only job is deciding WHICH
two months.

```go
// monthlyMetricsPeriods returns the first instant of the current calendar
// month and of the month before it, both anchored to now's calendar day in
// loc (RD3). Unlike monthlyCapacityPeriod, this has no "should I run" gate --
// step 5 runs every night, so the only question is which two months, never
// whether to sync at all.
func monthlyMetricsPeriods(now time.Time, loc *time.Location) (current, previous time.Time) {
	today := clock.CalendarDay(now, loc)
	current = time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	previous = current.AddDate(0, -1, 0)
	return current, previous
}
```

`current.AddDate(0, -1, 0)` is safe against month-length overflow for the same reason
`monthlyCapacityPeriod`'s equivalent line is: `current` is always constructed as the 1st of a
month, and subtracting one month from the 1st of any month always lands on the 1st of the previous
month — there is no 31st-of-a-shorter-month edge case to guard against.

`today.Year()`/`today.Month()` are read from `clock.CalendarDay`'s result, not from `now` directly
— this is what makes the function zone-correct: `today` already reflects `loc`'s own calendar day,
so building the 1st of `today`'s month is correct regardless of which zone `now` was originally
expressed in.

| Alternative | Why rejected |
|---|---|
| Return a `run bool` like `monthlyCapacityPeriod`, always `true` | Rejected: a gate value that is always `true` is dead weight — it would force every caller to check a condition that can never be false, for no benefit. `monthlyCapacityPeriod`'s gate exists because it is sometimes `false`; this function's isn't. |
| Compute `previous` via `today.AddDate(0, 0, -today.Day())` truncated to month start | Rejected: more complex than constructing `current` directly from `today.Year()`/`today.Month()`, for the same result — and it revisits day-of-month arithmetic that the direct construction avoids entirely. |

### D3 — `runMonthlyMetricsStep` is a thin, wall-clock-reading wrapper; the period math is pure and fully tested

**Not a roadmap decision — the testability shape for RD3/RD6, following Context facts 7 and 12,
mirroring `monthlyCapacityPeriod`/`runMonthlyCapacityStep`'s own split (RM52 tier 2 D2).**

```go
// runMonthlyMetricsStep is the "sync monthly metrics" step (RM67 tier 4,
// RD3). Reads the real clock (D1), delegates the two-period decision to
// monthlyMetricsPeriods (pure, fully unit-tested), then enumerates
// registered vehicles the same way processChargingData and
// recalculateAnalytics already do -- deduplicated by TeslaID, since the use
// case is keyed on the car, not the account. For each vehicle it calls the
// use case twice: current month, then previous month.
func (p *processor) runMonthlyMetricsStep(ctx context.Context) {
	current, previous := monthlyMetricsPeriods(clock.Now(), clock.Zone())

	vehicles, err := p.acct.AllRegisteredVehicles(ctx)
	if err != nil {
		logging.Note("Processor", "runMonthlyMetricsStep", "listing vehicles: %v", err)
		return
	}

	teslaIDs := make([]int64, 0, len(vehicles))
	for _, v := range vehicles {
		if slices.Contains(teslaIDs, v.TeslaID) {
			continue
		}
		teslaIDs = append(teslaIDs, v.TeslaID)
	}

	for _, teslaID := range teslaIDs {
		p.callMonthlySyncer(ctx, teslaID, current)
		p.callMonthlySyncer(ctx, teslaID, previous)
	}
}

// callMonthlySyncer calls the use case for one vehicle and one period, and
// logs the outcome. Split out from runMonthlyMetricsStep so this half is
// testable with a fake and a fixed period, with no clock and no vehicle
// enumeration involved (design.md D3). Errors are logged, never fatal: the
// same "errors are logged, isolated" pattern every other step in this file
// uses -- a missed month self-heals the next night, since SyncMonth rewrites
// the row rather than appending to it (Context fact 8).
func (p *processor) callMonthlySyncer(ctx context.Context, teslaID int64, period time.Time) {
	if _, err := p.monthlySyncer.SyncMonth(ctx, teslaID, period); err != nil {
		logging.Note("Processor", "callMonthlySyncer", "vehicle %d: period %s: %v", teslaID, period.Format("2006-01"), err)
		return
	}
	logging.Note("Processor", "callMonthlySyncer", "vehicle %d: period %s: synced", teslaID, period.Format("2006-01"))
}
```

Calling `callMonthlySyncer` twice in sequence, uncoupled by any shared `if`/`err` check, is what
gives failure isolation between a vehicle's current-month and previous-month calls: a failing
current-month call returns from `callMonthlySyncer`, not from `runMonthlyMetricsStep`, so the very
next line still runs the previous-month call for the same vehicle.

**What is and is not covered, stated plainly so the reviewer does not read this as a gap:**

| Function | Pure? | Unit-tested? | Why |
|---|---|---|---|
| `monthlyMetricsPeriods` | yes | **yes** — Group A below | No I/O; this is where the actual RD3/RD6 month-selection logic lives |
| `callMonthlySyncer` | no (calls the port) | **yes** — Group B below | No clock, no vehicle enumeration; a fake port and a fixed `(teslaID, period)` make it deterministic |
| `runMonthlyMetricsStep` | no (reads `clock.Now()`) | partial — the vehicle loop and dedup are covered through the fake roster (Group C); the `clock.Now()`/`clock.Zone()` read itself is an accepted gap, same as `runMonthlyCapacityStep`'s | The wall-clock read has no injectable seam, exactly the category `internal/app/AGENTS.md` §Testing notes already accepts for step 4 |

| Alternative | Why rejected |
|---|---|
| Add a clock seam to `Processor` (a `now func() time.Time` field) | Rejected: `Processor` deliberately has no clock seam today (RM36 tier 2's own decision, reaffirmed by RM52 tier 2 D2). Adding one now, for one step, would set a precedent this change has no mandate to set. |
| One function, no split | Rejected: would make the whole thing untestable, including the RD3/RD6 period logic itself — the piece of new logic that most needs a deterministic test. |

### D4 — The new `NewProcessor` parameter's position

**Not a roadmap decision — a direct application of Context fact 5's existing grouping rule.**

```go
func NewProcessor(
	collector telemetry.Collector,
	superchargerHistoryReader telemetry.SuperchargerHistoryReader,
	runWriter telemetry.RunWriter,
	sessionWriter charging.SessionWriter,
	mirrorWatermarks charging.MirrorWatermarkStore,
	monthlyCapacityCalculator charging.MonthlyCapacityCalculator,
	acct account.Service,
	recalculator analytics.Recalculator,
	analyticsReader analytics.Reader,
	gapWriter analytics.GapWriter,
	monthlySyncer analytics.MonthlySyncer, // NEW — fourth analytics port
	loc *time.Location,
) Processor
```

Placed immediately after `gapWriter`, keeping all four `analytics` ports contiguous — the same
grouping-by-owning-module the existing eleven parameters already follow, and `loc` stays last since
it is not owned by any module. Every call site (`cmd/poller/main.go`, `newTestProcessor` in
`processor_test.go`) updates in the same change.

| Alternative | Why rejected |
|---|---|
| Append at the very end, after `loc` | Rejected: breaks the existing grouping-by-module convention — a reader scanning the parameter list for "what does `analytics` need" would have to check two places instead of one. |

### D5 — No spec capability split; this is a delta to the existing `process-vehicle-data` capability

**Not a roadmap decision — an OpenSpec authoring choice**, the same one `RM52-app-add-monthly-
capacity-step` made (its own D4). `process-vehicle-data`
(`openspec/specs/process-vehicle-data/spec.md`) already describes this operation as a fixed-order
sequence of steps with a short-circuit. This tier adds a fifth step to the same operation, so it is
a delta to the same capability, not a new one — MODIFYING the "steps in order" requirement to five,
MODIFYING the short-circuit requirement to cover the new step, and ADDING one requirement for the
every-night, two-period sync itself. See `specs/process-vehicle-data/spec.md` in this change.

---

## Date/clock design — summary

| Question | Answer | Where |
|---|---|---|
| Which zone decides "current" and "previous" month? | The platform default, `clock.Zone()` (`America/Bogota`) — **not** `p.loc` | D1 |
| Which moment is read? | `clock.Now()` — never a raw `time.Now()` | D1, D3 |
| Does the step ever skip a night? | No — RD3 runs it every night, unlike step 4 | D2 |
| How is "current month" computed? | `time.Date(today.Year(), today.Month(), 1, 0,0,0,0, time.UTC)`, where `today = clock.CalendarDay(now, loc)` | D2, `monthlyMetricsPeriods` |
| How is "previous month" computed? | `current.AddDate(0, -1, 0)`, safe because `current` is always the 1st | D2 |
| Is the period function unit-tested? | Yes, fully — `monthlyMetricsPeriods` is pure (Group A) | D2 |
| Is the wiring around it unit-tested? | Partial — the vehicle loop/dedup is covered (Group C); the raw `clock.Now()` read is an accepted gap, same category as step 4's | D3 |

---

## Test Contract

Expected values authored **before** implementation, per `ai/go-conventions.md` §Testing ("author
their expected values up front, in the change's `design.md`"). **Tests written later must assert
THIS contract**, not whatever the implementation happens to produce.

**Conventions**, following `internal/app`'s existing ones (`processor_test.go`, `monthly_capacity_
step_test.go`): fresh `uuid.New()` where an id is needed; fakes satisfy the exact port interface
(`var _ analytics.MonthlySyncer = (*fakeMonthlySyncer)(nil)`); table-style cases where the
input/output shape repeats.

### Group A — `monthlyMetricsPeriods` (pure, no fakes, no DB)

New file `internal/app/monthly_metrics_step_test.go`.

| ID | `now` | `loc` | Expected `(current, previous)` | What it proves |
|---|---|---|---|---|
| **A1** | `time.Date(2026,9,1,0,30,0,0,bogota)` | `America/Bogota` | `(2026-09-01, 2026-08-01)` | The plain case: today is the 1st of the month — `current` is that same month, `previous` is the month before it. |
| **A2** | `time.Date(2026,9,15,12,0,0,0,bogota)` | `America/Bogota` | `(2026-09-01, 2026-08-01)` | **Mid-month**: whatever day of the month `now` falls on, `current` still normalizes to the 1st of that same month — unlike step 4, there is no day-of-month gate to pass. |
| **A3** | `time.Date(2027,1,1,10,0,0,0,bogota)` | `America/Bogota` | `(2027-01-01, 2026-12-01)` | **Year boundary**: the previous month of January is December of the PRIOR year, not month `0`. |
| **A4** | `time.Date(2026,9,1,3,0,0,0,time.UTC)` — this UTC instant is `2026-08-31 22:00` in Bogota (UTC-5) | `America/Bogota` | `(2026-08-01, 2026-07-01)` | **Zone-aware boundary, the load-bearing case.** The UTC calendar day is the 1st of September, but the Bogota calendar day is still the 31st of August — a naive UTC-day check would wrongly report September as current. This is the direct analogue of `monthlyCapacityPeriod`'s own A4. |
| **A5** | `time.Date(2026,9,1,6,0,0,0,time.UTC)` — this UTC instant is `2026-09-01 01:00` in Bogota | `America/Bogota` | `(2026-09-01, 2026-08-01)` | The zone-aware counterpart to A4: a UTC instant whose UTC day is also the 1st correctly reports September as current here too, for the right reason (it IS the 1st in Bogota) — not by coincidence. |
| **A6** | `time.Date(2026,3,15,12,0,0,0,time.UTC)` | `time.UTC` | `(2026-03-01, 2026-02-01)` | `loc` is a real parameter, not hardcoded to Bogota inside the function — passing `time.UTC` still works correctly. `clock.Zone()` is what supplies Bogota at the one real call site (D1), not this function. |

### Group B — `callMonthlySyncer` (fake port, no clock)

Same file.

| ID | Fake `SyncMonth` returns | Call | Expected | What it proves |
|---|---|---|---|---|
| **B1** | `(analytics.VehicleMonthlyMetrics{}, nil)` | `callMonthlySyncer(ctx, 42, 2026-09-01)` | The fake recorded exactly one call with `teslaID == 42` and `period == 2026-09-01`; no error propagates (the function has no return value) | The happy path: the port is called with the right arguments. |
| **B2** | `(analytics.VehicleMonthlyMetrics{}, errors.New("db down"))` | `callMonthlySyncer(ctx, 42, 2026-09-01)` | The fake still recorded exactly one call with the same arguments; the function returns nothing — the error is logged and swallowed, never propagated | Errors from the monthly sync never become a `ProcessVehicleData` error — the same isolation every other step already guarantees. |

### Group C — `runMonthlyMetricsStep` / `ProcessVehicleData` wiring (through the fake roster)

`internal/app/monthly_metrics_step_test.go` for C1-C4 (a fake `account.Service` plus
`fakeMonthlySyncer`, no full `newTestProcessor` needed); `internal/app/processor_test.go` for C5.

| ID | Setup | Expected | What it proves |
|---|---|---|---|
| **C1** | Fake roster returns two distinct vehicles (`teslaID` 1 and 2); `fakeMonthlySyncer` always succeeds | `fakeMonthlySyncer` recorded exactly 4 calls: `(1, current)`, `(1, previous)`, `(2, current)`, `(2, previous)` — current before previous, for each vehicle | The two-call-per-vehicle shape (RD3) and the current-before-previous order. |
| **C2** | Same two vehicles; `fakeMonthlySyncer` errors ONLY for `(teslaID: 1, period: current)` | All 4 calls still happen: `(1, current)` errors and is logged, but `(1, previous)`, `(2, current)`, `(2, previous)` all still run | **The load-bearing isolation case**: a vehicle's current-month failure never blocks its own previous-month call, nor another vehicle's calls. |
| **C3** | Same two vehicles; `fakeMonthlySyncer` errors for EVERY call belonging to `teslaID: 1` (both periods), succeeds for `teslaID: 2` | `teslaID: 2`'s two calls both still happen and both succeed | One vehicle's total failure never blocks another vehicle. |
| **C4** | `account.Service.AllRegisteredVehicles` returns an error | `fakeMonthlySyncer.calls == 0`; the step returns immediately, logged | Mirrors `processChargingData`'s/`recalculateAnalytics`'s own enumeration-failure shape — a listing failure is a whole-step failure, not per-vehicle. |
| **C5** | `fakeCollector` returns an error (the existing step-1 whole-cycle-failure fixture) | `fakeMonthlySyncer.calls == 0` | **Step 5 is skipped by the same short-circuit as steps 2-4** — deterministic regardless of the real calendar date, because on the `err != nil` branch `runMonthlyMetricsStep` (and therefore `clock.Now()`) is never reached at all. |

A duplicate-`TeslaID` dedup case (mirroring `processChargingData`'s own dedup test) belongs in the
same file: two vehicle entries sharing one `teslaID` (simulating one car registered to two
accounts) must still produce exactly 2 calls for that `teslaID` (current + previous), not 4.

---

## Roadmap-decision mapping

| design.md | roadmap | Subject |
|---|---|---|
| D1 | **RD3** | reads `clock.Zone()`, not `p.loc` |
| D2 | **RD3, RD6** | the period function has no gate; always both months |
| D3 | **RD3** | the wall-clock wrapper vs. the pure/tested split |
| D4 | — | `NewProcessor`'s new parameter position (module-grouping convention) |
| D5 | — | delta targets the existing `process-vehicle-data` capability |
| Context fact 8 | **RD8** | `SyncMonth` is a sync, not an append — safe to call every night for the same period |
| Context fact 10 | — | vehicle enumeration mirrors steps 2/3's existing dedup shape |

RD1, RD2, RD4, RD5, RD7, RD8, RD9 are tiers 1-3's and are not re-implemented here. RD10 (the naming
constraint) is honored by adding no new exported type at all — see "New Go type" below.

## New Go type

**None.** This tier adds two new PRIVATE functions (`monthlyMetricsPeriods`,
`callMonthlySyncer`) and one new method (`runMonthlyMetricsStep`) on the existing, unexported
`processor` type. No new type is declared, so RD10's banned-suffix rule
(`Processor`/`Manager`/`Handler`/`Helper`/`Data`/`Info`/`Object`/`Thing`) has nothing to apply to.
This mirrors step 4 (RM52 tier 2), which also added no new type.

## Risks

1. **A missed night (the poller down, or `SyncMonth` erroring for both periods) is not retried by
   a dedicated mechanism.** This is accepted by construction: the very next night's run re-syncs
   the same current/previous window, and `SyncMonth` rewrites rather than appends, so a missed
   night self-heals within at most one night's delay (Context fact 8). A gap older than the
   trailing two months requires a manual, out-of-band call — no tool for that exists yet, and
   building one is not this tier's job (RD3's own accepted trade-off).
2. **`POLLER_TIMEZONE` diverging from `America/Bogota` in some future deployment** would make step
   5's "current/previous month" (via `clock.Zone()`) disagree with step 3's "yesterday" (via
   `p.loc`) about what "today" means. This is the same accepted risk RM52 tier 2 D1 already
   recorded for step 4 — RD3 names the fixed platform zone on purpose.
