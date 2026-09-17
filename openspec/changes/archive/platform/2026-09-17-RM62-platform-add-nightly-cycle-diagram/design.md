# Design — RM62-platform-add-nightly-cycle-diagram

## Context

`internal/app/processor.go`'s `ProcessVehicleData` runs four steps in order.
`kkpa/context/architecture/nightly-cycle.md` maps the code but is not a
picture. The roadmap's own goal: a person understands the cycle without
reading Go. RD9, added by the user after the roadmap was written, extends
that goal to `Recalculate` — the part of step 3 that turns a snapshot pair
into the `_calc` figures stored on `vehicle_metrics`.

This change writes the OpenSpec artifacts only. No diagram file exists yet —
tasks.md hands that work to whoever implements it next.

## D1 — One thin delta, on the existing `platform` capability

**Decision:** `specs/platform/spec.md` gains one `ADDED` requirement,
"Nightly Cycle Reference Diagrams," with four scenarios — each diagram
findable from the guide, plus the guide's precedence statement.

**Why not zero deltas, as first considered:** the dispatch explicitly allowed
a docs-only change to skip a spec, so that was tried first. `openspec
validate --strict` hard-refuses it: *"Change must have at least one delta. No
deltas found."* This is a deterministic tool signal, not a style preference —
per `ai/go-conventions.md`'s own "deterministic signals over human
round-trips," it overrides the initial call rather than being argued around.
It also matches what the roadmap itself predicted: *"tier 4's OpenSpec spec is
thin by nature"* — the roadmap author expected a spec to exist, just a small
one, and this is that spec.

**Why `platform`, not a new capability:** `openspec/specs/platform/spec.md`
already exists and already holds this project's other cross-cutting,
non-`internal/`-module requirements (the deploy stack, the tz guard, test
database isolation). A diagram's discoverability from its guide is the same
kind of fact — true of the whole platform, owned by no domain module. Inventing
a new capability folder for one requirement would split one project's
cross-cutting specs across two files for no reason.

**Why these four scenarios and no others:** each names an outcome a reader can
actually check (open the guide, find the link, read the stated precedence) —
not an implementation detail like a file's exact byte size or archify's
internal validation receipt, which belong in tasks.md, not a capability spec.

## D2 — RD9 lands as a third diagram, not a section of the workflow one

**Decision:** a third archify diagram, `nightly-cycle-derivation`, type
`dataflow`.

**Why not a section inside the workflow diagram:** the workflow diagram's
mental model is process order — steps, gates, loops. `Recalculate`'s
derivation is a different mental model — data in, a formula, data out. Archify
itself keeps these apart: `workflow` is for "processes, approval gates, tool
calls, runbooks"; `dataflow` is for "pipelines, ETL/ELT, lineage." Mixing them
in one diagram means either the process steps lose their clarity or the
derivation loses its own — archify's authoring rule caps a diagram at "one
obvious main path, at most 12 primary nodes." Step 3 already has two named
halves (metrics reconciliation, gap reconciliation) inside a per-vehicle loop.
Adding the derivation's own inputs (two snapshots, two charge sources), one
formula box, and six output figures would roughly double step 3's node count
and blur two different questions ("what order do things happen" vs. "what
does this number come from") into one picture.

**Why `dataflow` and not `workflow` for this third diagram:** the derivation
has no gate, no branch, no per-vehicle loop of its own — it is one snapshot
pair plus one charge total flowing through one pure function into one output
row. That is a pipeline, not a process. `dataflow`'s own type description
("pipelines, ETL/ELT, lineage, governance, consumers") is the closest fit.

**What this costs, stated plainly:**

- A third diagram to author, validate, and deliver at `--quality showcase` —
  roughly the same authoring cost as the sequence diagram, since `dataflow`
  is a distinct schema archify's fast-authoring path treats like any other
  type (read its schema + one example, author fresh JSON).
- A third diagram to keep current forever. `kkpa/context/architecture/
nightly-cycle.md`'s own "Rendered view" section already states "it does not
  self-update" for the one published Artifact; the same now applies three
  times over instead of twice. Any future change to `consumption.go` or
  `consumed.go`'s derivation must remember to refresh this diagram too, or it
  goes stale exactly like the published Artifact already has (see D5).
- Two diagrams were roadmap Decision 8's own call, made before RD9 existed.
  This design does not reopen that call — it adds one diagram beside it,
  for a requirement the roadmap did not anticipate.

**Rejected alternative:** cram a fourth "step 3 detail" panel into the
workflow diagram's own viewport using `meta.views` (archify's curated-chapter
feature). Rejected because a view is a camera angle on the SAME authored
nodes, not a second diagram type — it cannot switch from process grammar to
data-lineage grammar. It would not solve the mental-model mismatch above.

