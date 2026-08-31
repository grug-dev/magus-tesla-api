## Context

Tier 1 (`RM35-clock-add-bogota-time-package`, archived) built `internal/clock` — `Zone()`,
`Now()`, `LoadOrDefault(name)`, `CalendarDay(t, loc)` — and adopted it nowhere. This tier is
the first of five adopting tiers (roadmap `RM35-timezone-centralization`, D4). Its scope is
one fallback in one file:

```go
// internal/config/config.go:87-90 (before this tier)
cfg.PollerTimezone = envStripped("POLLER_TIMEZONE")
if cfg.PollerTimezone == "" {
    cfg.PollerTimezone = "Local"
}
```

`"Local"` is a `time.LoadLocation` special case that resolves to the host process's own zone
— environment-dependent, and never `America/Bogota` unless the host happens to be configured
for it. Downstream, `cmd/poller/main.go:83` does `time.LoadLocation(cfg.PollerTimezone)` and
`log.Fatalf`s on error, so whatever string this module produces must always be resolvable.

Performance profile: **read-heavy** (`ai/architecture.md` §7). Not relevant to this tier's own
work — `config.Load()` runs once per process at startup (`cmd/setup`, `cmd/web`, `cmd/poller`),
never on a per-request or per-poll-cycle path, and the added logic is a single string
comparison plus, in the unset case, a call to `internal/clock`'s already-computed
`platformZone` (no I/O, no allocation beyond the string).

## Goals / Non-Goals

**Goals:**
- Make the `POLLER_TIMEZONE` unset-fallback obtain its zone name from `internal/clock` — the
  platform's sole owner of the default zone (D2) — rather than hard-coding `"Local"` or any
  zone-name literal in `internal/config`.
- Avoid the `LoadOrDefault("")` trap tier 1 documented (D10): substitute the default **before**
  any resolution call, so an unset `POLLER_TIMEZONE` can never silently become UTC.
- Give the previously-uncovered unset-default branch direct, offline test coverage (the one
  exception RM35 D6 permits this tier).

**Non-Goals (out of scope for this tier):**
- Validating `POLLER_TIMEZONE` inside `internal/config`. `cmd/poller` already validates it via
  `time.LoadLocation` + `log.Fatalf`; duplicating that here would be redundant and is explicitly
  out of scope — `cmd/*` is the composition root (roadmap D4) and this tier does not touch it.
- Any migration, backfill, or database object — this module owns none and this tier adds none.
- Adopting `clock.Now()` or `clock.CalendarDay` anywhere — `internal/config` has no "now" or
  calendar-day computation to replace; those belong to tiers 3–6 (`telemetry`, `analytics`,
  `app`, `gateway`).
