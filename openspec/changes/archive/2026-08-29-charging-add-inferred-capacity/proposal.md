Source: MAG-25 — https://linear.app/magus-monitor/issue/MAG-25/inferred-capacity
Design gate: **TRIPPED.** This change creates a new database column on two existing
tables (`manual_charge_entries`, `charge_sessions`) via a new goose migration. Per
`openspec/config.yaml` §design and `CLAUDE.md` §Pipeline config → `Design-Gates: database`,
design.md carries the full DDL, the rationale with rejected alternatives, and the index
plan, and **the owner must confirm that design before any implementation is dispatched.**
Unit tests: **integration-only**, matching this module's RM29 tier 6 / RM30 tier 1 /
RM31 precedent. The derived value is computed by PostgreSQL, not by Go — there is no Go
function to unit-test offline, and `Entry`/`Session` gain no new value-receiver method.
The whole contract is `DATABASE_URL`-gated integration coverage (design.md §Test Contract,
T1–T24), whose expected values are fixed in design.md **before** implementation, per
`ai/go-conventions.md` §Testing authoring order.

---

## ⚠️ Column name — one added segment vs the ticket's wording

The ticket's acceptance criterion names the column **`inferred_capacity_calc`**. This
proposal specifies **`inferred_capacity_kwh_calc`** — the ticket's name with the mandatory
unit segment inserted.

**We kept your `_calc`. We added the `_kwh` the project requires.** `CLAUDE.md`
§Non-negotiables and `ai/go-conventions.md` §"Read optimization" require that *every*
persisted unit-bearing column end in its display-unit suffix, so "the unit is readable from
the column name alone, no migration or comment required." The stored value is kilowatt-hours
(kWh delivered ÷ fractional state-of-charge delta = kWh), so `_kwh` is not optional. The
ticket's literal name is the only thing here that would break a stated non-negotiable, and
it breaks it by *omission* — nothing in it is wrong, it is just missing one segment.

**The `_calc` half is not a concession — it is this project's own convention**, and keeping
it is what makes the name right rather than merely legal. `internal/analytics/vehicle_metrics`
already established `_calc` for *stored, derived* columns, placing it **after** the unit
suffix: `distance_traveled_km_calc`, `battery_used_pct_calc`, `km_per_pct_calc`,
`estimated_range_km_calc`, `days_spanned_calc`
(`internal/analytics/db/migrations/20260821000001_add_vehicle_metrics.sql:67-71`). So the
specified name reads exactly as this codebase already names a stored derived quantity:
`<what>_<unit>_calc`.

| Option | Name | Unit-suffix rule | `_calc` convention | Ticket wording |
|---|---|---|---|---|
| **specified** | `inferred_capacity_kwh_calc` | ✅ | ✅ | + one segment |
| ticket literal | `inferred_capacity_calc` | ❌ violates | ✅ | exact |
| unit-only | `inferred_capacity_kwh` | ✅ | ✗ | + one segment, − `_calc` |

**Override path preserved.** One word at the design gate switches every artifact to either
other row — it is a pure rename with no other consequence (same DDL, same expression, same
type, same guard, same tests), touching only the migration, both column comments,
`charging.go`, `service.go`, `session_reader.go`, both test files and `AGENTS.md`. The Go
field would become `InferredCapacityCalc` or `InferredCapacityKWh` respectively.

---

## Why

The platform stores two independent records of energy going into a vehicle —
`manual_charge_entries` (user-typed home/work/third-party sessions) and `charge_sessions`
(the nightly Supercharger mirror) — and both already carry the three facts needed to infer
the pack's usable capacity: energy added, start battery %, and end battery %. Nothing
computes that inference today, so a fact the platform already holds is invisible.

MAG-25 asks for it as a stored per-row value:

```
inferred_capacity = energy_added / ((end_soc - start_soc) / 100)
```

The ticket is explicit that the value must be current on **every insert and every update**,
and that it must be **backfilled for rows that already exist**. Both requirements are
satisfied by making the column a PostgreSQL `GENERATED ALWAYS AS (...) STORED` column
rather than by writing Go on the write path — an engine guarantee instead of a discipline
that no reviewer can verify across the module's *three* independent write paths
(`Writer.Create`/`Writer.Update`, `SessionWriter.MirrorSessions`, and
`SessionVerifier.VerifySession`, which by design changes exactly the battery percentages
this formula divides by). The full argument, with the rejected alternatives, is
design.md **D2**.

This also aligns with the project's `Performance-Profile`: a `STORED` generated column is
precomputation paid on the write path — mostly the nightly poller — for the benefit of
reads, which is the trade this platform mandates.

