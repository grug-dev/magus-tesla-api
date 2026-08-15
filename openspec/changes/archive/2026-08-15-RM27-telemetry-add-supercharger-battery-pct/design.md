## Context

`internal/telemetry` owns `supercharger_sessions` (migration `20260716000001`, one row per
Tesla `session_id`, upserted nightly via `UpsertSuperchargerSession` because billing state
mutates post-session — `is_paid`/invoices finalize over time). The table's own header
comment already documents "no battery percentage" because `GET /api/1/dx/charging/history`
carries no SOC field — verified against the real captured response
(`cmd/explore-tesla-api/output/2-ChargingHistory.json`).

MAG-14 (source ticket) asks for start/end battery percentage on this table. The roadmap
(`openspec/roadmaps/RM27-supercharger-battery-percentage.md`) settled seven binding
decisions (R1–R7) with the user via grill-me before this design was written. This design
implements R2/R3 (the schema) and R5's read-port half; R1/R4/R6 bound what this tier must
NOT do (no estimator, no persisted estimate); R7 bounds scope (no verification UI).

Primary and only module: **`internal/telemetry/`**. `internal/battery` (tier 2) is not
created, read, or touched by this change.

## Goals / Non-Goals

**Goals:**

- Three new nullable columns on `supercharger_sessions`: `start_battery_pct`,
  `end_battery_pct` (both `SMALLINT CHECK (0..100)`, mirroring `manual_charge_entries`),
  and `battery_pct_source` (`TEXT CHECK`, allowed values decided below) — the **live
  override trio**.
- Two more new nullable columns: `start_battery_pct_est`, `end_battery_pct_est` (same
  `SMALLINT CHECK (0..100)` shape) — a **frozen, write-once verification snapshot** of
  what the estimator showed at the moment a human corrected it (D6). NOT a revival of the
  nightly-refreshed `_est` shape R6 rejected — see D6 for the distinction and why it does
  not violate R4/R6.
- All five columns are **never auto-written**: excluded from
  `UpsertSuperchargerSession`'s `INSERT` column list and its `ON CONFLICT DO UPDATE SET`
  clause (R3 — the most load-bearing constraint in this change; D6 extends it to the two
  snapshot columns).
- `SuperchargerReader`'s two existing methods surface all five columns on every returned
  `SuperchargerSession`, so tier 2 (`internal/battery`) can implement `verified ??
  estimated` (R5) once it lands — and so a future drift-log consumer can read the frozen
  snapshot without a new port method.
- Correct `CHECK` behavior: out-of-range percentages and an unrecognized
  `battery_pct_source` value are rejected at the database; the two snapshot columns get
  the identical range `CHECK`.
- A same-day/any-day re-upsert of a session whose trio (and/or snapshot pair) was already
  set (by a future writer, out of scope here, or by direct SQL in this tier's own tests)
  leaves all five columns untouched even when the session's mutable/derived columns
  (billing, `raw_data`) change.

**Non-Goals:**

- No estimator. No code in this module computes a battery percentage from
  `energy_kwh`/timestamps — that is entirely `internal/battery`'s job (R4), a separate
  future change.
- **No nightly-refreshed `_est` columns** — the specific shape R6 rejected (a `_est` pair
  recomputed and overwritten on every poll, which goes silently stale as the taper model
  improves while pretending to be current). `start_battery_pct_est`/`end_battery_pct_est`
  (D6) are a different thing: a dated, write-once observation that is *supposed* to stay
  exactly as recorded — see D6 for why that is not the same non-goal.
- No verification UI, no Writer method for either the trio or the snapshot pair, no
  gateway change. Nothing in this repository writes any of the five columns after this
  migration applies (R7).
- No new index (justified below).

---

## Design Decisions

### D1 (R2) — Exact column shape, mirroring `manual_charge_entries`

```sql
start_battery_pct   SMALLINT CHECK (start_battery_pct BETWEEN 0 AND 100),
end_battery_pct     SMALLINT CHECK (end_battery_pct   BETWEEN 0 AND 100),
```

Both nullable, no `DEFAULT`. This is a verbatim mirror of
`manual_charge_entries.start_battery_pct`/`end_battery_pct`
(`internal/manualcharge/db/migrations/20260718000001_add_manual_charge_entries.sql:35-36`):
same type (`SMALLINT` — 0–100 fits comfortably in `int16`, no reason for `INTEGER`), same
inline range `CHECK`, same nullability, same lack of a cross-field "end >= start" check
(unlike `manual_charge_entries`'s `started_at`/`ended_at` ordering check, "end % >= start %"
is not always true for a charge — DC fast-charging is monotonically increasing SOC in
practice, but asserting that in the schema forecloses a future correction path for bad data
with no compensating benefit; the verification UI, when it ships, is the right place to
validate user input, not a blanket DB constraint on a column no writer touches yet).

**Rejected alternative:** `INTEGER` instead of `SMALLINT`. Rejected — no value in this
column can exceed 100, `SMALLINT`'s 2-byte width is strictly sufficient, and matching
`manual_charge_entries`'s precedent (`ai/go-conventions.md`'s "AI-efficiency" principle: a
closed, small vocabulary an agent mirrors instead of re-deriving) removes a judgment call a
future agent would otherwise have to re-make from scratch.

### D2 (R2) — `battery_pct_source`: TEXT + CHECK, nullable, no DEFAULT, value set `('user_verified', 'polled')`

```sql
battery_pct_source  TEXT CHECK (battery_pct_source IN ('user_verified', 'polled')),
```

**Shape — TEXT + CHECK, not a Postgres `ENUM` type:** mirrors the project's own existing
precedent for exactly this kind of small closed vocabulary —
`manual_charge_entries.charging_type CHECK (IN ('AC','DC'))` and `.location_kind CHECK (IN
('HOME','WORK','OTHER'))` (same migration file, lines 37–38). A Postgres `ENUM` type would
require `ALTER TYPE ... ADD VALUE` (which cannot run inside the same transaction as other
DDL, and cannot be removed) to extend later; a `CHECK` constraint is extended by a plain
`ALTER TABLE ... DROP CONSTRAINT ... ADD CONSTRAINT ...` in a normal future migration — the
exact mechanism the roadmap's R2 asks this design to "leave room" for a future `'polled'`
value with.

