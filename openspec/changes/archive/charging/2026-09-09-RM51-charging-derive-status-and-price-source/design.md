# Design — RM51-charging-derive-status-and-price-source

> **Design gate: `database`.** This change adds one column to `manual_charge_entries`. Per
> `CLAUDE.md` §Pipeline config → `Design-Gates: database` and `openspec/config.yaml` §design,
> **the owner must confirm this document before any implementation task is dispatched.** It is
> written to be reviewable on its own: the complete DDL, the rationale with rejected
> alternatives, the index plan justified against the declared read patterns, and the Test
> Contract are all here.

Source ticket: MAG-58 · Roadmap: `openspec/roadmaps/RM51-external-charges-completion.md`, tier 1
of 2. Roadmap decisions **RD1–RD5** are binding and were confirmed with the user at the
2026-09-09 `grill-me` interview. This document does not re-open them; where it says something the
roadmap does not, it says so explicitly (**D6**, **D7** below).

---

## Context

Facts that constrain every decision here. Each was read out of the repository, not recalled.

1. **`manual_charge_entries` is owned exclusively by `internal/charging`** — no cross-module FK,
   no cross-module read (`ai/architecture.md` §2). Its schema source of truth is the migration
   directory; there is no `schema.sql`.
2. **`price` is `NUMERIC(14,2) NOT NULL CHECK (price >= 0)`**, and `charging.Entry.Price` is a
   plain `float64`, not a pointer (roadmap "Verified codebase findings"). This is why an untyped
   price box and a real zero look identical on the row today.
3. **`RequiredFieldsFor` and `missingFields` already exist** in `internal/charging/validation.go`
   (RM33), and `Writer.Create` / `Writer.Update` in `service.go` already call `normalizeStatus`
   then `missingFields` before any database call, in that order, before the capacity-derivation
   seam. RD1's promotion helper belongs in that same sequence — **after** `normalizeStatus` and
   **before** `missingFields`, so a submission that is invalid outright (e.g. missing
   `location_kind`) is rejected before promotion is even considered, and so promotion sees the
   status the entry will actually be validated against.
4. **`energy_source` is the precedent for a provenance column a caller may never supply**:
   `resolveEnergy` in `service.go` computes it in Go and the caller's own `e.EnergySource` is
   never read (RM33 design.md D4). RD4 applies the identical shape to `price_source`.
5. **`status`/`energy_source`/`location_kind`/`charging_type` are all `TEXT` + `CHECK` on this
   table**, not a Postgres `ENUM` — the precedent RD3 follows.
6. **The gateway already turns an empty price box into `0`**
   (`internal/gateway/handlers/external_charges.go`, ~line 1320) — unchanged by this roadmap. A
   caller that never sets the new `PriceConfirmed` field (every caller until tier 2 ships) still
   produces `price == 0`, which resolves to `UNCONFIRMED` under RD3's rule — the same outcome a
   zero price already produces today, now recorded with a name instead of being silent.
7. **All four read queries are `SELECT *`; both write queries name their columns explicitly**
   (`db/query.sql`, same fact RM33 recorded). A new column reaches every read for free, and
   cannot be bound by a write unless the write query is edited.
8. **Every `charging.Entry{...}` literal in `internal/gateway` uses named fields**
   (`grep -rn "charging.Entry{" internal/gateway/`). Two new zero-valued struct fields do not
   break compilation anywhere in the repo — unlike RM33's `*float64` type change.

## Goals / Non-Goals

**Goals**

- A charge entry that already carries every fact `DONE` needs is stored as `DONE`, on both
  `Create` and `Update`, without the user having to move the status control by hand.
- A zero price records whether it is a confirmed real amount or an unconfirmed placeholder.
- Both rules are module-computed and cannot be overridden by a caller's own claim.
- Neither rule requires a new index.

**Non-Goals**

- The tier-2 gateway checkbox, form wiring, and the "last month" date preset — that is
  `RM51-gateway-add-free-charge-and-month-preset`. This change does not touch `internal/gateway`.
- Any status *demotion* rule. Nothing in this change moves `DONE` back to `IN_PROGRESS`; RM33's
  existing "no transition rule" stands unchanged.
- A `price_zero_confirmed_at` timestamp, or making `price` nullable — both offered at the
  interview and rejected in favor of the smaller `price_source` column (roadmap RD3).
- Any index on `price_source`, including the partial index offered and declined at the gate
  (roadmap RD5, §Index Plan below).

---

## Database Changes

### The migration

`internal/charging/db/migrations/20260909000001_add_price_source.sql`

