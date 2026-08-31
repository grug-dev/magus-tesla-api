## Context

`internal/app` is the fifth tier of `RM35-timezone-centralization` and the fourth domain
module to adopt `internal/clock` (tiers 1–4 built `clock` and adopted it in `config`,
`telemetry`, and `analytics`). The roadmap's per-tier cell names two call sites:

- `internal/app/scheduler.go:32` — `NewScheduler`'s nil-`loc` fallback, currently
  `time.Local`.
- `internal/app/processor.go:190` — `recalculateAnalytics`'s hand-rolled
  `time.Now().In(p.loc)` day-truncation, computing the gap-reconciliation window's
  "yesterday".

**Verified by reading the source directly** (not assumed from the roadmap's line
references, which name the same two call sites this design confirms): `scheduler.go:31-33`
today reads
```go
if loc == nil {
    loc = time.Local
}
```
and `processor.go:190-191` reads
```go
y, m, d := time.Now().In(p.loc).Date()
end := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
```

`p.loc` is not this module's own default — it is a required constructor argument
(`NewProcessor(..., loc *time.Location)`), threaded from `cmd/poller`'s
`POLLER_TIMEZONE`-derived zone. `internal/app`'s own `AGENTS.md` and
`RM29-app-add-process-vehicle-data`'s design.md (D-B12, referenced via roadmap D6/D18)
already settled that this composition — not `internal/analytics`, which is deliberately
zone-free — owns "which day am I asking about" for the gap-reconciliation window. This
tier's job is narrower than tier 3's or tier 4's: it does not touch which zone
`recalculateAnalytics` uses, only how the day-truncation arithmetic is expressed.

Performance profile: **read-heavy** (`ai/architecture.md` §7) — not relevant to this
tier's own work. Both touched call sites are exclusively on the **write**-triggering path:
`Scheduler.Run` is the driving adapter that fires nightly `ProcessVehicleData` calls, and
`recalculateAnalytics` is step 3 of that same nightly cycle (`telemetry.TriggeredByScheduler`),
never invoked from a `Reader`-facing request. `clock.Now()`/`clock.Zone()`/
`clock.CalendarDay` are all pure, in-memory, no-I/O functions (tier 1 design), so this tier
introduces no new query, no new allocation pattern, and no change to any read-path latency.

## Goals / Non-Goals

**Goals:**
- Route `NewScheduler`'s nil-`loc` fallback through `clock.Zone()` (roadmap D4).
- Delegate `recalculateAnalytics`'s hand-rolled UTC-midnight truncation to
  `clock.CalendarDay`, with the computed `end` value byte-identical for every input the
  existing production caller (`cmd/poller`) or a hypothetical test would ever produce
  (design D-app-1's reproduction guarantee, mirroring tier-1 design D7's for the three
  original truncators).
- Preserve `p.loc` — the poller's own configured zone — completely unchanged as the zone
  `recalculateAnalytics` uses. This tier is a pure arithmetic delegation, not a zone
  change.
- Repair the one existing test this tier's fallback change breaks (roadmap D6), without
  weakening any assertion or changing any other expected value.
