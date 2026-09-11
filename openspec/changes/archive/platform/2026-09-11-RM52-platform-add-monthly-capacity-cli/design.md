# Design — RM52-platform-add-monthly-capacity-cli

Source ticket: MAG-32 · Roadmap: `openspec/roadmaps/RM52-vehicle-monthly-metrics.md`, tier 3
of 3. Roadmap decisions **RD1–RD14** are binding, confirmed with the owner at the
2026-09-10 `grill-me` interview. This document does not re-open them. RD8, RD9, and RD10
are this tier's own scope. RD1–RD7 and RD11–RD14 were implemented and archived by tier 1
(`openspec/changes/archive/charging/2026-09-10-RM52-charging-add-monthly-effective-capacity/`)
and tier 2
(`openspec/changes/archive/app/2026-09-10-RM52-app-add-monthly-capacity-step/`).

Two more decisions, **T1** and **T2**, were settled with the owner today, after the roadmap
was written. Both are recorded below as design decisions, with the same weight as an RD.

**No database design gate.** This change adds no table, column, index, constraint, or
migration, and reads no database directly beyond calling tier 1's existing port.
`openspec/config.yaml` §design's database gate (`CLAUDE.md` §Pipeline config →
`Design-Gates: database`) does not apply. Stated once, plainly, so this reads as a finding,
not an oversight.

---

## Context

Facts read from the repository, not recalled. Each one shapes a decision below.

1. **`charging.NewMonthlyCapacityCalculator(pool)` already exists.** It returns
   `charging.MonthlyCapacityCalculator`, one method:
   `Calculate(ctx context.Context, period time.Time, teslaID *int64) (MonthlyCapacityReport, error)`.
   (`internal/charging/charging.go:663-680`.)
2. **`MonthlyCapacityReport` has four fields:** `Period time.Time`, `VehiclesFound int`,
   `Measured int`, `Thin int`. Its own doc comment already says: "cmd/monthly-capacity
   (tier 3) prints this." (`internal/charging/charging.go:651-661`.)
3. **Tier 2's wiring is already done.** `cmd/poller/main.go:146` already passes
   `charging.NewMonthlyCapacityCalculator(pool)` into `app.NewProcessor`. `cmd/web` builds
   only the gateway, and RD8 keeps the gateway untouched, so `cmd/web/main.go` needs no
   change either. This tier's task 3 (see `tasks.md`) is to **verify** both facts, not
   implement them.
4. **`config.Load()` requires Tesla credentials.** It returns an error when
   `TESLA_CLIENT_ID` or `TESLA_CLIENT_SECRET` is empty (`internal/config/config.go:122`).
   A DB-only tool must not fail to start over a credential it never uses.
5. **`config.LoadMigration()` is the existing precedent for a smaller loader.** It calls
   `loadDotEnv()`, requires only `DATABASE_URL`, and returns a config struct
   (`internal/config/config.go:180`). It skips the Tesla-credential check entirely.
6. **Every existing `cmd-*` Makefile target follows one shape:** a `##` help line, a
   `.PHONY` entry, `@mkdir -p bin`, `go build -o bin/<name> ./cmd/<dir>`, then run the
   binary (`Makefile:730,755,760`). None of them takes an argument today — this tier sets
   that pattern for the first time.
7. **The Dockerfile builds each binary by name** (`web`, `poller`, `migrate`) —
   `deploy/docker/Dockerfile`. A new `cmd/` binary does not reach production on its own.
8. **`internal/app`'s `monthlyCapacityPeriod` is unexported**
   (`internal/app/processor.go:385`). This tier cannot import or reuse it, and must not
   export it just to be reused once (see D3).
9. **`cmd/` is the composition root and is exempt from `make tz-guard`.**
   `ai/go-conventions.md` §"platform's default time zone": *"`cmd/*` is exempt as the
   composition root — it calls `time.LoadLocation` explicitly and on purpose."* The
   Makefile's own `tz-guard` target confirms this mechanically: it greps only `internal`,
   never `cmd`. RD7's rule ("go through `internal/clock`") still guides this tier's design
   by intent — see D2 for exactly where it applies and where it does not.
