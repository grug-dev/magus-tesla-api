Source: MAG-20 — https://linear.app/magus-monitor/issue/MAG-20/datedate-time-time-zone
Roadmap: openspec/roadmaps/RM35-timezone-centralization.md
Tier: 4 of 7 (`analytics`; depends on tier 1, `RM35-clock-add-bogota-time-package`, already
archived and on disk)

## Why

Tier 1 built `internal/clock` (the platform's single owner of the default zone,
`America/Bogota`, and of calendar-day bucketing via `CalendarDay(t, loc)`). `internal/analytics`
owns one of the three copy-pasted UTC-midnight calendar-day truncators the roadmap's finding
names — `calendarDay(t)` — plus one bare `time.Now()` call inside `Reconcile`'s "don't
recompute a day that might still be in progress" clamp. This tier collapses the truncator into
`clock.CalendarDay` and routes the `time.Now()` call through `clock.Now()`.

## What Changes

- `internal/analytics/consumed.go` — `calendarDay(t time.Time) time.Time` **deleted**. Its two
  call sites (`effectiveDay`, `sumManualPctBetween`) now call `clock.CalendarDay(t, time.UTC)`
  directly. This is a **pure delete-and-delegate, zero behavior change**: `calendarDay`'s body
  was `t = t.UTC(); return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)` —
  algebraically identical to `clock.CalendarDay(t, time.UTC)` (`t.In(time.UTC)` and `t.UTC()`
  are the same conversion; tier-1 design D7 built `CalendarDay` explicitly to reproduce this
  call site's output for every input).
- `internal/analytics/recalculate.go` — four call sites inside `Reconcile`:
  - `yesterday := calendarDay(time.Now()).AddDate(0, 0, -1)` becomes
    `clock.CalendarDay(clock.Now(), time.UTC).AddDate(0, 0, -1)`. See "The `recalculate.go:268`
    decision" below — this is the one call site in this tier that needed a genuine judgment
    call rather than a mechanical swap.
  - The three `widen(calendarDay(...))` calls (over `s.EffectiveDate`, `s.ChargeStopDateTime`,
    `e.ChargedOn`) become `widen(clock.CalendarDay(..., time.UTC))` — same mechanical,
    zero-behavior-change swap as `consumed.go`'s two call sites.
- Stale doc comments correcting `calendarDay` references to `clock.CalendarDay` in
  `consumed.go`, `recalculate.go`, `consumed_test.go`, `recalculate_test.go`, and
  `db_integration_test.go` — none of these were behavior, only prose.
- `internal/analytics/consumed_test.go`, `recalculate_test.go`, `db_integration_test.go` —
  repaired: every direct `calendarDay(...)` call site (three, all in `db_integration_test.go`,
  computing a fixture's "N days before today" anchor) repointed to
  `clock.CalendarDay(t, time.UTC)`, identical arguments and identical expected values.
  `internal/clock` added to the import list of every file that gained a call site.
- `internal/analytics/AGENTS.md` — `internal/clock` added to "Allowed / forbidden imports →
  May import"; the `effectiveDay` port description corrected to name `clock.CalendarDay`
  instead of the deleted `calendarDay`.

### This module gains no new time-zone configuration or default-fallback behavior

Unlike tier 3 (`telemetry`), `internal/analytics` has no `Config.Location`-shaped seam and no
zone-fallback branch of its own — it is documented (`AGENTS.md` "Public interface", `design.md`
D-B12 of `battery-add-efficiency-metric`) as deliberately holding **no `*time.Location` of its
own**: every date it reasons about either arrives already bucketed by an upstream module (the
poller's configured zone, applied in `internal/telemetry`) or is a caller-supplied bare
UTC-midnight date (`ConsumedByDay`'s `start`/`end` parameters). This tier does not change that
invariant — see "The `recalculate.go:268` decision" below.

### The `recalculate.go:268` decision — preserve UTC exactly, do not introduce `clock.Zone()`

`Reconcile`'s `yesterday` clamp caps the incremental-reconciliation window so it never recomputes
a day that might still be in progress. The instant swap (`time.Now()` → `clock.Now()`) is a
provable no-op: `clock.Now()` returns the identical instant `time.Now()` does, merely expressed
in a different `*time.Location`, and `CalendarDay`'s `loc` parameter re-converts explicitly
regardless of the input's own location — so **the choice of `loc` is the only thing that can
change this line's output**, not the instant source.

This tier keeps `loc = time.UTC`, exactly as today, rather than switching to `clock.Zone()`
(`America/Bogota`). Reasoning:

- Every value `yesterday` is compared against (`minDay`/`maxDay`, built by the three
  `widen(clock.CalendarDay(..., time.UTC))` calls immediately above it in the same function) is
  itself UTC-bucketed. Clamping a UTC-bucketed range against a Bogota-bucketed "yesterday" would
  desynchronize the clamp from the range it clamps — a genuine, undocumented behavior change
  this roadmap's own D4 calls "behavior-preserving except the documented default," which this
  call site is not: nothing here currently falls back to an *unset* zone; it hard-codes UTC on
  purpose.
- Switching to `clock.Zone()` would shift the reconciliation cutoff by five hours (Bogota is
  UTC-5, no DST) once per day — a real, observable change to which days `Reconcile` is willing
  to touch, entirely unrequested by this tier's scope.
- It would also contradict this module's own documented invariant (D-B12) that it holds no
  `*time.Location` of its own — introducing a Bogota-zone assumption into `analytics` here would
  make that statement false for exactly this one call site.

**Conclusion: this is a behavior-preserving tier in full**, including `recalculate.go:268`. No
stored or externally observable value changes for any input the existing test suite already
exercises.

**Breaking?** No. Every call-site swap in this tier is either algebraically identical
(`consumed.go`'s two sites, three of `recalculate.go`'s four) or a provable no-op for the
reasons above (`recalculate.go:268`'s instant source) combined with a deliberate choice to keep
the bucketing zone unchanged (`recalculate.go:268`'s `loc` argument). `internal/analytics` is on
the dashboard read path (`Reader.RecentEfficiency`, `Reader.ConsumedByDay`,
`Reader.OdometerDeltaByDay`) — none of those methods, nor anything they call, is touched by
this tier; the only touched call sites are inside `Recalculate`/`Reconcile`, the write side
(`ai/architecture.md` §7's Reader/Collector split — the nightly `Collector` port). No dashboard
read is affected.

**No database object.** No migration, no schema change, no new column, no new index — this
tier only changes which function computes a value already being computed and stored (or
compared) exactly as before.

## Capabilities

### Modified Capabilities

- `analytics` — adds one requirement describing analytics' own reconciliation-cutoff behavior:
  the incremental reconciliation window is bounded by a UTC calendar day, not the platform's
  default time zone, and this module holds no time-zone configuration of its own (previously
  an implicit property of the code, never stated as a capability requirement).

## Impact

- `internal/analytics/consumed.go` — `calendarDay` deleted; two call sites repointed to
  `clock.CalendarDay(t, time.UTC)`; new `internal/clock` import; doc comments corrected.
- `internal/analytics/recalculate.go` — four call sites repointed (one instant-source swap +
  `loc` decision, three mechanical); new `internal/clock` import; doc comments corrected.
- `internal/analytics/consumed_test.go` — one stale comment reference corrected (no call-site
  change — this file never called `calendarDay` directly).
- `internal/analytics/recalculate_test.go` — one stale comment reference corrected.
- `internal/analytics/db_integration_test.go` — three `calendarDay(...)` fixture call sites
  repointed to `clock.CalendarDay(..., time.UTC)`, identical arguments and expected values; one
  stale comment reference corrected; new `internal/clock` import.
- `internal/analytics/AGENTS.md` — `internal/clock` added to "May import"; one stale
  `calendarDay` reference corrected.
- Root `README.md` — dependency graph gains an `analytics → clock` edge (leader-owned edit,
  applied together with tiers 5/6 — not made by this tier's worker).
- No `cmd/` edit — out of scope (RM35 D4's composition-root exemption); this tier has no `cmd/`
  caller of its own.