## What Changes

**Additive only.** No column, index, constraint, port, type, or query is removed or
re-shaped anywhere.

- **ADDED** — a new goose migration
  `internal/charging/db/migrations/20260829000001_add_inferred_capacity.sql`, which adds
  **one column to each of the two tables this module owns**:
  - `manual_charge_entries.inferred_capacity_kwh_calc NUMERIC GENERATED ALWAYS AS (...) STORED`
  - `charge_sessions.inferred_capacity_kwh_calc NUMERIC GENERATED ALWAYS AS (...) STORED`

  The two expressions are **not** copy-paste siblings: the source energy column differs in
  type between the tables (`NUMERIC(6,2)` vs `DOUBLE PRECISION`) and `charge_sessions`'
  energy is additionally nullable, so each table gets its own explicitly-cast expression
  (design.md **D4**). Full DDL in design.md §"Database Changes".
- **ADDED** — the guard semantics, identical on both tables: the value is computed **only**
  when energy, `start_battery_pct` and `end_battery_pct` are all non-NULL **and**
  `end_battery_pct > start_battery_pct`. Otherwise the column is `NULL` — never an error,
  never a stored negative, never a division by zero (design.md **D3**).
- **ADDED** — `InferredCapacityKWhCalc *float64` on `charging.Entry` and on `charging.Session`.
  Server-computed and **read-only**: it is ignored on `Writer.Create`/`Writer.Update`
  exactly as `ID`, `CreatedAt` and `UpdatedAt` already are, and the database physically
  rejects any attempt to write it (design.md **D7**). Without this field the new column
  would be unreachable through any public port, and the module boundary
  (`ai/architecture.md` §2) means no other module could ever read it — so this field is
  part of the column's deliverable, not scope creep.
- **CHANGED** — `internal/charging/db/models.go`, `query.sql.go` (both **generated**;
  `make sqlc` re-run). **No hand-edit to `internal/charging/db/query.sql` is required or
  permitted** — every read on both tables is `SELECT *` / `RETURNING *`, which sqlc
  expands to an explicit column list including the new column automatically, while every
  write names its columns explicitly and therefore cannot bind it. Verified by probe, not
  assumed (design.md **D8**).
- **CHANGED** — `internal/charging/service.go` (one new nullable `pgtype.Numeric → *float64`
  helper, wired into `rowToEntry`) and `internal/charging/session_reader.go` (the same
  helper wired into `rowToSession`).
- **CHANGED** — `internal/charging/AGENTS.md` (§Units convention, §Data Ownership,
  §Public Interface, §Testing Notes) — docs-track-structural-change,
  `CLAUDE.md` §Non-negotiables.
- **UNCHANGED** — every index, every constraint, every existing column, `SessionMirror`,
  `SessionWriter`, `SessionReader`, `SuperchargerSessionAnalyticsReader`,
  `SessionVerifier`, `Writer`, `Reader`, `sqlc.yaml`, and every file outside
  `internal/charging/`. No port gains a method. No new query is written.

**Breaking:** **no.** Purely additive at the schema level (a new nullable column), at the
sqlc level (a new struct field on two read models; no `*Params` struct changes — probed),
and at the Go port level (a new field on two returned structs; no interface signature
changes). Every existing caller compiles and behaves identically.

**Modules affected:** **`charging` only.** Both tables are owned exclusively by
`internal/charging`. No other module's code, schema, migration, or port changes.
`internal/analytics` and `internal/gateway` consume `charging`'s ports and will silently
receive the new field on the structs they already receive; neither is required to do
anything, and neither is touched by this change.

## Read paths affected

