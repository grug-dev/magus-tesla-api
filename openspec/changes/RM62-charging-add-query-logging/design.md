# Design — RM62-charging-add-query-logging

## Context

`internal/charging` exposes eight public ports (`internal/charging/charging.go`):

- **`Writer`** — 3 methods: `Create`, `Update`, `Delete`. Implemented by `*writer`
  (`service.go`).
- **`Reader`** — 4 methods: `ListEntriesByVehicle`, `ListEntriesByVehicles`,
  `ListEntriesByVehicleBetween`, `ListEntriesByVehicleUpdatedSince`. Implemented by
  `*reader` (`service.go`).
- **`SessionWriter`** — 1 method: `MirrorSessions`. Implemented by `*sessionWriter`
  (`session_writer.go`).
- **`SessionReader`** — 1 method: `ListSessionsByVehicleBetween`. Implemented by
  `*sessionReader` (`session_reader.go`).
- **`SuperchargerSessionAnalyticsReader`** — embeds `SessionReader` and adds 2 methods:
  `ListSessionsByVehicleUpdatedSince`, `ListSessionsByVehicle`. Implemented by the
  **same** `*sessionReader` concrete type as `SessionReader` — `charging.go`'s own doc
  comment on `NewSuperchargerSessionAnalyticsReader` states this directly ("returning
  the same underlying `*sessionReader` `NewSessionReader` returns — one concrete type
  satisfies both interfaces"). There are two separate exported constructors,
  `NewSessionReader` and `NewSuperchargerSessionAnalyticsReader`, precisely so
  `internal/gateway` and `internal/analytics` can each depend on the narrower interface
  they actually need (`charging.go`'s comment on `SuperchargerSessionAnalyticsReader`).
- **`SessionVerifier`** — 1 method: `VerifySession`. Implemented by `*sessionVerifier`
  (`session_verifier.go`).
- **`MirrorWatermarkStore`** — 2 methods: `MirrorWatermark`, `AdvanceMirrorWatermark`.
  Implemented by `*mirrorWatermarkStore` (`mirror_watermark.go`).
- **`MonthlyCapacityCalculator`** — 1 method: `Calculate`. Implemented by
  `*monthlyCapacityCalculator` (`monthly_capacity.go`).

### Every caller of every method, walked from the code

Read directly from `internal/app/processor.go`, `internal/analytics/recalculate.go`,
`internal/gateway/handlers/*.go`, `cmd/poller/main.go`, `cmd/web/main.go`, and
`cmd/monthly-capacity/main.go` — not from the knowledge base alone.

**The nightly cycle's direct callers (`internal/app/processor.go`):**

- `processChargingData` (step 2, once per vehicle): `p.mirrorWatermarks.MirrorWatermark`,
  `p.superchargerHistoryReader.SuperchargerHistoryByVehicleUpdatedSince` (telemetry, not
  this module), `p.sessionWriter.MirrorSessions`,
  `p.mirrorWatermarks.AdvanceMirrorWatermark`.
- `recalculateAnalytics` (step 3, once per vehicle): `p.recalculator.Reconcile`
  (`analytics.Recalculator`, not this module directly — but see below),
  `p.analyticsReader.ConsumedByDay`, `p.gapWriter.ReconcileWindow`.
- `runMonthlyCapacityStep` → `callMonthlyCapacityCalculator` (step 4, once a month):
  `p.monthlyCapacityCalculator.Calculate`.

**The nightly cycle's indirect callers, one hop through `internal/analytics`
(`internal/analytics/recalculate.go`):**

- `recalculator.Reconcile` (called by step 3 above) calls
  `r.supercharger.ListSessionsByVehicleUpdatedSince` and
  `r.manual.ListEntriesByVehicleUpdatedSince` directly, then — only if at least one
  source returned rows — calls `r.Recalculate` on itself.
- `recalculator.Recalculate` calls `r.supercharger.ListSessionsByVehicleBetween` and
  `r.manual.ListEntriesByVehicleBetween`. `Recalculate` has **two** callers: (a)
  `Reconcile` above, on the nightly path, and (b)
  `internal/gateway/handlers/external_charges.go`'s post-write hook
  (`recalculateAfterExternalChargeWrite`), which calls it directly, synchronously,
  right after a manual charge `Writer.Create`/`Update`/`Delete` succeeds — a write
  request, not a page-load GET.
- `r.supercharger` is the `charging.SuperchargerSessionAnalyticsReader` this module
  hands to `analytics.NewRecalculator` — built via `charging.NewSuperchargerSessionAnalyticsReader(pool)`, both in
  `cmd/poller/main.go` (nightly) and `cmd/web/main.go` (the gateway's own
  `AnalyticsRecalculator`, used only for the write-triggered `Recalculate` call above,
  never for `Reconcile` — `cmd/web` never calls `Reconcile`).
- `r.manual` is `charging.Reader`, built via `charging.NewReader(pool)` in both the
  same two places.

**The gateway's own direct callers (`internal/gateway/handlers/*.go`), reached only on a
live HTTP request:**

- `handlers.go` wires `Deps.ChargingWriter` → `charging.NewWriter(pool)`,
  `Deps.ChargingReader` → `charging.NewReader(pool)`,
  `Deps.SuperchargerReader` → `charging.NewSessionReader(pool)`,
  `Deps.SuperchargerVerifier` → `charging.NewSessionVerifier(pool)`.
- `external_charges.go`: `h.chargingWriter.Create/.Update/.Delete` (the manual-charge
  form's save/edit/delete handlers — every one of `Writer`'s 3 methods, and ONLY these
  handlers call them anywhere in the repo); `h.chargingReader.ListEntriesByVehicleBetween`
  (`buildExternalChargesPage` — the manual-charges page's own list render, and
  `inProgressConflictOn` — the create/edit form's same-day-conflict guard, both request
  paths); `h.chargingReader.ListEntriesByVehicles` (`fetchEntryVM`,
  `fetchEntryTeslaIDAndChargedOn` — row lookups used by the edit/delete handlers).
- `supercharger.go`: `h.superchargerReader.ListSessionsByVehicleBetween` (the
  Supercharger-stats page's own list render, twice — the page load and its history
  chart); `h.superchargerVerifier.VerifySession` (the battery-percentage edit PATCH
  handler).
- `Reader.ListEntriesByVehicle` and `SuperchargerSessionAnalyticsReader.ListSessionsByVehicle`
  have **no caller anywhere in the repo** — grep of the whole tree for
  `.ListEntriesByVehicle(` and `.ListSessionsByVehicle(` finds only each method's own
  `chargingdb` query call inside its own implementation file.

**The manual CLI tool (`cmd/monthly-capacity/main.go`):** calls
`charging.NewMonthlyCapacityCalculator(pool).Calculate(ctx, period, teslaID)` directly,
on demand, from a terminal — never from a web request.

## Goals / Non-Goals

**Goals:**
- Every charging-module call the nightly cycle makes, directly or through
  `analytics.Recalculator`, prints one `logging.Note` line naming its vehicle and, where
  relevant, its date window and the rows/count it touched.
- Zero behavior change: every decorated method's return value and error pass through
  unaltered.
- Zero added log lines on any request `internal/gateway` serves.
- Wiring lives inside the module's own constructors, so `cmd/poller`, `cmd/web`, and
  `cmd/monthly-capacity` all get the decorators automatically, with no `cmd/` edit.

**Non-goals (explicitly out of scope for this tier):**
- Logging `Writer` (any method), `SessionReader`'s `NewSessionReader`-wired calls, or
  `SessionVerifier` — see the port table and D1/D2/D3.
- Logging `Reader.ListEntriesByVehicleBetween`, `Reader.ListEntriesByVehicles`,
  `Reader.ListEntriesByVehicle` — see D4.
- Logging `SuperchargerSessionAnalyticsReader.ListSessionsByVehicle` — no caller today
  — see D5.
- `internal/tesla` / `internal/account` query logging — not in this roadmap (see the
  roadmap's own "Out of scope").
- Any schema, table, column, index, or migration change — there is none in this tier.
- Structured logging / `slog` adoption (`ai/go-conventions.md` §Logging — stdlib `log`
  only, project-wide, unrelated to this tier).

## Port table

The single most important table in this document. "In" means the port gets a decorator;
"out" means it does not, and no code in this tier touches its constructor.

| Port | In/out | Methods logged | Reason |
|---|---|---|---|
| `Writer` | **OUT** | none — no decorator | All 3 methods' only callers are `internal/gateway/handlers/external_charges.go`'s save/edit/delete handlers. Zero nightly-path caller, direct or indirect. |
| `Reader` | **IN, partial** | `ListEntriesByVehicleUpdatedSince` (1 of 4) | This is the one method whose only caller anywhere is `analytics.Recalculator.Reconcile` — nightly-only. `ListEntriesByVehicleBetween` and `ListEntriesByVehicles` are dominated by direct gateway reads (a page render, a conflict check, two row lookups) that fire on every request; `ListEntriesByVehicle` has no caller at all. See D4. |
| `SessionWriter` | **IN, full** | `MirrorSessions` (1 of 1) | Its only caller anywhere is `internal/app/processor.go`'s `processChargingData`, step 2, nightly. |
| `SessionReader` | **OUT** | none — no decorator | This constructor (`NewSessionReader`) is wired ONLY to `internal/gateway`'s `Deps.SuperchargerReader`. Both its call sites (`supercharger.go`) serve a live page render. See D2/D3. |
| `SuperchargerSessionAnalyticsReader` | **IN, partial** | `ListSessionsByVehicleBetween`, `ListSessionsByVehicleUpdatedSince` (2 of 3) | This is a SEPARATE exported constructor (`NewSuperchargerSessionAnalyticsReader`) from `SessionReader`'s, even though both build the same concrete type. Every call reaching it goes through `analytics.Recalculator` (`Reconcile`, nightly-only, or `Recalculate`, nightly- or write-triggered) — never a bare gateway dashboard read, which uses the other constructor. `ListSessionsByVehicle` has no caller anywhere today; left silent. See D3/D5. |
| `SessionVerifier` | **OUT** | none — no decorator | Its only caller is `internal/gateway/handlers/supercharger.go`'s battery-percentage edit PATCH handler. Zero nightly-path caller. |
| `MirrorWatermarkStore` | **IN, full** | `MirrorWatermark`, `AdvanceMirrorWatermark` (2 of 2) | Both methods' only caller anywhere is `internal/app/processor.go`'s `processChargingData`, step 2, nightly. |
| `MonthlyCapacityCalculator` | **IN, full** | `Calculate` (1 of 1) | Its only callers are `internal/app/processor.go`'s step 4 (nightly, once a month) and `cmd/monthly-capacity`, a manual CLI tool — neither is a web request. |

Five ports get a decorator; three (`Writer`, `SessionReader`, `SessionVerifier`) do not.
Of the five decorated ports, two log every method (`SessionWriter`,
`MirrorWatermarkStore`, plus the single-method `MonthlyCapacityCalculator`) and two log
a strict subset (`Reader`: 1 of 4; `SuperchargerSessionAnalyticsReader`: 2 of 3).

## Decisions

### D1 — `Writer` is excluded entirely; no decorator, no wiring change

Grepping every caller of `charging.Writer` in the repo shows exactly one file:
`internal/gateway/handlers/external_charges.go`. All three methods —
`Create`, `Update`, `Delete` — are called only from that file's manual-charge
save/edit/delete handlers, each firing once per user form submission. None has any
nightly-path caller: `internal/app` never imports or references `charging.Writer`, and
neither does `internal/analytics`.

**Why not add a silent decorator anyway, "for consistency" with the other excluded
ports:** a decorator that logs nothing is dead code — three empty pass-through methods
and a compile-time assertion that guards against nothing this tier's own goal cares
about. The roadmap's own tier row says "a port you exclude entirely needs no decorator
at all — say so." This is that case.

### D2 — `SessionReader` is excluded entirely, even though its concrete type is decorated under a different name

`NewSessionReader` is wired only into `internal/gateway`'s `Deps.SuperchargerReader`
(`cmd/web/main.go`). Its two call sites, both in `supercharger.go`, serve the
Supercharger-stats page's list render and its history chart — both fire on every page
load. There is no nightly-path caller reachable through this specific constructor.

This is not the same statement as "`ListSessionsByVehicleBetween` is never logged" — see
D3, which explains why the identical method, reached through the sibling constructor
`NewSuperchargerSessionAnalyticsReader`, DOES log. D2 is narrowly about which
CONSTRUCTOR this tier leaves untouched.

### D3 — `SuperchargerSessionAnalyticsReader` gets its own decorator, separate from and independent of `SessionReader`'s exclusion

`charging.go` already gives `SessionReader` and `SuperchargerSessionAnalyticsReader` two
separate exported constructors over the same concrete `*sessionReader` type, for exactly
the reason this tier now exploits: `internal/gateway` depends on the narrow
`SessionReader` interface (1 method) and `internal/analytics` depends on the wider
`SuperchargerSessionAnalyticsReader` interface (3 methods, embedding the first). Because
Go interfaces are structural, wrapping the value ONE constructor returns has no effect on
what the OTHER constructor returns — they call the same unexported `newSessionReader`,
but nothing forces them to wrap its result the same way.

This tier decorates only `NewSuperchargerSessionAnalyticsReader`. Every call that
reaches it is driven by `internal/analytics`'s own `Recalculator` — `Reconcile`
(nightly-only) or `Recalculate` (nightly-triggered via `Reconcile`, or write-triggered
via the gateway's post-write hook, never a bare page-load GET). `NewSessionReader`
stays exactly as it is today, so the gateway's own Supercharger-stats page gains zero
log lines from this tier.

Within the decorator: `ListSessionsByVehicleBetween` and
`ListSessionsByVehicleUpdatedSince` both log — their only path to being called is
through `analytics.Recalculator`, which is itself already decorated (tier 2) to log
`Recalculate`/`Reconcile` around every such call. `ListSessionsByVehicle` stays a silent
pass-through — see D5.

**Rejected alternative — merge `SessionReader` and `SuperchargerSessionAnalyticsReader`
into one decorated interface, and have the gateway construct it too, filtering by
method.** This would touch `charging.go`'s two-constructor design (see its own doc
comment, quoted in Context, for why it exists) and reproduce the exact per-method
silence this design already achieves for free by using the constructor split that is
already there. Not touching `charging.go`'s existing interface/constructor shape at all
is simpler and lower-risk.

### D4 — `Reader` logs only `ListEntriesByVehicleUpdatedSince`; its other 3 methods are silent pass-throughs

Unlike `SuperchargerSessionAnalyticsReader`, `charging.Reader` has exactly ONE exported
constructor, `NewReader`, used for BOTH `internal/gateway`'s `Deps.ChargingReader` (the
manual-charges page's own reads) AND `analytics.NewRecalculator`'s `manual` field (the
nightly/write-triggered reads). There is no constructor-level split available here
without changing `charging.go`'s existing shape, which this tier does not do (see D3's
rejected alternative — the same reasoning applies here in reverse: `Reader` was never
split into two interfaces the way `SessionReader`/`SuperchargerSessionAnalyticsReader`
were, and inventing that split now, for logging alone, is a bigger and riskier change
than this tier's stated scope). So the choice here is per-METHOD, inside one decorator,
mirroring `internal/analytics/query_log.go`'s own `loggingReader` shape exactly.

- **`ListEntriesByVehicleUpdatedSince` logs.** Its only caller anywhere in the repo is
  `analytics.Recalculator.Reconcile` — nightly-only, exactly the same shape as tier 2's
  `analytics.Reader.ConsumedByDay`.
- **`ListEntriesByVehicleBetween` stays silent**, even though it IS reached on the
  nightly path (via `Recalculate`, called from `Reconcile`). It is EXCLUDED anyway
  because its call volume is dominated by two direct gateway call sites:
  `buildExternalChargesPage` (the manual-charges page's own list render — fires on
  every page view) and `inProgressConflictOn` (the create/edit form's same-day-conflict
  guard — fires on every submission). Because `NewReader` is the single shared
  constructor (unlike `SuperchargerSessionAnalyticsReader`, D3), logging this method
  logs it for EVERY caller alike — there is no way to log only the nightly-triggered
  calls and skip the gateway-triggered ones. Given the roadmap's own explicit warning
  ("logging all eight adds gateway-request noise to every page load, which is not what
  this ticket asked for"), the live-request traffic here is far higher-frequency than
  the once-per-vehicle-per-night `Reconcile`-triggered call, so this method is treated
  as a gateway read for logging purposes.
- **`ListEntriesByVehicles` stays silent.** Both its callers
  (`fetchEntryVM`, `fetchEntryTeslaIDAndChargedOn`) are gateway row-lookup helpers used
  by the edit/delete handlers; it has no nightly-path caller at all.
- **`ListEntriesByVehicle` stays silent.** It has no caller anywhere in the repo today.

**The gap this opens is closed by D10, not accepted.** Because
`ListEntriesByVehicleBetween` stays silent inside this module's decorator, a nightly run
would not show how many manual charge entries fed a given `Recalculate` call. The user
rejected accepting that. D10 closes it from the caller's side instead, which neither of
the two alternatives considered here could do.

### D5 — `ListSessionsByVehicle` stays a silent pass-through: no caller today

Grep of the whole repository for `.ListSessionsByVehicle(` (the exact call, not its
`…Between`/`…UpdatedSince` siblings) finds only the method's own implementation inside
`session_reader.go`. Nothing — not `internal/analytics`, not `internal/gateway`, not any
`cmd/` — calls it. It exists on `SuperchargerSessionAnalyticsReader` reachable but
unused.

The decorator still implements it (required for `var _
SuperchargerSessionAnalyticsReader = (*loggingSuperchargerSessionAnalyticsReader)(nil)`
to compile), as a silent pass-through with a comment stating there is no caller today —
the same shape tier 2 gave `analytics.Reader`'s three dashboard-only methods. If a
future caller appears, whether to log it is that change's decision to make, informed by
who the new caller is — exactly the reasoning tier 2's own D1 gives for not
pre-emptively deciding a hypothetical caller's logging needs now.

### D6 — Every method on `SessionWriter`, `MirrorWatermarkStore`, and `MonthlyCapacityCalculator` logs

These three ports have no off-path method to keep silent — mirroring tier 2's D2 for
`Recalculator`/`GapWriter`:

- `SessionWriter.MirrorSessions` — sole caller is `processChargingData`, nightly.
- `MirrorWatermarkStore.MirrorWatermark` / `.AdvanceMirrorWatermark` — sole caller is the
  same function, nightly.
- `MonthlyCapacityCalculator.Calculate` — sole callers are the nightly step-4 gate
  (once a month) and `cmd/monthly-capacity` (a manual CLI tool, not a web request).

Logging `Calculate` from this module's own decorator is not redundant with
`internal/app/processor.go`'s existing `callMonthlyCapacityCalculator` log line (which
predates this roadmap, from `RM52-app-add-monthly-capacity-step`): that line narrates
"the step ran, here is the outcome" at the orchestration level; this decorator's line is
the query-boundary record of the actual port call, the same relationship tier 2's own
`Recalculator`/`GapWriter` lines already have to `internal/app`'s per-vehicle narration
lines (tier 1). Neither replaces the other.

### D7 — Log line arguments and delegation order (Test Contract inputs)

All lines use the message topic `charging query:`. `tesla_id` prints via `%d` (`int64`).
An instant (`since`, `observed`, `cursor`) prints RFC3339 UTC, matching
`internal/telemetry/query_log.go`'s identical choice for the same kind of value. A
calendar-day window (`start`/`end`, `from`/`to`) prints `2006-01-02`, matching
`internal/analytics/query_log.go`'s identical choice — both `Reader`'s and
`SuperchargerSessionAnalyticsReader`'s `…Between` methods take whole UTC calendar days
per their own doc comments (`charging.go`).

| Type | Method | Delegates | Format | Args |
|---|---|---|---|---|
| `Reader` | `ListEntriesByVehicleUpdatedSince` | after (need `rows`) | `charging query: tesla_id=%d since=%s rows=%d` | `teslaID, since.UTC().Format(time.RFC3339), len(result)` |
| `SessionWriter` | `MirrorSessions` | before (write; line survives a failure) | `charging query: tesla_id=%d sessions=%d` | `firstTeslaID(sessions), len(sessions)` — see note below |
| `SuperchargerSessionAnalyticsReader` | `ListSessionsByVehicleBetween` | after (need `rows`) | `charging query: tesla_id=%d start=%s end=%s rows=%d` | `teslaID, from.UTC().Format("2006-01-02"), to.UTC().Format("2006-01-02"), len(result)` |
| `SuperchargerSessionAnalyticsReader` | `ListSessionsByVehicleUpdatedSince` | after (need `rows`) | `charging query: tesla_id=%d since=%s rows=%d` | `teslaID, since.UTC().Format(time.RFC3339), len(result)` |
| `MirrorWatermarkStore` | `MirrorWatermark` | after (the cursor value IS the point) | `charging query: tesla_id=%d cursor=%s` | `teslaID, cursor.UTC().Format(time.RFC3339)` |
| `MirrorWatermarkStore` | `AdvanceMirrorWatermark` | before (write; line survives a failure) | `charging query: tesla_id=%d observed=%s` | `teslaID, observed.UTC().Format(time.RFC3339)` |
| `MonthlyCapacityCalculator` | `Calculate` | after (need the report counts) | `charging query: period=%s tesla_id=%s found=%d measured=%d thin=%d` | `period.Format("2006-01"), scope, result.VehiclesFound, result.Measured, result.Thin` — `scope` is `"all"` when `teslaID == nil`, else the decimal vehicle id, matching `processor.go`'s own `period.Format("2006-01")` choice for this same value |

**`MirrorSessions`'s `firstTeslaID` helper:** the port's own doc comment
(`charging.go`) makes no promise that every `SessionMirror` in one call shares a single
vehicle, but `processChargingData` — its only caller — always builds `mirrored` from one
`teslaID`'s sessions per call. The decorator reads `sessions[0].TeslaID` when the slice
is non-empty and prints `0` when it is empty (an edge case `processChargingData` never
actually produces, since it `continue`s past a zero-row read before calling
`MirrorSessions` at all) — the log line still documents the actual usage pattern rather
than inventing a per-session breakdown nobody asked for.

Write methods (`MirrorSessions`, `AdvanceMirrorWatermark`) log BEFORE delegating,
mirroring `internal/telemetry/query_log.go`'s `loggingStore` and
`internal/analytics/query_log.go`'s `loggingRecalculator`/`loggingGapWriter` — their
arguments are known upfront, and the line must still appear if the write itself fails.
Read methods (`ListEntriesByVehicleUpdatedSince`, both `SuperchargerSessionAnalyticsReader`
methods, `MirrorWatermark`, `Calculate`) log AFTER, because the value worth recording
(`rows`, the cursor, the report counts) only exists once `inner` has returned.

### D8 — File layout and wiring, mirroring `internal/telemetry/query_log.go` and `internal/analytics/query_log.go`

One new file, `internal/charging/query_log.go`, holding all five decorators — one
cohesive feature ("instrument the module's nightly-path seams"), the same grouping
reason both precedent files give.

Each decorator:
- Implements its wrapped interface EXPLICITLY, never by embedding (roadmap Decision 2 —
  embedding would let a future interface method be satisfied silently by promotion, so
  that call would never be logged; explicit implementation turns a missed override into
  a compile error).
- Has exactly one field, `inner <Interface>`, set by an unexported `newLogging*(inner
  <Interface>) *logging*` constructor.
- Carries a compile-time assertion: `var _ Reader = (*loggingReader)(nil)`, `var _
  SessionWriter = (*loggingSessionWriter)(nil)`, `var _
  SuperchargerSessionAnalyticsReader = (*loggingSuperchargerSessionAnalyticsReader)(nil)`,
  `var _ MirrorWatermarkStore = (*loggingMirrorWatermarkStore)(nil)`, `var _
  MonthlyCapacityCalculator = (*loggingMonthlyCapacityCalculator)(nil)`.

Wiring — one line changed in each of five existing constructors, all in `charging.go`:

```go
// charging.go, NewReader — BEFORE:
func NewReader(pool *pgxpool.Pool) Reader {
	return newReader(pool)
}
// AFTER:
func NewReader(pool *pgxpool.Pool) Reader {
	return newLoggingReader(newReader(pool))
}
```

```go
// charging.go, NewSessionWriter — BEFORE:
func NewSessionWriter(pool *pgxpool.Pool) SessionWriter {
	return newSessionWriter(pool)
}
// AFTER:
func NewSessionWriter(pool *pgxpool.Pool) SessionWriter {
	return newLoggingSessionWriter(newSessionWriter(pool))
}
```

```go
// charging.go, NewSuperchargerSessionAnalyticsReader — BEFORE:
func NewSuperchargerSessionAnalyticsReader(pool *pgxpool.Pool) SuperchargerSessionAnalyticsReader {
	return newSessionReader(pool)
}
// AFTER:
func NewSuperchargerSessionAnalyticsReader(pool *pgxpool.Pool) SuperchargerSessionAnalyticsReader {
	return newLoggingSuperchargerSessionAnalyticsReader(newSessionReader(pool))
}
// NewSessionReader (the sibling constructor) is UNCHANGED — see D2/D3.
```

```go
// charging.go, NewMirrorWatermarkStore — BEFORE:
func NewMirrorWatermarkStore(pool *pgxpool.Pool) MirrorWatermarkStore {
	return newMirrorWatermarkStore(pool)
}
// AFTER:
func NewMirrorWatermarkStore(pool *pgxpool.Pool) MirrorWatermarkStore {
	return newLoggingMirrorWatermarkStore(newMirrorWatermarkStore(pool))
}
```

```go
// charging.go, NewMonthlyCapacityCalculator — BEFORE:
func NewMonthlyCapacityCalculator(pool *pgxpool.Pool) MonthlyCapacityCalculator {
	return newMonthlyCapacityCalculator(pool)
}
// AFTER:
func NewMonthlyCapacityCalculator(pool *pgxpool.Pool) MonthlyCapacityCalculator {
	return newLoggingMonthlyCapacityCalculator(newMonthlyCapacityCalculator(pool))
}
```

`NewWriter`, `NewSessionReader`, `NewSessionVerifier` are NOT touched — see D1/D2.

Because `cmd/poller/main.go`, `cmd/web/main.go`, and `cmd/monthly-capacity/main.go` all
call these five exported constructors directly (Context above), no `cmd/` file needs to
change — all three binaries get the decorated instances the next time they build.

### D9 — No behavior change; no credentials or raw payloads in scope

Every decorator's method body is: (optionally) log, then `return
l.inner.Method(...)` unchanged, or `result, err := l.inner.Method(...)` followed by
logging and `return result, err` unchanged. No decorator inspects, retries,
short-circuits, or transforms a result or error. A caller holding any of the five
decorated port values cannot observe any difference in return value, error, or side
effect from this change — the only observable difference is the new log lines.

**Credentials and raw payloads:** none of the ten in-scope methods (`Reader`'s one
logged method, `SessionWriter`'s one, `SuperchargerSessionAnalyticsReader`'s two,
`MirrorWatermarkStore`'s two, `MonthlyCapacityCalculator`'s one — five methods total
across five ports, plus the silent pass-throughs) takes a `tesla.Credentials`, a token,
or a raw vendor JSON payload as a parameter or returns one. `internal/charging` itself
imports no `internal/tesla` and holds no credential type anywhere in its public surface
(`internal/charging/AGENTS.md` §Allowed Imports / §Responsibility already states this
module "is isolated from the Tesla Fleet API"). The entire credential-leak risk this
rule guards against does not exist in this module, by construction — the same
conclusion tier 1's own header comment reaches for `internal/telemetry`'s
non-`call_counter.go` decorators.

### D10 — The nightly `ListEntriesByVehicleBetween` read is logged from the CALLER, in `internal/analytics`

**Decided by the user**, after reading D4's proposed gap.

D4 is right that this module cannot log the method selectively: `NewReader` is one
shared constructor, so a decorator here logs every caller alike, including two gateway
call sites that fire on every page render and every form submission.

But the call can be logged where it is *made*. `internal/analytics/recalculate.go` calls
`r.manual.ListEntriesByVehicleBetween` in exactly one place, inside `Recalculate`.
Verified by grep, `Recalculate` has three callers and none is a page-load GET:

| Caller | When it runs |
|---|---|
| `internal/analytics/recalculate.go` — `Reconcile` | nightly |
| `internal/gateway/handlers/external_charges.go` | after a manual charge is saved |
| `internal/gateway/handlers/supercharger.go` | after a session battery percentage is edited |

So a log line at that call site fires on the nightly path and after a write, never on a
page render. The two high-frequency gateway readers of
`ListEntriesByVehicleBetween` go straight to `charging.Reader` and never pass through
`Recalculate`, so they stay silent exactly as D4 requires.

**Shape:** one `logging.Note` call in `recalculate.go`, immediately after the read
returns, so it can report the row count:

```
logging.Note("Recalculator", "Recalculate",
    "manual entries read: tesla_id=%d start=%s end=%s rows=%d",
    teslaID, chargeStart.UTC().Format("2006-01-02"), end.UTC().Format("2006-01-02"), len(entries))
```

**Why the topic is `manual entries read:` and not `charging query:` or `analytics
query:`.** Those two topics are the output of the two modules' decorators. This line is
neither — it is emitted from business logic in `internal/analytics` about a read from
`internal/charging`. Giving it either topic would make a grep for that topic return a
line the decorator did not produce. A distinct phrase keeps every topic honest and stays
greppable.

**Known cost, stated plainly:** this is the only log line in the three RM62 logging tiers
that does not live in a `query_log.go` decorator. Someone looking for "where does this
module log" will find `internal/analytics/query_log.go` and miss this one. The user chose
this over a second decorator type in `internal/analytics` after being shown both, on the
grounds that one line is cheaper than a type plus a wiring change.

**Module ownership:** this edit belongs to `internal/analytics`, not `internal/charging`.
It is carried in this tier by an explicit path grant to the worker rather than a separate
tier, to avoid a second full artifacts-and-review cycle for one line. That grant is
recorded in tasks.md.

## Test Contract — expected `cmd/poller --once` log addition for one vehicle

Authored before the implementation exists, per `ai/go-conventions.md` "contract-first
authoring." This is the acceptance condition the owner checks by eye (no automated test
enforces it — see "Tests excluded" below).

Assumes tiers 1 and 2 of this roadmap have already landed (they have). For one vehicle,
`tesla_id=3744325961659064`, a nightly run where step 2's mirror watermark starts at
`2026-09-16T03:00:00Z`, finds 2 new sessions with a maximum `updated_at` of
`2026-09-17T02:55:00Z`, and step 3's `Reconcile` (already logging since tier 2) finds new
Supercharger data and re-derives a window:

```
[Processor] [processChargingData] vehicle 3744325961659064: 2 session(s)
[MirrorWatermarkStore] [MirrorWatermark] charging query: tesla_id=3744325961659064 cursor=2026-09-16T03:00:00Z
[SessionWriter] [MirrorSessions] charging query: tesla_id=3744325961659064 sessions=2
[MirrorWatermarkStore] [AdvanceMirrorWatermark] charging query: tesla_id=3744325961659064 observed=2026-09-17T02:55:00Z
[Recalculator] [Reconcile] analytics query: tesla_id=3744325961659064
[SuperchargerSessionAnalyticsReader] [ListSessionsByVehicleUpdatedSince] charging query: tesla_id=3744325961659064 since=2026-09-16T02:55:00Z rows=2
[Reader] [ListEntriesByVehicleUpdatedSince] charging query: tesla_id=3744325961659064 since=2026-09-16T02:55:00Z rows=0
```

`processChargingData`'s own summary line (`vehicle %d: %d session(s)`, already existing,
unchanged by this tier) still prints AFTER the mirror completes — this tier's new lines
appear in between the watermark read and that summary. `MirrorWatermark` logs before
`MirrorSessions`/`AdvanceMirrorWatermark` because `processChargingData` reads the cursor
first, in that order (`internal/app/processor.go`).

`ListEntriesByVehicleUpdatedSince`'s line appears only if `Reconcile`'s call to it
actually runs — it always does, alongside the Supercharger read, as part of
`Reconcile`'s own three-source fetch. `rows=0` here means no manual charge entries
changed since the watermark; a non-zero count would show the same line with a higher
`rows` value.

On the first day of a month, step 4 additionally produces:

```
[MonthlyCapacityCalculator] [Calculate] charging query: period=2026-08 tesla_id=all found=3 measured=2 thin=1
[Processor] [callMonthlyCapacityCalculator] monthly capacity: period 2026-08: 3 vehicle(s) found, 2 measured, 1 thin
```

(the `[Processor]` line already exists, unchanged; the `[MonthlyCapacityCalculator]`
line is new, and appears immediately before it, mirroring how every other decorated
port's line sits immediately before or after its own `[Processor]` narration line).

**What does NOT appear, and why:** no `[Reader] [ListEntriesByVehicleBetween]` line and
no `[SuperchargerSessionAnalyticsReader] [ListSessionsByVehicleBetween]` line for a
`Recalculate` call triggered by `Reconcile` above — both stay silent by design (D3/D4).
The vehicle and window are still visible on the `[Recalculator] [Recalculate] analytics
query: tesla_id=... start=... end=...` line tier 2 already prints for that call. No
`[Writer]`, `[SessionReader]`, or `[SessionVerifier]` line ever appears in a poller log
— those ports carry no nightly caller at all (D1/D2).

## Tests excluded (roadmap Decision 1)

This change adds no test file. The roadmap's own reasoning: the compile-time assertion
per decorator (`var _ Reader = (*loggingReader)(nil)`, etc.) already guarantees every
decorated port's methods are implemented — a method added later without a matching
override is a build error, not a silent gap. There is no formatting logic complex enough
to warrant a characterization test beyond that; `internal/logging.Note`'s own test
(`internal/logging/logging_test.go`) already verifies the `[Type] [Method] message`
format every new call here reuses unchanged.

What this leaves uncovered: whether each decorator's arguments actually match this
design's table (D7) — nothing catches a typo'd field name or a swapped argument order at
build time, only at read time. The owner's manual `cmd/poller --once` smoke check
against the Test Contract above is the verification signal, the same posture tier 2 took.

## Database

**None.** This change touches no table, column, index, constraint, view, or migration.
`internal/charging` owns `manual_charge_entries`, `supercharger_sessions`,
`monthly_effective_capacity`, and `mirror_watermarks`
(`internal/charging/AGENTS.md` §Data Ownership) and this change does not alter that
ownership or any column.

## Knowledge base

Checked `kkpa/context/architecture/nightly-cycle.md` for a fact this change makes false.

It already documents step 2's and step 4's port maps
(`charging.SessionWriter.MirrorSessions`, `charging.MonthlyCapacityCalculator.Calculate`,
`charging.MirrorWatermarkStore`) and step 3's charging-sourced reads
(`charging.SuperchargerSessionAnalyticsReader`, `charging.Reader`) — none of these
change: no port signature, return type, or caller changes in this tier. It also already
has a "Conventions & gotchas" bullet block for tier 2's own analytics query logging
(the three bullets starting "`internal/analytics` logs its own queries, but only on the
nightly path" through "Do not add an interface field to a type just to make a self-call
re-enter its own wrapper"). It has NO equivalent bullets for `internal/charging` yet —
this change makes the guide incomplete, not wrong, once implemented.

**Task 4 of this change's `tasks.md` adds the missing bullets** (mirroring tier 2's own
three-bullet shape: which ports/methods log, which stay silent and why, and the
explicit-implementation/compile-time-assertion rule) to
`kkpa/context/architecture/nightly-cycle.md`'s "Conventions & gotchas" section, and
checks whether the "Rendered view" Artifact needs a republish note. This follows
`CLAUDE.md`'s "Docs track structural change" rule (the KB is explicitly in scope) and
mirrors how tier 2 handled the identical situation for its own module.
