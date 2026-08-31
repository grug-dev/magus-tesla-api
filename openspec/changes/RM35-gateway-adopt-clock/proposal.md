Source: MAG-20 — https://linear.app/magus-monitor/issue/MAG-20/datedate-time-time-zone
Roadmap: openspec/roadmaps/RM35-timezone-centralization.md
Tier: 6 of 7 (`gateway`; depends on tier 1, `RM35-clock-add-bogota-time-package`, already
archived and on disk. Tiers 2–5 — `config`, `telemetry`, `analytics`, `app` — are independent
sibling adopters and are not a dependency of this tier.)

## Why

Tier 1 built `internal/clock` (the platform's single owner of the default zone,
`America/Bogota`). `internal/gateway` owns three of the roadmap's named call sites: the two
UTC-fallback functions behind the `browser_tz` cookie (`browserLocation`,
`browserLocationFromHeader`), one of the three copy-pasted UTC-midnight calendar-day truncators
(`history.go`'s `startOfDay`), and four raw `time.Now()` call sites in `handlers.go`. This tier
routes all of them through `clock`, without touching the cookie itself — MAG-7's `browser_tz`
mechanism still wins for a signed-in user; only the *fallback* when no cookie is present (or the
cookie is malformed/empty) changes, from `time.UTC` to `clock.Zone()`.

## What Changes

- `internal/gateway/handlers/tz.go` — the UTC fallback in `browserLocation` (3 return sites) and
  `browserLocationFromHeader` (3 return sites) becomes `clock.Zone()` (roadmap D4). The
  `browser_tz` cookie parse/win path is byte-for-byte unchanged — this is a pure fallback-default
  swap, not a change to which zone a signed-in user's own cookie resolves to.
  - `startOfDayIn` and its documented DST limitation (MAG-7 review finding R1-6) are UNCHANGED —
    it is a zone-*parameterized* helper (the caller supplies `loc`), not a duplicated
    UTC-only truncator, and the roadmap explicitly names it as "keep as-is."
  - `browserToday`'s own `time.Now()` call is UNCHANGED — the roadmap's per-tier cell for
    `gateway` does not list it, and it needs no change: the "now" instant is unaffected by which
    zone `browserLocation` falls back to (see design.md D-gw-4 for the full argument).
- `internal/gateway/handlers/history.go` — `startOfDay` (line 61, the gateway's ONE definition)
  now delegates to `clock.CalendarDay(t, time.UTC)` instead of hand-truncating. Pure
  delete-and-delegate: the formula (`t.In(time.UTC).Date()`, re-expressed at UTC midnight) is
  byte-identical to what `startOfDay` computed before, so every caller's output is unchanged.
  `supercharger.go:57`'s `startOfMonth` is a DIFFERENT function (month granularity) and is
  deliberately left untouched, per the roadmap's own corrected finding (2026-08-30).
- `internal/gateway/handlers/handlers.go` — four `time.Now()` call sites become `clock.Now()`
  (roadmap D4): `mapVehicles`'s `isStale(snap.CapturedAt, time.Now())`, `dashboardFor`'s
  `mapDashboardSnapshot(ctx, &vm, snap, time.Now())`, `navHeaderFor`'s `now := time.Now()`, and
  `TeslaCallback`'s `AccessExpiresAt: time.Now().Add(...)`. Every one of these four values is
  either consumed only through `.Sub()` (an absolute-instant, zone-independent operation) or
  persisted through `pgtype.Timestamptz` (which discards the Go value's `Location` field on
  write) — see design.md D-gw-5 for the verified byte-identical argument, mirroring
  `RM35-telemetry-adopt-clock`'s D-tel-2.
- `internal/gateway/AGENTS.md` — adds an "Allowed / forbidden imports" section naming
  `internal/clock`; corrects the RD9 (`browser_tz` cookie script) doc-comment's stale claim that
  the JS-failure fallback is `time.UTC` — it is now `clock.Zone()` (`America/Bogota`).
- `internal/gateway/handlers/history_test.go`, `internal/gateway/handlers/charges_test.go` —
  repaired per roadmap D6: every test that asserted the browser-cookie-absent fallback is
  `time.UTC` is repointed at `clock.Zone()` (renamed where the old name embedded "UTC"). No new
  test is added.
- `openspec/specs/gateway/spec.md`'s "Dashboard History Charts" requirement — the two
  fallback-default sentences and the "no cookie" scenario's example values are updated from
  `time.UTC` to `clock.Zone()` (`America/Bogota`); every other sentence in the requirement is
  unchanged (RM35 D1 touches only the default).
- Root `README.md` — the dependency-graph edit (gateway's row gains `clock`; `internal/clock`'s
  prose row's adoption list gains `gateway`) is the **leader's** job (roadmap D25), not this
  worker's — out of this tier's sandbox.

## Is this the ticket's own "no UTC" claim, taken literally? No — scoped by roadmap D1.

The MAG-20 acceptance criterion "the app does not use UTC" is scoped by roadmap D1: it means the
*default* zone is never UTC, not that the string `time.UTC` disappears from the codebase. This
tier still uses `time.UTC` explicitly in two places that are NOT defaults: `startOfDay`'s
delegation to `clock.CalendarDay(t, time.UTC)` (the `pgtype.Date` storage encoding, unchanged
per D1) and `supercharger.go`'s `startOfDay(time.Now().UTC())` call sites (design.md D9a of
`RM30-gateway-read-supercharger-stats-from-charging` — a deliberate, previously-recorded choice
to use plain UTC rather than the browser's zone for that endpoint, out of this tier's scope and
left untouched).

**Breaking?** No observable production behavior changes for any signed-in user who has ever
loaded an authenticated page — the `browser_tz` cookie is set on every `layouts.BaseAuth` render
and wins unconditionally. The change is observable only for: (a) a direct API caller with no
`browser_tz` cookie (previously got UTC-default date math, now gets Bogota-default), and (b) a
`<noscript>` browser or a request that races the cookie-setting script's first execution (same
shift). Both are the exact class of default-only behavior change roadmap D1 already authorized
platform-wide, and are the point of this whole roadmap.

**Read paths affected:** `DashboardHistoryFragment` (`GET /ui/dashboard/history`) and every
`charges.go` handler that calls `browserToday(c)` with no cookie present — all of them already
read-path endpoints per `ai/architecture.md` §7's Reader/Collector split; this tier changes which
zone their date-window math defaults to, not which port they call or how many reads they issue.

**No database object.** No migration, no schema change, no new column, no new index — this tier
changes only which function computes a date-boundary value already being computed.

## Capabilities

### Modified Capabilities

- `gateway` — the "Dashboard History Charts" requirement's default-timezone-fallback behavior
  (previously specified as `time.UTC`) changes to the platform default (`clock.Zone()`,
  `America/Bogota`) whenever no `browser_tz` cookie is present or usable. The `browser_tz` cookie
  path itself is unchanged.

## Impact

- `internal/gateway/handlers/tz.go` — 6 `return time.UTC` → `return clock.Zone()`; doc comments
  corrected; new `internal/clock` import.
- `internal/gateway/handlers/history.go` — `startOfDay` body delegates to
  `clock.CalendarDay(t, time.UTC)`; doc comment corrected; new `internal/clock` import.
- `internal/gateway/handlers/handlers.go` — 4 `time.Now()` → `clock.Now()`; new `internal/clock`
  import.
- `internal/gateway/AGENTS.md` — new "Allowed / forbidden imports" section; RD9 doc-comment
  fallback claim corrected.
- `internal/gateway/handlers/history_test.go` — `TestBrowserLocation_Fallbacks`,
  `TestParseHistoryRange_NoCookieFallsBackToUTC` (renamed
  `TestParseHistoryRange_NoCookieFallsBackToPlatformDefault`), and
  `TestParseHistoryRange_BothAbsent_DefaultSixDayWindow` repaired; new `internal/clock` import.
- `internal/gateway/handlers/charges_test.go` — `todayUTCMidnight` renamed
  `todayDefaultZoneMidnight` and repointed at `clock.Zone()` (2 call sites repaired);
  `TestChargePage_DateDefaultsToToday` repaired; new `internal/clock` import.
- `openspec/specs/gateway/spec.md` — "Dashboard History Charts" requirement's fallback-default
  wording and one scenario's example values updated (synced from this change's delta spec on
  archive).
- No `cmd/` edit — out of scope (RM35 D4's composition-root exemption). No `README.md` edit —
  reserved for the leader (roadmap D25).
