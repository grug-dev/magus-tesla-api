Source: MAG-20 — https://linear.app/magus-monitor/issue/MAG-20/datedate-time-time-zone
Roadmap: openspec/roadmaps/RM35-timezone-centralization.md
Tier: 7 of 7 (`platform`, cross-cutting — the FINAL tier; depends on tiers 2–6, all archived:
`config`, `telemetry`, `analytics`, `app`, `gateway` have all adopted `internal/clock`)

## Why

Roadmap decision **D5** reserves the enforcement mechanism for last: `make tz-guard`, a
grep-based guard mirroring `money-guard`'s shape, wired into `make check`. Wiring it earlier
would have failed on every module not yet migrated (D5's own rationale) — now that tiers 2–6
are archived, every deliberate fallback in the repo already routes through `internal/clock`,
so the guard can be turned on without a single pre-existing violation it does not explicitly
allow.

Per `CLAUDE.md`'s AI-efficiency rule, this guard is the point of the whole roadmap: without it,
the convention decays the moment an agent types `time.Now()` again, and nothing catches it
until a human notices days or weeks later. A deterministic, cheap, fail-fast signal beats a
human code-review round-trip.

This proposal and its sibling artifacts (design.md, specs/platform/spec.md, tasks.md) are
produced in the **same dispatch** as the implementation, per this roadmap's tier-7 convention
(mirroring tiers 2–6).

## What Changes

- **New `make tz-guard` target** in the `Makefile`, mirroring `money-guard`'s exact shape
  (grep-based, prints a helpful error, `exit 1` on a hit, a success message otherwise), with a
  `// tz:allow: <reason>` escape hatch. Three grep legs, all scoped to `internal/` (never
  `cmd/`, which is exempt by roadmap D4 simply by never being scanned), excluding
  `internal/clock/` itself, every `_test.go` file, and comment-only lines:
  1. Raw `time.Now()` outside `internal/clock`.
  2. A hand-rolled "midnight of some day" `time.Date(...)` construction whose last four
     numeric arguments are all zero, whatever the trailing zone argument (catches both a
     UTC-hardcoded truncator and a zone-parameterized one).
  3. A hardcoded IANA time-zone string literal (`"Region/City"` shape) outside `internal/clock`.
- **Wired into `.PHONY` and `make check`**: `check: build vet ui-guard i18n-guard money-guard
  tz-guard test` (kept last before `test`, per D5).
- **Six `// tz:allow: <reason>` markers** added to the six call sites the guard would otherwise
  flag on the current tree — see design.md D-plat-6 for the full list and why it is six, not
  the roadmap's stated four (a discrepancy found and corrected here, the same way the roadmap's
  own "Findings" section already corrected an earlier miscount of `supercharger.go`'s
  `startOfMonth`).
- **Docs**: `README.md`'s guard-chain line (`make check` composition) and `CLAUDE.md`'s
  allowed-commands list gain `make tz-guard`; `ai/go-conventions.md`'s time-zone rule gets a
  pointer to the guard that now enforces it repo-wide.

**Not breaking.** No Go logic changes — only comment-only `// tz:allow:` markers on six
existing lines (three of which already had a trailing comment; the marker is appended to it or
added fresh), a new Makefile target, and documentation. No test's behavior or assertion
changes.

**Affected modules:** none in the runtime sense. This is `platform` — a cross-cutting change
touching the `Makefile`, `README.md`, `CLAUDE.md`, `ai/go-conventions.md`, and comment-only
edits inside `internal/gateway/handlers/tz.go` and `internal/gateway/handlers/supercharger.go`
(no logic change, per this tier's explicit sandbox grant).

## Capabilities

### New Capabilities

- `platform` (tz-guard) — a repo-wide, deterministic guard that keeps `internal/clock` the
  sole owner of the platform's default time zone, "now", and calendar-day normalization,
  exactly as `ai/go-conventions.md` and `RM35-timezone-centralization` D2 require.

### Modified Capabilities

(none — no existing capability's behavior changes; this tier adds enforcement only)

## Impact

- `Makefile` — one new target, one `.PHONY` entry, one `check` dependency added.
- `README.md`, `CLAUDE.md`, `ai/go-conventions.md` — doc updates per `CLAUDE.md`'s
  workflow-decisions rule (a new `make` target must be documented in the same change).
- `internal/gateway/handlers/tz.go`, `internal/gateway/handlers/supercharger.go` — six
  comment-only `// tz:allow: <reason>` markers, zero logic changes.

**No database object is created or changed by this tier.** It owns no table, runs no query,
and touches no migration. **No hot read path is affected**: the guard is a `make`-time static
grep over source text, never executed at runtime, so `ai/architecture.md` §7's read-heavy
performance profile is not engaged by this change.
