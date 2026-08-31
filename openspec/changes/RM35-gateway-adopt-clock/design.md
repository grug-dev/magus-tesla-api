## Context

`internal/gateway` is the sixth tier of `RM35-timezone-centralization` and the last domain
module to adopt `internal/clock` before tier 7's `make tz-guard`. Unlike tiers 2–5, gateway's
call sites are NOT primarily about a nightly-batch "now"/"calendar day" — they are about the
`browser_tz` cookie mechanism (MAG-7) that lets a signed-in user's own browser timezone drive the
dashboard's date math, with a documented UTC fallback for every case the cookie is unavailable.

The roadmap's per-tier cell for `gateway` names three items, verified against the current source
(line numbers had already drifted slightly from the roadmap's original count, corrected
2026-08-30 per the roadmap's own note about `supercharger.go`'s `startOfMonth`):

- `internal/gateway/handlers/tz.go` — `browserLocation` (3 `return time.UTC` sites: nil
  `*gin.Context`/`*http.Request`, missing/empty cookie, unparseable IANA name) and
  `browserLocationFromHeader` (the same 3-way fallback, for the `*http.Request`-only test
  variant) — 6 fallback return sites total, all becoming `clock.Zone()`.
- `internal/gateway/handlers/history.go:61` — `startOfDay(t)`, the gateway's ONE definition (not
  two — `supercharger.go:57`'s `startOfMonth` is month granularity, a different function, per the
  roadmap's own corrected finding).
- `internal/gateway/handlers/handlers.go:391,461,729,924` (verified against the current file: the
  import-block addition in this tier shifts these to 392/462/730/925, but the four logical call
  sites are unchanged) — `time.Now()` → `clock.Now()` in `mapVehicles`, `dashboardFor`,
  `navHeaderFor`, and `TeslaCallback`.

**Explicitly NOT in scope, verified by reading the source directly:**

- `startOfDayIn` (`tz.go`) — a zone-*parameterized* helper (the caller supplies `loc`); it is not
  a duplicated UTC-only truncator like `startOfDay`, and the roadmap explicitly says "keep
  `startOfDayIn` and its documented DST limitation." Its own DST caveat (MAG-7 review finding
  R1-6) is untouched.
- `browserToday`'s own `time.Now()` call (`tz.go`) — the roadmap's per-tier cell for gateway lists
  only the 4 `handlers.go` sites for the raw-`time.Now()`-to-`clock.Now()` swap; `browserToday`'s
  `time.Now()` is not one of them. See D-gw-4 below for why leaving it alone is correct, not an
  oversight.
