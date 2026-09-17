# Tasks — RM62-platform-add-nightly-cycle-diagram

Ownership: every task is **[cross-cutting: platform worker]**. No `internal/`
module is touched. Paths: `kkpa/docs/diagrams/nightly-job/` (new) and
`kkpa/context/architecture/nightly-cycle.md` (one section edited).

**No unit tests** — roadmap Decision 1 / design.md "Testing". No task below
writes a `_test.go` file; this change produces no Go code at all.

**Facts come from the code, not from the KB guide alone.** Every task that
authors diagram content must re-read the exact file+symbol design.md D4/D5
cites — the KB guide is a cross-check, never the source. Do not copy any fact
from the published Artifact linked in `nightly-cycle.md`'s "Rendered view"
section — design.md D7 confirms it is stale.

## Parallel-safety

Waves 1, 2, and 3 each produce one diagram's file set and touch no file any
other wave touches. They may run in parallel, by the same or different
agents. Wave 4 (the KB edit) needs the real, delivered filenames from all
three, so it is sequenced last.

---

## Wave 1 — `nightly-cycle-workflow` (workflow diagram)

- [x] **1.1** Read `internal/app/processor.go`'s `ProcessVehicleData`,
  `processChargingData`, `recalculateAnalytics`, `monthlyCapacityPeriod`,
  `callMonthlyCapacityCalculator`, `recordRun` — confirm design.md D4's fact
  table against the current file (index lag / prior edits could have moved
  something). Do not start authoring before this read.
  `depends_on`: — · `parallel_ok`: with 2.1, 3.1

- [x] **1.2** Read `~/.claude/skills/archify/schemas/workflow.schema.json`,
  `~/.claude/skills/archify/schemas/common.schema.json`, and one workflow
  example under `~/.claude/skills/archify/examples/` (`*.workflow.json`).
  Read only those files, per archify's fast-authoring path.
  `depends_on`: — · `parallel_ok`: with 2.2, 3.2

- [x] **1.3** Author `kkpa/docs/diagrams/nightly-job/nightly-cycle-workflow.json`
  (schema `workflow`, `meta.quality_profile: "showcase"`). Content, per
  design.md D4/D6 and roadmap tier-4 scope:
  - Four steps in order: sync fleet data (step 1), mirror charging data (step
    2), recalculate analytics (step 3), measure monthly capacity (step 4).
  - The step-1 failure short-circuit: steps 2-4 run only when step 1
    succeeds.
  - Step 2's and step 3's per-vehicle loop (one pass per distinct vehicle).
  - Step 3's two halves per vehicle (metrics reconciliation, then gap
    reconciliation) and that a failed metrics half skips that vehicle's gap
    half.
  - Step 4's gate: runs only on the first calendar day of the month,
    measuring the previous month, every vehicle in one call.
  - One `poll_runs` summary recorded on every exit path, including the
    step-1 failure path.
  - One clear main path, side branches for the short-circuit and the step-4
    gate, at most 12 primary nodes. Labels: B2 words, no glossary needed —
    reuse this codebase's own log-line topics (`metrics reconciliation:`,
    `gap reconciliation:`, `session mirror`, `monthly capacity:`) where they
    fit (design.md D9).
  `depends_on`: 1.1, 1.2 · `parallel_ok`: no

- [x] **1.4** Validate:
  `node bin/archify.mjs validate workflow kkpa/docs/diagrams/nightly-job/nightly-cycle-workflow.json --quality showcase --json`
  (run from `~/.claude/skills/archify/`). Fix diagnosed issues and re-validate
  until a showcase pass: all artifact checks, 0 composition errors, 0
  warnings.
  `depends_on`: 1.3 · `parallel_ok`: no

- [x] **1.5** Deliver:
  `node bin/archify.mjs deliver workflow kkpa/docs/diagrams/nightly-job/nightly-cycle-workflow.json kkpa/docs/diagrams/nightly-job/nightly-cycle-workflow.html --quality showcase --json`.
  A non-zero exit is not success — fix and retry.
  `depends_on`: 1.4 · `parallel_ok`: no

