## Context

`platform` is the seventh and final tier of `RM35-timezone-centralization`. Tiers 1–6
(`clock`, `config`, `telemetry`, `analytics`, `app`, `gateway`) are all archived: every
deliberate zone/now/day-truncation fallback in the repo already routes through
`internal/clock`. This tier's only job is the enforcement mechanism the roadmap deferred to
last (D5): `make tz-guard`.

This tier owns **no `internal/` module** — it is cross-cutting, per `openspec/config.yaml`'s
convention (`platform` is the canonical domain name for changes not owned by one module). Its
sandbox is explicit: `Makefile`, `README.md`, `CLAUDE.md`, `ai/go-conventions.md`, this
change's own OpenSpec folder, and comment-only edits (no logic change) to the two gateway files
carrying the guard's legitimate escapes.

Performance profile: **not engaged**. `make tz-guard` is a static grep over source text, run at
`make check` time, never at runtime. It touches no read path, hot or otherwise
(`ai/architecture.md` §7).

**No database object** is created, changed, or even referenced by this tier. It owns no table,
runs no query, and adds no migration.

### Measurements — verified, not trusted blind

The dispatch prompt's own pre-flight measurements were re-run against the current tree before
any pattern was finalized, per its own instruction ("verify them, do not trust them blind").
Two corrections came out of that verification (see D-plat-6):

- Raw `time.Now()` outside `internal/clock`, excluding `_test.go` and comment-only lines:
  **exactly 4**, confirmed — `internal/gateway/handlers/tz.go:108` (`browserToday`) and
  `internal/gateway/handlers/supercharger.go:134,169,201`.
- The dispatch's claimed "ZERO" `time.Date(..., 0, 0, 0, 0, time.UTC)` occurrences outside
  `internal/clock` is **off by one**: `internal/gateway/handlers/supercharger.go:59`
  (`startOfMonth`) already has exactly that literal shape (`t.Year(), t.Month(), 1, 0, 0, 0, 0,
  time.UTC`) — the dispatch's own "Known legitimate escapes" list even names this line
  separately, which is the tell that it was never really zero.
- A second, non-UTC-literal sibling exists at `internal/gateway/handlers/tz.go:94`
  (`startOfDayIn`) — `time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)` — which the
  dispatch's own escape list also names (`tz.go:79 startOfDayIn`). It does not match a
  UTC-only literal pattern, only a general "four trailing zero args" one (see D-plat-2).
- Hardcoded IANA zone strings outside `internal/clock`, `cmd/`, `_test.go`, and comments:
  **zero**, confirmed.
- Raw `time.Now()` inside `_test.go` files: **109 occurrences across 16 files**, all fixture
  timestamps or elapsed-time measurements. Literal `time.Date(Y, M, D, 0, 0, 0, 0, time.UTC)`
  inside `_test.go` files: **well over 150 occurrences** across `telemetry`, `charging`,
  `analytics`, and `gateway/handlers` test suites — every one a literal test-fixture date, not
  a hand-rolled truncation of "now". These counts are why D-plat-3 excludes test files
  wholesale rather than requiring per-line markers.

## Goals / Non-Goals

**Goals:**
- Add `make tz-guard`, wired into `.PHONY` and `make check` (last, before `test`, per D5).
- Guard exactly the invariant `ai/go-conventions.md`'s time-zone rule and `RM35` D2 state:
  `internal/clock` is the sole owner of the default zone, "now", and calendar-day
  normalization; nothing outside it hand-rolls any of the three.
- Mark every one of the six current call sites the guard would otherwise flag with a
  `// tz:allow: <reason>` comment, so `make tz-guard` passes cleanly on the current tree.
- Document the new target where `CLAUDE.md`'s workflow-decisions rule requires it: `README.md`,
  `CLAUDE.md`, and (since it directly enforces an existing rule) `ai/go-conventions.md`.

**Non-Goals:**
- Any Go logic change. Every edit inside `internal/gateway/handlers/` is a same-line trailing
  comment; nothing behavioral moves.
- Any database object, migration, or schema change — this tier owns none and needs none.
- Re-litigating tiers 1–6's own decisions (D1–D6, D-gw-1..10, etc.) — restated here only where
  this tier's guard must not contradict them (D1: never flag the `pgtype.Date` encoding).