**Nullable, no DEFAULT — NOT `NOT NULL DEFAULT '...'`:** a `DEFAULT` would force every
row this tier's own migration or the nightly poller ever inserts to carry a non-NULL
`battery_pct_source`, which would be a lie — nothing computes or asserts a source for those
rows. NULL is the correct, honest state for "no override exists."

**Why the allowed value set is `('user_verified', 'polled')` and NOT `('estimated',
'user_verified', 'polled')`, despite the roadmap prompt's "must distinguish 'estimated'
from 'user_verified'":** that distinction is real and must be preserved, but it does not
require `'estimated'` to ever be a value **stored** in this column. R4 places the
estimator in `internal/battery`, computing **on read**; R6 forbids persisting that
estimate anywhere, in any column, precisely because a stored estimate goes stale the
moment the taper model improves. If `battery_pct_source` could hold `'estimated'`, some
future writer would eventually be tempted to persist "estimated, as of write time" — the
exact anti-pattern R6 rules out. Instead, the distinction is carried **structurally**, by
column presence, not by a stored label:

- `start_battery_pct`/`end_battery_pct`/`battery_pct_source` all NULL ⟹ **estimated**: tier
  2's `verified ?? estimated` (R5) falls through to its live, on-read computation. Nothing
  in telemetry ever says the word "estimated" — the absence of an override *is* the signal.
- `battery_pct_source` non-NULL ⟹ a human-owned override exists, and this column says why:
  `'user_verified'` (the future verification UI) or `'polled'` (the future measured-SOC
  polling alternative the roadmap logged to the backlog as a trigger to revisit, not
  chosen now).

**Amendment — this structural distinction still holds with `start_battery_pct_est`/
`end_battery_pct_est` in the schema (D6), and it is worth restating precisely why:** D6's
two snapshot columns do NOT reopen the question of whether `'estimated'` belongs in this
CHECK's value set. `battery_pct_source` continues to describe only the trio's *live*
value — why the current override exists — never the snapshot pair. The two `_est` columns
carry their own meaning entirely through column identity, not through `battery_pct_source`:
they record what the estimate **was**, once, at verification time, while the *absence* of
the trio (this section's rule, unchanged) continues to signal "no override — compute live."
The two facts compose without conflict: a verified session has `battery_pct_source =
'user_verified'` (why the live trio exists) **and** `start_battery_pct_est`/
`end_battery_pct_est` (what was displayed when it was set) — two independent, non-competing
pieces of information, neither of which is the word `'estimated'` stored anywhere.

**Rejected alternatives:**
- **Include `'estimated'` in the CHECK's value set, written by a future write path that
  persists the tier-2 estimate into telemetry.** Rejected on R4 (would require `battery →
  telemetry`, an import cycle back into the module that stores the trio) and R6 (a
  persisted estimate goes stale on model improvement — this is the roadmap's own stated
  rejection reason for the 5-column shape the user's original ticket implied).
- **A Postgres `ENUM` type (`CREATE TYPE battery_pct_source_t AS ENUM (...)`).** Rejected
  per the shape discussion above — harder to extend, no compensating benefit over `CHECK`,
  and inconsistent with the project's own two existing precedents in the sibling
  `manual_charge_entries` table.
- **A cross-column consistency `CHECK` (`battery_pct_source IS NOT NULL` iff at least one
  pct column is non-NULL, or iff both are).** Considered and rejected as premature: the
  verification UI that is the only thing that will ever write these columns does not exist
  yet (R7), so asserting its input-validation rules in the schema now risks blocking a
  legitimate future shape (e.g., a UI that lets a user verify only the start or only the
  end percentage) that hasn't been designed. If the verification UI's design later wants
  this invariant, it is a one-line addition in that change, not a preemptive constraint
  here.

### D3 (R3, LOAD-BEARING) — Mechanism: omit the trio from the upsert entirely, not merely from `DO UPDATE SET`

The roadmap phrases R3 as "excluded from `ON CONFLICT DO UPDATE SET`." This design goes one
step further and excludes the trio from the `INSERT` column list too — i.e.,
`UpsertSuperchargerSession`'s SQL text (`internal/telemetry/db/query.sql:312-334`) is left
**completely unchanged** except for an added header comment. This is stronger than the
minimum R3 asks for, for a concrete reason:

- Nothing in this repository writes the trio in this tier (R7). If the columns were added
  to the `INSERT` list bound to Go params that are always zero-value (`nil`) today, a
  future worker extending this query for an unrelated reason could plausibly "complete the
  pattern" by copying the trio into `DO UPDATE SET` too, defeating R3 by well-intentioned
  imitation of the query's own existing shape (every other column in this query appears in
  both places).
- By contrast, a column that is **absent from the query text entirely** has nothing to
  imitate. A future writer for the trio (the verification UI's Writer port, tier 2's
  follow-up) will need its own dedicated query — e.g. `SetSuperchargerBatteryPct` — a
  natural place to reason about R3 fresh rather than inherit it silently.
- A fresh `INSERT` with the three columns absent from the column list gets the SQL column
  default (`NULL`, since none of the three columns declares a `DEFAULT`) automatically —
  satisfying test-contract (a) below with zero code.

**Verifiable as a task:** tasks.md's T7.2 is the direct regression test (re-upsert with
changed billing data leaves an already-set trio untouched), and T4's acceptance criteria
require grepping the final `UpsertSuperchargerSession` query text for the three column
names and asserting zero matches — a static, deterministic signal an agent or reviewer can
run without a database.

**Rejected alternative:** add the trio to the `INSERT` list only (bound to
always-`NULL`/zero-value Go params), omitting them only from `DO UPDATE SET`. Functionally
equivalent for a fresh insert, but reintroduces exactly the imitation risk above, and adds
three parameters to a Go call site (`upsertSuperchargerSession`, `service.go`) that would
always be `nil` in this tier — dead code with no test that can meaningfully exercise the
non-nil branch (since no writer produces one). Rejected in favor of the zero-footprint
"absent entirely" shape.

