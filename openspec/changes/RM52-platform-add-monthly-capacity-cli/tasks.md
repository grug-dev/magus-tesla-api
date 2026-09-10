# Tasks — RM52-platform-add-monthly-capacity-cli

Ownership legend: **[module: platform worker]** — inside a granted path only:
`cmd/monthly-capacity/`, `internal/config/`, `Makefile`, `README.md`, `cmd/README.md`,
`internal/charging/AGENTS.md`, `docs/`, `kkpa/context/`. **[leader]** — the pipeline
leader. **[owner]** — the human. See `design.md` D1–D7 and T1/T2 for the rationale behind
each group.

> ## No design gate
>
> This change adds no table, column, index, or migration (design.md header). Work may
> start without an owner confirmation step.

## Ordering constraints

- **Wave 1 builds the pure logic first**, since the binary and its tests both depend on it.
- **Wave 2 builds the runnable and the config loader in parallel** — disjoint files
  (`cmd/monthly-capacity/` vs. `internal/config/`).
- **Wave 3's tests cannot compile before Wave 2** provides the functions they test. Their
  expected values are already fixed in `design.md` §Test Contract — author against that
  contract, not against whatever the implementation produces.
- **Wave 4 is the verification task (F13)** — it can run any time after task 2.1 exists,
  since it only reads `cmd/poller/main.go` and `cmd/web/main.go`, both untouched by this
  change.
- **Wave 5 is documentation**, after the code it describes exists.
- **The KB curate run is the leader's**, after Wave 5's other doc edits land, since it
  spans the whole roadmap's story, not one file.

---

## Wave 1 — pure logic

- [x] **1.1** **[module: platform worker]** Create `cmd/monthly-capacity/period.go`,
  `package main`. Add, verbatim from `design.md` D1–D3, including their doc comments:
  - `parsePeriod(raw string) (time.Time, error)`
  - `previousMonth(now time.Time, loc *time.Location) time.Time`
  - `resolvePeriod(raw string, now time.Time, loc *time.Location) (time.Time, error)`
  - `teslaIDPointer(id int64, wasSet bool) *int64`

  Import `internal/clock` and stdlib `time`. No other import needed in this file.
  `depends_on`: — · `parallel_ok`: yes (first task)

---

## Wave 2 — the runnable and the config loader

