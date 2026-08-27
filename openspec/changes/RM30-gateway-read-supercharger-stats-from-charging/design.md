# Design — RM30-gateway-read-supercharger-stats-from-charging

> Numbering note: this document's decisions are **D1, D2, …**, scoped to this change only —
> distinct from the roadmap's own **Decision 1–7** in
> `openspec/roadmaps/RM30-supercharger-stats-read-from-charging.md` (cited as **roadmap
> Decision N**) and from tier 1's archived decisions in
> `openspec/changes/archive/charging/2026-08-27-RM30-charging-add-session-read-port/design.md`
> (cited as **tier 1 D1**, …). **D1–D4 and D6 transcribe binding instructions from the
> leader's dispatch** (which itself transcribes the roadmap's tier-2 proposal prompt and
> roadmap Decisions 1/2/3/5/6); each says so in its heading. **D5 and D7–D10 are judgment
> calls this artifacts pass had to make** to turn those into a buildable, testable change —
> each states its reasoning so the pipeline leader and any future reader can evaluate it
> without re-deriving it.

## Context

Five facts about the existing code shape everything below.

1. **The port this tier consumes already exists and is fully specified.**
   `charging.SessionReader.ListSessionsByVehicleBetween(ctx, accountID, teslaID, from, to)`
   (tier 1, `internal/charging/charging.go`) takes whole UTC calendar days, `to` inclusive of
   its entire day (translated to a half-open upper bound internally), returns
   `[]charging.Session` **ascending** by `ChargeStopDateTime` (oldest-first — tier 1 D3, matching
   the index `idx_charge_sessions_vehicle_stop` was built ASC for), and **never** returns a
   session whose `TeslaID` is `nil` for any `teslaID` filter (tier 1 D6 — SQL `NULL = value` is
   neither true nor false). This tier's job is to call it correctly and adapt its shape to the
   page's existing display contract, not to design a new port.

2. **The page's existing month-based flow does everything this tier removes.**
   `internal/gateway/handlers/supercharger.go`'s `clampSuperchargerMonths` /
   `defaultSuperchargerMonths` / `superchargerReadLimit` / the in-memory
   `ChargeStartDateTime >= since` filter are the exact pieces the dispatch's proposal prompt
   names for removal, replaced by the platform's mandated `?start=&end=` contract
   (`internal/gateway/AGENTS.md` §"HTTP date-filter convention") via a new
   `parseSuperchargerRange`, mirroring `parseHistoryRange` (`internal/gateway/handlers/history.go`).

3. **`fragments.RangePreset` already exists and is directly reusable.** Added by
   `RM8-gateway-history-date-range` for the dashboard history preset buttons
   (`internal/gateway/templates/fragments/history_vm.go`), it is `{Label, StartStr, EndStr,
   Active string/string/string/bool}` — exactly the shape a "3/6/12 months" preset button
   emitting an absolute `?start=&end=` href needs. Both `RangePreset` and
   `SuperchargerStatsView` live in the same package (`fragments`), so reusing it costs zero new
   imports and zero new types — the AI-efficiency "closed, small vocabulary" rule applied
   directly, not just cited.

4. **`charging.Session` and `telemetry.SuperchargerSession` share every field this page reads.**
   `ChargeStartDateTime`, `ChargeStopDateTime`, `SiteLocationName`, `EnergyKWh`, `TotalCost`,
   `Currency` all exist on both types with identical Go types (`time.Time`, `string`,
   `*float64`, `*float64`, `*string`). `charging.Session` has **no** `CountryCode` or
   `BillingType` field — `charge_sessions` never carried either column (tier 1 Context fact 1;
   roadmap Decision 1) — so `buildSuperchargerRows`/`SuperchargerRowVM` dropping those two
   fields is not an added constraint this tier invents, it is what the new type's shape already
   forces; the compiler enforces it the moment the type swap lands (task 2.1 cannot reference a
   field that does not exist).

