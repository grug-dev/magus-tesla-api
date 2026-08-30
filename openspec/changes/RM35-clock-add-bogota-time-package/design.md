## Context

The project has three deliberate, separately-documented time zones today (roadmap "Findings"
section): the poller's `config.PollerTimezone` (default `"Local"`), the per-user `browser_tz`
cookie (MAG-7), and the `pgtype.Date` UTC-midnight storage encoding (design D-B7 of an earlier
change — a representation, not a zone). None of these is wrong, but nothing owns "what zone
applies when nothing else says otherwise," and three separate packages have each hand-rolled a
UTC-midnight calendar-day truncator to cope:

- `internal/analytics/consumed.go:79` — `calendarDay(t time.Time) time.Time`
- `internal/gateway/handlers/history.go:61` — `startOfDay(t time.Time) time.Time`
- `internal/telemetry/service.go:559` — `dateOnly(t time.Time, loc *time.Location) time.Time`

**Finding, verified by reading all three files directly (not assumed from the roadmap's line
references):** the roadmap's tier table lists `internal/gateway/handlers/supercharger.go:58` as
a *fourth* truncator, `startOfDay`. That line number is `startOfMonth` in the current source —
a different, coarser function that itself calls `startOfDay` (line 134, 169, 201, 520 of
`supercharger.go`) rather than redefining it. `startOfDay` is defined exactly once, in
`history.go`, and both files share it because they are the same Go package (`handlers`). So
there are **three** distinct truncator implementations to collapse into `clock.CalendarDay`, not
four — the roadmap's per-tier task list for tier 5 (`RM35-gateway-adopt-clock`) already reads
correctly either way, since "history.go:62 + supercharger.go:58 `startOfDay` → `clock.CalendarDay`"
names one symbol used from two files, not two symbols. This does not change tier 1's deliverable;
it is noted here so the leader and tier 5's implementer do not go looking for a second gateway
definition that does not exist.

Performance profile: **read-heavy** (`ai/architecture.md` §7). Not relevant to this tier's own
work — `internal/clock` is a pure, in-memory function library with no table, no query, and (since
nothing imports it yet) no request path, hot or otherwise. It becomes relevant only once a later
tier calls `CalendarDay` from a real read or write path; that tier's own proposal evaluates it.

## Goals / Non-Goals

**Goals:**
- Give the platform exactly one place that decides the default time zone (`America/Bogota`) and
  exactly one function that normalizes a moment to its calendar day in a given zone, matching
  D2's "one concern per package, named for the concern."