**D6 extends this exact mechanism, unmodified, to `start_battery_pct_est`/
`end_battery_pct_est`** — see D6 below. The two snapshot columns are absent from
`UpsertSuperchargerSession` for the identical reason and by the identical means as the
trio; D3's rejected alternative applies equally to them, and is not re-argued there.

### D4 (R3) — Update the table's own comment; it is now stale

The table's `COMMENT ON TABLE supercharger_sessions` (migration `20260716000001:50-55`)
currently asserts "no battery percentage." This design corrects that claim in the same
migration that adds the trio — leaving it uncorrected would mean the table's own
authoritative, in-database documentation contradicts its own schema the moment this
migration applies. See the DDL below for the exact replacement text.

### D5 — Go-level surface: additive struct fields, one new mapping helper, no write-path change

`SuperchargerSession` (`internal/telemetry/telemetry.go:293-313`) gains five pointer
fields at the end of the struct (mirroring the migration's physical column-append order —
the module's established convention, most recently followed by the five `_calc` columns in
`telemetry-add-derived-consumption-columns`):

```go
StartBatteryPct     *int    // 0-100 inclusive; SMALLINT CHECK in DB. NULL = no human-verified value (tier 2's on-read estimate applies, R5) — NEVER written by this tier (R3/R7)
EndBatteryPct       *int    // same shape/nullability as StartBatteryPct
BatteryPctSource    *string // 'user_verified' | 'polled' | NULL. NULL = no override exists. NEVER 'estimated' — that state is computed on read by internal/battery and never persisted here (R6)
StartBatteryPctEst  *int    // FROZEN write-once snapshot of the estimate when StartBatteryPct was verified (design D6). NULL until verified. NEVER refreshed after write; NEVER read back into internal/battery's live computation — not a cache
EndBatteryPctEst    *int    // same frozen/write-once/never-a-cache semantics as StartBatteryPctEst, paired with EndBatteryPct
```

`rowToSuperchargerSession` (`internal/telemetry/mapping.go:153-`) gains five mapped
fields using:
- A **new** helper, `pgNullableInt16AsInt(v pgtype.Int2) *int`, added to `mapping.go`
  alongside the module's existing `pgNullableFloat64`/`pgNullableInt32AsInt`/
  `pgNullableText` (line 15/38/49) — same nil-on-invalid shape, sized for `SMALLINT`
  (`pgtype.Int2`) instead of the existing `pgtype.Int4`/`pgtype.Float8` helpers. This is
  the module's first `SMALLINT` column, hence the first `Int2` helper; `internal/manualcharge`
  already established the identical pattern (`intPtrToPgInt2`/`pgInt2ToIntPtr`,
  `internal/manualcharge/service.go:381-`) for its own `start_battery_pct`/`end_battery_pct`
  — this design mirrors that shape rather than importing it (modules never import each
  other's `db`-adjacent internals; the four-line helper is cheaper to duplicate than to
  abstract across a module boundary for a single reuse). **Reused for all four `SMALLINT`
  columns** (`StartBatteryPct`, `EndBatteryPct`, `StartBatteryPctEst`, `EndBatteryPctEst`) —
  one helper, four call sites, no per-column duplication.
- The **existing** `pgNullableText` helper for `battery_pct_source` — no new helper needed.

`upsertSuperchargerSession` (`internal/telemetry/service.go:863-`) is **functionally
unchanged** — it does not read `s.StartBatteryPct`/`s.EndBatteryPct`/`s.BatteryPctSource`/
`s.StartBatteryPctEst`/`s.EndBatteryPctEst` at all, because
`telemetrydb.UpsertSuperchargerSessionParams` (sqlc-generated from the unchanged query) has
no fields for them. A one-line comment is added near the function noting this is deliberate
(D3/D6/R3), for a future reader who might otherwise wonder why five struct fields are
visibly unused at that call site.

**Rejected alternative:** write a no-op mapping in `upsertSuperchargerSession` that
explicitly sets three local `pgtype.Int2{}`/`pgtype.Text{}` zero values "for clarity," even
though the generated params struct has no fields to receive them. Rejected — dead code that
would not compile (the generated struct literally has no such fields to assign), and the
header comment achieves the same clarity without inventing assignments to nothing.

### D6 — `start_battery_pct_est` / `end_battery_pct_est`: a frozen, write-once verification snapshot (NOT a revival of the rejected nightly-refreshed shape)

Two more nullable columns, same shape as D1's trio:

```sql
start_battery_pct_est  SMALLINT CHECK (start_battery_pct_est BETWEEN 0 AND 100),
end_battery_pct_est    SMALLINT CHECK (end_battery_pct_est   BETWEEN 0 AND 100),
```

**What they are:** a **permanent drift log**, not a cache and not a second copy of the
live trio. When the (future, out-of-scope) verification UI writes
`start_battery_pct`/`end_battery_pct`/`battery_pct_source` in a single write, it also
writes `start_battery_pct_est`/`end_battery_pct_est` in that **same** write — capturing
what `internal/battery`'s estimator displayed to the human **at the moment of correction**
(e.g. "the model said 82, the human said 79"). After that one write, both snapshot columns
are **frozen**: nothing ever updates them again, including a later re-verification of the
same session (see rejected alternatives below) or a later improvement to the taper model.

**A future reader must not "fix" this back to a nightly-refreshed pair — read this
paragraph first if tempted.** `start_battery_pct_est`/`end_battery_pct_est` LOOK like the
5-column shape the roadmap's "Rejected alternatives" section already rejected (`_est` pair
written nightly), but they are a different mechanism with the opposite correctness
property:

| | Rejected nightly-refreshed `_est` (roadmap "Rejected alternatives") | `start/end_battery_pct_est` (D6, this design) |
|---|---|---|
| Written | Every nightly poll, by the poller | ONCE, by the verification UI, at verification time |
| Refreshed | Yes — recomputed on every poll | NEVER — frozen the instant it is written |
| Staleness relative to a newer taper model | A BUG (silently wrong, pretends to be current) | The ENTIRE POINT (a dated observation is supposed to never change) |
| Legal writer | None — see below | The gateway verification UI, via a telemetry write port |
| Violates R6? | Yes — this is exactly what R6 forbids | No — R6 forbids a value that pretends to track a model it no longer reflects; a value that openly records a historical snapshot makes no such claim |