5. **The existing chart-bucketing code assumes a fixed, caller-supplied month COUNT, not a
   window.** `buildSuperchargerChart(filtered, months, since)` pre-allocates exactly `months`
   buckets because `months` came straight from the validated `?months=N` query value. Under
   `?start=&end=`, there is no `months` integer for an arbitrary caller-supplied window — the
   bucket count must be **derived from the window itself** (D10 below).

## Goals / Non-Goals

**Goals**

- `GET /supercharger-stats` and `GET /ui/supercharger-stats` accept `?start=YYYY-MM-DD&end=YYYY-MM-DD`,
  never `?months=N`, matching the platform's one closed HTTP date-filter vocabulary (D1).
- The single Supercharger-session read is `charging.SessionReader.ListSessionsByVehicleBetween`,
  bounded by the requested date window at the database, replacing the old row-limit-then-filter
  shape (D2, D3).
- The sessions table keeps its pre-existing newest-first display order despite the port
  returning oldest-first (D3).
- The 3/6/12-month preset buttons keep their existing labels and keep landing on exactly 3, 6,
  or 12 monthly chart bars respectively — a cosmetic regression here (e.g. a "6-month" preset
  that renders 7 bars) is not acceptable (D5, D10).
- `CountryCode`/`BillingType` are gone from the view model, the template, and the catalogue —
  compile-time enforced, not just visually removed (D4).
- `internal/gateway` no longer imports `internal/telemetry` for the Supercharger Stats path
  specifically (dashboard/history paths are unaffected — roadmap Decision 2).

**Non-Goals**

- Any change to `internal/charging` — tier 1 already shipped `SessionReader` with everything
  this tier needs.
