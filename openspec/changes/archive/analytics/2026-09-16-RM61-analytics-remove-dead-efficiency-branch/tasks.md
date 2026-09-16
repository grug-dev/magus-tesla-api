# Tasks — RM61-analytics-remove-dead-efficiency-branch

Ownership legend: **[module: analytics worker]** — inside `internal/analytics/` only.
**[leader]** — outside the module sandbox (`cmd/web`, `cmd/poller`, `internal/gateway`,
`internal/app`), so a module worker may not touch these without an explicit grant. **[owner]**
— the human. See design.md **D1–D3** for the rationale behind each group.

No design gate applies here (design.md's own header: no database object is touched).
Implementation may start immediately once this proposal is accepted.

## Ordering constraints

- **Wave 1 is the whole deletion and is internally ordered**: 1.1 (delete the dead files) must
  land before 1.2 (shrink the surviving files that referenced them) can compile clean, and
  1.2 before 1.3 (tests) can compile. 1.4 and 1.5 are comment/doc-reference fixes that can run
  any time after 1.1 but are grouped here for the same commit.
- **This tier's build goes red the moment `analytics.go`/`reader.go` change, and stays red
  until the leader's Wave 2 lands** (the two `NewReader` call sites and the two test-fake
  stubs live outside this module's sandbox). This is expected and matches the roadmap's own
  tier description — do not treat a red `go build ./...` outside `internal/analytics` as a
  regression until Wave 2 is done; do treat one still red *after* Wave 2 as a real finding.
- **Wave 3 (tests) depends on Wave 1 being fully applied** — a test file referencing a deleted
  symbol will not compile.
- **Wave 4 (docs) can run in parallel with Wave 3** — different files, no dependency between
  them.
- **No comment in any file this tier touches may cite `design.md`, a decision id (`D1`…`D3`),
  or `RM61`/`RD9`/`MAG-40`.** Write the reason itself. This binds every wave below.
- **This tier's central deliverable is a subtraction, not an addition** — resist the urge to
  add anything not named in proposal.md §"What Changes". If a task below seems to call for a
  new abstraction, stop and report it instead of adding one.

---

## Wave 1 — the deletion (module: analytics)

- [x] **1.1** **[module: analytics worker]** Re-run the verification grep first, as the gate on
  everything below:
  ```
  grep -rn RecentEfficiency --include="*.go" --include="*.templ" .
  ```
  Confirm the result matches proposal.md §"Verification" exactly (7 files, no handler, no
  template, no `cmd` production call). **If a new caller has appeared since this proposal was
  written, STOP and report it — do not proceed with deletion.** Assuming it matches, delete:
  - `internal/analytics/derive.go` (whole file: `deriveEfficiency`, `socReadings`)
  - `internal/analytics/capacity.go` (whole file: `packCapacityKWh`, `capacityFor`)
  - From `internal/analytics/analytics.go`: the `RecentEfficiency` method from the `Reader`
    interface (with its doc comment), the `Efficiency` struct, the `DefaultWindow` constant,
    and the package doc comment at the top of the file (design.md **D2** — rewrite it to
    describe the module's surviving responsibility: deriving and serving `vehicle_metrics`
    through `ConsumedByDay`/`OdometerDeltaByDay`/`BatteryLevelByDay`/`LatestMetricsForVehicles`,
    dropping every mention of efficiency, Wh/km, or a `car_type` capacity table).
  - From `internal/analytics/reader.go`: the `(*reader).RecentEfficiency` method, `carTypeFor`,
    the `vehicleLookup` interface, `chargingSourceLimit`, `sumSuperchargerKWh`,
    `sumManualKWh` (design.md **D1** — both are unreachable once `RecentEfficiency` is gone),
    and the `telemetry`, `supercharger`, `manual`, `account`, `window`, `now` fields from the
    `reader` struct — it collapses to `metrics vehicleMetricsStore` alone.
  - `NewReader`'s signature shrinks to `func NewReader(pool *pgxpool.Pool) Reader`, its body to
    `return &reader{metrics: analyticsdb.New(pool)}`. Its doc comment is rewritten — it
    currently explains the now-deleted `telemetryReader`/`supercharger`/`manual`/`acct`/
    `window` parameters; rewrite it to describe only the surviving `pool` parameter.
  - Confirm `internal/analytics/recalculate.go` is untouched by this task —
    `NewRecalculator`'s own four parameters are unrelated (design.md Context fact 3).
  `depends_on`: — (verification is the gate) · `parallel_ok`: no (blocks everything else)

- [x] **1.2** **[module: analytics worker]** Fix the one in-module file left referencing a
  deleted symbol or the old six-argument constructor:
  - `internal/analytics/db_integration_test.go`: shrink `newRealReader`
    (`func newRealReader(pool *pgxpool.Pool) Reader`) to call `NewReader(pool)` alone — drop
    the `telemetry.NewReader(pool)`, `charging.NewSuperchargerSessionAnalyticsReader(pool)`,
    `charging.NewReader(pool)`, `&fakeVehicleLookup{}`, `DefaultWindow` arguments (design.md
    Context fact 6). Update its doc comment, which currently explains those arguments. Do
    **not** touch `newRealRecalculator` — `NewRecalculator`'s own signature is unchanged.
  - Same file, line ~1249: `recordingSuperchargerReader`'s doc comment contrasts itself with
    "`reader_test.go`'s own `fakeSuperchargerReader` (which exists to test `RecentEfficiency`'s
    own call shape...)". Both `fakeSuperchargerReader` and `RecentEfficiency` are gone after
    task 1.3 — reword the comment to describe `recordingSuperchargerReader`'s own purpose
    without the now-nonexistent cross-reference (design.md Context fact 5).
  - `internal/analytics/recalculate.go:33`: the `recalcOverlap` doc comment says it "mirrors
    reader.go's own `chargingSourceLimit` named-constant convention" — `chargingSourceLimit` no
    longer exists after task 1.1. Reword to describe the named-constant convention itself,
    without naming a deleted constant (design.md Context fact 7).
  `depends_on`: 1.1 · `parallel_ok`: with 1.4, 1.5

