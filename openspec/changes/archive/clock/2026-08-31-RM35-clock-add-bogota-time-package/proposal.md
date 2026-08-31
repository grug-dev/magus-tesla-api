Source: MAG-20 — https://linear.app/magus-monitor/issue/MAG-20/datedate-time-time-zone
Roadmap: openspec/roadmaps/RM35-timezone-centralization.md
Tier: 1 of 7 (`clock`, new module; tiers 2–6 adopt it per-module — `config`, `telemetry`,
`analytics`, `app`, `gateway` — each depending on this tier; tier 7,
`RM35-platform-add-tz-guard`, ships last once every adopting tier is merged)

## Why

MAG-20's acceptance criterion "the app does not use UTC" is scoped by roadmap decision **D1**:
it means the platform's *default* time zone is never UTC, not that the string `time.UTC`
disappears from the codebase. Today the app already has three deliberate, separately-documented
time zones (poller config, the per-user `browser_tz` cookie, and the `pgtype.Date` UTC-midnight
storage encoding) — see the roadmap's "Findings" section. What it does NOT have is one place that
owns "what zone applies when nothing else says otherwise," and it has three separate
copy-pasted calendar-day truncators doing the same UTC-midnight normalization with subtly
different signatures.

This is **tier 1 of 7** of roadmap `RM35-timezone-centralization`
(`openspec/roadmaps/RM35-timezone-centralization.md`), which captures the binding decisions
(D1–D6) already agreed with the user before any artifact was written. This proposal and its
sibling artifacts create the `internal/clock` package itself — `Zone()`, `Now()`,
`CalendarDay(t, loc)`, `LoadOrDefault(name)` — plus its unit tests (D6, tier-1-only) and the
docs D3 requires. **This tier adopts `clock` nowhere.** Tiers 2–6 (each a separate, dependent
OpenSpec change) are the ones that switch `config`, `telemetry`, `analytics`, `app`, and
`gateway` over to calling it; tier 7 adds the `make tz-guard` enforcement once every adopter has
migrated (D5 — wiring the guard before every module adopts would fail `make check` for up to six
tiers).

## What Changes

- **New package `internal/clock`** — stdlib `time` only (D2), never `internal/util`, never an
  extension of `internal/config` (both explicitly rejected in the roadmap). Public surface:
  - `Zone() *time.Location` — the platform default, `America/Bogota`.
  - `Now() time.Time` — the current time expressed in `Zone()`.
  - `LoadOrDefault(name string) *time.Location` — parses an IANA zone name, falling back to
    `Zone()` when the name does not resolve.
  - `CalendarDay(t time.Time, loc *time.Location) time.Time` — the single replacement for the
    project's copy-pasted UTC-midnight truncators (see `design.md` D7 for the signature
    reconciliation across the three existing implementations, and a finding: the roadmap lists
    four call sites, but two of them — `gateway/handlers/history.go:62` and
    `gateway/handlers/supercharger.go:58` — already share ONE function definition, so there are
    three distinct implementations to collapse, not four).
- **`internal/clock/AGENTS.md`** (a task for the implementer, not written by this proposal —
  see `tasks.md` T3): `Agent-Name: clock`, an empty module Doc-Pack, the module's
  responsibility, its public interface, and its defining constraint — stdlib `time` only, never
  extended with unrelated helpers.
- **Docs (D3)** — also implementation tasks, not written by this proposal: the full convention in
  `ai/go-conventions.md` §Coding Rules (next to the existing `_km`/`_c`/`_psi` display-units
  rule), a one-line non-negotiable in `CLAUDE.md`, and a **new** pointer to `ai/go-conventions.md`
  in the root `AGENTS.md` (it has none today).
- **Root `README.md`** — "Project Structure" tree gains `internal/clock/`, and the "Architecture"
  table gains a row for it, per `CLAUDE.md`'s docs-track-structural-change rule (a new module is
  being added).
- **Unit tests (D6)** — pure, offline, no DB, no container: the one package this roadmap narrows
  the owner's standing "no unit tests" default for, on the grounds that an off-by-one in
  `CalendarDay` would shift day attribution across telemetry, analytics, and charging
  simultaneously once tiers 2–6 adopt it.

**This proposal creates OpenSpec artifacts only — zero Go code.** The package, its tests, its
`AGENTS.md`, and the doc edits above are all captured as `tasks.md` items for the implementation
phase, which the user authorizes separately after reviewing this proposal.

**Not breaking.** `internal/clock` is a brand-new package with zero importers as of this tier —
no existing code path changes behavior, because nothing calls it yet. The only files touched
that already exist are documentation (`ai/go-conventions.md`, `CLAUDE.md`, `AGENTS.md`,
`README.md`) — no Go source, no schema, no config.

**Affected modules:** none, in the runtime sense — `internal/clock` is new and adopted by no
other module in this tier. `internal/config`, `internal/telemetry`, `internal/analytics`,
`internal/app`, and `internal/gateway` are the intended future consumers, each via its own
dependent tier (2–6) — none of them is touched here.

## Capabilities

### New Capabilities

- `clock` — the platform's time-zone-and-calendar-day-normalization capability: a single default
  zone (`America/Bogota`), the current time expressed in it, IANA zone-name resolution with a
  safe fallback, and one way to normalize any moment to its calendar day in a given zone.

### Modified Capabilities

(none — every other capability in this roadmap is modified by its own dependent tier, not this
one)

## Impact

- `internal/clock` — new package, four exported symbols, unit tests, `AGENTS.md`.
- Docs — `ai/go-conventions.md`, `CLAUDE.md`, root `AGENTS.md`, root `README.md`.
- No other module's Go source, schema, or config changes in this tier.

**No hot read path and no database are touched by this tier.** `internal/clock` is a pure,
stateless, in-memory function library (per `openspec/config.yaml`'s performance-sensitivity
rule, stated explicitly rather than omitted): it owns no table, runs no query, and — because
nothing imports it yet — sits on no request path, hot or otherwise. The read-heavy performance
profile (`ai/architecture.md` §7) becomes relevant only once a later tier calls `CalendarDay` (a
pure function, O(1), no I/O) from an actual read or write path — evaluated in that tier's own
proposal, not here.
