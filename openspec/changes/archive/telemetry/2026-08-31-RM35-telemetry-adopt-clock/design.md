## Context

`internal/telemetry` is the third tier of `RM35-timezone-centralization` and the first
**domain module** to adopt `internal/clock` (tiers 1–2 built `clock` and adopted it in the
`cmd/`-level `config` package only). The roadmap's finding section names `telemetry` as owning
one of the three copy-pasted UTC-midnight calendar-day truncators:

- `internal/telemetry/service.go:559` — `dateOnly(t, loc)` — **the odd one out**: unlike
  `analytics.calendarDay(t)` and `gateway.startOfDay(t)` (both UTC-only, no zone parameter),
  `dateOnly` already converts into an explicit `loc` before extracting the date — a genuinely
  zone-aware computation. Tier 1's `clock.CalendarDay(t, loc)` was built by generalizing this
  exact function (design D7 of `RM35-clock-add-bogota-time-package`): `CalendarDay`'s formula
  is `dateOnly`'s formula verbatim, so this tier's swap is a delete-and-delegate, not a
  reimplementation.

`telemetry` additionally owns two independent fallbacks that pre-date the roadmap and are not
one of the three named truncators:

- `(*service).now()` — nil-`Config.Clock` fallback, currently `time.Now()`.
- `(*service).location()` — nil-`Config.Location` fallback, currently `time.Local`.

