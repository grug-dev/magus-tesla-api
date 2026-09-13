# Sync proposal — telemetry

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `architecture/telemetry-ingest-only.md`
Source spec:  `openspec/specs/telemetry/spec.md`
Generated:    `2026-09-13`
Status: PENDING REVIEW

Origin: RM57 tier 1 (`RM57-telemetry-rekey-supercharger-history-on-tesla-id`, ticket MAG-67),
archived 2026-09-13. That change synced 4 MODIFIED and 1 REMOVED requirement into the main
telemetry spec.

---

## Why this proposal is small

The change's own docs task and the review round already corrected most of this guide — the
`tesla_id NOT NULL` rule, the unregistered-VIN skip, the removal of the account-wide port, and
the `internal/app` consumer row are all present and correct. Verified before drafting, so these
blocks add only what the spec says and the guide still does not.

Four requirements' rules are already covered and are **deliberately not repeated here**:
"there is ONE updated-since read port and it is per vehicle", "a session for an unregistered VIN
is never stored", the change-detecting upsert column lists, and the consumer map.

---

## [guide] ## Conventions & gotchas — APPEND

- **The charging counters are three, and they are independent.** A cycle counts sessions
  upserted, accounts whose charging-history fetch failed, and sessions skipped because the VIN
  was not a registered vehicle. A skip is never also an upsert or a fetch failure, and a fetch
  failure is never a skip. All three appear on the per-cycle log line, so a run that stored
  nothing still says whether it skipped or failed.
  _Source: spec telemetry — Requirement: Cycle Report Charging Counters._

- **The Supercharger read port has exactly two methods, and both take a Tesla id alone.** One
  returns a vehicle's sessions newest-first with a limit; one returns the sessions whose charge
  stop time falls in a caller-supplied window, oldest-first with no limit, because the window
  itself bounds the result. The old account-scoped "all of an account's sessions" method is gone
  — the ledger stores no account to filter on, and nothing called it.
  _Source: spec telemetry — Requirement: Supercharger Session Read Port._

- **The date-windowed read judges a session by its stop time, not its start time.** A session
  that starts before the window and stops inside it belongs to the window. Filtering on start
  time instead silently drops every session that crosses a window edge.
  _Source: spec telemetry — Requirement: Supercharger Session Read Port._

- **The updated-since read must stay a single index scan with no sort step.** The spec puts this
  obligation on the persistence layer, not just on the query: the index has to satisfy the
  vehicle filter, the instant predicate and the ordering together. Adding a column to the
  `ORDER BY`, or reordering the index, reintroduces a sort that the mirror pays on every
  nightly run.
  _Source: spec telemetry — Requirement: Supercharger Session Updated-Since Read Port._

- **Every stored session carries a vehicle identifier — the ledger refuses to store one without.**
  This is the ledger's own rule, not a side effect of a read port. It is why an unregistered VIN
  is skipped at write time rather than stored and filtered later.
  _Source: spec telemetry — Requirement: Supercharger Session Ledger._

## [index] ## Architecture topics — ADD ROWS

| `charging_skipped_unregistered` | the cycle-log label for a session skipped because its VIN is not a registered vehicle → `architecture/telemetry-ingest-only.md` |
| `cycle report charging counters` | the three independent charging counters (upserted · fetch failures · skipped unregistered) → `architecture/telemetry-ingest-only.md` |

---

## Reviewer notes — two things `apply-sync` will NOT fix

`from-spec` may only write `## Glossary`, `## How maintenance works` and
`## Conventions & gotchas`. Both items below sit outside those sections, so they need a hand
edit. Neither is urgent.

1. **`architecture/telemetry-ingest-only.md`, the RM39 banner (around line 17)** still reads
   "RM39 tier 5 renamed it to `telemetry.SuperchargerHistoryReader`, **its four methods** to
   `SuperchargerHistoryBy*`". That was true at RM39. The port has **three** methods now. The
   sentence is accurate as history but reads as a current count, and `AGENTS.md` had the same
   "four reads" error corrected during RM57. Suggest: "its methods" — the number adds nothing
   and is what rots.

2. **The `_Source:` on the existing "a session for an unregistered VIN is never stored" bullet**
   (around line 212) cites `Requirement: Supercharger Session Updated-Since Read Port`. The rule
   is really the Ledger's. The RM57 reviewer saw this, called the attribution "slightly loose"
   rather than broken, and did not reopen it — the pointer does resolve. Fix it opportunistically
   if you touch that bullet.