```sql
-- +goose Up
-- RM51 tier 1 (MAG-58): a manual charge entry gains a price provenance marker,
-- distinguishing a real zero-cost charge from a price nobody entered.
--
-- WHY TWO STATEMENTS, NOT ONE (roadmap RD3) -- mirrors 20260720000001's
-- backfill-then-constrain shape, NOT 20260829000002's single-statement shape.
-- 20260829000002 could use one ADD COLUMN ... DEFAULT ... because its backfill
-- value equalled the column DEFAULT for every existing row. Here it does not: the
-- correct backfill value depends on each row's OWN price (positive -> USER, zero
-- -> UNCONFIRMED), so a DEFAULT alone cannot express it. The ADD COLUMN carries the
-- fail-closed default (UNCONFIRMED) for any future insert that omits the column;
-- a separate UPDATE promotes the rows whose historical price was actually typed.
--
-- WHY TEXT + CHECK, NOT A POSTGRES ENUM (roadmap RD3). Same reasoning
-- 20260829000002 already gave for status/energy_source: this table already models
-- location_kind, charging_type, status and energy_source this way, and an enum's
-- value set is painful to widen later (ALTER TYPE ... ADD VALUE cannot run inside a
-- transaction block before PG12, and a value can never be removed).
--
-- WHY 'UNCONFIRMED' IS THE DEFAULT, NOT 'USER' (roadmap RD3). It fails closed: a
-- future insert that forgets this column then honestly says "nobody vouched for
-- this price" rather than falsely claiming it is real. USER as the default would
-- claim the opposite.
--
-- WHY THE BACKFILL SPLITS ON price > 0, NOT ON WHETHER price WAS "EVER SUPPLIED"
-- (roadmap RD3) -- there is no such flag to split on. A historical positive price
-- was typed by a person, so USER is true. A historical zero was never confirmed one
-- way or the other before this column existed, so UNCONFIRMED is honest -- and it
-- surfaces those old rows as worth a second look, the same spirit as
-- 20260829000002's IN_PROGRESS backfill.
--
-- COST: the ADD COLUMN is METADATA-ONLY on PostgreSQL 11+ -- a non-volatile constant
-- DEFAULT is stored in the catalogue since PG11, not written into every row. The
-- UPDATE rewrites only the rows it touches (every row with price > 0) and takes no
-- stronger a lock than any ordinary UPDATE on this table already takes.

ALTER TABLE manual_charge_entries
    ADD COLUMN price_source TEXT NOT NULL DEFAULT 'UNCONFIRMED'
        CHECK (price_source IN ('USER','UNCONFIRMED'));

UPDATE manual_charge_entries SET price_source = 'USER' WHERE price > 0;

COMMENT ON COLUMN manual_charge_entries.price_source IS
    'Provenance of price: USER when the amount is known to be real (a positive '
    'price, or a caller-confirmed zero), UNCONFIRMED when a zero price has not been '
    'confirmed as a real free charge (RM51/MAG-58). Always computed by '
    'internal/charging, never accepted from a caller -- the same shape energy_source '
    'already uses. Historical rows were backfilled by their price at migration time: '
    'positive -> USER, zero -> UNCONFIRMED. Not indexed: nothing predicates on it '
    '(design.md Index Plan).';

-- +goose Down
-- LOSSY, not guarded -- unlike 20260829000002's DROP NOT NULL restoration, there is
-- no CHECK or NOT NULL this Down could fail to satisfy, so DROP COLUMN always
-- succeeds without a blocking guard.
--
-- The loss is real but bounded. Every price > 0 row's provenance is trivially
-- recomputable by re-running the Up migration's own backfill rule. A ZERO-price row
-- a caller confirmed as real AFTER this Up ran is a different story: once dropped,
-- it is indistinguishable from a row that was always unconfirmed, because no other
-- column carries that fact. Accepted: a schema rollback is not a data rollback, the
-- same acceptance 20260720000001 already recorded for its location_kind backfill.
ALTER TABLE manual_charge_entries DROP COLUMN IF EXISTS price_source;
```

### Resulting column (the one column this change touches)

| Column | Type | Null | Default | Constraint | Indexed |
|---|---|---|---|---|---|
| `price_source` | `TEXT` | NOT NULL | `'UNCONFIRMED'` | `CHECK (price_source IN ('USER','UNCONFIRMED'))` | **no** |

### Query changes (`db/query.sql`)