## D3 — File naming

**Decision:** three diagram slugs, each mirroring the `runtime-architecture`
file set (`<slug>.json`, `<slug>.html`, `<slug>.visual-check.{1440x900,
2048x1320}.{dark,light}.png`, `<slug>.visual-check.html`,
`<slug>.visual-check.json`):

- `nightly-cycle-workflow`
- `nightly-cycle-sequence`
- `nightly-cycle-derivation`

**Why `nightly-cycle-*`, not `nightly-job-*`:** the *folder* is
`kkpa/docs/diagrams/nightly-job/` (roadmap Decision 7, fixed and out of scope
for this change to revisit). The *files inside it* mirror the KB's own term
for the same subject — `kkpa/context/architecture/nightly-cycle.md` and its
glossary (`nightly cycle`, `nightly collection`, `nightly poll`, `nightly
batch`) all say "nightly cycle," never "nightly job." A reader who finds the
diagram from the guide's own links should see a name that matches what the
guide called it.

## D4 — Where every fact for the diagrams comes from

Read directly, not taken from the KB guide alone, per the dispatch
instruction. The KB guide (`kkpa/context/architecture/nightly-cycle.md`) was
used only as a cross-check against these same files, never as a source on its
own.

| Fact | Source |
|---|---|
| Four steps, in order, called from one `if err == nil` block | `internal/app/processor.go` `ProcessVehicleData`, lines 76-92 |
| Step 1 failure short-circuits steps 2-4; every exit path still calls `recordRun` | `internal/app/processor.go` `ProcessVehicleData` — the `if err == nil { ... }` guard, then the unconditional `p.recordRun(...)` below it |
| Step 2 loops per distinct `tesla_id`, bounded by a per-vehicle watermark | `internal/app/processor.go` `processChargingData`, the `teslaIDs` dedup loop and `p.mirrorWatermarks.MirrorWatermark` call |
| Step 3 loops per distinct vehicle, two halves (`Reconcile` then gap work), a failed `Reconcile` skips that vehicle's gap half | `internal/app/processor.go` `recalculateAnalytics`, the `for _, v := range distinct` loop and its `continue` on `Reconcile` error |
| Step 4 gates on "today is the 1st of the month" and measures the previous month, every vehicle in one call (`teslaID = nil`) | `internal/app/processor.go` `monthlyCapacityPeriod` and `callMonthlyCapacityCalculator` |
| One `poll_runs` row recorded on every exit path via `buildPollRun`/`recordRun` | `internal/app/processor.go` lines 87-91, 108-142 |
| `app` calls `telemetry.Collector.CollectAll` for step 1 | `internal/app/processor.go` line 80 |
| Inside `CollectAll`, the paid Fleet API calls are `ListVehicles`, `WakeUp` (via `waitUntilOnline`), `VehicleData`, `ChargingHistory` | `internal/telemetry/service.go` `listStates`, `attemptVehicle`, `collectChargingHistory` |
| `CollectAll` writes `vehicle_snapshots` (`insertSnapshot`), `poll_attempts` (`insertPollAttempt`), `supercharger_history` (`upsertSuperchargerHistory`) — all DB writes, not Fleet API calls | `internal/telemetry/service.go` `attemptVehicle`, `record`, `collectChargingHistory` |
| `app` calls `charging.SessionWriter.MirrorSessions` (DB write) and `telemetry.SuperchargerHistoryReader` (DB read) for step 2 | `internal/app/processor.go` `processChargingData` |
| `app` calls `analytics.Recalculator.Reconcile`, `analytics.Reader.ConsumedByDay`, `analytics.GapWriter.ReconcileWindow` for step 3 — all DB reads/writes, no Fleet API call anywhere in `internal/analytics` | `internal/app/processor.go` `recalculateAnalytics`; confirmed by reading `internal/analytics/recalculate.go` end to end — it imports `telemetry`/`charging`/`clock`/`logging`, never `tesla` |
| `app` calls `charging.MonthlyCapacityCalculator.Calculate` for step 4 (DB read/write) | `internal/app/processor.go` `callMonthlyCapacityCalculator` |

