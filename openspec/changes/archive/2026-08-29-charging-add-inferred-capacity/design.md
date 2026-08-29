# Design — charging-add-inferred-capacity

> **Numbering note.** This document's decisions are **D1, D2, …**, scoped to this change
> only — distinct from RM29 tier 6's archived decisions (cited as **RM29 D1**, …), RM30's
> (**RM30 D1**, …) and RM31's (**RM31 D1**, …). **D2, D3, D5 and D6 transcribe binding
> instructions from the leader's dispatch** (which transcribes the grounding interview
> settled with the owner); each says so in its heading. **D7–D10 are decisions this
> artifacts pass had to make** to turn those into a buildable change.
>
> **D1 and D4 each revise what the dispatch proposed, and both revisions were reviewed and
> accepted by the leader before this document was finalized.** D1's dispatched form posed a
> false binary (the ticket's `_calc` vs the `_kwh` non-negotiable) that an unwritten project
> convention dissolves — the two compose. D4's dispatched precision rejects legal rows,
> reproduced as an actual PostgreSQL error. Each says so in its own heading and carries its
> evidence.

> **This document is a design gate.** `CLAUDE.md` §Pipeline config declares
> `Design-Gates: database`, and this change adds two database columns. The owner reads
> this and says go / no-go **before** any implementation is dispatched.
>
> **One item still wants an explicit yes from you beyond the schema itself: the result
> type** (**D4**). It specifies unconstrained `NUMERIC` rather than the `NUMERIC(8,3)` the
> interview proposed, because `NUMERIC(8,3)` **rejects legal rows** — reproduced, not
> argued; the PostgreSQL error is quoted verbatim.
>
> The **column name** is no longer an open question: it was, and the leader has since
> settled it on the `vehicle_metrics` precedent (**D1**). `inferred_capacity_kwh_calc` keeps
> the ticket's own `_calc` and adds the `_kwh` the project mandates. proposal.md's box at
> the top still carries the comparison, and the override is still one word — it is simply no
> longer a decision this design is waiting on.

---

## Context

Six facts about the existing code and schema shape everything below. All were verified
against the live repository and a live PostgreSQL 16, not recalled.

1. **Both tables already carry all three inputs, and both already carry them nullably —
   except one.** `manual_charge_entries` has `energy_added_kwh NUMERIC(6,2) NOT NULL
   CHECK (> 0)` plus `start_battery_pct` / `end_battery_pct SMALLINT CHECK (BETWEEN 0 AND
   100)`, both **nullable**. `charge_sessions` has `energy_kwh DOUBLE PRECISION`
   (**nullable** — "NULL when the session had no kWh fee") plus the same two nullable
   `SMALLINT` percentage columns. So the formula's inputs are already there; only the
   output is missing. The one asymmetry — energy is `NOT NULL` on one table and nullable on
   the other — is what forces two expressions instead of one (**D4**).

2. **This module has *three* independent write paths into these two tables, and one of
   them exists specifically to change the percentages the formula divides by.**
   `Writer.Create` / `Writer.Update` (`CreateEntry` / `UpdateEntry`),
   `SessionWriter.MirrorSessions` (`MirrorChargeSession`, whose `ON CONFLICT DO UPDATE SET`
   refreshes `energy_kwh` every night as Tesla's fees settle), and
   `SessionVerifier.VerifySession` (`VerifyChargeSession`, RM31 — a *human* correcting
   `start_battery_pct` / `end_battery_pct`). A Go-side computation would have to be
   correct in all three, forever, including in the one whose entire purpose is to move a
   divisor. This is the single strongest argument for **D2**.

3. **Every read on both tables is `SELECT *`; every write names its columns explicitly.**
   All seven read queries in `internal/charging/db/query.sql` are `SELECT * FROM …`, and
   `CreateEntry` / `UpdateEntry` / `VerifyChargeSession` end in `RETURNING *`. Meanwhile
   every `INSERT` / `UPDATE` names its columns one by one. That asymmetry is what makes
   **D2** cost zero query edits — proven, not assumed (**D8**).

4. **The project already stores derived values in columns, and already has a suffix for
   them.** `internal/analytics/vehicle_metrics` carries five `_calc` columns
   (`distance_traveled_km_calc`, `battery_used_pct_calc`, `km_per_pct_calc`,
   `estimated_range_km_calc`, `days_spanned_calc`) — the precomputed daily read model that
   replaced a live on-every-read derivation, per RM29 roadmap D1. The `_calc` suffix comes
   **after** the unit suffix. This is the precedent behind proposal.md's naming box.

5. **…but *this* module's own precedent for derived values is the opposite: read-time
   methods.** `Entry.CostPerKWh()`, `Entry.BatteryDelta()` and `Entry.SessionDuration()`
   are computed on read and never persisted — and `openspec/specs/manual-charge-log/spec.md`
   §"Derived read-time values" is a **standing requirement** saying so. This change stores a
   derived value in a table where three sibling derived values are deliberately not stored.
   That tension is real and is addressed head-on in **D2**, not glossed.

6. **The `charge_sessions` mirror is stricter than a normal table in one relevant way.**
   `MirrorSessions` upserts every supplied session **in one transaction**, and RM29's
   contract is that "a single mis-scoped entry rejects the WHOLE call and writes nothing."
   So any per-row database error on that path — including a numeric overflow — aborts the
   entire night's mirror for that account. This is what makes **D4**'s overflow finding a
   correctness issue rather than an aesthetic one.

## Goals / Non-Goals

**Goals**

- One new column per table, holding the ticket's formula's result, **guaranteed current on
  every insert and every update by the database engine** rather than by Go-side discipline
  (**D2**).
- Existing rows populated as a **side effect of the migration itself** — no separate
  backfill `UPDATE`, no ordering hazard, nothing for a future writer to forget (**D2**,
  **D9**).
- `NULL`, never an error and never a nonsense value, whenever the inputs cannot support the
  formula (**D3**).
- The value reachable through the module's existing public ports without adding a single
  query or port method (**D7**, **D8**).
- Expected values for every integration test fixed **here**, before implementation exists
  (§Test Contract), per `ai/go-conventions.md` §Testing authoring order.

**Non-Goals** (each is a deliberate exclusion — see **D5**)

- No per-vehicle aggregate table, rollup, summary column, materialized view, or
  `internal/analytics` metric.
- No `internal/gateway` handler, route, template, i18n key, or UI of any kind.
- No new port method, no new query, no change to any existing interface signature.
- No change to `internal/telemetry.supercharger_sessions`, which holds the same three
  inputs upstream.
- No index (**D6**), no minimum-delta quality filter, no convergence of the two tables'
  vocabularies (backlog item 12).

---

## Decisions

### D1 — Column name `inferred_capacity_kwh_calc` (revised — dispatch's D1 corrected by an unwritten project convention)

**Decision: `inferred_capacity_kwh_calc`** — the ticket's own `_calc`, with the mandatory
`_kwh` unit segment inserted before it.

**The naming convention this follows, stated here because it exists nowhere else in
writing.** This project names a **stored, derived** column
`<what>_<unit>_calc` — unit suffix first, `_calc` last.

> `internal/analytics/vehicle_metrics` (`internal/analytics/db/migrations/20260821000001_add_vehicle_metrics.sql:67-71`)
> carries five such columns: `distance_traveled_km_calc`, `battery_used_pct_calc`,
> `km_per_pct_calc`, `estimated_range_km_calc`, `days_spanned_calc`. Each is a value
> computed rather than observed, persisted rather than derived on read, and each places
> `_calc` **after** its unit suffix.

`ai/go-conventions.md` documents the unit-suffix half of that pattern and **says nothing
about `_calc`** — it is an established *code* convention with no written home. Anyone
naming a derived column will therefore either re-derive it from `vehicle_metrics` or miss it
entirely. **Task 3.3 writes it down** in `internal/charging/AGENTS.md` §Units convention, so
the next agent reads the rule instead of rediscovering it.

**Why this name and not the two alternatives.**

- **Not the ticket's literal `inferred_capacity_calc`.** `CLAUDE.md` §Non-negotiables and
  `ai/go-conventions.md` §"Read optimization" require every persisted unit-bearing column to
  end in its display-unit suffix, whose whole point is that "the unit is readable from the
  column name alone, no migration or comment required." The stored value is kilowatt-hours
  (kWh ÷ a dimensionless fraction = kWh), so this would be the repository's first
  unit-bearing column to break that rule. It breaks it purely by **omission** — the `_calc`
  half is already correct.
- **Not `inferred_capacity_kwh`** (this change's own first draft, and what the dispatched D1
  specified). It is legal but discards the `_calc` signal, leaving this the only stored
  derived column in the repository that does not announce itself as one — and it drifts
  *further* from the ticket's wording than the chosen name does, for no gain. The dispatched
  D1 framed the choice as a binary between the ticket's `_calc` and the `_kwh`
  non-negotiable; the `vehicle_metrics` precedent shows that was a false binary, since the
  two compose. The leader accepted this correction.
- **Not** `inferred_capacity` (no unit — same violation as the ticket's), nor
  `usable_capacity_kwh_calc` / `pack_capacity_kwh_calc`: both assert more than the value
  knows. This is one record's *inference*, not a measurement, and a small-delta record's
  figure is noisy (§Risks item 1). "Inferred" is the honest word and the ticket's own.

**Go field name: `InferredCapacityKWhCalc`** on `charging.Entry` and `charging.Session`
(**D7**), matching this module's existing `EnergyAddedKWh` / `EnergyKWh` capitalization of
the unit. sqlc generates `InferredCapacityKwhCalc` on the `chargingdb` structs — confirmed by
probe against the project's own sqlc v1.31.1, not assumed — exactly as `EnergyAddedKWh` and
`EnergyAddedKwh` already differ today.

**Override path preserved.** proposal.md's box carries the three-row comparison for the
owner. Switching to either alternative at the gate is a pure rename — same DDL, same
expression, same type, same guard, same Test Contract — across the migration, both column
comments, `charging.go`, `service.go`, `session_reader.go`, both test files and `AGENTS.md`.

### D2 — The value is a PostgreSQL `GENERATED ALWAYS AS (…) STORED` column, not Go (binding — dispatch)

**Decision.** Both columns are generated columns. Nothing in Go ever computes, writes, or
is permitted to write the value.

**Why this is the right site, restated against this repository's actual shape.** The ticket
requires correctness "every time a record is inserted or updated into the charging module
tables." Context 2 counts the write paths that must honor that: **three**, and growing.
`Writer.Create` and `Writer.Update` set both percentages and the energy.
`MirrorChargeSession`'s `ON CONFLICT DO UPDATE SET` refreshes `energy_kwh` every single
night as Tesla's fees settle. And `VerifyChargeSession` exists *specifically* so a human can
change `start_battery_pct` and `end_battery_pct` — the divisor. A Go-side implementation
would need a recomputation in all three, plus in every write path added later, and RM31's
`VerifySession` is a live example of a write path added later by a different change, months
after the table shipped.

That is a discipline **no reviewer can verify from a diff**: to check it you must enumerate
every write path in the module and confirm each one recomputes. A generated column makes it
an engine property — checkable in one place, `\d manual_charge_entries`, and impossible to
regress. The AI-efficiency argument in `CLAUDE.md` §Non-negotiables names this exact trade:
*"deterministic signals over human round-trips… codegen that fail fast and cheap."* Here the
signal is stronger than a lint — the database physically refuses the wrong thing
(see §"Write-protection, reproduced").

**It also discharges the acceptance criterion's second half for free.**
`ALTER TABLE … ADD COLUMN … GENERATED ALWAYS AS (…) STORED` computes the value for **every
pre-existing row** as part of the same statement (reproduced below, §"Backfill,
reproduced"). No separate backfill `UPDATE`, no question about whether the backfill ran
before or after some other migration, nothing to forget.

**And it is the right shape for this project's `Performance-Profile`:** *"writes are mostly
done by pollers at midnight, so denormalizing, indexing aggressively, and precomputing for
reads is acceptable."* A `STORED` generated column is precisely precomputation-on-write for
the benefit of reads.

**The honest tension (Context 5), addressed.** This module's three existing derived values
— `CostPerKWh`, `BatteryDelta`, `SessionDuration` — are read-time methods, and
`manual-charge-log`'s spec has a standing requirement saying they are computed and *never
persisted*. Why is this one different?

- **The ticket says so, in the acceptance criterion:** *"The new column
  `inferred_capacity_calc` is **created** and calculated for existing rows."* The
  deliverable is a column, not a method. A read-time method would not satisfy the
  criterion as written.
- **The project already draws this line the same way.** `vehicle_metrics` (Context 4) is
  the precedent for *promoting* a derived value from live computation to a stored column,
  and RM29 roadmap D1's stated reason is the read-heavy profile — the identical reason
  here.
- **The three read-time methods stay read-time.** This change modifies none of them and
  modifies that spec requirement not at all. The specs gain a *new* requirement alongside
  it; §"Spec deltas" and proposal.md §Impact both say so explicitly, so a future reader does
  not conclude the module changed its mind about `CostPerKWh`.

**Rejected alternatives.**

| Alternative | Why rejected |
|---|---|
| **Go-side computation on the write path** | Must be repeated in three existing write paths and every future one; unverifiable from a diff; needs a separate backfill `UPDATE` in the migration; and `MirrorChargeSession`'s `ON CONFLICT DO UPDATE SET` would need it too, where forgetting it silently leaves a stale value behind a *correct-looking* nightly refresh. This is the alternative the dispatch explicitly ruled against, and Context 2 is why. |
| **A `BEFORE INSERT OR UPDATE` trigger** | Achieves the engine guarantee, but: it is a second schema object with hidden control flow (a reader of the table definition sees nothing); it needs its own separate backfill `UPDATE` for existing rows, re-introducing exactly what the generated column removes; it can be turned off (`ALTER TABLE … DISABLE TRIGGER`) whereas a generated column cannot be bypassed at all; and it costs a per-row PL/pgSQL invocation instead of an inlined expression. Strictly worse on every axis that matters here. |
| **A `VIEW` or computed-on-read expression** | Fails the acceptance criterion ("the new **column** is created … in charge_sessions and manual_charge_entries"); adds a new schema object that other modules would need a port for; and pays the computation on the **read** path, which is precisely the direction the `Performance-Profile` forbids. |
| **A read-time Go method (`Entry.InferredCapacityKWhCalc()`), mirroring `CostPerKWh`** | The closest thing to this module's existing convention, and the reason the tension above is stated rather than hidden. Rejected for the three reasons in that paragraph — chiefly that the ticket's deliverable is a column, and that `Session` would need the method duplicated since `Entry` and `Session` are deliberately distinct types (RM30 D4). |
| **A `GENERATED … VIRTUAL` column** | Not available: PostgreSQL 16 supports `STORED` only. (PostgreSQL 18 added virtual generated columns; this project targets `postgres:16-alpine` per `internal/charging/testdb_test.go`.) Moot, and `STORED` is what the read-heavy profile wants regardless. |

### D3 — Compute only when all inputs are present **and** the delta is strictly positive; otherwise `NULL` (binding — dispatch)

**Decision.** The value is computed **iff** energy is non-`NULL` (a live check only on
`charge_sessions`, where the column is nullable), `start_battery_pct` is non-`NULL`,
`end_battery_pct` is non-`NULL`, **and** `end_battery_pct > start_battery_pct`. In every
other case the column is `NULL`.

**Why each clause.**

- **All three inputs present** — the ticket says so verbatim: *"only the field will be
  calculated when the record contains all the fields: energy added, end battery % and start
  battery %. Otherwise, ignore the calculation and the field will be NULL."*
- **`end > start`, not merely `end <> start`** — this clause is *not* in the ticket, and is
  the one place this design adds a rule the ticket does not state. Two reasons, both
  physical:
  - **`end = start` is a division by zero.** In PostgreSQL, numeric division by zero raises
    `division_by_zero` (SQLSTATE 22012) — it does not produce `NULL` or infinity. Inside a
    generated column that error surfaces at `INSERT`/`UPDATE` time, so without this guard a
    perfectly ordinary row ("I plugged in at 74% and unplugged at 74%") would be **rejected
    by the database**. On `charge_sessions` that rejection aborts the whole nightly mirror
    transaction (Context 6). This clause is not defensive tidiness; it is what keeps a
    legal row insertable.
  - **`end < start` yields a negative "capacity."** Battery capacity is a non-negative
    physical quantity; a negative one is not a smaller value, it is a meaningless one.
    Storing it would put a number that cannot be true into a column downstream consumers
    will average. A decreasing SoC across a charge record means the record's percentages
    are wrong or reversed — the honest output is "unknown," which is `NULL`.
- **`NULL`, not an error and not a sentinel.** `NULL` is already this schema's established
  "nothing recorded" for every optional column on both tables, and it is what the ticket
  asks for. No `-1`, no `0`, no `CHECK` that rejects the row.

**Consequence to state plainly:** a row with an equal or decreasing SoC is stored
successfully with a `NULL` capacity. It is not flagged, not rejected, and not logged. If
the owner wants such rows surfaced as suspect, that is a separate feature (§Risks, item 4).

### D4 — Per-table expressions; result type **unconstrained `NUMERIC`** with `ROUND(…, 3)` (binding shape — dispatch; **precision departs, with evidence**)

**Decision, part 1 — two expressions, not one (binding, as dispatched).** The two tables'
source energy columns differ, so the expression is written per table with explicit casts
rather than copy-pasted. The two expressions differ in **exactly two ways**, both forced by
the source column, and in no other way:

| | `manual_charge_entries` | `charge_sessions` |
|---|---|---|
| Source energy column | `energy_added_kwh NUMERIC(6,2) NOT NULL` | `energy_kwh DOUBLE PRECISION` **nullable** |
| `energy … IS NOT NULL` guard | **absent** — the column is `NOT NULL`; the check would be dead code that falsely implies nullability | **present** — required by D3 |
| Cast on energy | **absent** — already `NUMERIC`; stays in `NUMERIC` end-to-end, no float round-trip | **`::NUMERIC`** — so both tables expose the identical type to readers, and `NULL` energy still yields `NULL` |

Everything else — the percentage guards, the `(end - start)::NUMERIC / 100` divisor, the
`ROUND(…, 3)`, the `ELSE NULL` — is character-identical between the two.

The `float8 → numeric` cast on `charge_sessions` is worth one sentence because a test
expectation depends on it: PostgreSQL converts a `double precision` to `numeric` via its
shortest round-trip decimal representation, so the stored float `52.273` becomes the numeric
`52.273` exactly, **not** `52.27299999999999…`. That is why T13's expected value is a clean
`73.624`. Reproduced, not assumed.

**Decision, part 2 — the result type. This departs from the dispatch's proposal, and here
is why.** The dispatch proposed `NUMERIC(8,3)`, inviting a different choice with a stated
reason. **`NUMERIC(8,3)` is not safe: it rejects legal rows.**

Reproduced on PostgreSQL 16 with the exact DDL, inserting a row that the *existing*
`manual_charge_entries` schema permits today (`energy_added_kwh` up to `9999.99` by
`NUMERIC(6,2)`, minimum positive delta 1%):

```
INSERT INTO mce VALUES (9999.99, 0, 1);
ERROR:  numeric field overflow
DETAIL:  A field with precision 8, scale 3 must round to an absolute value less than 10^5.
```

`NUMERIC(8,3)` caps the value at `99999.999`. The maximum a legal `manual_charge_entries`
row can produce is `9999.99 / 0.01 = 999999.000` — one order of magnitude over. So adding
the column with that precision would convert a row that inserts fine today into a hard
`INSERT` failure. That is a regression introduced by a feature that is supposed to be
purely additive.

On `charge_sessions` it is worse, because there is **no upper bound at all** to reason
about: `energy_kwh` is `DOUBLE PRECISION` with no `CHECK`, mirrored from
`telemetry.supercharger_sessions`, which mirrors data Tesla controls. Any fixed precision
can overflow on data this project does not own — and by Context 6, one overflowing row
aborts the whole night's `MirrorSessions` transaction for that account. A schema choice
that lets an upstream vendor's outlier take down the nightly sync is not a trade worth
making for a self-documenting type annotation.

**Therefore: unconstrained `NUMERIC`, with `ROUND(…, 3)` inside the expression.** This gets
both properties independently:

- **`ROUND(…, 3)` pins the *scale*** — `round(numeric, integer)` returns a numeric of
  exactly that scale, so every stored value has three decimals (`70.400`, `66.629`,
  `0.010`), matching the ticket's worked examples. Verified: the probe's column reports
  `format_type` `numeric` and its values print at scale 3.
- **Unconstrained precision pins nothing else** — so no legal input can overflow, on either
  table, ever. Proven by inserting `energy_kwh = 1e9, 0 → 1%` and getting
  `100000000000.000` rather than an error.

It also keeps D4's own consistency requirement: **the same declaration on both tables**, so
both map to `pgtype.Numeric` in sqlc and to `*float64` in the domain (**D7**) — readers
cannot tell the two tables apart by this column's type.

`round(numeric, integer)` rounds **half away from zero** in PostgreSQL (so `x.xxx5` →
`x.xxx6`, not banker's rounding). No value in the Test Contract sits on a half; the rule is
stated so a future test author does not have to rediscover it.

**Alternative left on the table for the owner:** `NUMERIC(9,3)` is provably safe for
`manual_charge_entries` alone (max `999999.000` = exactly 9 digits, 3 decimal). It is *not*
safe for `charge_sessions`, so choosing it means either accepting an unbounded-input
overflow risk on the nightly mirror, or declaring the two columns with different precisions
and giving up D4's same-type-to-readers property. Both are worse; unconstrained `NUMERIC`
is the recommendation. **The owner can overrule at the gate.**

### D5 — Scope is the per-row column on the two named tables, and nothing else (binding — dispatch)

The ticket's phrase *"This data will be saved per vehicle"* is satisfied by the per-row
column, which rolls up per vehicle on read: every row on both tables already carries
`account_id`, `vin` and `tesla_id`, so a per-vehicle view of inferred capacity is a
`WHERE` clause away for any future consumer.

Explicitly **not built**: a per-vehicle aggregate table, a rollup, a summary column, a
materialized view, an `internal/analytics` read model or metric, an `internal/gateway`
handler / route / template / i18n key / UI, any new port method, any new query, and any
change to `internal/telemetry.supercharger_sessions`. The acceptance criterion names
exactly one deliverable — the column — and this change delivers exactly that.

The follow-on work worth doing is enumerated in proposal.md §"Follow-on work (noted, not
built)" for the backlog. It is deliberately not built here.

### D6 — Index plan: **no index on either column**, and here is the reason (binding requirement to state — dispatch)

**Decision: no index is created on `manual_charge_entries.inferred_capacity_kwh_calc` or on
`charge_sessions.inferred_capacity_kwh_calc`.** No existing index is altered either.

**Justified against the project's declared read patterns**, per `openspec/config.yaml`
§design ("an index plan justified against the project's read patterns"):

- **No read predicates on this column.** The ticket declares no query that filters, joins,
  sorts, or groups by inferred capacity. Every existing read on both tables
  (proposal.md §"Read paths affected") selects the whole row and filters on
  `account_id` / `tesla_id` / a date column — all already served by
  `idx_manual_charge_entries_vehicle_time`, `idx_manual_charge_entries_account_time` and
  `idx_charge_sessions_vehicle_stop`. The new column is **projected**, never
  **predicated**. An index accelerates predicates; a projected-only column gains nothing
  from one.
- **An index here would be pure cost.** Every `INSERT` and every `UPDATE` that changes any
  of the three inputs would maintain an extra B-tree — on the nightly mirror path, for
  zero read benefit. The `Performance-Profile` permits paying write cost *for reads*; there
  is no read here to pay for.
- **The project's own leading-column convention would forbid this shape anyway.**
  `ai/go-conventions.md`: *"`account_id` is the leading index column on every multi-tenant
  table."* An index on a bare derived column violates that; a compliant
  `(account_id, tesla_id, inferred_capacity_kwh_calc)` index is only worth building once a real
  query exists to be shaped around — and none does.
- **Precedent.** `internal/analytics/vehicle_metrics` indexes none of its five `_calc`
  columns, for the same reason: they are read-model *payload*, selected with the row, never
  used as a predicate.

**When this should be revisited:** the first consumer that filters (`WHERE
inferred_capacity_kwh_calc IS NOT NULL`) or sorts by this column. A partial index
`… (account_id, tesla_id) WHERE inferred_capacity_kwh_calc IS NOT NULL` would then be the
natural shape, since the `NULL` rows are exactly the ones such a consumer skips. That is
follow-on work (proposal.md §"Follow-on work", item 1), not this change.

### D7 — The value is exposed on `charging.Entry` and `charging.Session` as a read-only `*float64`

**Decision.** `Entry` and `Session` each gain `InferredCapacityKWhCalc *float64`. No port
signature changes; no new method; no new query.

**Why this is part of the deliverable, not scope creep.** `ai/architecture.md` §2 forbids
any other module from reading `charge_sessions` or `manual_charge_entries` directly — access
is *only* through this module's public ports, and those ports return `Entry` and `Session`.
A column with no field on those types is, by the module boundary, **permanently invisible to
the entire rest of the platform.** D5's own justification for calling the per-row column
sufficient ("it rolls up per vehicle on read") presupposes something can read it. The field
is what makes D5 true.

It costs nothing beyond the field itself: the sqlc read models already gain the value
automatically (**D8**), so this is one struct field plus one mapping line per type.

**`*float64`, not `float64`:** `NULL` is a first-class, expected outcome (**D3**), and this
module's established convention for a nullable column is a pointer — `Entry.StartBatteryPct
*int`, `Session.EnergyKWh *float64`, `Session.TotalCost *float64`. `nil` means "the inputs
did not support the formula," matching `NULL` exactly. A `float64` zero value would be
indistinguishable from a real (if absurd) computed `0`.

**Read-only, and this must be documented on the field.** `Writer.Create` and
`Writer.Update` take an `Entry`. A caller *can* set `InferredCapacityKWhCalc` on the struct it
passes in, and the value will be **silently ignored** — `CreateEntry` / `UpdateEntry` name
their columns explicitly and do not include it. This is not a new footgun shape: `ID`,
`CreatedAt` and `UpdatedAt` are already server-assigned fields on `Entry` with exactly the
same "ignored on write, populated on the returned value" contract. The field's doc comment
must say so in those words, and must say that the database rejects the write outright if
anything ever tries (§"Write-protection, reproduced").

**Naming:** `InferredCapacityKWhCalc`, matching this module's existing `EnergyAddedKWh` /
`EnergyKWh` capitalization of the unit (`KWh`, not `Kwh`) on domain types — the generated
`chargingdb` structs use sqlc's `InferredCapacityKwhCalc`, and the two differ exactly as
`EnergyAddedKwh` / `EnergyAddedKWh` already do today. (If the owner takes proposal.md's
`inferred_capacity_kwh_calc`, so a `_calc` segment is present in both, spelled to each
side's convention.)

### D8 — Zero hand-written query changes; `make sqlc` picks the column up automatically — **probed, not assumed**

**Decision.** `internal/charging/db/query.sql` is **not edited by this change.** The only
step is re-running `make sqlc` after the migration lands.

This is the direct consequence the dispatch asked to have stated, and it was verified by
generating with the project's own `sqlc v1.31.1` against a replica schema carrying the
proposed generated columns. Results:

- **`SELECT *` and `RETURNING *` expand to include it.** sqlc rewrote
  `SELECT * FROM manual_charge_entries …` to
  `SELECT id, account_id, energy_added_kwh, start_battery_pct, end_battery_pct, inferred_capacity_kwh_calc FROM …`
  and added `&i.InferredCapacityKwhCalc` to the generated `row.Scan(…)` list. So **all seven
  read queries and all three `RETURNING *` writes gain the column with no edit.**
- **The generated models gain one field**, typed `pgtype.Numeric` (the sqlc default for
  `NUMERIC`; the module's existing `EnergyAddedKwh` / `Price` are the same type, so the
  mapping path is already established):
  ```go
  type ManualChargeEntry struct { …; InferredCapacityKwhCalc pgtype.Numeric }
  type ChargeSession     struct { …; InferredCapacityKwhCalc pgtype.Numeric }
  ```
- **No `*Params` struct gains the field.** `CreateEntryParams` came back with exactly its
  four pre-existing fields. sqlc derives params from the query's explicit column list, and
  every write in this module has one — so there is structurally no way to bind the column,
  in `CreateEntry`, `UpdateEntry`, `MirrorChargeSession` or `VerifyChargeSession`.
- **sqlc parses `GENERATED ALWAYS AS (…) STORED` cleanly.** No error, no warning. (This was
  the one real unknown; `sqlc.yaml` needs no change either.)

**Implementation consequence:** task 1.2's acceptance is a *diff review* — the two model
structs each gain exactly one field, `query.sql.go`'s `SELECT`/`RETURNING` column lists and
`Scan` lists each gain exactly one entry, and **no `*Params` struct changes at all**.
Anything else in that diff means something was hand-edited that should not have been.

**Go mapping.** `rowToEntry` (`service.go`) and `rowToSession` (`session_reader.go`) each
gain one line calling a **new** helper — `pgtype.Numeric` is the one nullable pgtype this
module has no `…ToPtr` helper for yet:

```go
// pgNumericToFloat64Ptr maps a nullable pgtype.Numeric to *float64.
// nil (SQL NULL) → nil; otherwise the numeric's float64 value.
// Placed in service.go beside the other pg*To*Ptr helpers; session_reader.go's
// rowToSession calls it too (same package).
func pgNumericToFloat64Ptr(v pgtype.Numeric) (*float64, error) { … }
```

It must go through `Float64Value()` and check `.Valid` — the same path `rowToEntry` already
uses for `EnergyAddedKwh` / `Price`, but nil-guarded, since those two columns are `NOT NULL`
and this one is not. `rowToEntry` already returns `(Entry, error)` so it absorbs the error
naturally; `rowToSession` currently returns a bare `Session`, so **either** it gains an
`error` return (touching `session_reader.go`, `session_verifier.go` and every caller) **or**
the helper is written non-erroring, treating an unconvertible numeric as `nil`. Choose the
**non-erroring** form: a `Float64Value()` failure on a numeric this schema can produce is
not reachable (the value is finite by construction — it is a quotient of finite numerics),
and widening `rowToSession`'s signature would ripple through three files for an unreachable
branch. Document that choice on the helper.

### D9 — One migration file, both tables; the `ALTER` *is* the backfill

**Decision.** A single new goose migration,
`internal/charging/db/migrations/20260829000001_add_inferred_capacity.sql`, adds both
columns. Not two files.

**Why one file:** the two columns are one feature with one shared guard contract (**D3**);
splitting them would let a database exist in a state where one table has the column and the
other does not, for no benefit. Both tables are owned by this module, so there is no
cross-module ordering question at all — unlike RM29 tier 6's backfill, this migration reads
nothing outside `internal/charging` and needs no `to_regclass` guard, no sentinel comments,
and no `DO $$ … $$` block.

**Timestamp `20260829000001`** — after this module's current latest (`20260823000001`), per
the module's existing numbering. Versions are unique within the module, which is all goose
requires here (`ai/go-conventions.md` §Testing: versions are not unique across the repo, and
each module's directory is applied with its own provider).

**The backfill is the `ALTER`, reproduced.** PostgreSQL computes a `STORED` generated
column's value for every existing row as part of `ADD COLUMN` (it is a table-rewriting
form). Verified on PostgreSQL 16 by seeding rows **first** and then running the exact
`ALTER`:

```
 energy_added_kwh | s  | e  | inferred_capacity_kwh_calc
------------------+----+----+-----------------------
             7.04 | 64 | 74 |                70.400   ← pre-existing row, populated by the ALTER
            41.31 | 18 | 80 |                66.629   ← pre-existing row, populated by the ALTER
          9999.99 |  0 |  1 |            999999.000   ← the row NUMERIC(8,3) would have rejected
             5.00 |    | 80 |                         ← NULL start  → NULL
             5.00 | 80 | 80 |                         ← zero delta  → NULL
             5.00 | 80 | 20 |                         ← negative Δ  → NULL
```

So the acceptance criterion's *"and calculated for existing rows"* is satisfied by the
migration statement itself. **No separate backfill `UPDATE` is written, and none should
be.**

**Operational note for the migration's own comment:** `ADD COLUMN … GENERATED … STORED`
takes an `ACCESS EXCLUSIVE` lock and **rewrites the table**. Both tables are small on this
platform (one household's charge history), so the rewrite is effectively instantaneous;
this is recorded so a future reader does not mistake it for a metadata-only add.

**`-- +goose Down`** drops both columns (`DROP COLUMN IF EXISTS`, in the reverse order of
the `Up` for symmetry). It is **non-destructive by construction**: the column stores
nothing that is not fully recomputable from `energy*`, `start_battery_pct` and
`end_battery_pct`, none of which this migration touches. Re-running `Up` reproduces every
value exactly. This is a materially safer `Down` than RM29 tier 6's, and the migration's
comment should say why in one line.

### D10 — Backfill-of-existing-rows is verified by the **owner on the real database**, not by an integration test

**Decision.** No integration test asserts "rows that existed before the migration got a
value." Instead, tasks.md carries an explicit owner-run verification step against the real
database after `make migrate-up`.

**Why — stated so a reviewer does not read this as a gap.** The package's test database
(`internal/charging/testdb_test.go`) provisions a **fresh** Postgres and applies every
migration *before any row exists*, so in the test environment there is definitionally
nothing to backfill. Simulating one would mean either mutating the shared schema mid-suite
(dropping and re-adding the column on a pool shared across the whole package — actively
dangerous) or replaying the `ALTER` against a probe table with the real table's name
substituted in, which tests a string rewrite rather than the shipped statement.

More to the point: **the behaviour under test is PostgreSQL's, not ours.** Our contribution
is the expression, and the Test Contract below covers it exhaustively — including T11 and
T20/T21, which exercise the *same* recomputation mechanism on `UPDATE`. What genuinely needs
confirming is that the owner's **production rows** end up populated, and the only place that
can be confirmed is the owner's database. That is a real check, not a proxy for one, and
tasks.md §"Owner verification" gives the exact `psql` to paste.

RM29 tier 6's sentinel-extraction test is the right precedent for a hand-written backfill
`INSERT … SELECT` that could drift from what ships. There is no such statement here (**D9**)
— that is the whole point of the generated column — so there is nothing for it to guard.

---

## Database Changes

### The migration (`internal/charging/db/migrations/20260829000001_add_inferred_capacity.sql`)

This is the exact DDL. It was executed verbatim against PostgreSQL 16; every value in the
Test Contract below is its observed output, not a hand calculation.

```sql
-- +goose Up
-- inferred_capacity_kwh_calc: the pack capacity implied by one charge record, in kWh
--     inferred_capacity_kwh_calc = energy_added / ((end_battery_pct - start_battery_pct) / 100)
-- (MAG-25, charging-add-inferred-capacity). Added to BOTH tables this module owns:
-- manual_charge_entries (user-typed) and charge_sessions (the Supercharger mirror).
--
-- WHY A GENERATED COLUMN AND NOT GO (design.md D2). The ticket requires the value to be
-- current on every INSERT and every UPDATE. This module has THREE independent write paths
-- into these tables -- Writer.Create/Update, SessionWriter.MirrorSessions (whose
-- ON CONFLICT DO UPDATE SET refreshes energy_kwh nightly as fees settle), and
-- SessionVerifier.VerifySession (whose entire purpose is letting a human change the two
-- percentages this formula divides by) -- plus every write path a future change adds.
-- A Go-side computation would have to be correct in all of them, forever, and no reviewer
-- can verify that from a diff. GENERATED ALWAYS AS ... STORED makes it an engine property
-- instead: the value cannot be stale, and the column cannot be written at all (any attempt
-- fails with "column ... can only be updated to DEFAULT", SQLSTATE 428C9).
--
-- THE ALTER *IS* THE BACKFILL (design.md D9). ADD COLUMN ... GENERATED ... STORED computes
-- the value for every pre-existing row as part of this statement, so the ticket's "and
-- calculated for existing rows" needs NO separate backfill UPDATE -- and unlike
-- 20260823000001's backfill, this migration reads nothing outside internal/charging, so it
-- needs no to_regclass guard, no DO $$ block, and no cross-module ordering assumption.
-- Cost: this is a table-rewriting ALTER holding ACCESS EXCLUSIVE. Both tables hold one
-- household's charge history, so the rewrite is effectively instantaneous. Noted so a
-- reader does not mistake it for a metadata-only add.
--
-- GUARD SEMANTICS, identical on both tables (design.md D3). Computed ONLY when every input
-- is present AND end_battery_pct > start_battery_pct; NULL otherwise. The strict > is not
-- cosmetic:
--   * end = start is a DIVISION BY ZERO. Postgres raises SQLSTATE 22012 -- it does not
--     yield NULL -- so without this clause an ordinary row (plugged in at 74%, unplugged at
--     74%) would be REJECTED at INSERT. On charge_sessions that rejection aborts the whole
--     nightly MirrorSessions transaction for the account (RM29: one bad entry rejects the
--     whole call).
--   * end < start yields a NEGATIVE capacity, which is not a physical quantity. A
--     decreasing SoC across a charge record means the percentages are wrong; the honest
--     output is "unknown" = NULL, never a stored negative.
--
-- TYPE: unconstrained NUMERIC, scale pinned to 3 by ROUND(..., 3) (design.md D4). NOT
-- NUMERIC(8,3), which was the first proposal and which REJECTS LEGAL ROWS: the largest
-- value a legal manual_charge_entries row can produce is 9999.99 / 0.01 = 999999.000,
-- against that type's 99999.999 ceiling ("ERROR: numeric field overflow"). charge_sessions
-- is worse -- energy_kwh is DOUBLE PRECISION with no CHECK, mirrored from data Tesla
-- controls, so NO fixed precision is provably safe there, and one overflowing row would
-- abort the night's mirror. Unconstrained precision cannot overflow; ROUND(..., 3) still
-- pins every stored value to the three decimals the ticket's worked examples use.
-- Postgres's round(numeric, integer) rounds half AWAY FROM ZERO, not banker's rounding.
--
-- THE TWO EXPRESSIONS DIFFER IN EXACTLY TWO WAYS, both forced by the source column
-- (design.md D4), and in no other way:
--   1. the "energy IS NOT NULL" guard appears only on charge_sessions, whose energy_kwh is
--      nullable; manual_charge_entries.energy_added_kwh is NOT NULL, so the check there
--      would be dead code falsely implying nullability.
--   2. the ::NUMERIC cast on energy appears only on charge_sessions, whose energy_kwh is
--      DOUBLE PRECISION; the cast is what makes both tables expose the SAME type to
--      readers. manual_charge_entries stays in NUMERIC end to end -- no float round-trip.
--
-- NO INDEX on either column, deliberately (design.md D6): nothing filters, joins, sorts or
-- groups by this column -- every read on both tables selects the whole row and predicates
-- on account_id/tesla_id/a date, all already served by the three existing indexes. An index
-- on a projected-never-predicated column is pure write and storage cost on a path that
-- gains nothing. Same reason internal/analytics indexes none of vehicle_metrics' five _calc
-- columns. Revisit only when a consumer actually filters on it, at which point a partial
-- (account_id, tesla_id) WHERE inferred_capacity_kwh_calc IS NOT NULL is the natural shape.
--
-- NO CHECK CONSTRAINT (e.g. > 0): the expression already cannot produce a non-positive
-- value -- energy_added_kwh is CHECKed > 0, and the guard forces a positive divisor -- so a
-- constraint here would be unreachable, and on charge_sessions (whose energy_kwh carries no
-- CHECK at the source, RM29 D1: "a mirror stricter than its source could not repair a
-- source row it refuses to accept") it would be actively wrong.

ALTER TABLE manual_charge_entries
    ADD COLUMN inferred_capacity_kwh_calc NUMERIC
    GENERATED ALWAYS AS (
        CASE
            WHEN start_battery_pct IS NOT NULL
             AND end_battery_pct   IS NOT NULL
             AND end_battery_pct > start_battery_pct
            THEN ROUND(
                     energy_added_kwh
                     / ((end_battery_pct - start_battery_pct)::NUMERIC / 100),
                     3
                 )
            ELSE NULL
        END
    ) STORED;

ALTER TABLE charge_sessions
    ADD COLUMN inferred_capacity_kwh_calc NUMERIC
    GENERATED ALWAYS AS (
        CASE
            WHEN energy_kwh        IS NOT NULL
             AND start_battery_pct IS NOT NULL
             AND end_battery_pct   IS NOT NULL
             AND end_battery_pct > start_battery_pct
            THEN ROUND(
                     energy_kwh::NUMERIC
                     / ((end_battery_pct - start_battery_pct)::NUMERIC / 100),
                     3
                 )
            ELSE NULL
        END
    ) STORED;

COMMENT ON COLUMN manual_charge_entries.inferred_capacity_kwh_calc IS
    'Pack capacity in kWh implied by this entry: energy_added_kwh / ((end_battery_pct - '
    'start_battery_pct) / 100), rounded to 3 decimals. GENERATED ALWAYS AS ... STORED -- '
    'recomputed by the engine on every INSERT and UPDATE, and unwritable by any caller '
    '(design D2). NULL when either percentage is absent or when end_battery_pct is not '
    'strictly greater than start_battery_pct -- an equal delta would be a division by zero '
    'and a negative delta a negative capacity, neither of which is a physical quantity '
    '(design D3). Unconstrained NUMERIC because NUMERIC(8,3) would reject a legal '
    'max-energy/min-delta row (design D4). NOT indexed: nothing predicates on it '
    '(design D6).';

COMMENT ON COLUMN charge_sessions.inferred_capacity_kwh_calc IS
    'Pack capacity in kWh implied by this session: energy_kwh / ((end_battery_pct - '
    'start_battery_pct) / 100), rounded to 3 decimals. Same GENERATED ALWAYS AS ... STORED '
    'mechanism, same guard, and the same unconstrained NUMERIC type as '
    'manual_charge_entries.inferred_capacity_kwh_calc -- the two expressions differ only in the '
    'energy IS NOT NULL guard (energy_kwh is nullable here) and the ::NUMERIC cast '
    '(energy_kwh is DOUBLE PRECISION here), both forced by the source column (design D4). '
    'NULL additionally whenever energy_kwh is NULL, i.e. the session had no kWh fee. This '
    'column recomputes when the nightly mirror refreshes energy_kwh AND when a human '
    'corrects the percentages through SessionVerifier.VerifySession -- neither write path '
    'names this column, and neither has to (design D2).';

-- +goose Down
-- NON-DESTRUCTIVE by construction, unlike 20260823000001's Down: this column stores nothing
-- that is not fully recomputable from energy_added_kwh/energy_kwh, start_battery_pct and
-- end_battery_pct, none of which this migration touches. Re-running Up reproduces every
-- value exactly, on both tables.
ALTER TABLE charge_sessions        DROP COLUMN IF EXISTS inferred_capacity_kwh_calc;
ALTER TABLE manual_charge_entries  DROP COLUMN IF EXISTS inferred_capacity_kwh_calc;
```

### Write-protection, reproduced

The claim in **D2** that the column is unwritable is an engine guarantee, observed:

```
UPDATE mce SET inferred_capacity_kwh_calc = 1;
ERROR:  column "inferred_capacity_kwh_calc" can only be updated to DEFAULT
DETAIL:  Column "inferred_capacity_kwh_calc" is a generated column.
```

This is why no query change is needed to *protect* the column, and why **D7**'s "read-only
field" contract is enforced by the database rather than by a doc comment. It is asserted as
T12 / T22.

### Backfill, reproduced

See **D9** for the observed output of seeding rows first and then running the `ALTER`.

### Index plan

**No index added; no index altered.** Full justification in **D6**. The three existing
indexes — `idx_manual_charge_entries_vehicle_time (account_id, tesla_id, charged_on DESC)`,
`idx_manual_charge_entries_account_time (account_id, charged_on DESC)`, and
`idx_charge_sessions_vehicle_stop (account_id, tesla_id, charge_stop_date_time)` — are
untouched and continue to serve every existing read exactly as before, because no query's
`WHERE` / `ORDER BY` / `LIMIT` changes. Neither table has an index-only scan to lose: every
read is `SELECT *` already.

---

## Test Contract

Expected values authored **before** implementation, per `ai/go-conventions.md` §Testing
("author their expected values up front, in the change's `design.md`, before the
implementation exists"). **Tests written later must assert THIS contract**, not whatever
the implementation happens to produce.

Every numeric value below was **observed** on PostgreSQL 16 running the exact DDL in
§"Database Changes" — none is a hand calculation. All are at `ROUND(…, 3)`, i.e. scale 3.

**Float comparison:** the domain field is `*float64` converted from `NUMERIC`, so assert
`math.Abs(*got - want) < 1e-9`, never `==`. Assert `nil` / non-`nil` explicitly for every
`NULL` case.

**Fixture hygiene** (following this module's existing convention): fresh `uuid.New()`
account ids per test; `charge_sessions` `session_id`s in the **960001–960099** range,
disjoint from RM29's 920001–920099, RM30's 940001–940099, RM31's 950001–950099, and the
real backfilled `734860294`. Assert only against `charging.Entry` / `charging.Session`
domain fields and direct SQL column values — **never `pgtype`**
(`internal/charging/AGENTS.md` §Testing Notes).

### Group A — `manual_charge_entries` (source `NUMERIC(6,2) NOT NULL`)

Seed via `Writer.Create`. Read back via `Reader.ListEntriesByVehicle` (T1–T3, T9–T11) or
direct SQL where a non-port path is needed (T12).

| ID | `energy_added_kwh` | start % | end % | Expected `inferred_capacity_kwh_calc` | What it proves |
|---|---|---|---|---|---|
| **T1** | `7.04` | 64 | 74 | **`70.400`** | Ticket worked example 2 (its "70.40"). |
| **T2** | `41.31` | 18 | 80 | **`66.629`** | Ticket worked example 3. The ticket writes "66.63" — that is its own 2-decimal restatement of `66.6290…`; at scale 3 the value is `66.629`. Assert `66.629`. |
| **T3** | `52.27` | 29 | 100 | **`73.620`** | Ticket worked example 1 *as this table can store it*. `energy_added_kwh` is `NUMERIC(6,2)`, so the ticket's `52.273` is not representable here and rounds to `52.27` on insert, giving `73.620` — **not** `73.624`. The `52.273` figure is a Supercharger number and belongs to T13. This case exists so nobody "fixes" the expectation to match the ticket's Supercharger row. |
| **T4** | `7.04` | `NULL` | 74 | **`NULL`** | D3: missing start. |
| **T5** | `7.04` | 64 | `NULL` | **`NULL`** | D3: missing end. |
| **T6** | `7.04` | `NULL` | `NULL` | **`NULL`** | D3: both missing. |
| **T7** | `7.04` | 74 | 74 | **`NULL`** | D3: zero delta. **The row must INSERT successfully** — it must not raise `division_by_zero`. Assert both the successful insert and the `NULL`. |
| **T8** | `7.04` | 74 | 64 | **`NULL`** | D3: negative delta. Row inserts; no negative value is stored. |
| **T9** | `0.01` | 0 | 100 | **`0.010`** | Minimum legal energy, maximum delta — the smallest value the schema can produce. Proves scale 3 is preserved on a sub-unit result. |
| **T10** | `9999.99` | 0 | 1 | **`999999.000`** | Maximum legal energy, minimum positive delta. **This row must INSERT successfully.** It is the D4 regression test: under the originally proposed `NUMERIC(8,3)` this insert fails with `numeric field overflow`. If this test ever starts failing, the column's type was narrowed. |

- **T11 — recompute on `UPDATE`.** Create T1's row (`7.04`, 64 → 74, value `70.400`). Then
  `Writer.Update` the same entry changing `end_battery_pct` to **84**, leaving energy
  unchanged. Expect **`35.200`** (`7.04 / 0.20`). Proves the value tracks an update even
  though `UpdateEntry`'s `SET` clause never names the column and no Go code computes it.
- **T12 — the column is unwritable.** Direct SQL:
  `UPDATE manual_charge_entries SET inferred_capacity_kwh_calc = 1 WHERE id = $1`. Expect an
  **error**, matching PostgreSQL's `column "inferred_capacity_kwh_calc" can only be updated to
  DEFAULT` (SQLSTATE `428C9`). Assert the error is non-nil and, if matching on code, use the
  SQLSTATE rather than the message text.

### Group B — `charge_sessions` (source `DOUBLE PRECISION`, nullable)

Seed via `SessionWriter.MirrorSessions`. Read back via
`SessionReader.ListSessionsByVehicleBetween` (or `SuperchargerSessionAnalyticsReader.
ListSessionsByVehicle`), and via direct SQL for T22.

| ID | `energy_kwh` | start % | end % | Expected `inferred_capacity_kwh_calc` | What it proves |
|---|---|---|---|---|---|
| **T13** | `52.273` | 29 | 100 | **`73.624`** | Ticket worked example 1, the Supercharger row, at full `DOUBLE PRECISION` input. **The ticket says "73.63"; that is its own imprecise 2-decimal rounding of `73.6239…`.** The correct value at scale 3 is `73.624`. Assert `73.624`. Also proves the `float8 → numeric` cast takes the shortest round-trip decimal (`52.273`, not `52.27299999…`). |
| **T14** | `NULL` | 29 | 100 | **`NULL`** | D4: a session with no kWh fee. This case exists only on this table. |
| **T15** | `52.273` | `NULL` | 100 | **`NULL`** | D3: missing start. |
| **T16** | `52.273` | 29 | `NULL` | **`NULL`** | D3: missing end. |
| **T17** | `52.273` | 50 | 50 | **`NULL`** | D3: zero delta. Row must mirror successfully — no aborted transaction. |
| **T18** | `52.273` | 80 | 18 | **`NULL`** | D3: negative delta. |
| **T19** | `1000000000` | 0 | 1 | **`100000000000.000`** | Unbounded `DOUBLE PRECISION` source. **`MirrorSessions` must succeed.** The D4 regression test for this table: any fixed precision would abort the whole mirror transaction here (Context 6). |

Rows T14–T19 cannot be seeded with percentages through `MirrorSessions` (`SessionMirror`
has no percentage fields, by RM29 D6 design). Seed the session first via `MirrorSessions`,
then set the percentages via `SessionVerifier.VerifySession` — which is itself the
mechanism T20 tests. For T15/T16 use `VerifySession` with one `nil` argument; for a row
needing no percentages at all, skip the verify step.

- **T20 — verification recomputes (the load-bearing case).** `MirrorSessions` a session
  with `energy_kwh = 41.31` and no percentages → read back, expect
  **`InferredCapacityKWhCalc == nil`**. Then `SessionVerifier.VerifySession(ctx, accountID, id,
  ptr(18), ptr(80))` → expect **`66.629`**. This is the single most important test in the
  change: `VerifyChargeSession`'s `SET` clause names only `start_battery_pct`,
  `end_battery_pct`, `battery_pct_source` and `updated_at`, and no Go code anywhere computes
  capacity — the value appears purely because the engine recomputes it. It is the direct
  proof of **D2**'s central claim. Assert it against `VerifySession`'s own returned
  `charging.Session`, so the returned value is proven fresh too (`RETURNING *`).
- **T21 — a nightly re-mirror recomputes.** Take T20's verified session (`41.31`, 18 → 80,
  value `66.629`). Call `MirrorSessions` again for the same `(account_id, session_id)` with
  `EnergyKWh = 44.64` — the `ON CONFLICT DO UPDATE SET` refresh path. Expect
  **`72.000`** (`44.64 / 0.62`). Proves the value tracks the nightly fee-settlement refresh,
  and — together with T20 — that the two write paths that touch the three inputs from
  opposite directions both keep it correct.
- **T22 — the column is unwritable.** Same as T12, against `charge_sessions`.
- **T23 — domain mapping, sessions.** Through `SessionReader.ListSessionsByVehicleBetween`,
  assert T13's session returns `Session.InferredCapacityKWhCalc` non-`nil` and ≈ `73.624`, and
  T17's returns `nil`. Proves the `rowToSession` wiring and the `NULL → nil` mapping
  (**D7**, **D8**). No `pgtype` in the assertion.
- **T24 — domain mapping, entries.** Through `Reader.ListEntriesByVehicle`, assert T1's
  entry returns `Entry.InferredCapacityKWhCalc` non-`nil` and ≈ `70.400`, and T7's returns
  `nil`. Same proof for `rowToEntry`.

### Not covered by an automated test, deliberately

- **Backfill of rows that existed before the migration** — **D10**. The suite's database is
  provisioned fresh, so there is nothing to backfill in it; the real check is the owner's
  post-`migrate-up` query in tasks.md §"Owner verification", and the behaviour was already
  reproduced in §"Backfill, reproduced".

### What must NOT change

A test asserting any of these must not be weakened, and a reviewer should reject a diff
that does:

- `T2 = 66.629`, `T13 = 73.624`. The ticket's `66.63` / `73.63` are its own 2-decimal
  restatements; **do not "correct" the expectations to match the ticket's prose.**
- `T3 = 73.620`, not `73.624`. `manual_charge_entries` cannot store `52.273`.
- `T10` and `T19` must **insert/mirror successfully**. They are the type-safety regression
  tests (**D4**).
- `T7` / `T17` must insert successfully with a `NULL` result, never raise
  `division_by_zero`.
- `T20` and `T21` must assert a **recomputed** value after a write that never names the
  column.

---

## Risks / Trade-offs

1. **A small SoC delta produces a mathematically valid but practically worthless figure.**
   A 1-point delta divides by `0.01`, so a ±0.5% reading error becomes a ±50% capacity
   error. T10's `999999.000` kWh is the reductio. **Accepted for this change:** the ticket
   asks for the per-row value, and filtering is a product decision about *presentation*, not
   about what the column stores. Storing the raw figure keeps the column an honest record of
   the formula. Mitigation belongs in whatever consumer aggregates it — a minimum-delta
   floor, a median rather than a mean, or both (proposal.md §"Follow-on work", items 1 and
   4). **Flagged for the owner at the gate**: if you want a floor *in the column*, say so
   now — it is a one-clause change to the `CASE` here and a much larger change later.

2. **`ADD COLUMN … GENERATED … STORED` rewrites both tables under `ACCESS EXCLUSIVE`.**
   Trivial at this platform's scale (one household's charge history); recorded because the
   statement is not the metadata-only `ADD COLUMN` a reader might assume.

3. **A future `INSERT INTO <table> SELECT *`, `COPY <table> FROM …` (all columns), or
   `pg_restore` of a column-list-free dump will now fail**, because a generated column
   cannot be inserted into. Nothing in the repository does this today —
   `20260823000001`'s backfill uses an explicit column list, and every generated query
   does too — but a future migration that copies a whole row must name its columns. Noted
   here because it is the one real ongoing constraint the generated column imposes.

4. **Rows with an equal or decreasing SoC are stored silently with a `NULL` capacity**
   (**D3**), not flagged as suspect. If the owner wants such rows surfaced (they usually
   indicate a mistyped entry), that is a separate feature — not this change.

5. **The two tables now name the same concept identically (`inferred_capacity_kwh_calc`) while
   naming their *inputs* differently (`energy_added_kwh` vs `energy_kwh`).** This slightly
   increases the vocabulary mismatch RM29 D1 already accepted and backlog item 12 will have
   to converge. Accepted: giving the output two different names to match two different input
   names would be strictly worse for a consumer reading both tables.

6. **Unconstrained `NUMERIC` is less self-documenting than a declared precision.** A reader
   cannot tell the intended magnitude from the type alone. Mitigated by the column comment
   and by `ROUND(…, 3)` being visible in the expression. Accepted as the cost of **D4**'s
   overflow safety — the alternative is a type that rejects legal rows.

## Verification signals

Run during this artifacts pass, on the live repository and a **throwaway** PostgreSQL 16
database (never the project database), to make the numbers above observations rather than
claims:

| Signal | Result |
|---|---|
| Exact `ALTER … ADD COLUMN … GENERATED … STORED` DDL for both tables, executed | Accepted by PostgreSQL 16 — the expression is `IMMUTABLE`-clean (`round(numeric,int)` and the `float8→numeric` cast both are) |
| All ticket worked examples + every D3 `NULL` case, inserted and read back | Produced every value in the Test Contract |
| `NUMERIC(8,3)` with `energy_added_kwh = 9999.99`, delta 1 | **`ERROR: numeric field overflow`** — the finding behind **D4** |
| Unconstrained `NUMERIC` with `energy_kwh = 1e9`, delta 1 | `100000000000.000`, no error |
| `UPDATE … SET inferred_capacity_kwh_calc = 1` | `ERROR: column … can only be updated to DEFAULT` (**D2**, T12/T22) |
| Rows seeded **before** the `ALTER`, then `ALTER` run | All pre-existing rows populated — **D9** |
| `UPDATE … SET end_battery_pct = <new>` | Value recomputed / cleared to `NULL` as the guard dictates — **D2**, T11 |
| `sqlc v1.31.1` (the project's own version) generating against the proposed schema | Parses the generated column; models gain `InferredCapacityKwhCalc pgtype.Numeric`; `SELECT *`/`RETURNING *` expand to include it; **no `*Params` struct changes** — **D8** |

**Not run** (per `Test-Execution-Policy`): `go test ./...`, `make test`,
`make test-with-db`, `make check`. This is an artifacts-only pass — no Go was written, no
migration was added to `internal/charging/db/migrations/`, and `sqlc generate` was **not**
run against the project (the probe used an isolated scratch project outside the repository).
