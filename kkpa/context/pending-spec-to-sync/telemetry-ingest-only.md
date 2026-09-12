# Sync proposal — telemetry (guide: telemetry-ingest-only)

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `architecture/telemetry-ingest-only.md`
Source spec:  `openspec/specs/telemetry/spec.md`
Generated:    2026-09-12
Status: PENDING REVIEW

---

## [guide] ## Glossary — REPLACE

- **Known as:** `telemetry module`, `vehicle snapshots`, `supercharger history`, `nightly collection`, `telemetry schema`, `who reads telemetry`, `telemetry vs analytics`, `can the gateway read telemetry`, `ingest module`, `poll run`, `run summary`, `telemetry query logging`, `fleet api logging`, `poll account election`, `elected polling account`
- **Internal name:** `internal/telemetry` — ports `telemetry.Reader`, `telemetry.SuperchargerHistoryReader` (reads), `telemetry.Collector` (write), `telemetry.RunWriter` (run summary write) — tables (all in schema `telemetry`) `vehicle_snapshots`, `supercharger_history`, `poll_attempts`, `poll_runs`

## [guide] ## Conventions & gotchas — APPEND

- **One car is polled once a night, not once per account.** A car can be registered to
  several accounts. Before the per-account collection loop runs, the module elects exactly
  one account to poll each distinct vehicle. Adding a second account for a car does not add
  a second Fleet API call or a second snapshot row.
  _Source: spec telemetry — Requirement: Poll Account Election._
- **The election prefers OWNER but never requires it.** It picks an OWNER-access candidate
  when one exists, and otherwise any account that registered the car. It never skips a
  vehicle. An OWNER-only filter would silently stop polling a DRIVER-only car, and Tesla
  cannot backfill the lost days.
  _Source: spec telemetry — Requirement: Poll Account Election._
- **The election is pure and deterministic.** It uses only the vehicle and access-type data
  already returned by enumerating registered vehicles. It never checks whether an account's
  Tesla connection still works, and a tie resolves the same way every run, whatever order the
  account module returned the candidates in.
  _Source: spec telemetry — Requirement: Poll Account Election._
- **A `poll_attempts` row names the elected account, not every account.** The account on an
  attempt is the one whose token paid for that call. After the election there is exactly one
  such account per car per cycle, so the row no longer tells you who else registered the car.
  _Source: spec telemetry — Requirement: Per-Vehicle Isolation And Attempt Recording._
- **Every snapshot read port takes Tesla ids only.** The latest-snapshot, history,
  updated-since and preceding-day ports identify vehicles by their Tesla numeric id. No
  caller passes an account id to read snapshots.
  _Source: spec telemetry — Requirements: Latest Snapshot Read Port; Snapshot History Read Port; Snapshot Updated-Since Read Port; Preceding-Snapshot Read Port._

## [index] ## Architecture topics — ADD ROWS

| `poll account election` (one account elected per car per cycle — prefer OWNER, never skip a car) | `architecture/telemetry-ingest-only.md` |
| `elected polling account` | synonym of `poll account election` → `architecture/telemetry-ingest-only.md` |
| `who polls a car registered to two accounts` | synonym of `poll account election` → `architecture/telemetry-ingest-only.md` |
| `why is my car polled twice` | the problem the election fixed → `architecture/telemetry-ingest-only.md` |