**Why a nightly-refreshed `_est` had no legal writer, and why that is what actually killed
it (this is why the frozen-snapshot shape is architecturally possible at all where the
nightly shape was not):** `internal/battery` owns the estimator and `internal/telemetry`
owns `supercharger_sessions` (`ai/architecture.md` §2 module data ownership — a module
never writes another module's table). A nightly-refreshed `_est` pair would need SOME
writer computing a fresh estimate on every poll and persisting it into telemetry's table:
`battery` cannot write into telemetry's table (ownership), and telemetry computing the
estimate itself would just be the same forbidden dependency inverted (`telemetry →
battery`, still a cycle against `battery → telemetry`, R4). **No caller can legally
perform "compute in battery, persist in telemetry, every night, unattended."** The frozen
snapshot sidesteps this entirely: it is written **once**, by a **human-initiated** action
in the gateway, which is allowed to depend on both modules at once —
`gateway → battery` (read the current estimate to show the human) and `gateway → telemetry`
(write it, in the same request, alongside the trio, through a dedicated telemetry write
port). No module ever imports another module's internals; the gateway is the one place in
the architecture that is expected to compose two ports (`ai/architecture.md` §5's
`gateway → account.TokensFor → tesla.GetVehicleData → domain module` request-flow pattern
is the identical shape: the gateway is the orchestrator that legally sees more than one
module at a time). This is not implemented in this tier (R7 — no gateway/Writer code
ships here) but is why the schema is safe to add now: a legal writer *exists in the
architecture*, even though it is not built yet.

**Never an input to tier 2's computation, and never treated as a cache:** `internal/battery`
computes the live estimate fresh on every read (R4/R5/R6, unchanged by this decision).
`start_battery_pct_est`/`end_battery_pct_est` are written **from** that computation at one
point in time; they are never read **back into** it. A future tier-2 implementation that
opportunistically reads these columns "to save a computation" would defeat the entire
purpose of the drift log (there would be nothing left to compare a later verification
against) — this design explicitly forbids that usage, and `AGENTS.md` (tasks.md T7.1) must
carry the same warning where a future `battery` module implementer will read it.

**Same R3 protection, extended:** `start_battery_pct_est`/`end_battery_pct_est` are
excluded from `UpsertSuperchargerSession`'s `INSERT` column list and `ON CONFLICT DO UPDATE
SET` clause by the identical mechanism as the trio (D3) — the nightly poller has no
legitimate reason to ever touch them, and the same imitation risk D3 describes applies
identically.

**Rejected alternatives:**
- **The nightly-refreshed `_est` shape itself** (the roadmap's original "Rejected
  alternatives" entry, restated here with the sharper reason): no legal writer exists for
  it under the module-ownership rule, independent of the staleness argument R6 already
  gives. Two independent reasons to reject the same shape.
- **A separate audit/history table** (e.g. `supercharger_battery_pct_verifications`, one
  row per verification event, FK'd to `session_id`). Considered — it would cleanly extend
  to preserve **every** re-verification of the same session as its own row, whereas the
  two frozen columns in this design can hold only the single most recent verification's
  snapshot (a second verification of an already-verified session would overwrite
  `start_battery_pct_est`/`end_battery_pct_est`, same as it overwrites the trio itself).
  Rejected for THIS tier as disproportionate: a second table plus a second query for what
  is, in practice, a low-volume log (one row per user-initiated verification, not a
  per-poll write) is more machinery than the single-verification case justifies. **Logged
  here as the trigger to revisit**: if repeated re-verification of the same session turns
  out to be common enough that losing prior snapshots matters, promote the drift log to
  its own table in a future change — the two frozen columns are not meant to preclude that
  migration path.
- **A `verified_at` timestamp column alongside the trio.** Offered to the user during this
  design's review and declined for now — no current consumer needs "when was this
  verified," only "what did the estimate say when it was verified" (which
  `start_battery_pct_est`/`end_battery_pct_est` already answer) and "is it verified" (which
  `battery_pct_source IS NOT NULL` already answers). Not added; can be added later as a
  sixth nullable column with no migration conflict if a future need arises.

---

## Schema

### DDL (goose migration)

**Filename:** `internal/telemetry/db/migrations/20260815000001_add_supercharger_battery_pct.sql`
(next available slot after `20260814000001`).

```sql
-- +goose Up
-- internal/telemetry — add the human-owned battery-percentage verification/override
-- trio, plus a frozen verification-time snapshot pair, to supercharger_sessions
-- (MAG-14, RM27-telemetry-add-supercharger-battery-pct, tier 1 of RM27 —
-- openspec/roadmaps/RM27-supercharger-battery-percentage.md).
--
-- start_battery_pct / end_battery_pct / battery_pct_source are NOT model output and
-- NOT derived from the Tesla API (GET /api/1/dx/charging/history carries no SOC field
-- of any kind — verified against the real captured response). They exist purely as a
-- human-owned verification/override channel: NO code in this repository writes them
-- as of this migration. The verification UI that will eventually write them is a
-- separate, out-of-scope future change (R7, backlog entry 11). The estimator that
-- computes a value to show when no override exists lives in internal/battery,
-- computes ON READ, and NEVER persists its result here (R4/R6) — this table has no
-- column for an ongoing/refreshed estimate and never will; NULL on the trio IS the
-- "no override, use the live estimate" state (R5).
--
-- start_battery_pct_est / end_battery_pct_est (design D6) are a DIFFERENT thing from
-- an "estimate column" in the sense R6 forbids: they are written EXACTLY ONCE, in the
-- SAME write that sets the trio, capturing what internal/battery's estimator showed
-- AT THAT MOMENT — a permanent, frozen drift log ("model said 82, human said 79").
-- They are NEVER refreshed afterward, including by a later, improved taper model;
-- staleness relative to a newer model is the CORRECT, intended behavior for a dated
-- observation, not a bug. They are NEVER read back into internal/battery's live
-- computation (never a cache). Do NOT "fix" these into a nightly-refreshed pair in a
-- future change — see design.md D6 for the full distinction and why a
-- nightly-refreshed pair has no legal writer under this project's module-ownership
-- rule, while this write-once pair does (gateway -> battery read, gateway ->
-- telemetry write, both legal, no cycle).
--
-- LOAD-BEARING (R3): all FIVE of these columns are DELIBERATELY ABSENT from
-- UpsertSuperchargerSession's INSERT column list and its ON CONFLICT DO UPDATE SET
-- clause (query.sql) — not merely excluded from the UPDATE half. The poller
-- re-upserts a session nightly because Tesla billing state mutates post-session
-- (is_paid, invoices finalize over time); if any of these five were bound as a
-- parameter of that query, a user's future verified value (or its frozen snapshot)
-- would be silently overwritten on the next nightly re-upsert — the concrete bug
-- this design exists to prevent. They belong on the "immutable, never overwritten"
-- side of this table's own header comment, alongside session_id/vin/timestamps —
-- not the "derived, refreshed on conflict" side energy_kwh/total_cost/is_paid/
-- raw_data are on.
ALTER TABLE supercharger_sessions
    ADD COLUMN start_battery_pct     SMALLINT CHECK (start_battery_pct     BETWEEN 0 AND 100),
    ADD COLUMN end_battery_pct       SMALLINT CHECK (end_battery_pct       BETWEEN 0 AND 100),
    ADD COLUMN battery_pct_source    TEXT     CHECK (battery_pct_source IN ('user_verified', 'polled')),
    ADD COLUMN start_battery_pct_est SMALLINT CHECK (start_battery_pct_est BETWEEN 0 AND 100),
    ADD COLUMN end_battery_pct_est   SMALLINT CHECK (end_battery_pct_est   BETWEEN 0 AND 100);

COMMENT ON COLUMN supercharger_sessions.start_battery_pct IS
    'Human-verified/override battery % at charge start (0-100). NULL = no override; '
    'reads fall back to internal/battery''s on-read estimate (R5). Excluded from '
    'UpsertSuperchargerSession''s INSERT and ON CONFLICT DO UPDATE SET -- never '
    'auto-written by the nightly poller (R3).';
COMMENT ON COLUMN supercharger_sessions.end_battery_pct IS
    'Human-verified/override battery % at charge end (0-100). Same NULL convention '
    'and the same R3 write-protection as start_battery_pct.';
COMMENT ON COLUMN supercharger_sessions.battery_pct_source IS
    'Why start/end_battery_pct are set: user_verified (verification UI, out of scope '
    'this tier) or polled (future measured-SOC alternative, logged to the backlog, '
    'not implemented). NULL means no override exists. Never ''estimated'' -- that '
    'state is computed on read by internal/battery and is never persisted here (R6).';
COMMENT ON COLUMN supercharger_sessions.start_battery_pct_est IS
    'FROZEN, write-once snapshot of internal/battery''s live estimate at the moment '
    'start_battery_pct was verified/overridden -- a permanent drift log entry, not a '
    'cache. Written exactly once, in the same write as the trio; NEVER refreshed '
    'again, including by a later improved taper model (staleness here is correct, '
    'not a bug -- design D6). NEVER read back into internal/battery''s live '
    'computation. Excluded from UpsertSuperchargerSession like the trio (R3).';
COMMENT ON COLUMN supercharger_sessions.end_battery_pct_est IS
    'FROZEN, write-once snapshot of internal/battery''s live estimate at the moment '
    'end_battery_pct was verified/overridden. Same write-once, never-refreshed, '
    'never-a-cache, R3-protected semantics as start_battery_pct_est (design D6).';

COMMENT ON TABLE supercharger_sessions IS
    'Tesla-billed Supercharger and DC fast-charging sessions per account. '
    'Covers sessions returned by GET /api/1/dx/charging/history only (no home/AC '
    'charging). The Tesla API itself carries no battery-percentage field; '
    'start_battery_pct/end_battery_pct/battery_pct_source are a human-owned '
    'verification/override channel, and start_battery_pct_est/end_battery_pct_est '
    'are a frozen write-once snapshot of the estimate at verification time (both '
    'added by RM27 tier 1, MAG-14) -- all five excluded from the nightly UPSERT so '
    'a verified value or its snapshot is never silently overwritten (R3). '
    'Owned by internal/telemetry; no other module reads this table directly. '
    'UPSERT on session_id (not append-only): billing state is mutable post-session.';

-- +goose Down
ALTER TABLE supercharger_sessions
    DROP COLUMN IF EXISTS end_battery_pct_est,
    DROP COLUMN IF EXISTS start_battery_pct_est,
    DROP COLUMN IF EXISTS battery_pct_source,
    DROP COLUMN IF EXISTS end_battery_pct,
    DROP COLUMN IF EXISTS start_battery_pct;
-- Down intentionally does not restore the pre-migration COMMENT ON TABLE text —
-- goose Down migrations in this module have never restored superseded comments
-- (see 20260805000001's and 20260806000001's own Down sections for the same
-- precedent); the comment is documentation, not schema, and a Down that ran this
-- migration's Up at all means the "no battery percentage" claim was already known
-- to be stale.
```

### Index plan

**No new index — both of the table's two existing indexes already fully serve every read
this tier adds or changes.**

- `idx_supercharger_sessions_vehicle_time (account_id, tesla_id, charge_start_date_time
  DESC)` serves `SuperchargerSessionsByVehicle`.
- `idx_supercharger_sessions_account_time (account_id, charge_start_date_time DESC)`
  serves `SuperchargerSessionsByAccount`.

Both queries are `SELECT * FROM supercharger_sessions WHERE ... ORDER BY
charge_start_date_time DESC LIMIT ...` (`query.sql:342-358`) — neither query's `WHERE`
predicate nor its `ORDER BY` references any of the five new columns, and this change does
not add a new query, a new predicate, or a new sort key on any of them. All five
(`start_battery_pct`, `end_battery_pct`, `battery_pct_source`, `start_battery_pct_est`,
`end_battery_pct_est`) ride along on the same heap-row fetch both queries already pay for,
exactly like every prior column-addition to this module's tables (TPMS pressures, the five
`_calc` columns, `max_range_charge_counter`) — no query plan changes for either existing
read.

**No index is added on `battery_pct_source` for a hypothetical future "worklist" query**
(e.g. "show all unverified sessions," `WHERE battery_pct_source IS NULL`). No such query
exists in this tier or its known future consumer (tier 2's read-only estimator, which reads
the trio off a row it already fetched by `session_id`/account, never by filtering on
`battery_pct_source`). Building that index now would be indexing for a consumer that
doesn't exist yet — exactly what `ai/architecture.md` §3's "don't build and version a
surface for consumers that don't exist yet" principle and `CLAUDE.md`'s AI-efficiency
"don't over-abstract" guidance both counsel against. If the verification UI (backlog entry
11) needs that worklist query, indexing `battery_pct_source` is a one-line addition to
*that* change, made against its own real access pattern.

This verdict also matches the project's read-heavy Performance-Profile from the other
direction: an index that serves no query only adds write-side maintenance cost (on every
nightly `UpsertSuperchargerSession`) for zero read benefit — the wrong trade under a profile
that already accepts writer cost only in exchange for real reader payoff.

---

## Write Path

**No change to `UpsertSuperchargerSession`'s SQL text** (`internal/telemetry/db/query.sql`)
beyond an added header comment. Restated precisely, the query's `INSERT` column list,
`VALUES`, and `ON CONFLICT (session_id) DO UPDATE SET` clause are byte-for-byte identical
before and after this change — see D3 above for why "add and then immediately exclude" was
rejected in favor of "never mention them at all." D6 extends this identically to
`start_battery_pct_est`/`end_battery_pct_est` — all five new columns are absent from this
query's text, not just the original three.

**Proposed header-comment addition** (inserted immediately above the existing
`-- name: UpsertSuperchargerSession :exec` header, `query.sql:305-311`):

```sql
-- LOAD-BEARING (R3, RM27-telemetry-add-supercharger-battery-pct): start_battery_pct,
-- end_battery_pct, battery_pct_source, start_battery_pct_est, and end_battery_pct_est
-- are DELIBERATELY ABSENT from both the INSERT column list and the ON CONFLICT DO
-- UPDATE SET clause below. The first three are a human-owned verification/override
-- channel; the last two are a frozen, write-once verification-time snapshot of the
-- estimate (design D6) -- NEVER refreshed, NEVER a cache read by internal/battery
-- (see the column comments added by migration 20260815000001). If this query touched
-- any of the five, a user's verified value or its frozen snapshot would be silently
-- overwritten by the next nightly re-upsert. A fresh INSERT leaves all five at their
-- column default (NULL); a re-upsert never assigns any of them. A future writer for
-- these columns belongs on a dedicated query on a dedicated Writer port (out of
-- scope here, backlog entry 11) -- do not "complete the pattern" by adding them here.
```

**`upsertSuperchargerSession`** (`internal/telemetry/service.go:863-`) is unchanged except
for a one-line comment (see D5) noting that `SuperchargerSession`'s five new fields are
deliberately unread at this call site.

---

## Read Path

**No new read method, no query text change.** `SuperchargerSessionsByAccount` and
`SuperchargerSessionsByVehicle` (`reader.go:104-141`) already call `SELECT *`
(`query.sql:342`, `:354`) — once the migration applies and `make sqlc` regenerates
`telemetrydb`, the generated `telemetrydb.SuperchargerSession` row struct gains
`StartBatteryPct pgtype.Int2`, `EndBatteryPct pgtype.Int2`, `BatteryPctSource pgtype.Text`,
`StartBatteryPctEst pgtype.Int2`, `EndBatteryPctEst pgtype.Int2` with zero query.sql edits.

### `rowToSuperchargerSession` (`internal/telemetry/mapping.go`)

Five new lines added to the function body, using the mapping helpers from D5:

```go
StartBatteryPct:    pgNullableInt16AsInt(r.StartBatteryPct),
EndBatteryPct:      pgNullableInt16AsInt(r.EndBatteryPct),
BatteryPctSource:   pgNullableText(r.BatteryPctSource),
StartBatteryPctEst: pgNullableInt16AsInt(r.StartBatteryPctEst),
EndBatteryPctEst:   pgNullableInt16AsInt(r.EndBatteryPctEst),
```

added to the `SuperchargerSession{...}` struct literal the function already returns
(`mapping.go:190-`), placed last to mirror the migration's physical column-append order.

### New mapping helper: `pgNullableInt16AsInt` (`internal/telemetry/mapping.go`)

```go
// pgNullableInt16AsInt converts a nullable pgtype.Int2 (SMALLINT) to *int:
// {Valid: false} -> nil, {Valid: true} -> &v. First SMALLINT column in this module;
// mirrors internal/manualcharge's identical intPtrToPgInt2/pgInt2ToIntPtr shape for
// its own start_battery_pct/end_battery_pct (module boundaries mean the four-line
// helper is duplicated here, not imported).
func pgNullableInt16AsInt(v pgtype.Int2) *int {
    if !v.Valid {
        return nil
    }
    r := int(v.Int16)
    return &r
}
```

Placed alongside the existing `pgNullableFloat64`/`pgNullableFloat4AsFloat64`/
`pgNullableInt32AsInt`/`pgNullableText` helpers (`mapping.go:15-59`), same style, same file.

## Read Paths Affected (config.yaml proposal rule)

`SuperchargerReader.SuperchargerSessionsByAccount` and
`SuperchargerReader.SuperchargerSessionsByVehicle` — every existing and future caller of
either method now receives five additional, always-present (possibly nil) fields on every
returned `SuperchargerSession`, at zero additional query cost (same row fetch, same index,
same query plan). This is the same "extend the row struct, extend the mapper, touch no
query" pattern the module's own TPMS/`_calc`-column precedents already established, applied
here to a table that had never before been extended since its creation.

---

## Test Contract (authored before implementation, binding)

Per the project's TEST-EXECUTION-POLICY, these DB-integration tests are written but not run
by the assistant; they belong in a new or existing DB-gated test file in
`internal/telemetry/` (the module's `testdb` helper — see `AGENTS.md` §Testing notes — makes
them self-skip without Docker/`DATABASE_URL`). Because no Go writer exists for the trio or
the snapshot pair in this tier, tests (b)/(b2)/(c) set them via a raw SQL
`UPDATE`/`INSERT ... ON CONFLICT` issued directly against the test pool — this is legitimate
here specifically because it is testing a **database constraint and the absence of a Go
write path**, not exercising an app-level API that doesn't exist yet.

### (a) A fresh insert seeds all five columns as NULL

- **Given** no existing row for `session_id = 900001`.
- **When** `UpsertSuperchargerSession` is called once with ordinary session params (e.g.
  `energy_kwh = 45.2`, `is_paid = false`, `total_cost = 12000`, `currency = "COP"`).
- **Then** the stored row has `start_battery_pct IS NULL`, `end_battery_pct IS NULL`,
  `battery_pct_source IS NULL`, `start_battery_pct_est IS NULL`, `end_battery_pct_est IS
  NULL`.

### (b) R3 regression — a re-upsert with changed billing data leaves an already-set trio untouched (the most important test in this change)

- **Given** `session_id = 900002` already stored via `UpsertSuperchargerSession` with
  `energy_kwh = 30.0`, `is_paid = false`, `total_cost = 9000`.
- **And** its trio has been set directly (simulating a future verification write, since no
  Go writer exists in this tier): `start_battery_pct = 18`, `end_battery_pct = 76`,
  `battery_pct_source = 'user_verified'`.
- **When** `UpsertSuperchargerSession` is called again for the same `session_id = 900002`
  with **changed** mutable fields — `is_paid = true` (billing finalized), `total_cost =
  9000` (unchanged), a `raw_data` payload with a different invoice-status field —
  simulating the nightly poller's re-fetch.
- **Then** the resulting single row has `is_paid = true` (the mutable column DID refresh)
  **and** `start_battery_pct = 18`, `end_battery_pct = 76`, `battery_pct_source =
  'user_verified'` (the trio is BYTE-FOR-BYTE unchanged — not merely "still non-NULL", but
  equal to the pre-upsert values).
- **And** `updated_at` DID advance (the row itself was touched by the upsert) — confirming
  the trio's stability is a property of the query's column list, not of some broader "row
  untouched" behavior that would also (incorrectly) freeze `updated_at` or the other
  mutable columns.

### (b2) R3 regression, extended to the frozen snapshot pair — a re-upsert leaves an already-set `_est` pair untouched (design D6)

- **Given** `session_id = 900003` already stored via `UpsertSuperchargerSession` with
  `energy_kwh = 22.5`, `is_paid = false`, `total_cost = 7000`.
- **And** its trio AND its frozen snapshot pair have been set together, in one direct SQL
  statement (simulating the future verification UI's single write): `start_battery_pct =
  20`, `end_battery_pct = 80`, `battery_pct_source = 'user_verified'`,
  `start_battery_pct_est = 22`, `end_battery_pct_est = 78` — the snapshot deliberately
  differs from the verified value (22≠20, 78≠80), matching the real "model said X, human
  said Y" use case.
- **When** `UpsertSuperchargerSession` is called again for the same `session_id = 900003`
  with **changed** mutable fields — `is_paid = true`, a `raw_data` payload with a different
  invoice-status field — simulating a nightly re-fetch, and, separately, a SECOND direct-SQL
  re-upsert simulating a later, unrelated nightly run.
- **Then** after every re-upsert, the resulting row's `is_paid` reflects the latest
  mutable-field value **and** `start_battery_pct_est = 22`, `end_battery_pct_est = 78`
  remain BYTE-FOR-BYTE unchanged from their original values — exactly like (b), applied to
  the snapshot pair. This is the direct regression test for D6's "same R3 protection,
  extended" claim.
- **And**, distinctly from (b): this test does NOT simulate a second *verification* of the
  same session (i.e. a second direct-SQL write that changes `start_battery_pct_est`/
  `end_battery_pct_est` themselves) — that scenario is explicitly out of this test's scope
  per D6's rejected "separate audit table" alternative (repeated verification overwriting
  the single snapshot pair is accepted behavior, not a bug this test guards against).

### (c) CHECK constraints reject out-of-range / unrecognized values

Each of the following, issued as a direct SQL statement against the test pool, must fail
with a Postgres constraint-violation error (`23514`, `check_violation`) and must not modify
any row:

- `UPDATE supercharger_sessions SET start_battery_pct = 101 WHERE session_id = 900001` —
  rejected (`> 100`).
- `UPDATE supercharger_sessions SET start_battery_pct = -1 WHERE session_id = 900001` —
  rejected (`< 0`).
- `UPDATE supercharger_sessions SET end_battery_pct = 101 WHERE session_id = 900001` —
  rejected, mirroring the two cases above for the second column.
- `UPDATE supercharger_sessions SET start_battery_pct_est = 101 WHERE session_id = 900001`
  — rejected (`> 100`), the identical bound applied to the snapshot column.
- `UPDATE supercharger_sessions SET start_battery_pct_est = -1 WHERE session_id = 900001` —
  rejected (`< 0`).
- `UPDATE supercharger_sessions SET end_battery_pct_est = 101 WHERE session_id = 900001` —
  rejected, mirroring the two cases above for the second snapshot column.
- `UPDATE supercharger_sessions SET battery_pct_source = 'estimated' WHERE session_id =
  900001` — rejected: `'estimated'` is deliberately NOT in the allowed value set (D2).
- `UPDATE supercharger_sessions SET battery_pct_source = 'bogus' WHERE session_id = 900001`
  — rejected: not in `('user_verified', 'polled')`.
- Sanity check (must SUCCEED, to prove the CHECK isn't overly strict): `UPDATE
  supercharger_sessions SET start_battery_pct = 0, end_battery_pct = 100,
  battery_pct_source = 'polled', start_battery_pct_est = 0, end_battery_pct_est = 100
  WHERE session_id = 900001` — `0` and `100` are both inclusive boundary values (`BETWEEN 0
  AND 100`) and must be accepted on all four `SMALLINT` columns; `'polled'` must be
  accepted even though this tier never writes it in application code.

### (d) The reader returns the trio and the frozen snapshot pair

- **Given** `session_id = 900002` stored with `start_battery_pct = 18`, `end_battery_pct =
  76`, `battery_pct_source = 'user_verified'` (as set up in (b)), and `session_id = 900003`
  stored with the additional `start_battery_pct_est = 22`, `end_battery_pct_est = 78` (as
  set up in (b2)).
- **When** `SuperchargerSessionsByAccount(ctx, accountID, 0)` and
  `SuperchargerSessionsByVehicle(ctx, accountID, teslaID, 0)` are both called for the
  relevant account/vehicle.
- **Then** both returned slices contain a `SuperchargerSession` for `session_id = 900002`
  whose `StartBatteryPct != nil && *StartBatteryPct == 18`, `EndBatteryPct != nil &&
  *EndBatteryPct == 76`, `BatteryPctSource != nil && *BatteryPctSource == "user_verified"`.
- **And** a `SuperchargerSession` for `session_id = 900003` whose `StartBatteryPctEst !=
  nil && *StartBatteryPctEst == 22`, `EndBatteryPctEst != nil && *EndBatteryPctEst == 78`.
- **And**, for a session with an untouched (NULL) trio and snapshot pair (e.g. `session_id
  = 900001` before test (c)'s sanity-check `UPDATE`), the returned `SuperchargerSession`
  has `StartBatteryPct == nil`, `EndBatteryPct == nil`, `BatteryPctSource == nil`,
  `StartBatteryPctEst == nil`, `EndBatteryPctEst == nil`.

---

## Go-Level Seam Summary (single-source, change-locality)

Exactly one new value shape — the five columns
(`start_battery_pct`/`end_battery_pct`/`battery_pct_source`/`start_battery_pct_est`/
`end_battery_pct_est`) — flows through exactly one path, and only in the read direction:
`rowToSuperchargerSession` is the **only** place any of the five columns is mapped from
`pgtype` to the domain type. There is no write-direction seam at all in this tier (D3/D5/D6)
— the entire "write" surface of this change is five `ADD COLUMN` statements with no
`DEFAULT` other than the implicit `NULL`.

## Scope Boundary

This change's entire surface is `internal/telemetry`: one migration, five struct fields,
one new mapping helper (reused across four `SMALLINT` columns), five mapped fields in one
existing function, two documentation comments (query.sql, `upsertSuperchargerSession`),
`AGENTS.md` updates, new DB-integration tests. No file outside `internal/telemetry/` is
touched. `internal/battery` does not exist as a change target here and is not read,
referenced, or scaffolded by this change — including the gateway/Writer plumbing D6
describes as the eventual legal writer for the trio and the snapshot pair; none of that is
built in this tier (R7).

## Risks / Trade-offs

- **The trio's and the snapshot pair's write paths genuinely do not exist yet.** This is
  intentional (R7), not an oversight, but it does mean tests (b)/(b2)/(c)/(d) above set up
  their fixtures via direct SQL rather than through a Go API — acceptable because those
  tests exist specifically to prove a DB-level guarantee (the CHECK constraints, and the
  absence of any query path that could touch any of the five columns), not to exercise
  application code that doesn't exist.
- **`battery_pct_source`'s allowed-value set is a `CHECK`, not an `ENUM`.** Accepted
  trade-off per D2 — cheaper to extend, consistent with this project's own precedent,
  costs nothing today since nothing queries on the column's distinct values in a way an
  `ENUM`'s type safety would meaningfully protect (the only current reader is
  `rowToSuperchargerSession`, mapping to a plain `*string`).
- **No cross-column consistency `CHECK`** (D2's rejected alternative) — accepted as
  premature; revisit when the verification UI's actual input shape is designed.
- **The frozen snapshot pair can hold only the most recent verification** (D6's "separate
  audit table" rejected alternative) — a second verification of an already-verified session
  overwrites `start_battery_pct_est`/`end_battery_pct_est`, losing the prior snapshot.
  Accepted for this tier as disproportionate to build a second table for; logged as the
  explicit trigger to revisit if repeated re-verification of the same session turns out to
  be common.
- **No `verified_at` timestamp.** Offered to the user and declined for now (D6) — can be
  added later as a sixth nullable column with no migration conflict.

## Migration Plan (implementation order for the worker(s))

1. `internal/telemetry/db/migrations/20260815000001_add_supercharger_battery_pct.sql` — the
   DDL above (no dependencies).
2. `internal/telemetry/telemetry.go` — five new `SuperchargerSession` fields (no
   dependencies; pure struct addition).
3. `internal/telemetry/db/query.sql` — header-comment-only addition to
   `UpsertSuperchargerSession` (depends on step 1, so the comment can cite the migration
   filename correctly). Leader runs `make sqlc` after this step to regenerate `telemetrydb`
   with the five new columns on `SuperchargerSession`/`SuperchargerSessionsByAccount`/
   `SuperchargerSessionsByVehicle`'s generated row types (`SELECT *` picks them up with no
   further query.sql edit).
4. `internal/telemetry/mapping.go` — new `pgNullableInt16AsInt` helper; five new mapped
   fields in `rowToSuperchargerSession` (depends on steps 2, 3).
5. `internal/telemetry/service.go` — one-line documentation comment near
   `upsertSuperchargerSession` (depends on step 2; no functional change).
6. New DB-integration tests: the five test-contract scenarios (a), (b), (b2), (c), (d)
   above (depends on steps 1, 3, 4).
7. `internal/telemetry/AGENTS.md` — "Data ownership" / "DTO / units conventions" additions
   documenting the trio, the frozen snapshot pair, their NULL conventions, the R3
   write-exclusion, and the explicit "never a cache, never nightly-refreshed" warning for
   `start_battery_pct_est`/`end_battery_pct_est` (depends on step 1; can run any time after
   the schema is finalized).
8. Verification: `go build ./...`, `go vet ./...`, `gofmt -l`,
   `openspec validate RM27-telemetry-add-supercharger-battery-pct --strict`.