## D5 — The `Recalculate` derivation, as it is now (MAG-81, `48f7944`)

Both fabricated-if-from-memory facts, verified in the code:

**`deriveConsumption` takes a `chargePct` argument.**

```go
func deriveConsumption(prev *telemetry.Snapshot, cur telemetry.Snapshot, chargePct float64) consumptionCalc {
```

— `internal/analytics/consumption.go` line 128, function `deriveConsumption`.

**`KmPerPctCalc` divides by `consumed` (`consumed_pct`), not by the raw
battery drop.**

```go
consumed := float64(batteryUsed) + chargePct
calc.ConsumedPct = &consumed

if consumed > 0 { // only a positive divisor yields a truthful ratio
    kmPerPct := distance / consumed
    calc.KmPerPctCalc = &kmPerPct
```

— `internal/analytics/consumption.go` lines 147-152, same function. `batteryUsed`
(line 138: `prev.BatteryLevelPct - cur.BatteryLevelPct`) is the raw drop and
is still reported as `BatteryUsedPctCalc`, but it is no longer the divisor —
`consumed` is. The function's own comment states the before/after directly:
*"the divisor used to be the RAW BatteryUsedPctCalc... On a day the vehicle
both drove and charged, the battery ends HIGHER than it started, so the raw
figure is negative or zero, the guard failed, and the day's efficiency was
silently NULL — 12 of 124 stored days on the owner's own history."*

`chargePct` itself is summed in `internal/analytics/consumed.go`'s
`deriveVehicleMetrics`, line ~315: `sumSuperchargerPctBetween(...) +
sumManualPctBetween(...)`, then passed into `deriveConsumption` at line 318.

**Both facts also appear in `internal/analytics/analytics.go`'s doc comments**
(`VehicleStatus.ConsumedPct`, `VehicleStatus.KmPerPctCalc`), read as a
cross-check — they describe the same post-MAG-81 shape, not a source
independent of the two files above.

## D6 — What the derivation diagram shows

Inputs (left column): the snapshot immediately before the day
(`prev`), the day's own snapshot (`cur`), the Supercharger sessions and
manual charge entries whose stop time/date falls in the matching span.

Transform 1 — charge total: `chargePct` = sum of every matching session's and
entry's battery-percentage gain (`consumed.go`'s `sumSuperchargerPctBetween` +
`sumManualPctBetween`). Labelled plainly: "how much charging happened this
day."

