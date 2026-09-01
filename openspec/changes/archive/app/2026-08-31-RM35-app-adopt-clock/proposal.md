Source: MAG-20 — https://linear.app/magus-monitor/issue/MAG-20/datedate-time-time-zone
Roadmap: openspec/roadmaps/RM35-timezone-centralization.md
Tier: 5 of 7 (`app`; depends on tier 1, `RM35-clock-add-bogota-time-package`, already
archived and on disk)

## Why

Tiers 1–4 built `internal/clock` (the platform's single owner of the default zone,
`America/Bogota`) and adopted it in `internal/config`, `internal/telemetry`, and
`internal/analytics`. This tier — `RM35-app-adopt-clock` — is the fourth domain-module
adopter: `internal/app` has its own zone fallback (`Scheduler`'s nil-`loc` default) and its
own hand-rolled UTC-midnight calendar-day truncation (`recalculateAnalytics`'s "yesterday"
math). This tier routes both through `internal/clock`, preserving every existing semantic
the module's own `AGENTS.md` and `RM29-app-add-process-vehicle-data`'s design.md already
established.

## What Changes

- `internal/app/scheduler.go` — `NewScheduler`'s nil-`loc` fallback changes from
  `time.Local` to `clock.Zone()` (roadmap D4). Its doc comment, which stated the
  `time.Local` convention by name, is corrected in the same edit
  (`CLAUDE.md`'s "docs track structural change" / stale-comment rule).
- `internal/app/processor.go` — `recalculateAnalytics`'s hand-rolled day-truncation:
  ```go
  y, m, d := time.Now().In(p.loc).Date()
  end := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
  ```
  becomes
  ```go
  end := clock.CalendarDay(clock.Now(), p.loc).AddDate(0, 0, -1)
  ```
  This is a pure delete-and-delegate, algebraically identical for every input
  (design.md D-app-1) — `clock.CalendarDay`'s formula is `y, m, d := t.In(loc).Date();
  time.Date(y, m, d, 0, 0, 0, 0, time.UTC)`, byte-for-byte the same computation this call
  site already performed. `clock.Now()` replaces the bare `time.Now()` call per
  `ai/go-conventions.md`'s "never call raw `time.Now()` outside `internal/clock`"; the two
  return the same instant, only re-expressed in a different `*time.Location`
  (design.md D-app-2).
  **`p.loc` — the poller's own configured zone — is preserved unchanged.** This is the
  one thing this tier does NOT touch: roadmap D6/D18 and `RM29-app-add-process-vehicle-data`
  design.md D-B12 (via `ai/architecture.md`'s cross-reference in the module's own
  `AGENTS.md`) established that this composition, not `internal/analytics`, owns the
  question "which day am I asking about" — replacing `p.loc` with `clock.Zone()` here
  would silently discard the operator's `POLLER_TIMEZONE` configuration and is out of
  scope for this tier.
- `internal/app/AGENTS.md` — the `Scheduler`/`NewScheduler` port description's "A nil `loc`
  falls back to `time.Local`" line is corrected; `internal/clock` is added to the
  "Allowed / forbidden imports → May import" list; the stale
  `TestScheduler_NilLocationDefaultsToLocal` reference is renamed to match the repaired
  test (below).
- `internal/app/scheduler_test.go` — `TestScheduler_NilLocationDefaultsToLocal` is renamed
  `TestScheduler_NilLocationDefaultsToClockZone` and repaired to assert
  `sched.loc == clock.Zone()` (was `== time.Local`) — the one existing test this tier's
  behavior change breaks (roadmap D6). No other test in the module asserts on the
  `time.Local`/`clock.Zone()` fallback or on `recalculateAnalytics`'s day math (the three
  orchestration steps, including `recalculateAnalytics`, ship with no offline/unit test by
  the module's own recorded design — `RM29-app-add-process-vehicle-data` design.md D12 —
  so there is no test of that call site to repair).

### Cross-module note this tier does NOT resolve

`internal/telemetry/telemetry.go`'s `Config.Location` doc comment (added by tier 3,
`RM35-telemetry-adopt-clock`) states: *"internal/app's Scheduler is a separate,
not-yet-migrated fallback: as of this tier it still falls back to time.Local on a nil loc,
pending RM35-app-adopt-clock (tier 5)."* That comment is now stale — this tier is the
`RM35-app-adopt-clock` it refers to, and the fallback it describes no longer exists. This
tier does **not** edit it: `internal/telemetry` is outside this tier's module sandbox.
Reconciling that comment is the leader's/a `telemetry`-scoped follow-up's job.

### Is the nil-`loc` fallback reachable in production? No — verified, not assumed.

`internal/app.NewScheduler` is called from exactly one production site,
`cmd/poller/main.go:142`:
```go
scheduler := app.NewScheduler(processor, cfg.PollerScheduleHour, cfg.PollerScheduleMinute, loc, tcfg)
```
`loc` here is resolved unconditionally at `cmd/poller/main.go:83-86` via
`time.LoadLocation(cfg.PollerTimezone)`, with a `log.Fatalf` on error — there is no code
path in `cmd/poller` that calls `NewScheduler` with a nil `loc`. A repo-wide search for
`NewScheduler(` finds no other call site outside `internal/app/scheduler_test.go` (three
calls, one of which — `TestScheduler_NilLocationDefaultsToClockZone` — is the only caller
that ever passes `nil`) and `internal/app/scheduler.go` itself (the definition).
**Conclusion: the nil-`loc` fallback is dead code in every current production path**,
exercised only by that one offline unit test and by any future caller that forgets to
supply a zone. The change is made anyway — same rationale tier 3 gave for `telemetry`'s
equivalent (currently-unreachable) fallback: a `time.Local` default left in source is
exactly the drift roadmap D5's `tz-guard` (tier 7) exists to catch, and migrating it now,
while inert, is strictly safer than migrating it later under a real caller's pressure.

**Breaking?** No observable production behavior changes. The nil-`loc` fallback swap is
currently unreachable (above), so no live deployment is affected. The
`recalculateAnalytics` day-math swap is algebraically identical for every input
(design.md D-app-1) and does not change `p.loc`, the zone that actually decides which
calendar day "yesterday" is — so this tier changes no stored or observable value on any
path, reachable or not.

**Read paths affected:** none. `recalculateAnalytics` runs only inside `ProcessVehicleData`
(the nightly `Collector`-adjacent write path, `telemetry.TriggeredByScheduler`), never from
a `Reader`-facing call. `Scheduler.Run` is likewise a write-triggering driving adapter, not
a read path. `ai/architecture.md` §7's Reader/Collector split is unaffected.

**No database object.** No migration, no schema change, no new column, no new index — this
tier owns no table (`internal/app`'s Data Ownership is "none" — see its `AGENTS.md`) and
this change only changes which function computes an already-computed, already-stored value
the same way as before.

## Capabilities

### Modified Capabilities

- `app` — adds one requirement describing the default-timezone behavior of the daily
  scheduler when no explicit collection timezone is configured (previously
  unspecified — the module's spec was silent on the fallback).

## Impact

- `internal/app/scheduler.go` — `NewScheduler`'s nil-`loc` fallback changed; doc comment
  corrected; new `internal/clock` import.
- `internal/app/processor.go` — `recalculateAnalytics`'s day-truncation delegated to
  `clock.CalendarDay(clock.Now(), p.loc)`; doc comment extended; new `internal/clock`
  import.
- `internal/app/AGENTS.md` — `Scheduler` port description and imports list corrected; stale
  test-name reference renamed.
- `internal/app/scheduler_test.go` — one test repaired (renamed + repointed assertion).
- No `cmd/` edit — out of scope (RM35 D4's composition-root exemption); `cmd/poller`
  already passes an explicit `loc` and needs no change for this tier to land.
- No `internal/telemetry` edit — its stale cross-module comment about this tier is flagged
  above for the leader to reconcile; out of this tier's sandbox.
- Root `README.md` — the leader's own edit (not this tier's): `app`'s dependency-graph row
  gains `clock`.
