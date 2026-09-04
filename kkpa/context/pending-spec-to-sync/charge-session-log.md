# Sync proposal — charge-session-log

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `workflows/supercharger-stats-read.md`
Source spec:  `openspec/specs/charge-session-log/spec.md`
Generated:    `2026-09-04`
Status: PENDING REVIEW

> **Routing note (read before applying).** This capability resolves to an existing
> `workflows/` guide, not to a `use-case/` file. Under the current use-case/workflow
> boundary a capability with one external trigger would be a use case, but
> `workflows/supercharger-stats-read.md` predates that rule and the skill's own
> instruction is to leave such files alone. This proposal therefore **extends the existing
> workflow guide** and creates no second entry. Do not move the file as part of applying this.

> **Origin — this draft REPLACES the 2026-09-03 one and now covers TWO archived tiers.**
> The earlier draft (RM41 tier 2 only) was never applied. It is superseded rather than lost:
> the capability spec has since absorbed a second archive, so this draft is regenerated from
> the spec as it stands today and carries both tiers' content.
> - `RM41-charging-drop-estimate-columns` (tier 2, MAG-36), archived 2026-09-03 — dropped
>   `start_battery_pct_est` / `end_battery_pct_est` from `charging.supercharger_sessions`.
> - `RM41-charging-add-session-status` (tier 4, MAG-45), archived 2026-09-04 — ADDED the
>   requirement "A Charge Session Carries A Lifecycle Status" and MODIFIED the retrieval and
>   correction requirements to carry it (+1 added, ~2 modified).

> **Delta note (why the status bullets are narrower than the spec).** The live guide was
> hand-edited on 2026-09-03 while fixing review finding R1-F1, so it ALREADY states that
> `VerifySuperchargerSession`'s SET clause reaches `status` as a fourth computed target, and
> that no gateway route can supply it. Those facts are deliberately NOT repeated below. What
> the guide still lacks — and what only the spec can supply — is the *behavioral* rule: what
> each of the three states means, and that a correction recomputes it every time.

---

## [guide] ## Glossary — REPLACE

