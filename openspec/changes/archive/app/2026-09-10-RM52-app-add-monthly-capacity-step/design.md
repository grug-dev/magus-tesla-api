# Design — RM52-app-add-monthly-capacity-step

Source ticket: MAG-32 · Roadmap: `openspec/roadmaps/RM52-vehicle-monthly-metrics.md`, tier 2 of 3.
Roadmap decisions **RD1–RD14** are binding and were confirmed with the owner at the 2026-09-10
`grill-me` interview. This document does not re-open them. RD6 and RD7 are this tier's own scope;
RD1–RD5 and RD9–RD11 were implemented and archived by tier 1
(`openspec/changes/archive/charging/2026-09-10-RM52-charging-add-monthly-effective-capacity/`).

**No database design gate.** This change adds no table, column, index, constraint, or migration.
It reads no database directly. `openspec/config.yaml` §design's database gate
(`CLAUDE.md` §Pipeline config → `Design-Gates: database`) does not apply — recorded explicitly so
this is a finding, not an assumption.

---

## Context

Facts that constrain every decision here. Each was read out of the repository, not recalled.

1. **`ProcessVehicleData(ctx, triggeredBy)` runs once per cycle, not once per vehicle.** It takes
   no vehicle argument. So the new step is ONE call scoped to every vehicle (`teslaID` nil), never
   a per-vehicle loop (`internal/app/processor.go:69`).
2. **Its current shape is a short-circuit, not three independent calls.** Step 1
   (`p.collector.CollectAll`) returns `(report, err)`. Steps 2 and 3 run only inside
   `if err == nil { ... }`, and both return nothing — they log and isolate their own failures
   internally (`processor.go:73-77`). The new step 4 joins that same block, in the same position
   (RD6: "after step 3", "follows the existing short-circuit").
3. **`internal/app` must not import `internal/charging/db`.** It already imports `internal/charging`
   for `SessionWriter` and `MirrorWatermarkStore` — both public ports, never the generated
   `chargingdb` package (`internal/app/AGENTS.md` §Allowed/forbidden imports). This change adds a
   third `charging` port, `MonthlyCapacityCalculator`, to the same allow-listed import — no new
   import path.
4. **`NewProcessor`'s ten parameters are grouped by owning module** — three `telemetry` ports, two
   `charging` ports, one `account` port, three `analytics` ports, then `loc` (`internal/app/app.go:82-93`).
   The natural slot for an eleventh, `charging`-owned parameter is beside the other two `charging`
   ports, not at the end.
5. **`p.loc` is the poller's *configurable* zone** (`POLLER_TIMEZONE`, `cmd/poller/main.go:91-93`),
   used today only by `recalculateAnalytics`'s "yesterday" math. It is loaded via
   `time.LoadLocation(cfg.PollerTimezone)` — not guaranteed to equal `America/Bogota`, even though
   it is configured that way in practice. RD7 names a specific zone: "read through
   `internal/clock`, in `America/Bogota`" — the platform's fixed default, obtained via
   `clock.Zone()`, not the poller's configurable one. This tier's step therefore reads
   `clock.Zone()` directly and does **not** reuse `p.loc` (see D1).
6. **`clock.Now()` and `clock.Zone()` are the only sanctioned way to read "now" and the default
   zone** (`ai/go-conventions.md` §"platform's default time zone"; `make tz-guard` enforces it
   repo-wide). `clock.CalendarDay(t, loc)` normalizes a moment to its calendar day in `loc`,
   expressed at UTC midnight — the same representation `pgtype.Date` expects
   (`internal/clock/calendar_day.go`).
7. **`nextRun` in `scheduler.go` is the existing precedent for a pure, clock-free date function**:
   it takes `now time.Time` as a parameter rather than reading `clock.Now()` itself, so it is
   directly unit-testable (`internal/app/scheduler.go:86`). This tier's new gate function follows
   the same shape.
