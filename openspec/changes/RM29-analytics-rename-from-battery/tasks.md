# Tasks — RM29-analytics-rename-from-battery

Ownership legend: **[module: battery worker]** — inside `internal/battery/` (soon
`internal/analytics/`), the sandboxed worker's own territory. **[leader]** — outside that
module (six call-site files, root docs, other modules' `AGENTS.md`, the OpenSpec specs
folder) — a module worker may not edit another module's files, so these are leader-owned
per the pipeline's sandbox rule. See design.md D1–D6 for the full rationale behind each
group.

**Hard ordering constraint (design.md, Risks):** the package rename (1.x) and every
call-site re-point (2.x) touch a tree that does not compile until *all* of them land
together — there is no safe intermediate compiling state between "package battery, six
importers" and "package analytics, six importers." Wave 1 and Wave 2 are therefore executed
as **one atomic wave**: both must complete before anything is committed or before `go
build ./...` is run for verification. Tasks within the wave are listed with
`depends_on` for planning/parallel-authoring purposes (e.g. 2.1–2.5 can be drafted in any
order since they touch disjoint files), but none of them is individually buildable — the
wave lands as a unit.

## Wave 1 — module rename (module: battery worker)

> Moves in this wave use plain `mv`, **not** `git mv`: a module worker never runs git
> (staging and commits are the leader's, at wave boundaries). Nothing is lost — git records
> no renames, so `mv` + the leader's `git add -A` yields a commit identical to `git mv`,
> blame included (design.md D1).

- [ ] **1.1** Rename `internal/battery/battery.go` → `internal/analytics/analytics.go` via
  `mv`. Update the package clause `package battery` → `package analytics` and the
  leading doc comment ("Package battery is the platform's first derived-metrics module…"
  → "Package analytics is…", and its two internal "the battery module"-style references,
  if any, generalized to "the analytics module"). No identifier, signature, or logic
  change (design.md D1, D2).
  `depends_on`: — · `parallel_ok`: yes (own file)

- [ ] **1.2** `mv internal/battery/capacity.go internal/analytics/capacity.go`;
  package clause only.
  `depends_on`: — · `parallel_ok`: yes

- [ ] **1.3** `mv internal/battery/consumed.go internal/analytics/consumed.go`;
  package clause only.
  `depends_on`: — · `parallel_ok`: yes

- [ ] **1.4** `mv internal/battery/derive.go internal/analytics/derive.go`; package
  clause only.
  `depends_on`: — · `parallel_ok`: yes

- [ ] **1.5** `mv internal/battery/reader.go internal/analytics/reader.go`; package
  clause only. No change to `Reader`, `Efficiency`, `DayConsumption`, `NewReader`,
  `DefaultWindow`, `GapReconciliationWindow`, or the unexported `vehicleLookup` interface —
  all keep their exact names (design.md D2).
  `depends_on`: — · `parallel_ok`: yes

- [ ] **1.6** `mv internal/battery/consumed_test.go internal/analytics/consumed_test.go`;
  package clause only. No assertion, fixture, or test-name change (roadmap D10; proposal
  Testing section).
  `depends_on`: 1.3 (moves the file it tests) · `parallel_ok`: with 1.1/1.2/1.4/1.5

- [ ] **1.7** `mv internal/battery/derive_test.go internal/analytics/derive_test.go`;
  package clause only.
  `depends_on`: 1.4 · `parallel_ok`: yes

- [ ] **1.8** `mv internal/battery/reader_test.go internal/analytics/reader_test.go`;
  package clause only.
  `depends_on`: 1.5 · `parallel_ok`: yes

- [ ] **1.9** `mv internal/battery/AGENTS.md internal/analytics/AGENTS.md`. Update:
  `Agent-Name: battery` → `Agent-Name: analytics`; every `internal/battery`/`battery.*`
  reference throughout the file (Responsibility, Public interface, Allowed/forbidden
  imports, Data ownership, Testing sections) → `internal/analytics`/`analytics.*`; add the
  one forward-looking sentence noting RM29 scope (owns what the app calculates; gains a
  database from tier 3) per design.md D5. Do not otherwise rewrite the file's content or
  claims — this tier changes naming only.
  `depends_on`: — · `parallel_ok`: yes

## Wave 2 — call-site re-point (leader — cross-module, same atomic wave as Wave 1)

- [ ] **2.1** `cmd/poller/main.go` — re-point the import
  `"github.com/cristianpena/magus-tesla-api/internal/battery"` → `.../internal/analytics`;
  update every `battery.` qualifier (`battery.NewReader`, `battery.DefaultWindow`,
  `battery.GapReconciliationWindow`) → `analytics.`; the local variable `batteryReader` and
  the `newGapReconciler` parameter name `batteryReader battery.Reader` → `analyticsReader
  analytics.Reader` for vocabulary consistency (design.md D2's "same-file consistency"
  reasoning applies equally to composition-root local names). No change to
  `reconcilingCollector`'s or `newGapReconciler`'s logic (proposal "Modules Affected" —
  explicitly verbatim this tier).
  `depends_on`: 1.1–1.9 (needs the new package to exist) · `parallel_ok`: with 2.2–2.5

- [ ] **2.2** `cmd/web/main.go` — re-point the import and the `Deps{BatteryReader:
  battery.NewReader(...)}` construction → `Deps{AnalyticsReader: analytics.NewReader(...)}`.
  `depends_on`: 1.1–1.9, 2.3 (the `Deps.AnalyticsReader` field must exist first) ·
  `parallel_ok`: no (ordered after 2.3)

- [ ] **2.3** `internal/gateway/gateway.go` — re-point the import; rename the `Deps`
  struct field `BatteryReader battery.Reader` → `AnalyticsReader analytics.Reader`
  (design.md D2) and its single forwarding use
  (`BatteryReader: d.BatteryReader` → `AnalyticsReader: d.AnalyticsReader`). Update the
  field's doc comment ("BatteryReader is the battery module's read port… Injected from
  cmd/web via battery.NewReader(...)") to match.
  `depends_on`: 1.1–1.9 · `parallel_ok`: with 2.1, 2.4, 2.5

- [ ] **2.4** `internal/gateway/handlers/handlers.go` — re-point the import; rename
  `Deps.BatteryReader` → `Deps.AnalyticsReader` and the unexported `Handler.batteryReader`
  field → `analyticsReader`, plus the one forwarding line in the constructor
  (`batteryReader: d.BatteryReader` → `analyticsReader: d.AnalyticsReader`). Update the
  matching doc comment.
  `depends_on`: 1.1–1.9, 2.3 (consumes `Deps.AnalyticsReader`) · `parallel_ok`: no

- [ ] **2.5** `internal/gateway/handlers/history.go` — re-point the import; update every
  `battery.` qualifier (`battery.Reader`, `battery.DayConsumption`) and the
  `h.batteryReader.ConsumedByDay(...)` call → `h.analyticsReader.ConsumedByDay(...)`.
  `depends_on`: 1.1–1.9, 2.4 (consumes the renamed `Handler.analyticsReader` field) ·
  `parallel_ok`: no

- [ ] **2.6** `internal/gateway/handlers/history_test.go` — re-point the import; update
  every `battery.` qualifier; rename `fakeBatteryReader` → `fakeAnalyticsReader`,
  `newHandlerForHistoryWithBattery` → `newHandlerForHistoryWithAnalytics`,
  `TestHandler_BatteryReaderDepsForwarding` → `TestHandler_AnalyticsReaderDepsForwarding`,
  and every local variable literally named `batteryReader` in this file → `analyticsReader`
  (design.md D2's same-file-consistency call — these three names aren't in the proposal's
  bullet list but are stale vocabulary echoes of the field this file exists to test). No
  assertion, fixture value, or test-scenario change — names only.
  `depends_on`: 1.1–1.9, 2.4, 2.5 · `parallel_ok`: no

## Wave 3 — OpenSpec specs folder move (leader — hard dependency before archive)

- [ ] **3.1** `git mv openspec/specs/battery/ openspec/specs/analytics/`. Hand-edit
  `spec.md`'s `# battery Specification` → `# analytics Specification` and the `## Purpose`
  paragraph's opening "Derived battery analytics over stored telemetry…" → "Derived
  analytics over stored telemetry…" (design.md D4). Do not touch the `## Requirements`
  body — deltas apply against it unchanged.
  `depends_on`: none from Wave 1/2 (independent content), but **must complete before Task
  5.2** (`openspec validate --changes --strict`) and before any archive step — the CLI
  rejects `## MODIFIED Requirements` against a nonexistent main spec (design.md D4). ·
  `parallel_ok`: yes, with all of Wave 1/2

## Wave 4 — documentation (leader — docs-track-structural-change, same change per CLAUDE.md)

Each of these is independent of the others (disjoint files) and independent of Wave
3, but depends on Wave 1/2 having landed so the doc content being written describes the
post-rename state truthfully.

- [ ] **4.1** `README.md` — Project Structure tree (`internal/battery/` line → `internal/
  analytics/`), Architecture table row, and the Dependency graph code block (5 occurrences:
  `cmd/web`'s import list, `cmd/poller`'s import list, `gateway`'s import list,
  `handlers`'s import list, and the `LAYER 2` row `battery ────────────► account,
  manualcharge, telemetry` → `analytics ────────────► …`). Design.md D6 has the verified
  line-by-line list.
  `depends_on`: 1.1–2.6 · `parallel_ok`: yes, with 4.2–4.6

- [ ] **4.2** `ai/architecture.md` §4 — the Target Directory Blueprint's `battery/` example
  domain module entry and its two file-comment lines; the §6 DTO-naming example
  `battery.State`; the §2 import-cycle worked example's two `battery` mentions. Do
  **not** touch §7's `battery_level` column-name mention (telemetry's data, unrelated —
  design.md D6).
  `depends_on`: 1.1–2.6 · `parallel_ok`: yes

- [ ] **4.3** `internal/gateway/AGENTS.md` — the `Deps.BatteryReader battery.Reader` bullet
  and its three follow-on lines (`battery.NewReader(...)`, "internal/battery owns no
  database", "no batterydb package"). Do **not** touch the "Battery consumed" chart-panel
  prose, the `battery-info` `data-testid` example, or the `account, charging, battery,
  drives` domain-grouping example list — domain-word/forward-looking examples, not this
  module's Go identifiers (design.md D6).
  `depends_on`: 1.1–2.6 · `parallel_ok`: yes

- [ ] **4.4** `internal/telemetry/AGENTS.md` — the five `internal/battery` prose
  references (the `GapWriter` port's intended-caller documentation, `poll_attempts`
  caller-boundary note, the RM27 taper-curve aside). Prose only — telemetry never imports
  battery/analytics, confirmed, and this tier changes nothing about that boundary.
  `depends_on`: 1.1–2.6 · `parallel_ok`: yes

- [ ] **4.5** `docs/battery-consumed-graph.md` — update the ~12 `internal/battery/*.go`
  file-path references and `battery.ConsumedByDay`/`battery.GapReconciliationWindow`
  qualifiers throughout the pipeline walkthrough to `internal/analytics/*.go` /
  `analytics.*`. **Do not rename the file itself** — the filename names the domain feature
  ("Battery Consumed" pipeline), not the Go package (design.md D6).
  `depends_on`: 1.1–2.6 · `parallel_ok`: yes

- [ ] **4.6** `openspec/roadmaps/backlog.md` — §7's header (`## 7. battery / tesla /
  account / telemetry — Trim-exact pack capacity` → `## 7. analytics / tesla / account /
  telemetry — …`) and its two `internal/battery`/`internal/battery/capacity.go` path
  references. **Do not** edit the historical citations `battery-add-efficiency-metric` or
  decision ID `D1b` — they name an already-archived change verbatim; archived change names
  are never retconned (design.md D6).
  `depends_on`: 1.1–2.6 · `parallel_ok`: yes

(`cmd/README.md` was checked and carries no `battery` reference — no task.)

## Wave 5 — verification (assistant-run signals, then owner-run suite)

- [ ] **5.1** Run and report: `go build ./...`, `go vet ./...`, `gofmt -l .` (expect clean
  — no output — per design.md's import-ordering risk note; if `gofmt -l` lists a touched
  file, run `gofmt -w` on it). Also `make ui-guard`, `make i18n-guard`, `make money-guard`
  as standing standalone guards, expected no-ops for a non-HTML, non-string, non-monetary
  change.
  `depends_on`: 1.1–4.6 (everything must have landed for the tree to build) ·
  `parallel_ok`: no (final gate)

- [ ] **5.2** Run `openspec validate --changes --strict` and report the result. This is
  the change's status-check substitute — `openspec status`/`openspec instructions` reject
  the uppercase `RM29-…` change name (their validator demands lowercase); `validate` does
  not.
  `depends_on`: 3.1 (specs folder must exist), 5.1 · `parallel_ok`: no

- [ ] **5.3** Hand off to the owner. Exact commands to paste (not run by the assistant,
  per the Test-Execution-Policy):
  ```
  go test ./internal/analytics/... ./internal/gateway/... ./cmd/...
  ```
  or the full suite:
  ```
  go test ./...
  ```
  Until the owner runs one of these and reports the result, this tier's status is
  **awaiting-user-verification**, never "done" (design.md "Verification signals").
  `depends_on`: 5.1, 5.2 · `parallel_ok`: no