- **Known as:** `supercharger stats`, `Supercharger session`, `charge session log`, `Supercharger Stats page`, `/supercharger-stats`, `fast charging stats`, `session battery edit`, `verify session battery`, `battery percentage correction`, `session battery percentages`, `supercharger chart axes`, `session status`, `session lifecycle status`, `done calculated`
- **Internal name:** `SuperchargerStatsPage` / `SuperchargerStatsFragment` / `SuperchargerRowStatic` / `SuperchargerRowEditFragment` / `SuperchargerRowUpdate` (gateway handlers) → `charging.SessionReader` (`ListSessionsByVehicleBetween`) / `charging.SessionVerifier` (`VerifySession`) / `charging.SessionWriter` — table `charging.supercharger_sessions` (renamed from `charge_sessions`, RM39 tier 3), owned by the `charging` module. **Changed by RM30** (was `telemetry.SuperchargerReader` over telemetry's own, still-`public`, `supercharger_sessions`, the raw ingestion buffer). **Extended by RM31 tier 4**: the chart carries `YYYY-MM` bar labels + reused kWh y-axis ticks, and the session table carries the record's battery-percentage columns. **Write path added by RM31 tier 5** (`RM31-gateway-add-session-battery-edit`): a signed-in user can now correct a session's `start_battery_pct`/`end_battery_pct` inline through `charging.SessionVerifier.VerifySession` — still NO Create and NO Delete (see "Write path" below). **Narrowed by RM41 tier 2** (`RM41-charging-drop-estimate-columns`, 2026-09-03): the record now carries **two** percentages plus a provenance — `start_battery_pct`, `end_battery_pct`, `battery_pct_source` — not four. The frozen estimated pair `start_battery_pct_est` / `end_battery_pct_est` no longer exists in the table, in `charging.Session`, or on the page. **Extended by RM41 tier 4** (`RM41-charging-add-session-status`, MAG-45, 2026-09-04): the record also carries a **lifecycle status** — `charging.SessionStatus` on `charging.Session.Status`, backed by `charging.supercharger_sessions.status` (TEXT + CHECK, DEFAULT `IN_PROGRESS`) — with three codes `IN_PROGRESS` / `DONE_CALCULATED` / `DONE`, computed by `VerifySession` alone.

- **Inferred pack capacity:** `charging.Session.InferredCapacityKWhCalc` (`*float64`), backed by the DB-generated column `charging.supercharger_sessions.inferred_capacity_kwh_calc` (renamed from `charge_sessions.inferred_capacity_kwh_calc`, RM39 tier 3). Also known as `inferred capacity`.

## [guide] ## Conventions & gotchas — APPEND

- **A charge session record carries exactly two battery percentages plus one provenance — never a frozen estimated pair.** `start_battery_pct`, `end_battery_pct` and `battery_pct_source` are the whole verification channel. The frozen estimated pair a prior revision also stored was removed because no code path in the platform ever wrote it, and the estimator originally meant to populate it (`derivedStartBatteryPct`) writes the real verified start percentage instead. Do not re-add an estimate column believing it is merely unimplemented — it was deliberately reversed.
  _Source: spec charge-session-log — Requirement: Battery Percentage Verification On A Charge Session._
- **Any recorded percentage is 0–100 inclusive, provenance is "user verified" or "polled", and a record carrying either percentage must also carry a provenance.** The constraint is enforced in the same statement that sets or clears the percentages, so no intermediate row state can violate it.
  _Source: spec charge-session-log — Requirement: Battery Percentage Verification On A Charge Session._
- **Synchronizing charge session records never writes, clears or overwrites the verification channel** — not even when the same sync pass updates that record's registered vehicle identifier or its energy, cost, currency or payment facts. The nightly sync's own type has no field to bind one to, so a reversal of this rule would fail to compile rather than corrupt data silently.
  _Source: spec charge-session-log — Requirement: Battery Percentage Verification On A Charge Session._
- **A retrieved record carries every fact the capability holds, so a caller needs no second request to describe the session** — site, energy, cost, currency, payment status, the two verified percentages, their provenance, and the lifecycle status. When adding a fact to the table, add it to the read port too, or you reintroduce the follow-up request this rule exists to prevent.
  _Source: spec charge-session-log — Requirement: Charge Sessions Are Retrievable For A Vehicle Within A Time Window._
- **A correction changes the start percentage, the end percentage, the provenance and the lifecycle status, and nothing else** — identity, time window, site, energy, cost, currency and payment facts are unaffected however many times a correction runs, and whether the start percentage was supplied or derived. This list no longer names a frozen estimated pair.
  _Source: spec charge-session-log — Requirement: A Charge Session's Battery Percentages Are Correctable By A Human._
- **A correction naming a non-existent record and one naming another account's record are rejected identically** — the two cases are indistinguishable to the caller by design, so a probe cannot use the error to discover whether a session id exists.
  _Source: spec charge-session-log — Requirement: A Charge Session's Battery Percentages Are Correctable By A Human._
- **The lifecycle status is a function of the two percentages, and the ONLY place the derived/supplied distinction survives.** Either percentage absent ⇒ `IN_PROGRESS` — including a record carrying only a start, and one carrying only an end whose start could not be derived. Both present ⇒ `DONE_CALCULATED` when *this write* derived the start, `DONE` in every other case. Provenance deliberately does NOT record that distinction (a derived start is still `user_verified`), so the status is the one signal that tells the two apart. Do not try to recover it from `battery_pct_source`.
  _Source: spec charge-session-log — Requirement: A Charge Session Carries A Lifecycle Status._
- **The status is recomputed on EVERY correction, never left at its prior value — including a correction that clears both percentages, which resets it to `IN_PROGRESS`.** A `DONE_CALCULATED` record whose start is later supplied directly becomes `DONE`. Treat the column as a cache of the current percentages, not as a historical fact about the record: nothing in it is meant to survive a correction that contradicts it.
  _Source: spec charge-session-log — Requirement: A Charge Session Carries A Lifecycle Status._
- **The status is never an input.** No caller — gateway route, mirror, or sync pass — may supply it; `VerifySession` computes it, and `MirrorSuperchargerSession` deliberately excludes it from both its INSERT column list and its `ON CONFLICT DO UPDATE SET`, so a freshly mirrored session takes `IN_PROGRESS` from the column DEFAULT. Adding it to the mirror to "complete the pattern" would let ingestion overwrite a human's verified state.
  _Source: spec charge-session-log — Requirement: A Charge Session Carries A Lifecycle Status._

## [index] ## Workflows — ADD ROWS

| `supercharger session status` (the three-state `charging.Session.Status`: `IN_PROGRESS` / `DONE_CALCULATED` / `DONE`, recomputed on every correction) | `workflows/supercharger-stats-read.md` |

---

## Manual review notes — two `INDEX.md` rows this proposal CANNOT fix automatically

`apply-sync`'s `[index]` grammar is **ADD ROWS** only: it appends rows and skips any whose first
cell already exists. It has no way to *edit* a row already present, so adding a corrected row here
would create a duplicate instead of replacing the wrong one. Fix both by hand when applying:

**1. `kkpa/context/INDEX.md`, Workflows table** — this row's first cell is now wrong (carried over
from the superseded draft; still unfixed):

```
| `supercharger session battery percentages` (the four `charging.Session` % columns; nil ⇒ `—`) | `workflows/supercharger-stats-read.md` |
```

Suggested replacement (`four` → the real shape after RM41 tier 2):

```
| `supercharger session battery percentages` (the two `charging.Session` % columns + `battery_pct_source`; nil ⇒ `—`) | `workflows/supercharger-stats-read.md` |
```

**2. `kkpa/context/INDEX.md`, entities table (line ~15)** — the `Supercharger session` row
describes the record's correctable surface and now omits the status:

```
| `Supercharger session` | `charging.Session` / mirrored into `supercharger_sessions` ... its `start_battery_pct`/`end_battery_pct` are correctable by the gateway via `charging.SessionVerifier` (RM31) — still no gateway Create or Delete | entity | `workflows/supercharger-stats-read.md` |
```

Suggested addition to that cell: after the `RM31` clause, note that the same call auto-sets a
three-state `status` (RM41 tier 4, MAG-45) which no caller may supply.

No other INDEX row needs changing: the concept and its aliases all still resolve correctly, and
the rows at lines 14–21 describe the ports and use case, not the column count or the status.
