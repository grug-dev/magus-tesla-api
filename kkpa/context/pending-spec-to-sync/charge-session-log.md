# Sync proposal — charge-session-log

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `workflows/supercharger-stats-read.md`
Source spec:  `openspec/specs/charge-session-log/spec.md`
Generated:    2026-08-29
Status: PENDING REVIEW

> **Routing note (for the reviewer).** `charge session log` already resolves to a `workflows/`
> file that predates the use-case/workflow boundary rule, so per the skill's "existing
> `workflows/` files are left alone" rule this proposal **extends that guide in place**. Same
> question as the `manual-charge-log` proposal — say so if you would rather migrate it.

---

## [guide] ## Glossary — APPEND

- **Inferred pack capacity:** `charging.Session.InferredCapacityKWhCalc` (`*float64`), backed by the DB-generated column `charge_sessions.inferred_capacity_kwh_calc`. Also known as `inferred capacity`.

## [guide] ## Conventions & gotchas — APPEND

- **The inferred pack capacity is derived by the database, never by Go.** It is a
  `GENERATED ALWAYS AS (…) STORED` column, so it is correct on every write path with no caller
  action. Do not add a Go-side computation and do not name the column in any write.
  _Source: spec charge-session-log — Requirement: Inferred Pack Capacity Is Recorded On Every Charge Session._
- **Two independent paths keep it fresh, and neither one names it.** The nightly mirror's
  `ON CONFLICT DO UPDATE SET` refresh of `energy_kwh` as Tesla's fees settle, and a human
  correcting the percentages through `charging.SessionVerifier.VerifySession`. The value
  recomputes from opposite directions without either path knowing it exists — including on the
  `charging.Session` that `VerifySession` itself returns, because the query is `RETURNING *`.
  _Source: spec charge-session-log — Requirement: Inferred Pack Capacity Is Recorded On Every Charge Session._
- **`SessionMirror` deliberately has no field for it, exactly as it has none for the verified
  percentages.** That is not an omission to "fix" — the mirror must not be able to write either.
  _Source: spec charge-session-log — Requirement: Inferred Pack Capacity Is Recorded On Every Charge Session._
- **Expect mostly `nil` on this table.** The value needs the verified percentages, which only
  exist for sessions a human has verified, plus a non-`NULL` `energy_kwh` (a session with no kWh
  fee has none). An unverified session legitimately records no capacity.
  _Source: spec charge-session-log — Requirement: Inferred Pack Capacity Is Recorded On Every Charge Session._
- **A recorded absence must never fail a synchronization pass.** The guard (all three inputs
  present **and** end % strictly greater than start %) exists partly to protect the nightly
  mirror: an equal delta would be a division by zero, and because one bad row rejects the whole
  `MirrorSessions` call, that error would abort the entire night's sync for the account. The
  column's type is unconstrained `NUMERIC` for the same reason — `energy_kwh` is vendor-controlled
  `DOUBLE PRECISION` with no `CHECK`, so any fixed precision could overflow on data the project
  does not own and take down the sync.
  _Source: spec charge-session-log — Requirement: Inferred Pack Capacity Is Recorded On Every Charge Session._
- **Do not narrow the column's type.** If a test asserting a very large capacity (energy `1e9`
  over a 1-point delta) ever starts failing, someone added a precision constraint and reintroduced
  the abort-the-nightly-mirror risk.
  _Source: spec charge-session-log — Requirement: Inferred Pack Capacity Is Recorded On Every Charge Session._

## [index] ## Glossary & routing — entities — ADD ROWS

| `session inferred capacity` | `charging.Session.InferredCapacityKWhCalc` / `charge_sessions.inferred_capacity_kwh_calc` (DB-generated) | entity | `workflows/supercharger-stats-read.md` |