- [x] **1.6** Run
  `node bin/archify.mjs visual-check kkpa/docs/diagrams/nightly-job/nightly-cycle-workflow.html --json`
  and record the result (pass/fail, any overflow at the checked viewports).
  `depends_on`: 1.5 · `parallel_ok`: no

---

## Wave 2 — `nightly-cycle-sequence` (sequence diagram)

- [x] **2.1** Read `internal/app/processor.go` (all four `p.<port>.<Method>`
  call sites), `internal/telemetry/service.go` (`listStates`,
  `attemptVehicle`, `collectChargingHistory` — the four paid Fleet API calls
  and the three DB writes inside `CollectAll`), and
  `internal/analytics/recalculate.go` (confirm `analytics` never imports
  `tesla` — every one of its calls is a DB read or write). Confirm design.md
  D4's fact table against the current files before authoring.
  `depends_on`: — · `parallel_ok`: with 1.1, 3.1

- [x] **2.2** Read `~/.claude/skills/archify/schemas/sequence.schema.json`,
  `~/.claude/skills/archify/schemas/common.schema.json`, and one sequence
  example under `~/.claude/skills/archify/examples/` (`*.sequence.json`).
  `depends_on`: — · `parallel_ok`: with 1.2, 3.2

- [x] **2.3** Author
  `kkpa/docs/diagrams/nightly-job/nightly-cycle-sequence.json` (schema
  `sequence`, `meta.quality_profile: "showcase"`). Content, per design.md
  D4:
  - Participants: `app` (Processor), `telemetry`, `tesla`/Fleet API
    (external), `charging`, `analytics`, the database.
  - `app → telemetry.CollectAll` (step 1), and inside it: the four paid
    Fleet API calls (`ListVehicles`, `WakeUp`, `VehicleData`,
    `ChargingHistory`) each marked as a paid external call, plus the three
    DB writes (`vehicle_snapshots`, `poll_attempts`, `supercharger_history`)
    each marked as a database write.
  - `app → telemetry.SuperchargerHistoryByVehicleUpdatedSince` (DB read) and
    `app → charging.MirrorSessions` (DB write) — step 2.
  - `app → analytics.Reconcile`, `app → analytics.ConsumedByDay`,
    `app → analytics.ReconcileWindow` — step 3, each marked as a database
    call, none a Fleet API call.
  - `app → charging.Calculate` — step 4, database read + write.
  - Every message marked plainly as either "paid Fleet API call" or
    "database read"/"database write" — the one distinction RD's own scope
    asks this diagram to carry.
  `depends_on`: 2.1, 2.2 · `parallel_ok`: no

- [x] **2.4** Validate:
  `node bin/archify.mjs validate sequence kkpa/docs/diagrams/nightly-job/nightly-cycle-sequence.json --quality showcase --json`.
  Fix and re-validate to a showcase pass.
  `depends_on`: 2.3 · `parallel_ok`: no

- [x] **2.5** Deliver:
  `node bin/archify.mjs deliver sequence kkpa/docs/diagrams/nightly-job/nightly-cycle-sequence.json kkpa/docs/diagrams/nightly-job/nightly-cycle-sequence.html --quality showcase --json`.
  `depends_on`: 2.4 · `parallel_ok`: no

- [x] **2.6** Run
  `node bin/archify.mjs visual-check kkpa/docs/diagrams/nightly-job/nightly-cycle-sequence.html --json`
  and record the result.
  `depends_on`: 2.5 · `parallel_ok`: no

---

## Wave 3 — `nightly-cycle-derivation` (dataflow diagram, RD9)

- [x] **3.1** Read `internal/analytics/consumption.go`'s `deriveConsumption`
  and `consumed.go`'s `deriveVehicleMetrics`/`sumSuperchargerPctBetween`/
  `sumManualPctBetween` in full. Confirm design.md D5's two quoted facts
  (the `chargePct` parameter; `KmPerPctCalc` dividing by `consumed`, not the
  raw drop) against the current file — this is the exact regression a stale
  diagram would repeat.
  `depends_on`: — · `parallel_ok`: with 1.1, 2.1

