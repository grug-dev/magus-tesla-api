## Context

`internal/analytics` is the fourth tier of `RM35-timezone-centralization` and the second
**domain module** to adopt `internal/clock` (tier 3, `telemetry`, is archived). The roadmap's
finding section names `analytics` as owning one of the three copy-pasted UTC-midnight
calendar-day truncators:

- `internal/analytics/consumed.go:79-82` (roadmap line numbers; verified by reading the source
  directly at the current lines) — `calendarDay(t time.Time) time.Time`, body
  `t = t.UTC(); return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)`. UTC-only,
  no zone parameter — unlike `telemetry`'s now-deleted `dateOnly`, this function was never
  zone-aware; it only ever bucketed at UTC midnight. Tier 1's `clock.CalendarDay(t, loc)` was
  built (design D7 of `RM35-clock-add-bogota-time-package`) to reproduce this exact call site's
  output for every input when called with `loc = time.UTC`.

`analytics` additionally owns one independent, un-named-by-the-roadmap bare `time.Now()` call
inside `Reconcile`'s "don't recompute a day that might still be in progress" clamp
(`recalculate.go:268`, now `269` after the doc-comment growth this tier added — see "Verified
by reading the source" below). The roadmap's per-tier cell for `analytics` names both:
"`consumed.go:80-82` `calendarDay` → `clock.CalendarDay`; `recalculate.go:268` `time.Now()` →
`clock.Now()`. Module still owns no `*time.Location` (D-B12 holds)."

**Verified by reading the source directly** (not assumed from the roadmap's line numbers, which
had already drifted slightly by the time this tier started): `calendarDay`'s two call sites are
`effectiveDay` (`consumed.go`, then line 78, now deleted along with the function) and
`sumManualPctBetween` (`consumed.go`, then line 143). `Reconcile`'s bare `time.Now()` sits at
what was `recalculate.go:268`, immediately above the `widen` closure and the three
`widen(calendarDay(...))` calls that consume its result.

Performance profile: **read-heavy** (`ai/architecture.md` §7) — not relevant to this tier's own
work. Every touched call site (`effectiveDay`, `sumManualPctBetween`, `Reconcile`'s `yesterday`
clamp and its three `widen(...)` inputs) sits exclusively on the **write** path
(`Recalculate`/`Reconcile`, the `Recalculator` port — `internal/app`'s nightly orchestration is
the only production caller, per `AGENTS.md`). No `Reader` method (`RecentEfficiency`,
`ConsumedByDay`, `OdometerDeltaByDay` — the dashboard read path) is touched by this tier.
`clock.Now()`/`clock.CalendarDay` are pure, in-memory, no-I/O functions (tier 1 design), so this
tier introduces no new query, no new allocation pattern worth optimizing, and no change to any
read-path latency.

## Goals / Non-Goals

**Goals:**
- Delete `analytics`'s own `calendarDay` in favor of `clock.CalendarDay(t, time.UTC)`, with the
  write-path result (every `vehicleMetricRow.MetricDate`, and therefore the stored
  `vehicle_metrics.metric_date` column, plus `Reconcile`'s day-range clamp) byte-identical for
  every input the existing test suite already exercises.
- Route `Reconcile`'s bare `time.Now()` through `clock.Now()`, per roadmap D4, while keeping the
  bucketing zone at `time.UTC` — see "The central decision" below for why this is not a
  mechanical swap the way the other three call sites are.
- Repair every existing test broken by deleting `calendarDay` (roadmap D6), without weakening
  any assertion or changing any expected value the deleted function itself would have produced.
- Correct every doc comment that names `calendarDay` by name (`CLAUDE.md`'s "docs track
  structural change" / stale-comment rule).

**Non-Goals (explicitly out of scope for this tier):**
- `internal/app`'s `scheduler.go`/`processor.go` nil-`loc` fallback — `RM35-app-adopt-clock`
  (tier 5)'s job, not this one's.
- `internal/gateway`'s `startOfDay` — tier 6.
- `make tz-guard` — tier 7, shipped last (roadmap D5).
- Any migration, backfill, or database object — this tier owns no new table, column, index, or
  constraint, and changes no schema. See "No database change" below.
- Touching `cmd/` — out of scope (RM35 D4's composition-root exemption); this tier has no
  `cmd/` caller of its own (`internal/app`'s `ProcessVehicleData` is the only production caller
  of `Reconcile`, per `AGENTS.md`).
- Giving `internal/analytics` a `*time.Location` of its own, or a `Config.Location`-shaped seam
  like `telemetry`'s. This module is documented (`AGENTS.md` "Public interface", this module's
  own `battery-add-efficiency-metric` design D-B12) as deliberately holding no time-zone
  configuration — every date it reasons about arrives already bucketed by an upstream module or
  is a caller-supplied bare UTC-midnight date. This tier does not change that invariant; it is
  the central subject of the decision below.

## No database change

This tier touches no schema, migration, table, column, index, constraint, or view. It changes
which function computes a value that was already being computed, stored, or compared exactly as
before — `vehicle_metrics.metric_date`'s value, and the day-range `Reconcile` clamps its
`Recalculate` call to, are unaffected in every case the current test suite exercises (see "This
tier is fully behavior-preserving" below). No index plan, no migration plan, and no rollback
plan beyond a plain `git revert` of the touched Go/doc files are needed.

## Decisions

Roadmap decisions restated as they apply to this tier, followed by this tier's own findings.

### D1 — Scope: defaults only, zero migrations (restated, binding)

This tier changes no fallback field and owns no configuration. `internal/analytics` has no
`Config`-shaped seam at all (unlike `telemetry`'s `Config.Location`/`Config.Clock`) — every
touched call site is an unconditional function call, not a nil-check fallback. No migration; no
storage encoding is touched.

### D2 — `internal/clock` is the sole owner of the default zone and calendar-day computation (restated, binding)

`analytics` obtains the current instant via `clock.Now()` and the calendar-day computation via
`clock.CalendarDay(t, loc)` — never re-implementing either. `internal/clock` imports stdlib
`time` only, and `analytics` already sits below `app`/`gateway` in the dependency direction
(`ai/architecture.md` §2 — `clock` is LAYER 0, no internal dependencies at all), so this import
creates no cycle.

### D4 — This tier's job within the sweep (restated, binding)

Per the roadmap's tier table, verbatim: swap `calendarDay` for `clock.CalendarDay` at its two
call sites in `consumed.go`, swap `Reconcile`'s bare `time.Now()` for `clock.Now()` in
`recalculate.go`. "Module still owns no `*time.Location` (D-B12 holds)" is the roadmap's own
constraint on how the second swap may be done — it forecloses simply also switching the
bucketing `loc` to `clock.Zone()`, which is exactly the temptation the central decision below
addresses and rejects.

### D6 — Unit tests: repair only, no new coverage beyond what deleting `calendarDay` forces (restated, binding)

This tier adds no new `_test.go` file and no new test function beyond what repairing broken
tests requires. Every test touched below already existed; none gains a new assertion this tier
did not already need to make the suite compile.

### D-ana-1 — The central decision: `recalculate.go`'s `yesterday` clamp keeps `time.UTC`, never `clock.Zone()`

This is the one call site in this tier that needed a genuine judgment call rather than a
mechanical swap, and it is why this tier is not simply "s/calendarDay/clock.CalendarDay/ four
times."

**The instant-source swap is a provable no-op, independent of the zone decision.**
`clock.Now()` is defined (tier 1, `clock.go`) as `time.Now().In(clock.Zone())` — it returns the
identical instant `time.Now()` does, merely re-expressed in a different `*time.Location`.
`clock.CalendarDay(t, loc)`'s `loc` parameter re-converts explicitly via `t.In(loc)` regardless
of the input's own `Location` field, so **the choice of `loc` is the only thing that can change
this line's output** — the instant source itself cannot, no matter which of `time.Now()` or
`clock.Now()` supplies it.

**Decision: `yesterday := clock.CalendarDay(clock.Now(), time.UTC).AddDate(0, 0, -1)` — `loc`
stays `time.UTC`, exactly as today, rather than becoming `clock.Zone()` (`America/Bogota`).**

Reasoning:

1. **Every value `yesterday` is compared against is itself UTC-bucketed, and mixing zones would
   desynchronize the clamp from the range it clamps.** `Reconcile` builds `minDay`/`maxDay` via
   three `widen(clock.CalendarDay(..., time.UTC))` calls (over `s.EffectiveDate`,
   `s.ChargeStopDateTime`, `e.ChargedOn`) immediately above the `yesterday` clamp, in the same
   function. `end := maxDay.AddDate(0, 0, 1); if end.After(yesterday) { end = yesterday }` only
   produces a coherent clamp when `yesterday` is bucketed in the same zone as `maxDay` — if
   `yesterday` were computed at `clock.Zone()` (UTC-5, no DST) while `maxDay` stays UTC-bucketed,
   the comparison would silently compare two "days" that do not start or end at the same instant,
   producing an off-by-up-to-one-day clamp that depends on which side of the UTC/Bogota boundary
   the actual data happens to fall. That is a genuine, undocumented behavior change this
   roadmap's own D4 calls "behavior-preserving except the documented default," which this call
   site is not: nothing here currently falls back to an *unset* zone; `time.UTC` is hard-coded on
   purpose, and there is no fallback branch to redirect.
2. **Switching to `clock.Zone()` would shift the reconciliation cutoff by five hours (Bogota is
   UTC-5, no DST) once per day** — a real, observable change to which days `Reconcile` is
   willing to touch, entirely unrequested by this tier's scope and not one of the two changes the
   roadmap's tier table names for this module.
3. **It would contradict this module's own documented invariant (D-B12 of
   `battery-add-efficiency-metric` design.md, restated in `AGENTS.md` "Public interface") that
   `internal/analytics` holds no `*time.Location` of its own.** Introducing a Bogota-zone
   assumption into `analytics` here — even only for this one call site — would make that
   statement false. Every other date this module reasons about arrives already bucketed by an
   upstream module (`telemetry`'s `Config.Location`, applied on `telemetry`'s own write path) or
   is a caller-supplied bare UTC-midnight date; introducing a second, locally-decided zone for
   exactly one clamp would be inconsistent with that design, not an extension of it.

**Rejected alternative — `clock.Zone()` for consistency with the roadmap's stated intent** ("no
module hand-rolls a 'now' or a 'start of day' again"): rejected because the roadmap's intent is
about the **default zone a fallback resolves to**, not about which zone every date computation
in the codebase must share. `analytics`'s bucketing zone was never a fallback — it is, and
remains, a deliberate `time.UTC` chosen because the module has no zone-decision seam of its own
and because every UTC-bucketed value it compares against (`widen(...)`'s inputs) is itself
UTC-bucketed by construction. Applying `clock.Zone()` here would be "improving" a call site the
roadmap's own tier cell did not ask to change, and `consumed.go`'s `effectiveDay`/
`sumManualPctBetween` doc comments (both corrected by this tier) state the identical reasoning
for the other two UTC-only call sites this module owns — this decision keeps all three call
sites internally consistent with each other and with D-B12.

### D-ana-2 — `calendarDay` is deleted, not kept as a private wrapper (this tier's decision)

**Decision:** delete `calendarDay` entirely; `effectiveDay` and `sumManualPctBetween` call
`clock.CalendarDay(t, time.UTC)` directly.

**Why not keep `calendarDay` as a one-line wrapper around `clock.CalendarDay`** (rejected): a
wrapper that does nothing but forward its arguments to another package's function is exactly the
indirection `CLAUDE.md`'s AI-efficiency "do not over-abstract" rule warns against — it costs a
reader one extra hop to learn that `calendarDay` does nothing `clock.CalendarDay` doesn't already
do, for zero behavioral or testing benefit. Mirrors `RM35-telemetry-adopt-clock`'s identical
decision (design D-tel-1) for `dateOnly`. The three direct `calendarDay(...)` fixture call sites
(all in `db_integration_test.go`, computing "N days before today" anchors) are repaired to call
`clock.CalendarDay(t, time.UTC)` instead — the exact same arguments and expected values, just
naming the function that now actually performs the computation.

## This tier is fully behavior-preserving

Unlike tier 3 (`telemetry`), which changed one real fallback (`location()`'s nil-`Config.Location`
branch, `time.Local` → `clock.Zone()`), **this tier changes no fallback at all** — `analytics` has
no `Config`-shaped seam to fall back through. Every call-site swap in this tier is either:

- **Algebraically identical** — `consumed.go`'s two `calendarDay` → `clock.CalendarDay(t,
  time.UTC)` sites, and three of `recalculate.go`'s four sites (the `widen(...)` calls). Tier 1's
  `CalendarDay` was built (design D7) specifically to reproduce `calendarDay`'s output for every
  input when `loc = time.UTC`: `calendarDay`'s body was `t = t.UTC(); return time.Date(t.Year(),
  t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)` — `t.UTC()` and `t.In(time.UTC)` are the same
  conversion, so the two functions are the same formula under different names.
- **A provable no-op for the reasons D-ana-1 gives** — `recalculate.go`'s `yesterday` clamp: the
  instant-source swap (`time.Now()` → `clock.Now()`) cannot change the line's output because
  `CalendarDay`'s `loc` parameter re-converts unconditionally, and the deliberate choice to keep
  `loc = time.UTC` unchanged means the output is identical to before for every input.

**Conclusion: no stored or externally observable value changes for any input the existing test
suite already exercises.** A tier that changes nothing observable is a good outcome here, not a
weak one — it is what "collapse three copy-pasted truncators into one, plus route one bare
`time.Now()` through the platform's clock" should produce when the module being touched has no
zone-fallback branch of its own to begin with. The value this tier delivers is entirely about
`ai/go-conventions.md`'s new non-negotiable ("obtain now/a calendar day only through
`internal/clock`, never hand-roll a UTC-midnight truncation") and about `make tz-guard`'s (tier
7) ability to grep-enforce that rule repo-wide once every module has adopted it — not about any
change in what `analytics` computes.

`internal/analytics` is on the dashboard read path (`Reader.RecentEfficiency`,
`Reader.ConsumedByDay`, `Reader.OdometerDeltaByDay`) — none of those methods, nor anything they
call, is touched by this tier; the only touched call sites are inside `Recalculate`/`Reconcile`,
the write side (`ai/architecture.md` §7's Reader/Collector split). No dashboard read is affected.

**Breaking?** No, for the same reasons.

## Test Contract

Per `ai/go-conventions.md` §Testing, expected values are authored here before the implementation
change (though in this repair-only tier every expected value below already existed — repair only
*relocates* the assertion to a new call target, it changes no value).

**`clock.CalendarDay(t, time.UTC)` replacing `calendarDay(t)` at its two `consumed.go` call
sites** (`effectiveDay`, `sumManualPctBetween`) — no test calls either function directly; both
are exercised only through `deriveVehicleMetrics`'s existing table-driven fixtures in
`consumed_test.go`. Every existing fixture's expected `MetricDate`/day-range value is unchanged,
because `clock.CalendarDay(t, time.UTC)` and the deleted `calendarDay(t)` compute the identical
formula (`t.UTC()` ≡ `t.In(time.UTC)`).

**Every direct `calendarDay(t)` fixture call site** (three, all in `db_integration_test.go`,
computing a fixture's "N days before today" anchor) becomes `clock.CalendarDay(t, time.UTC)` with
**identical arguments and identical expected values**:
1. `clock.CalendarDay(time.Now(), time.UTC).AddDate(0, 0, -20)` — `day0` in
   `TestReconcile_FirstRun_BackfillsFullHistory`-style fixtures.
2. `clock.CalendarDay(time.Now(), time.UTC).AddDate(0, 0, -22)` — a second "days before today"
   anchor in the same file.
3. `clock.CalendarDay(refNow, time.UTC).AddDate(0, 0, -25)` — an explicit `refNow`-anchored case
   ("three weeks ago and then some — safely before yesterday"), which exercises `Reconcile`'s
   `yesterday` clamp end-to-end and is the closest thing this suite has to a direct test of
   D-ana-1's `yesterday` line. Its expected outcome (the fixture day is included, being safely
   before `yesterday`) is unchanged, because D-ana-1 established the clamp's output for any given
   instant is unchanged by this tier.

**`recalculate_test.go`'s `wantChargeStart` fixture** (`day(2026, 7, 31)`, documented inline as
`effectiveDay(2026-08-01) = clock.CalendarDay(2026-08-01, time.UTC) - 1`): unchanged expected
value — this is a doc-comment-only repair (naming `clock.CalendarDay` instead of `calendarDay`),
not a call-site change, since `recalculate_test.go` never called `calendarDay` directly.

No test asserts on `Reconcile`'s `yesterday` clamp's absolute wall-clock value directly (doing so
would be inherently flaky against real `time.Now()`) — every existing assertion anchors fixture
dates safely clear of the "yesterday" boundary (mirrors `AGENTS.md`'s existing guidance: "it has
no injectable clock and clamps in UTC — a deliberate call (RM29 D13), so do not widen the port to
make a test deterministic; anchor fixture dates clear of the boundary instead"). This tier
preserves that testing strategy unchanged; D-ana-1 keeping `loc = time.UTC` is what keeps those
existing anchors valid without repair.

## Risks / Trade-offs

- **[Non-risk, verified]** `consumed.go`'s two `calendarDay` → `clock.CalendarDay(t, time.UTC)`
  swaps are algebraically identical (tier-1 design D7's explicit reproduction guarantee) — zero
  behavior change for any caller.
- **[Non-risk, verified]** `recalculate.go`'s `yesterday` clamp's instant-source swap changes no
  stored or observable value (D-ana-1) — not a risk, a proven no-op given the deliberate choice
  to keep `loc = time.UTC`.
- **[Non-risk, by design]** Choosing NOT to switch `yesterday`'s bucketing zone to `clock.Zone()`
  is not a missed opportunity — D-ana-1 explains why doing so would be a real, unrequested
  behavior change that desynchronizes the clamp from the UTC-bucketed range it clamps.
- **[Trade-off]** `internal/analytics` gains a new inter-package dependency, `internal/clock` —
  accepted: `clock` imports nothing project-local (D2), so this cannot create a cycle, and it is
  the dependency direction the whole roadmap requires. (Root `README.md`'s dependency graph
  already reflects this edge as of this tier — a leader-owned edit per the proposal, applied
  together with tiers 5/6.)

## Migration Plan

None — this tier owns no database object, adds no table, column, index, or constraint, and runs
no migration. Rollback is a plain revert of the touched Go/doc files; nothing downstream depends
on this tier's specific implementation (tiers 5/6 are independent modules; tier 7's `tz-guard`
depends on this tier having landed, not on any specific internal shape).

## Open Questions

None. The one question this design had to resolve — whether `Reconcile`'s `yesterday` clamp
should also switch its bucketing zone to `clock.Zone()` once its instant source becomes
`clock.Now()` — is answered by D-ana-1: no, keep `time.UTC`, because every value the clamp is
compared against is itself UTC-bucketed and this module owns no `*time.Location` of its own
(D-B12).
