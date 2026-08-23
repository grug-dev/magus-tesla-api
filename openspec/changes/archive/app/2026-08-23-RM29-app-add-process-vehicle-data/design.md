# Design — RM29-app-add-process-vehicle-data

> Numbering note: this document's decisions are **D1, D2, …**, scoped to this change
> only — distinct from the roadmap's own **D1–D10** in
> `openspec/roadmaps/RM29-modular-monolith-boundaries.md` (cited as **roadmap D2**, …)
> and from this tier's own pre-artifacts interview outcomes, settled with the owner and
> handed to this artifacts pass as **RD2–RD8** (cited that way throughout). **D1–D8 each
> carry exactly one RD outcome** (stated in their heading) and transcribe it — they are
> not re-litigated. **D9–D13 are decisions this artifacts pass had to make** to turn
> RD2–RD8 into a buildable, testable change; each says so in its heading so the owner and
> reviewer can weigh them separately from the settled interview.
>
> **Amendment (2026-08-22): RD8 supersedes RD5.** The owner overturned RD5 after the
> first artifacts pass. **D4 was rewritten in place** to carry RD8 (`Scheduler` moves to
> `internal/app`, not `cmd/poller`, and its four tests move with it); **D3, D11, D12**,
> the Context, the Goals, the Test Contract and the Risks list were reconciled with it;
> and **D13** was added for the clock-seam question the relocation raises. No decision
> was renumbered. Nothing below still asserts the RD5 plan.

## Context

Roadmap violation #3: `cmd/poller`'s `reconcilingCollector` — plus the two closures it
wraps, `newSessionMirrorer` (added by T6) and `newNightlyReconciler` (added by T3) — is
business orchestration living in a `cmd/` binary, because there was nowhere else to put
it. It hand-rolls exactly the `ProcessVehicleData` = sync + process-charging +
recalculate split the roadmap's own diagram calls for:

```
Scheduler ──┐
            ├──> ProcessVehicleData ──┬── Sync Fleet data       (telemetry)
API ────────┘                         ├── Process Charging data (the T6 mirror)
                                      └── Recalculate Analytics (analytics)
```

Roadmap **D2** originally assigned this new module ownership of `poll_attempts` too,
renamed to `process_runs`, with a `triggered_by` column. **The pre-artifacts interview
revisited that call and reversed it** — see D1 below, which is the load-bearing decision
this whole design pivots on: `internal/app` ends up owning **no schema at all**.

Two facts about the existing code shape everything below.

1. **`cmd/poller` has no tests, and the project has accepted that as permanent for
   composition-root wiring.** RM29 tier 3's review caught a `Reconcile` call site that
   was never written while `go build`, `go vet` and the full suite all stayed green —
   documented in the roadmap file as "composition-root wiring in `cmd/` is invisible to
   every automated signal this project has; it needs reading, not trusting." Every tier
   since has treated the owner's own `cmd/poller --once` run as the acceptance signal
   for wiring changes, not the test suite. This tier inherits that norm for the wiring
   it leaves in `cmd/poller` and for the three orchestration steps it moves (D12). It
   is **background, not a justification for dropping any coverage**: under RD8 no test
   is lost in this tier (D4), and this fact is the reason the scheduler must *not* land
   in `cmd/poller` — code that lands there becomes invisible to every automated signal
   this project has.
2. **`telemetry.Scheduler` and `cmd/poller`'s post-cycle closures are two different
   kinds of "orchestration."** `Scheduler` decides **when** to run (a clock concern);
   `reconcilingCollector`/`newSessionMirrorer`/`newNightlyReconciler` decide **what a run
   does** (a use-case concern). This tier's central move is separating those: the *what*
   becomes `internal/app`'s `Processor` port; the *when* stays a **driving adapter** —
   it never becomes part of the port — and its source relocates into `internal/app`
   alongside the port it drives, because `cmd/` may hold no business logic
   (`CLAUDE.md` §Non-negotiables) and 131 lines of timer/date math is business logic.
   See D3 (the composition, unchanged) and D4 (the location, settled by RD8).

## Goals / Non-Goals

**Goals**

- A new `internal/app` module exposes exactly one port, `Processor.ProcessVehicleData`,
  composing three named steps in a fixed order, with the exact per-step error isolation
  `cmd/poller` already has today (D3, D6, D8).
- `internal/telemetry` gains no new *behavior*, only a widened `Collector` signature and
  two new `poll_attempts` columns it already owned the table for (D1, D5, D7).
- `cmd/poller` becomes **wiring only**: it constructs `app.NewProcessor(...)` and
  `app.NewScheduler(...)` and calls `Run` (or, for `--once`, `ProcessVehicleData`
  directly). It stops deciding *what* a run does **and** stops holding the timer/date
  logic that decides *when* — both now live in `internal/app` (D4, RD8).
- Zero behavior change to any existing log line, error-isolation rule, or the two moved
  steps' bodies — this is a relocation, not a redesign (D6, D8, D10).
- `internal/app` owns no table, no migration, no pool (D1, D2).

**Non-Goals**

- Renaming, moving, or restructuring `poll_attempts` beyond the two added columns
  (roadmap D2, superseded — D1).
- The manual-rerun HTTP API (tier 8, parked, roadmap D9) — `internal/app`'s port exists
  now so tier 8 has something to call, but tier 8's transport, auth and overlap
  protection are out of scope here.
- Any change to `internal/charging` or `internal/analytics` Go code. Both are consumed
  through their existing public ports, unchanged.
- Recovering `run_id` for any row written before this migration (D7).
- A new unit-test suite for `internal/app`'s three **orchestration steps** (D12). Note
  this is not the same as "`internal/app` has no tests": the module does ship
  `scheduler_test.go`, but those four tests are **pre-existing coverage relocating with
  its code** (D4), not new coverage invented for this tier.

---

## Decisions

### D1 — `poll_attempts` stays in `telemetry`; this SUPERSEDES roadmap D2 (carries RD2)

Roadmap **D2** said: *"New `internal/app` module owns `ProcessVehicleData` /
`SyncVehicleData` / `RecalculateVehicleData` **and** the run log. `poll_attempts` moves
there as `process_runs`, gaining `triggered_by` (`scheduler` | `api`). Answers the
ticket's open question: not `telemetry`, because `triggered_by` is not something Tesla
reported."*

**This tier's interview revisited that reasoning and found it does not hold.** D2's own
justification is a purity test — "not something Tesla reported" — and that test is the
right one for `vehicle_snapshots` (roadmap violation #1, already fixed by tier 4:
`vehicle_snapshots` now holds only what Tesla reported, with the five derived columns
moved to `analytics`). It is **the wrong test for `poll_attempts`**, because
`poll_attempts` never held anything Tesla reported in the first place:

| Column | What it records | Whose fact is it? |
|---|---|---|
| `attempted_at` | when *we* made the attempt | our clock |
| `outcome` | success/failure of *our* API call | our classification |
| `reason` | *our* diagnosis of why (asleep-timeout, unauthorized, api-error, ok) | our classification |
| `account_id`, `tesla_id` | which of *our* accounts/vehicles | our own identifiers |