- [x] **1.3** **[module: analytics worker]** `internal/analytics/reader_test.go` — remove
  exactly the coverage proposal.md §"Tests removed" names and nothing else:
  - The eight `TestRecentEfficiency_*` functions.
  - The four fakes those tests alone construct: `fakeTelemetryReader`,
    `fakeSuperchargerReader`, `fakeManualReader`, `fakeVehicleLookup`, and the two helpers
    `fp`/`sp` (confirm no surviving test in this file or any other `_test.go` file in this
    package calls `fp(`, `sp(`, or references these four fake types before deleting — design.md
    **T7**).
  - **Leave untouched**: `fakeVehicleMetricsStore` and the five `TestReader_*` functions
    (`ConsumedByDay_ReadsPrecomputedRows`, `OdometerDeltaByDay_ClampsOnRead`,
    `ConsumedByDay_ExcludesPredecessorlessRow`, `OdometerDeltaByDay_ExcludesPredecessorlessRow`,
    `VehicleMetricsStoreError_Propagates`) — none of them call `RecentEfficiency` or any symbol
    task 1.1 deletes.
  - Delete `internal/analytics/derive_test.go` in full (all eight `Test*` functions and the
    `snap()` helper — design.md confirms `snap()` has no caller outside this file and
    `reader_test.go`'s now-deleted `TestRecentEfficiency_*` tests).
  - There is no dedicated `capacity_test.go` to delete — `capacityFor` had no test file of its
    own.
  `depends_on`: 1.1 · `parallel_ok`: with 1.2, 1.4, 1.5

- [x] **1.4** **[module: analytics worker]** Run the Test Contract's checks **T1–T8**
  (design.md §"Test Contract") after 1.1–1.3 land. Report each one's result — pass/fail, and
  for T1/T7 the actual grep output if non-empty. This is the acceptance check for the whole
  wave, not a formality: a task above is not "done" until its corresponding contract check
  passes.
  `depends_on`: 1.1, 1.2, 1.3 · `parallel_ok`: no

- [x] **1.5** **[module: analytics worker]** Run the cheap deterministic signals the
  `Test-Execution-Policy` allows, scoped to this module first: `gofmt -l ./internal/analytics`,
  `go vet ./internal/analytics/...`, `go build ./internal/analytics/...`. These three should be
  clean at this point even though the **repo-wide** `go build ./...` is still red until Wave 2
  (leader) lands — see "Ordering constraints" above.
  `depends_on`: 1.1, 1.2, 1.3 · `parallel_ok`: with 1.4

---

## Wave 2 — cross-module compile fix (leader-owned; outside the analytics sandbox)

- [x] **2.1** **[leader]** `cmd/web/main.go:70` — shrink the `analytics.NewReader(...)` call to
  `analytics.NewReader(pool)`. Rewrite the comment immediately above it (currently explains
  that the window argument is "required by the signature but unused by ConsumedByDay" — that
  argument no longer exists, so the comment's whole premise is gone; state instead, briefly,
  what the port is used for here).
  `depends_on`: 1.1 (analytics's `NewReader` signature must be final) · `parallel_ok`: with 2.2

- [x] **2.2** **[leader]** `cmd/poller/main.go:125` (and its own near-identical comment,
  around line 107) — same fix as 2.1: shrink the call to `analytics.NewReader(pool)`, rewrite
  the comment that explains the now-nonexistent window argument.
  `depends_on`: 1.1 · `parallel_ok`: with 2.1

- [x] **2.3** **[leader]** `internal/gateway/handlers/history_test.go:145-146` — remove the
  `(*fakeAnalyticsReader) RecentEfficiency` stub (the one that panics). `analytics.Reader` no
  longer declares this method, so leaving the stub is harmless to compilation but is dead code;
  remove it so the fake's method set matches the interface it implements, method for method.
  `depends_on`: 1.1 · `parallel_ok`: with 2.1, 2.2, 2.4

- [x] **2.4** **[leader]** `internal/app/processor_test.go:241-244` — remove the
  `(fakeAnalyticsReader) RecentEfficiency` stub (the no-op one), same reasoning as 2.3.
  `depends_on`: 1.1 · `parallel_ok`: with 2.1, 2.2, 2.3

- [x] **2.5** **[leader]** Run `go build ./...` and `go vet ./...` **repo-wide** once 2.1–2.4
  are all done. This is design.md **T4** — expected clean. If either fails anywhere, stop and
  report; do not assume it is expected at this point in the tier.
  `depends_on`: 2.1, 2.2, 2.3, 2.4 · `parallel_ok`: no

---

## Wave 3 — documentation (`CLAUDE.md` §Non-negotiables: docs track structural change)

- [x] **3.1** **[module: analytics worker]** `internal/analytics/AGENTS.md`:
  - §"Responsibility" — rewrite the paragraph that currently describes the module's "first
    (and currently only) metric" as rolling energy-per-kilometre with a `car_type` capacity
    table (design.md **D3**). State instead what the module does after this change: it derives
    and serves the precomputed `vehicle_metrics` figures (battery-consumed, odometer delta,
    battery level, latest status) through its `Reader` port, plus the `charge_gaps` worklist.
    Drop the "Full design rationale... `battery-add-efficiency-metric/design.md`" sentence —
    that archived change described the now-deleted branch specifically.
  - §"Public interface (the port)" — drop the `RecentEfficiency` row from the `Reader` method
    table.
  - §"Allowed / forbidden imports" — drop the `internal/account` entry from "May import" (design.md
    Context fact 4: after this change, `internal/analytics` imports no `internal/account`
    symbol anywhere, production or test).
  - §"Data ownership" — the sentence "The one non-database piece of module-local state is
    `capacity.go`'s `packCapacityKWh`..." appears **twice** in the current file. Both instances
    describe a file this change deletes; remove both sentences (do not replace them with
    anything — the module now has no non-database module-local state worth calling out).
  `depends_on`: 1.1, 1.2, 1.3 (describe the post-deletion state, not the pre-deletion one) ·
  `parallel_ok`: with 3.2

- [x] **3.2** **[module: analytics worker]** Confirm no other doc under `internal/analytics/`
  or the root `README.md` needs an edit. This change removes no module, no runnable, and no
  table — the root `README.md`'s "Project Structure" tree and "Architecture" table are
  unaffected. Report this confirmation rather than skipping it silently.
  `depends_on`: — · `parallel_ok`: with 3.1

- [x] **3.3** **[leader]** Sync this change's own `specs/analytics/spec.md` delta into
  `openspec/specs/analytics/spec.md` via the project's normal spec-sync step, at the point the
  pipeline calls for it (not necessarily this wave). Confirm the sync touches only
  `openspec/specs/`, never `openspec/changes/archive/`.
  `depends_on`: — (tracked here so it is not forgotten)

- [x] **3.4** **[leader]** Grep `kkpa/context/` for `RecentEfficiency`, `Efficiency`,
  `capacityFor`, `car_type` capacity, or `analytics.DefaultWindow`. If any guide describes the
  deleted branch, correct it in the same change (`CLAUDE.md` §Non-negotiables: the KB is
  included in "docs track structural change"). Report what was found, even if nothing was.
  `depends_on`: 1.1 · `parallel_ok`: with 3.1, 3.2

---

## Owner verification (`Test-Execution-Policy`: the owner runs the suite)

- [x] **O1** **[owner]** Run the suite. Nothing above may be reported as `done` on the
  assistant's say-so; work that is complete but unexecuted is **`awaiting-user-verification`**.
  ```bash
  make check
  ```
  (`make check` = `build vet lint ui-guard i18n-guard money-guard tz-guard migration-guard
  boundary-guard theme-guard vehicleref-guard tenancy-guard naming-guard archive-guard test`.
  No migration in this change, so `make migrate-up` is not needed first.
  `go test ./internal/analytics/...` runs this module's own suite specifically.)

## Cross-module tasks the leader owns

- [x] **L1** **[leader]** Confirm `go build ./...`/`go vet ./...` are green repo-wide once
  Wave 2 lands (design.md **T4** — tracked here as well as in task 2.5 so it is not missed at
  the wave boundary).
- [x] **L2** **[leader]** Confirm the root `README.md` needs no edit (task 3.2's finding,
  cross-checked): this change removes no module, no runnable, and no table from the schema it
  documents.
  `depends_on`: 3.2 · `parallel_ok`: yes
