# Sync proposal — process-vehicle-data

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `architecture/nightly-cycle.md`
Source spec:  `openspec/specs/process-vehicle-data/spec.md`
Generated:    `2026-09-10`
Status: APPLIED 2026-09-11

Derived from change `RM52-app-add-monthly-capacity-step` (ticket MAG-32, roadmap RM52 tier 2,
archived `openspec/changes/archive/app/2026-09-10-RM52-app-add-monthly-capacity-step`). That
change touched three requirements: it **renamed + modified** "Processing A Vehicle-Data Cycle
Runs Three Steps In Order" to "…Four Steps In Order", **modified** "A Whole-Cycle Synchronization
Failure Skips The Remaining Steps", and **added** "Monthly Vehicle Capacity Is Measured Only On
The First Day Of The Month, For The Previous Month".

**Read this before applying.** Most of this capability's behaviour is **already in the guide**.
Task 4.2 of that same change rewrote `architecture/nightly-cycle.md` in-change — the four-step
glossary, a new §Step 4 section, the port-map row, the table effect, and the `clock.Zone()` vs
`POLLER_TIMEZONE` gotcha are all live already. So this proposal is deliberately **small**: it
carries only the two spec rules that the in-change doc pass did NOT state, each with its spec
citation. Applying it is close to a no-op by design, and that is the healthy outcome — it means
the docs rule worked.

**One thing this proposal CANNOT fix, and the leader fixed by hand instead:** `INDEX.md`'s
`nightly cycle` row described the concept as "the 3-step `ProcessVehicleData` orchestration".
That text is the row's own first cell, and the `[index] ADD ROWS` grammar skips a row whose first
cell already exists — so a proposal can never correct it. The leader edited that row directly in
the archive commit's follow-up, because the staleness was caused by this change and
`CLAUDE.md` §"docs track structural change" requires it fixed in the same change.

---

## [guide] ## Glossary — REPLACE

- **Known as:** `nightly cycle`, `nightly collection`, `nightly poll`, `nightly batch`, `the poller run`, `poll run summary`
- **Internal name:** `app.Processor.ProcessVehicleData` — the 4-step orchestration; since RM36 it also measures its own span and records one `telemetry.PollRun` per invocation through the `telemetry.RunWriter` port

## [guide] ## Conventions & gotchas — APPEND

- **A failed monthly-capacity measurement never changes the cycle's reported outcome.** Step 4's error is logged and swallowed, exactly like step 2's and step 3's. The cycle still reports what steps 1–3 produced. A missed month is not lost work: the next run of `cmd/monthly-capacity` recomputes that period. _Source: spec process-vehicle-data — Requirement: Monthly Vehicle Capacity Is Measured Only On The First Day Of The Month, For The Previous Month._
- **Step 4 always measures EVERY vehicle in one call, never one vehicle picked by the cycle.** The nightly caller always passes `teslaID = nil`. Scoping to a single vehicle is the manual tool's job (`cmd/monthly-capacity -tesla-id`, roadmap RD8), never the nightly cycle's. Adding a per-vehicle loop here would re-pool the same rows once per vehicle. _Source: spec process-vehicle-data — Requirement: Monthly Vehicle Capacity Is Measured Only On The First Day Of The Month, For The Previous Month._

## [index] ## Architecture — ADD ROWS

| `monthly capacity step` | synonym of the nightly cycle's step 4 → `architecture/nightly-cycle.md` |
| `step 4` | the nightly cycle's monthly-capacity step → `architecture/nightly-cycle.md` |
