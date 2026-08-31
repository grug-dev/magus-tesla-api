Source: MAG-20 — https://linear.app/magus-monitor/issue/MAG-20/datedate-time-time-zone
Roadmap: openspec/roadmaps/RM35-timezone-centralization.md
Tier: 2 of 7 (`config`; depends on tier 1, `RM35-clock-add-bogota-time-package`, already
archived and on disk at `internal/clock`)

## Why

Tier 1 built `internal/clock`, the platform's single owner of the default time zone
(`America/Bogota`), but adopted it nowhere. This tier — `RM35-config-adopt-clock` — is the
**first adopter**: `internal/config`'s `POLLER_TIMEZONE` env-var fallback, which today
resolves to `"Local"` (the host's own zone), changes to `internal/clock`'s platform default.

This is the change roadmap decision **D1** ("America/Bogota becomes the default wherever a
zone is currently missing or falls back") is actually about for this module, and it is the
first tier to make `ai/go-conventions.md`'s time-zone convention enforceable rather than
aspirational.

## What Changes

- `internal/config/config.go:87-90` — the `POLLER_TIMEZONE` fallback changes from the
  literal string `"Local"` to the platform's default zone name, obtained from
  `internal/clock.Zone().String()` (never hard-coded in this module — `ai/go-conventions.md`
  and RM35 D2 make `internal/clock` the sole owner of that name). Extracted into a small,
  independently testable function, `pollerTimezoneOrDefault(v string) string`, so the
  unset-vs-set branch has direct test coverage without depending on a real `.env` file
  (`Load()`'s existing constraint).
  - **Deliberately not implemented via `clock.LoadOrDefault("")`.** `time.LoadLocation("")`
    resolves to UTC with no error — a stdlib special case `LoadOrDefault` inherits unchanged
    (tier-1 design D10) — so routing an unset `POLLER_TIMEZONE` through it would silently
    produce UTC, the exact outcome this whole roadmap exists to prevent. The default is
    substituted **before** any call to `LoadOrDefault`/`LoadLocation`, closing that trap.
  - A **set** value — valid or not — still passes through untouched. This module does not
    validate the zone name; `cmd/poller` (out of scope for this tier, per RM35 D4's
    composition-root exemption) still does, via `time.LoadLocation` + `log.Fatalf`, unchanged.
- `internal/config/AGENTS.md` — **created** (did not exist before this tier). Mirrors
  `internal/account/AGENTS.md`'s shape: `Agent-Name: config`, an empty module Doc-Pack, the
  module's responsibility, public interface, allowed imports (now including `internal/clock`),
  data ownership (none), and testing notes.
- One new unit test, `internal/config/timezone_test.go` — the single narrow exception RM35 D6
  permits tiers 2–6 (which otherwise add no new tests): the unset-`POLLER_TIMEZONE` default had
  zero existing coverage, and it is now the thing standing between the platform and a silent
  UTC poller.

**No documentation elsewhere states the old `"Local"` default.** A repo-wide check (README,
`docs/0-set-up/*.md`, `.env` / `.env.example`, and every other `.md`/`.env*` file) found no
occurrence of `POLLER_TIMEZONE` describing a default value outside this roadmap's own planning
docs and `internal/telemetry/AGENTS.md`, which mentions the env var only to explain why
`captured_date` is Go-computed rather than a DB expression — it never states a default, so it
needed no change. `.env` and `.env.example` do not set `POLLER_TIMEZONE` at all.

**Breaking — a deliberate, documented behavior change (this tier's whole point).** Any
deployment that does not set `POLLER_TIMEZONE` moves its poller's calendar-day bucketing and
its 03:30 schedule interpretation from the **host's local zone** to **`America/Bogota`**. On a
host already configured for `America/Bogota` (or UTC-5 with no DST) this is a no-op in
practice; on any other host it changes which wall-clock moment `03:30` means and which calendar
day a given capture is attributed to. This is exactly the behavior roadmap D1 authorizes and is
not softened here.

**No database object, no hot read path.** `internal/config` owns no table and this change adds
none; `Load()` runs once per process at startup (`cmd/setup`, `cmd/web`, `cmd/poller`), never on
a per-request or per-poll-cycle path.

## Capabilities

### New Capabilities

- `config` — did not previously have a spec capability (`openspec/specs/` has no `config`
  folder). Named for the existing `internal/config` module (mirrors the project's module ↔
  capability mapping, `openspec/config.yaml`), scoped narrowly in this tier to the one behavior
  this change touches: the `POLLER_TIMEZONE` default. Broader `config` behavior (`.env` loading,
  token persistence, other typed fields) is not specified here — this proposal adds only the
  requirement this tier's code actually implements.

### Modified Capabilities

(none)

## Impact

- `internal/config/config.go` — one fallback branch changed, one small function extracted.
- `internal/config/AGENTS.md` — new file.
- `internal/config/timezone_test.go` — new file, one test (RM35 D6 exception).
- `cmd/poller`, `cmd/web`, `cmd/setup` — indirectly affected (they call `config.Load()`), not
  edited (RM35 D4 — `cmd/*` is the composition root, out of scope for every adopting tier).
- No schema, no migration, no other module's Go source.