10. **`internal/clock` exposes three functions this tier can use:** `Zone() *time.Location`
    (the platform default, `America/Bogota`), `Now() time.Time` (the current moment,
    already expressed in `Zone()`), and `CalendarDay(t time.Time, loc *time.Location)
    time.Time` (normalizes `t` to its calendar day in `loc`, expressed at UTC midnight —
    `internal/clock/clock.go`, `internal/clock/calendar.go`).
11. **`cmd/poller/main.go`'s own doc comment explains why that package has zero tests:**
    "this package has no tests and never has... it owns no business logic at all." That
    reasoning does not carry over here — `cmd/monthly-capacity` is not thin wiring around
    an application-layer port call; its flag/period logic is the one piece of real logic
    this tier adds, and nothing in `internal/` owns it. See D4 for where its tests live.
12. **`internal/config`'s existing tests already cover the `LoadMigration`
    DATABASE_URL-required / .env-optional shape** (`internal/config/config_test.go`,
    `unsetMigrationEnv` helper). `LoadDatabase`'s own tests reuse the same pattern (see
    §Test Contract Group C).

## Goals / Non-Goals

**Goals**

- A `cmd/monthly-capacity` runnable, callable by hand, that computes one month's effective
  capacity for one vehicle or every vehicle (RD8).
- A `make cmd-monthly-capacity` target that follows the existing `cmd-*` shape and adds
  optional arguments for the first time (RD9).
- A new, minimal `config.LoadDatabase()` that a DB-only tool can call without tripping
  `config.Load()`'s Tesla-credential requirement (T2).
