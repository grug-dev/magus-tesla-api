# Proposal — RM51-charging-derive-status-and-price-source

Source: MAG-58 — https://linear.app/magus-monitor/issue/MAG-58/chargin-external-charges-v2-done-status
Roadmap: `openspec/roadmaps/RM51-external-charges-completion.md` — **tier 1 of 2**, module `charging`,
implementing roadmap decisions **RD1–RD5** verbatim.
Design gate: **TRIPPED.** This change adds one column to `manual_charge_entries` via a new goose
migration. Per `openspec/config.yaml` §design and `CLAUDE.md` §Pipeline config →
`Design-Gates: database`, design.md carries the full DDL, the rationale with rejected
alternatives, and the index plan, and **the owner must confirm that design before any
implementation is dispatched.**
Unit tests: **included** — the module's standing testing convention
(`ai/go-conventions.md` §Testing) applies unconditionally to `internal/charging`: "Tests for this
module are welcome and required (no paid-API risk)" (`internal/charging/AGENTS.md` §Testing
Notes). Both kinds: offline unit tests for the promotion helper and the price-source rule (no
DB), plus `DATABASE_URL`-gated integration tests for the schema and the write paths. Expected
values are fixed in design.md §Test Contract **before** implementation, per
`ai/go-conventions.md` §Testing authoring order.

---

## Why

Two gaps in `manual_charge_entries` today:

1. **Finishing a charge needs a manual step nobody remembers.** An entry becomes `DONE` only when
   a user moves the status control by hand. If they fill in `ended_at` and `end_battery_pct` but
   leave the control on `IN_PROGRESS`, the entry stays open forever, even though every fact `DONE`
   needs is already there.
2. **A zero price is ambiguous.** `price` is `NOT NULL`, and the gateway already turns an empty
   price box into `0`. So a real free charge and a forgotten price look identical — nothing on the
   row says which one happened.

RD1/RD2 close the first gap: the module promotes a complete `IN_PROGRESS` entry to `DONE` itself,
on both `Create` and `Update`, so "all the facts are there" and "the record is marked done" cannot
drift apart. RD3–RD5 close the second: a new `price_source` column records whether a zero price
was confirmed as real by the person, or is still unconfirmed and worth a second look.

## What Changes

- **ADDED** — a promotion helper in `internal/charging`, called from both `Writer.Create` and
  `Writer.Update`, placed **after** `normalizeStatus` and **before** `missingFields` (roadmap
  RD1/RD2). Rule: if the submitted status is `IN_PROGRESS` and `missingFields` evaluated against
  `RequiredFieldsFor(StatusDone)` is empty, the entry is stored as `DONE`. It reuses
  `missingFields`/`RequiredFieldsFor` — it never hardcodes `ended_at`/`end_battery_pct`.
  **Promotion only — the module never demotes** an entry from `DONE` to `IN_PROGRESS`; that stays
  a user's explicit choice, unaffected by this change.
- **ADDED** — one goose migration
  `internal/charging/db/migrations/20260909000001_add_price_source.sql`, adding one column to
  `manual_charge_entries`:
  - `price_source TEXT NOT NULL DEFAULT 'UNCONFIRMED' CHECK (price_source IN ('USER','UNCONFIRMED'))`
  - a same-migration backfill `UPDATE manual_charge_entries SET price_source = 'USER' WHERE price > 0`

  Two statements, not one (mirrors `20260720000001`'s shape, not `20260829000002`'s
  single-statement one): the correct backfill value depends on each row's own `price`, so it
  cannot be expressed as the column's `DEFAULT`. Full DDL, rationale and index plan in
  design.md §"Database Changes".
- **ADDED** — `Entry.PriceSource PriceSource` (module-computed output) and
  `Entry.PriceConfirmed bool` (caller-supplied input), plus the exported `PriceSource` type and
  its two constants (`PriceSourceUser`, `PriceSourceUnconfirmed`) — the identical shape
  `EnergySource`/`EnergySourceUser`/`EnergySourceEstimated` already uses (roadmap RD4).
