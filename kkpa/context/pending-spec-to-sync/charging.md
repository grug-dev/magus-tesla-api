# Sync proposal — charging

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `workflows/supercharger-stats-read.md`
Source spec:  `openspec/specs/charging/spec.md`
Generated:    2026-09-14
Status: PENDING REVIEW

---

## Why this target

The spec's two new requirements — **Supercharger Session Vehicle Keying** and **Supercharger
Port Vehicle Scoping** — describe the session store's key and its port signatures. Two guides
already carry those facts correctly, so they get no block here:

- `architecture/charging-tables.md` — already states `tesla_id BIGINT NOT NULL`, no `account_id`,
  and `UNIQUE (session_id)`.
- `architecture/nightly-cycle.md` — already states the per-vehicle cursor, with spec citations.
- `use-case/charging/verify-session-battery.md` — already shows `VerifySession(ctx, teslaID, …)`
  and the always-runs recalculation.

`workflows/supercharger-stats-read.md` is the guide the spec **invalidates**. See "Manual edits
needed" below — one of its bullets now states the opposite of the spec.

---

## [guide] ## Conventions & gotchas — APPEND

- **A session is keyed on the vehicle, never on an account.** `charging.supercharger_sessions`
  carries `tesla_id NOT NULL` and no `account_id` column. Which cars a user may see is recorded
  once, by the account module's vehicle registry. Do not add an account column back to scope a
  read — scope it by `tesla_id`.
  _Source: spec charging — Requirement: Supercharger Session Vehicle Keying._

- **One `session_id` is one stored row, store-wide.** Uniqueness is `UNIQUE (session_id)`, not a
  pair. A Supercharger session happened to exactly one car, so two rows for one `session_id`
  would be two records of one event. Re-mirroring the same `session_id` under a different
  vehicle updates the row to the newest vehicle; it never inserts a second one.
  _Source: spec charging — Requirement: Supercharger Session Vehicle Keying._

- **When the re-key had to collapse a duplicated pair, the copy with human-entered percentages
  wins.** Every other column is re-derived from the mirrored source on the next sync, so the
  hand-entered battery percentages are the only value a delete could destroy.
  _Source: spec charging — Requirement: Supercharger Session Vehicle Keying._

- **Every public Supercharger port takes `teslaID int64` and no account id.** This covers the
  mirror write, the three session reads, and the verification write. A port that still asks for
  an account id is stale code, not a second scoping style.
  _Source: spec charging — Requirement: Supercharger Port Vehicle Scoping._

- **`VerifySession` keeps a scope — it did not lose one.** It matches on BOTH `id` AND
  `tesla_id`. Naming a vehicle the session does not belong to changes nothing and returns the
  same error an unknown id returns, so "not yours" and "does not exist" stay indistinguishable
  from outside. Never replace that predicate with an id-only match.
  _Source: spec charging — Requirement: Supercharger Port Vehicle Scoping._

- **The mirror write validates no owning account, and an empty set stays a successful no-op.**
  No session carries an account, so there is nothing to check across the batch.
  _Source: spec charging — Requirement: Supercharger Port Vehicle Scoping._

---

## Manual edits needed (the block grammar cannot express these)

`APPEND` and `ADD ROWS` can only add. These three lines already exist and now say the opposite
of the spec, so they need a hand edit at apply time.

**1. `workflows/supercharger-stats-read.md` — DELETE this bullet from `## Conventions & gotchas`
(it is around line 99):**

> - **Sessions with a `NULL` `TeslaID` are invisible here, by construction and by design** — the
>   single read is scoped by `TeslaID`, so they can never match. Never add a second account-wide
>   read to discover or disclose them, and show no "N sessions hidden" notice. _Source: spec
>   gateway — Requirement: Unattributed Supercharger sessions are out of scope._

Why: `tesla_id` is `NOT NULL`. A session with no vehicle cannot exist, so there is no hidden set
to reason about. The bullet teaches a reader to guard a branch the schema has removed. The
"never add an account-wide read" advice survives in the first new bullet above.

**2. `INDEX.md` line 267 — replace `per-account` with `per-vehicle`:**

> `| `mirror_watermarks` | the per-account mirror cursor; never advances to `now()` → `architecture/charging-tables.md` |`

**3. `INDEX.md` line 285 — replace `per-account` with `per-vehicle`:**

> `| `mirror watermark` (the per-account cursor bounding step 2 of the nightly cycle; holds telemetry's `updated_at`, owned by `internal/charging`) | `architecture/nightly-cycle.md` |`

Why both: the cursor table is keyed on `tesla_id` now. An account with two cars holds two
cursors. `architecture/nightly-cycle.md` already says this correctly; only the INDEX rows that
point at it are stale.

---

## [index] ## Architecture topics — ADD ROWS

| `session vehicle keying` | why a Supercharger session carries `tesla_id` and no `account_id`, and why `session_id` is unique store-wide → `architecture/charging-tables.md` |
| `supercharger port scoping` | every charging Supercharger port takes `teslaID` alone; `VerifySession` matches id AND vehicle → `workflows/supercharger-stats-read.md` |
| `wrong vehicle on a session write` | returns the same error an unknown id returns, on purpose → `workflows/supercharger-stats-read.md` |
