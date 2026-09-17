Source: MAG-57 — https://linear.app/magus-monitor/issue/MAG-57/nightly-job-tests
Roadmap: openspec/roadmaps/RM62-nightly-cycle-observability.md
Tier: 4 of 4 (`RM62-platform-add-nightly-cycle-diagram`, cross-cutting — no
`internal/` module owns it). No technical dependency on tiers 1-3 (`app`,
`analytics`, `charging`): those tiers change what the nightly cycle *logs*,
never what it *does*. This tier draws the cycle's steps and calls, which no
logging tier can invalidate (roadmap Decision 9). All three earlier tiers are
already archived.

## Why

A person cannot see the nightly cycle's shape without reading four Go files
across three modules. `kkpa/context/architecture/nightly-cycle.md` is a
maintenance map — paths and symbols, not a picture. The roadmap's own goal:
"a person understands the nightly cycle step by step without reading Go."

A second, later requirement (RD9, added by the user after this roadmap was
written) asks for the same treatment of `Recalculate` — the part of step 3
that turns a snapshot pair, plus the day's charge records, into the six
`_calc` figures stored on `vehicle_metrics`. `Recalculate` changed two commits
ago (MAG-81, `48f7944`): `deriveConsumption` gained a `chargePct` argument, and
the efficiency ratio now divides by the charge-corrected `consumed_pct`
instead of the raw battery drop. A diagram drawn from memory would get both
facts wrong.

## What Changes

- **Two archify diagrams**, per roadmap Decision 8, in
  `kkpa/docs/diagrams/nightly-job/`:
  - `nightly-cycle-workflow` (`workflow` type) — the four steps of
    `app.Processor.ProcessVehicleData` in order, the step-1 short-circuit, the
    per-vehicle loops inside steps 2 and 3, the first-of-the-month gate on
    step 4, and the one `poll_runs` summary recorded on every exit path.
  - `nightly-cycle-sequence` (`sequence` type) — the cross-module call chain
    `app → telemetry → tesla`/Fleet API, `app → charging`, `app → analytics`,
    marking which calls are paid Fleet API calls and which are database reads
    or writes.
- **A third archify diagram**, added for RD9, not part of the original
  roadmap: `nightly-cycle-derivation` (`dataflow` type) — what `Recalculate`
  derives for one day's `vehicle_metrics` row: the snapshot pair and the
  span's charge total going in, `deriveConsumption`'s charge-corrected
  `consumed_pct` and the two efficiency figures it now divides coming out. See
  design.md D2 for why this is a third diagram rather than a section of the
  workflow one, and what that costs.
- **A knowledge-base reference.** `kkpa/context/architecture/nightly-cycle.md`
  already has a `## Rendered view (visual map)` section pointing at one
  published Artifact. This change adds the three local files there and states
  the same precedence rule that section already states for the Artifact: code
  first, then the guide, then the rendering.
- **No `internal/` code changes.** This is a cross-cutting `platform` change;
  see design.md D1 for the one thin spec delta it does carry.

## Breaking

**No.** This change adds documentation files and a knowledge-base reference.
No Go file, port, database object, HTTP route, or i18n key changes.

## Modules Affected

- None inside `internal/`. This is a cross-cutting `platform` change — no
  `internal/` module owns diagram or KB assets.
- `kkpa/docs/diagrams/nightly-job/` — new folder, three diagram file sets.
- `kkpa/context/architecture/nightly-cycle.md` — one section edited (the
  existing "Rendered view" section gains the three new local links).

## Database Changes

**None.** No table, column, index, constraint, view, or migration.

## Read Paths Affected

**None.** No runtime code changes; nothing is added to, removed from, or
reordered on any read or write path.

## Capabilities

### Added Capabilities

None.

### Modified Capabilities

- **`platform`** — gains one requirement, "Nightly Cycle Reference Diagrams":
  the three diagrams must be findable from the knowledge-base guide, and the
  guide must state the code-then-guide-then-rendering precedence for them.
  See `specs/platform/spec.md` `## ADDED Requirements` and design.md D1.

### Out of scope (explicitly deferred)

Per the roadmap's own "Out of scope" list, closed as "working as designed" by
the MAG-57 analysis:

- The wake poll interval and `ListVehicles` call pattern.
- Any date filter on `ChargingHistory`.
- `analytics.GapReconciliationWindow`'s value (30 days).
- Renaming `internal/app/processor.go`.
- Any end-to-end or integration test of the whole cycle.
- Logging changes for `app`, `analytics`, or `charging` — those are tiers 1-3,
  already archived.

## Testing

Per roadmap Decision 1: **no unit tests.** This change produces no Go code —
diagrams and a doc edit have nothing to unit-test.

`make archive-guard` is the one signal from the Test-Execution-Policy that
applies here (this branch's baseline is the merge-base with `main`, which
already carries the three archived tiers as committed history — this change
must not touch anything under `openspec/changes/archive/`). Exact command for
the owner, once implementation lands: `make archive-guard`. `go build`,
`go vet`, `gofmt`, and every other guard have nothing to check — this change
adds no `.go` file.