- A fourth grep leg for "any bare `time.Local`/host-zone reference" — not asked for by the
  roadmap and not measured; adding an unmeasured pattern risks an unverified false-positive
  surface this tier has no budget to audit.

## Decisions

### D-plat-1 — Three grep legs, not one

The guard runs three independent patterns, ORed together (a failure in any one fails the whole
target), mirroring `ui-guard`'s single-`if` shape rather than three separate `make` targets —
one guard, one message, one place to look, consistent with `money-guard`/`i18n-guard`'s
existing "one target per convention" precedent (this convention is one, even though it spans
three syntactic shapes):

1. **Raw `time.Now()`.** The mandatory minimum per the dispatch — the literal call the
   `ai/go-conventions.md` rule and every adopting tier (2–6) name explicitly.
2. **Hand-rolled "midnight of some day" `time.Date(...)` construction** — any `time.Date` call
   whose last four numeric arguments before the trailing (zone or `time.UTC`) argument are all
   zero. Included, not skipped, because:
   - it is the exact shape `internal/clock.CalendarDay` replaced across tiers 2–6 (design.md D7
     of tier 1), so a *new* occurrence is precisely the regression this roadmap exists to catch;
   - the roadmap's own "Known legitimate escapes" list names TWO sites for this pattern —
     `supercharger.go:57 startOfMonth` and `tz.go:79 startOfDayIn` — meaning the roadmap itself
     expects this leg to exist and to require exactly those two markers (see D-plat-6);
   - deliberately **not** restricted to a literal trailing `time.UTC` — `startOfDayIn`'s
     zone-parameterized form (`..., loc)`) is the same hand-rolled-truncation shape with a
     variable last argument, and a pattern that only matched a hardcoded `time.UTC` would miss
     it, silently narrowing the guard below what the roadmap's own escape list expects.
3. **Hardcoded IANA zone string literal** (`"Region/City"` shape). Included because it directly
   guards `RM35` D2 ("internal/clock is the sole owner of ... naming a zone") and, per the
   Measurements above, currently catches **zero** real sites outside comments and `cmd/` — a
   guard that is free today and closes off a whole regression class (a future
   `time.LoadLocation("America/...")` dropped into a domain module) is worth adding even though
   nothing currently trips it. Rejected: leaving this leg out "since nothing currently needs
   it" — that reasoning would have also rejected `money-guard`'s and `i18n-guard`'s existing
   guards on their first day, before they had ever caught anything.

**Rejected — four legs, adding a `time.Local`/bare-zone-name check:** not measured, not asked
for, and no current call site motivates it. `ai/go-conventions.md`'s Coding Rules and the
`Non-Goals` above name this open, not silently taken.

### D-plat-2 — Scope: `internal/` recursively, `cmd/` never scanned, `internal/clock/` and `_test.go` excluded by path

All three legs scan `internal --include='*.go'` (recursive), then pipe through `grep -v` filters
excluding:
- `^internal/clock/` — the implementation itself; flagging `clock.go`/`calendar.go` for using
  the primitives they exist to provide would be absurd.