Every existing column is already "our own fact about our own attempt." `triggered_by` —
which invocation caused this attempt — is exactly the same *kind* of fact: metadata
about our own process, not a purity violation. Adding it to `poll_attempts` extends a
table that was never about "what Tesla said" to begin with; it does not blur a boundary
that table ever had.

**Consequence: `internal/app` owns zero schema.** No migration directory, no `db/`
package, no `sqlc.yaml` entry. This is the single fact that makes this tier's
`internal/app` unusually small compared to every other RM29 tier, and it is why the
Database Changes section below lives entirely inside `internal/telemetry`'s existing
migration directory.

**Rejected — carry out roadmap D2 as written (`process_runs` in `app`).** Two costs that
buying nothing: (a) it requires a data migration copying every historical
`poll_attempts` row into a new table, or leaving history split across two tables forever;
(b) it gives `internal/app` a database dependency and a `db/` package purely to hold a
log table nothing in this tier reads back structurally (the only consumer of run
correlation is a future operator `SELECT ... GROUP BY run_id`, not application code) —
exactly the kind of table-for-its-own-sake the AI-efficiency "do not over-abstract" rule
warns against paying for.

### D2 — Grain is unchanged: one row per (vehicle, run); `run_id` correlates (carries RD3)

`poll_attempts` keeps its existing grain — **one row per (vehicle, run)** — the existing
schema comment at `internal/telemetry/db/migrations/20260710000002_init_telemetry.sql:49`
already says exactly this, and this tier does not touch that comment's truth. No new
run-level table is added. A caller wanting run-level facts (e.g. "how many vehicles did
run X touch, and how many succeeded") computes them with `GROUP BY run_id` over the
existing table.

**Why this preserves telemetry's own D5 signal.** `poll_attempts` doubles as the
per-vehicle sleep-behavior / availability signal (design D5 of the tier-3 telemetry
change): a daily collapse to one row per vehicle would destroy that. Adding `run_id` as a
correlating column, rather than replacing the grain with a run-level summary, keeps every
existing row and every existing read pattern intact — this tier adds a column, not a
redesign.

**Rejected — a separate run-level summary table** (one row per run, with
attempted/succeeded/failed counts). Rejected because nothing in this tier reads it: the
only consumer of "how did run X go" today is the log line `telemetry.LogCycle` already
emits from the in-memory `CycleReport` at the end of the very call that produced it —
there is no persisted-then-later-read use case yet. Building the summary table
speculatively repeats the mistake T6's D9 explicitly declined to make for
`charge_sessions`' reader port ("it would have no caller in this tier, so none is
built").

### D3 — `ProcessVehicleData` has three named steps; Scheduler and the API are peers that CALL it (carries RD4)

The `Processor` port has exactly one method, and its body runs exactly three named
steps, in this fixed order:

1. **Sync fleet data** — `telemetry.Collector.CollectAll`.
2. **Process charging data** — the Supercharger-session mirror (T6's mechanism, moved
   wholesale — D6).
3. **Recalculate analytics** — `analytics.Recalculator` + gap reconciliation (moved
   wholesale — D8).

This is a **three**-step split, not roadmap D2's "sync + recalculate" two-step
shorthand — the roadmap's prose undercounted the mirror step T6 had already built by the
time this tier was designed. The scheduler and the future manual-rerun API are **peer
driving adapters that CALL `ProcessVehicleData`**, exactly like the port diagram shows —
neither is *inside* the use case, and the use case has and needs no knowledge of "when"
or "over HTTP".

**Reconciling RD4 with RD8 — the composition is unchanged; only a source location
moved.** RD8 (D4) puts `Scheduler`'s *source file* in `internal/app`, which is the same
package that declares the port it drives. That is a package-location fact, not a
composition fact, and RD4 still binds in full: `Processor` has **no `Scheduler` field**
and never will; `ProcessVehicleData` **never consults a clock** and never learns what
triggered it beyond the `telemetry.TriggeredBy` value its caller hands in; `app.Scheduler`
holds a `Processor` and calls it from the outside, exactly as `cmd/poller` did. It is a
driving adapter that merely happens to be co-located with the port it drives, and
`cmd/poller` still constructs it and starts it — the wiring direction
(`cmd/poller → app.NewScheduler → Processor`) is identical to what RD4 described.

**The cost, stated honestly.** The two peers are no longer symmetric in *location*: the
tier-8 API adapter, when it is eventually built, will live in its own `cmd/` binary (or
the gateway), not beside the scheduler. So a reader will find one driving adapter inside
`internal/app` and the other outside it. The owner accepted that asymmetry deliberately:
the `cmd/` rule — *"`cmd/` stays thin (zero business logic)"* (`CLAUDE.md`
§Non-negotiables) — is **written**, while the symmetry of the two adapters' folders is
**taste**. A written rule outranks taste. If tier 8 ever wants the symmetry back, the
cheap move at that point is to bring the API's *driving* logic in too — not to push the
scheduler's date math back into a `cmd/`.

### D4 — `Scheduler` moves `telemetry` → `internal/app`, NOT `cmd/poller`; its four tests move with it (carries **RD8**, which SUPERSEDES RD5)

> **This decision was reversed by the owner (RD8, 2026-08-22T16:10:00Z) after the first
> artifacts pass.** RD5 — which the original D4 transcribed — sent `Scheduler` to
> `cmd/poller` and deleted its four tests. RD8 overturns that in both halves. The
> reversal is recorded here in full, including the leader's own corrected framing of the
> argument it originally used, because this project records reversals rather than
> glossing them.

**What moves.** `internal/telemetry/scheduler.go`'s `Scheduler` struct, `NewScheduler`,
`Run` and the pure `nextRun` function are **relocated** out of `internal/telemetry` into
a new file `internal/app/scheduler.go` (`package app`), **essentially verbatim** —
same fields, same doc comments (with `Collector` reworded to `Processor` where the prose
names the port), same timer/select/graceful-shutdown structure, same pure `nextRun` math.
There is **exactly one substantive change**, in `Run`'s tick handler:

```go
// before (internal/telemetry/scheduler.go)
report, err := s.collector.CollectAll(ctx)
LogCycle(report, err)

// after (internal/app/scheduler.go)
report, err := s.processor.ProcessVehicleData(ctx, telemetry.TriggeredByScheduler)
telemetry.LogCycle(report, err)
```

`LogCycle` is still called by the scheduler, immediately after the call returns, with the
same arguments in the same order — so the log line and its position relative to the
cycle's other output are unchanged (D11, Test Contract C1). `internal/telemetry.Collector`
stays a pure port: nothing about *when* to call it lives inside `telemetry` any more, and
`telemetry` gains no import of `internal/app` (the dependency runs the other way, which is
the direction that was already legal).

**Why not `cmd/poller` — the owner's reasoning.**

1. **It would violate a written non-negotiable.** `CLAUDE.md` §Non-negotiables: *"`cmd/`
   stays thin (zero business logic)."* `scheduler.go` is 131 lines of timer, date and
   DST-aware rollover logic. Moving it into `cmd/poller` puts business logic into `cmd/`
   **in the very tier whose entire purpose is removing business logic from that file** —
   the change would delete `reconcilingCollector`, `newSessionMirrorer` and
   `newNightlyReconciler` from `cmd/poller` and then hand it back a comparable volume of
   logic through the other door. Net progress on roadmap violation #3 would be close to
   zero for that file.
2. **The leader's coverage argument for RD5 was overstated, and is corrected here.** The
   first artifacts pass partly sold RD5 on "`cmd/poller` has no tests, so the scheduler
   loses coverage — accept it." That framing was wrong on the facts: **Go permits
   `_test.go` files in a `main` package**, so the four tests could have moved to
   `cmd/poller` either way. Coverage was never the thing forcing the choice. The real
   cost of RD5 was the `CLAUDE.md` violation in (1), and the leader under-weighted it
   while over-weighting a constraint that did not exist.
3. **Symmetry with tier 8 is taste; the `cmd/` rule is written.** RD5's remaining
   argument was that the scheduler belongs wherever the future API trigger will live, so
   the two driving adapters sit side by side. That symmetry is a real (if mild) good, but
   tier 8 is parked and undesigned — its location is a guess — and a guessed aesthetic
   does not outrank a written rule. D3 states the accepted asymmetry explicitly.

**The coverage story inverts: nothing is lost.** Under RD5 the four
`TestNextRun`/`TestScheduler_*` tests were to be deleted. Under RD8 they are
**preserved**, moving into `internal/app/scheduler_test.go` alongside the code they
cover — a pure file relocation of pre-existing coverage, with their assertions unchanged
(the Test Contract's group S states the seam adaptation their collector stubs need). This
tier therefore ends with **no automated-coverage regression at all**. Any statement to
the contrary elsewhere in this change's artifacts is a leftover from RD5 and is a defect.

**Rejected — (a) move `Scheduler` to `cmd/poller` (RD5, what RD8 supersedes).** Rejected
on the `CLAUDE.md` "`cmd/` stays thin (zero business logic)" rule, and on the plain
contradiction of emptying `cmd/poller/main.go` of orchestration in the same change that
refills `cmd/poller` with timer/date logic (task 4.1 exists to *empty* that file). Its
one genuine merit — location symmetry with the parked tier-8 API adapter — is taste
against a written rule.

**Rejected — (b) leave `Scheduler` in `internal/telemetry`, calling `app.Processor`
instead of `telemetry.Collector`.** Rejected for the same two reasons as before RD8, both
untouched by the reversal: it leaves a *timing* mechanism inside a *data-collection*
module, where it only ever sat because of what it happened to be named after; and
`telemetry` would have to import `internal/app` to name the `Processor` type, inverting
the dependency direction this tier establishes (`app → telemetry`) and putting
`telemetry` above the application layer that composes it. Once the scheduler calls the
`Processor` port rather than `Collector`, it has no remaining reason to live in
`telemetry`.

### D5 — `Collector.CollectAll` widens to accept a `RunContext`; the type is `telemetry`'s (carries RD6a)

```go
package telemetry

type TriggeredBy string

const (
	TriggeredByScheduler TriggeredBy = "scheduler"
	TriggeredByAPI       TriggeredBy = "api"
)

type RunContext struct {
	RunID       uuid.UUID
	TriggeredBy TriggeredBy
}

type Collector interface {
	CollectAll(ctx context.Context, run RunContext) (CycleReport, error)
}
```

**Why `telemetry` owns the type, not `app`.** `telemetry` owns the `poll_attempts`
column these values are stamped into (D1); the project's own precedent is that the
module owning a column owns the Go type that fills it (mirrors `charging.SessionMirror`
carrying exactly `charge_sessions`' mirrorable columns, T6 D6). `internal/app` **consumes**
the type — it generates a fresh `RunID` once per invocation
(`uuid.New()`) and builds the `RunContext` to pass down; it declares neither the type nor
its constants.

**Threading, not service state.** `record()` (`service.go:456`) is the single place that
stamps `RunID`/`TriggeredBy` onto every `Attempt` it writes. It is called from four sites
inside `collectAccount` (`service.go:181,198,210,217`), all of which already receive
`*CycleReport` as a parameter. `run RunContext` is threaded the same way — as a plain
parameter on `CollectAll → collectAccount → record` — **never stored as a field on
`*service`**. `*service` is a long-lived object built once in `cmd/poller` and reused
across every scheduled cycle; storing the current run's identity as mutable state on it
would make a concurrent or future overlapping call silently attribute one run's attempts
to another's `RunID` the moment two calls interleaved. A parameter cannot do that by
construction.

**`Attempt` gains two fields:**

```go
type Attempt struct {
	AccountID   uuid.UUID
	TeslaID     int64
	AttemptedAt time.Time
	Outcome     Outcome
	Reason      Reason
	RunID       uuid.UUID   // NEW — the RunContext.RunID this attempt was written under
	TriggeredBy TriggeredBy // NEW — the RunContext.TriggeredBy this attempt was written under
}
```

Both are plain (non-pointer) on the **write** side: every `Attempt` `record()` builds
carries a real `RunContext` supplied by its caller (ultimately generated by
`internal/app`), so there is never a legitimate reason to write a zero `RunID` or an
empty `TriggeredBy`. The **column**, however, is nullable for `run_id` (D7 — legacy rows
have none) and `NOT NULL DEFAULT 'scheduler'` for `triggered_by`. Whether that asymmetry
requires a nullable Go type on any *read* path (a query that maps a `poll_attempts` row
back into `Attempt`, if the module ever exposes one) is a per-query question the
`telemetry` worker resolves against whatever sqlc actually generates for the nullable
`run_id` column — flagged here rather than asserted, mirroring how T6's D8(b) treated an
sqlc-generation question it could not answer without running the tool.

### D6 — The Supercharger-session mirror moves `cmd/poller` → `internal/app` wholesale (carries RD6b)

`cmd/poller/main.go`'s `newSessionMirrorer` (its full body: the per-account
deduplication over `acct.AllRegisteredVehicles`, the
`superchargerReader.SuperchargerSessionsByAccount(ctx, accountID, 0)` read, the
eleven-field `telemetry.SuperchargerSession` → `charging.SessionMirror` mapping, and the
`sessionWriter.MirrorSessions` call with its per-account log-and-continue error handling)
relocates into `internal/app` as the "process charging data" step, unmodified in
substance. This is exactly what T6's own design.md D7 already flagged: *"the
orchestration... moves into `internal/app` wholesale at tier 7."*

**Both sides' boundary rules are unchanged.** `internal/charging` keeps its declared
`MUST NOT import internal/telemetry` rule
(`internal/charging/AGENTS.md`); `internal/telemetry` still does not know `internal/charging`
exists. Neither module's Go code changes at all — only the caller composing their two
ports moves one level up, from `cmd/poller` to `internal/app`. The mapping itself stays
**field-name-for-field-name, no renames, no derivation** — the same property T6's D1
naming decision bought so the mapping stays checkable by inspection.

**One reference goes stale and must be corrected in this change, not later.**
`internal/charging/AGENTS.md`'s forbidden-imports section currently reads: *"the mirror's
data arrives already mapped, from `cmd/poller` (the composition root)."* Once this tier
lands, that sentence is wrong — the data now arrives from `internal/app`. This is a
one-sentence fix inside `internal/charging`'s own doc file; see tasks.md for its owner.

### D7 — Database Changes are carried by `telemetry`'s own migration directory (carries RD7 — CONFIRMED BY THE OWNER, see §Database Changes)

The full schema, rationale and index plan are in §"Database Changes (design gate)"
below, per `openspec/config.yaml`'s rule that any DB-touching change carries this section
in full. Summary: `ALTER TABLE poll_attempts ADD COLUMN run_id UUID, ADD COLUMN
triggered_by TEXT NOT NULL DEFAULT 'scheduler'` in
`internal/telemetry/db/migrations/`. No CHECK (the typed `TriggeredBy` constant guards
every value this codebase writes). No index (nothing reads `run_id` or `triggered_by` in
this tier). `run_id` nullable and permanently unbackfilled for legacy rows.
`triggered_by`'s `NOT NULL DEFAULT 'scheduler'` is itself the backfill for existing rows,
in one metadata-only statement, and is correct because no non-scheduler entry point
existed before this tier. `cmd/poller --once` records `scheduler` (the owner does not
need it distinguishable from the nightly path; it is a testing tool).

---

### D8 — Step ordering and the whole-cycle short-circuit are preserved verbatim (mine — buildability)

`ProcessVehicleData`'s body reproduces `reconcilingCollector.CollectAll`'s exact control
flow:

```go
func (p *processor) ProcessVehicleData(ctx context.Context, triggeredBy telemetry.TriggeredBy) (telemetry.CycleReport, error) {
	run := telemetry.RunContext{RunID: uuid.New(), TriggeredBy: triggeredBy}

	report, err := p.collector.CollectAll(ctx, run) // step 1 — sync fleet data
	if err != nil {
		return report, err // whole-cycle failure: steps 2 and 3 never run
	}

	p.processChargingData(ctx)   // step 2 — was newSessionMirrorer
	p.recalculateAnalytics(ctx)  // step 3 — was newNightlyReconciler

	return report, nil
}
```

Both preserved properties are load-bearing, not incidental:

- **The short-circuit.** If step 1's whole-cycle enumeration fails (e.g.
  `account.AllRegisteredVehicles` itself errors), steps 2 and 3 are skipped entirely —
  exactly `reconcilingCollector.CollectAll`'s existing behavior, and for the same reason:
  both later steps read data step 1 was supposed to have just written; running them
  against a cycle that never happened would reconcile against stale or absent input.
- **The order.** Charging-data processing (step 2) runs **before** analytics
  recalculation (step 3) — the same order T6's own design.md D7 established ("propagate
  data, then derive from it"). Nothing reads `charge_sessions` yet (T6 D9), so the order
  is not yet load-bearing in the same sense it was there, but it is the order to
  establish now for the same reason T6 gave: the cheap time to fix an order is before
  anything depends on it, not in the change that adds the first dependent.
- **Per-step isolation is untouched.** `processChargingData`'s per-account
  log-and-continue and `recalculateAnalytics`'s per-vehicle log-and-continue (including
  the existing rule that a vehicle whose `Reconcile` fails is skipped for its gap step
  too, rather than falling through to it) move with their bodies, unmodified.