- **ADDED** — `resolvePriceSource(e Entry) PriceSource`, applying the roadmap RD3 rule table:
  `price > 0` → `USER`; `price == 0` and `PriceConfirmed` → `USER`; `price == 0` and not
  confirmed → `UNCONFIRMED`. Called from both `Writer.Create` and `Writer.Update`. **The caller's
  own `Entry.PriceSource` is never read** — the gateway sends only the boolean intent, and
  `charging` alone decides the stored provenance (roadmap RD4).
- **CHANGED** — `internal/charging/db/query.sql`: `CreateEntry` and `UpdateEntry` bind the new
  `price_source` column. **No read query changes** — all four are `SELECT *`, which sqlc expands
  to include the new column automatically.
- **CHANGED** — `internal/charging/db/models.go`, `db/query.sql.go` (both **generated**;
  `make sqlc` re-run). `sqlc.yaml` needs no change.
- **CHANGED** — `internal/charging/validation.go` (the promotion helper, beside `missingFields`),
  `internal/charging/service.go` (wiring both rules into `Create`/`Update`, plus the
  `price_source` mapping on read), `internal/charging/charging.go` (domain surface: `PriceSource`
  type + `Entry` fields).
- **CHANGED** — `internal/charging/AGENTS.md` (§Public Interface, §Data Ownership, §Testing
  Notes) — docs-track-structural-change, `CLAUDE.md` §Non-negotiables.