- `_test\.go:` — see D-plat-3.
- lines already carrying the literal substring `tz:allow` (the escape hatch itself, checked
  anywhere on the line, matching `money-guard`'s `grep -v 'money:allow'` convention).
- comment-only lines — see D-plat-4.

`cmd/*` is **never included in the scan path at all** — not excluded by a `grep -v`, excluded
by construction, because the grep target is `internal`, not `internal cmd`. This is the correct
realization of roadmap D4 ("cmd/* is out of scope: it is the composition root and loads the
zone on purpose"): a path never scanned cannot produce a false positive that then needs a
marker, unlike an excluded-by-grep site which still requires someone to remember why. Verified:
`cmd/explore-tesla-api/main.go:68`'s `flag.String("tz", "America/Bogota", ...)` needs **zero**
`tz:allow` marker under this design, exactly as D4 intends.

**Why scan all of `internal/`, not just `internal/gateway/` like the other three guards:** this
convention is genuinely repo-wide — tiers 2–6 touched `config`, `telemetry`, `analytics`,
`app`, and `gateway`, five different modules, not one. `money-guard`/`i18n-guard`/`ui-guard`
are gateway-scoped because their conventions (currency formatting, i18n, DaisyUI classes) are
gateway-only concerns (only the gateway renders HTML or money labels). This one is not, so its
guard is not.

### D-plat-3 — Test files excluded wholesale, mirroring `money-guard`'s own precedent

**Decision:** every `_test.go` file is excluded from all three grep legs, unconditionally, no
per-line markers.

**Why, given the roadmap's own warning** ("Tier 6 found SIX test sites where a wrong time
anchor produced bugs invisible to a green suite run... If you exclude `_test.go` wholesale, the
guard cannot catch that class ever again"):

The tier-6 bug class (design.md D-gw-9/D-gw-10 of `RM35-gateway-adopt-clock`) was **never** a
raw-call-shape defect a grep can see. Every one of the six broken test sites called
`startOfDay(time.Now())` or compared against a *handler-computed* window — syntactically
identical, character-for-character, to dozens of other call sites in the same files that were
and remain correct. The defect was semantic: *which* zone the test's expectation was anchored
in, versus which zone the handler under test actually used after `browserLocation`'s fallback
changed. No regex distinguishes a `time.Now()` used to build a correct anchor from one used to
build a stale one — the call looks the same either way. A guard that could catch that class
would have to understand each test's own assertion, which is a code-review judgment, not a
grep.

Given that, the choice is between two costs, and this design picks the smaller one:
- **Guard `_test.go`:** 109 real `time.Now()` call sites plus 150+ literal
  `time.Date(Y,M,D,0,0,0,0,time.UTC)` fixture-date constructions (Measurements, above) would
  each need a `// tz:allow:` marker — a wall of markers with zero discriminating power, since
  every one of them is legitimate (a DB fixture timestamp, an elapsed-time measurement, a
  literal test date), and the one bug class this tier is warned about would slip through every
  single marker anyway, because the marker only certifies "this call shape is expected here",
  which was never in question.
- **Exclude `_test.go`:** zero markers, zero false positives, and the guard stays exactly as
  useful for the one thing it *can* detect (a raw call shape reappearing in **production** code)
  as it would be with test files included. The tier-6 bug class remains the responsibility of
  disciplined test authorship (design.md D-gw-6, D-gw-9, D-gw-10 of the gateway tier: a named
  `browserTodayNoCookie()` helper, and a documented two-anchor rule) — not of `tz-guard`, which
  was never capable of catching it.

This mirrors `money-guard`'s own `grep -v '_test\.go:'` line precisely (design.md D4 of
`gateway-format-currency-values`) — the same reasoning applies there: a hardcoded
`fmt.Sprintf("%.2f USD", ...)` inside a test asserting an expected string is not the bug the
guard exists to catch, and excluding it is not new to this tier.

**This is a documented trade-off, not an oversight.** The gap it leaves — a future test written
against the wrong zone anchor — is exactly as open after this tier as it was after tier 6; no
new risk is introduced, and no risk this tier could plausibly close is left closed.

### D-plat-4 — Comment-only lines excluded via a same-line content filter

**Finding:** `time.Now()` and IANA zone name literals both appear, verbatim, inside doc
comments that *explain* the very convention this guard enforces — e.g.
`internal/app/processor.go:188-199` (four separate comment lines narrating "time.Now().UTC()"
and "raw time.Now() outside internal/clock" in prose), `internal/gateway/handlers/tz.go:28-29`
("America/Bogota", "Europe/London" as example cookie values in a doc comment), and
`internal/gateway/handlers/supercharger.go:73` (a comment describing `startOfDay(time.Now().UTC())`
as prose). A pattern that fires on prose describing the convention is self-defeating: it would
require `// tz:allow:` markers on comment lines that contain no code at all, and money-guard's
own precedent has no equivalent problem because `fmt.Sprintf("%.Nf %s"` essentially never
appears as English prose.

**Decision:** every grep leg's output is piped through
`grep -vE '^[^:]+:[^:]+:[[:space:]]*//'` — a filter that drops any matched line whose content
(the part after `grep -rn`'s `file:line:` prefix) begins, after optional leading whitespace,
with `//`. Verified against the current tree: this removes exactly the 8 real comment-only
false positives across `processor.go`, `tz.go`, `supercharger.go`, `charges.go`, and
`history.go`, and removes zero real violations (none of the six genuine call sites is itself a
comment).

**Implementation note (portability):** the filter must use `[^:]+` (one-or-more), not
`[^:]*` (zero-or-more) — verified empirically that BSD `grep` (macOS, the primary dev host)
fails to match `^[^:]*:[^:]*:[[:space:]]*//` against a real `grep -rn` line even though the
line unambiguously satisfies the pattern, while the `+` form matches correctly and consistently
on both BSD and GNU grep. `tasks.md` T1 records this as an explicit acceptance check.

### D-plat-5 — Escape hatch: same-line trailing `// tz:allow: <reason>`

Every file this guard scans is `.go`, which has a real comment syntax — unlike `i18n-guard`'s
`.templ` pass, which had to place its marker on the *preceding* line because Templ markup has
no in-markup comment syntax. `tz-guard` therefore mirrors `money-guard`'s simpler convention: a
trailing `// tz:allow: <reason>` comment on the same line as the flagged code, checked via
`grep -v 'tz:allow'` before the comment-only filter runs (so a code line carrying the marker is
dropped before it ever reaches the comment-only check).

### D-plat-6 — Six call sites need a marker, not the dispatch's stated four — a corrected count

Running the three finalized patterns against the current tree (post tier 1–6) surfaces exactly
six non-test, non-comment matches:

| # | Site | Leg | Reason it is legitimate |
|---|---|---|---|
| 1 | `tz.go:108` (`browserToday`) | raw `time.Now()` | Design D-gw-3 of `RM35-gateway-adopt-clock`: a provable no-op swap already covered by `browserLocation`'s `clock.Zone()` fallback; deliberately left un-migrated. |
| 2 | `supercharger.go:134` | raw `time.Now()` | Design D9a of `RM30-gateway-read-supercharger-stats-from-charging`: deliberately plain UTC, not the browser cookie's zone. |
| 3 | `supercharger.go:169` | raw `time.Now()` | Same as #2. |
| 4 | `supercharger.go:201` | raw `time.Now()` | Same as #2 (previously lacked even the explanatory comment #2/#3 carry — added here). |
| 5 | `tz.go:94` (`startOfDayIn`) | midnight-construction | Design D-gw-2 of `RM35-gateway-adopt-clock`: zone-*parameterized* truncation returning a `loc`-anchored time, a different representation than `clock.CalendarDay`'s fixed UTC-midnight — not a duplicate. |
| 6 | `supercharger.go:59` (`startOfMonth`) | midnight-construction | Month-level truncation, not a day-level duplicate of `startOfDay` — the exact distinction the roadmap's own 2026-08-30 correction note already draws. |

The dispatch's "four legitimate escapes" undercounts by two: it correctly named all six
individual sites across its "Known legitimate escapes" list and its "Real `time.Now()` calls...
exactly 4" measurement, but described them as one combined count of four. This design's finalized
grep patterns are the ones that determine the true marker count — six — and all six are added in
`tasks.md` T2. No marker is added anywhere the guard's own patterns do not actually fire; the
count is derived from running the patterns, not assigned in advance.

### D-plat-7 — `.PHONY` and `check` wiring, last before `test`

`check: build vet ui-guard i18n-guard money-guard tz-guard test` — `tz-guard` inserted
immediately before `test` (kept last per D5's explicit instruction and the existing pattern of
listing standalone guards before the trailing test phase). `.PHONY` gains `tz-guard` in the
existing guard cluster (`ui-guard i18n-guard money-guard`).

### D-plat-8 — No database object (restated, explicit per `openspec/config.yaml`)

This tier creates no table, column, index, constraint, view, or migration, and modifies none.
`openspec/config.yaml`'s design rule requiring a schema + rationale + index plan for any
DB-touching change does not apply — there is no DB-touching change here. Stated explicitly
rather than silently omitted, per this tier's dispatch instruction.

## Test Contract

Authored before the final regex was written, per `ai/go-conventions.md` §Testing's
"author expected values up front" rule — restated here for a `make`-guard rather than a Go test,
since that is what this tier ships instead of `_test.go` coverage (there is no Go code to unit
test; the guard's own correctness is proven by direction (a) and (b) below).

**(a) The guard MUST pass — zero output, exit 0 — on the current tree** once the six
`// tz:allow:` markers from D-plat-6 are in place. Verified per grep leg:
- Leg 1 (raw `time.Now()`): 0 matches after markers (was 4 before).
- Leg 2 (midnight construction): 0 matches after markers (was 2 before).
- Leg 3 (hardcoded IANA zone): 0 matches (was already 0).

**(b) The guard MUST fail — non-zero exit, printing the offending line — on each of these
inputs**, verified by temporarily introducing each into a scratch file inside `internal/` and
running `make tz-guard` against it, then removing it:
1. A bare `time.Now()` call in a non-test `.go` file under `internal/`, with no `tz:allow`
   marker and not inside a comment.
2. A `time.Date(y, m, d, 0, 0, 0, 0, time.UTC)`-shaped call (any trailing final argument),
   with no `tz:allow` marker and not inside a comment.
3. A `"America/Bogota"`-shaped (or any `"Region/City"`-shaped) string literal in code (not a
   comment), with no `tz:allow` marker.

**Legitimate inputs the guard must NOT flag** (already covered by the exclusions above, restated
as an explicit negative contract):
- Any of the above three shapes appearing **inside** `internal/clock/` (the implementation).
- Any of the above three shapes appearing **inside** a `_test.go` file, anywhere.
- Any of the above three shapes appearing on a line whose content (after `file:line:`) starts,
  after optional leading whitespace, with `//` (a comment-only line).
- Any of the above three shapes on a line carrying a trailing `// tz:allow: <reason>` comment.
- Anything inside `cmd/` (never scanned, per D-plat-2).
- The `pgtype.Date` encoding sites (`internal/telemetry/service.go`'s `dateFrom`,
  `internal/charging/service.go`'s `dateFromTime`, both `pgtype.Date{Time: t, Valid: true}`) —
  confirmed by inspection to use no `time.Date(...)` call at all, so no leg can ever match them
  (D1's requirement satisfied by construction, not by exclusion).

## Risks / Trade-offs

- **[Accepted, documented]** The `_test.go` blanket exclusion (D-plat-3) means a future test
  anchored on the wrong zone is not caught by this guard — it never could be, by any grep. Not a
  regression from tier 6's state.
- **[Non-risk, verified]** All six comment-only markers are additive, same-line, zero logic
  change — `go build ./...`/`go vet ./...` behavior is provably unaffected by a trailing `//`
  comment.
- **[Non-risk, verified]** The guard's `internal/`-wide scope (broader than the other three
  guards' `internal/gateway/` scope) currently produces exactly the six expected matches, no
  more — confirmed by running all three legs against the full tree before finalizing (Test
  Contract (a) above).

### Known blind spots — what this guard does NOT catch (review round 1, R1–R5)

A grep guard is a cheap net, not a proof. These were found by the reviewer deliberately
attacking the patterns, and are recorded so a future maintainer does not over-trust a green
`make tz-guard`. **This list is part of the guard's contract: extend it whenever a new
evasion is found.**

| # | Evasion | Status |
|---|---|---|
| R1 | `t.Truncate(24 * time.Hour)` — the classic Go day-rounding footgun (it rounds against the zero time in UTC, not local midnight). No `time.Date` shape, so leg 2 never saw it. | **FIXED** — leg 4 added, catching both `24 * time.Hour` and `time.Hour * 24`, while leaving sub-day truncations (`time.Microsecond`) alone. |
| R2 | A `time.Date(...)` call with its midnight arguments spread across several lines. | **Open** — inherent to a line-oriented grep. Accepted. |
| R3 | `var nowFn = time.Now` then `nowFn()`. The literal `time.Now()` never appears at the call site. | **Open, and partly intended** — this is the clock-seam pattern `scheduler.go` already uses deliberately for testability. Guarding it would fight a pattern the project wants. |
| R4 | A `/* … */` block comment discussing the guarded shapes is flagged as code (false POSITIVE, not negative) — the comment filter only understands `//`. | **Open** — not reachable today: `internal/` currently contains no block comments. A future one gets a `tz:allow`. |
| R5 | A zone name assembled by concatenating constants. | **Open** — beyond what any grep can do. Accepted. |

The honest summary: this guard reliably stops the **careless** reintroduction of a raw `now`,
a hand-rolled midnight, a day-scale `Truncate`, or a hardcoded zone. It does not stop a
determined or unusual construction, and — see D-plat-3 — it does not look at tests at all.

**It also would not have caught any of tier 6's six bugs**, which were frame *mismatches*
between two legitimately-obtained values, not illegitimate calls. That class needs a type the
compiler can check, not a grep; it is recorded as a backlog item rather than pretended away
here.

## Migration Plan

None — no database object, no code-behavior change. Rollback is a plain revert of the
`Makefile` target, the `.PHONY`/`check` wiring, the six comment markers, and the doc edits.