- Confirm, not re-do, tier 2's wiring of `MonthlyCapacityCalculator` into `cmd/poller`.
- Update every doc `CLAUDE.md` §Non-negotiables requires for a new runnable: root
  `README.md`, `cmd/README.md`, `internal/charging/AGENTS.md`, `internal/config/AGENTS.md`,
  and the KB (RD10, item 4/5 of the roadmap's tier-3 scope).

**Non-Goals**

- Any change to `internal/charging` Go code, `internal/app`, or any migration — both
  already archived. `internal/charging/AGENTS.md` is a doc edit, and is in scope.
- Any change to `internal/gateway` — never touched by this roadmap (RD8).
- Shipping the tool in the production Docker image — **T1**, decided today: local only.
- Automatic backfill of any kind — RD9 is explicit: a person runs this tool by hand, every
  time.
- A second monthly metric, or the `analytics.vehicle_monthly_metrics` table RD12 defers.

---

## Decisions

### D1 — Flag surface: `-period` and `-tesla-id`, both optional

```
go run ./cmd/monthly-capacity                            # previous month, every vehicle
go run ./cmd/monthly-capacity -period 2026-08             # one month, every vehicle
go run ./cmd/monthly-capacity -tesla-id 123               # previous month, one vehicle
go run ./cmd/monthly-capacity -period 2026-08 -tesla-id 123
```

- `-period string` — a calendar month, layout `2006-01` (Go's reference layout for
  `YYYY-MM`). Empty means "the previous month" (D2).
- `-tesla-id int64` — a Tesla vehicle id. Not given means "every vehicle with at least one
  usable record this period" (`Calculate`'s own `teslaID == nil` contract, Context fact 1).

**Detecting "not given" correctly.** A Tesla vehicle id is a real, positive number, but this
tool does not lean on that fact — it uses the standard, general-purpose way to tell whether
a flag was actually typed: `flag.Visit`, which iterates only the flags a caller actually
set. This works correctly even in the edge case where a future caller passes `-tesla-id 0`
on purpose (Test Contract case B3) — a check like `id != 0` would wrongly treat that as
"not given."

```go
// teslaIDPointer turns a flag.Int64Var's value into the *int64 the port wants.
// wasSet comes from flag.Visit, never from checking id != 0 -- a caller could
// legitimately pass -tesla-id 0, and this function must not silently treat
// that as "no flag given."
func teslaIDPointer(id int64, wasSet bool) *int64 {
	if !wasSet {
		return nil
	}
	return &id
}
```

| Alternative | Why rejected |
|---|---|
| Treat `id == 0` as "not given" | Tesla ids are never 0 in practice, but relying on that is a silent assumption about data, not a rule of the flag itself. `flag.Visit` is the correct, general answer and costs nothing extra. |

### D2 — Period resolution: `internal/clock` for the default, direct parsing for an explicit month

**Two different questions, two different answers — this is the "right zone" design item
the dispatch asked for.**

**When `-period` is empty**, the tool must answer "what is the previous month, right now?" —
exactly the same question tier 2's `monthlyCapacityPeriod` answers for the nightly step,
minus the "is today the 1st?" gate (this tool runs on any day, on purpose). This question
is genuinely zone-sensitive: "now" means different calendar days in different zones near a
day boundary. So this path uses `internal/clock`, exactly as RD7 asks:

```go
// previousMonth mirrors internal/app's monthlyCapacityPeriod (RM52 tier 2), minus its
// "is today the 1st?" gate -- this tool answers "what is the previous month, right now?"
// on any day it is run, not only the first of the month. now and loc are parameters, not
// read internally, so this stays a pure, fully-tested function -- the same shape
// monthlyCapacityPeriod itself uses.
func previousMonth(now time.Time, loc *time.Location) time.Time {
	today := clock.CalendarDay(now, loc)
	return today.AddDate(0, -1, 0)
}
```

The one call site passes `previousMonth(clock.Now(), clock.Zone())` — never a raw
`time.Now()`, never a hardcoded zone string.

**When `-period` is given**, e.g. `"2026-08"`, there is no "now" to resolve and no zone
ambiguity: the caller named an exact calendar month directly. Parsing it is not a
"midnight-of-a-day" construction — it is a direct read of a value the caller already fully
specified:

```go
// parsePeriod parses a "YYYY-MM" period string into the first instant of that month,
// UTC-midnight-stamped -- the same representation clock.CalendarDay always returns
// (matching pgtype.Date's storage encoding, so it compares equal to what Calculate reads
// back). time.Parse with no zone information in the layout already returns a UTC time, so
// no separate zone conversion is needed or correct here: raw did not come from a clock
// reading, so there is no "which zone is now in" question to answer.
func parsePeriod(raw string) (time.Time, error) {
	return time.Parse("2006-01", raw)
}
```

`time.Parse("2006-01", "2026-08")` returns `2026-08-01T00:00:00Z` directly — already the
representation this table and `Calculate` expect. `time.Parse("2006-01", "2026-13")`
returns an error (month out of range), rejected before any flag, pool, or query runs.

| Alternative | Why rejected |
|---|---|
| Route the explicit `-period` value through `clock.CalendarDay` too, "for consistency" | Wrong, not just unnecessary: `CalendarDay` converts an **instant** into a zone's calendar day. Feeding it a UTC midnight that does not represent a real "now" reading can shift the parsed month by a day near Bogota's offset, silently changing which month the caller asked for. The two paths answer different questions and must stay separate. |
| Hand-construct `time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)` after manually splitting `"YYYY-MM"` | `time.Parse` already does this, correctly, with input validation built in (rejects `"2026-13"`, rejects `"2026-8"` under the strict `2006-01` layout) — a hand-rolled split would have to reimplement that validation with no benefit. `cmd/` is exempt from `tz-guard`'s grep (Context fact 9) either way, so this is a code-quality choice, not a guard-compliance one. |

### D3 — `resolvePeriod` composes the two paths; no duplicated app code

```go
// resolvePeriod is the single entry point main() calls: raw is the -period flag's raw
// string. Empty means "the previous month" (D2); non-empty is parsed directly (D2). This
// duplicates ~5 lines of internal/app's own monthlyCapacityPeriod math rather than
// exporting it (Context fact 8) -- app.monthlyCapacityPeriod is deliberately unexported,
// carries a gate this tool does not want ("is today the 1st?"), and exporting a function
// from an application-layer package for one manual-tool caller would widen app's public
// surface for a need that is five lines of straight-line date math, not a shared rule
// that could drift between two copies.
func resolvePeriod(raw string, now time.Time, loc *time.Location) (time.Time, error) {
	if raw == "" {
		return previousMonth(now, loc), nil
	}
	return parsePeriod(raw)
}
```

| Alternative | Why rejected |
|---|---|
| Export `app.MonthlyCapacityPeriod` and reuse it here, stripping its gate with a boolean parameter | Rejected: this widens `internal/app`'s public port for a single external caller, and the "strip the gate" parameter would need its own test coverage in a package that has no reason to know a CLI tool exists. Five lines of duplicated, independently-tested date math costs less than that coupling (F17, `CLAUDE.md` §Non-negotiables over-abstraction warning). |

### D4 — Where the tests live: `cmd/monthly-capacity/period_test.go`, package `main`

**Not a re-derivation of the "cmd/ has no tests" convention — a direct reading of why it
exists (Context fact 11).** `cmd/poller` has zero tests because it is deliberately zero
business logic: every real decision lives in `internal/app`, and the file's own doc comment
says so. `cmd/monthly-capacity` is different: `parsePeriod`, `previousMonth`,
`resolvePeriod`, and `teslaIDPointer` are this tier's own new logic, and nothing in
`internal/` owns them (D3 explains why they do not move into `internal/app`). Go's testing
tool works normally on `package main` — there is no technical reason these functions cannot
be tested where they live.

No new `internal/` package is created to hold four small, single-caller functions. That
would be the over-abstraction `CLAUDE.md` §Non-negotiables warns against: a package's worth
of indirection for logic with exactly one caller and no reuse in sight.

| Alternative | Why rejected |
|---|---|
| Create `internal/monthlycli` (or similar) just to host these four functions and their tests | Rejected: over-abstraction for a single caller. `cmd/` packages are not barred from tests by the language, only by this project's own choice for `cmd/poller` specifically — a choice this tier does not need to inherit. |
| Leave the logic untested, matching `cmd/poller` | Rejected: unlike `cmd/poller`'s wiring, this logic has real branches (empty vs. non-empty `-period`, a malformed month, the `id == 0`-but-explicit case) worth locking down before the tool ships, per `ai/go-conventions.md` §Testing's authoring-order rule for pure logic. |

### D5 — Output and exit codes

```go
// printReport mirrors internal/app's callMonthlyCapacityCalculator log line exactly
// (RM52 tier 2 design.md D2) -- one gold-standard format for this report, used by both
// the nightly step's log line and this tool's stdout line.
fmt.Printf("monthly capacity: period %s: %d vehicle(s) found, %d measured, %d thin\n",
	report.Period.Format("2006-01"), report.VehiclesFound, report.Measured, report.Thin)
```

- **Success:** the line above, to stdout. Exit code `0`.
- **Any error** (a bad `-period` value, a `config.LoadDatabase` error, a pool-creation
  error, or `Calculate` returning an error): printed to stderr via `log.Fatalf`, which
  exits `1`. Mirrors `cmd/poller`'s own `log.Fatalf` shape on a whole-cycle failure.

| Alternative | Why rejected |
|---|---|
| Invent a new report format | Rejected: `CLAUDE.md` §Non-negotiables asks for a closed, small vocabulary — one format for "here is what a `MonthlyCapacityReport` says," reused everywhere it is printed, is cheaper for a future reader than two similar-but-different lines. |

### D6 — The `make cmd-monthly-capacity` target: the first `cmd-*` target with arguments

```make
cmd-monthly-capacity: ## Build cmd/monthly-capacity into ./bin and run it. Optional: PERIOD=2026-08 TESLA_ID=123 (defaults: previous month, every vehicle). Needs DATABASE_URL.
	@mkdir -p bin
	go build -o bin/monthly-capacity ./cmd/monthly-capacity
	./bin/monthly-capacity $(if $(PERIOD),-period $(PERIOD)) $(if $(TESLA_ID),-tesla-id $(TESLA_ID))
```

```
make cmd-monthly-capacity                              # previous month, every vehicle
make cmd-monthly-capacity PERIOD=2026-08 TESLA_ID=123   # one month, one vehicle
```

`$(if $(PERIOD),-period $(PERIOD))` expands to nothing when `PERIOD` is unset, and to
`-period 2026-08` when set — the standard GNU Make conditional, no new Make machinery. Every
other `cmd-*` target (Context fact 6) takes no argument at all, so this is a new shape, not
an extension of an old one; it is kept as close to the existing pattern as the new need
allows: same `##` line style, same `@mkdir -p bin`, same `go build -o bin/<name>
./cmd/<dir>` line.

| Alternative | Why rejected |
|---|---|
| A second target, `cmd-monthly-capacity-for`, that takes arguments, leaving the plain one argument-free | Rejected: two targets for one binary is worse for a future reader than one target with two optional variables, and the Makefile's own `help` output would show two similar lines instead of one self-explanatory one. |
| Require both `PERIOD` and `TESLA_ID` always | Rejected: RD8 explicitly wants both to default (previous month, every vehicle) — requiring them would contradict the roadmap's own contract for the tool's flags. |

### D7 — `config.LoadDatabase() (string, error)` (T2)

```go
// LoadDatabase reads config for a database-only tool (cmd/monthly-capacity). Unlike Load,
// it does not require any Tesla credential -- a tool that never calls the Fleet API has no
// reason to fail over one. It loads .env the same way Load and LoadMigration do (a missing
// .env is not an error), then reads DATABASE_URL, erroring when it is empty. Nothing else.
func LoadDatabase() (string, error) {
	if err := loadDotEnv(); err != nil {
		return "", err
	}
	dbURL := envStripped("DATABASE_URL")
	if dbURL == "" {
		return "", fmt.Errorf("DATABASE_URL must be set")
	}
	return dbURL, nil
}
```

**Returns a bare string, not a wrapping struct.** `LoadMigration` returns a struct because
it carries three fields (`DatabaseURL`, `MigrationsRoot`, `MigrationsDirs`). `LoadDatabase`
has exactly one fact to return. A single-field struct here would be pure indirection for no
benefit — the over-abstraction `CLAUDE.md` §Non-negotiables warns against — so this mirrors
`LoadMigration`'s **behavior** (loads `.env`, requires only `DATABASE_URL`, nothing else)
without mirroring its **shape**.

**Why a DB-only tool must not inherit `Load`'s Tesla-credential check (Context fact 4):**
`cmd/monthly-capacity` never calls the Fleet API, wakes no car, and needs no OAuth scope.
Requiring `TESLA_CLIENT_ID`/`TESLA_CLIENT_SECRET` from it would force every environment
that runs this tool to also hold Tesla credentials it never uses — exactly the reasoning
`LoadMigration` already established for `cmd/migrate` (Context fact 5).

| Alternative | Why rejected |
|---|---|
| Reuse `config.Load()` and ignore its Tesla-credential requirement | Rejected: `Load()` returns an error and a `nil` config when the credential check fails (Context fact 4) — there is nothing to "ignore," the call itself fails first. |
| Add a `DatabaseOnly bool` parameter to `Load()` | Rejected: branches one function's behavior on a caller-supplied flag instead of having two small, honestly-named functions — `LoadMigration` already set the precedent of "a new loader per shape of caller," not "one loader with modes." |
| Return a single-field `DatabaseConfig` struct, for shape-consistency with `MigrationConfig` | Rejected: a struct wrapping one string is indirection with no payoff — the caller would immediately unwrap it (`cfg.DatabaseURL`) with no other field ever read. |

### T1 — The tool is local only; it does not ship to the production image

**Decided with the owner today, overriding the roadmap's silence on deployment.** The
Dockerfile (Context fact 7) builds `web`, `poller`, and `migrate` by name; this tier adds no
fourth name. Consequence, stated plainly: RD9's "backfill by running the tool" works for any
database an operator can reach directly (a local checkout against a tunneled or exposed
`DATABASE_URL`), but backfilling the **production** database through a container image is
out of scope for this change. No backlog entry is added for shipping it later — the owner
declined one when asked.

| Alternative | Why rejected |
|---|---|
| Add `monthly-capacity` as a fourth binary in the Dockerfile | Rejected by the owner today — a manual, occasional tool does not need a standing place in the deploy image. |

### T2 — `config.LoadDatabase()` is a new function (see D7)

Recorded here as its own numbered decision because it was settled with the owner today,
outside the roadmap's original text, with the same weight as an RD.

---

## Verification tasks (not implementation — tier 2 already did the work)

| Claim | Where to check | Expected |
|---|---|---|
| `cmd/poller/main.go` passes `charging.NewMonthlyCapacityCalculator(pool)` into `app.NewProcessor` | `cmd/poller/main.go`, the `app.NewProcessor(...)` call | Present, already there since tier 2 |
| `cmd/web/main.go` does not need a change | `cmd/web/main.go` | Builds only the gateway; RD8 keeps the gateway untouched |

Both are one task in `tasks.md` (task 4.1) — **verify, do not re-implement.**

---

## Docs and KB — what changes and why

| Doc | Change | Why |
|---|---|---|
| Root `README.md` | "Project Structure" gains `cmd/monthly-capacity/`; the dependency graph's composition-root block gains `cmd/monthly-capacity ─► charging, config` | `CLAUDE.md` §Non-negotiables: a new runnable is a structural change |
| `cmd/README.md` | New row in the binaries table | Same rule, binary-specific home |
| `internal/charging/AGENTS.md` | Note the CLI as `MonthlyCapacityCalculator`'s second caller | The existing text only names `internal/app`; leaving it would make the doc read as if the port had one caller after this change ships |
| `internal/config/AGENTS.md` | §Public interface gains `LoadDatabase`; §Testing gains its cases | Same structural-change rule, for the new exported function |
| `kkpa/context/architecture/nightly-cycle.md` | Port-map row for `charging.MonthlyCapacityCalculator` gains the CLI as a second caller | Same reason as the `AGENTS.md` note above, at the KB layer |
| `kkpa/context/workflows/vehicle-monthly-metrics.md` (new) | The whole monthly-metrics story: table, estimator, nightly step, CLI, and where a second metric would go (RD12) | RD10 names this guide by name as the roadmap's own deliverable; run via `kkpa-context-curate` (leader task, `tasks.md`). **Corrected at review:** this row first said `entities/vehicle-monthly-metrics/guide.md`. The curate skill routes by trigger — the job has no single external trigger, so it is a workflow, not an entity. RD10 fixes only the name, which is unchanged. Owner-confirmed on 2026-09-11. |

**KB grep result.** Searched `kkpa/context/` for `charging` (`grep -rn charging kkpa/context/`,
excluding `pending-spec-to-sync/` — those are staged, human-gated proposals, not live
guides). `INDEX.md` and `architecture/nightly-cycle.md` already name tier 1 and tier 2
correctly (`monthly_effective_capacity`, the four-step cycle, step 4's gate). The one gap
both leave is the port-map row noted above: it names `app` as `MonthlyCapacityCalculator`'s
only caller, which becomes incomplete once this tier's CLI calls it directly. No other guide
needs a change for this tier.

---

## Test Contract

Expected values authored **before** implementation, per `ai/go-conventions.md` §Testing.
**Tests written later must assert this contract**, not whatever the implementation
produces.

### Group A — `previousMonth` and `resolvePeriod`'s empty-string path (pure, no I/O)

New file `cmd/monthly-capacity/period_test.go`, package `main`.

| ID | `now` | `loc` | Expected `previousMonth(now, loc)` | What it proves |
|---|---|---|---|---|
| **A1** | `time.Date(2026,9,15,10,0,0,0,bogota)` | `America/Bogota` | `2026-08-01 00:00:00 UTC` | The plain case: September's previous month is August. |
| **A2** | `time.Date(2027,1,10,8,0,0,0,bogota)` | `America/Bogota` | `2026-12-01 00:00:00 UTC` | Year boundary: January's previous month is December of the **prior** year. |
| **A3** | `time.Date(2026,9,1,3,0,0,0,time.UTC)` — this UTC instant is `2026-08-31 22:00` in Bogota (UTC-5) | `America/Bogota` | `2026-07-01 00:00:00 UTC` | **Zone-aware, load-bearing case.** In UTC this instant is already September; in Bogota it is still August. The previous month must be computed from the **Bogota** calendar day (July), not the UTC one (August) — the direct reuse of tier 2's own A4/A5 proof, applied to this tool's default path. |
| **A4** | same instant as A3 | `time.UTC` | `2026-08-01 00:00:00 UTC` | `loc` is a real parameter, not hardcoded to Bogota inside the function — passing `time.UTC` changes the result for the same instant, proving the zone is actually used, not just accepted. **Leader correction (2026-09-10, after wave 1):** this cell first read `2026-07-01`, which contradicted the case's own purpose — the same value as A3 proves nothing changed. In UTC the instant is 1 September, so the month start is September and the previous month is August. The prose was right; the value was wrong. |

| ID | `raw` | Expected `resolvePeriod(raw, now, loc)` | What it proves |
|---|---|---|---|
| **A5** | `""` | `2026-08-01 00:00:00 UTC`, for A1's `now`/`loc` | Empty `-period` delegates to `previousMonth`. The expected value is written out, never obtained by calling `previousMonth` in the test — a test whose oracle is the function under test still passes when that function breaks. |
| **A6** | `"2026-08"` | `(2026-08-01 00:00:00 UTC, nil)` — `now`/`loc` irrelevant here | A given period is parsed directly, ignoring `now`/`loc` entirely (D2). |

### Group B — `parsePeriod` and `teslaIDPointer` (pure, no I/O)

Same file.

| ID | Input | Expected | What it proves |
|---|---|---|---|
| **B1** | `parsePeriod("2026-08")` | `(2026-08-01 00:00:00 UTC, nil)` | The plain case. |
| **B2** | `parsePeriod("2026-13")` | `(zero time.Time, error)` | An out-of-range month is rejected. |
| **B3** | `parsePeriod("2026-8")` | `(zero time.Time, error)` | The layout is strict: `2006-01`, not `2006-1` — a single-digit month is rejected, not silently accepted. |
| **B4** | `parsePeriod("")` | `(zero time.Time, error)` | An empty string is never passed here directly in `main` (D3 routes it to `previousMonth` first), but `parsePeriod` itself must still reject it if called directly — it does not special-case empty input. |
| **B5** | `teslaIDPointer(123, true)` | pointer to `123` | The plain case: an explicitly-given id is used. |
| **B6** | `teslaIDPointer(0, false)` | `nil` | The default case: no `-tesla-id` flag means every vehicle. |
| **B7** | `teslaIDPointer(0, true)` | pointer to `0` | **Load-bearing case.** An explicit `-tesla-id 0` is honored as a real value, not treated as "not given" — this is the whole reason D1 uses `flag.Visit` instead of an `id != 0` check. |

### Group C — `config.LoadDatabase` (offline, `.env`/env-var only, no real DB connection)

`internal/config/config_test.go`, reusing the existing `withTempDir` helper and a new
`unsetDatabaseEnv` helper mirroring `unsetMigrationEnv`'s exact shape.

| ID | Setup | Expected | What it proves |
|---|---|---|---|
| **C1** | `.env` present with `DATABASE_URL=postgres://...`; `TESLA_CLIENT_ID`/`SECRET` unset | `LoadDatabase()` returns the DSN, `nil` error | The plain case, and proof this loader does not require Tesla credentials (unlike `Load()`). |
| **C2** | No `.env` file; `DATABASE_URL` set in the real environment | `LoadDatabase()` returns the DSN, `nil` error | The container case — a missing `.env` is not an error, mirroring `Load`/`LoadMigration`. |
| **C3** | No `.env` file; `DATABASE_URL` unset everywhere | `LoadDatabase()` returns `("", error)` | The one required value is actually required. |
| **C4** | `.env` present with `DATABASE_URL` set; real environment also sets a **different** `DATABASE_URL` | `LoadDatabase()` returns the **real environment's** value | `godotenv.Load()`'s existing non-overriding behavior — a real environment variable always wins — applies to this loader too, unchanged. |

---

## Roadmap-decision mapping

| design.md | roadmap / owner decision | Subject |
|---|---|---|
| D1 | **RD8** | flag surface, `-tesla-id` nil-detection |
| D2, D3 | **RD7** (by intent, not by guard), **RD8** | period resolution, default vs. explicit |
| D4 | — | where the new tests live |
| D5 | — | output format and exit codes |
| D6 | **RD9** | the `make` target's shape |
| D7 | **T2** (owner, 2026-09-10) | `config.LoadDatabase()` |
| T1 | **T1** (owner, 2026-09-10) | local-only tool, no Docker change |

RD1–RD7 (save D2/D3's intent-level use) and RD11–RD14 are tier 1's or tier 2's, both
archived, and are not re-implemented here.

---

## Risks

1. **A future deploy need might want this tool in production.** T1 accepts that gap
   deliberately; if it becomes real, it is a new, separate decision — not something this
   change should pre-guess with a backlog entry the owner already declined.
2. **`resolvePeriod`'s empty-string path and `main`'s flag wiring share no test with the
   real `clock.Now()` reading**, the same accepted-gap category tier 2's own
   `runMonthlyCapacityStep` already carries: the pure math (`previousMonth`) is fully
   tested; the one line that reads the real clock is three lines of composition, not logic,
   and is not separately tested.
