# Sync proposal — process-vehicle-data

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `architecture/nightly-cycle.md`
Source spec:  `openspec/specs/process-vehicle-data/spec.md`
Generated:    `2026-08-31`
Status: APPLIED 2026-09-01

Derived from the one requirement merged by change `RM36-app-record-poll-run` (ticket
MAG-35, archived `2026-08-31-RM36-app-record-poll-run`, the final tier of roadmap
RM36): **Every Cycle Records A Poll Run Summary**. Pairs with the sibling proposal
`telemetry.md`, which carries tier 1's storage-side requirements.

---

## [guide] ## Glossary — REPLACE

- **Known as:** `nightly cycle`, `nightly collection`, `nightly poll`, `nightly batch`, `the poller run`, `poll run summary`
- **Internal name:** `app.Processor.ProcessVehicleData` — the 3-step orchestration; since RM36 it also measures its own span and records one `telemetry.PollRun` per invocation through the `telemetry.RunWriter` port

## [guide] ## How maintenance works — APPEND

- **Add a fact to the recorded run summary:** the value must first exist on `telemetry.CycleReport` (populated inside `telemetry.CollectAll`). Then extend `telemetry.PollRun` and the `poll_runs` schema on the telemetry side, and map the new field in `internal/app`'s `buildPollRun`. `internal/app` gains no pool and no table — it only maps and calls the port.
- **Change what the cycle measures:** `start`/`finish` are read in `ProcessVehicleData` via `internal/clock`, bracketing all three steps. Anything that needs its own timing is a separate measurement, not a widening of these two.

## [guide] ## Conventions & gotchas — APPEND

- **Every cycle records exactly one summary — including a cycle that fails outright.** A whole-cycle synchronization failure short-circuits steps 2 and 3 but still records a row, with all counts zero and timings reflecting how fast the failure was. Before this, a failed cycle wrote zero `poll_attempts` rows and so left no trace of having run at all. _Source: spec process-vehicle-data — Requirement: Every Cycle Records A Poll Run Summary._
- **Recording the summary can never change the cycle's reported outcome.** A failed summary write is logged and swallowed; `ProcessVehicleData` returns exactly what its three steps produced. In `internal/app` this is enforced structurally — `recordRun` returns nothing, so the compiler prevents it, not a convention. _Source: spec process-vehicle-data — Requirement: Every Cycle Records A Poll Run Summary._
- **The record point is a fall-through, not a second call site.** Both the success path and the failure short-circuit fall through to one measurement/record tail. Adding an early `return` anywhere in `ProcessVehicleData` silently reintroduces the untraced-run bug this design exists to prevent. _Source: spec process-vehicle-data — Requirement: Every Cycle Records A Poll Run Summary._
- **Both poller entry points are covered by construction.** The nightly schedule and `cmd/poller --once` both call `ProcessVehicleData`, so neither can diverge from the other. Never record a run from `cmd/`. _Source: spec process-vehicle-data — Requirement: Every Cycle Records A Poll Run Summary._

## [index] ## Architecture topics — ADD ROWS

| `poll run summary` (one row per `ProcessVehicleData` invocation, recorded on every exit path incl. whole-cycle failure) | `architecture/nightly-cycle.md` |
| `run duration` | `ProcessVehicleData`'s clock-measured start-to-finish span → `architecture/nightly-cycle.md` |