8. **`charging.MonthlyCapacityCalculator.Calculate(ctx, period time.Time, teslaID *int64)
   (charging.MonthlyCapacityReport, error)`** is tier 1's already-built, already-archived port
   (`internal/charging/charging.go:663-671`). Its own doc comment states: "`period` must be the
   first instant of the month to compute; this module does not compute 'now' or 'the previous
   month' itself... the tier-2 caller decides both and passes the result in." This tier is that
   caller. `teslaID` nil means every vehicle (RD8's "no `-tesla-id`" case) — this tier always
   passes `nil`; scoping to one vehicle is tier 3's manual `cmd/monthly-capacity` flag, not
   anything the nightly cycle does.
9. **`charging.NewMonthlyCapacityCalculator(pool *pgxpool.Pool) charging.MonthlyCapacityCalculator`**
   is the only exported constructor for that port (`internal/charging/charging.go:674-679`).
   `cmd/poller/main.go` already holds the shared `pool` every other port is built from.
10. **`internal/app`'s existing test fake roster** (`processor_test.go`) has one fake per
    `Processor` collaborator, wired through `newTestProcessor`, following design D9 of
    `RM36-app-record-poll-run`: real behavior only where a fixture needs it, otherwise a
    zero-value stub that satisfies the interface and stays out of the way. This tier adds one
    more fake to that roster, `fakeMonthlyCapacityCalculator`, following the same shape.
11. **`internal/app`'s testing convention accepts a specific, named coverage gap for wall-clock
    wiring.** `recalculateAnalytics`'s own "yesterday" computation (`clock.CalendarDay(clock.Now(),
    p.loc)`) has no dedicated unit test — `internal/app/AGENTS.md` §Testing notes documents this as
    a deliberate, accepted gap for orchestration code that reads the real clock directly, with no
    injectable seam. This tier's own `runMonthlyCapacityStep` reads `clock.Now()` the same way, for
    the same reason, and inherits the same accepted-gap treatment (see D2).

## Goals / Non-Goals

**Goals**

- Step 4 of `ProcessVehicleData`: calls tier 1's `MonthlyCapacityCalculator.Calculate` for the
  previous month, once a month, only on the month's first day, following the existing step-1
  short-circuit (RD6).
- The month-boundary math is pure, unit-tested, and zone-correct via `internal/clock` — never a
  raw `time.Now()`, a hardcoded zone string, or a hand-rolled UTC-midnight truncation (RD7).
- `NewProcessor`'s new parameter and `cmd/poller/main.go`'s updated call site are the only places
  outside `internal/app` this change touches.

**Non-Goals**

- Any change to `internal/charging` — tier 1 already built and archived `MonthlyCapacityCalculator`
  (done).
- `cmd/monthly-capacity`, the `make` target, and the docs/KB updates that name it — tier 3.
- Any gateway surface — `internal/gateway` is never touched by this roadmap (RD8).
- Backfilling any past month automatically — RD9 is explicit: no automatic backfill, ever. Past
  periods are computed by tier 3's manual tool.
- A database object of any kind — this tier calls an existing port; it owns nothing.

---

## Decisions

### D1 — The step reads `clock.Zone()`, not `p.loc`

**Not a roadmap decision to re-derive — a direct reading of RD7's own wording, made explicit
because `p.loc` already exists on `processor` and could look like the obvious choice.**

RD7 says: *"'First day of the month' is read through `internal/clock`, in `America/Bogota`."* That
names the platform's fixed default zone, obtained via `clock.Zone()`. `p.loc` is a different,
narrower thing: the poller's own **configurable** zone (`POLLER_TIMEZONE`), used today only by
`recalculateAnalytics`'s "yesterday" math (Context fact 5). The two happen to agree in every
deployment today, but nothing enforces that, and RD7 names the platform default specifically —
not "whatever zone the poller happens to be configured with."

```go
period, run := monthlyCapacityPeriod(clock.Now(), clock.Zone())
```

`clock.Now()` already returns the current moment expressed in `clock.Zone()`
(`internal/clock/clock.go`'s own doc comment: "the current moment expressed in the platform's
default zone"). Passing `clock.Zone()` into `CalendarDay` alongside it is not redundant — it says,
explicitly and at the call site, which zone this calendar-day computation uses, matching every
other `clock.CalendarDay(clock.Now(), <zone>)` call site in this file (`recalculateAnalytics`'s own
line uses the same shape, with `p.loc` in its own zone slot).

| Alternative | Why rejected |
|---|---|
| Reuse `p.loc` | Contradicts RD7's literal wording ("in `America/Bogota`", the platform default — not the poller's configurable zone). Would also silently change behavior if a deployment ever sets `POLLER_TIMEZONE` to something else, with no test able to catch it since RD7's own contract is about the fixed zone. |
| Add a new `*time.Location` parameter to `NewProcessor` just for this step | Rejected: `internal/app` needs no zone of its own beyond `clock.Zone()`, which is free to call from anywhere and needs no threading through the constructor — an unnecessary parameter is exactly the over-abstraction `CLAUDE.md` §Non-negotiables warns against. |

### D2 — `runMonthlyCapacityStep` is a thin, wall-clock-reading wrapper; the gate itself is a pure, fully-tested function

**Not a roadmap decision — the testability shape for RD6/RD7, following Context facts 7 and 11.**

The date/clock logic is split into two functions with different testing treatment, mirroring how
`buildPollRun`/`recordRun` split pure mapping from I/O in `RM36-app-record-poll-run`:

```go
// monthlyCapacityPeriod applies RD6/RD7's gate: pure over its inputs, no clock
// read of its own -- mirrors nextRun's shape (scheduler.go). now is a moment
// already resolved to the platform's own zone; loc is the SAME zone, passed
// explicitly so the function's own behavior does not depend on which zone now
// happens to already be expressed in. Returns run=false on every day but the
// first of the month. On the first, returns the PREVIOUS month's first
// instant -- charging.MonthlyCapacityCalculator.Calculate's own contract
// ("period must be the first instant of the month to compute").
func monthlyCapacityPeriod(now time.Time, loc *time.Location) (period time.Time, run bool) {
	today := clock.CalendarDay(now, loc)
	if today.Day() != 1 {
		return time.Time{}, false
	}
	return today.AddDate(0, -1, 0), true
}
```

`today.AddDate(0, -1, 0)` is safe against month-length overflow because `today.Day()` is always
`1` at that point — subtracting one month from the 1st of any month always lands on the 1st of the
previous month, with no 31st-of-a-shorter-month edge case to guard against.

```go
// runMonthlyCapacityStep is the "monthly capacity" step (design.md D2, RM52 tier
// 2). Reads the real clock (D1) and delegates the actual gate decision to
// monthlyCapacityPeriod, which is pure and fully unit-tested. This function's
// own body -- the clock.Now()/clock.Zone() read plus the branch on run -- is
// deliberately NOT unit-tested, for the same accepted reason
// recalculateAnalytics's own "yesterday" line is not: it reads the real wall
// clock directly, with no injectable seam, exactly like every other line in
// this file that calls clock.Now() (Context fact 11).
func (p *processor) runMonthlyCapacityStep(ctx context.Context) {
	period, run := monthlyCapacityPeriod(clock.Now(), clock.Zone())
	if !run {
		return
	}
	p.callMonthlyCapacityCalculator(ctx, period)
}