- Correct every doc comment that states the `time.Local` fallback by name, in this same
  change (`CLAUDE.md`'s "docs track structural change" / stale-comment rule).

**Non-Goals (explicitly out of scope for this tier):**
- Changing which zone `recalculateAnalytics` uses. `p.loc` stays the poller's own
  configured zone — replacing it with `clock.Zone()` would silently discard the operator's
  `POLLER_TIMEZONE` setting and contradicts roadmap D6/D18 and
  `RM29-app-add-process-vehicle-data` design.md D-B12. If this were ever the right call it
  would be a distinct, explicitly-scoped decision, not a side effect of adopting `clock`.
- `internal/telemetry`'s `Config.Location` doc comment, which names this tier and is now
  stale now that this tier has landed — outside this tier's module sandbox; flagged in
  `proposal.md` for the leader to reconcile.
- `internal/gateway` — tier 6.
- `make tz-guard` — tier 7, shipped last (roadmap D5).
- Any migration, backfill, or database object — this tier owns no table and adds none
  (`internal/app`'s Data Ownership is "none" per its own `AGENTS.md`).
- Touching `cmd/poller` — out of scope (RM35 D4's composition-root exemption). It already
  passes an explicit `loc` and requires no change for this tier.
- Giving `ProcessVehicleData`'s three orchestration steps (including
  `recalculateAnalytics`) offline/unit coverage. That gap predates this tier and this
  roadmap (`RM29-app-add-process-vehicle-data` design.md D12) and roadmap D6 does not ask
  a tier to invent coverage for code that was never covered — see Test Contract below.

## Decisions

Roadmap decisions restated as they apply to this tier, followed by this tier's own
findings/decisions (D-app-1 onward).

### D1 — Scope: defaults only, zero migrations (restated, binding)

This tier's only fallback-affecting field is `NewScheduler`'s nil-`loc` parameter. No
migration; `internal/app` owns no table, so D1's "zero migrations, zero backfill" is
trivially satisfied by this tier's shape.

### D2 — `internal/clock` is the sole owner of the default zone and calendar-day computation (restated, binding)

`internal/app` obtains the default zone via `clock.Zone()` and the calendar-day
computation via `clock.CalendarDay(t, loc)` — never re-implementing either. `internal/clock`
imports stdlib `time` only, and `app` sits **above** every domain module in the call graph
(only `cmd/` binaries call it — `internal/app/AGENTS.md` "Allowed / forbidden imports"),
so this import creates no cycle (`ai/architecture.md` §"Dependency direction").

### D4 — This tier's job within the sweep (restated, binding)

Per the roadmap's tier table, verbatim: swap `scheduler.go:32`'s nil-`loc` fallback from
`time.Local` to `clock.Zone()`; swap `processor.go:190`'s hand-rolled day math for
`clock.CalendarDay`/`clock.Now()` equivalents; preserve the D6/D18 "poller's own zone"
semantics. "Behavior-preserving except the documented default change" — this design's
D-app-1/D-app-2 below account for exactly which of the two changes is genuinely
behavior-preserving and which (the nil-`loc` fallback) is a real, currently-unreachable
behavior change.

### D6 — Unit tests: repair only, no new coverage beyond what the fallback change forces (restated, binding)

This tier adds no new `_test.go` file and no new test function. Exactly one existing test —
`TestScheduler_NilLocationDefaultsToLocal` — asserts on the changed fallback, and is
repaired (renamed + repointed). `recalculateAnalytics` has no existing test to repair: its
three orchestration steps ship with no offline/unit test by the module's own recorded
design (`RM29-app-add-process-vehicle-data` design.md D12), and roadmap D6 does not ask a
tier to invent coverage a prior tier explicitly declined to add.

### D-app-1 — `recalculateAnalytics`'s day math is algebraically identical to `clock.CalendarDay(t, p.loc)` (this tier's verification)

**Verified against `internal/clock/calendar.go` directly, not assumed:**

```go
// clock.CalendarDay(t, loc):
func CalendarDay(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
```

The existing `processor.go` code:

```go
y, m, d := time.Now().In(p.loc).Date()
end := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
```

substitutes `t = time.Now()` and `loc = p.loc` into exactly `CalendarDay`'s body, then
calls `.AddDate(0, 0, -1)` on the result. So
`clock.CalendarDay(time.Now(), p.loc).AddDate(0, 0, -1)` produces the identical
`time.Time` value for every `time.Now()`/`p.loc` pair — same year/month/day extraction,
same `time.Date(..., time.UTC)` construction, same trailing `AddDate`. This is a pure
delete-and-delegate, exactly the shape tier 3 used for `telemetry.dateOnly` (tier-1
design D7's reproduction guarantee, generalized to a fourth call site).

**Decision: delegate directly at the call site; do not keep a private wrapper.** Per
`CLAUDE.md`'s AI-efficiency "do not over-abstract" rule (and mirroring tier 3's D-tel-1
for the identical shape of decision): a wrapper around `clock.CalendarDay` that does
nothing but forward its arguments costs a reader one extra hop for zero behavioral or
testing benefit. `processor.go` calls `clock.CalendarDay` inline.

### D-app-2 — `clock.Now()` replaces the bare `time.Now()` call; this is a re-expression, not a behavior change (this tier's finding)

`clock.Now()` is defined (tier 1, `clock.go`) as `time.Now().In(Zone())` — the **same
instant** `time.Now()` would return, re-expressed in `America/Bogota` rather than the
process's own default. Because the very next operation on that value is
`clock.CalendarDay(_, p.loc)`, which immediately calls `.In(p.loc)` on whatever it is
given, the value's *incoming* `Location` field is discarded before it can be observed —
`t.In(loc)` for any `time.Time` `t` representing the same instant returns the same result
regardless of `t`'s own zone. Swapping `time.Now()` → `clock.Now()` here therefore changes
nothing about `end`'s computed value, for any input, ever. It is made anyway — rather than
leaving the bare `time.Now()` in place — because `ai/go-conventions.md` states "never call
raw `time.Now()`… outside `internal/clock`" as a hard convention with `make tz-guard`
(roadmap D5, tier 7) enforcing it repo-wide once every tier has migrated; leaving one
inert `time.Now()` here would force `tz-guard` to special-case it with a `// tz:allow:`
marker it does not deserve (it is not one of the roadmap's documented legitimate escapes),
or leave `make check` red once tier 7 lands. Migrating it now, while its zone is genuinely
unobservable, is strictly safer than leaving it for tier 7 to discover.

**Why `p.loc` is preserved and NOT replaced with `clock.Zone()`.** Unlike `time.Now()`'s
zone (discarded by the immediate `.In(p.loc)` conversion), `p.loc` **is** the zone
`clock.CalendarDay` converts into — it is the one input that actually determines which
calendar day "yesterday" resolves to for an instant near a day boundary. Roadmap D6/D18
and `RM29-app-add-process-vehicle-data` design.md D-B12 already settled that this
composition (`internal/app`, via its `NewProcessor(..., loc *time.Location)` constructor
argument) owns that question, precisely so `internal/analytics` can stay zone-free
(mirrored in `internal/app/AGENTS.md`'s own "Data ownership" framing: this module composes
other modules' ports and takes no positions of its own beyond what its callers configure
it with). Substituting `clock.Zone()` here would silently discard whatever
`POLLER_TIMEZONE` an operator configured and replace it with the platform default — a real,
observable behavior change for every deployment that sets `POLLER_TIMEZONE` to anything
other than `America/Bogota`, and explicitly the kind of change RM35 D1 scopes out
("defaults only... wherever a zone is currently missing or falls back" — `p.loc` is never
missing here; it is a mandatory constructor argument, always supplied by `cmd/poller`).

### D-app-3 — The nil-`loc` fallback change is a real behavior change, but is unreachable in every current production path (this tier's finding, mirroring tier 3's D-tel-3)

Unlike the `time.Now()` swap (D-app-2, provably a no-op), `NewScheduler`'s nil-`loc`
fallback **is** observable by whoever hits the nil branch: it becomes the `Scheduler`'s own
`loc` field, which `nextRun` uses directly to compute the next scheduled fire time, and
which — since `Scheduler` holds no separate zone for anything else — is the only zone this
adapter ever has.

**Reachability, verified by searching every `.go` file in the repository for
`NewScheduler(`** (excluding the definition itself and an unrelated worktree checkout of a
different branch): the only production call site is `cmd/poller/main.go:142`
```go
scheduler := app.NewScheduler(processor, cfg.PollerScheduleHour, cfg.PollerScheduleMinute, loc, tcfg)
```
where `loc` is unconditionally resolved at `cmd/poller/main.go:83-86`:
```go
loc, err := time.LoadLocation(cfg.PollerTimezone)
if err != nil {
    log.Fatalf("invalid POLLER_TIMEZONE %q: %v", cfg.PollerTimezone, err)
}
```
`time.LoadLocation` either returns a valid, non-nil `*time.Location` or the process exits
before `NewScheduler` is ever called — there is no code path in `cmd/poller` that passes a
nil `loc`. Compounding this, tier 2 (`RM35-config-adopt-clock`, archived) already made
`cfg.PollerTimezone` never resolve to an empty string, so `LoadLocation` always receives a
genuine zone name. **The nil-`loc` fallback branch in `NewScheduler` is therefore dead code
in every current production path**, exercised only by
`TestScheduler_NilLocationDefaultsToClockZone` (this tier's repaired test) and by any
future caller that forgets to supply a zone.

**Why change it anyway, given it is unreachable today** (same rationale as tier 3's
D-tel-3): a fallback stated as `time.Local` in source is exactly the drift roadmap D5's
`tz-guard` (tier 7) exists to catch — grep-based enforcement only works once every fallback
in the codebase actually points at `clock`. Migrating it now, while inert, is strictly
safer than migrating it later under the pressure of a real caller depending on whichever
behavior was already there.

## Test Contract

Per `ai/go-conventions.md` §Testing, expected values are authored here before the
implementation change (though in this repair-only tier the expected value already existed
— repair only *relocates* the assertion to a new target, it changes no other value).

**`NewScheduler`'s repaired nil-`loc` fallback test**
(`TestScheduler_NilLocationDefaultsToClockZone`, formerly
`TestScheduler_NilLocationDefaultsToLocal`):
```go
sched := NewScheduler(&stubCollector{}, 3, 30, nil, telemetry.Config{})
// want: sched.loc == clock.Zone() (was: sched.loc == time.Local)
```
No other test in `scheduler_test.go` asserts on the nil-`loc` fallback or on
`recalculateAnalytics`'s day math — `TestNextRun`,
`TestScheduler_ShutsDownWithoutRunningWhenCancelled`, and
`TestScheduler_RunsAndLogsOneCycle` all pass an explicit non-nil `loc` (`time.UTC`) and are
**unchanged** by this tier: their expected `time.Date` literals do not move.

**`recalculateAnalytics`'s day math** — no existing test exercises this call site (the
three orchestration steps ship with no offline/unit test — `RM29-app-add-process-vehicle-
data` design.md D12), so there is no expected value to author here. The verification signal
remains what `internal/app/AGENTS.md`'s Testing notes already document: the owner's own
`go run ./cmd/poller --once`, whose log output (`"gap reconciliation: %s → %s"`, unchanged
by this tier's swap since D-app-1 proves the printed `start`/`end` values are identical)
is the operational check.

## Risks / Trade-offs

- **[Non-risk, verified]** `recalculateAnalytics`'s day-math swap changes no computed or
  observable value (D-app-1) — algebraically identical delegation, not a risk.
- **[Non-risk, verified]** The `time.Now()` → `clock.Now()` swap inside that same call
  changes no computed value either (D-app-2) — the incoming zone is discarded by the
  immediate `.In(p.loc)` conversion regardless of which function produced the instant.
- **[Risk, accepted, currently inert]** `NewScheduler`'s nil-`loc` fallback swap is a real
  behavior change for a future caller that constructs a `Scheduler` without an explicit
  zone — **currently unreachable** in production (D-app-3), so no live deployment is
  affected. If a future caller ever does hit this branch, its schedule will fire at
  `hour:minute` in `America/Bogota` instead of the host's local zone — the same class of
  default change roadmap D1 already authorized platform-wide.
- **[Trade-off]** `internal/app` gains a new inter-package dependency, `internal/clock` —
  accepted: `clock` imports nothing project-local (D2), so this cannot create a cycle, and
  it is the dependency direction the whole roadmap requires. `internal/app`'s own
  `AGENTS.md` already documents `app` as sitting above every domain module in the call
  graph, so this addition is consistent with its existing import shape.
- **[Deliberately unresolved by this tier]** `internal/telemetry`'s `Config.Location` doc
  comment names this tier and is now stale. Left as-is because it is outside this tier's
  module sandbox — flagged in `proposal.md` for the leader.

## Migration Plan

None — this tier owns no database object, adds no table, column, index, or constraint, and
runs no migration. Rollback is a plain revert of the touched Go/doc files; nothing
downstream depends on this tier's specific implementation (tier 6, `gateway`, is an
independent module; tier 7's `tz-guard` depends on this tier having landed, not on any
specific internal shape).

## Open Questions

None. The one question this design had to resolve — whether the nil-`loc` fallback change
is safe to make given it appears to have no live caller — is answered by D-app-3's
reachability analysis: safe, and worth making now precisely because it is currently inert
(mirroring tier 3's identical resolution for `telemetry`'s equivalent fallback).