### D9 — `internal/app` depends on `internal/account`'s public port, alongside `telemetry`/`charging`/`analytics` (mine — buildability)

Both moved steps need `account.Service.AllRegisteredVehicles` to enumerate accounts and
vehicles — exactly as `newSessionMirrorer` and `newNightlyReconciler` do today. Since
`internal/app` sits **above** every domain module in the call graph (its only callers are
`cmd/poller` today and, later, a `cmd/api` — never a sibling domain module) and
`internal/account` sits at the **bottom** of the graph already (every domain module that
needs identity/tokens already depends on it, and it depends on nothing this roadmap
touches), `app → account` is a forward, one-way dependency with no cycle risk
(`ai/architecture.md` §"Dependency direction"). `internal/app/AGENTS.md` therefore
declares four allowed imports, not three: `internal/telemetry`, `internal/charging`,
`internal/analytics`, and `internal/account` — all consumed through their public ports
only, exactly as every other domain module in this project consumes its dependencies.

### D10 — `NewProcessor`'s ports; no `*pgxpool.Pool` anywhere in `internal/app` (mine — buildability)

```go
package app

// Processor is the platform's application-layer port — see the package doc comment
// for the three-step diagram (design.md D3).
type Processor interface {
	// ProcessVehicleData runs one full cycle for triggeredBy. See design.md D5, D8.
	ProcessVehicleData(ctx context.Context, triggeredBy telemetry.TriggeredBy) (telemetry.CycleReport, error)
}

// NewProcessor builds a Processor from its collaborators' PUBLIC PORTS only — every
// argument is an interface, not a *pgxpool.Pool or a concrete DB-backed type.
// internal/app owns no table and no pool (design.md D1/D2): every read and write this
// use case performs happens through one of these seven arguments.
func NewProcessor(
	collector telemetry.Collector,
	superchargerReader telemetry.SuperchargerReader,
	sessionWriter charging.SessionWriter,
	acct account.Service,
	recalculator analytics.Recalculator,
	analyticsReader analytics.Reader,
	gapWriter analytics.GapWriter,
	loc *time.Location,
) Processor
```