Per `openspec/config.yaml` §proposal ("performance-sensitive proposals must name the read
path(s) they affect"). Every read on both tables is `SELECT *`, so **all seven existing
read paths carry one additional `NUMERIC` column per row after this change**, and no
other change:

| Port method | Query | Effect |
|---|---|---|
| `Reader.ListEntriesByVehicle` | `ListEntriesByVehicle` | +1 column per row |
| `Reader.ListEntriesByAccount` | `ListEntriesByAccount` | +1 column per row |
| `Reader.ListEntriesByVehicleBetween` | `ListEntriesByVehicleBetween` | +1 column per row |
| `Reader.ListEntriesByVehicleUpdatedSince` | `ListEntriesByVehicleUpdatedSince` | +1 column per row |
| `SessionReader.ListSessionsByVehicleBetween` | `ListSessionsByVehicleBetween` | +1 column per row |
| `SuperchargerSessionAnalyticsReader.ListSessionsByVehicleUpdatedSince` | same-named query | +1 column per row |
| `SuperchargerSessionAnalyticsReader.ListSessionsByVehicle` | same-named query | +1 column per row |
| `Writer.Create` / `Writer.Update` / `SessionVerifier.VerifySession` | `RETURNING *` | +1 column returned |

**No query plan changes.** No `WHERE`, `ORDER BY`, `JOIN` or `LIMIT` clause is touched; no
predicate names the new column; every existing index
(`idx_manual_charge_entries_vehicle_time`, `idx_manual_charge_entries_account_time`,
`idx_charge_sessions_vehicle_stop`) is untouched and still serves exactly the scans it
served before. Neither table has an index-only scan to lose — every read is `SELECT *`
already. The cost is one extra `NUMERIC` value serialized per returned row.

**No new index is added** — deliberately, with the reason stated rather than left implicit
(design.md **D6**): the ticket declares no read that *filters or sorts* by this column, and
an index on a column nothing predicates on is pure write and storage cost on a read path
that gains nothing.

**Write paths**, for completeness: one `ALTER TABLE` full-table rewrite at migration time
(both tables are small — a personal-scale platform), then one `CASE` + one division + one
`ROUND` per inserted or updated row thereafter. Those rows are written by the nightly
poller and by occasional human edits, which is exactly the traffic the `Performance-Profile`
says may pay for read-side precomputation.

## Impact

- **Affected specs:** `manual-charge-log` (**ADDED** requirement) and `charge-session-log`
  (**ADDED** requirement). No existing requirement in either spec is modified or removed —
  in particular `manual-charge-log`'s existing "Derived read-time values" requirement is
  untouched and remains true: `CostPerKWh`, `BatteryDelta` and `SessionDuration` stay
  read-time and unstored. This change adds a *separate*, stored, database-computed value
  alongside them, and design.md **D2** explains why this one is stored when those are not.
- **Affected code:** `internal/charging/` only —
  `db/migrations/20260829000001_add_inferred_capacity.sql` (new),
  `db/models.go` + `db/query.sql.go` (regenerated), `charging.go`, `service.go`,
  `session_reader.go`, two new `_test.go` files, `AGENTS.md`.
- **Design gate:** **tripped** — the owner confirms design.md before implementation is
  dispatched. **One item needs an explicit yes beyond the schema itself:** the **result
  type/precision** (design.md **D4**, which departs from the interview's proposed
  `NUMERIC(8,3)` for a reproduced-overflow reason). The **column name** was the second such
  item and is now settled on the `vehicle_metrics` precedent (design.md **D1**, and the box
  at the top of this file) — the override remains one word, but the design is not waiting
  on it.
- **Deferred, explicitly NOT in scope** (design.md **D5**): any per-vehicle aggregate
  table, rollup, materialized view or summary column; any `internal/analytics` read model
  or metric; any `internal/gateway` handler, template, route, i18n key or UI; any new port
  method or query; any change to `internal/telemetry.supercharger_sessions` (which holds
  the same three input columns and could carry the same derived column — deliberately not
  done, see §"Follow-on work" below); and any convergence of the two tables' vocabularies
  (backlog item 12).

## Follow-on work (noted, not built)

Recorded here so it is visible at review and can be filed against
`openspec/roadmaps/backlog.md` by the leader — **none of it is in this change**:

1. **Surface inferred capacity per vehicle.** The ticket's phrase "saved per vehicle" is
   satisfied by the per-row column, which rolls up per vehicle on read — but nothing
   actually rolls it up yet. A per-vehicle aggregate (median or trimmed mean over the
   rows where the column is non-NULL, which is the statistically meaningful reading —
   a single small-delta session is a noisy capacity estimate) belongs in
   `internal/analytics`, not here, and needs its own ticket.
2. **A dashboard surface for pack-capacity degradation over time.** Capacity inferred
   per session, plotted against odometer or date, is the battery-degradation graph the
   project's own brainstorm list already wants ("Battery health log"). Needs a gateway
   change and an analytics read model.
3. **The same column on `internal/telemetry.supercharger_sessions`.** That table holds
   the same three inputs and is `charge_sessions`' upstream source. Adding it there was
   rejected for this change: it is another module (a boundary this worker may not cross),
   the ticket names only the charging tables, and RM29's mirror already brings the inputs
   here. Worth a ticket only if a telemetry-side consumer ever appears.
4. **A minimum-delta quality filter.** `end - start = 1%` produces a mathematically valid
   but practically worthless capacity figure (design.md §Risks). Deciding a floor — and
   whether it belongs in the column, in a read filter, or in the future aggregate — is a
   product question this change deliberately does not answer.