- `supercharger.go:57`'s `startOfMonth` and `supercharger.go`'s `startOfDay(time.Now().UTC())`
  call sites (design.md D9a of `RM30-gateway-read-supercharger-stats-from-charging`) — a
  previously-recorded, deliberate choice to use plain UTC for that endpoint, independent of the
  browser cookie. Untouched by this tier and by roadmap D1 (D1 only replaces a *missing/fallback*
  zone; D9a is not a fallback, it is the endpoint's own designed behavior).

Performance profile: **read-heavy** (`ai/architecture.md` §7) — not relevant to this tier's own
work. Every touched call site sits on a read-path HTTP handler (`DashboardHistoryFragment`,
`charges.go`'s handlers, `dashboardFor`, `navHeaderFor`, `mapVehicles`) or a one-time OAuth
callback (`TeslaCallback`) — none of them issues a new query, adds an allocation pattern, or
changes read latency; `clock.Zone()`/`clock.Now()`/`clock.CalendarDay` are pure, in-memory,
no-I/O functions (tier 1 design).

## Goals / Non-Goals

**Goals:**
- Route `browserLocation`/`browserLocationFromHeader`'s no-cookie/malformed-cookie fallback
  through `clock.Zone()`, with the `browser_tz` cookie's own win path byte-for-byte unchanged
  (roadmap D1: the cookie always wins when present).
- Delete `history.go`'s own UTC-truncation body from `startOfDay` in favor of
  `clock.CalendarDay(t, time.UTC)`, with every caller's output algebraically identical to before
  (mirrors `RM35-telemetry-adopt-clock`'s D-tel-1 "delete, don't wrap" precedent).
- Route the four named `handlers.go` `time.Now()` call sites through `clock.Now()`, verified
  byte-identical in every stored or externally observable value (D-gw-5 below).
- Repair every existing test broken by the fallback-default change (roadmap D6), without
  weakening any assertion or changing any expected value beyond what the new default forces.
- Correct every doc comment that states the old `time.UTC` fallback as current behavior
  (`CLAUDE.md`'s "docs track structural change" rule) and add `internal/clock` to
  `internal/gateway/AGENTS.md`'s import list.
- Update the "Dashboard History Charts" requirement's fallback-default wording in the delta spec
  so the merged main spec no longer states `time.UTC` as the no-cookie behavior.

**Non-Goals (explicitly out of scope for this tier):**
- Any change to the `browser_tz` cookie mechanism itself, its script (RD9), its cookie name, or
  its parse path — MAG-7's cookie-wins behavior is completely unchanged.
- `startOfDayIn`, `supercharger.go`'s `startOfMonth`, or `supercharger.go`'s explicit
  `startOfDay(time.Now().UTC())` call sites — all deliberately left alone (see Context above).
- `make tz-guard` — tier 7, shipped last (roadmap D5).
- Any migration, backfill, or database object — this tier owns no table and adds none.
- Touching `cmd/` — out of scope (RM35 D4's composition-root exemption); no `cmd/` file
  constructs a `gateway.Deps` with a zone-dependent field this tier's fallback change reaches.
- The root `README.md` dependency-graph edit — the leader's job (roadmap D25).
- New test coverage beyond what repairing broken tests requires (roadmap D6: new coverage is
  `internal/clock`'s alone, tier 1).

## Decisions

Roadmap decisions restated as they apply to this tier, followed by this tier's own findings.

### D1 — Scope: defaults only, zero migrations (restated, binding)

This tier's only user-observable change is a fallback *default* (UTC → `America/Bogota`) for
callers with no usable `browser_tz` cookie. No migration; no `pgtype.Date`/`DATE` column is
touched by this tier at all — the gateway owns no such column, and `startOfDay`'s output
(UTC-midnight-represented `time.Time` values used only in-memory for chart bucketing and
date-string formatting, never written to a `DATE` column) is unaffected in value, only in which
function computes it.

### D2 — `internal/clock` is the sole owner of the default zone and calendar-day computation (restated, binding)

`gateway` obtains the default zone via `clock.Zone()` and the calendar-day computation via
`clock.CalendarDay(t, loc)` — never re-implementing either. `internal/clock` imports stdlib
`time` only, and is LAYER 0 (no internal dependencies) in `ai/architecture.md`'s dependency
graph, so `gateway` importing it — already the topmost consumer of every other module — creates
no cycle.

### D4 — This tier's job within the sweep (restated, binding)

Per the roadmap's tier table, verbatim: swap the `browserLocation`/`browserLocationFromHeader`
fallback, collapse `history.go`'s `startOfDay` into `clock.CalendarDay`, and swap the four named
`handlers.go` `time.Now()` sites. "The `browser_tz` cookie still wins — only the fallback
changes" is the roadmap's own one-line summary of this tier, and every decision below traces back
to preserving that.

### D6 — Unit tests: repair only, no new coverage beyond what the fallback-default change forces (restated, binding)

This tier adds no new `_test.go` file and no new test function beyond what repairing the
fallback-default assertions requires. Every test touched below already existed.

### D-gw-1 — `startOfDay` is deleted-and-delegated, not kept as a wrapper (this tier's decision, mirrors D-tel-1)

**Decision:** `startOfDay`'s body becomes `return clock.CalendarDay(t, time.UTC)` — one line, no
intermediate wrapper.

**Why not keep a two-line body that calls `clock.CalendarDay` and does nothing else** (rejected):
already the shape `RM35-telemetry-adopt-clock`'s D-tel-1 settled — a wrapper that forwards
verbatim to another package's function is exactly the indirection `CLAUDE.md`'s AI-efficiency
"do not over-abstract" rule warns against. `startOfDay` itself is KEPT (not deleted outright,
unlike telemetry's `dateOnly`) because it has many call sites across `history.go`,
`supercharger.go`, `handlers_test.go`, `history_test.go`, and `supercharger_test.go` — repointing
every one of them at `clock.CalendarDay(t, time.UTC)` directly would be a much larger, purely
mechanical diff for zero behavioral benefit, since `startOfDay`'s name and one-argument shape are
still meaningful call-site vocabulary in this file. The distinction from telemetry's `dateOnly`:
`dateOnly` took a `loc` parameter that made it a strict duplicate of `CalendarDay`'s general
form, so keeping it added a redundant, more-general-than-needed wrapper; `startOfDay` takes NO
`loc` parameter and hardcodes `time.UTC` — a narrower, single-argument convenience whose callers
never wanted the flexibility `CalendarDay` offers, so it earns its keep as gateway's own
UTC-specific spelling.

### D-gw-2 — `startOfDayIn` is NOT touched, and is not a duplicate `startOfDay` (this tier's finding)

`startOfDayIn(t, loc)` returns a `time.Time` carrying `loc` itself as its `Location` — a
different, incompatible representation from `clock.CalendarDay`, which always returns its result
expressed at UTC midnight (the `pgtype.Date` storage encoding, tier 1 design). Every caller of
`startOfDayIn` (`browserToday`, and by extension every `.Format`/`.AddDate` that follows) relies
on the returned value's `Location` being the browser's own zone so that
`.Format("2006-01-02")` produces the correct browser-local calendar-date string. Reimplementing
`startOfDayIn` on top of `CalendarDay` would require re-converting the UTC-midnight result back
into `loc` — extra work for an identical outcome — so it stays as-is, matching the roadmap's
explicit instruction to keep it (including its documented DST limitation, MAG-7 review finding
R1-6, verbatim).

### D-gw-3 — `browserToday`'s own `time.Now()` call is left alone (this tier's finding, not an oversight)

**Finding:** the roadmap's per-tier cell for `gateway` lists exactly three items — the
`browserLocation`/`browserLocationFromHeader` fallback, `history.go`'s `startOfDay`, and four
named `handlers.go` `time.Now()` sites. `browserToday`'s `time.Now()` call
(`startOfDayIn(time.Now(), browserLocation(c))`) is not among them.

**Why leaving it alone is correct, not an omission:** `browserToday`'s behavior with no cookie
already changes as a direct, automatic consequence of `browserLocation`'s fallback swap — it
calls `browserLocation(c)` internally, so once that returns `clock.Zone()` instead of `time.UTC`,
`browserToday`'s no-cookie result shifts to Bogota-local midnight with ZERO further code change.
Additionally swapping `browserToday`'s `time.Now()` to `clock.Now()` would be a no-op in effect
(`clock.Now()` is the same instant as `time.Now()`, re-expressed in a different `Location` that
`startOfDayIn`'s own `t.In(loc)` immediately discards by re-converting into `loc` anyway) — there
is no behavior this second swap would add. This matches `RM35-telemetry-adopt-clock`'s
established practice of listing exactly the roadmap's named call sites and not silently expanding
scope to "every `time.Now()` in the file," per `openspec/config.yaml`'s architecture-scoping
intent and the owner's standing preference for minimal scope.

### D-gw-4 — The four `handlers.go` `time.Now()` swaps are verified byte-identical in every stored or observable value (this tier's finding, mirrors D-tel-2)

**Finding, not just a design choice:** `clock.Now()` is `time.Now().In(clock.Zone())` — the same
instant `time.Now()` would return, re-expressed in a different `*time.Location`. Each of the four
call sites consumes that value in a way that discards or is invariant to `Location`:

1. `mapVehicles`: `isStale(snap.CapturedAt, time.Now())` → `isStale` computes
   `now.Sub(capturedAt) > stalenessThreshold` — `Time.Sub` operates on absolute instants,
   independent of either operand's `Location`.
2. `dashboardFor`: `mapDashboardSnapshot(ctx, &vm, snap, time.Now())` → inside,
   `isStale(snap.CapturedAt, now)` (same `.Sub()` argument as above); the only other use of the
   snapshot's own timestamp in that function, `vm.LastUpdated = snap.CapturedAt.UTC().Format(...)`,
   does not use the `now` parameter at all.
3. `navHeaderFor`: `now := time.Now()` feeds `connectedAt(snap.CapturedAt, now)` (`.Sub()`,
   same argument) and `relativeLastSeen(snap.CapturedAt, now, ctx)` (`d := now.Sub(capturedAt)`,
   same argument).
4. `TeslaCallback`: `AccessExpiresAt: time.Now().Add(...)` is persisted via
   `account.Service.SaveTeslaTokens` into a `timestamptz` column through `pgtype.Timestamptz` —
   Postgres `timestamptz` stores an absolute instant (internally UTC) with no zone of its own;
   the Go value's `Location` field is discarded by the round-trip exactly as
   `RM35-telemetry-adopt-clock`'s D-tel-2 established for `telemetry.Snapshot.CapturedAt`.

**Consequence:** all four swaps are behavior-preserving not because the change is unlikely to
matter, but because the one field that changes (`Location`) is provably never read by any
consumer these four call sites have.

### D-gw-5 — The three broken tests are repaired to the new fallback; the fallback itself is NOT reverted

The owner's first suite run failed three history tests. All three anchored their expected
window on `startOfDay(time.Now())` — UTC midnight — while the handler anchors on
`browserToday`, whose no-cookie fallback this tier moved to `clock.Zone()` (−05).
`time.Time.Equal` compares instants, so the two midnights sit five hours apart.

The fallback stands. A signed-in user carrying a `browser_tz` cookie already received a −05
window **before** this tier; T1 changed only the no-cookie default, which is exactly what
roadmap D1 asked for. The tests asserted the old default, so the tests are what moved.

### D-gw-6 — A named `browserTodayNoCookie()` test helper, not three inlined expressions

21 other call sites in `history_test.go` correctly keep `startOfDay`: they pass their own
"today" straight into `buildHistoryView` / `dashboardFor` and never cross the
`browserLocation` fallback. Only the 3 sites that compare against a **handler-computed**
window need the browser anchor. A named helper carries a doc comment recording which anchor
belongs where, so the next edit cannot silently pick the wrong one.

### D-gw-7 — `buildHistoryPresets` compares calendar dates, not instants (pre-existing bug, fixed here)

`Active: start.Equal(pStart) && end.Equal(pEnd)` compared two different time frames:
`start`/`end` are UTC-midnight-of-D (`time.Parse` of a bare `"2006-01-02"` in
`parseHistoryRange`), while `pStart`/`pEnd` derive from `today` = `browserToday(c)`,
midnight-of-D in the **browser's** zone. `Equal` compares instants, so `Active` could only
ever be true when the browser's UTC offset was exactly zero.

**This is a pre-existing bug, not one this tier introduced** — the line is byte-identical on
`main`. Every signed-in user whose `browser_tz` names a non-UTC zone has had a dead preset
selector: no button ever highlighted. It merely *looked* correct because the no-cookie
fallback was `time.UTC`, the one case where the frames coincided. Removing that coincidence
is what exposed it.

Fixed by comparing the formatted `"2006-01-02"` date on both sides. Both are date-valued by
construction — the preset hrefs are already built from the same formatted strings — so this
is the comparison the code always meant. The same frame-mismatch warning already appears in
this file at the `end <= today` cap; `buildHistoryPresets` never got the same treatment.

**Scope note:** fixing this goes beyond MAG-20's literal text. The owner was shown the
alternatives (separate change / revert the tier) and chose to fix it here, because the test
cannot go green without either this fix or a weakened assertion.

**Follow-up not taken:** roadmap D6 limits tiers 2–6 to repairing tests, not adding them, so
no NEW regression test asserting "a cookie-carrying user sees an active preset" was written.
That gap is recorded for the backlog.

### D-gw-9 — The frame-mismatch class had SIX test sites, not three (corrected twice by review)

The tier's first pass found 3. Review round 1 found a 4th (R1-1). Review round 2 found the
5th and 6th (R2-1), after the leader had explicitly judged them safe.

The leader's wrong reasoning is recorded because it is the useful part: it read
`parseHistoryRange`'s cap as `end <= today` and concluded a UTC-derived `end` clears it.
The cap is `end <= yesterday` (`history.go` step 4), and `calendarDateAfter` compares each
`time.Time`'s date **in its own Location** — so a UTC-yesterday `end` is one calendar day
past browser-yesterday whenever UTC and Bogota disagree on the date.

The class is therefore: **any test that lets a value cross the `browserLocation` fallback —
whether by comparing against a handler-computed window (R1-1) or by sending a param the
handler validates against one (R2-1) — must anchor on `browserTodayNoCookie()`.** Sites that
pass their own `today` straight into `buildHistoryView` / `dashboardFor` never reach the
fallback and correctly keep `startOfDay`; `TestBuildHistoryView_PresetsCarryAbsoluteHrefs`
shares the identical line and is deliberately untouched for exactly that reason.

Every one of these fails ONLY between 00:00 and 05:00 UTC. No suite run outside that window
can find them, which is why three green runs did not.

## Test Contract

Per `ai/go-conventions.md` §Testing, expected values are authored here before the implementation
change (in this repair-only tier, every expected concept below already existed — repair only
*relocates* the assertion's target zone/name, it does not invent a new assertion).

**`browserLocation`/`browserLocationFromHeader` fallback (`TestBrowserLocation_Fallbacks`,
`history_test.go`):**
1. No cookie → `clock.Zone()` (was `time.UTC`).
2. Valid IANA cookie (`"America/Bogota"`) → that location, unaffected (cookie always wins).
3. Malformed cookie (`"Not/A/Zone"`) → `clock.Zone()` (was `time.UTC`).
4. Empty cookie value → `clock.Zone()` (was `time.UTC`).

**`parseHistoryRange`'s no-cookie default window
(`TestParseHistoryRange_NoCookieFallsBackToPlatformDefault`, renamed from
`TestParseHistoryRange_NoCookieFallsBackToUTC`):**
- `end` equals `startOfDayIn(time.Now(), clock.Zone()).AddDate(0, 0, -1)` (was
  `startOfDay(time.Now()).AddDate(0, 0, -1)` with `time.UTC` truncation).
- `end.Location()` equals `clock.Zone()` (was `time.UTC`).

**`parseHistoryRange`'s both-absent default window
(`TestParseHistoryRange_BothAbsent_DefaultSixDayWindow`):**
- `wantEnd` equals `startOfDayIn(time.Now(), clock.Zone()).AddDate(0, 0, -1)` (was
  `startOfDay(time.Now()).AddDate(0, 0, -1)`); `wantStart` is unchanged arithmetic
  (`wantEnd.AddDate(0, 0, -historyRangeWindowDays)`).

**`charges.go`'s no-cookie default window (`todayDefaultZoneMidnight`, renamed from
`todayUTCMidnight`, `charges_test.go`):**
- Returns `startOfDayIn(time.Now(), clock.Zone())` (was UTC-midnight truncation of
  `time.Now().UTC()`). Both call sites (`TestChargeCreate_D2_...`,
  `TestChargeRowUpdate_D3_...`) consume it only via `.Format("2006-01-02")` /
  `defaultChargesWindow`, so no further repair is needed beyond the helper's own body.

**`TestChargePage_DateDefaultsToToday` (`charges_test.go`):**
- `todayDefault` equals `time.Now().In(clock.Zone()).Format("2006-01-02") + "T00:00"` (was
  `time.Now().UTC().Format("2006-01-02") + "T00:00"`).

**Every test that sets an explicit `browser_tz` cookie is unaffected** — verified by grep: no
other test in `history_test.go`, `charges_test.go`, or `supercharger_test.go` asserts a no-cookie
fallback value beyond the five listed above; `supercharger_test.go`'s
`startOfDay(time.Now().UTC())` call sites pass an already-UTC-converted argument (design.md D9a
of `RM30-gateway-read-supercharger-stats-from-charging`), so `startOfDay`'s delegate-to-
`clock.CalendarDay(t, time.UTC)` body produces an identical result regardless of this tier.

## Risks / Trade-offs

- **[Non-risk, verified]** `handlers.go`'s four `time.Now()` swaps change no stored or observable
  value (D-gw-4) — not a risk, a proven no-op for every current caller.
- **[Risk, accepted, narrow]** The `browserLocation`/`browserLocationFromHeader`/`startOfDay`
  fallback-default swap IS a real, intended behavior change for any caller with no usable
  `browser_tz` cookie: a direct API caller, a `<noscript>` browser, or a request racing the
  cookie script's first execution. This is exactly roadmap D1's platform-wide intent, and every
  signed-in user who has loaded any authenticated page at least once already carries the cookie
  and is unaffected.
- **[Non-risk, verified]** `startOfDay` → `clock.CalendarDay(t, time.UTC)` is algebraically
  identical (D-gw-1) — zero behavior change for any caller passing an already-known `time.Time`.
- **[Trade-off]** `internal/gateway` gains a new inter-package dependency, `internal/clock` —
  accepted: `clock` imports nothing project-local (D2), so this cannot create a cycle, and
  `gateway` is already the platform's topmost consumer of every domain module.

## Migration Plan

None — this tier owns no database object, adds no table, column, index, or constraint, and runs
no migration. Rollback is a plain revert of the touched Go/doc/spec files; tier 7's `make
tz-guard` depends on this tier having landed (so every fallback in the repo points at `clock`
before the guard is wired in), not on any specific internal shape of this tier's implementation.

## Open Questions

None. The one question this design had to resolve — whether `browserToday`'s own `time.Now()`
call needed a matching swap — is answered by D-gw-3: no, because `browserLocation`'s fallback
swap already produces the intended behavior change with zero further code, and swapping
`time.Now()` there too would be a provable no-op.