Transform 2 — `deriveConsumption`: produces `distance_traveled_km_calc`
(odometer difference), `battery_used_pct_calc` (raw battery drop, `prev` minus
`cur`), `consumed_pct` (raw drop plus the charge total — "what the battery
really spent"), and, only when `consumed_pct` is positive,
`km_per_pct_calc` and `estimated_range_km_calc` (both divided by
`consumed_pct`).

One annotation carries the MAG-81 fact in B2 words: "A day that both drove and
charged used to show no efficiency number. Now it does — the calculation
uses what the battery really spent, not just the raw drop."

Output (right column): one `vehicle_metrics` row, written by `Recalculate`'s
UPSERT.

Left out on purpose: the four TPMS pressure deltas (`consumption.go`'s
`tpmsDeltaPSI`) and the flag/gap detection in `consumed.go`
(`inferMissingChargingType`). Both are real outputs of the same function, but
neither is what RD9 asked for — "consumed and derived consumption" — and
including them would double the diagram's node count for a fact nobody asked
to see explained. `entities/vehicle-metrics/guide.md` already documents them.

## D7 — Knowledge-base reference

`kkpa/context/architecture/nightly-cycle.md`'s existing "## Rendered view
(visual map)" section names one published Artifact and states: *"Precedence,
when they disagree: the code is the source of truth, then this guide, then
the artifact... It does not self-update."*

This change adds the three local files under that same section, with the
identical precedence sentence extended to cover them — code first, then the
guide, then any rendering, local file or Artifact alike. Tasks.md's doc task
carries the exact wording.

**The linked Artifact is already stale** — it still shows `charge_sessions` /
`MirrorChargeSession` and an `accountID` parameter RM57 removed. Not this
tier's scope to fix (the dispatch says so explicitly), but the new local
files must not copy any fact from it. D4/D5's source table above is the only
input the diagrams may draw from.

## D8 — `kkpa/context/INDEX.md`: no new row

Checked. `INDEX.md` already routes every alias of "the nightly cycle"
(`nightly cycle`, `nightly collection`, `nightly poll`, `nightly batch`, `the
poller run`, `ProcessVehicleData`) to `architecture/nightly-cycle.md`. The
three new diagrams are renderings of facts that guide already claims — they
are not a new concept, page, or use case that needs its own glossary row. The
derivation diagram's subject (`consumed_pct`, `km_per_pct_calc`) is likewise
already routed: `INDEX.md`'s `battery drain` / `calc fields` rows point to
`entities/vehicle-metrics/guide.md`, which is where the derivation's meaning
is documented in prose; this diagram is a picture of that same material, filed
under the cycle's own guide because it depicts *when* the derivation runs, not
a new fact about *what* the columns mean. No row added.

## D9 — Archify authoring approach (for whoever implements this)

Follow archify's fast-authoring path (`~/.claude/skills/archify/SKILL.md`)
per diagram: read the one matching schema (`workflow.schema.json`,
`sequence.schema.json`, `dataflow.schema.json`) plus `common.schema.json` and
one matching example, author fresh JSON (new IDs, this project's own wording
— never copy the example's facts), `meta.quality_profile: "showcase"`,
validate after every edit, `deliver` once at the end, then `visual-check`.
Reuse the project's own log-line topics as step/message labels where they fit
(`metrics reconciliation:`, `gap reconciliation:`, `session mirror`,
`monthly capacity:`) — they are already the vocabulary this codebase and its
KB use for these exact steps, and roadmap Decision 9 notes they are "useful
labels for the steps if you want them." B2 rule binds every label: no term
that needs a glossary to read.

## D10 — grill-me

`openspec/config.yaml`'s proposal rule calls for the `grill-me` skill on every
proposal. Not run here: every open question this proposal answers was already
settled before this tier was dispatched — roadmap Decisions 1-9 (written and
confirmed with the user before any tier existed) plus RD9 (given verbatim in
the dispatch, itself already "the user's own requirement, binding"). The only
genuine design choice left open by the dispatch — D1 (spec or no spec) and D2
(one diagram or three for RD9) — are decided above with their reasoning
shown, exactly what an interactive grill would have produced, but the person
who would be grilled is not present in this dispatch to answer follow-up
questions. If either call is wrong, it is cheap to reverse (D1's cost note
above; D2 is additive, not a rewrite of the other two diagrams).

## Database

**None.** No table, column, index, constraint, view, or migration.

## Testing

No unit tests (roadmap Decision 1) — this change produces no Go code.
`make archive-guard` is the only Test-Execution-Policy signal that applies;
see proposal.md "Testing".
