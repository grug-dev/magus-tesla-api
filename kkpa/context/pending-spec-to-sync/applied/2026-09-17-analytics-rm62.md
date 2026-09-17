# Sync proposal — analytics

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `architecture/nightly-cycle.md`
Source spec:  `openspec/specs/analytics/spec.md`
Generated:    `2026-09-17`
Status: APPLIED 2026-09-17

---

<!--
Delta scope: only the two requirements RM62 tier 2 added are new to the KB —
"Nightly-Path Query Logging" and "Dashboard-Only Reads Are Not Logged". Every other
requirement in this spec predates the change and is already covered.

Target choice: `architecture/nightly-cycle.md` is where step 3 lives, and it already carries
the tier-1 bullet about per-vehicle log attribution. The two bullets belong side by side.
The telemetry equivalent is indexed as `telemetry query logging` -> `architecture/telemetry-ingest-only.md`,
so the `[index]` block below mirrors that naming for analytics.

No `## Component map` block — a spec carries behavior, not file paths.
-->

## [guide] ## Conventions & gotchas — APPEND

- **`internal/analytics` logs its own queries, but only on the nightly path.** Three
  decorators in `internal/analytics/query_log.go` carry the `analytics query:` topic:
  `Recalculator` (both methods), `GapWriter.ReconcileWindow`, and `Reader.ConsumedByDay`
  — the one `Reader` method the poller calls. The write methods log **before** delegating,
  so the line survives a failed write; `ConsumedByDay` logs **after**, because it reports
  the row and flagged counts it got back.
  _Source: spec analytics — Requirement: Nightly-Path Query Logging._

- **The other three `Reader` methods log nothing, and that is deliberate — do not "finish"
  it.** `OdometerDeltaByDay`, `BatteryLevelByDay` and `LatestMetricsForVehicles` have no
  poller caller. Every caller is a gateway dashboard or history page on a live HTTP
  request, so logging them would add a line to nearly every page load. They are silent
  pass-throughs inside the same decorator, not missing work.
  _Source: spec analytics — Requirement: Dashboard-Only Reads Are Not Logged._

- **Each decorator implements its port explicitly, never by embedding.** Embedding would
  let a method added later to `Reader`, `Recalculator` or `GapWriter` be satisfied
  silently by promotion, and that call would never be logged. The compile-time assertion
  per decorator turns that into a build error instead. `internal/telemetry/query_log.go`
  uses the same shape for the same reason — mirror it when adding a port.
  _Source: spec analytics — Requirement: Nightly-Path Query Logging._

- **`Reconcile`'s internal call to `Recalculate` is not logged, by design.** It is a method
  call on the concrete type, so it never re-enters the decorator. The window it derived is
  still readable: the `telemetry query:` line for `SnapshotsByVehicleBetween` on the very
  next line carries the same `start`/`end`. Do not add an interface field to a type just to
  make a self-call re-enter its own wrapper.
  _Source: spec analytics — Requirement: Nightly-Path Query Logging._

## [index] ## Architecture — ADD ROWS

| `analytics query logging` (the nightly path only — `Recalculator`, `GapWriter`, and `Reader.ConsumedByDay`; dashboard reads stay silent) | `architecture/nightly-cycle.md` |
| `analytics query:` | the log topic of `analytics query logging` → `architecture/nightly-cycle.md` |