- Any change to `internal/telemetry`, the dashboard vehicle-card tiles, or the dashboard history
  charts (roadmap Decision 2 — a separate backlog item per the roadmap's "Future work").
- Any database migration, index, column, or constraint (roadmap Decision 1 — no DB object is
  touched by this tier, same as tier 1).
- Reproducing `parseHistoryRange`'s `browser_tz`-cookie-driven "browser-local today" frame on
  this endpoint (D9a — a deliberate, documented scope cut, not an oversight). The no-future-date
  REJECTION itself is IN scope (D9b); only history's browser-local frame and its
  yesterday — rather than today — boundary are cut.

---

## Decisions

### D1 — `?start=&end=` replaces `?months=N`, mirroring `parseHistoryRange`'s shape (binding — dispatch/roadmap Decision 5)

A new `parseSuperchargerRange(c *gin.Context, today time.Time) (start, end time.Time, ok bool)`
in `supercharger.go` replaces `clampSuperchargerMonths`. It reuses `parseHistoryRange`'s
validation **shape** (same parameter names, same `time.Parse("2006-01-02", ...)` mechanics, same
`ok bool` degrade-to-empty-state contract) but is a **separate function with its own constants**
— it is not a generalization of `parseHistoryRange` into a shared helper, because the two
endpoints' defaults, caps, and (per D9 below) "today" semantics genuinely differ, and forcing
one parameterized function to cover both would trade a few duplicated lines for a harder-to-read
call site at both endpoints — the AI-efficiency "do not over-abstract" rule applied directly
(`CLAUDE.md` §"AI efficiency"): the two functions are short, self-describing, and read
side-by-side without indirection.

Validation, in order (mirrors ALL of `parseHistoryRange`'s rejection rules —
`internal/gateway/AGENTS.md` §"HTTP date-filter convention" states them as a closed set. Its
"no future date" rule is reproduced here at a DIFFERENT boundary — UTC today rather than
browser yesterday — see D9):

1. Both `start` and `end` absent → default window (D5 below), `ok=true`.
2. Either present → both required and well-formed `YYYY-MM-DD` (`time.Parse`), else `ok=false`.
3. `end.Before(start)` → `ok=false`.
4. `end.After(today)` → `ok=false` (D9). `end == today` IS accepted — the boundary is strictly
   after, unlike history's browser-yesterday cap.
5. Window wider than `superchargerRangeMaxDays` (400, D6) → `ok=false`.

```go
const superchargerRangeDefaultMonths = 6
const superchargerRangeMaxDays = 400

func parseSuperchargerRange(c *gin.Context, today time.Time) (start, end time.Time, ok bool) {
	rawStart := c.Query("start")
	rawEnd := c.Query("end")

	if rawStart == "" && rawEnd == "" {
		end = today
		start = monthsBackFrom(end, superchargerRangeDefaultMonths) // D5
		return start, end, true
	}

	s, err := time.Parse("2006-01-02", rawStart)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	e, err := time.Parse("2006-01-02", rawEnd)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	if e.Before(s) {
		return time.Time{}, time.Time{}, false
	}
	if e.After(today) { // D9 — no future window; e == today is accepted
		return time.Time{}, time.Time{}, false
	}
	if e.Sub(s).Hours()/24 > float64(superchargerRangeMaxDays) {
		return time.Time{}, time.Time{}, false
	}
	return s, e, true
}
```

On `ok=false`, both `SuperchargerStatsPage` and `SuperchargerStatsFragment` render the empty
state with `Presets: nil` (no selector — mirrors `DashboardHistoryFragment`'s malformed-request
branch exactly, `internal/gateway/AGENTS.md` §"HTTP date-filter convention": *"On 400, render the
empty-state placeholder ... and return no preset selector"*). `SuperchargerStatsFragment` uses
`renderFragmentError` (sets `HX-Error-Fragment: true` so htmx actually swaps the 400 body in,
per `ai/htmx-go-integration.md` §"Response conventions"); `SuperchargerStatsPage` uses plain
`render` at the same 400 status, because it is a full top-level page navigation, not an htmx
swap target — the `HX-Error-Fragment` opt-in exists specifically for htmx's `responseHandling`
config, which does not apply to a normal browser GET.

**REVISED at Apply (wave 2, progress.json `decisions[]` D5; confirmed by review round 1 finding
R1-1).** `SuperchargerStatsPage`'s 400 path uses **`renderError`, not plain `render`.** The
paragraph above reasoned from *who consumes the response today*; the binding rule in
`ai/htmx-go-integration.md` §"Response conventions" is stated in terms of *what the body is* —
if a non-2xx body is a renderable component, it goes through `renderError`. `renderError` is
`render` plus the `HX-Error-Fragment` header, so on a plain browser GET the two are
byte-identical in what the user sees; the original reasoning was not wrong about today's
behaviour, it was scoped to a caller rather than to the rule. Choosing the rule keeps the two
entry points symmetric and means a future htmx-driven navigation to this page cannot silently
start dropping its own 400 body. Both branches are reached through the shared
`superchargerStatsViewFor` helper (tasks.md 2.1), which returns `(view, status)` and leaves each
handler to pick its render pair.

### D2 — `Deps.SuperchargerReader` changes type, not name (binding — dispatch)

`gateway.Deps.SuperchargerReader`, `handlers.Deps.SuperchargerReader`, and
`Handler.superchargerReader` all change type from `telemetry.SuperchargerReader` to
`charging.SessionReader`. The field/variable **name is unchanged** — this is a type swap, not a
rename, so every call site that merely *wires* the dependency (`cmd/web/main.go`,
`gateway.go`'s `handlers.Deps{...}` construction) needs only its right-hand-side constructor
call updated, not a field-name search-and-replace. `internal/gateway` continues to import
`internal/telemetry` package-wide (for `TelemetryReader`/`Snapshot` — the dashboard tiles and
the history battery chart, roadmap Decision 2); only `supercharger.go`,
`supercharger_test.go`, and `supercharger_vm.go` stop referencing it.

### D3 — Handler reverses the port's ascending order once, before building any of the three view sections (binding — dispatch/tier 1 D3)

`ListSessionsByVehicleBetween` returns `ChargeStopDateTime` ASC (oldest-first — tier 1 D3,
**do not** "fix" the port to DESC; doing so would force a sort step on every call, the exact
outcome tier 1's index design avoided). The Supercharger Stats sessions table has always
displayed newest-first (pre-existing UX, unchanged by this tier). `buildSuperchargerStatsView`
reverses the slice **once**, immediately after the read and before it is handed to
`buildSuperchargerTiles`/`buildSuperchargerChart`/`buildSuperchargerRows`:

```go
for i, j := 0, len(sessions)-1; i < j; i, j = i+1, j-1 {
	sessions[i], sessions[j] = sessions[j], sessions[i]
}
```

Reversing once up front (rather than only the rows slice, separately, after mapping) is
correct because the tiles and chart are order-independent aggregations (a sum and a
month-bucketed sum do not care about slice order) — doing the reversal exactly once keeps the
three builder functions identical in shape to before (each still just iterates `sessions` in
whatever order it receives) and avoids a second, rows-only reverse that would leave tiles/chart
and the table processing two different orderings of the same data for no reason.

### D4 — `CountryCode`/`BillingType` removed from the VM, template, and catalogue (binding — roadmap Decision 1)

`fragments.SuperchargerRowVM` drops both fields. `superchargerTable` (the `.templ` file) drops
both `<td>` cells and both header `i18n.T(ctx, ...)` calls
(`i18n.KeySuperchargerCountry`, `i18n.KeySuperchargerBillingType`). `internal/gateway/i18n/catalog.go`
removes both `Key` constants and their `ES`/`EN` catalogue entries. This is not optional
cleanup: `charging.Session` (D2/Context fact 4) has no `CountryCode`/`BillingType` field, so
`buildSuperchargerRows` cannot populate those `SuperchargerRowVM` fields even if they were left
in place — the type change makes the removal a compile error if skipped, not a style choice.
`KeySuperchargerMonthsPreset` is **kept, unchanged** — it is not a country/billing-type key, and
the "3/6/12 months" preset button copy is unaffected by the query-contract change (D1).

### D5 — Default window and preset windows stay MONTH-ALIGNED, not a raw `AddDate(0, -N, 0)` (judgment call — this pass)

**The problem.** The dispatch says the default window (both params absent) is "6 months,"
preserving current behavior. The *current* behavior (`clampSuperchargerMonths` +
`startOfMonth(time.Now()).AddDate(0, -months+1, 0)`) is **month-aligned**: `since` is always the
1st of some month, so a `months`-bucket chart always renders exactly `months` bars. A naive
`end.AddDate(0, -6, 0)` (today minus 6 calendar months, same day-of-month) is NOT month-aligned
— it produces a `start` that falls mid-month, which (per D10's window-derived bucket count)
would render **7** monthly bars for the "6-month" window instead of 6, because the partial first
month still occupies a whole bucket. That is a visible, avoidable regression the roadmap did not
ask for and the dispatch's phrase "preserves current behaviour" argues directly against.

**The decision.** A new helper, month-aligned like the old `since` computation, generates both
the default window's `start` and every preset's `start`:

```go
// monthsBackFrom returns the whole-day, month-aligned start of the n-calendar-month window
// ending in end's own month: the 1st of the month n-1 months before end's month. Mirrors the
// pre-RM30 months-based window's exact chart-bucket behavior (design.md D5) — n calendar
// buckets, never n+1 — so the 3/6/12 presets and the 6-month default still render exactly n
// bars, not n or n+1 depending on today's day-of-month.
func monthsBackFrom(end time.Time, n int) time.Time {
	return startOfMonth(end).AddDate(0, -(n - 1), 0)
}
```

Default: `end = today`, `start = monthsBackFrom(today, superchargerRangeDefaultMonths)`. Presets
(D-preset below): for each `n` in `{3, 6, 12}`, `pEnd = today`, `pStart = monthsBackFrom(today, n)`.

**This does NOT apply to an explicit, caller-supplied `?start=&end=` window** (D1 step 2) — a
direct API caller's literal dates are honored exactly as given, un-aligned, exactly like
`parseHistoryRange` honors an explicit non-preset window. Month-alignment is a **preset/default
convenience**, not a validation rule; the cap (D6) is the only bound on a custom window's shape.

### D6 — 400-day cap, endpoint-specific (binding — roadmap Decision 6)

`superchargerRangeMaxDays = 400` — wider than `historyRangeMaxDays` (90) because `charge_sessions`
is sparse (a handful of rows per month) where `vehicle_snapshots`/history's source table is dense
(many rows per day); 400 days comfortably covers the 12-month preset plus leap-year slack without
meaningfully widening the read `idx_charge_sessions_vehicle_stop` serves (tier 1's own index proof
already covers an arbitrary-width window — the index is a range scan regardless of how wide the
predicate is). This is the same conclusion roadmap Decision 6 already reached; restated here so
this tier's implementer does not re-derive it. `internal/gateway/AGENTS.md`'s cap statement moves
from one flat number to "per-endpoint, set by data density" (task 4.1) precisely so this number has
a documented home next to history's 90.

### D7 — No database object; `charging.SessionReader` is consumed exactly as tier 1 shipped it (binding — roadmap Decision 1, restated)

This change adds no migration, table, column, index, or constraint, and it adds no method to
`charging.SessionReader` — `ListSessionsByVehicleBetween`'s existing signature
`(ctx, accountID, teslaID, from, to)` is called as-is. Per the dispatch's explicit instruction:
**if this design had concluded the port or the schema needed to change, that conclusion would be
reported as blocked**, because it would trip the project's `database` design gate
(`CLAUDE.md` §Pipeline config → Design-Gates), which requires the owner's sign-off before Apply.
That conclusion was not reached (Context fact 1 — the port already does exactly what this page
needs), so this tier proceeds without a gate confirmation step.

### D8 — The fake reader enforces the SAME `TeslaID`-nil-exclusion contract the real port has (judgment call — this pass, carrying tier 1 D6 into the test double)

`SuperchargerStatsFragment`/`Page` no longer need Go-level "unattributed session" filtering
logic (the old code trusted the fake to mirror the real telemetry port's `WHERE tesla_id = $2`
behavior; nothing changed conceptually — `ListSessionsByVehicleBetween`'s SQL excludes a `NULL`
`tesla_id` row for any `teslaID` filter, tier 1 D6). The rewritten
`fakeSessionReader.ListSessionsByVehicleBetween` (renamed from `fakeSuperchargerReader`) MUST
keep enforcing this in its own filtering logic — returning only sessions whose `TeslaID != nil
&& *TeslaID == teslaID` — so the existing "unattributed sessions never appear" test coverage
(Test Contract T10) continues to prove the page-level behavior rather than silently starting to
pass only because the fake stopped filtering. This is a carry-forward, not a new rule; stated
explicitly because a test-double rewrite is exactly the kind of edit that silently drops an
implicit contract if nobody names it.

### D9 — UTC "today" (no `browser_tz`), but the no-future-date rejection IS reproduced (revised — leader triage)

This decision has two halves that an earlier draft conflated. They are settled differently.

**(a) The timezone frame is plain UTC.** `today` for this endpoint is
`startOfDay(time.Now().UTC())` (reusing `history.go`'s existing `startOfDay` helper, same
package). This endpoint does NOT consult the `browser_tz` cookie or call `browserToday(c)`.
Reproducing RM8's browser-local machinery would add a real runtime dependency (a cookie, a
`time.LoadLocation` call) to an endpoint the roadmap never asked to make timezone-aware, and it
matches the OLD `clampSuperchargerMonths` code path, which never consulted `browser_tz` either.
If a future change wants browser-local semantics here, that is its own decision to make and
document, not an implicit inheritance from this tier.

**(b) A future `end` IS rejected — at UTC today, not browser yesterday.**
`internal/gateway/AGENTS.md` §"HTTP date-filter convention" states the 400 rules as a **closed
set**, and `end.After(startOfDay(now))` is one of them; the roadmap's tier 2 row says "same 400
rules". Dropping the rule would be a deviation from a stated convention, not a gap in the
dispatch's test enumeration (which said "at minimum").

It also has a real failure mode here, contrary to an earlier draft's reasoning. Because D10
derives the chart's bucket count from the **window** rather than from the returned rows,
`?start=2026-03-01&end=2026-12-31` — comfortably inside the 400-day cap, so nothing else
catches it — would render roughly ten empty future month bars. That is precisely the "bars
drawn for periods that cannot have data" defect RM8's rule exists to prevent; the sparse-vs-dense
difference between `charge_sessions` and `vehicle_snapshots` does not save this endpoint,
because the buckets do not come from the rows.

**The boundary differs from history's, deliberately.** `parseHistoryRange` rejects at browser
**yesterday** because the nightly telemetry poll captures "today"'s snapshot tomorrow, so a
history window ending today always had an empty last bar (RM8 D11) — a data-availability fact
specific to `vehicle_snapshots`. Supercharger sessions have **no such collection lag**: a session
that ends this morning is mirrored into `charge_sessions` and is readable today. So this endpoint
accepts `end == today` and rejects only `end` strictly after it. Same rule, correct boundary for
this table's write timing.

### D10 — Chart bucket count is derived from the window, not passed in as a `months` integer (judgment call — this pass)

`buildSuperchargerChart`'s signature changes from `(filtered []telemetry.SuperchargerSession,
months int, since time.Time)` to `(sessions []charging.Session, start, end time.Time)`. Bucket
count and each bucket's calendar month are derived from the window itself, reusing the two
helpers the old month-based code already had (`startOfMonth`, `monthsBetween` — no new date-math
primitive is introduced):

```go
func buildSuperchargerChart(sessions []charging.Session, start, end time.Time) fragments.HistoryChart {
	if len(sessions) == 0 {
		return fragments.HistoryChart{Empty: true}
	}
	anchor := startOfMonth(start)
	numMonths := monthsBetween(anchor, startOfMonth(end)) + 1

	type bucket struct {
		label string
		kwh   float64
	}
	buckets := make([]bucket, numMonths)
	for i := 0; i < numMonths; i++ {
		buckets[i].label = anchor.AddDate(0, i, 0).Format("Jan 2006")
	}
	for _, s := range sessions {
		if s.EnergyKWh == nil {
			continue
		}
		idx := monthsBetween(anchor, s.ChargeStartDateTime.UTC())
		if idx < 0 || idx >= numMonths {
			continue // defensive: port-bounded sessions should always land in-window
		}
		buckets[idx].kwh += *s.EnergyKWh
	}
	// ... maxKWh / HeightPct / Tooltip loop is UNCHANGED from the pre-existing implementation.
}
```

For a `monthsBackFrom`-derived default/preset window (D5), `start` is already the 1st of its
month, so `numMonths` always equals exactly the requested preset count (D5's whole point). For
an explicit, non-aligned custom window (D1 step 2), the first and/or last bucket can represent a
**partial** calendar month — this is expected and not a defect: the bucket still sums whatever
sessions in the requested window fall in that month, exactly as a partial first/last day already
behaves on `parseHistoryRange`'s day-axis charts for a non-preset window. No test in this tier's
Test Contract asserts a specific bucket count for a non-aligned custom window, because the
dispatch's enumerated cases do not require one (T2's explicit-window case only asserts `ok=true`
and the parsed bounds, not chart shape) — a future change that wants a stricter guarantee here is
free to add one without this design blocking it.

---

## Database Changes

**None.** No migration file is added or edited, and no new sqlc query is added —
`ListSessionsByVehicleBetween` (tier 1) is called with its existing signature, unchanged. This
section exists per `openspec/config.yaml`'s "design.md is REQUIRED for any DB-touching change"
to record explicitly that this tier is not one.

---

## Test Contract (expected values authored before implementation, per `ai/go-conventions.md`)

All tests in this tier are **pure/offline** (`httptest` + a fake `charging.SessionReader`,
mirroring `history_test.go`'s existing pattern) — no `DATABASE_URL` gate. Fixtures use
`charging.Session{...}` values built directly in test code; no seeding, no SQL.

**T1. Both `start` and `end` absent → the month-aligned 6-month default window (D5).**
`parseSuperchargerRange(c, today)` with no query params. Expected: `ok=true`,
`end == today`, `start == monthsBackFrom(today, 6)` — e.g. for `today = 2026-08-27`: `end =
2026-08-27`, `start = 2026-03-01` (`startOfMonth(2026-08-27)=2026-08-01`, minus 5 months =
`2026-03-01`).

**T2. An explicit, valid `?start=&end=` window is honored exactly as given (un-aligned).**
`?start=2026-06-15&end=2026-08-20`. Expected: `ok=true`, `start ==
2026-06-15T00:00:00Z`, `end == 2026-08-20T00:00:00Z` — NOT rounded to a month boundary (D5's
"only presets/default are month-aligned" clause).

**T3. `end` before `start` is rejected.** `?start=2026-08-20&end=2026-06-15`. Expected:
`ok=false`.

**T4. A malformed non-ISO date is rejected — either side.**
`?start=2026-13-40&end=2026-08-20`. Expected: `ok=false`. Also: `?start=2026-06-15&end=not-a-date`.
Expected: `ok=false`.

**T5. Only one of `start`/`end` present is rejected.** `?start=2026-06-15` (no `end`). Expected:
`ok=false`. Also `?end=2026-08-20` (no `start`). Expected: `ok=false`.

**T6. A window wider than the 400-day cap is rejected; exactly 400 days is accepted.**
`?start=2025-07-01&end=2026-08-06` (401 days wide) → `ok=false`. The same `start` with
`end=2026-08-05` (exactly 400 days wide) → `ok=true`.

**T7. Zero sessions in the window renders the empty state.** Fake returns `[]charging.Session{}`
for the resolved window. Expected: `buildSuperchargerStatsView` returns `Empty=true`,
`Chart.Empty=true`, `len(Sessions)==0`, all four tile strings at their zero-value/placeholder
form (`Sessions="0"`, `AvgKWh="—"`).

**T8. Reader error degrades to the empty state, never propagates.** Fake returns a non-nil
`error`. Expected: same as T7 (`Empty=true`, `Chart.Empty=true`), and the error is logged (not
returned to the caller, mirrors the pre-existing `buildSuperchargerStatsView` degrade branch).

**T9. Newest-first display ordering survives the port's ascending order.** Fake returns three
sessions **ascending** by `ChargeStopDateTime`: S1 (Jan), S2 (Feb), S3 (Mar) — i.e. in the exact
order the real port would return them. Expected: `v.Sessions[0]` corresponds to S3 (most recent
`ChargeStopDateTime`/`ChargeStartDateTime`), `v.Sessions[2]` corresponds to S1 (oldest) — the
handler's single reverse (D3) is proven by asserting the OUTPUT order is the REVERSE of the
fake's input order, not by asserting against a hardcoded expectation that happens to coincide.

**T10. A session with `TeslaID == nil` never appears, mirroring the real port's contract (D8).**
Fake seeded with an unattributed session (`TeslaID: nil`) alongside an attributed one
(`TeslaID: ptrInt64(42)`), fake's `ListSessionsByVehicleBetween` filters exactly like the real
SQL would. Call with `teslaID=42`. Expected: only the attributed session appears in
`v.Sessions`; the unattributed session's `EnergyKWh` is excluded from the Energy/Sessions tiles.

**T11. The 3/6/12-month presets always yield exactly that many chart bars (D5+D10 together).**
`buildSuperchargerPresets(ctx, start, end, today)` for `today = 2026-08-27`: `Presets[0]`
(n=3): `StartStr="2026-06-01"`, `EndStr="2026-08-27"`; `Presets[1]` (n=6): `StartStr="2026-03-01"`;
`Presets[2]` (n=12): `StartStr="2025-09-01"`. Then, for the n=6 preset's resolved window, seed one
session per month across all 6 months and assert `buildSuperchargerChart` returns exactly 6 bars
(not 5, not 7) — the D5/D10 regression this design exists to prevent.

**T12. `SuperchargerRowVM` and `charging.Session` carry no `CountryCode`/`BillingType` (D4,
compile-time).** Not a runtime test — recorded here so the implementer does not add either field
back defensively. If a future need for country/billing display resurfaces, that is a new
change against `internal/charging`'s schema, not a gateway-side workaround.

**T13. `end` equal to UTC today is ACCEPTED (D9b boundary, inclusive side).**
`?start=2026-03-01&end=2026-08-27` with `today = 2026-08-27`. Expected: `ok=true`, `end ==
2026-08-27T00:00:00Z` — proving this endpoint does NOT inherit history's browser-yesterday cap.

**T14. `end` after UTC today is REJECTED with no selector (D9b boundary, exclusive side).**
`?start=2026-03-01&end=2026-08-28` with `today = 2026-08-27` — one day into the future, and
only 180 days wide, so the 400-day cap (T6) cannot be what rejects it. Expected: `ok=false`, and
at the handler level HTTP 400 with `Empty=true` and `Presets: nil` (the same malformed-request
degradation T4/T5 assert). A far-future `end` inside the cap
(`?start=2026-03-01&end=2026-12-31`) is rejected by the same rule.

### What must NOT change

- `TestBuildSuperchargerTiles_*` and `TestBuildSuperchargerRows_*`'s existing assertions about
  currency aggregation (never summed across currencies), nil-cost/nil-currency exclusion, and
  "—" placeholders — these are unaffected by the type/port swap; only the input slice's element
  type changes from `telemetry.SuperchargerSession` to `charging.Session`.
- The route paths (`GET /supercharger-stats`, `GET /ui/supercharger-stats`), the auth-guard
  redirect-to-`/login` behavior, and the `#supercharger-stats-content` htmx target/swap
  mechanics — none of this tier's changes touch routing or auth.

---

## Risks / Trade-offs

- **`parseSuperchargerRange` duplicates `parseHistoryRange`'s validation shape instead of
  sharing a helper.** Accepted deliberately (D1) — the two endpoints' defaults, caps, and
  "today" semantics differ (D5, D9), so a shared parameterized helper would need enough
  branching to erase the readability win a shared function is supposed to buy.
- **A custom (non-preset) `?start=&end=` window can render a chart with a partial first/last
  monthly bucket.** Accepted (D10) — no test in this tier's contract requires otherwise, and the
  behavior mirrors how `parseHistoryRange`'s day-axis already treats a non-preset window's
  boundary days.
- **This endpoint rejects a future `?end=` at UTC today, where `parseHistoryRange` rejects at
  browser yesterday.** Deliberate (D9b) — the extra day history excludes is its nightly-poll
  capture lag (RM8 D11), which `charge_sessions` does not have. Consequence: the two endpoints
  answer "is this window in the future?" one calendar day apart, and a user in a UTC+N timezone
  can see a supercharger window accepted that history would reject. Accepted as the honest
  reading of each table's own write timing.

## Verification signals

Per the binding `Test-Execution-Policy`, the assistant runs and reports: `go build ./...`,
`go vet ./...`, `gofmt -l .`, `make build`, `make vet`, `make bins`, `make templ` (after the
`.templ` edit, task 2.2, to regenerate `supercharger_stats_templ.go`), `make css` (only if a new
Tailwind/DaisyUI class is introduced — this tier introduces none, so `make css` is a no-op
safety run), and the three standalone guards — `make ui-guard` (no new inline DaisyUI class),
`make i18n-guard` (every string still resolves through `i18n.T`, including the KEPT
`KeySuperchargerMonthsPreset` and the REMOVED-key sweep), `make money-guard` (no new raw
currency literal). It also runs `openspec validate RM30-gateway-read-supercharger-stats-from-charging --strict`.

The owner alone runs the suite:

```
go test ./internal/gateway/...
```

or the full suite:

```
go test ./...
```

Until the owner runs these and reports the results, this tier's implementation status is
**awaiting-user-verification**, never "done".