- [x] **3.2** Read `~/.claude/skills/archify/schemas/dataflow.schema.json`,
  `~/.claude/skills/archify/schemas/common.schema.json`, and one dataflow
  example under `~/.claude/skills/archify/examples/` (`*.dataflow.json`).
  `depends_on`: — · `parallel_ok`: with 1.2, 2.2

- [x] **3.3** Author
  `kkpa/docs/diagrams/nightly-job/nightly-cycle-derivation.json` (schema
  `dataflow`, `meta.quality_profile: "showcase"`). Content, per design.md D6:
  - Inputs: the previous day's snapshot, the current day's snapshot, the
    Supercharger sessions and manual charge entries in the matching span.
  - Transform 1: the day's charge total (`chargePct`) — sum of every
    matching session's and entry's battery-percentage gain.
  - Transform 2: `deriveConsumption` — produces `distance_traveled_km_calc`,
    `battery_used_pct_calc` (raw drop), `consumed_pct` (raw drop plus the
    charge total), and — only when `consumed_pct` is positive —
    `km_per_pct_calc` and `estimated_range_km_calc`, both divided by
    `consumed_pct`.
  - One annotation stating the MAG-81 change in plain B2 words (design.md D6
    gives the exact wording to use).
  - Output: one `vehicle_metrics` row.
  - Leave out the four TPMS deltas and the flag/gap detection — design.md D6
    explains why.
  `depends_on`: 3.1, 3.2 · `parallel_ok`: no

- [x] **3.4** Validate:
  `node bin/archify.mjs validate dataflow kkpa/docs/diagrams/nightly-job/nightly-cycle-derivation.json --quality showcase --json`.
  Fix and re-validate to a showcase pass.
  `depends_on`: 3.3 · `parallel_ok`: no

- [x] **3.5** Deliver:
  `node bin/archify.mjs deliver dataflow kkpa/docs/diagrams/nightly-job/nightly-cycle-derivation.json kkpa/docs/diagrams/nightly-job/nightly-cycle-derivation.html --quality showcase --json`.
  `depends_on`: 3.4 · `parallel_ok`: no

- [x] **3.6** Run
  `node bin/archify.mjs visual-check kkpa/docs/diagrams/nightly-job/nightly-cycle-derivation.html --json`
  and record the result.
  `depends_on`: 3.5 · `parallel_ok`: no

---

## Wave 4 — Knowledge-base reference

- [ ] **4.1** Edit `kkpa/context/architecture/nightly-cycle.md`'s existing
  `## Rendered view (visual map)` section: add the three local files
  delivered above (`nightly-cycle-workflow.html`, `nightly-cycle-sequence.html`,
  `nightly-cycle-derivation.html`, relative to
  `kkpa/docs/diagrams/nightly-job/`), each with one line saying what it
  shows. Extend the section's existing precedence sentence ("the code is the
  source of truth, then this guide, then the artifact") to name the local
  files alongside the published Artifact — code first, then the guide, then
  any rendering. Do not remove or edit the existing published-Artifact
  paragraph; design.md D7 confirms fixing that link is out of this tier's
  scope.
  `depends_on`: 1.5, 2.5, 3.5 · `parallel_ok`: no

- [ ] **4.2** Confirm `kkpa/context/INDEX.md` needs no new row — design.md D8
  already reasons through this; re-check it against the file as it stands at
  implementation time (a later change could have added new nightly-cycle
  rows since this design was written) and note the outcome in the final
  report.
  `depends_on`: — · `parallel_ok`: with 4.1

---

## Verification (owner-run; see Test-Execution-Policy)

This tier ships no Go code — `go build`, `go vet`, `gofmt`, and every
per-language guard except one have nothing to check.

```
make archive-guard
```

Confirms this branch touches nothing under `openspec/changes/archive/`. No
other command in the Test-Execution-Policy applies to this change.