Only the two write queries change. `CreateEntry` adds `price_source` to its column list and
`@price_source` to its `VALUES`. `UpdateEntry` adds `price_source = @price_source` to its `SET`.
Both bindings carry a short comment pointing at `energy_source`'s existing precedent: the value is
**computed by Go and never accepted from an external caller**. **No read query is edited** — all
four are `SELECT *`, which sqlc expands to include the new column automatically (Context fact 7).
`sqlc.yaml` is not touched.

### Expected sqlc diff (verify after `make sqlc`; report anything else)

- `db/models.go` — `ManualChargeEntry` gains **exactly one** field: `PriceSource string`, carrying
  the `COMMENT ON COLUMN` text as its doc comment. Every other field is untouched;
  `SuperchargerSession` is untouched.
- `db/query.sql.go` — `CreateEntryParams` and `UpdateEntryParams` each gain the same one field;
  every `SELECT *` / `RETURNING *` column list and every `Scan(…)` list gains one entry. **No
  other `*Params` struct changes** — in particular not `MirrorSuperchargerSessionParams` or
  `VerifySuperchargerSessionParams`, which belong to the untouched `supercharger_sessions` table.
  If one did, stop and report it: it means a query outside this change's scope was edited.

---

## Index Plan

Mandatory under `openspec/config.yaml` §design ("an index plan justified against the project's
read patterns") and `CLAUDE.md` §Design-Gates.

### The declared read patterns on `manual_charge_entries`

Unchanged from RM33's own table — this change adds no new read pattern:

| # | Port method | Predicates | Order | Index used |
|---|---|---|---|---|
| 1 | `Reader.ListEntriesByVehicle` | `account_id`, `tesla_id` | `charged_on DESC` | `idx_..._vehicle_time` |
| 2 | `Reader.ListEntriesByAccount` | `account_id` | `charged_on DESC` | `idx_..._account_time` |
| 3 | `Reader.ListEntriesByVehicleBetween` | `account_id`, `tesla_id`, `charged_on BETWEEN` | `charged_on DESC` | `idx_..._vehicle_time` |
| 4 | `Reader.ListEntriesByVehicleUpdatedSince` | `account_id`, `tesla_id`, `updated_at >=` (residual) | `charged_on DESC` | `idx_..._vehicle_time` |

### Decision: **no new index**, and no change to either existing index (roadmap RD5)

**`price_source` appears in no `WHERE`, `ORDER BY`, `JOIN`, or `GROUP BY` clause, in this tier or
any planned future one.** All four reads above fetch whole rows inside an already-bounded window
(a vehicle, an account, a date range, an updated-since cursor); `price_source` rides along in the
row exactly like `status` and `energy_source` already do — projected, never predicated. The
read-heavy `Performance-Profile` licenses aggressive indexing **for reads that exist**; it does
not license indexing a column no query mentions. On a two-value column, an index would additionally
serve the planner poorly on its own terms: most rows carry one value in steady state, so a
sequential scan is competitive or better anyway.

**Rejected at the gate:** a partial index
`ON (vehicle_vin, charged_on) WHERE price_source = 'UNCONFIRMED'`. The user was offered this
exact shape — a "show me unconfirmed zero prices" worklist index, the same idea RM33's own §Index
Plan flagged as its revisit trigger for `status` — and declined it. No such worklist view exists
yet, and building the index ahead of the query that would use it is exactly the aggressive-write,
zero-read-benefit cost RM33's design.md already rejected for `status`.

**Revisit trigger — stated so the next agent does not have to re-derive it.** If a future change
adds a read that *filters* by `price_source` (e.g. a page listing unconfirmed zero-price charges
for review), the right shape is the partial index above, `account_id`-leading if the read is
per-tenant — mirroring RM33's own `status` revisit trigger. Do **not** create a standalone
`(price_source)` index.

---

## Decisions

### D1 — The promotion helper: rule, placement, and the never-demote guarantee

*Implements roadmap **RD1** and **RD2** verbatim.*

```go
// promoteIfComplete promotes e to StatusDone when its submitted status is
// StatusInProgress and every field RequiredFieldsFor(StatusDone) demands is
// already present -- reusing missingFields/RequiredFieldsFor rather than a
// hardcoded {ended_at, end_battery_pct} list, so a future change to the DONE
// set tightens promotion automatically (roadmap RD1). Promotion only: an entry
// already StatusDone is returned unchanged -- this function never assigns
// StatusInProgress to anything. Called from both Writer.Create and
// Writer.Update, immediately after normalizeStatus and before missingFields,
// so the entry is validated against the status it will actually be stored
// with (design.md Context fact 3).
func promoteIfComplete(e Entry) Entry {
    if e.Status != StatusInProgress {
        return e
    }
    check := e
    check.Status = StatusDone
    if len(missingFields(check)) == 0 {
        e.Status = StatusDone
    }
    return e
}
```

Call site, identical in `Create` and `Update`:

```go
status, err := normalizeStatus(e.Status)
if err != nil {
    return Entry{}, err
}
e.Status = status
e = promoteIfComplete(e)          // <-- new, RD1/RD2

if missing := missingFields(e); len(missing) > 0 {
    return Entry{}, missingFieldsError(e.Status, missing)
}
```

**Why after `normalizeStatus`.** Promotion only makes sense once `e.Status` is a real, validated
value — evaluating an unrecognized status string against `RequiredFieldsFor` would be meaningless.

**Why before `missingFields`.** The check that actually enforces the required-field rule must see
the entry's *final* status, including any promotion — otherwise an entry that just got promoted
to `DONE` would be validated as if it were still `IN_PROGRESS`, silently under-enforcing `DONE`'s
own rule the very moment it starts applying.

**Why it changes nothing for an explicit `DONE` submission.** `promoteIfComplete` returns
immediately when `e.Status != StatusInProgress` — an entry submitted as `DONE` with fields
missing still reaches `missingFields` unpromoted and is still rejected exactly as RM33 specified.
Test Contract **C10** pins this: nothing about this change weakens `DONE`'s existing validation.

**The "known and accepted cost" (roadmap RD1), traced through to a concrete consequence.**
Because promotion also fires on `Update`, and RM33 already permits `DONE → IN_PROGRESS` as an
explicit user choice (RM33 design.md D5, "no transition rule"), a user who "reopens" a `DONE`
entry by submitting `Status: IN_PROGRESS` **without also clearing `ended_at` or
`end_battery_pct`** gets the entry **immediately re-promoted back to `DONE`** by this same helper
— the row never visibly reopens. This is not a bug introduced here; it is RD1's known cost made
concrete by RM33's own reopening mechanism. To actually reopen an entry, the caller must clear at
least one of the two `DONE`-only fields, exactly as RM33 already required. Test Contract **C11**
proves this interaction rather than leaving it implicit.

| Alternative | Why rejected |
|---|---|
| Derive status entirely from the fields, delete the status control | Rejected at the interview (roadmap RD1): makes RD13's JS toggle dead code and removes user control entirely. |
| An explicit "keep open" checkbox | Rejected at the interview (roadmap RD1): a new field on the wire for a case the user does not want. |
| Promote only on `Update`, matching the ticket's literal text | Rejected by the user (roadmap RD2): would let `Create` produce a complete-but-open entry, breaking "complete fields ⇒ `DONE`" as a true invariant of the table. |
| Hardcode `{FieldEndedAt, FieldEndBatteryPct}` in the promotion check | Rejected explicitly (roadmap RD1): duplicates `RequiredFieldsFor(StatusDone)`'s own set; a future change to that set would silently stop being reflected in promotion. |

### D2 — `price_source TEXT NOT NULL DEFAULT 'UNCONFIRMED' CHECK (…)`, two statements, no index

*Implements roadmap **RD3** verbatim.* `TEXT` + `CHECK` over a Postgres `ENUM` (Context fact 5),
`UNCONFIRMED` as the fail-closed default, a two-statement shape because the backfill value is not
uniform across rows (Context fact 2). See §Database Changes for the full DDL and §Index Plan for
"no index."

**The DEFAULT does not implement the rule — only the migration's one-time backfill and the Go
write path do.** A raw `INSERT` at the SQL level that omits `price_source` always lands on
`'UNCONFIRMED'` regardless of the row's own `price`, because a column `DEFAULT` cannot see another
column's value. The `price > 0 ⇒ USER` half of the rule is enforced in exactly two places: the
migration's own backfill `UPDATE` (once, for pre-existing rows) and `resolvePriceSource` in Go
(on every future write, via `Writer`). Test Contract **B3** proves this distinction empirically
rather than leaving it as a claim a future reader has to trust.

| Alternative | Why rejected |
|---|---|
| A single `ADD COLUMN ... DEFAULT` statement, mirroring `20260829000002` | Only possible when the backfill value equals the default for every row, which is not true here — the correct backfill depends on each row's `price`. Roadmap RD3. |
| Postgres `ENUM` | Inconsistent with `location_kind` / `charging_type` / `status` / `energy_source`, which this table already models as `TEXT` + `CHECK`; and an enum's value set is painful to widen later. Roadmap RD3. |
| `price_zero_confirmed_at TIMESTAMPTZ` | Records one event instead of the field's provenance, and does not mirror a pattern this table already has (`energy_source`). Rejected at the interview. Roadmap RD3. |
| Make `price` nullable, use `NULL` for "blank" | The more structurally correct fix, but far larger — it touches every `CHECK`, every read, and the gateway's existing `price == "" → 0` coercion. The user chose the smaller change. Roadmap RD3. |

### D3 — `PriceSource`/`PriceConfirmed` on `Entry`; `charging` computes, the gateway supplies only intent

*Implements roadmap **RD4** verbatim.*

```go
// PriceSource is the provenance of Entry.Price: PriceSourceUser when the amount
// is known to be real (a positive price, or a caller-confirmed zero),
// PriceSourceUnconfirmed when a zero price has not been confirmed as a real
// free charge (RM51 design.md D2/D3). ALWAYS COMPUTED BY internal/charging on
// Create/Update -- a value set on the Entry passed to Writer is ignored and
// overwritten, the same shape EnergySource already uses.
type PriceSource string

const (
    PriceSourceUser        PriceSource = "USER"
    PriceSourceUnconfirmed PriceSource = "UNCONFIRMED"
)
```

`Entry` gains two fields:

- **`PriceSource PriceSource`** — module-computed output, mirroring `EnergySource`'s doc-comment
  shape: "a value set here is ignored and overwritten."
- **`PriceConfirmed bool`** — caller-supplied input. The gateway's *only* contribution to this
  rule: a plain boolean intent, never the provenance itself. Read only when `Price == 0`; ignored
  when `Price > 0` (§D5 below). **Not persisted directly** — it drives `PriceSource`, which is
  what gets written and read back; a round-trip through `Reader` always returns `PriceConfirmed:
  false` on every entry, because the confirmed-intent signal is not itself a stored fact, only its
  consequence (`price_source`) is. This is a deliberate, narrower surface than a stored flag would
  be — see the rejected `price_zero_confirmed_at` alternative in **D2**.

```go
// resolvePriceSource applies the RD3 rule table: a positive price is always
// USER (someone typed a real amount); a zero price is USER only when the
// caller confirmed it is a real free charge (e.PriceConfirmed), UNCONFIRMED
// otherwise. The caller's own e.PriceSource is never read (design.md D3),
// mirroring resolveEnergy's EnergySource computation.
func resolvePriceSource(e Entry) PriceSource {
    if e.Price > 0 {
        return PriceSourceUser
    }
    if e.PriceConfirmed {
        return PriceSourceUser
    }
    return PriceSourceUnconfirmed
}
```

Called from both `Create` and `Update`, alongside where `price` is already encoded into its
`pgtype.Numeric` param — no `ctx`, no database, purely a function of the `Entry`'s own two fields.

| Alternative | Why rejected |
|---|---|
| Accept a caller-supplied `PriceSource` and trust it | Rejected at the interview (roadmap RD4): lets the gateway drift from the rule, exactly the failure mode `EnergySource`'s precedent already closed off. |
| Persist `PriceConfirmed` as its own column | Redundant with `price_source`, which already encodes everything `PriceConfirmed` would let a reader reconstruct for a `price > 0` row, and *cannot* be reconstructed for a `price == 0` row anyway (the module doesn't need to re-derive the caller's original intent — only the outcome). Adds a column for no query. |

### D4 — No index (roadmap RD5)

See §Index Plan for the full justification, the rejected partial index, and the revisit trigger.
Recorded as a numbered decision because `openspec/config.yaml` §design requires the index plan to
be an explicit, justified part of the design rather than an omission.

### D5 — Precedence: a positive price always wins over `PriceConfirmed`

**Not a separate roadmap decision — the direct reading of RD3's rule table, made explicit because
`resolvePriceSource` is written as two sequential `if`s rather than a single lookup.** The table
has three rows, but the first condition (`price > 0`) is checked first and returns immediately:
a caller that (incorrectly, or maliciously) sets both `Price: 100.00` and `PriceConfirmed: true`
or `PriceConfirmed: false` gets `PriceSourceUser` either way — the confirmation flag is only ever
consulted when `Price == 0`. Test Contract **A10** and **C4** both pin this, from the unit and the
integration side respectively, so the precedence is proven rather than assumed from reading the
function body.

### D6 — Interaction with `normalizeStatus`'s empty-status rule (forced, not covered by the roadmap)

**Not a roadmap decision.** RM33's `normalizeStatus` maps an empty `Status` (`""`, Go's zero
value) to `StatusInProgress` before `promoteIfComplete` ever runs. A caller that never sets
`Status` at all but
happens to supply every `DONE`-required field is therefore **also** subject to promotion: the
empty status normalizes to `IN_PROGRESS`, and `promoteIfComplete` then promotes it to `DONE` if
the fields are complete. This is the correct reading of RD1 ("if the submitted status is
`IN_PROGRESS`") rather than an edge case to special-case away, because normalization has already
run by the time promotion sees the value — there is no meaningful difference, at that point,
between "the caller explicitly said `IN_PROGRESS`" and "the caller said nothing." Test Contract
**C9** exercises this path explicitly so it is proven rather than assumed.

**Which callers actually hit this path — checked, not assumed.** The gateway is **not** one of
them. `parseExternalChargeForm` sets `Status: status` on the `Entry` it builds
(`internal/gateway/handlers/external_charges.go`), so every gateway write already carries an
explicit status today. The stale comment in `validation.go` — "parseExternalChargeForm builds an
Entry with no Status field today" — describes RM33 tier 1, before tier 2 wired the control up.
Do not trust it; it is a leftover. So D6 covers direct callers of the port and future ones, not
the live UI. **The user-facing consequence of RD1 is therefore the explicit case, not this one:**
a person who deliberately selects `IN_PROGRESS` with both fields filled gets `DONE` saved. That
is RD1 working as the user chose, and it is the behaviour to expect the moment this tier ships —
before tier 2 changes any screen. Fixing the stale comment is out of scope here; it is noted so
the tier 2 worker does not repeat the mistake.

### D7 — The `Down` migration is lossy but unguarded, unlike RM33's

**Not a roadmap decision.** `20260829000002`'s `Down` had to guard against an impossible state
(`energy_added_kwh IS NULL` rows can't satisfy a restored `NOT NULL`) and refuses rather than
destroy. This migration adds no `NOT NULL` and no generated column, so its `Down` has no
impossible state to guard against — `DROP COLUMN` always succeeds. The loss is real (a
caller-confirmed real zero price becomes indistinguishable from an always-unconfirmed one once
the column is gone) but bounded and already precedented: `20260720000001`'s `Down` accepts the
identical shape of loss for its `location_kind` backfill, and this migration follows that simpler
precedent rather than `20260829000002`'s guarded one, because nothing here is *impossible* to
roll back — only *lossy*, which every `Down` in this table's history already accepts for its own
backfill.

### Roadmap-decision mapping

| design.md | roadmap | Subject |
|---|---|---|
| D1 | **RD1, RD2** | promotion helper — rule, placement, never-demote, both write paths |
| D2 | **RD3** | `price_source` column, TEXT+CHECK, two-statement backfill, UNCONFIRMED default |
| D3 | **RD4** | `PriceSource`/`PriceConfirmed` on `Entry`; module computes, gateway supplies intent |
| D4 | **RD5** | index plan — "no index," justified, with the rejected partial index and the revisit trigger |
| D5 | — | precedence: `price > 0` always wins over `PriceConfirmed` (forced, a direct reading of RD3) |
| D6 | — | interaction with empty-status normalization (forced; not covered by the roadmap) |
| D7 | — | `Down` migration semantics (forced; not covered by the roadmap) |

Roadmap **RD6–RD8** are tier 2 (`RM51-gateway-add-free-charge-and-month-preset`) and are not
implemented here.

---

## Test Contract

Expected values authored **before** implementation, per `ai/go-conventions.md` §Testing ("author
their expected values up front, in the change's `design.md`, before the implementation exists").
**Tests written later must assert THIS contract**, not whatever the implementation happens to
produce.

**Conventions**, following this module's existing ones (`internal/charging/AGENTS.md` §Testing
Notes): fresh `uuid.New()` account ids per test; assert only against `charging.Entry` domain
fields and direct SQL column values — **never `pgtype`**, in any file; assert `nil`/non-`nil`
explicitly for every optional field. Integration tests are `DATABASE_URL`-gated through the
package's existing `testdb_test.go` and mirror `db_entry_status_integration_test.go`'s style.

### Group A — offline unit tests (no DB)

New files: `entry_status_test.go` (existing file, extended, package `charging`) for A1–A6;
`price_source_test.go` (new, package `charging`) for A7–A10.

**A1–A6 — `promoteIfComplete`.** Every case supplies `ChargedOn` and `LocationKind` unless stated
otherwise, since those two are required at every status and a promotion check must see the full
`DONE` set, not just the `IN_PROGRESS → DONE` skip set.

| ID | Input (`Status`, then which of `ChargedOn`/`LocationKind`/`EndedAt`/`EndBatteryPct` are set) | Expected `Status` after `promoteIfComplete` | What it proves |
|---|---|---|---|
| **A1** | `IN_PROGRESS`; all four set | `DONE` | The headline rule: a complete `IN_PROGRESS` entry is promoted (RD1). |
| **A2** | `IN_PROGRESS`; `ChargedOn`, `LocationKind`, `EndBatteryPct` set, `EndedAt` nil | `IN_PROGRESS` (unchanged) | Missing `ended_at` blocks promotion. |
| **A3** | `IN_PROGRESS`; `ChargedOn`, `LocationKind`, `EndedAt` set, `EndBatteryPct` nil | `IN_PROGRESS` (unchanged) | Missing `end_battery_pct` blocks promotion — the symmetric case. |
| **A4** | `IN_PROGRESS`; `LocationKind`, `EndedAt`, `EndBatteryPct` set, `ChargedOn` zero | `IN_PROGRESS` (unchanged) | Proves the helper reuses the **full** `RequiredFieldsFor(StatusDone)` set, not just `{ended_at, end_battery_pct}` — a field required at every status still gates promotion. |
| **A5** | `IN_PROGRESS`; `ChargedOn`, `EndedAt`, `EndBatteryPct` set, `LocationKind` nil | `IN_PROGRESS` (unchanged) | Symmetric to A4, for `location_kind`. |
| **A6** | `DONE`; `EndedAt` and `EndBatteryPct` both nil (an inconsistent state, constructed only to probe the guard) | `DONE` (unchanged) | **Never demotes.** The function's first line returns immediately for any status other than `IN_PROGRESS` — it cannot be talked into assigning `IN_PROGRESS` under any input (RD1's "never demotes" guarantee, proven at the unit level). |

**A7–A10 — `resolvePriceSource`.**

| ID | Input (`Price`, `PriceConfirmed`) | Expected | What it proves |
|---|---|---|---|
| **A7** | `100.00`, `false` | `PriceSourceUser` | A positive price is always `USER` (RD3 row 1). |
| **A8** | `0`, `true` | `PriceSourceUser` | A confirmed zero is `USER` (RD3 row 2). |
| **A9** | `0`, `false` | `PriceSourceUnconfirmed` | An unconfirmed zero is `UNCONFIRMED` (RD3 row 3). |
| **A10** | `100.00`, `true` | `PriceSourceUser` | **Precedence** (D5): a positive price wins regardless of the confirmation flag — the same outcome as A7, proving the flag is not even consulted. |

### Group B — schema, defaults, and constraints (integration, direct SQL)

`db_promotion_price_source_integration_test.go` (new). Direct `INSERT`s where stated, because
these assert the *database's* behaviour, not the port's.

| ID | Statement | Expected | What it proves |
|---|---|---|---|
| **B1** | `INSERT` naming only pre-migration columns (no `price_source`), `price = 8000.00` | row created; `price_source = 'UNCONFIRMED'` | **The DEFAULT mechanism**, not the real historical backfill (which the owner verifies post-`migrate-up`, see below): a raw insert that never names the column always lands on the column `DEFAULT`. |
| **B2** | `INSERT … price_source = 'FREE'` | error, SQLSTATE **`23514`** | The `price_source` `CHECK` (D2). |
| **B3** | `INSERT` naming only pre-migration columns, `price = 8000.00` (same shape as B1, restated as its own case for clarity) | `price_source = 'UNCONFIRMED'`, **not** `'USER'` | **The DEFAULT does not implement the price-based rule** — only the migration's one-time backfill `UPDATE` and the Go write path do (D2). A row inserted after this migration, at the raw SQL level, with a positive price and no explicit `price_source` still defaults to `UNCONFIRMED`; only `Writer.Create`/`Writer.Update` (Group C) apply the price-aware rule. |

### Group C — write-path behaviour through `Writer`/`Reader` (integration)

Same file. Seed via `Writer.Create` unless stated; read back via `Reader.ListEntriesByVehicle`.

| ID | Create/Update input (beyond the always-present `AccountID`/`TeslaID`/`VIN`/`ChargedOn`/`LocationKind`/`Currency`) | Expected stored state | What it proves |
|---|---|---|---|
| **C1** | `Price: 8000.00`, `PriceConfirmed: false` | `PriceSource == USER` | A positive price is `USER` through the real write path (RD3 row 1). |
| **C2** | `Price: 0`, `PriceConfirmed: true` | `PriceSource == USER` | A confirmed zero is `USER` (RD3 row 2). |
| **C3** | `Price: 0`, `PriceConfirmed: false` | `PriceSource == UNCONFIRMED` | An unconfirmed zero is `UNCONFIRMED` (RD3 row 3) — also the behaviour of every caller that has not adopted `PriceConfirmed` yet (Context fact 6). |
| **C4** | `Price: 8000.00`, `PriceConfirmed: true` | `PriceSource == USER` | Precedence through the write path (D5), matching A10. |
| **C5** | `Price: 0`, `PriceConfirmed: false`, **and** `PriceSource: PriceSourceUser` set on the same `Entry` | `PriceSource == UNCONFIRMED` | **A caller cannot set the provenance directly** — it is always computed (D3), matching `EnergySource`'s precedent (RM33 C4). |
| **C6** | Seed C3's row (`UNCONFIRMED`), then `Update` with `Price: 0`, `PriceConfirmed: true` | `PriceSource == USER` | Provenance is **recomputed on every write, never sticky** — mirrors `EnergySource`'s C14 precedent from RM33. |
| **C7** | `Status: IN_PROGRESS`, `EndedAt: ptr(t)`, `EndBatteryPct: ptr(90)` (all other `DONE`-required fields present) | created; `Status == DONE` | **The headline promotion feature, through `Writer.Create`** (RD1). |
| **C8** | Seed an `IN_PROGRESS` row missing `EndedAt`/`EndBatteryPct`, then `Update` supplying both while still submitting `Status: IN_PROGRESS` | `Status == DONE` | **Promotion also fires on `Update`** (RD2) — the second write path, proven independently of C7. |
| **C9** | `Status: ""` (empty, Go's zero value), all `DONE`-required fields present | created; `Status == DONE` | **D6** — an omitted status is not a special case: it normalizes to `IN_PROGRESS` first, then promotes exactly as an explicit `IN_PROGRESS` would. |
| **C10** | `Status: DONE`, `EndedAt: ptr(t)`, `EndBatteryPct: nil` | **error** naming `end_battery_pct`; row count unchanged | **Promotion changes nothing for an explicit `DONE` submission** — an incomplete `DONE` entry is still rejected exactly as RM33 specified (D1). |
| **C11** | Seed a `DONE` row (C7's shape), then `Update` with `Status: IN_PROGRESS`, `EndedAt` and `EndBatteryPct` **left unchanged** (still non-nil) | `Status == DONE` (re-promoted, not reopened) | **The known-and-accepted cost, proven concretely** (D1): "reopening" without clearing the two `DONE`-only fields is immediately re-promoted back to `DONE`. Reopening still requires clearing at least one field, exactly as RM33 already required. |

### Owner verification (not automatable here)

The package's test database is provisioned fresh with every migration applied *before* any row
exists, so the real backfill of pre-existing production rows cannot be exercised by a test — the
same limitation RM33 recorded for its own backfill (RM33 design.md, "Owner verification"). **B1**
and **B3** prove the mechanism (the column `DEFAULT`, and that it is price-blind); the owner
confirms the real outcome after `make migrate-up`:

```sql
SELECT price_source, count(*), count(*) FILTER (WHERE price = 0) AS zero_price_rows
  FROM manual_charge_entries
 GROUP BY price_source;
```

Expected on a database migrated from before this change: every `price > 0` row under `USER`,
every `price = 0` row under `UNCONFIRMED` — i.e. the `zero_price_rows` count for the `USER` group
is `0`, and the `zero_price_rows` count for the `UNCONFIRMED` group equals that group's total.

---

## Risks

1. **The re-promotion-on-reopen interaction (D1/C11)** is subtle: a user who thinks they are
   reopening an entry by flipping the status control, without also clearing an end-of-session
   field, sees no visible change. Mitigated by: it is the roadmap's own stated "known and accepted
   cost," and tier 2's UI is unaffected by this tier (the status control's behaviour when it comes
   to clearing fields does not change).
2. **`PriceConfirmed` is never populated on read (D3).** A future consumer that expects to see
   "was this confirmed" on a read entry only has `PriceSource` to look at, not the original
   boolean. Deliberate: `PriceSource` is the persisted fact; a second stored flag would duplicate
   it for a `price > 0` row and be unreconstructable anyway for the `price == 0` case once
   overwritten. Documented so nobody "fixes" this by adding a column later without re-deriving
   the reasoning.
3. **The `Down` migration is lossy** (D7), same category of risk `20260720000001` already accepts
   for `location_kind`. Not new to the project's risk profile, but recorded because
   `openspec/config.yaml` §design asks for the rationale on every schema change.