- **CHANGED** — `kkpa/context/workflows/manual-charge-crud.md` and `kkpa/context/INDEX.md`: the
  promotion rule, the price-source rule, and a new INDEX row for `price source` (roadmap "Verified
  codebase findings"). Never touches `openspec/changes/archive/`.
- **UNCHANGED** — every index; `status`'s `CHECK`/backfill/`RequiredFieldsFor`; `energy_source`
  and the derivation seam; `inferred_capacity_kwh_calc`; the whole `supercharger_sessions` table
  and its ports; every file outside `internal/charging/` except the two KB files above.

**Out of scope, deliberately:** the "this charge was free" checkbox, the price-input wiring, and
the "last month" date preset — all of that is tier 2, `RM51-gateway-add-free-charge-and-month-preset`
(module `gateway`, depends on this tier). This change does not touch `internal/gateway`.

## Breaking?

**NO.** Unlike `RM33-charging-add-entry-status` (which changed `Entry.EnergyAddedKWh` from
`float64` to `*float64`), every change here is **additive**: two new `Entry` fields
(`PriceSource`, `PriceConfirmed`), one new exported type, one new unexported helper wired into
existing methods. No interface gains, loses, or re-signs a method — `Writer`, `Reader`,
`SessionWriter`, `SessionReader`, `SuperchargerSessionAnalyticsReader`, and `SessionVerifier` are
all untouched, and so are every existing `Entry` field's type and meaning.

Every `charging.Entry{...}` literal in `internal/gateway` uses **named fields**
(`grep -rn "charging.Entry{" internal/gateway/` confirms this), so two new zero-valued fields on
the struct do not break compilation. A gateway caller that does not yet set `PriceConfirmed`
behaves exactly as it does today: `price > 0` still resolves to `USER`, and `price == 0` (the
gateway's existing empty-price-box behaviour) resolves to `UNCONFIRMED` — the same outcome any
zero price already produces, now with a name. **`go build ./...` and `go vet ./...` stay green
repeatedly across module boundaries** after this tier lands, with no leader-dispatched
cross-module compile fix needed (contrast RM33's design.md D11).

## Modules affected

- **`charging`** — owner. Schema, domain type, both write paths' behaviour, validation, tests,
  `AGENTS.md`.
- **`gateway`** — **not affected by this tier.** It does not yet read or write `PriceSource` /
  `PriceConfirmed`; that wiring is tier 2. `go build ./internal/gateway/...` is unaffected because
  the change is purely additive (see §Breaking above).
- **`analytics`** — consumes `charging.Reader.ListEntriesByVehicleUpdatedSince`. Per `grep`, it
  reads `Entry.EnergyAddedKWh` and other fields but not `Price`/`PriceSource`; the leader should
  confirm with `go build ./...` after the change rather than trusting this sentence.
- No other module. `manual_charge_entries` is owned exclusively by `internal/charging`
  (`ai/architecture.md` §2); no cross-module FK, no cross-module read.

## Read paths affected

Per `openspec/config.yaml` §proposal. Every read on `manual_charge_entries` is `SELECT *`, so all
four carry **one additional column per row** after this change, and nothing else:

| Port method | Query | Effect |
|---|---|---|
| `Reader.ListEntriesByVehicle` | `ListEntriesByVehicle` | +1 column per row |
| `Reader.ListEntriesByAccount` | `ListEntriesByAccount` | +1 column per row |
| `Reader.ListEntriesByVehicleBetween` | `ListEntriesByVehicleBetween` | +1 column per row |
| `Reader.ListEntriesByVehicleUpdatedSince` | `ListEntriesByVehicleUpdatedSince` | +1 column per row |
| `Writer.Create` / `Writer.Update` | `RETURNING *` | +1 column returned |

**No query plan changes.** No `WHERE`, `ORDER BY` or `LIMIT` clause is touched and no predicate
names the new column. Both existing indexes
(`idx_manual_charge_entries_vehicle_time`, `idx_manual_charge_entries_account_time`) are untouched
and still serve exactly the scans they served before.

**No new index is added** (roadmap RD5, offered a partial index at the design gate and declined —
see design.md §Index Plan). Nothing filters, sorts, joins, or groups by `price_source` in this
tier or any planned future one; if that changes, the query that needs it is what justifies the
index, added then.

**Write path:** one `ADD COLUMN` with a non-volatile constant `DEFAULT` — metadata-only on
PostgreSQL 11+, no table rewrite — plus one `UPDATE` scoped to `WHERE price > 0`, which rewrites
only the rows it touches. Cheaper than either of `RM33`'s two migrations.

## Impact

- **Affected spec:** `manual-charge-log` — one **MODIFIED** requirement ("A charge entry has a
  recorded status that governs its required fields", gaining the auto-promotion rule) and one
  **ADDED** requirement (price provenance). No requirement is removed.
- **Affected code:** `internal/charging/` only —
  `db/migrations/20260909000001_add_price_source.sql` (new), `db/query.sql`,
  `db/models.go` + `db/query.sql.go` (regenerated), `charging.go`, `validation.go`, `service.go`,
  new `_test.go` files, `AGENTS.md` — plus `kkpa/context/workflows/manual-charge-crud.md` and
  `kkpa/context/INDEX.md` (KB notes, per the roadmap's verified findings).
- **Design gate: tripped.** The owner confirms design.md before implementation. One item wants an
  explicit yes beyond the DDL itself: the **promotion placement** (design.md D1) — after
  `normalizeStatus`, before `missingFields` — so a submission that is itself invalid for `DONE`
  (e.g. missing `location_kind`) is rejected before promotion is even considered.
- **`MIGRATIONS_DIRS` order check (recorded, not just claimed).** The shared order is
  account → telemetry → charging → analytics. This migration's `ALTER TABLE` and `UPDATE` touch
  only `manual_charge_entries`, a table `internal/charging` owns exclusively, and reads no other
  module's table — unlike `20260823000001`'s Supercharger-history backfill, which is the one
  cross-module read dependency the order exists to protect. So the order is **not at risk**, and
  this is a finding, not an assumption: verified by reading the migration's own SQL, which names
  no table outside `manual_charge_entries`.
- **Deferred, explicitly NOT in scope:** the tier-2 gateway checkbox, form wiring, and "last
  month" preset; any status *demotion* rule; any index on `price_source` (roadmap RD5, revisit
  trigger recorded in design.md); the partial index offered and declined at the gate.

## Modules affected — summary table

| Module | Change |
|---|---|
| `charging` | Owner. Schema, domain, write-path rules, tests, docs. |
| `gateway` | None in this tier (additive change; no compile impact). |
| `analytics` | Read-only consumer; unaffected fields. |
