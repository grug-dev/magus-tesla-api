# Sync proposal — manual-rerun-api

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `use-case/app/trigger-manual-rerun.md`
Source spec:  `openspec/specs/manual-rerun-api/spec.md`
Generated:    2026-09-08
Status: PENDING REVIEW

---

## Two things to decide before you apply this

**1. The module folder.** The endpoint lives in `cmd/poller`, which is not a child of
`internal/`, so it is not a module by this project's own definition. This proposal files it
under `app`, because the flow's real work is `app.Processor.ProcessVehicleData` and the sibling
guide `architecture/nightly-cycle.md` already documents that cycle. Change the target path if
you prefer a flat `use-case/trigger-manual-rerun.md`.

**2. Three sections will stay empty.** `from-spec` reads only `spec.md`, and a spec carries
behavior, never file paths. So `## Entry point`, `## Flow` and `## Database` would be applied as
template placeholders. If you want them filled, do **not** apply this proposal — run
`/kkpa-context-curate use-case "POST /internal/rerun/<token>"` instead. That mode traces the
call path and fills all three, and it will pick up the blocks below anyway.

---

<!--
Allowed [guide] sections for a use-case target: `## Input / output`, `## Conventions & gotchas`,
`## Related use cases`. `## Entry point`, `## Flow` and `## Database` are never proposed from a
spec — same rule that protects `## Component map`.
-->

## [guide] ## Input / output — REPLACE

- **Input:** none. The whole request is the path. The last path segment is the shared secret,
  and the route exists only when that secret is configured. There is no body, no header, and no
  session.
- **Output:** `202 Accepted` with `{"status":"started"}` when a cycle starts. `409 Conflict`
  when a cycle is already running, from either trigger. `404 Not Found` when the secret in the
  path is wrong, or when no secret is configured at all.
- The response carries **no run identifier**. Find the run afterward in the `poll_runs` row whose
  trigger is `api`.

## [guide] ## Conventions & gotchas — APPEND

- **No secret configured means no endpoint at all.** There is no state where the endpoint answers
  without one. Not "open", not "default secret" — the route does not exist.
  _Source: spec manual-rerun-api — Requirement: The Endpoint Is Never Open By Accident._
- **The secret travels in the URL path, not a header.** So it reaches proxy access logs and shell
  history. This was accepted knowingly, for a single-user tool.
  _Source: spec manual-rerun-api — Requirement: The Endpoint Is Never Open By Accident._
- **Only one vehicle-data cycle ever runs at a time, across every trigger.** A manual request
  arriving while any cycle runs is rejected, and starts nothing. The lock is shared, so it covers
  the scheduler and the API together.
  _Source: spec manual-rerun-api — Requirement: A Manual Rerun Never Overlaps Any Other Cycle._
- **The scheduler can lose a night.** If a manual cycle is still running when the scheduled time
  arrives, that night's collection does not run. It is recorded as skipped, and the schedule
  continues normally the next day. This is a deliberate trade, not a bug.
  _Source: spec manual-rerun-api — Requirement: A Manual Rerun Never Overlaps Any Other Cycle._
- **The response is sent before the cycle finishes.** The cycle runs on the platform's own
  lifetime, not the request's. Closing the connection right after the response does not stop it.
  Any future change here must keep that property.
  _Source: spec manual-rerun-api — Requirement: A Manual Rerun Responds Immediately And Runs In The Background._

## [guide] ## Related use cases — APPEND

- `architecture/nightly-cycle.md` — the same three-step cycle this endpoint starts. The scheduler
  is its other trigger.

## [index] ## Use cases — ADD ROWS

| `trigger a manual rerun` | `POST /internal/rerun/<token>` | `app` | `use-case/app/trigger-manual-rerun.md` |
| `manual rerun` | `POST /internal/rerun/<token>` | `app` | `use-case/app/trigger-manual-rerun.md` |
| `rerun the nightly cycle` | `POST /internal/rerun/<token>` | `app` | `use-case/app/trigger-manual-rerun.md` |
| `force a collection cycle` | `POST /internal/rerun/<token>` | `app` | `use-case/app/trigger-manual-rerun.md` |
| `on-demand poll` | `POST /internal/rerun/<token>` | `app` | `use-case/app/trigger-manual-rerun.md` |
