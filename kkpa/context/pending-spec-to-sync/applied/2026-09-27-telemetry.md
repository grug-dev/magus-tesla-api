# Sync proposal — telemetry

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `architecture/telemetry-ingest-only.md`
Source spec:  `openspec/specs/telemetry/spec.md`
Generated:    `2026-09-27`
Status: APPLIED 2026-09-27

Delta scope: the two requirements added by `RM69-telemetry-add-collect-for-vehicles` —
"Retry Collection For A Specified Vehicle Subset" and "Retry Trigger Attribution". Every other
requirement in the spec was already reflected in the guide.

---

## [guide] ## How maintenance works — APPEND

- **Collect only some cars (the retry path):** `telemetry.Collector.CollectVehicles(ctx, run, teslaIDs)` runs the same step 1 as `CollectAll` — same poll election, same per-account grouping, same wake-and-fetch, same `poll_attempts` rows — but only for the given `tesla_id`s. Both methods share one enumeration step, so election and grouping cannot drift. Retry runs are stamped `telemetry.TriggeredByRetry` (`"retry"`).

## [guide] ## Conventions & gotchas — APPEND

- **An unregistered `tesla_id` passed to `CollectVehicles` leaves no trace.** No Fleet API call, no `poll_attempts` row, no error. It is filtered out before any account is touched.
  _Source: spec telemetry — Requirement: Retry Collection For A Specified Vehicle Subset._
- **An empty car list makes zero Fleet API calls** and returns an all-zero `CycleReport`. The caller does not need to guard it.
  _Source: spec telemetry — Requirement: Retry Collection For A Specified Vehicle Subset._
- **The Supercharger fetch in `CollectVehicles` still sees the account's FULL car list**, not only the retried cars. If it saw only the retried cars, a session of the account's other car would be dropped and counted as "unregistered". That is why `collectAccount` takes two vehicle lists — never merge them into one.
  _Source: spec telemetry — Requirement: Retry Collection For A Specified Vehicle Subset._
- **A car registered to several accounts is retried through the same elected account as the nightly run** — never through another account.
  _Source: spec telemetry — Requirement: Retry Collection For A Specified Vehicle Subset._
- **`triggered_by` is plain `text` with no `CHECK`.** A new trigger value (`retry`) needs only a Go constant on `telemetry.TriggeredBy`, no migration.
  _Source: spec telemetry — Requirement: Retry Trigger Attribution._

## [index] ## Architecture topics — ADD ROWS

| `collect vehicles` (retry a car subset: `telemetry.Collector.CollectVehicles`) | `architecture/telemetry-ingest-only.md` |
| `TriggeredByRetry` | synonym of `collect vehicles` — the `retry` trigger value → `architecture/telemetry-ingest-only.md` |