Both are addressed by this tier per the roadmap's per-tier cell for `telemetry`
("`service.go:107-112` `location()` fallback `time.Local` → `clock.Zone()`; `service.go:102`
`now()` → `clock.Now()`; `service.go:561` `dateOnly` → `clock.CalendarDay`").

**Verified by reading the source directly** (not assumed from the roadmap's line numbers,
which had already drifted by a few lines from `dateOnly`'s doc comment growing since the
roadmap was written): the three call sites are at `service.go:97-102` (`now`),
`service.go:106-112` (`location`), and `service.go:518` (`snapshotFrom`'s call into the
now-deleted `dateOnly`, itself previously at `service.go:556-561`).

Performance profile: **read-heavy** (`ai/architecture.md` §7) — not relevant to this tier's own
work. All three touched call sites (`now`, `location`, `snapshotFrom`) are exclusively on the
**write** path (`CollectAll`, the nightly `Collector` port, per the module's own Reader/
Collector split). No `Reader` method changes. `clock.Now()`/`clock.Zone()`/`clock.CalendarDay`
are all pure, in-memory, no-I/O functions (tier 1 design), so this tier introduces no new query,
no new allocation pattern worth optimizing, and no change to any read-path latency.

## Goals / Non-Goals

**Goals:**
- Delete `telemetry`'s own `dateOnly` in favor of `clock.CalendarDay`, with the write-path
  result (`Snapshot.CapturedDate`, and therefore the stored `captured_date` column) byte-
  identical for every input the existing test suite already exercises (D7's reproduction
  guarantee).
- Route `now()`'s nil-`Clock` fallback and `location()`'s nil-`Location` fallback through
  `clock.Now()` / `clock.Zone()` respectively, per roadmap D4.
- Preserve both test-injection seams (`Config.Clock`, `Config.Location`) exactly as they
  behave today when non-nil — only the nil branch's target changes.
- Repair every existing test broken by deleting `dateOnly` (roadmap D6), without weakening any
  assertion or changing any expected value the deleted function itself would have produced.
- Correct every doc comment that names `dateOnly` or states the `time.Local` fallback by name,
  in this same change (`CLAUDE.md`'s "docs track structural change" / stale-comment rule).

**Non-Goals (explicitly out of scope for this tier):**
- `internal/app`'s `Scheduler` (a separate, relocated component — see "Scope note" below) —
  its own nil-`loc` fallback to `time.Local` is `RM35-app-adopt-clock` (tier 5)'s job, not
  this one's. This tier's `Config.Location` doc comment is corrected to state telemetry's own
  behavior accurately without claiming `app` has changed.
- `internal/analytics`'s `calendarDay` and `internal/gateway`'s `startOfDay` — tiers 4 and 6.
- `make tz-guard` — tier 7, shipped last (roadmap D5).
- Any migration, backfill, or database object — this tier owns no table and adds none; the
  `pgtype.Date` storage encoding (`CapturedDate`/`captured_date`) is unchanged (roadmap D1).
- Touching `cmd/poller` — out of scope (RM35 D4's composition-root exemption). It already
  passes an explicit `Location` (verified below) and requires no change for this tier.

## Decisions

Roadmap decisions restated as they apply to this tier, followed by this tier's own findings.

### D1 — Scope: defaults only, zero migrations (restated, binding)

This tier's only fallback-affecting field is `Config.Location`'s (and, non-observably,
`Config.Clock`'s — see D-tel-2 below). No migration; the `pgtype.Date` UTC-midnight storage
encoding for `CapturedDate`/`captured_date` is untouched — `clock.CalendarDay` was built by
tier 1 (design D7) specifically to preserve it.

### D2 — `internal/clock` is the sole owner of the default zone and calendar-day computation (restated, binding)

`telemetry` obtains the default zone via `clock.Zone()` and the calendar-day computation via
`clock.CalendarDay(t, loc)` — never re-implementing either. `internal/clock` imports stdlib
`time` only, and `telemetry` already sits below `app`/`analytics`/`gateway` in the dependency
direction (`ai/architecture.md` §2's "domain module may depend on a sibling below it" — `clock`
is LAYER 0, no internal dependencies at all), so this import creates no cycle.

### D4 — This tier's job within the sweep (restated, binding)

Per the roadmap's tier table, verbatim: swap `location()`'s fallback, swap `now()`'s fallback,
delete `dateOnly` in favor of `clock.CalendarDay` at its one call site. "Behavior-preserving
except the documented default change" — this design's "Why not..." sections below account for
exactly which of the three changes is genuinely behavior-preserving and which is not.

### D6 — Unit tests: repair only, no new coverage beyond what deleting `dateOnly` forces (restated, binding)

This tier adds no new `_test.go` file and no new test function beyond what repairing broken
tests requires. Every test touched below already existed; none gains a new assertion this
tier did not already need to make the suite compile.

### D-tel-1 — `dateOnly` is deleted, not kept as a private wrapper (this tier's decision)

**Decision:** delete `dateOnly` entirely; `snapshotFrom` calls `clock.CalendarDay(capturedAt,
loc)` directly.

**Why not keep `dateOnly` as a one-line wrapper around `clock.CalendarDay`** (rejected): a
wrapper that does nothing but forward its arguments to another package's function is exactly
the indirection `CLAUDE.md`'s AI-efficiency "do not over-abstract" rule warns against — it
costs a reader (human or agent) one extra hop to learn that `dateOnly` does nothing `clock.
CalendarDay` doesn't already do, for zero behavioral or testing benefit. The three test files
whose fixtures called `dateOnly(t, time.UTC)` directly (`db_integration_test.go` and its four
siblings) are repaired to call `clock.CalendarDay(t, time.UTC)` instead — the exact same
arguments and expected values, just naming the function that now actually performs the
computation. This is what roadmap D6 means by "repair, don't preserve the deleted surface."

### D-tel-2 — `now()`'s fallback change is verified byte-identical in every stored value, not merely "low risk" (this tier's finding)

**Finding, not just a design choice:** `clock.Now()` is defined (tier 1, `clock.go`) as
`time.Now().In(Zone())` — it returns the **same instant** `time.Now()` would, re-expressed in
a different `*time.Location`. Every value `now()` feeds — `Snapshot.CapturedAt` and
`Attempt.AttemptedAt` — is persisted via this module's `timestamptzFrom` helper into a
Postgres `timestamptz` column through `pgtype.Timestamptz`. Postgres `timestamptz` has no zone
of its own: it stores an absolute instant (internally UTC) and any client reading it back
receives that same instant, formatted however the client asks — the Go `time.Time`'s
`Location` field it happened to be constructed with at write time is discarded entirely by the
round-trip through the database. **Consequence:** swapping `now()`'s fallback from
`time.Now()` to `clock.Now()` cannot produce a different stored `captured_at` or
`attempted_at` value for any input, ever — it is not "behavior-preserving because unlikely to
matter," it is behavior-preserving because the changed field (`Location`) is provably
discarded by every consumer this module has (the DB round-trip) before it could be observed.
The one place a `time.Time`'s `Location` *would* be observable — a direct, un-round-tripped
`.String()`/`.Format()` call on the value `now()` returns — does not occur anywhere in this
module; `snapshotFrom` passes `capturedAt` straight to `clock.CalendarDay(capturedAt, loc)`,
which itself calls `.In(loc)` and so is invariant to whatever zone `capturedAt` arrived in.

### D-tel-3 — `location()`'s fallback change IS a real behavior change, but is unreachable in every current production path (this tier's finding)

Unlike `now()`, `location()`'s result is not merely carried through — it is the `loc` argument
`clock.CalendarDay` converts into, so a different zone can produce a different calendar day for
an instant near a day boundary. This tier does not soften that: the swap from `time.Local` to
`clock.Zone()` is a genuine behavior change **for whoever hits the nil branch**.

**Reachability, verified by reading `cmd/poller/main.go` directly** (the only production
caller of `telemetry.NewService`/`telemetry.Config` in the repository — confirmed by
searching every `cmd/` and `internal/` file for `telemetry.Config{`/`telemetry.NewService(`;
the only other match is `internal/app/scheduler_test.go`, a test):

```go
// cmd/poller/main.go:83-91
loc, err := time.LoadLocation(cfg.PollerTimezone)
if err != nil {
    log.Fatalf("invalid POLLER_TIMEZONE %q: %v", cfg.PollerTimezone, err)
}
tcfg := telemetry.Config{WakeTimeout: cfg.PollerWakeTimeout, Location: loc}
```

`Location` is unconditionally set from a `loc` that is either a valid, resolved
`*time.Location` or a fatal process exit — there is no code path in `cmd/poller` that
constructs `telemetry.Config` with a nil `Location`. Compounding this, tier 2
(`RM35-config-adopt-clock`, archived) already changed `cfg.PollerTimezone` so it is never an
empty string — unset now resolves to `"America/Bogota"` before `cmd/poller` ever calls
`time.LoadLocation`. **The nil-`Location` fallback branch in `(*service).location()` is
therefore dead code in every current production path.** It is exercised only by this module's
own offline test (`dedupe_test.go`'s repaired `TestService_Location_FallsBackToClockZone`) and
would be exercised by a future caller that forgot to set `Location`.

**Why change it anyway, given it is unreachable today** (rejected alternative: leave `time.
Local` since nothing currently observes it): a fallback stated as `time.Local` in source is
exactly the kind of latent trap roadmap D5's `tz-guard` (tier 7) exists to catch — grep-based
enforcement only works once every fallback in the codebase actually points at `clock`. Leaving
one un-migrated "because nothing calls it today" would either force `tz-guard` to special-case
it with a `// tz:allow:` marker it does not deserve (it is not one of the roadmap's documented
legitimate escapes — `pgtype.Date` encoding or a `cmd/*` composition root) or leave a stale
default for the day a caller does hit it. Migrating it now, while it is inert, is strictly
safer than migrating it later under the pressure of a real caller depending on whichever
behavior was already there.

## Test Contract

Per `ai/go-conventions.md` §Testing, expected values are authored here before the
implementation change (though in this repair-only tier every expected value below already
existed — repair only *relocates* the assertion to a new call target, it changes no value).

**`clock.CalendarDay` replacing `dateOnly` in `dedupe_test.go`** (renamed
`TestDateOnly_*` → `TestCalendarDay_*`, same three cases, same expected values):
1. `CalendarDay(2026-01-15T20:00:00Z, America/Bogota)` → `2026-01-15T00:00:00Z` (comfortably
   inside the local day).
2. `CalendarDay(2026-01-02T02:00:00Z, America/Bogota)` → `2026-01-01T00:00:00Z` (UTC day ahead
   of local day — proves zone-aware, not UTC-only, bucketing).
3. `CalendarDay(2026-03-10T05:30:00Z, time.UTC)` → `2026-03-10T00:00:00Z` (UTC-as-loc is a
   no-op).

**`(*service).location()`'s repaired fallback test** (`TestService_Location_FallsBackToClockZone`,
formerly `TestService_Location_FallsBackToTimeLocal`):
```go
s := &service{cfg: Config{}}
got := s.location()
// want: got == clock.Zone() (was: got == time.Local)
```
`TestService_Location_UsesConfiguredLocation` (the non-nil-`Location` case) is **unchanged** —
it was never testing the fallback, so nothing about it needed repair.

**Every `dateOnly(t, time.UTC)` fixture call site** across `db_integration_test.go`,
`db_preceding_snapshot_integration_test.go`, `db_read_integration_test.go`,
`db_sourcea_integration_test.go`, `db_tpms_integration_test.go` becomes
`clock.CalendarDay(t, time.UTC)` with **identical arguments and identical expected values** —
these are all fixture setup computing an expected `CapturedDate`/`captured_date` for a row
under test, never themselves the behavior under test. Because `clock.CalendarDay(t, time.UTC)`
is defined as `t.In(time.UTC).Date()` re-expressed at UTC midnight — algebraically identical
to what `dateOnly(t, time.UTC)` computed — no fixture's expected value changes.

## Risks / Trade-offs

- **[Non-risk, verified]** `now()`'s fallback swap changes no stored or observable value
  (D-tel-2) — not a risk, a proven no-op for every current caller.
- **[Risk, accepted, currently inert]** `location()`'s fallback swap is a real behavior change
  for a future caller that constructs `telemetry.Config{}` without `Location` — **currently
  unreachable** in production (D-tel-3), so no live deployment is affected by this tier. If a
  future caller ever does hit this branch, its snapshots will date by `America/Bogota` instead
  of the host's zone — the same class of change roadmap D1 already authorized platform-wide,
  and the one this whole roadmap exists to make the default everywhere.
- **[Non-risk, verified]** `dateOnly` → `clock.CalendarDay` is algebraically identical
  (tier-1 design D7's explicit reproduction guarantee) — zero behavior change for any caller,
  reachable or not.
- **[Trade-off]** `internal/telemetry` gains a new inter-package dependency,
  `internal/clock` — accepted: `clock` imports nothing project-local (D2), so this cannot
  create a cycle, and it is the dependency direction the whole roadmap requires.

## Migration Plan

None — this tier owns no database object, adds no table, column, index, or constraint, and
runs no migration. The `sqlc generate` re-run (comment-only source change in `query.sql`)
touches only generated comment text in `query.sql.go`; no query, parameter, or return type
changes. Rollback is a plain revert of the touched Go/doc files; nothing downstream depends on
this tier's specific implementation (tiers 4–6 are independent modules; tier 7's `tz-guard`
depends on this tier having landed, not on any specific internal shape).

## Open Questions

None. The one question this design had to resolve — whether `location()`'s fallback change is
safe to make given it appears to have no live caller — is answered by D-tel-3's reachability
analysis: safe, and worth making now precisely because it is currently inert (see "Why change
it anyway" above).