`loc` is required for the exact same reason `newNightlyReconciler` required it today: the
gap-reconciliation window's "yesterday" is resolved in the poller's own configured zone,
not UTC (roadmap D6/D18) — `internal/app` needs no `*time.Location` of its own beyond
this passthrough, mirroring `internal/analytics`' existing zone-free design.

### D11 — `LogCycle`/`formatFailures` stay in `telemetry`; `internal/app` never logs the cycle summary itself (mine — buildability)

`LogCycle` and `formatFailures` (`scheduler.go:88-117` today) format and print a
`telemetry.CycleReport` — they have no dependency on `Scheduler` and **do not travel with
it** to `internal/app` (D4). They relocate into a new file,
`internal/telemetry/report.go`, since their old home (`scheduler.go`) leaves this module
entirely. `internal/app` calls the exported `telemetry.LogCycle` across the module
boundary, exactly as `cmd/poller`'s `--once` path already does today — this is why
`LogCycle` was exported in the first place (see its own doc comment: "so cmd/poller
(one-shot mode) and Scheduler.Run share the exact same report formatter").

`internal/app`'s `ProcessVehicleData` **does not call `LogCycle`**. It returns the
`telemetry.CycleReport` to its caller unchanged (D7's decision to reuse the type rather
than wrap it is what makes this possible with zero translation). Every caller of
`Processor.ProcessVehicleData` is responsible for logging the report if it wants to,
exactly mirroring today's split: `internal/app`'s relocated `Scheduler` calls
`telemetry.LogCycle(report, err)` inside `Run`'s tick handler after each
`ProcessVehicleData` call (the same position `Scheduler.Run` called it from before the
move — D4), and the existing `--once` path — still in `cmd/poller`, unmoved — switches
its one call from `collector.CollectAll(ctx)` to
`processor.ProcessVehicleData(ctx, telemetry.TriggeredByScheduler)` and keeps its
existing `telemetry.LogCycle(report, err)` call verbatim.

**Why not have `internal/app` log internally.** A use-case port that prints to `log`
as a side effect of every call is harder to reuse from a future caller that wants the
report for something other than a log line (e.g. tier 8's API, which will presumably want
to turn the report into an HTTP response body, not stdout). Keeping `ProcessVehicleData`
a pure function of its inputs (mutations aside) to its returned report is what lets both
`cmd/poller` and the future API decide independently what to do with the result.

### D12 — No new unit-test suite for `internal/app`'s three orchestration steps; the honest accounting of why (mine — buildability, per roadmap D10; AMENDED for RD8)

> **Scope clarification added with RD8.** This decision covers **only the three
> orchestration steps** (D6, D8). It is **not** a claim that `internal/app` is a
> test-free package: since RD8 the module also ships `internal/app/scheduler_test.go`
> with the four relocated `Scheduler`/`nextRun` tests (D4). Those are **pre-existing
> coverage moving with its code**, not new coverage invented for this tier, so they sit
> outside roadmap D10's "no new unit tests for new code" bar rather than violating it.
> The distinction matters at review: an *accepted gap* (the three steps) and a
> *violation* (a deleted test) look identical in a coverage delta and are completely
> different facts. There is no deleted test in this tier.

`internal/app`'s three orchestration pieces — the top-level `ProcessVehicleData` control
flow (D8), the charging-mirror step (D6) and the analytics-reconcile step (D8) — are
**pure relocations of code that has never had a unit test**, because all three lived in
`cmd/poller`, and this project has never written a `cmd/poller` test (Context, fact 1).
Roadmap **D10** ("characterization only… no new unit tests for new code") forbids
inventing coverage for genuinely new code; it does not ask this tier to retroactively
test code that predates it and was never tested. There is no prior output to
characterize for these three pieces, in the same sense T6's own D10 accounting found none
for `MirrorSessions` ("nothing moves, so there is no prior output to characterize, and no
new unit test is added").

This is **not** the same situation as T6's, and the difference is worth stating plainly:
T6 added genuinely new storage and a genuinely new write path with no history at all, and
gave it full DB-integration coverage (its Test Contract groups A–C) precisely because it
was new. This tier's `internal/app` code is not new *behavior* — it is existing,
unchanged behavior acquiring a new, more testable home. The honest read is that this
change makes these three pieces **more testable in principle** (a real package, not
`cmd/`) while **choosing not to spend that new testability** in this tier, to keep the
change a pure relocation and match D10's letter. The verification signal for this code
remains what it already was: the owner's own `go run ./cmd/poller --once`, whose expected
log output is Test Contract group C below. (The *scheduler* half of this module is a
different case entirely — it arrives with its four tests intact, group S.)

**Flagged for a future backlog entry, not fixed here:** now that this logic lives in a
real package, a future change could give it offline coverage using fakes for
`telemetry.SuperchargerReader`/`charging.SessionWriter`/`account.Service`/`analytics.*`,
mirroring `internal/telemetry/service_test.go`'s existing fake-store pattern. That is a
strict testability *improvement* available to a later change, not a requirement of this
one.


### D13 — `app.NewScheduler` keeps the `cfg telemetry.Config` clock seam unchanged (mine — the amendment pass had to settle this; not an owner-interview outcome)

> **This is a decision this RD8 amendment pass had to make**, flagged as such so the
> owner and reviewer weigh it separately from RD8 itself, which settled only *where*
> `Scheduler` lives, not what its constructor looks like after the move.

Today's signature is:

```go
func NewScheduler(collector Collector, hour, minute int, loc *time.Location, cfg Config) *Scheduler
```

`cfg` is `telemetry.Config` and the scheduler reads **exactly one field from it**,
`cfg.Clock`, to set its `now func() time.Time` seam. The relocated version keeps that
parameter, changing only the collaborator type and the package qualifier:

```go
package app

func NewScheduler(processor Processor, hour, minute int, loc *time.Location, cfg telemetry.Config) *Scheduler
```

**Why keep it: the minimum-diff bias wins, and nothing concrete argues against it.**
Roadmap **D10** makes this tier characterization-only — a relocation, not a redesign — so
a narrower seam needs a positive reason, not merely a nicer shape. There is none:

- **`cmd/poller`'s wiring stays literally the same expression.** It already builds one
  `tcfg := telemetry.Config{WakeTimeout: …, Location: loc}` and passes it to *both* the
  collector and the scheduler, and `main.go`'s own comment documents that as deliberate
  ("One telemetry.Config drives both the collector … and the scheduler (clock seam, left
  nil here so both use the wall clock)"). Keeping the parameter keeps that comment true
  and the call site untouched apart from the `telemetry.NewScheduler` → `app.NewScheduler`
  rename — which is exactly the diff a reviewer reading task 4.1 wants to see.
- **The four relocated tests keep their existing construction calls**, gaining only a
  package qualifier: `NewScheduler(sc, 3, 30, time.UTC, Config{})` becomes
  `NewScheduler(sc, 3, 30, time.UTC, telemetry.Config{})`, and
  `Config{Clock: func() time.Time { return fixed }}` likewise. That is a mechanical,
  eyeball-checkable edit; a `now func() time.Time` parameter would instead rewrite every
  construction site's final argument into a different *kind* of value (`nil` vs a
  literal), which is precisely the "wide mechanical diff where one line quietly changes
  meaning" risk this change already carries once (the ~24 `CollectAll` call sites).
- **The cross-module coupling is already documented and intended.** `telemetry.Config`'s
  own `Location` doc comment cross-references `NewScheduler` explicitly, precisely so the
  snapshot's calendar day and the scheduler's "today" cannot drift apart. Splitting the
  scheduler onto a different clock parameter weakens a link the `telemetry` module went
  out of its way to write down.

**Rejected — a narrower `now func() time.Time` seam.** It is genuinely the tighter
interface (the scheduler would stop accepting two fields it ignores), and if this tier
were designing the scheduler from scratch it would be the right call. Rejected here
because it buys no behavior, costs the extra test churn and the wiring-comment
invalidation above, and spends them inside a characterization-only tier. Cheap to do
later as a standalone one-file change, and cheaper then than the review risk of bundling
it into this relocation.

**Ownership and the import rule.** `Config` is owned by `internal/telemetry` and stays
there — `internal/app` does not declare, wrap, or re-export it. `app` importing
`telemetry.Config` is already inside the allowed-imports rule: `internal/app/AGENTS.md`
permits `internal/telemetry` through its public surface (D9), and `Config` is part of
that public surface, in the same way `Collector`, `RunContext`, `CycleReport` and
`LogCycle` are. No new module dependency is created by this decision — `app` already
imports `telemetry` for the port it calls.


---

## Database Changes (design gate — full schema, rationale, index plan)

> **This change trips the built-in `database` design gate.** The schema below was
> **confirmed by the owner during the pre-artifacts interview** (RD7) and is transcribed
> here verbatim, not redesigned. Nothing is created, altered or dropped outside
> `internal/telemetry`'s own migration directory — `internal/app` owns no schema (D1).

**File:**
`internal/telemetry/db/migrations/20260823000002_add_run_id_triggered_by_poll_attempts.sql`
(version distinct from `internal/charging`'s `20260823000001` purely for legibility in
the shared `goose_db_version` table; cross-directory version-number collisions are
already tolerated by design — `internal/account` and `internal/charging` both ship a
`20260720000001` — so this is not a correctness requirement, only a convenience.)

### Up

```sql
-- +goose Up
-- poll_attempts gains two columns recording WHICH invocation of app.ProcessVehicleData
-- wrote each attempt, and what triggered that invocation (RM29 tier 7, roadmap D2 as
-- SUPERSEDED below — design.md D1).
--
-- WHY THIS STAYS HERE INSTEAD OF MOVING TO A NEW MODULE. Roadmap D2 said this table
-- moves to internal/app as process_runs, justified by "triggered_by is not something
-- Tesla reported." That purity test is right for vehicle_snapshots (RM29 violation #1,
-- fixed by tier 4) and wrong for this table: poll_attempts never held anything Tesla
-- reported to begin with — attempted_at is our own clock, outcome/reason are our own
-- classification of our own API call. triggered_by is the same kind of fact as every
-- column already here, not a boundary violation (design.md D1).
--
-- GRAIN IS UNCHANGED: one row per (vehicle, run), as this table's own original comment
-- already said (see this file's own header, migration 20260710000002). run_id
-- correlates every vehicle's row from one ProcessVehicleData invocation; a caller
-- wanting run-level facts computes them with GROUP BY run_id over this table — no
-- separate run-level table is added (design.md D2).
--
-- run_id is NULLABLE and PERMANENTLY UNBACKFILLED: a legacy row's run identity was
-- never recorded and cannot be recovered, so it stays NULL rather than being assigned a
-- fabricated value. triggered_by is NOT NULL DEFAULT 'scheduler': every row written
-- before this migration was written by the only entry point that existed — the
-- scheduled nightly poller — so the default backfills every existing row correctly, in
-- one metadata-only ALTER with no data migration and no invented value.
--
-- NO CHECK CONSTRAINT on triggered_by: the typed Go constant (telemetry.TriggeredBy —
-- "scheduler" | "api") guards every value this codebase can ever write; a DB CHECK
-- would only guard a hand-written SQL statement outside Go, which nothing in this
-- project does (mirrors the reasoning charging.SessionMirror's compile-time protection
-- used in RM29 tier 6, design.md D6 there).
--
-- NO INDEX on run_id or triggered_by: nothing reads either column in this tier (see
-- this design's Index Plan). The table gains roughly one row per registered vehicle
-- per night, so adding an index later — if a read ever needs one — costs nothing lost
-- by waiting. Same call as RM29 tier 6's I6 on a comparably speculative surface.
ALTER TABLE poll_attempts
    ADD COLUMN run_id       UUID,
    ADD COLUMN triggered_by TEXT NOT NULL DEFAULT 'scheduler';

COMMENT ON COLUMN poll_attempts.run_id IS
    'Correlates every vehicle''s attempt row from one app.ProcessVehicleData '
    'invocation. Generated once per invocation by internal/app (uuid.New()) and '
    'passed down via telemetry.RunContext (design.md D5). NULL on every row written '
    'before this migration — that run''s identity was never recorded and is not '
    'recoverable; never backfilled, never will be.';
COMMENT ON COLUMN poll_attempts.triggered_by IS
    'What triggered the run that wrote this attempt: scheduler (the nightly poller, '
    'including cmd/poller --once) or api (a future manual re-run, RM29 tier 8, '
    'parked). NOT NULL DEFAULT ''scheduler'' backfills every pre-migration row '
    'correctly, since no non-scheduler entry point existed before this tier. Guarded '
    'by the typed Go constant telemetry.TriggeredBy — no DB CHECK (design.md D1/D7).';

-- +goose Down
ALTER TABLE poll_attempts
    DROP COLUMN triggered_by,
    DROP COLUMN run_id;
```

**Down is non-destructive today.** Both columns hold data this tier itself introduced
(no other table is the source of truth for `run_id`/`triggered_by`); dropping them loses
only run-correlation metadata that this tier is the first to record, never data that
predates it.

### Index Plan

Declared read patterns for these two columns, and the object serving each:

| # | Object | Read/write pattern served | Justification |
|---|---|---|---|
| — | *(none added)* | — | No read in this tier filters, joins, or groups by `run_id` or `triggered_by`. |

**Deliberately NOT added, and the trigger to revisit each:**

- **`(run_id)`** — a future per-run summary read (`SELECT ... FROM poll_attempts WHERE
  run_id = $1` or a `GROUP BY run_id` report). Speculative today: nothing in this tier or
  the parked tier 8 plan reads a specific run's rows back. Revisit when an operator-facing
  "show me run X" view or API endpoint is actually built — at that point the natural
  index is `(run_id)` alone (a point lookup on a high-cardinality UUID needs no
  additional leading column) or `(account_id, run_id)` if the eventual read is
  account-scoped.
- **`(triggered_by)`** — a two-value column is never worth an index on its own; it would
  not usefully narrow any scan. A future "list every API-triggered run" read, if one is
  ever built, would lead with `account_id` or a time bound first, with `triggered_by` as
  a cheap residual filter — not the index's leading column.
- **Anything combining the two.** No declared read filters on both together.

**Write cost accepted.** Two columns on an `INSERT`-mostly table (`poll_attempts` is
append-only except for this tier, which adds no update path to it at all) that already
writes roughly one row per registered vehicle per night. Per the binding
`Performance-Profile`, this is exactly the class of cost the platform is willing to pay
at write time; here it is not even a new write, just two wider ones.

---

## Test Contract (expected values authored before implementation, per `ai/go-conventions.md`)

### Group A — offline (`internal/telemetry`, package `telemetry`, EARLY wave — no DB, compiles against the widened `Collector` signature immediately, no migration required)

**A1. Every existing `TestCollectAll_*` fixture in `service_test.go` compiles and passes
unchanged against the widened signature.** All ~24 call sites of the form
`svc.CollectAll(context.Background())` become
`svc.CollectAll(context.Background(), run)` using one shared test-package `RunContext`
fixture (e.g. a package-level helper `testRun()` returning a fresh
`telemetry.RunContext{RunID: uuid.New(), TriggeredBy: telemetry.TriggeredByScheduler}`
per call, so parallel subtests never share a `RunID`). Expected: every existing
assertion on the returned `CycleReport` and error is **identical** to before this change
— this is the literal characterization bar (roadmap D10): the widened signature changes
nothing about what `CollectAll` computes, only what it is told about the invocation.

**A2. `record()` stamps `RunID` and `TriggeredBy` onto every `Attempt` it writes.** Using
the existing fake `store.insertPollAttempt` capture already present in `service_test.go`
(it already records what `CollectAll` would persist), drive `CollectAll(ctx,
RunContext{RunID: someID, TriggeredBy: TriggeredByAPI})` against a single successful
vehicle (reuse an existing single-vehicle-success fixture rather than inventing a new
one). Expected: the captured `Attempt.RunID == someID` and
`Attempt.TriggeredBy == TriggeredByAPI`, in addition to every field the existing fixture
already asserts (`Outcome`, `Reason`, `AttemptedAt`, `AccountID`, `TeslaID`).

**A3. Two invocations in the same test stamp their own `RunID`, never the other's.** Call
`CollectAll` twice in one test with two different `RunContext.RunID` values against the
same fake store/vehicle fixture. Expected: the first call's captured `Attempt`(s) carry
the first `RunID`; the second call's carry the second — proving `run` is threaded per
call and never cached on `*service` (design.md D5's "threading, not service state").

### Group B — DB-integration (`internal/telemetry`, package `telemetry_test` or the module's existing `DATABASE_URL`-gated style, FINAL wave — needs the migration applied and sqlc regenerated)

**B1. `dbStore.insertPollAttempt` round-trips `RunID`/`TriggeredBy`.** Insert an
`Attempt` carrying a concrete `RunID` (`uuid.New()`) and `TriggeredBy: TriggeredByAPI`
through the store seam; read the row back with a direct SQL `SELECT run_id, triggered_by
FROM poll_attempts WHERE id = ...`. Expected: `run_id` equals the supplied UUID exactly;
`triggered_by = 'api'`.

**B2. A pre-migration-shaped row defaults correctly.** Direct `INSERT INTO
poll_attempts (account_id, tesla_id, attempted_at, outcome, reason) VALUES (...)`,
omitting both new columns — reproducing exactly what happened to every real row when
this migration's `ALTER` ran against the live database. Expected: `run_id IS NULL`;
`triggered_by = 'scheduler'`.

**B3. `triggered_by = 'scheduler'` round-trips through the Go store seam too**, not only
via direct SQL. Insert an `Attempt` with `TriggeredBy: TriggeredByScheduler` through
`dbStore.insertPollAttempt`; read back. Expected: `triggered_by = 'scheduler'` — pins
that the Go-side enum value and the column's own string default agree character-for-
character (a typo in either would otherwise pass B2 and this case independently while
silently disagreeing).

### Group C — `cmd/poller --once` (documented expectation, not an automated test — mirrors T6's "documented expectation rather than an in-suite test," since composition-root wiring has no test harness, Context fact 1)

**C1. Log output ordering is unchanged.** After the full tier lands and `make migrate-up`
runs, `go run ./cmd/poller --once` completes successfully, and its log shows, in the same
relative order as before this change: zero or more `"session mirror: account ... N
session(s)"` lines, then zero or more `"metrics reconciliation: ..."` /
`"gap reconciliation: ..."` lines, then exactly one final
`"telemetry cycle: attempted=... succeeded=... failures={...} ..."` line. The ordering is
unchanged because `LogCycle` is still called by the caller *after* the cycle returns
(design.md D11), from the same position in both entry points: the `--once` path is still
`cmd/poller`'s own code, now calling `Processor.ProcessVehicleData` instead of
`Collector.CollectAll`; the daemon path is `app.Scheduler.Run`'s tick handler, which
calls `telemetry.LogCycle` exactly where `telemetry.Scheduler.Run` used to (D4).

**C2. Every attempt from one run shares one `run_id`, and it is `'scheduler'`.**
```
psql "$DATABASE_URL" -c "SELECT tesla_id, run_id, triggered_by FROM poll_attempts ORDER BY attempted_at DESC LIMIT 5;"
```
run immediately after the `--once` invocation above. Expected: every one of the returned
rows (assuming 5 or fewer registered vehicles were just processed; adjust the `LIMIT` to
the real vehicle count if larger) shares the **same** `run_id` value, and every row's
`triggered_by = 'scheduler'`.

### Group S — offline (`internal/app`, package `app`, WAVE 3 — the four relocated `Scheduler`/`nextRun` tests; no DB, no migration)

These are **not new tests.** They are `internal/telemetry/scheduler_test.go`'s existing
four, moving into `internal/app/scheduler_test.go` with the code they cover (D4). They
are characterization tests in the strictest sense: **same inputs, same expected outputs,
only the seam's type changes.** Expected values are restated here up front, per
`ai/go-conventions.md`'s contract-first rule, so the worker never has to read the new
implementation to know what to assert.

**What changes in the file, and nothing else:**

- `package telemetry` → `package app`.
- `Config{...}` → `telemetry.Config{...}` in the two `NewScheduler` calls that pass one
  (D13 keeps the parameter, so the *value* is unchanged — only the qualifier is added).
- `CycleReport`/`Reason`/`ReasonAPIError` → `telemetry.CycleReport` /
  `telemetry.Reason` / `telemetry.ReasonAPIError`.
- The two collector stubs become **`Processor` fakes** (next paragraph).
- `TestFormatFailures` and the three `TestWaitUntilOnline_*` tests do **not** come here —
  they stay in `internal/telemetry` (they cover `report.go` and `wake.go`, neither of
  which moves). See the removal/move list below.

**The stub conversion (the only non-mechanical edit).** `stubCollector` and
`reportCollector` currently satisfy `telemetry.Collector` with
`CollectAll(context.Context) (CycleReport, error)`. Each becomes a fake satisfying
`app.Processor` — one method,
`ProcessVehicleData(ctx context.Context, triggeredBy telemetry.TriggeredBy) (telemetry.CycleReport, error)`
— that **records the call** (the same `calls int` counter, plus the received
`triggeredBy` where a test wants it) and **returns the same canned value the collector
stub returned**: the zero `telemetry.CycleReport{}` and `nil` for the shutdown stub, and
the caller-supplied `report`/`err` pair for the reporting stub. Nothing else about either
fake changes, so `TestScheduler_RunsAndLogsOneCycle` drives the identical report through
the identical `LogCycle` call and produces the identical logged output.

**S1. `TestNextRun` — unchanged, five cases, identical expected values.** `nextRun` is
copied verbatim and is still an unexported pure function in the new package, so the test
calls it exactly as before. Expected, for `hour=3, minute=30`: `2026-07-10T01:00Z` →
`2026-07-10T03:30Z`; `2026-07-10T09:00Z` → `2026-07-11T03:30Z`; `2026-07-10T03:30Z` →
`2026-07-11T03:30Z` (strictly-after rollover); `2026-07-10T23:00Z` in `PLUS5` →
`2026-07-12T03:30+05:00` (proves the math runs in `loc`, not UTC);
`2026-07-31T20:00Z` → `2026-08-01T03:30Z` (month rollover).

**S2. `TestScheduler_ShutsDownWithoutRunningWhenCancelled`** — construct with an
already-cancelled context. Expected, unchanged: `Run` returns an error satisfying
`errors.Is(err, context.Canceled)` within one second, and the fake's call count is
**exactly 0** — i.e. `ProcessVehicleData` is never invoked once shutdown has begun. (This
assertion is *stronger* after the move than before, because the thing that must not fire
is now the whole three-step cycle, not just the collect step.)

**S3. `TestScheduler_NilLocationDefaultsToLocal`** — `NewScheduler(fake, 3, 30, nil,
telemetry.Config{})`. Expected, unchanged: `sched.loc == time.Local`. Still a
same-package test reaching an unexported field, which is exactly why the file is
`package app` and not `package app_test`.

**S4. `TestScheduler_RunsAndLogsOneCycle`** — fake clock fixed at
`2026-07-10T03:29:59.99Z` so `nextRun` lands ~10ms out; sleep 100ms; cancel. The fake
returns `telemetry.CycleReport{Attempted: 3, Succeeded: 2, FailuresByReason:
{ReasonAPIError: 1}}` together with `errors.New("cycle: boom")` — the same pair as today
— so the logging path (`telemetry.LogCycle`, which prints the whole-cycle-error line
*and* the summary line) is exercised for a failing cycle. Expected, unchanged: `Run`
returns `context.Canceled`, and the fake's call count is **≥ 1**. The test asserts on the
count and clean shutdown, not on log text, exactly as before.

### What must NOT change

- `internal/charging`'s `SessionWriter`/`SessionMirror`/`NewSessionWriter` contract:
  unchanged. `internal/app` is simply its new caller.
- `internal/analytics`'s `Recalculator`/`Reader`/`GapWriter` contracts: unchanged, same
  reason.
- `telemetry.Collector`'s per-vehicle/per-account error isolation and `Reason` mapping:
  unchanged — only the signature widens by one parameter.
- Every existing `poll_attempts` row's `account_id`, `tesla_id`, `attempted_at`,
  `outcome`, `reason` value: untouched by this migration (it only adds columns).
- The four/five/six-way branching inside `collectAccount` (unauthorized-whole-account,
  `ListVehicles` 401, transient `ListVehicles` error, per-vehicle loop): unchanged in
  substance — `run` is threaded through, nothing about the branches themselves changes.

### What moves, unchanged (nothing is deleted outright)

- `internal/telemetry/scheduler.go`'s `Scheduler`, `NewScheduler`, `Run` and `nextRun`:
  **relocated** to `internal/app/scheduler.go`, essentially verbatim, with the single
  call-site change in `Run`'s tick handler (design.md **D4**) and the constructor's
  `cfg Config` → `cfg telemetry.Config` qualifier (design.md **D13**). The file
  `internal/telemetry/scheduler.go` ceases to exist, but **no logic is discarded** — the
  telemetry worker's task 2.4 removes it precisely because the app worker's task 3.4
  re-creates it.
- `internal/telemetry/scheduler_test.go`'s `TestNextRun`,
  `TestScheduler_ShutsDownWithoutRunningWhenCancelled`,
  `TestScheduler_NilLocationDefaultsToLocal`, `TestScheduler_RunsAndLogsOneCycle`:
  **relocated** to `internal/app/scheduler_test.go` with their assertions and expected
  values intact — Test Contract **group S** above states each one's expected values and
  the collector-stub → `Processor`-fake conversion. **This tier loses no automated
  coverage.**
- `TestFormatFailures` and `TestWaitUntilOnline_OnlineImmediately` /
  `TestWaitUntilOnline_TimesOutWhenAsleep` / `TestWaitUntilOnline_UnauthorizedPropagates`
  (also currently in `scheduler_test.go`, despite testing `formatFailures`/`LogCycle` and
  `wake.go`'s `waitUntilOnline` respectively, neither of which moves): **stay inside
  `internal/telemetry`** — `TestFormatFailures` to the new `report_test.go`, the three
  `TestWaitUntilOnline_*` to a new `wake_test.go`. Their assertions and fixtures are
  unchanged; only the file changes. Do **not** carry these four to `internal/app`; the
  code they cover is not going there.

**The one thing that is deliberately NOT created:** a `Scheduler`, a `nextRun`, or any
copy of either, left behind in `internal/telemetry` or newly written in `cmd/poller`.
Exactly one copy of this logic exists after this change, in `internal/app`.

---

## Risks / Trade-offs

- **`Scheduler`'s relocation is a verbatim-move claim that only a diff can verify** (D4).
  `go vet` proves the new `internal/app/scheduler.go` compiles and that
  `internal/app/scheduler_test.go` still type-checks against it; neither proves the
  timer/select/DST logic came across unaltered. The reviewer should diff the new file
  against the deleted `internal/telemetry/scheduler.go` and confirm the **only**
  substantive differences are the package clause, the `Processor`/`ProcessVehicleData`
  call site, the `telemetry.` qualifiers, and the `telemetry.LogCycle` call.
- **The two driving adapters are no longer symmetric in location** (D3, D4). After RD8
  the scheduler lives in `internal/app` while the parked tier-8 API adapter will live in
  a `cmd/` binary or the gateway. Accepted deliberately: the `cmd/`-stays-thin rule is
  written, the symmetry is taste. The risk is that a future reader mistakes the
  co-location for "the scheduler is part of the port" — D3 exists to answer that reader,
  and `internal/app/AGENTS.md` §Responsibility repeats it where an agent will actually
  look.
- **`internal/app`'s three orchestration steps ship with no automated test of their own
  logic** (D12) — note this is about the three steps only; the module's scheduler half
  arrives with its four tests intact (D4, Test Contract group S).
  Accepted because none existed before (they lived in `cmd/poller`), but it does mean a
  future bug in, say, the mirror step's per-account deduplication would be caught only by
  a live `--once` run or production symptoms, not by CI. Flagged as a good future backlog
  candidate now that the code has a real home — and cheaper than before, since RD8 means
  the module already has a `_test.go` file and a test-package convention to extend.
- **`Collector.CollectAll`'s signature change touches roughly 24 call sites in one test
  file.** A wide, mechanical diff is easy to get "compiles, but one assertion was quietly
  dropped during the edit" wrong; `go vet` catches only compilation, not a silently
  weakened assertion. Reviewer should spot-check a sample of the ~24 updated call sites
  against their pre-change form, not just confirm the file compiles.
- **`run_id` is NULL on every row written before this migration, forever.** No later
  change can recover which run wrote a legacy row. Acceptable because nothing reads
  `run_id` yet and the alternative (inventing a synthetic value) would be actively
  misleading — a fabricated `run_id` implies a real correlation that does not exist.
- **`internal/charging/AGENTS.md`'s composition-root reference is the one cross-module
  doc fix riding on this change** (D6). If it is missed, a future reader of that file
  will believe `cmd/poller` still builds the mirror mapping directly, which after this
  tier is false.
- **A related doc may also be stale and was not verified**: `internal/analytics`'s own
  `AGENTS.md` (outside this worker's doc pack, not read while producing this design) may
  describe `cmd/poller` as "the only production caller of `Reconcile`" — a claim this tier
  makes literally true of `internal/app` instead. Flagged for the leader to grep and
  confirm/fix; not asserted as broken here since the file was not read.

## Verification signals

Per the binding `Test-Execution-Policy`, the assistant runs and reports: `go build ./...`,
`go vet ./...`, `gofmt -l .`, `make build`, `make vet`, `make bins`, `make sqlc` (after
the migration and after any `query.sql` change — this tier adds no new query, so this is
a regression check that the schema-only migration does not perturb existing generated
code), and the three standalone guards — `make ui-guard` (no-op: no gateway markup),
`make i18n-guard` (no-op: no user-facing string), `make money-guard` (no-op: no gateway
handler formats money). It also runs `openspec validate --changes --strict`.

The owner alone runs the suite and the database steps:

```
make migrate-up
make migrate-status          # telemetry's directory shows the new migration Applied
go test ./internal/telemetry/... ./internal/app/... ./cmd/...
```

or the full suite:

```
go test ./...
```

and, because `cmd/poller` — now wiring only (D4) — has no tests of its own (Context,
fact 1; design.md D12), its rewiring is verified by running it:

```
go run ./cmd/poller --once
```

then, to confirm the migration and the stamping against the real data (Test Contract
group C):

```
psql "$DATABASE_URL" -c "SELECT tesla_id, run_id, triggered_by FROM poll_attempts ORDER BY attempted_at DESC LIMIT 5;"
```

Until the owner runs these and reports the results, this tier's implementation status is
**awaiting-user-verification**, never "done".
