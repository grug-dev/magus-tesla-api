# Sync proposal — charge-session-log

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: workflows/supercharger-stats-read.md
Source spec:  openspec/specs/charge-session-log/spec.md
Generated:    2026-08-28
Status: PENDING REVIEW

---

## [guide] ## Glossary — REPLACE

- **Known as:** `supercharger stats`, `Supercharger session`, `charge session log`, `Supercharger Stats page`, `/supercharger-stats`, `fast charging stats`
- **Internal name:** `SuperchargerStatsPage` / `SuperchargerStatsFragment` (gateway handlers) → `charging.SessionReader` / `charging.SessionWriter` (`charge_sessions` table).

## [guide] ## Conventions & gotchas — APPEND

- **Durable Supercharger record with unalterable site name** — `charge_sessions` maintains a durable record per account and session ID. The charging site name is write-once and frozen upon creation, whereas energy delivered, total cost, currency, and payment settlement status track and update from the source on every sync pass. _Source: spec charge-session-log — Requirement: Supercharger Charge Session Record & Requirement: The Charging Site Is Fixed On Record; Energy, Cost, Currency And Payment Status Track The Source._
- **VIN is write-once; vehicle_id is refreshed per sync** — VIN is durable and write-once, while `vehicle_id` refreshes to match the account's currently registered vehicle registry (set to absent if the vehicle is no longer registered). Unregistered vehicle sessions remain retained in storage but are excluded from vehicle-scoped retrievals. _Source: spec charge-session-log — Requirement: Registered Vehicle Identifier Is Refreshed, VIN Is Not._
- **Synchronization never overwrites battery percentage verification** — `charge_sessions` carries optional verified battery percentages (start/stop), provenance ("user verified" or "polled"), and frozen estimates. Sync passes never write, clear, or overwrite these 5 verification fields. Percentage values must be between 0 and 100 inclusive and require a provenance when present. _Source: spec charge-session-log — Requirement: Battery Percentage Verification On A Charge Session._
- **One-time legacy import defaults missing provenance to "user verified"** — Previously collected sessions with verified battery percentages but missing provenance are imported with provenance set to "user verified". Sessions without percentages receive no provenance. Import is repeatable and idempotent. _Source: spec charge-session-log — Requirement: One-Time Import Of Previously Collected Sessions._
- **Human battery percentage corrections derive provenance and preserve session facts** — Human corrections to start/end battery percentages set provenance automatically to "user verified" (or clear percentages and provenance if neither percentage is supplied). Corrections touch ONLY start %, end %, and provenance, preserving all other session facts (site, energy, cost, currency, payment, frozen estimates). Out-of-bounds percentages (outside 0-100) or wrong-account attempts are rejected cleanly. _Source: spec charge-session-log — Requirement: A Charge Session's Battery Percentages Are Correctable By A Human._
- **Session retrieval interfaces support window, recency-of-update, and count queries** — Sessions can be retrieved by whole-calendar-day window (inclusive bounds, ordered earliest stop first), by recency of update/creation (ordered earliest stop first, including battery % edits), or by recency of occurrence up to a bounded count (ordered newest stop first). Unregistered vehicles are omitted from vehicle-scoped retrievals, and empty matches return empty results rather than errors. _Source: spec charge-session-log — Requirement: Charge Sessions Are Retrievable For A Vehicle Within A Time Window, Requirement: Charge Sessions Are Retrievable For A Vehicle By Recency Of Update, & Requirement: Charge Sessions Are Retrievable For A Vehicle By Recency Of Occurrence, Bounded By Count._

## [index] ## Glossary & routing — entities — ADD ROWS

| `charge session log` | `charging.SessionReader` / `charging.SessionWriter` (`charge_sessions`) | entity | `workflows/supercharger-stats-read.md` |
| `charge session record` | synonym of `charge session log` | entity | `workflows/supercharger-stats-read.md` |