// callMonthlyCapacityCalculator calls the tier-1 port for one period and logs
// the outcome. Split out from runMonthlyCapacityStep so this half -- the part
// that actually calls the calculator and decides what to log -- is testable
// with a fake and a fixed period, with no clock involved (design.md D2).
// Errors are logged, never fatal: the same "errors are logged, isolated"
// pattern processChargingData and recalculateAnalytics already use -- a missed
// month self-heals next month, and RD9's manual tool covers backfill.
func (p *processor) callMonthlyCapacityCalculator(ctx context.Context, period time.Time) {
	report, err := p.monthlyCapacityCalculator.Calculate(ctx, period, nil)
	if err != nil {
		log.Printf("monthly capacity: period %s: %v", period.Format("2006-01"), err)
		return
	}
	log.Printf("monthly capacity: period %s: %d vehicle(s) found, %d measured, %d thin",
		period.Format("2006-01"), report.VehiclesFound, report.Measured, report.Thin)
}
```

**What is and is not covered, stated plainly so the reviewer does not read this as a gap:**

| Function | Pure? | Unit-tested? | Why |
|---|---|---|---|
| `monthlyCapacityPeriod` | yes | **yes** — Group A below | No I/O; this is where the actual RD6/RD7 logic lives |
| `callMonthlyCapacityCalculator` | no (calls the port) | **yes** — Group B below | No clock; a fake port and a fixed `period` make it deterministic |
| `runMonthlyCapacityStep` | no (reads `clock.Now()`) | **no** — accepted gap | Three-line wiring: read the clock, call the pure gate, call the tested function. Testing it would require faking `clock.Now()`, which this codebase does not do for `Processor` (Context fact 11 — the same gap already accepted for `recalculateAnalytics`'s date line) |

The important behavior — "is today the 1st, and if so what period do we compute" and "does the
port get called correctly, and are its outcomes logged, and are its errors swallowed, not
propagated" — is fully covered. What is left untested is three lines of composition that read the
real clock, exactly the same category of gap this module already accepts elsewhere.

| Alternative | Why rejected |
|---|---|
| Add a clock seam to `Processor` (a `now func() time.Time` field, like `Scheduler`'s `s.now`) | Rejected: `Processor` deliberately has no clock seam today (`RM36-app-record-poll-run` design D2/D3: "`ProcessVehicleData` reads `clock.Now()` for its `start`/`finish` measurement points" directly, no injection). Adding one now, for one step, would be a new precedent this change has no mandate to set — and `Scheduler`'s own `cfg telemetry.Config.Clock` seam exists for a documented, narrow reason (`internal/app/AGENTS.md`: "kept rather than narrowed... do not narrow it opportunistically") that does not extend to `Processor`. |
| One function, no split | Rejected: would make the whole thing untestable, including the RD6/RD7 gate logic itself — the one piece of new logic in this tier that most needs a deterministic test (dispatch's own "highest-risk part of the tier"). |

### D3 — The new `NewProcessor` parameter's position

**Not a roadmap decision — a direct application of Context fact 4's existing grouping rule.**

```go
func NewProcessor(
	collector telemetry.Collector,
	superchargerHistoryReader telemetry.SuperchargerHistoryReader,
	runWriter telemetry.RunWriter,
	sessionWriter charging.SessionWriter,
	mirrorWatermarks charging.MirrorWatermarkStore,
	monthlyCapacityCalculator charging.MonthlyCapacityCalculator, // NEW — third charging port
	acct account.Service,
	recalculator analytics.Recalculator,
	analyticsReader analytics.Reader,
	gapWriter analytics.GapWriter,
	loc *time.Location,
) Processor
```

Placed immediately after `mirrorWatermarks`, keeping all three `charging` ports contiguous — the
same grouping-by-owning-module the existing nine parameters already follow. Every call site
(`cmd/poller/main.go`, `newTestProcessor` in `processor_test.go`) updates in the same change.

| Alternative | Why rejected |
|---|---|
| Append at the end, before `loc` | Rejected: breaks the existing grouping-by-module convention for no benefit — a reader scanning the parameter list for "what does `charging` need" would have to check two places instead of one. |

### D4 — No spec capability split; this is a delta to the existing `process-vehicle-data` capability

**Not a roadmap decision — an OpenSpec authoring choice.** `RM29-app-add-process-vehicle-data` and
`RM36-app-record-poll-run` both wrote to the same capability, `process-vehicle-data`
(`openspec/specs/process-vehicle-data/spec.md`). This tier adds a fourth step to the same
operation those requirements already describe, so it is a delta to the same capability, not a new
one — MODIFYING the "three steps" requirement to four, MODIFYING the short-circuit requirement to
cover the new step, and ADDING one requirement for the RD6/RD7 gate itself. See `specs/process-
vehicle-data/spec.md` in this change.

---

## Date/clock design — summary (dispatch's own flagged highest-risk item)

| Question | Answer | Where |
|---|---|---|
| Which zone decides "first day of the month"? | The platform default, `clock.Zone()` (`America/Bogota`) — **not** `p.loc` | D1 |
| Which moment is read? | `clock.Now()` — never a raw `time.Now()` | D1, D2 |
| How is "first day" decided? | `clock.CalendarDay(now, loc).Day() == 1` | D2, `monthlyCapacityPeriod` |
| How is "the previous month" computed? | `today.AddDate(0, -1, 0)`, safe because `today.Day()` is always `1` at that point | D2 |
| What does tier 1's `Calculate` receive as `period`? | The previous month's first instant, UTC-midnight-stamped (the same representation `CalendarDay` always returns, matching `pgtype.Date`'s storage encoding) | D2 |
| Is the gate itself unit-tested? | Yes, fully — `monthlyCapacityPeriod` is pure (Group A) | D2 |
| Is the wiring around it unit-tested? | No — accepted gap, same category as `recalculateAnalytics`'s own clock read (Context fact 11) | D2 |

---

## Test Contract

Expected values authored **before** implementation, per `ai/go-conventions.md` §Testing ("author
their expected values up front, in the change's `design.md`"). **Tests written later must assert
THIS contract**, not whatever the implementation happens to produce.

**Conventions**, following `internal/app`'s existing ones (`processor_test.go`): fresh `uuid.New()`
where an id is needed; fakes satisfy the exact port interface (`var _ charging.
MonthlyCapacityCalculator = (*fakeMonthlyCapacityCalculator)(nil)`); table-style cases where the
input/output shape repeats.

### Group A — `monthlyCapacityPeriod` (pure, no fakes, no DB)

New file `internal/app/monthly_capacity_step_test.go`.

| ID | `now` | `loc` | Expected `(period, run)` | What it proves |
|---|---|---|---|---|
| **A1** | `2026-09-01T00:30:00Z`, already expressed in `America/Bogota` (i.e. `time.Date(2026,9,1,0,30,0,0,bogota)`) | `America/Bogota` | `(2026-08-01 00:00:00 UTC, true)` | The plain case: today is the 1st, previous month is August (RD6/RD7). |
| **A2** | `time.Date(2026,9,30,23,59,0,0,bogota)` | `America/Bogota` | `(zero time.Time, false)` | The last day of the month does **not** trigger — only the 1st does. |
| **A3** | `time.Date(2027,1,1,10,0,0,0,bogota)` | `America/Bogota` | `(2026-12-01 00:00:00 UTC, true)` | **Year boundary**: previous month of January is December of the prior year, not month `0`. |
| **A4** | `time.Date(2026,9,1,3,0,0,0,time.UTC)` — this UTC instant is `2026-08-31 22:00` in Bogota (UTC-5) | `America/Bogota` | `(zero time.Time, false)` | **Zone-aware boundary, the load-bearing case.** The UTC calendar day is the 1st, but the Bogota calendar day is still the 31st — a naive UTC-day check would wrongly trigger here. This is the direct analogue of `clock.CalendarDay`'s own `TestCalendarDay_BogotaCrossDay`. |
| **A5** | `time.Date(2026,9,1,6,0,0,0,time.UTC)` — this UTC instant is `2026-09-01 01:00` in Bogota | `America/Bogota` | `(2026-08-01 00:00:00 UTC, true)` | The zone-aware counterpart to A4: a UTC instant whose UTC day is also the 1st, correctly triggers here too, for the right reason (it IS the 1st in Bogota) — not by coincidence. |
| **A6** | `time.Date(2026,3,1,12,0,0,0,time.UTC)` | `time.UTC` | `(2026-02-01 00:00:00 UTC, true)` | `loc` is a real parameter, not hardcoded to Bogota inside the function — passing `time.UTC` still works correctly. `clock.Zone()` is what supplies Bogota at the one real call site (D1), not this function. |

### Group B — `callMonthlyCapacityCalculator` (fake port, no clock)

Same file.

| ID | Fake `Calculate` returns | Expected | What it proves |
|---|---|---|---|
| **B1** | `(charging.MonthlyCapacityReport{VehiclesFound: 4, Measured: 3, Thin: 1}, nil)` for `period = 2026-08-01` | The fake recorded exactly one call, with `period == 2026-08-01` and `teslaID == nil`; no error returned to the caller (nothing propagates — this function has no return value) | The happy path: the port is called with the right arguments, `teslaID` is always `nil` from this caller (Context fact 8). |
| **B2** | `(charging.MonthlyCapacityReport{}, errors.New("db down"))` | The fake still recorded exactly one call with the same `period`; the function returns nothing (its signature has no error) — the error is logged and swallowed, never propagated | Errors from the monthly job never become a `ProcessVehicleData` error — same isolation `processChargingData`/`recalculateAnalytics` already guarantee for their own steps. |

### Group C — `ProcessVehicleData`'s short-circuit (through the fake roster)

`internal/app/processor_test.go`, extending the existing Fixtures P3–P5.

| ID | Setup | Expected | What it proves |
|---|---|---|---|
| **C1** | `fakeCollector` returns an error (Fixture P4's existing setup, step-1 whole-cycle failure) | `fakeMonthlyCapacityCalculator.calls == 0` | **The fourth step is skipped by the same short-circuit as steps 2 and 3** (RD6) — this is deterministic regardless of the real calendar date, because on the `err != nil` branch `runMonthlyCapacityStep` (and therefore `clock.Now()`) is never reached at all. |
| **C2** | `fakeCollector` succeeds (Fixture P3's existing setup) | Test asserts nothing about `fakeMonthlyCapacityCalculator.calls` | **Deliberately no assertion here.** Whether the real `clock.Now()` falls on the 1st of the month on the day this test happens to run is not something a deterministic suite may depend on (D2's accepted gap) — asserting either `== 0` or `== 1` here would make the suite flaky on one day out of every month. |

C1 is the one assertion this tier's `ProcessVehicleData`-level test contract makes about the new
step, and it is the one that matters most: it proves wiring the new step into the same
`if err == nil` block did not accidentally move it outside the short-circuit.

---

## Roadmap-decision mapping

| design.md | roadmap | Subject |
|---|---|---|
| D1 | **RD7** | reads `clock.Zone()`, not `p.loc` |
| D2 | **RD6, RD7** | the gate's pure/impure split and its testing treatment |
| D3 | — | `NewProcessor`'s new parameter position (module-grouping convention) |
| D4 | — | delta targets the existing `process-vehicle-data` capability |
| Context fact 8 | **RD1, RD5, RD8** | this tier always passes `teslaID = nil`; scoping is tier 3's job |

RD1–RD5, RD9–RD14 are tier 1's (done) or tier 3's and are not re-implemented here.

---

## Risks

1. **`POLLER_TIMEZONE` diverging from `America/Bogota` in some future deployment** would make step
   4's "first day of the month" (via `clock.Zone()`) disagree with step 3's "yesterday" (via
   `p.loc`) about what "today" means. This is D1's own point: RD7 names the fixed platform zone on
   purpose, so this is accepted, not a defect — if it ever matters, RD7 itself would need to change
   first, not this tier's implementation of it.
2. **A missed month (the poller down on the 1st, or `Calculate` erroring) is not retried
   automatically.** This is RD9, by design: no automatic backfill; tier 3's `cmd/monthly-capacity`
   covers a manual re-run.