- [x] **2.1** **[module: platform worker]** Create `cmd/monthly-capacity/main.go`,
  `package main`. Following `cmd/poller/main.go`'s style (flag parsing, then a linear
  `main` body):
  - `flag.String("period", "", ...)` and `flag.Int64("tesla-id", 0, ...)`.
  - After `flag.Parse()`, use `flag.Visit` to detect whether `-tesla-id` was actually
    given (design.md D1) and build the `*int64` via `teslaIDPointer`.
  - Call `resolvePeriod(*periodFlag, clock.Now(), clock.Zone())`; on error, `log.Fatalf`
    (exit 1) before touching any config or database.
  - Call `config.LoadDatabase()`; on error, `log.Fatalf`.
  - Build a `*pgxpool.Pool` from the returned DSN; on error, `log.Fatalf`.
  - Call `charging.NewMonthlyCapacityCalculator(pool).Calculate(ctx, period, teslaID)`; on
    error, `log.Fatalf`.
  - On success, print the report line verbatim from design.md D5 and exit 0.
  - A short package doc comment: what the tool does, its two flags and their defaults, and
    one line noting it is local-only (not part of the deployed image, T1) — do **not** cite
    any change or decision ID (`CLAUDE.md`'s B2/comment rule); state the reason itself.
  `depends_on`: 1.1 · `parallel_ok`: with 2.2

- [x] **2.2** **[module: platform worker]** `internal/config/config.go`: add
  `LoadDatabase() (string, error)`, verbatim from `design.md` D7, including its doc
  comment. Placed near `LoadMigration` (both are "smaller loader" functions).
  `depends_on`: — · `parallel_ok`: with 2.1

---

## Wave 3 — tests

Every expected value is fixed in `design.md` §Test Contract. **Assert that contract.**

- [x] **3.1** **[module: platform worker]** Create `cmd/monthly-capacity/period_test.go`,
  `package main`. Cover Test Contract **Group A** (`previousMonth` A1–A4, `resolvePeriod`
  A5–A6) and **Group B** (`parsePeriod` B1–B4, `teslaIDPointer` B5–B7). Offline, no DB, no
  network — table-driven, mirroring `internal/app/monthly_capacity_step_test.go`'s style
  for the date-math cases.
  `depends_on`: 1.1 · `parallel_ok`: with 3.2

- [x] **3.2** **[module: platform worker]** `internal/config/config_test.go`: add
  `unsetDatabaseEnv` (mirrors `unsetMigrationEnv`'s exact shape — clears/restores
  `DATABASE_URL`) and `TestLoadDatabase_*` covering Test Contract **Group C** (C1–C4).
  Reuse the existing `withTempDir`/`writeEnvFile` helpers already in this file.
  `depends_on`: 2.2 · `parallel_ok`: with 3.1

---

## Wave 4 — verification (F13: tier 2 already did the wiring)

- [x] **4.1** **[module: platform worker]** Read `cmd/poller/main.go` and confirm the
  `app.NewProcessor(...)` call already passes `charging.NewMonthlyCapacityCalculator(pool)`
  (it does, since tier 2 — see design.md's Verification tasks table). Read
  `cmd/web/main.go` and confirm it builds only the gateway and needs no change (RD8 keeps
  the gateway untouched). **Do not edit either file.** Report both findings in this task's
  own status — a false "already fine" is not acceptable here; actually open both files.
  `depends_on`: — · `parallel_ok`: yes, any time

---

## Wave 5 — documentation (`CLAUDE.md` §Non-negotiables: docs track structural change)

- [x] **5.1** **[module: platform worker]** `cmd/monthly-capacity/README.md`: usage,
  both flags and their defaults, at least the four example invocations from design.md D1,
  and one clear line that this tool is local only — it is not part of the deployed
  production image (T1). Mirror `cmd/explore-tesla-api/README.md`'s shape and length.
  `depends_on`: 2.1 · `parallel_ok`: with 5.2, 5.3, 5.4

- [x] **5.2** **[module: platform worker]** `Makefile`:
  - Add `cmd-monthly-capacity` to the `.PHONY` list (`Makefile:74`, next to
    `cmd-poller-once`).
  - Add the target itself, verbatim from `design.md` D6, near the other `cmd-*` targets
    (`Makefile:730-761`).
  `depends_on`: 2.1 · `parallel_ok`: with 5.1, 5.3, 5.4

- [x] **5.3** **[module: platform worker]** Root `README.md`:
  - "Project Structure" tree: add a `cmd/monthly-capacity/` line, one sentence, next to the
    other `cmd/` entries.
  - "Architecture" → dependency graph: add one composition-root line,
    `cmd/monthly-capacity ─────► charging, config` (mirroring the existing
    `cmd/explore-tesla-api` line's format).
  - Do **not** edit the `internal/charging` Architecture-table row — it already documents
    `monthly_effective_capacity` from tier 1/2; verified current, not stale.
  `depends_on`: 2.1 · `parallel_ok`: with 5.1, 5.2, 5.4

- [x] **5.4** **[module: platform worker]** `cmd/README.md`: add a row to the binaries
  table for `cmd/monthly-capacity`, matching the existing rows' column shape (Binary /
  Command / Purpose / Lifetime). Note in the Purpose column that it is local-only, run by
  hand (T1, RD9) — not part of the two containerized binaries the table already calls out.
  `depends_on`: 2.1 · `parallel_ok`: with 5.1, 5.2, 5.3

- [x] **5.5** **[module: platform worker]** `internal/charging/AGENTS.md`: in the
  "Monthly effective pack capacity" subsection (§Public Interface), add one sentence
  noting `MonthlyCapacityCalculator` now has two callers — the nightly step
  (`internal/app`) and the manual `cmd/monthly-capacity` tool — both calling the same
  port, so neither can drift from the other's contract.
  `depends_on`: 2.1 · `parallel_ok`: with 5.1-5.4, 5.6

- [x] **5.6** **[module: platform worker]** `internal/config/AGENTS.md`:
  - §Public interface: add `LoadDatabase() (string, error)` — one paragraph, the same
    style as the existing `LoadMigration` entry: what it requires, what it skips, and why
    (mirrors `LoadMigration`, see design.md D7).
  - §Testing: one sentence noting `config_test.go` also covers `LoadDatabase` (Group C).
  `depends_on`: 2.2, 3.2 · `parallel_ok`: with 5.1-5.5

- [x] **5.7** **[module: platform worker]** `kkpa/context/architecture/nightly-cycle.md`:
  in the "Port map" table, the row `app | charging.MonthlyCapacityCalculator | charging |
  Calculate — step 4 only, once a month` gains a note that `cmd/monthly-capacity` calls
  the same port directly, on demand, bypassing `internal/app` entirely. Do **not** touch
  any other row, section, or file in `kkpa/context/` — the grep in design.md found no
  other guide invalidated by this tier.
  `depends_on`: 2.1 · `parallel_ok`: with 5.1-5.6

---

## Wave 6 — signals

- [x] **6.1** **[module: platform worker]** Run the cheap deterministic signals the
  `Test-Execution-Policy` allows: `gofmt -l ./cmd/monthly-capacity ./internal/config`,
  `go build ./...`, `go vet ./...`. All three should be clean repo-wide — this change adds
  one new package and one new function to an existing one; nothing else compiles
  differently. If anything fails outside those two paths, stop and report it rather than
  assuming it is expected.
  `depends_on`: 3.1, 3.2, 5.1-5.7 · `parallel_ok`: no

---

## Owner verification (`Test-Execution-Policy`: the owner runs the suite)

- [ ] **O1** **[owner]** Run the suite:
  ```bash
  make check
  ```
  (`build vet ui-guard i18n-guard money-guard tz-guard migration-guard boundary-guard
  archive-guard test`. No `make migrate-up` needed — this tier adds no migration.)
  Recorded as the owner's report, never claimed by the assistant. Work that is complete
  but unexecuted is **`awaiting-user-verification`**, never `done`.

- [ ] **O2** **[owner]** Optional manual spot-check: run
  `make cmd-monthly-capacity PERIOD=<a real past month> TESLA_ID=<a real vehicle id>`
  against a real `DATABASE_URL` and confirm the printed summary line matches what the
  `charging.monthly_effective_capacity` table actually holds for that vehicle and month.
  Not required before archiving — a verification convenience, not a gate.

## Cross-module tasks the leader owns

- [ ] **L1** **[leader]** Run the `kkpa-context-curate` skill to create
  `kkpa/context/entities/vehicle-monthly-metrics/guide.md` (RD10) — the whole
  monthly-metrics story end to end: the table (tier 1), the estimator (tier 1), the
  nightly step (tier 2), this tier's CLI, and where a future second metric would go
  (RD12, deferred). Mirror `kkpa/context/entities/vehicle-metrics/guide.md`'s shape and
  depth. Add its glossary/routing rows to `kkpa/context/INDEX.md` (e.g. `monthly
  capacity`, `pack capacity`, `effective capacity`, `capacity backfill`).
  `depends_on`: 5.1-5.7 · `parallel_ok`: no

- [ ] **L2** **[leader]** Confirm `go build ./...` / `go vet ./...` are green repo-wide
  once Wave 6 lands, and re-run `make tz-guard`, `make boundary-guard`,
  `make archive-guard` (this change's `Makefile`/`kkpa/context/` edits are exactly the
  kind of touch those guards watch).

- [ ] **L3** **[leader]** When this change archives, update
  `openspec/roadmaps/RM52-vehicle-monthly-metrics.md`'s tier 3 status to `[x]` — this
  closes the roadmap. Also update the "Tiers" table's own status legend row if all three
  now read `[x]`.

- [ ] **L4** **[leader]** Re-read `openspec/roadmaps/backlog.md` item 7 (trim-exact pack
  capacity), per the roadmap's own "Future work" note: *"gets much weaker once this
  measures the real pack. Re-read it after tier 3 and decide whether it still earns its
  place."* `backlog.md` is not a granted path for the platform worker in this tier — this
  is the leader's call, not part of this change's own scope.
