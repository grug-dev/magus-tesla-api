# Sync proposal — charge-session-log

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `entities/charge-session/guide.md`
Source spec:  `openspec/specs/charge-session-log/spec.md`
Generated:    2026-08-28 (refreshed after the RM31 tier-2 archive)
Status: PENDING REVIEW

---

<!--
Derived from the spec ONLY (no codebase exploration, no `--with-filemap`), so no
`## Component map` block is proposed and none is touched at apply time. The target guide does
not exist yet — apply-sync creates it from the guide template, then applies these blocks.

Curator's note for the reviewer: this proposal has been REFRESHED, not replaced. It still
carries the RM29 tier-6 retention requirements and the RM30 tier-1 retrieval requirement; the
RM31 tier-1 archive added the correction requirement ("A Charge Session's Battery Percentages
Are Correctable By A Human"), marked (RM31); and the RM31 **tier-2** archive now adds the two
further retrieval requirements ("...By Recency Of Update" and "...By Recency Of Occurrence,
Bounded By Count"), whose blocks are appended below and marked (RM31 t2).

Two things deliberately left alone:
- `workflows/supercharger-stats-read.md` — its Conventions still say "Read-only page — no edit
  API" and its INDEX row still reads "no user write path". That becomes false only when RM31
  **tier 4** (`RM31-gateway-add-session-battery-edit`) ships the actual UI write path, and that
  tier's roadmap prompt explicitly owns the edit. Do NOT correct it from this proposal — tier 1
  added a domain port with no caller, so the page really is still read-only today.
- `## Component map` — spec.md carries no file paths and `--with-filemap` was not passed.
-->

## [guide] ## Glossary — REPLACE

- **Known as:** `charge session`, `charge sessions`, `charge session log`, `Supercharger charge session`, `mirrored Supercharger session`, `battery percentage verification`, `verified battery percentage`
- **Internal name:** `charge-session-log` capability — the `charge_sessions` record owned by the `charging` module

## [guide] ## How maintenance works — APPEND

The capability has two halves — a synchronization that maintains the records, and a retrieval
that serves them.

**Maintaining the records (synchronization).** The platform collects Supercharger sessions
elsewhere; this capability keeps one durable record per account and session identifier, holding
one record for EVERY collected session whether or not anyone has verified its battery
percentages. Each record identifies the owning account, the vehicle by its durable VIN, the
vehicle by its currently registered identifier (which may be absent), the session's own
identifier, and the instants the session started and stopped. A synchronization is
all-or-nothing per account: one session belonging to a different account rejects the whole set.
An empty synchronization succeeds and changes nothing. Re-synchronizing an unchanged session
never duplicates it, and a session that disappears from the collected history is retained
unchanged.

**What is frozen vs what tracks the source.** The charging site name and the VIN are written
once and never change on later synchronizations. The energy delivered, total cost, currency and
payment-settled status are the opposite — every synchronization replaces each with the
collecting capability's current value, which is why the synchronization must run often enough
that settling fees stay current within one cycle. The currently registered vehicle identifier is
refreshed on every synchronization, and is absent when the VIN belongs to no vehicle currently
registered to the account.

**Retrieving the records.** A caller asks for one vehicle's sessions within an account over a
window of **whole calendar days**, and the window is inclusive of every instant of both its
first and its last day. Records are matched on their **stop** instant, ordered by that stop
instant earliest-first, and a retrieval that matches nothing returns an empty result rather than
an absence or an error. Each retrieved record carries every fact the capability holds — charging
site, energy, cost, currency, payment status, and the optional verified battery percentages with
their provenance and frozen estimated pair — so a caller never has to consult the collecting
capability to describe a session completely.

**Correcting the verified percentages (RM31).** Beyond the synchronization and the retrieval,
the capability offers a third, human-driven half: a correction that records or fixes one
record's verified start and end battery percentages. It is scoped to an account — it may only
reach a record belonging to that same account, whatever record identifier is supplied — and it
is a **full-value** operation, not a delta: every correction states both percentages' final
values. Either percentage may be present independently of the other; a correction supplying
neither clears both percentages AND their provenance as one outcome. Provenance is never an
input: it is derived, set to human-verified whenever at least one percentage ends up present.
Each supplied percentage must fall within zero to one hundred inclusive, and a rejected
correction leaves the record exactly as it was. A correction touches nothing else the record
carries — identity, time window, site, energy, cost, currency, payment facts, and the frozen
estimated percentages all survive any number of corrections — and a successful one hands back
the record's full current detail, so no separate retrieval is needed.

**Three retrieval shapes, and they do NOT share a sort order (RM31 t2).** Alongside the
calendar-day window read, the capability now offers two more retrievals, both account- and
vehicle-scoped like the first. **By recency of update** returns every session created or
modified at or after a caller-supplied instant, ordered by stop instant **earliest-first**; a
correction to a record's verified percentages counts as a modification for this purpose even
when the session's own time window never moved, which is precisely the mechanism by which a
human edit becomes visible to a downstream consumer that tracks a cursor. **By recency of
occurrence, bounded by count** returns the most recent sessions up to a caller-supplied count,
ordered by stop instant **most-recent-first**, and applies the capability's own default count
when the caller specifies no positive one. All three return an empty result rather than an
absence or an error, and none of the three ever returns a record whose vehicle is not currently
registered.

**What the record deliberately does NOT carry:** the vendor's raw payload, the account's country,
the vehicle's model designation, the session's billing-type classification, or the instant the
vehicle unlatched from the charger. Those stay with the capability that collects them.

## [guide] ## Conventions & gotchas — APPEND

- **The retrieval window is whole calendar days, both ends inclusive** — a session stopping at
  the very last instant of the window's last day is included; one stopping at the first instant
  of the following day is excluded. Callers pass days, not instants.
  _Source: spec charge-session-log — Requirement: Charge Sessions Are Retrievable For A Vehicle Within A Time Window._
- **Matching is on the STOP instant, never the start** — a session that starts before the window
  but stops inside it IS returned (with its start instant reported unchanged, still outside the
  window); a session that starts inside but stops after it is NOT. This shifts window-straddling
  sessions relative to any start-time-based view.
  _Source: spec charge-session-log — Requirement: Charge Sessions Are Retrievable For A Vehicle Within A Time Window._
- **Results are ordered earliest-stop-first (ascending)** — a caller that wants newest-first must
  reverse for display. Do not "fix" the retrieval to descending.
  _Source: spec charge-session-log — Requirement: Charge Sessions Are Retrievable For A Vehicle Within A Time Window._
- **No match returns an empty result, never an absence or an error** — callers must not treat
  "nothing found" as a failure case.
  _Source: spec charge-session-log — Requirement: Charge Sessions Are Retrievable For A Vehicle Within A Time Window._
- **A session whose vehicle is no longer currently registered is never retrieved by vehicle** —
  for ANY vehicle requested — even though the record continues to exist and to be retained. This
  is intended behavior, not a bug to special-case.
  _Source: spec charge-session-log — Requirement: Charge Sessions Are Retrievable For A Vehicle Within A Time Window._
- **A retrieved record is self-sufficient** — it carries the site, fee facts, and the verified
  battery percentages with provenance and frozen estimates, so no follow-up request to the
  collecting capability is needed to describe a session.
  _Source: spec charge-session-log — Requirement: Charge Sessions Are Retrievable For A Vehicle Within A Time Window._
- **The charging site name is frozen at first recording** — later synchronizations never change
  it, even when the source now reports a different name.
  _Source: spec charge-session-log — Requirement: The Charging Site Is Fixed On Record; Energy, Cost, Currency And Payment Status Track The Source._
- **Energy, cost, currency and payment status are NOT frozen** — every synchronization overwrites
  them with the source's current values, so the sync must run often enough that settling fees
  stay reflected within one cycle.
  _Source: spec charge-session-log — Requirement: The Charging Site Is Fixed On Record; Energy, Cost, Currency And Payment Status Track The Source._
- **VIN is write-once; the registered vehicle identifier is refreshed every synchronization** and
  is absent when the VIN belongs to no currently registered vehicle.
  _Source: spec charge-session-log — Requirement: Registered Vehicle Identifier Is Refreshed, VIN Is Not._
- **A synchronization naming another account's session is rejected in full** — no part of the
  set is recorded, including the correctly scoped sessions.
  _Source: spec charge-session-log — Requirement: Supercharger Charge Session Record._
- **The same session identifier under two accounts is two independent records** — neither
  account's record is affected by the other's synchronization.
  _Source: spec charge-session-log — Requirement: Supercharger Charge Session Record._
- **The record deliberately omits the raw vendor payload, country, vehicle model, billing type,
  and the unlatch instant** — do not add them here; they belong to the collecting capability.
  _Source: spec charge-session-log — Requirement: Supercharger Charge Session Record._

- **(RM31) A correction is FULL-VALUE, never a delta** — every correction states both
  percentages' final values, so a caller changing only one must re-supply the other's current
  value or it is cleared to absent. Read the record first when editing one field.
  _Source: spec charge-session-log — Requirement: A Charge Session's Battery Percentages Are Correctable By A Human._
- **(RM31) Provenance is derived, never supplied** — the capability accepts no provenance input;
  it is set to human-verified whenever at least one percentage ends up present, and cleared when
  neither does. There is no way for a correction to write the synchronized provenance value.
  _Source: spec charge-session-log — Requirement: A Charge Session's Battery Percentages Are Correctable By A Human._
- **(RM31) Supplying neither percentage clears BOTH percentages and the provenance, as one
  outcome** — not a no-op, and not a partial clear that leaves stale provenance behind.
  _Source: spec charge-session-log — Requirement: A Charge Session's Battery Percentages Are Correctable By A Human._
- **(RM31) Out-of-range is rejected with the record left exactly as it was** — the valid range is
  zero to one hundred inclusive, and rejection must leave no partial write behind.
  _Source: spec charge-session-log — Requirement: A Charge Session's Battery Percentages Are Correctable By A Human._
- **(RM31) A correction changes ONLY the two percentages and their provenance** — the frozen
  estimated percentages in particular are unreachable by any correction, no matter how many times
  one is performed. This is the capability's central structural guarantee.
  _Source: spec charge-session-log — Requirement: A Charge Session's Battery Percentages Are Correctable By A Human._
- **(RM31) An unknown record and another account's record are rejected IDENTICALLY** — the two
  cases are deliberately indistinguishable to the caller; do not add a "not found" distinction.
  _Source: spec charge-session-log — Requirement: A Charge Session's Battery Percentages Are Correctable By A Human._
- **(RM31) A successful correction returns the record's full current detail** — callers re-render
  from the returned record rather than issuing a follow-up retrieval.
  _Source: spec charge-session-log — Requirement: A Charge Session's Battery Percentages Are Correctable By A Human._


- **(RM31 t2) The three retrievals deliberately DISAGREE on sort direction** — the calendar-day
  window read and the by-recency-of-update read are ordered earliest-stop-first (ascending); the
  count-bounded read is ordered most-recent-stop-first (descending). This is intended, not an
  inconsistency to harmonise: a "most recent N" read is meaningless ascending. Check the
  direction per retrieval; never assume one from a sibling.
  _Source: spec charge-session-log — Requirements: ...Within A Time Window / ...By Recency Of Update / ...By Recency Of Occurrence, Bounded By Count._
- **(RM31 t2) A correction counts as a modification for the by-recency-of-update read** — even
  though it moves nothing about the session's own charging window. This is the entire mechanism
  that carries a human's verified percentages into a downstream cursor-driven consumer; a
  retrieval that filtered on the session's time instead of its modification instant would silently
  never see edits to old sessions.
  _Source: spec charge-session-log — Requirement: Charge Sessions Are Retrievable For A Vehicle By Recency Of Update._
- **(RM31 t2) The by-recency-of-update boundary is INCLUSIVE at the requested instant** — a
  record modified at exactly that instant is returned; one modified strictly before it is not.
  A consumer storing the instant of its last pass will therefore re-read the boundary record
  rather than skip it, which is the safe direction.
  _Source: spec charge-session-log — Requirement: Charge Sessions Are Retrievable For A Vehicle By Recency Of Update._
- **(RM31 t2) An unspecified or non-positive count falls back to the capability's OWN default** —
  the caller does not get "everything" and does not get an error. Do not read a missing count as
  unbounded.
  _Source: spec charge-session-log — Requirement: Charge Sessions Are Retrievable For A Vehicle By Recency Of Occurrence, Bounded By Count._
- **(RM31 t2) The unregistered-vehicle exclusion holds across ALL THREE retrievals** — a record
  whose vehicle is no longer currently registered is returned by none of them, for any vehicle
  requested, while the record itself continues to exist and to be retained.
  _Source: spec charge-session-log — Requirements: ...By Recency Of Update / ...By Recency Of Occurrence, Bounded By Count._

## [index] ## Glossary & routing — entities — ADD ROWS

| `charge session` | `charge-session-log` capability / `charge_sessions` (owned by `charging`) | entity | `entities/charge-session/guide.md` |
| `charge sessions` | synonym of `charge session` | entity | `entities/charge-session/guide.md` |
| `charge session log` | synonym of `charge session` | entity | `entities/charge-session/guide.md` |
| `mirrored Supercharger session` | synonym of `charge session` | entity | `entities/charge-session/guide.md` |