- Reproduce, byte-for-byte, the current output of all three existing truncators for every input
  they are ever called with today, so that when tiers 2–6 swap their call sites the existing
  suites are the regression net (roadmap D4: "behavior-preserving except the documented default
  change").
- Keep `internal/clock` genuinely minimal — stdlib `time` only, no non-time helper ever added
  (D2) — so no module can create an import cycle through it and its own `AGENTS.md` can state a
  one-line import rule an agent can enforce by inspection.

**Non-Goals (out of scope for this tier, all explicitly deferred to a later tier or never):**
- Adopting `clock` anywhere. Zero call sites in `config`, `telemetry`, `analytics`, `app`, or
  `gateway` change in this tier (D4 — tiers 2–6).
- `make tz-guard` or any `// tz:allow:` escape-hatch convention (D5 — tier 7, shipped last, once
  every adopting tier has migrated).
- Touching the `pgtype.Date` storage encoding or the `browser_tz` cookie (D1 — explicitly
  unchanged, forever, by this roadmap).
- Any migration, backfill, or database object of any kind — this tier owns no table.
- Writing the actual Go code, the `AGENTS.md`, or the doc edits this proposal describes — all of
  that is `tasks.md` work for the implementation phase, authorized separately after the user
  reviews this proposal (per this tier's dispatch: artifacts only).

## Decisions

The following restate the roadmap's binding decisions
(`openspec/roadmaps/RM35-timezone-centralization.md`) as they apply to this tier, per the
design-gate requirement that this document be self-contained, followed by this tier's own
implementation decisions (D7 onward — the roadmap's own decisions stop at D6).

### D1 — Scope: defaults only, zero migrations (restated, binding)

`America/Bogota` becomes the default wherever a zone is currently missing or falls back. The
`pgtype.Date` UTC-midnight storage encoding and the `browser_tz` cookie are explicitly
**unchanged** — this tier writes no migration and no DB code at all (it owns no table), so D1's
"zero migrations, zero backfill" is trivially satisfied by this tier's shape rather than by an
active choice.

### D2 — Package: `internal/clock`, stdlib `time` only (restated, binding)

One concern per package, named for the concern — never `internal/util` (the `shared`/`common`
anti-pattern named in `ai/architecture.md:72`), never an extension of `internal/config` (which is
a `cmd/`-level concern today; importing it from `telemetry`/`analytics`/`gateway`/`app` would
invert the dependency direction). `internal/clock` imports **only** the stdlib `time` package
(and, per D9 below, its companion `time/tzdata` — see that decision for why this is still
compliant) — nothing else, ever. Its own `AGENTS.md` (a `tasks.md` deliverable) states this as
the module's one-line defining constraint.

### D3 — Docs mirror the units rule (restated, binding)

The full convention goes in `ai/go-conventions.md` §Coding Rules, immediately next to the
existing `_km`/`_c`/`_psi` display-units rule. `CLAUDE.md` gets a one-line non-negotiable
pointing at it. Root `AGENTS.md` gets a **new** pointer to `ai/go-conventions.md` (it has none
today — the roadmap identifies this gap explicitly). All three edits are `tasks.md` items (T4,
T5, T6), not made by this proposal.

### D4 — This tier's job within the sweep (restated, binding)

Tiers 2–6 do the actual sweep (zone fallback → `clock.Zone()`, `time.Now()` → `clock.Now()`, the
three truncators → `clock.CalendarDay`). This tier's only job is to make `CalendarDay` capable of
replacing all three without changing what any of them currently return for any input they are
called with today — see D7 below for exactly how.

### D6 — Unit tests: this tier only (restated, binding)

New `_test.go` coverage is written for `internal/clock` alone. This narrows the owner's standing
"no unit tests" default by explicit decision: an off-by-one in `CalendarDay` would shift day
attribution across telemetry, analytics, and charging simultaneously once adopted. See "Test
Contract" below for the exact expected values, authored before any implementation exists
(`ai/go-conventions.md` §Testing "author their expected values up front").

### D7 — `CalendarDay` signature: an explicit `loc *time.Location` parameter, not an implicit default (tier-1 decision)

**The three existing truncators disagree on shape.** `analytics.calendarDay(t)` and
`gateway/handlers.startOfDay(t)` are byte-identical in behavior — both call `t.UTC()` then strip
the time-of-day, i.e. they always bucket by the moment's *UTC* calendar day, with no zone
parameter at all. `telemetry.dateOnly(t, loc)` instead converts `t` into an explicit `loc`
**before** extracting the calendar day, then re-expresses the result as UTC midnight — a
genuinely zone-aware computation, not a UTC-only truncation. These are not the same operation
with different call syntax; `dateOnly` produces a different calendar day than `startOfDay` for
any moment whose local date in `loc` differs from its UTC date (exactly the case that matters
near a day boundary).

**Decision: `CalendarDay` always takes an explicit `*time.Location`, computes the calendar date
by converting into it (`t.In(loc).Date()`), and returns that date at UTC midnight** (preserving
the storage encoding D1 requires unchanged):

```go
func CalendarDay(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
```

This is `dateOnly`'s exact formula, generalized. It reproduces every existing call site's
current output without any adopting tier having to special-case anything:

- `telemetry.dateOnly(t, loc)` → `clock.CalendarDay(t, loc)` — literally the same call, same
  result, for every `t`/`loc` telemetry already passes today.
- `analytics.calendarDay(t)` and `gateway.startOfDay(t)` → `clock.CalendarDay(t, time.UTC)` —
  because `t.In(time.UTC)` and `t.UTC()` are the same conversion, this reproduces both functions'
  current output exactly, for every `t`.

**Why not fold a default zone into `CalendarDay` itself** (e.g. a zero-arg or "loc defaults to
`clock.Zone()` when nil" form) — rejected. Two of the three current call sites (`analytics`,
`gateway`) deliberately bucket by UTC today, not by any configurable zone; silently defaulting
their calendar-day computation to `America/Bogota` the moment they adopt `clock.CalendarDay`
would be an undocumented behavior change smuggled into what D4 calls a "behavior-preserving"
sweep. Keeping the zone parameter explicit and mandatory pushes that decision to each adopting
tier's own proposal, where it belongs — `clock` stays a thin, honest wrapper with no hidden
default substitution, consistent with D2's minimalism.

**No nil-guard on `loc`.** Passing `nil` panics, exactly as calling `t.In(nil)` directly would
(Go's own `time.Time.In` panics on a nil `*Location`). This is deliberate: adding a
nil-check-and-substitute-`Zone()` branch would be exactly the kind of hidden default-substitution
logic the paragraph above rejects, just moved one level down.

### D8 — Finding: gateway's two roadmap-listed call sites are one function, not two (tier-1 finding, not a decision)

Recorded here rather than only in "Context" because it affects how tier 5
(`RM35-gateway-adopt-clock`) should be read: `history.go:62`'s `startOfDay` and
`supercharger.go:58` are not two separate `startOfDay` definitions — `supercharger.go:58` is
`startOfMonth`, a different, coarser function that itself *calls* `startOfDay` from
`history.go` (same package). Tier 5 therefore needs to change exactly one function definition
(in `history.go`) to make every call site across both files pick up `clock.CalendarDay`, not two.

### D9 — `Zone()`: computed once via `time.LoadLocation`, `time/tzdata` blank-imported, panics on failure (tier-1 decision)

```go
import (
	"time"
	_ "time/tzdata" // embeds the IANA database so LoadLocation never depends on the host OS
)

var platformZone = func() *time.Location {
	loc, err := time.LoadLocation("America/Bogota")
	if err != nil {
		panic("internal/clock: failed to load America/Bogota: " + err.Error())
	}
	return loc
}()

func Zone() *time.Location { return platformZone }
```

**Why blank-import `time/tzdata`.** `time.LoadLocation` normally reads the IANA database from
the host OS (`$ZONEINFO`, a well-known system path, or a Go-toolchain-bundled copy) — which is
not guaranteed present on every deployment base image (a minimal/distroless container, in
particular). `time/tzdata` is itself part of the Go standard library — it embeds the database
into the binary at build time — so this stays within the spirit of D2's "stdlib `time` only"
even though it is a second import path: it is `time`'s own official companion for exactly this
situation, not a third-party dependency. **Flagged explicitly for the user's review** (not
settled by D1–D6): this trades a small binary-size increase (order of a few hundred KB) for
never depending on the deployment environment's own tzdata. If the user prefers to rely on the
host OS's tzdata instead (smaller binary, a real but so-far-never-observed risk on this
project's deployment targets), dropping the blank import is a one-line change to make during
implementation — called out again in `tasks.md` T1.

**Why panic instead of silently falling back to UTC.** `America/Bogota` is a canonical,
permanently-stable IANA zone name; with `time/tzdata` embedded, `LoadLocation` cannot fail for it
in practice. A silent UTC fallback would defeat the entire point of this roadmap without anyone
noticing until day-attribution started drifting; a boot-time panic is loud, immediate, and
trivially caught by any deploy or CI smoke check — the far safer failure mode for a value every
other tier will treat as the platform's foundation.

### D10 — `LoadOrDefault`: no special-casing beyond what `time.LoadLocation` already does (tier-1 decision)

```go
func LoadOrDefault(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return Zone()
	}
	return loc
}
```

**Documented gotcha, not a bug:** Go's `time.LoadLocation` special-cases two inputs before ever
touching the tz database — `""` and `"UTC"` both return UTC with **no error**, and `"Local"`
returns the process's `time.Local` (host/environment-dependent) with no error. Because
`LoadOrDefault` adds no logic beyond "did `LoadLocation` error," these inputs do **not** fall
back to `Zone()` — `LoadOrDefault("")` and `LoadOrDefault("UTC")` both return UTC, and
`LoadOrDefault("Local")` returns whatever the host considers local, not `America/Bogota`. This
matters for tier 2 (`RM35-config-adopt-clock`): if `POLLER_TIMEZONE` is ever an empty string
(as opposed to genuinely unset), routing it through `LoadOrDefault("")` would silently produce
UTC, not the intended Bogota default — tier 2's own proposal should treat "unset" as "call
`Zone()` directly," not as "pass `""` to `LoadOrDefault`." Recorded here so that gotcha is not
rediscovered as a surprise during tier 2.

## Test Contract

D6 adds new coverage for `internal/clock` alone. Per `ai/go-conventions.md` §Testing, the
expected values are authored here, before the implementation exists.

**`Zone()`**
```go
if clock.Zone().String() != "America/Bogota" {
    t.Fatalf("Zone() = %s, want America/Bogota", clock.Zone())
}
```

**`Now()`**
```go
before := time.Now()
got := clock.Now()
after := time.Now()

if got.Location().String() != "America/Bogota" {
    t.Fatalf("Now().Location() = %s, want America/Bogota", got.Location())
}
if got.Before(before.Add(-2*time.Second)) || got.After(after.Add(2*time.Second)) {
    t.Fatalf("Now() = %v, want within [%v, %v] (allowing test execution slack)", got, before, after)
}
```
(The instant `Now()` returns must match the real current time regardless of which zone it is
*expressed* in — comparing `time.Time` values compares the underlying instant, not the zone, so
this assertion is zone-agnostic by construction. A small slack window absorbs test-execution
time, not clock drift.)

**`LoadOrDefault`**
```go
cases := []struct {
    name string
    want string // .String() of the returned *time.Location
}{
    {"America/New_York", "America/New_York"}, // valid IANA name → itself
    {"Not/AZone", "America/Bogota"},           // unresolvable → falls back to Zone()
    {"", "UTC"},                                // stdlib special case — NOT Bogota (D10)
    {"UTC", "UTC"},                              // stdlib special case
    {"Local", "Local"},                          // stdlib special case — host-dependent, not Bogota
}
```

**`CalendarDay`** — four scenarios, each with concrete, transcribable `time.Date` literals:

1. UTC input, UTC zone — a moment already at its own UTC-day start:
   ```go
   in := time.Date(2026, 6, 15, 14, 30, 0, 0, time.UTC)
   want := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
   got := clock.CalendarDay(in, time.UTC)
   ```

2. Cross-day zone conversion — Bogota is UTC-5, no DST, ever (Colombia abolished DST in 1993):
   a moment that is `2026-06-16` in UTC is still `2026-06-15` in Bogota local time:
   ```go
   in := time.Date(2026, 6, 16, 3, 0, 0, 0, time.UTC) // = 2026-06-15 22:00 in Bogota (UTC-5)
   want := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC) // the PREVIOUS day, not June 16
   got := clock.CalendarDay(in, clock.Zone())
   ```
   This is the case that distinguishes `CalendarDay` from a naive `t.UTC()` truncation (which
   would incorrectly return June 16 here) — proof that zone-aware bucketing, not UTC-only
   bucketing, is what the function does when given a non-UTC zone.

3. DST spring-forward transition, same local day either side (`America/New_York`, 2026-03-08,
   the US's second Sunday in March, 02:00→03:00 local, EST→EDT):
   ```go
   ny, _ := time.LoadLocation("America/New_York")

   beforeTransition := time.Date(2026, 3, 8, 6, 59, 0, 0, time.UTC) // = 01:59 EST
   afterTransition := time.Date(2026, 3, 8, 7, 1, 0, 0, time.UTC)   // = 03:01 EDT
   want := time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC)

   gotBefore := clock.CalendarDay(beforeTransition, ny) // want
   gotAfter := clock.CalendarDay(afterTransition, ny)   // want, same day — the 2am→3am
                                                          // jump does not perturb the calendar day
   ```

4. DST-adjacent cross-day case, proving zone-aware bucketing still holds right next to a
   transition (same zone/date as case 3, but the instant's UTC date and NY local date differ):
   ```go
   in := time.Date(2026, 3, 8, 4, 30, 0, 0, time.UTC) // = 2026-03-07 23:30 EST (pre-transition)
   want := time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC) // the PREVIOUS day, not March 8
   got := clock.CalendarDay(in, ny)
   ```

Cases 1–4 together are the regression net for tiers 2–6: any adopting tier that swaps a
truncator for `clock.CalendarDay(t, <the same loc it always used>)` is, by D7's construction,
guaranteed byte-identical output for every input its existing tests already exercise.

## Risks / Trade-offs

- **[Trade-off]** `time/tzdata` blank import adds a few hundred KB to every binary that imports
  `internal/clock` (which, after tiers 2–6, is effectively every binary in `cmd/`) →
  **Flagged for the user's confirmation** (D9) — not a cost the roadmap's D1–D6 anticipated;
  accepted here as the safer default, reversible with a one-line removal if the user prefers to
  rely on host tzdata instead.
- **[Trade-off]** `Zone()` panics at package-init time if `America/Bogota` ever fails to load →
  **Accepted**: with `time/tzdata` embedded this cannot happen in practice for a canonical IANA
  name; the alternative (silent UTC fallback) is a strictly worse failure mode for a value every
  other module will treat as authoritative.
- **[Risk]** `LoadOrDefault("")` and `LoadOrDefault("Local")` do not fall back to `Zone()` (D10),
  which could surprise a caller conflating "unset" with "empty string" → **Mitigation**:
  documented explicitly here and flagged for tier 2's own proposal, which is the one tier that
  actually threads a config-sourced string through `LoadOrDefault`.
- **[Finding, not a risk]** The roadmap's tier table names four truncator call sites; only three
  distinct implementations exist (D8) — does not change this tier's deliverable, flagged so tier
  5 is not surprised.

## Implementation Plan

(No "Migration Plan" — this tier owns no database object.)

1. `internal/clock/clock.go` — package doc comment (states the module's `AGENTS.md`-mirrored
   constraint: stdlib `time` only), `Zone()`, `Now()`, `LoadOrDefault()` (D9, D10).
2. `internal/clock/calendar.go` — `CalendarDay()` (D7).
3. `internal/clock/clock_test.go` / `internal/clock/calendar_test.go` — the Test Contract above,
   transcribed verbatim (D6).
4. `internal/clock/AGENTS.md` — module responsibility, public interface, the stdlib-`time`-only
   constraint, data ownership (none), testing notes.
5. `ai/go-conventions.md` §Coding Rules — the full convention, next to the `_km`/`_c`/`_psi` rule
   (D3).
6. `CLAUDE.md` — one-line non-negotiable pointing at the `ai/go-conventions.md` entry (D3).
7. Root `AGENTS.md` — new pointer to `ai/go-conventions.md` (D3; the gap the roadmap identifies).
8. Root `README.md` — "Project Structure" tree + "Architecture" table gain `internal/clock`
   (`CLAUDE.md`'s docs-track-structural-change rule — a new module, same change).
9. `go build ./...`, `go vet ./...`, `gofmt -l` pass. `go test ./...` is the owner's step
   (`Test-Execution-Policy`).

**Rollback:** delete `internal/clock/` and revert the four doc edits — nothing else in the repo
references the package yet, so this is a clean, single-commit revert with zero downstream
consequences (no adopting tier exists yet).

## Open Questions

None blocking this tier's proposal. Two items above are explicitly flagged for the user's
confirmation rather than silently decided: the `time/tzdata` blank import (D9's binary-size
trade-off) and the `LoadOrDefault("")`/`("Local")` pass-through behavior (D10, relevant to tier
2's design). Neither blocks this tier — both are implementation-detail decisions this tier's
own artifacts settle, flagged for visibility, not left as an open question requiring an answer
before Apply.