- Touching `cmd/poller`, `cmd/web`, or `cmd/setup` — all three call `config.Load()` and are
  therefore indirectly affected, but none is edited (RM35 D4's composition-root exemption).

## Decisions

Roadmap decisions restated as they apply to this tier, followed by this tier's own decision
(D-config-1).

### D1 — Scope: defaults only, zero migrations (restated, binding)

`America/Bogota` becomes the default wherever a zone is currently missing or falls back. This
tier's only fallback is `POLLER_TIMEZONE`'s empty-string case. No migration, no DB object —
this module owns no table.

### D2 — `internal/clock` is the sole owner of the default zone name (restated, binding)

`internal/config` obtains the name via `clock.Zone().String()`, never a literal
`"America/Bogota"` string. `internal/clock` imports stdlib `time` only, and `internal/config`
already sits below every domain module in the dependency direction, so importing `clock` from
`config` creates no cycle (`clock` imports nothing project-local at all).

### D4 — This tier's job within the sweep (restated, binding)

Per the roadmap's tier table: "`PollerTimezone` default `"Local"` → `clock` default
(`America/Bogota`), via `clock.LoadOrDefault`. `internal/config/config.go:87-89`." This design
deliberately does **not** call `clock.LoadOrDefault` on the raw env value — see D-config-1 below
for why the roadmap's literal wording is corrected in light of tier-1's own documented D10
gotcha, which postdates the roadmap's tier-table cell and explicitly calls out this exact case.

### D6 — Unit tests: this tier's one exception (restated, binding)

RM35 D6: tiers 2–6 add no new tests except to repair breakage. This tier's `POLLER_TIMEZONE`
default previously had **zero** test coverage (no existing test in `internal/config` exercised
the unset-env-var branch — `quote_test.go` covers only `envStripped`). Because that default is
now the thing standing between the platform and a silent UTC poller, one new test is added
(D6's own narrow-exception clause; see "Test Contract" below).

### D-config-1 — Substitute the default before resolution; do not route `""` through `LoadOrDefault` (this tier's decision)

**Decision:** implement the fallback as a small, directly testable function:

```go
func pollerTimezoneOrDefault(v string) string {
    if v != "" {
        return v
    }
    return clock.Zone().String()
}
```

called as `cfg.PollerTimezone = pollerTimezoneOrDefault(envStripped("POLLER_TIMEZONE"))`,
replacing the `if cfg.PollerTimezone == "" { cfg.PollerTimezone = "Local" }` block entirely.

**Why not `clock.LoadOrDefault(envStripped("POLLER_TIMEZONE"))`, despite the roadmap's tier
table naming it explicitly:** tier-1's own design (`RM35-clock-add-bogota-time-package`,
design.md D10, restated in `internal/clock/AGENTS.md`) documents that
`time.LoadLocation("")` — which `LoadOrDefault` calls with no special-casing beyond
error-checking — resolves to **UTC**, with no error, so `LoadOrDefault` never reaches its own
fallback for an empty string. Passing an unset `POLLER_TIMEZONE` (`""`) through
`LoadOrDefault` would therefore silently produce UTC, not `America/Bogota` — the single
outcome this entire roadmap exists to prevent. D10 flags this by name as "a note for tier 2's
own proposal," so this design treats that flag as binding over the roadmap table's shorthand
wording rather than as a conflicting instruction.

The fix is ordering, not a different function: decide "is this unset?" first (a plain
string-emptiness check config already had), and only ever hand `LoadOrDefault`/
`LoadLocation` a value that is either genuinely user-supplied or the already-resolved default
name. This keeps `internal/clock` exactly as thin as D2 requires — no special-casing added
there for `config`'s benefit — while keeping the trap closed at the one call site that matters.

**Why a named helper function instead of inlining the branch:** `Load()` cannot be unit-tested
directly without a real `.env` file and `TESLA_CLIENT_ID`/`TESLA_CLIENT_SECRET` set in the
process environment (its existing, pre-tier constraint — see `internal/config/AGENTS.md`
§Testing). Every other env-derived field with interesting branch logic (`envStripped`,
`envInt`, `envDuration`) is already a small pure function tested directly, without going
through `Load()`. `pollerTimezoneOrDefault` follows that existing shape rather than introducing
a new testing pattern — it is the minimal extraction that makes the previously-uncovered branch
testable, not a new abstraction layer (`CLAUDE.md`'s AI-efficiency "do not over-abstract" rule:
one function, one call site, zero indirection beyond what testability requires).

**Rejected — a named zone-name constant added to `internal/clock`:** the dispatch grant allowed
adding one if it would be cleaner. Rejected because `clock.Zone().String()` is already a single
expression that reads the *same* computed value `Zone()` itself uses (`platformZone`,
`time.LoadLocation("America/Bogota")`) — introducing a parallel string constant would create two
sources that must agree by convention rather than by construction, for no reduction in caller
complexity. `clock.Zone().String()` stays the single source of truth.

## Test Contract

Authored before implementation, per `ai/go-conventions.md` §Testing ("author their expected
values up front"). One new test, `internal/config/timezone_test.go`
(`TestPollerTimezoneOrDefault_Unset`), covering the RM35-D6-permitted exception — the unset
case only:

```go
got := pollerTimezoneOrDefault("")
want := clock.Zone().String() // == "America/Bogota"
// got must equal want, and want must equal the literal "America/Bogota"
```

**Set-but-valid** (e.g. `"America/New_York"`) and **set-but-invalid** (e.g. `"Not/AZone"`)
cases are *not* given new tests — both are unchanged, trivial pass-through behavior
(`pollerTimezoneOrDefault` returns `v` unmodified whenever `v != ""`), and RM35 D6 confines new
coverage in this tier to the one branch that previously had none. Their expected behavior is
recorded here for completeness rather than as a test obligation:
- `pollerTimezoneOrDefault("America/New_York")` → `"America/New_York"` (untouched).
- `pollerTimezoneOrDefault("Not/AZone")` → `"Not/AZone"` (untouched) — `cmd/poller`'s own
  `time.LoadLocation` + `log.Fatalf` is what rejects this at process startup, exactly as it did
  before this tier; `internal/config` performs no validation, before or after.

## Risks / Trade-offs

- **[Risk, accepted, this tier's whole point]** Any deployment relying on the implicit
  `"Local"` default now runs its poller in `America/Bogota` instead of the host's zone —
  a real behavior change for the poller's 03:30 schedule interpretation and its calendar-day
  attribution. **Mitigation:** documented plainly in `proposal.md`'s "Breaking" section, not
  softened; an operator who wants the old behavior can set `POLLER_TIMEZONE` explicitly to the
  host's zone name.
- **[Trade-off]** `internal/config` now imports `internal/clock` — a new inter-package
  dependency where previously `config` imported no project-local package. **Accepted:**
  `internal/clock` imports nothing project-local itself (stdlib `time` only, D2), so this
  cannot create a cycle, and it is the dependency direction the roadmap's whole premise
  requires — `config` is meant to obtain the default zone from `clock`, not reinvent it.
- **[Non-risk, confirmed]** `cmd/poller`'s existing fatal-on-invalid-zone behavior is unchanged
  — this tier touches no file under `cmd/`.

## Migration Plan

None — this tier owns no database object, adds no table, column, index, or constraint, and
runs no migration. Rollback is a plain revert of the two touched/added Go files and the new
`AGENTS.md`; nothing downstream depends on this tier yet (tiers 3–6 are independent, and
`cmd/poller` reads `cfg.PollerTimezone` as an opaque string exactly as it did before).

## Open Questions

None. The one apparent conflict — the roadmap tier table's literal "via `clock.LoadOrDefault`"
wording versus tier-1's own D10 gotcha — is resolved by D-config-1 above in favor of D10, which
the roadmap itself flags as the authoritative note for this tier ("Recorded here so that gotcha
is not rediscovered as a surprise during tier 2").
