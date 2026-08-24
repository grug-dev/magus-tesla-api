# Design — RM29-charging-add-charge-sessions

> Numbering note: this document's decisions are **D1, D2, …**, scoped to this change
> only — distinct from the roadmap's own **D1–D12** in
> `openspec/roadmaps/RM29-modular-monolith-boundaries.md` (cited as **roadmap D4**, …),
> from tier 5's archived decisions (cited as **tier 5 D1**, …), and from the binding
> interview outcomes the owner settled with the leader (cited as **T6-I1** … **T6-I5**).
> D1–D5 each carry exactly one interview outcome, named in their heading. **D6–D9 are
> decisions this artifacts pass had to make** to turn T6-I1–I5 into a buildable change;
> each says so in its heading so the owner can review them separately.

> **Design-gate revision (owner-confirmed).** **T6-I1 was amended at the gate**:
> `charge_sessions` is a real mirror of a charge session, not a verification sidecar. It
> now also carries `site_location_name`, `energy_kwh`, `total_cost`, `currency` and
> `is_paid`. Four of those five are **rewritten on every nightly upsert at the source**,
> so the original safety argument ("copy only the immutable subset, so the mirror cannot
> drift") no longer covers them and is **superseded**, not quietly retained — see D1,
> which states both the old argument and what replaces it. D6, the `MirrorChargeSession`
> query, the backfill, the Test Contract and the spec delta are all revised to match.
> **D9 stands** (no reader port this tier). **D8's sqlc question is closed** — verified by
> probe, not asserted.

## Context

Roadmap **D4**: `charging` owns `manual_charge_entries` (unchanged) plus a new
`charge_sessions` that normalizes `telemetry.supercharger_sessions` and hosts the five
battery-percentage verification/estimate columns. Manual ↔ Supercharger convergence is
deferred (backlog item 12) and is not in scope here.

Four facts about the existing schema shape everything below.

1. **`supercharger_sessions` has two halves with opposite mutability.**
   `UpsertSuperchargerSession`'s conflict clause is
   `ON CONFLICT (session_id) DO UPDATE SET raw_data, energy_kwh, total_cost, currency,
   is_paid, tesla_id, updated_at` — those columns are **rewritten on every nightly
   upsert** as Tesla's fees settle and invoices finalize (the table's own header comment:
   "billing state is mutable post-session"). Every other column —
   `session_id`, `account_id`, `vin`, `site_location_name`, `country_code`,
   `charge_start_date_time`, `charge_stop_date_time`, `unlatch_date_time`,
   `billing_type`, `vehicle_make_type`, `created_at` — is written once at INSERT and
   never refreshed. **This split, not a guess about volatility, is what decides how each
   mirrored column behaves here (D1, D6).**

2. **The five percentage columns have no writer anywhere in the repository.**
   `internal/telemetry/db/query.sql:343` carries a 12-line LOAD-BEARING comment
   explaining that all five are deliberately absent from both halves of the upsert, and
   `internal/telemetry/service.go:805` documents `upsertSuperchargerSession` deliberately
   not reading them. RM27 shipped the storage only; the verification UI is backlog item
   11.

3. **Migrations run directory by directory, in one fixed order.**
   `MIGRATIONS_DIRS = internal/account/… internal/telemetry/… internal/charging/…
   internal/analytics/…`, and `make migrate-up` runs `goose up` to completion in each
   directory before moving to the next. Version numbers do **not** reorder across
   modules. This single fact both forbids one thing (D4) and guarantees another (D5, D8).

4. **The read that matters filters on stop-time, not start-time.**
   `SuperchargerSessionsByVehicleBetween` — the telemetry query powering RM28's
   consumed-per-day derivation — filters `charge_stop_date_time`, per roadmap **D12**:
   energy is fully delivered at session stop, which is exactly what `end_battery_pct`
   corresponds to. Its own doc comment records that
   `idx_supercharger_sessions_vehicle_time` (start-time-leading) does **not** fully serve
   it, and that a dedicated stop-time index was consciously not added there.

## Goals / Non-Goals

**Goals**

- `internal/charging` owns a dense `charge_sessions` table: one row per Supercharger
  session, carrying identity, the session's time window, the session facts a charging
  read needs (site, energy, cost, currency, paid state), and the five battery-percentage
  columns (**D1**, **D2**).
- Every existing Supercharger session is present in `charge_sessions` after
  `make migrate-up`, with the one legacy provenance violation resolved (**D5**).
- The nightly poller reconciles `charge_sessions` against `supercharger_sessions` inside
  the same cycle that refreshed the source, and **cannot** clobber a human-verified
  percentage — structurally, not by comment (**D6**, **D7**).
- `internal/telemetry` ends this change byte-for-byte unchanged (**D4**).
- Zero consumer changes: `internal/analytics` and `internal/gateway` are untouched
  (**D9**).

**Non-Goals**

- Dropping anything from `telemetry`. Not one column, index, query, type or comment
  (**D4**); D9 documents what that follow-up actually costs.
- Manual ↔ Supercharger convergence (roadmap D4, backlog item 12). `charge_sessions` and
  `manual_charge_entries` stay two tables with two shapes — and, after the gate's naming
  decision, two vocabularies (D1).
- The verification UI that writes the percentages (backlog item 11). This tier ships the
  storage and the provenance constraint; no code writes a percentage.
- A read port on `charge_sessions` (**D9**) — it would have no caller in this tier.
- Copying `raw_data`, `country_code`, `unlatch_date_time`, `billing_type` or
  `vehicle_make_type`. **This exclusion list is closed** (**D1**).

---

## Decisions

### D1 — What `charge_sessions` carries, and why the drift argument changed (carries T6-I1, as amended at the design gate)

The table carries:

| Group | Columns |
|---|---|
| **Identity** | `account_id`, `vin`, `tesla_id`, `session_id` |
| **Time window** | `charge_start_date_time`, `charge_stop_date_time` |
| **Session facts** | `site_location_name`, `energy_kwh`, `total_cost`, `currency`, `is_paid` |
| **Verification (charging-owned)** | `start_battery_pct`, `end_battery_pct`, `battery_pct_source`, `start_battery_pct_est`, `end_battery_pct_est` |

**Not copied, and this list is closed:** `country_code`, `unlatch_date_time`,
`billing_type`, `vehicle_make_type`, `raw_data`.

**Why those five stay out.** Two reasons, and they compound. First, they are
vendor/provenance detail that **no declared read has asked for** — `country_code` and
`vehicle_make_type` describe the Tesla account and the car model rather than the charge;
`billing_type` and `unlatch_date_time` are billing/hardware minutiae; none appears in any
read path this design or the deferred re-point (D9) names. Second, **every copied field
is another field the mirror has to keep in step** — a permanent, recurring cost paid
nightly, forever, per column. A column earns its place in the mirror by being needed by a
charging read; none of these five is, and if one becomes so it can be added by a later
migration with a one-line backfill from the source, which still exists (D4).

`raw_data` additionally has a rule of its own: `ai/go-conventions.md` §"Read
optimization" makes the lossless JSONB the schema-drift **hedge**, and a hedge works
because exactly one place holds the untouched payload. `supercharger_sessions.raw_data`
is that place and is not going anywhere (D4). Duplicating it would double the storage and
halve the clarity about which copy is authoritative. Precedent: neither
`manual_charge_entries` (user-typed data) nor `charge_gaps` (a Go-computed conclusion)
carries `raw_data` either.

#### The superseded rationale — recorded, not left standing

The pre-gate version of this design copied **only** columns that fact 1 classifies as
write-once at the source, and justified the denormalization this way:

> *"Copying only the immutable half makes this a safe denormalization: a copied value can
> never drift from its source, because the source never changes it either."*

**That argument no longer covers this table**, and it is withdrawn rather than left to be
read as still applying. `energy_kwh`, `total_cost`, `currency` and `is_paid` sit squarely
in telemetry's `ON CONFLICT DO UPDATE SET` clause — they change after both copies were
written, which is the textbook definition of a sync liability. `tesla_id` was already in
that clause and was already the one acknowledged exception; there are now five.

#### What replaces it

**Drift is real, bounded, and reconciled by construction.** Three properties do the work
the immutability argument used to do:

1. **The mirror never originates a value.** Every mirrored column is copied; none is
   computed, defaulted or edited here. So the source is unambiguously authoritative and
   "reconcile" always means "overwrite from source", never "merge".
2. **Reconciliation runs in the same cycle as the change.** The mirror step is a
   post-cycle hook on the same collector run that refreshed `supercharger_sessions`
   (**D7**) — not a separate schedule, not a later job. The staleness window is the
   duration of one nightly cycle, not a day; and a failed mirror self-heals on the next
   cycle because every run re-mirrors from scratch, with no accumulated state to lose.
3. **The re-mirror is idempotent and total.** `MirrorChargeSession` upserts every session
   every pass and its conflict clause refreshes exactly the columns telemetry's own
   conflict clause refreshes (**D6**). A mirrored column therefore has the same
   write semantics on both sides by construction, rather than by two lists that have to
   be kept in agreement by hand.

The class distinction from fact 1 survives — it just moves from *"which columns do we
copy"* to *"which columns do we refresh"*:

| Class | Columns | At the source | In `charge_sessions` |
|---|---|---|---|
| Write-once | `session_id`, `account_id`, `vin`, `charge_start_date_time`, `charge_stop_date_time`, `site_location_name` | never in `ON CONFLICT` | never in `ON CONFLICT` — write-once here too |
| Refreshed | `tesla_id`, `energy_kwh`, `total_cost`, `currency`, `is_paid` | in `ON CONFLICT DO UPDATE SET` | in `ON CONFLICT DO UPDATE SET` |
| Refreshed but not copied | `raw_data` | in `ON CONFLICT DO UPDATE SET` | — |
| Charging-owned | the five percentage columns | write-protected (fact 2) | never touched by the mirror (**D6**) |

**The rule, stated once so D6 and the query doc comment can cite it:** *a mirrored column
gets exactly the write semantics its source column has.* `site_location_name` is not
refreshed **because telemetry does not refresh it** — not because someone judged a site
name unlikely to change. That removes the judgement call from every future column.

#### Naming: telemetry's column names, not charging's

`energy_kwh`, `total_cost`, `currency`, `site_location_name` — **not** charging's own
`energy_added_kwh`, `price`, `location_label`. The owner chose this at the gate so the
backfill stays a **literal column-to-column copy** whose correctness is checkable by
reading it, with no per-column rename to verify. Given that this migration is the one
irreversible step in the change (D4/D5), buying inspectable correctness there is worth a
naming inconsistency elsewhere.

**The accepted cost, recorded plainly:** `internal/charging` now names the same concepts
two different ways across its two tables — `energy_added_kwh` vs `energy_kwh`, `price` vs
`total_cost`, `location_label` vs `site_location_name`. Anyone reading the module has to
hold both vocabularies, and **backlog item 12's eventual convergence will therefore
involve a rename**, not just a merge. That is a known, priced consequence of this
decision, not an oversight.

Both vocabularies satisfy the platform unit convention independently: `energy_kwh` and
`energy_added_kwh` both carry the `_kwh` suffix, and `total_cost` and `price` are both
monetary amounts, which take **no** unit suffix and are instead paired with a `currency`
column — `CLAUDE.md`'s named money exemption. `is_paid` (a flag) and
`site_location_name` (a name) take no suffix by the same rule. `make money-guard` is
unaffected either way: it is a **gateway-only** grep for a handler hand-rolling a money
string with `fmt.Sprintf("%.Nf %s", …)` instead of calling `formatMoney`, and this change
touches no gateway file.

#### `total_cost` is `DOUBLE PRECISION` — copied deliberately, flagged as a problem

Telemetry stores `total_cost DOUBLE PRECISION`; charging's own
`manual_charge_entries.price` is `NUMERIC(14,2)`. This table copies the source's type, to
preserve the literal-copy property above — a `NUMERIC` target would make the backfill a
cast, and a cast is exactly the kind of step whose correctness stops being checkable by
inspection.

**Recorded, not fixed here: float is a questionable type for money.** Binary floating
point cannot represent most decimal money values exactly, so sums and comparisons
accumulate error; `NUMERIC` is the conventional choice and is what this module already
uses for the amounts it owns. Two consequences follow, both out of scope for this tier:
(a) the deferred convergence with `manual_charge_entries` (backlog item 12) **must**
reconcile `DOUBLE PRECISION` against `NUMERIC(14,2)`, which is a data-migration decision
with rounding semantics attached, not a rename; (b) changing telemetry's own column type
is a separate change against a module this tier may not touch (D4). Worth its own backlog
entry — flagged in the worker's report for the leader to file; **not** filed from inside
this change.

**Rejected — copy nothing mutable, keep the "cannot drift" guarantee.** This is the
pre-gate design. Rejected by the owner at the gate: it makes `charge_sessions` a
verification sidecar rather than a charge-session record, so any read that wants a
session's energy or cost must still reach into `telemetry` — which is a cross-module
database read the boundary rule forbids, or else a second port call and an in-Go join per
read. Buying an absolute no-drift guarantee at the price of an unusable read model is the
wrong trade for a platform whose stated `Performance-Profile` is read-heavy.

### D2 — Dense, not sparse: one row per Supercharger session (carries T6-I2)

`charge_sessions` holds one row for **every** Supercharger session, not only for sessions
that have verified percentages. Percentage columns start NULL; a future verification UI
(backlog item 11) fills them.

**Why.** A date-range read over `charge_sessions` alone returns every session with its
window, its site, its energy and its cost, plus any verified percentages — no join, and
specifically no join *into another module's table*, which `ai/architecture.md` §2 forbids
anyway. That is the entire point of the shape, and the gate's amendment to D1 sharpened
it: the dense mirror is now genuinely self-sufficient for a charge-session read, where
before it still needed telemetry for anything but the window.

**Rejected — the sidecar shape** (`session_id` + the five percentage columns only, one
row per *verified* session). Rejected on three counts: (a) every read needs the session's
window and facts, which the sidecar does not have, so every read becomes a cross-module
join or an in-Go merge on the hot read path; (b) the sidecar never lets `telemetry` drop
the percentage columns, so the ownership story never completes; (c) it makes "no verified
percentage" and "no session" the same state — absence of a row — which
`inferMissingChargingType` specifically has to tell apart.

**Rejected — the union-ready shape** (one table carrying both manual entries and
Supercharger sessions behind a `source` discriminator). Rejected by roadmap **D4** and
that roadmap's own "Rejected alternatives" section: the two sources differ in
granularity, trust level, provenance and available fields, and reconciling them is domain
modelling, not refactoring. It is backlog item 12, with a named trigger (a consumer that
needs to treat both uniformly, e.g. a combined cost-per-km metric). Building the union
now would force that modelling to happen inside a boundary refactor, which is how a
refactor turns into a redesign.

### D3 — Both vehicle keys; the index leads with stop-time (carries T6-I3)

The table carries **both** vehicle keys, exactly as `supercharger_sessions` does:

- `vin TEXT NOT NULL` — the durable key. Written once, never refreshed. A VIN survives
  deregistration and re-registration; it is what makes an orphaned row still meaningful.
- `tesla_id BIGINT` (nullable) — the currently-registered vehicle id, **refreshed on
  every sync**. NULL when the session's VIN is not a currently-registered vehicle of the
  account (sold/removed car). This mirrors telemetry's contract verbatim, per D1's rule:
  `collectChargingHistory` builds a VIN → TeslaID map from
  `account.AllRegisteredVehicles` and writes `tesla_id = NULL` on a miss, and
  `UpsertSuperchargerSession` refreshes `tesla_id` in its conflict clause, so this table
  refreshes it too.

`charging` does **not** re-resolve the VIN itself: its `AGENTS.md` forbids importing
`internal/account`, and re-resolving would need exactly that. The value is inherited from
the mirror source, which telemetry resolved that same night — one resolution, one owner,
no second copy of the mapping rule (D6, D7).

**Keys.** `UNIQUE (account_id, session_id)` — not telemetry's globally-unique
`UNIQUE (session_id)`. Tesla's session id is globally unique in practice, but a
multi-tenant table whose uniqueness is not account-scoped can be poisoned across tenants
by one bad id, and every other table in this platform scopes its keys by account. The
account-scoped form is strictly safer and costs nothing.

**Index.** `(account_id, tesla_id, charge_stop_date_time)` — see §"Index Plan" for the
full justification. The short version is fact 4: the read that matters is RM28's
consumed-per-day derivation, whose telemetry counterpart filters on
`charge_stop_date_time` per roadmap **D12** (energy is fully delivered at session stop,
which is what `end_battery_pct` corresponds to). So this index leads to **stop**-time —
deliberately unlike `idx_supercharger_sessions_vehicle_time`, which leads to start-time
and which `SuperchargerSessionsByVehicleBetween`'s own doc comment already records as an
imperfect fit for that query.

### D4 — Expand now, contract later; `internal/telemetry` is untouched (carries T6-I4) — HARD CONSTRAINT

Tier 6 ships **only** the `internal/charging/db/migrations/` migration:
`CREATE TABLE charge_sessions` + the backfill. It drops **nothing** from `telemetry`. Not
one file under `internal/telemetry/` is edited by this change.

**Why a DROP in the same change destroys the data.** Fact 3:
`MIGRATIONS_DIRS = account telemetry charging analytics`, and goose runs the directories
**in that order**, each to completion. A telemetry-directory
`ALTER TABLE supercharger_sessions DROP COLUMN start_battery_pct, …` therefore executes
**before** the charging-directory backfill is even considered, no matter what version
numbers the two files carry — version numbers do not reorder across directories. The
backfill would then read columns that no longer exist, and the migration would fail
*after* the data was already gone.

**And here it is not recoverable.** Tier 5 identified this same hazard (tier 5 D1) for a
table it could relocate wholesale. Here the percentage data cannot be recomputed: no code
path writes those columns (fact 2), so the one populated row was hand-entered by a human.
A DROP that runs first is permanent.

**Precedent.** RM29 already used expand-in-one-tier / contract-in-a-later-tier for
T3 → T4: tier 3 added `analytics.vehicle_metrics` and re-pointed every reader; tier 4, a
separate archived change, dropped the five `_calc` columns from `vehicle_snapshots` only
once that was true. This tier is the "expand" half of the same pattern; D9 describes its
"contract" half honestly.

**Rejected — one change, two migrations, ordered by version number.** The shape that
looks obviously fine and is the trap. `20260823000001` in `charging` and
`20260823000002` in `telemetry` would still run telemetry's first, because the loop is
over directories, not over a merged, sorted set of migrations. The Makefile's own comment
above `MIGRATIONS_DIRS` says exactly this: "Ordering this list CANNOT fix that: it orders
directories, not the migrations inside them."

### D5 — A provenance CHECK telemetry never had; the backfill resolves the one legacy violation (carries T6-I5)

`charge_sessions` carries a constraint `supercharger_sessions` does not:

```sql
CONSTRAINT charge_sessions_pct_source_required CHECK (
    (start_battery_pct IS NULL AND end_battery_pct IS NULL)
    OR battery_pct_source IS NOT NULL
)
```

If either verified percentage is set, `battery_pct_source` must be set. Telemetry's
value-range CHECKs are carried over unchanged (`start_battery_pct BETWEEN 0 AND 100`,
`end_battery_pct BETWEEN 0 AND 100`, `battery_pct_source IN ('user_verified','polled')`),
as are the two on the `_est` pair.

**The one legacy row.** The live database holds exactly one session with percentages —
`session_id = 734860294`, `start_battery_pct = 29`, `end_battery_pct = 100`,
`battery_pct_source = NULL`, both `_est` columns NULL — which violates the new CHECK. The
backfill resolves it with `COALESCE(battery_pct_source, 'user_verified')`, **guarded on a
percentage being non-null** so a row with no percentages keeps its NULL source rather
than being handed a fabricated one.

**Why `'user_verified'` is inference, not invention.** Fact 2: nothing in this repository
can write those columns. A percentage that is nevertheless present was therefore entered
by a human, directly against the database — which is precisely what `'user_verified'`
denotes in the enum RM27 defined (`'polled'` denotes a measured-SOC alternative that was
descoped and never implemented). The backfill records the only provenance the evidence
supports.

**Why only this constraint may be stricter than the source.** A mirror that enforces more
than its source can, in general, make a legitimate source row impossible to mirror — and
the mirror has no way to repair the source. So the rule this design follows is:
**`charge_sessions` may constrain only the columns it will own the writes for.** The five
percentage columns qualify: after this tier, no telemetry code writes them (none can),
and the future verification UI writes them here. Everything mirrored from telemetry
carries no constraint stricter than telemetry's own. Three concrete consequences, all
deliberate:

- **No `charge_stop_date_time >= charge_start_date_time` CHECK**, despite the analogous
  one on `manual_charge_entries`: `supercharger_sessions` permits a reversed window, so
  such a CHECK would let one odd upstream row fail the whole nightly mirror batch.
- **No `total_cost >= 0` or `energy_kwh >= 0` CHECK**, despite `manual_charge_entries`
  having both on its own columns: the source has neither, and a negative fee adjustment
  is a thing billing systems do.
- **No `currency` format or enum constraint**, for the same reason.

### D6 — The mirror port is structurally percentage-free (NOT covered by T6-I1–I5 — new decision; revised at the gate)

The write port takes a type that carries every mirrored column and has **no fields for
the five percentage columns**:

```go
// SessionMirror is the mirrorable subset of one Supercharger charge session: the
// identity, the time window, and the session facts internal/telemetry collects.
// It deliberately has NO battery-percentage fields — see SessionWriter.
//
// Field names mirror telemetry.SuperchargerSession's, which mirror the column
// names, so the whole path stays a literal copy (design.md D1).
type SessionMirror struct {
	AccountID uuid.UUID
	VIN       string
	TeslaID   *int64 // nil when the VIN is not a currently-registered vehicle
	SessionID int64

	ChargeStartDateTime time.Time
	ChargeStopDateTime  time.Time

	SiteLocationName string
	EnergyKWh        *float64 // nil when the session had no kWh fee
	TotalCost        *float64 // nil when the session had no fees
	Currency         *string  // nil when the session had no fees
	IsPaid           *bool    // nil when the session had no fees
}

// SessionWriter is the synchronization port called by the nightly orchestrator
// (cmd/poller today; internal/app after RM29 tier 7). Upsert-only: a session that
// disappears from Tesla's history stays mirrored.
type SessionWriter interface {
	// MirrorSessions upserts every supplied session under accountID, in one
	// transaction. Every entry's AccountID must equal accountID; a single
	// mis-scoped entry rejects the WHOLE call and writes nothing.
	MirrorSessions(ctx context.Context, accountID uuid.UUID, sessions []SessionMirror) error
}

func NewSessionWriter(pool *pgxpool.Pool) SessionWriter
```

**The percentage-free property survives the gate's amendment intact, and it is the point
of the type.** Telemetry protects the same five columns with a 12-line LOAD-BEARING
comment on a query and a second comment on the mapping function, because in that design
there *is* a `SuperchargerSession` struct carrying the fields and nothing but discipline
stops a future edit from binding them. Here the protection is a **compile error**: the
mirror path has no field to bind. That is the project's "deterministic signals over human
round-trips" rule applied to a schema invariant — a type the compiler checks beats a
comment a reviewer has to notice. Growing the type from six fields to eleven does not
weaken this at all; the five that matter are still absent, and their absence is still
enforced by the compiler rather than by attention.

The same protection is repeated at the SQL layer (belt and braces, and because sqlc
generates the params struct from the query text, not from the Go type): the query names
only the eleven mirrorable columns.

**The conflict clause refreshes exactly telemetry's set, minus `raw_data`:**

```
energy_kwh, total_cost, currency, is_paid, tesla_id, updated_at
```

It does **not** refresh `charge_start_date_time`, `charge_stop_date_time`,
`site_location_name`, `created_at`, or any percentage column. This is not five separate
judgement calls — it is D1's single rule applied mechanically: *a mirrored column gets
exactly the write semantics its source column has.* `site_location_name` is absent from
telemetry's conflict clause, so it is absent from this one. `raw_data` is the only
member of telemetry's refresh set that is missing here, and only because the column
itself is not copied (D1).

#### The `WHERE` guard: dropped

The pre-gate query carried
`WHERE charge_sessions.tesla_id IS DISTINCT FROM EXCLUDED.tesla_id` on the `DO UPDATE`,
so an unchanged row was not rewritten and `updated_at` stayed a true change signal. With
five refreshable columns instead of one, that predicate has to either grow to a five-way
`IS DISTINCT FROM` disjunction or go. **It goes.** Three reasons, in order of weight:

1. **D1's rule decides it.** Telemetry's own upsert has no such predicate: it bumps
   `updated_at = now()` on every nightly pass, for every session. A mirror that is *more*
   selective than its source produces an `updated_at` whose meaning differs from the
   source column of the same name — the exact opposite of the literal-copy property the
   owner chose at the gate. Keeping the guard would make the two `updated_at`s look
   identical and behave differently, which is worse than either behaviour on its own.
2. **A widened predicate is a second list that rots silently.** It would have to name
   every refreshed column; adding a column to the refresh set and forgetting the
   predicate causes `updated_at` to silently stop advancing for that column's changes.
   No compiler, test or guard in this project catches that — it is precisely the
   "closed vocabulary maintained by hand in two places" shape the AI-efficiency rule
   warns about. Dropping the guard deletes the second list.
3. **What it saves is negligible and is the cheap side of the profile.** One row rewrite
   per session per nightly pass, on a table holding 4 rows today, on a write path that
   runs at midnight. The binding `Performance-Profile` says writes may pay for reads;
   this is not even paying for a read.

**Consequence, stated so no test asserts the old behaviour:** `updated_at` on
`charge_sessions` means "the last mirror pass touched this row" — identical in meaning to
`supercharger_sessions.updated_at`, and **not** a "this row's data changed" signal on
either side. Test Contract B2 asserts this directly. Anything that later needs a true
change signal (e.g. a watermark for the deferred re-point, D9) must be designed for
explicitly, exactly as `analytics` had to do against telemetry's identically-behaving
column.

**Rejected — one `charging.Session` with all sixteen fields, percentages documented as
"ignored on write".** That is exactly telemetry's shape, and exactly the failure mode this
tier exists to remove. **Rejected — write the percentages through the mirror and let
`COALESCE` preserve existing values.** It makes the poller a writer of human-owned data,
reintroducing the clobber risk RM27's R3 was written to prevent, and it means the mirror
would push telemetry's copy back over a value verified in `charging` once the columns
eventually diverge.

**Return type is `error`, not `(int, error)`:** "rows affected" is not the count of
sessions handled and would make a misleading log line. `cmd/poller` logs `len(sessions)`,
which is unambiguous.

### D7 — `cmd/poller` re-mirrors the full history per account, after each collection cycle (NOT covered by T6-I1–I5 — new decision)

**Placement.** `cmd/poller`'s existing `reconcilingCollector` already decorates
`telemetry.Collector` so post-cycle work runs on both the scheduled path and `--once` by
construction. The mirror becomes the **first** post-cycle step, before the existing
metrics/gap reconciliation:

```
CollectAll (telemetry writes supercharger_sessions)
  └─ mirror step    (NEW — telemetry.SuperchargerReader → charging.SessionWriter)
  └─ reconcile step (unchanged — analytics.Reconcile, then the gap writer)
```

This placement is also what makes D1's drift argument true: `charge_sessions` is
reconciled inside the **same cycle** that refreshed `supercharger_sessions`, so the two
copies are never more than one collection cycle apart. Order relative to the reconcile
step is not a correctness requirement in this tier — nothing reads `charge_sessions` yet
(D9) — but "propagate data, then derive from it" stays correct once the deferred re-point
lands, so it is the order to establish now. The reconcile step's existing per-vehicle
isolation and error handling are **not touched**.

**Shape.** Per distinct account (deduplicated from `acct.AllRegisteredVehicles`, the same
enumeration the reconcile step already uses):

1. `superchargerReader.SuperchargerSessionsByAccount(ctx, accountID, 0)` — telemetry's
   existing public port. **Verified:** `resolveLimit` (`internal/telemetry/reader.go:150`)
   maps `limit <= 0` to `math.MaxInt32`, so `0` really does mean "every session".
2. Map each `telemetry.SuperchargerSession` → `charging.SessionMirror` (eleven fields,
   field-name-for-field-name — no renames, no derivation; see D1's naming decision, which
   is what makes this mapping trivially checkable).
3. `sessionWriter.MirrorSessions(ctx, accountID, mirrored)`.

The mapping lives in `cmd/poller`, the composition root — **not** in either module.
`charging` may not import `telemetry` (its `AGENTS.md` forbids it) and `telemetry` must
not know `charging` exists. This is the same "orchestration lives in `cmd/`" call
`newNightlyReconciler` already embodies (roadmap D4a), and it moves into `internal/app`
wholesale at tier 7.

**Per-account isolation:** one account's mirror failure is logged and skipped, never
fatal, mirroring `CollectAll`'s and the reconciler's existing behavior. A missed mirror
self-heals on the next cycle, because every run re-mirrors from scratch.

**Why full re-mirror rather than incremental.** `telemetry.SuperchargerReader` also
offers `SuperchargerSessionsByVehicleUpdatedSince`, which would allow a watermark-driven
incremental sync. Rejected for now on three grounds: (a) the source itself is already a
full fetch every night — the roadmap's own "Constraint that forced D7" records that
`collectChargingHistory` calls `ChargingHistory` with empty params, fetching the whole
history nightly, so an incremental mirror would be more selective than its own source;
(b) the upsert is idempotent and the whole pass is one indexed read plus a handful of
writes; (c) a watermark is state, and state needs its own table, its own reset story and
its own tests — cost this tier does not need to pay at 4 rows. **Documented trigger to
revisit:** when a single account's session count makes the nightly full pass a visible
cost (order 10⁴+ sessions), switch to `SuperchargerSessionsByVehicleUpdatedSince` with a
watermark in `charging`.

### D8 — The backfill is a guarded `DO` block; `charging`'s tests switch to `ProvisionDirs` (NOT covered by T6-I1–I5 — new decision; sqlc question closed at the gate)

The backfill reads `supercharger_sessions`, a table in another module's migration
directory. Three consequences had to be resolved.

**(a) It must not break a `charging`-only database.** `internal/charging`'s own test
provisioning applies only `db/migrations/*.sql` (a `//go:embed` directive cannot contain
`..`), so in that database `supercharger_sessions` does not exist and a bare
`INSERT … SELECT … FROM supercharger_sessions` fails at migration time. The backfill is
therefore wrapped in a `DO $$ … $$` block that returns early when
`to_regclass('public.supercharger_sessions') IS NULL`. PL/pgSQL plans a statement lazily,
on first execution, so the `INSERT` inside the guard is never planned when the guard
returns first — the standard idiom for a conditionally-present relation. In a real
database the guard always passes: `MIGRATIONS_DIRS` runs `telemetry` before `charging`
(fact 3), the same ordering fact that makes D4's DROP unsafe. **The dependency is soft:
ordering affects data completeness, never migration success.**

**(b) sqlc ignores the block — VERIFIED BY PROBE, not asserted.** The open question in
the pre-gate draft was whether sqlc, which builds `chargingdb`'s catalog from this
module's migration directory, would choke on the reference to a relation that directory
never creates. **The leader probed it directly before Apply:** this exact migration was
written into `internal/charging/db/migrations/`, `make sqlc` was run — **exit 0**,
`chargingdb` gained a correct `ChargeSession` model with every column, and **no
`supercharger_sessions` reference leaked into charging's generated code** — and the tree
was reverted. A PL/pgSQL body is an opaque string to the SQL parser, and sqlc behaves
accordingly. **There is no fallback to design for**; the `EXECUTE`-dynamic-SQL escape
hatch the draft carried is deleted as dead weight. Task 1.1 still runs `make sqlc`, now
as a **regression check confirming a known-good result**, not as an experiment.

**(c) The backfill must be testable.** A migration's backfill can never be observed
through normal test provisioning — goose applies it before any test can seed a source
row, so it always runs against an empty table. Two things follow:

- `internal/charging/testdb_test.go` switches from `testdb.Provision(ctx, fsys)` to
  `testdb.ProvisionDirs(ctx, "../telemetry/db/migrations", "db/migrations")` — telemetry
  first, so `supercharger_sessions` exists for the test to seed. This is the sanctioned
  form (`ai/go-conventions.md` §Testing: "more than one module's tables →
  `ProvisionDirs`"), it is already what `internal/analytics` does, and seeding another
  module's table with direct `INSERT`s from a `_test.go` file is explicitly the right
  answer there (RM29 decision D19). A migration *directory path* is not a Go import: the
  module's `MUST NOT import internal/telemetry` rule is untouched, and no `_test.go` file
  in `charging` imports the telemetry package.
- The backfill test **re-executes the shipped statement**, extracted at runtime from the
  migration file between two sentinel comments (`-- BACKFILL-BEGIN` /
  `-- BACKFILL-END`), which the test package can already read from its existing
  `//go:embed db/migrations/*.sql`. Re-execution is safe because the statement is
  idempotent (`ON CONFLICT … DO NOTHING`). **Rejected — copy the SQL into the test as a
  const**: two copies of one statement drift, and the copy that drifts is the one nobody
  runs in production.

**Note for `ai/go-conventions.md`:** that file currently states, of inter-directory
migration ordering, "Today none do [depend on another's schema]". This change makes that
sentence false. It is corrected in the same change (docs-track-change), naming
`charging` → `telemetry` as the first soft dependency and this design's rationale for why
it is soft.

### D9 — Zero consumer changes; and what the deferred contract change actually costs (scope boundary; confirmed at the gate)

**This tier requires no consumer change at all, and ships no reader port.**
`internal/analytics/consumed.go` reads `StartBatteryPct` / `EndBatteryPct` off
`telemetry.SuperchargerSession` in two load-bearing places —
`sumSuperchargerPctBetween` (the battery-consumed correction) and
`inferMissingChargingType` (which produces the `charge_gaps` flags tier 5 just moved).
Because this tier leaves telemetry's columns in place (D4), all of that keeps working,
untouched. A reader on `charge_sessions` would have no caller in this tier, so none is
built — the same call tier 5 made for `charge_gaps` ("a read port … still out of scope").
**Confirmed by the owner at the design gate.**

**The follow-up is NOT a one-line ALTER.** Recording its real shape here so nobody
under-estimates it later. Dropping the five percentage columns from `telemetry` requires,
in one change:

1. **A read port on `charging`** — a `SessionReader` with a bounded-window method
   (`SessionsByVehicleBetween(ctx, accountID, teslaID, start, end)`), plus its domain
   type, its query, and its own `DATABASE_URL`-gated tests. This is the reader D9 declines
   to build speculatively.
2. **Re-pointing `sumSuperchargerPctBetween` and `inferMissingChargingType`** off
   `[]telemetry.SuperchargerSession` and onto the new `charging` type. Both are pure
   functions today, so their signatures change and every one of their unit-test fixtures
   changes with them.
3. **Changing `deriveVehicleMetrics`' signature.** It currently takes
   `sessions []telemetry.SuperchargerSession` alongside `snapshots`, `entries` and the
   window. That parameter's type changes, which ripples into `recalculate.go`'s call site
   and into `consumed_test.go`'s fixtures.
4. **Deciding whether analytics still needs `telemetry.SuperchargerSession` at all.**
   *(Revised at the gate — this got easier.)* Before the amendment, energy and cost stayed
   in telemetry, so analytics would have had to read both ports and join them in Go by
   `session_id`. Now that `charge_sessions` carries `energy_kwh`, `total_cost`, `currency`
   and `is_paid` as well as the window and the percentages, the likely outcome is that
   analytics reads **only** `charging` and drops its telemetry-session dependency
   entirely. That has to be confirmed field by field against
   `deriveVehicleMetrics`, but the two-port join is no longer the expected shape — one of
   the concrete benefits the gate's amendment bought.
5. **The `telemetry` migration** dropping the five percentage columns, their five
   `COMMENT ON COLUMN`s and the `COMMENT ON TABLE` text that describes them, plus deleting
   the five fields from `telemetry.SuperchargerSession`, the LOAD-BEARING comment at
   `query.sql:343` and the note at `service.go:805`. Note the follow-up drops **only the
   percentage columns**: `energy_kwh`/`total_cost`/`currency`/`is_paid`/`site_location_name`
   stay in `supercharger_sessions`, where they are collected and refreshed — this tier
   copies them, it does not move them.
6. **Its own expand/contract check** — by then `charge_sessions` is the only copy of the
   percentages, so its correctness has to be established *before* the DROP, exactly as
   tier 3 preceded tier 4. It also makes this change's `-- +goose Down` destructive; see
   the Down block's own note.

Suggested home: a new backlog entry, or a tier appended to RM29 after T7. Either way it is
a full change with its own design gate, not a cleanup.

---

## Database Changes (design gate — full schema, rationale, index plan)

> **This change trips the built-in `database` design gate**, and the owner confirmed it
> with the D1 amendment recorded above. Everything in this section is the confirmed
> version: the complete `CREATE TABLE`, every constraint, the index, the backfill, and the
> goose Up/Down. Nothing is created, altered or dropped outside `internal/charging`.

**File:** `internal/charging/db/migrations/20260823000001_add_charge_sessions.sql`
(version distinct from `telemetry`'s `20260822000001`/`…2` purely for legibility in the
shared `goose_db_version` table; cross-directory version collisions are already tolerated
by design — both `internal/account` and `internal/charging` ship a `20260720000001`.)

### Up

```sql
-- +goose Up
-- charge_sessions: the charging module's record of each Tesla Supercharger charge
-- session — its identity, its time window, the session facts a charging read needs,
-- and the human-owned battery-percentage verification/estimate columns
-- (RM29 tier 6, roadmap D4, MAG-26).
--
-- WHAT THIS TABLE IS. A DENSE MIRROR: one row per Supercharger session, whether or
-- not anyone has verified its battery percentages. That is what lets a date-range
-- read over this table alone return every session with its window, site, energy,
-- cost and any verified percentages, with NO join into internal/telemetry — a join
-- the module boundary (ai/architecture.md §2) forbids anyway. See design.md D2.
--
-- WHAT IT DELIBERATELY DOES NOT CARRY, and this list is CLOSED: country_code,
-- unlatch_date_time, billing_type, vehicle_make_type, raw_data. Those are
-- vendor/provenance detail no declared read has asked for, and every copied field is
-- another field this mirror must keep in step, nightly, forever. raw_data
-- additionally must have exactly ONE home to work as the schema-drift hedge
-- ai/go-conventions.md requires, and that home is supercharger_sessions.raw_data.
-- A column earns a place here by being needed by a charging read; a later migration
-- can add one with a one-line backfill, since the source still exists (design.md D1).
--
-- MUTABILITY — the rule that decides every column's behavior here (design.md D1):
-- a mirrored column gets EXACTLY the write semantics its source column has.
--   * Write-once at the source, therefore write-once here: session_id, account_id,
--     vin, charge_start_date_time, charge_stop_date_time, site_location_name.
--   * Refreshed by telemetry's ON CONFLICT DO UPDATE SET, therefore refreshed by
--     ours: tesla_id, energy_kwh, total_cost, currency, is_paid. These DO drift —
--     Tesla's fees settle after the session ends — and the drift is reconciled by an
--     idempotent re-mirror running in the SAME nightly cycle that refreshed the
--     source (design.md D7), never by merging: this table originates no value.
--   * Never written by the mirror at all: the five battery-percentage columns.
--
-- NAMING: telemetry's column names are used verbatim (energy_kwh, total_cost,
-- currency, site_location_name) rather than this module's own manual_charge_entries
-- vocabulary (energy_added_kwh, price, location_label), so the backfill below stays a
-- literal column-to-column copy checkable by inspection. Accepted cost: this module
-- now names the same concepts two ways, and backlog item 12's convergence will
-- involve a rename (design.md D1).
--
-- NO raw_data JSONB, and that is not an oversight — see the closed exclusion list
-- above. Same reasoning as manual_charge_entries and charge_gaps, neither of which
-- carries raw_data.
--
-- NO FK on account_id or tesla_id: a cross-module FK into the account module's
-- tables would couple charging migrations to the account schema — exactly the
-- coupling ai/architecture.md §2 forbids. Referential integrity is upheld by flow:
-- the only writer receives sessions already scoped to an account and already
-- VIN-resolved by internal/telemetry that same night.
--
-- THIS MIGRATION DROPS NOTHING. internal/telemetry keeps all five percentage
-- columns and every reader of them keeps working (design.md D4/D9). A DROP in this
-- same change would run FIRST — MIGRATIONS_DIRS puts telemetry before charging and
-- goose runs directory by directory — and would destroy the data the backfill
-- below is copying.
CREATE TABLE charge_sessions (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- IDENTITY (mirrored)
    account_id              UUID   NOT NULL,        -- multi-tenant scope
    vin                     TEXT   NOT NULL,        -- durable vehicle key; write-once
    tesla_id                BIGINT,                 -- currently-registered vehicle id; REFRESHED every sync; NULL when the VIN is not a currently-registered vehicle
    session_id              BIGINT NOT NULL,        -- Tesla's session id

    -- THE SESSION'S TIME WINDOW (mirrored; write-once at the source, write-once here)
    charge_start_date_time  TIMESTAMPTZ NOT NULL,
    charge_stop_date_time   TIMESTAMPTZ NOT NULL,

    -- SESSION FACTS (mirrored). site_location_name is write-once at the source, so
    -- it is write-once here. The other four are in telemetry's ON CONFLICT DO UPDATE
    -- SET — they change as fees settle and invoices finalize — so they are refreshed
    -- on every mirror pass. All four are nullable at the source and stay nullable
    -- here: NULL means the session had no kWh fee / no fees at all.
    --
    -- total_cost is a MONETARY amount: no unit suffix (CLAUDE.md's named money
    -- exemption), paired with the currency column instead. Its DOUBLE PRECISION type
    -- is copied from telemetry deliberately, to keep the backfill a literal copy —
    -- but float is a questionable type for money, and converging this table with
    -- manual_charge_entries.price NUMERIC(14,2) (backlog item 12) will have to
    -- reconcile the two types with explicit rounding semantics (design.md D1).
    site_location_name      TEXT   NOT NULL,
    energy_kwh              DOUBLE PRECISION,       -- NULL when the session had no kWh fee
    total_cost              DOUBLE PRECISION,       -- NULL when the session had no fees
    currency                TEXT,                   -- NULL when the session had no fees
    is_paid                 BOOLEAN,                -- NULL when the session had no fees

    -- HUMAN-OWNED VERIFICATION CHANNEL (charging-owned; never written by the sync).
    -- NULL = nothing recorded. No code in this repository writes these as of this
    -- migration: the verification UI is backlog item 11. The sync path has no field
    -- for them at all (design.md D6) — protection by compile error, not by comment.
    start_battery_pct       SMALLINT CHECK (start_battery_pct     BETWEEN 0 AND 100),
    end_battery_pct         SMALLINT CHECK (end_battery_pct       BETWEEN 0 AND 100),
    battery_pct_source      TEXT     CHECK (battery_pct_source IN ('user_verified', 'polled')),

    -- FROZEN, WRITE-ONCE snapshot of whatever estimate was on screen at the moment
    -- the percentages above were verified — a permanent drift log, never refreshed,
    -- never read back as a cache (RM27 design D6, carried over verbatim in intent).
    start_battery_pct_est   SMALLINT CHECK (start_battery_pct_est BETWEEN 0 AND 100),
    end_battery_pct_est     SMALLINT CHECK (end_battery_pct_est   BETWEEN 0 AND 100),

    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),  -- first mirrored; never updated
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),  -- last mirror pass that touched this row (same meaning as supercharger_sessions.updated_at — NOT a "data changed" signal; design.md D6)

    -- Upsert target for the nightly mirror, and the point-lookup index for
    -- "this session". Account-scoped rather than telemetry's global
    -- UNIQUE (session_id): every key in this platform is scoped by tenant, and a
    -- global uniqueness rule lets one bad id from one tenant block another's row.
    CONSTRAINT charge_sessions_account_session_unique UNIQUE (account_id, session_id),

    -- PROVENANCE (design.md D5) — a constraint supercharger_sessions never had: a
    -- recorded percentage must say where it came from. Legal to enforce here and not
    -- there because these five columns are the ONLY ones this module will own the
    -- writes for; everything MIRRORED carries no constraint stricter than
    -- telemetry's own. Hence, deliberately: NO stop >= start CHECK (the source
    -- permits a reversed window), NO total_cost >= 0 or energy_kwh >= 0 CHECK (the
    -- source has neither, and negative fee adjustments happen), NO currency format
    -- constraint. A mirror stricter than its source could not repair a source row it
    -- refuses to accept.
    CONSTRAINT charge_sessions_pct_source_required CHECK (
        (start_battery_pct IS NULL AND end_battery_pct IS NULL)
        OR battery_pct_source IS NOT NULL
    )
);

COMMENT ON TABLE charge_sessions IS
    'Tesla Supercharger charge sessions as owned by internal/charging: identity, the '
    'session time window, the session facts (site, energy, cost, currency, paid state), '
    'and the human-owned battery-percentage verification/estimate columns. Dense — one '
    'row per session, verified or not (design D2). Deliberately carries NO country_code, '
    'unlatch_date_time, billing_type, vehicle_make_type or raw_data (closed list, design '
    'D1). Mirrored from telemetry.supercharger_sessions by the nightly orchestrator '
    'through public ports only, in the same cycle that refreshes the source; each '
    'mirrored column has exactly its source column''s write semantics, so energy_kwh / '
    'total_cost / currency / is_paid / tesla_id are refreshed on every pass and '
    'everything else mirrored is write-once. The sync path can never write the five '
    'percentage columns (design D6). No other module reads this table directly.';

COMMENT ON COLUMN charge_sessions.tesla_id IS
    'Currently-registered vehicle id, refreshed on every sync and set NULL when the '
    'VIN is not a currently-registered vehicle of the account — the same contract '
    'telemetry.supercharger_sessions.tesla_id carries. Resolution is inherited from '
    'telemetry, never recomputed here: internal/charging may not import '
    'internal/account (design D3).';
COMMENT ON COLUMN charge_sessions.site_location_name IS
    'Supercharger site name as Tesla reported it. Write-once: absent from telemetry''s '
    'ON CONFLICT DO UPDATE SET, therefore absent from ours (design D1''s rule).';
COMMENT ON COLUMN charge_sessions.energy_kwh IS
    'kWh delivered, derived by telemetry from the session''s fees. NULL when the '
    'session had no kWh fee. REFRESHED on every mirror pass — telemetry recomputes it '
    'nightly as fees settle.';
COMMENT ON COLUMN charge_sessions.total_cost IS
    'Total charged for the session, in the currency column''s currency. Monetary '
    'amount: no unit suffix by the platform money exemption, paired with currency '
    'instead. NULL when the session had no fees. REFRESHED on every mirror pass. '
    'DOUBLE PRECISION is copied from telemetry to keep the backfill a literal copy; '
    'float is a questionable type for money and converging with '
    'manual_charge_entries.price NUMERIC(14,2) will have to reconcile the two.';
COMMENT ON COLUMN charge_sessions.currency IS
    'ISO 4217 code for total_cost. NULL when the session had no fees. REFRESHED on '
    'every mirror pass.';
COMMENT ON COLUMN charge_sessions.is_paid IS
    'Whether every fee on the session is settled. NULL when the session had no fees. '
    'REFRESHED on every mirror pass — this is the column that most visibly changes '
    'after a session ends.';
COMMENT ON COLUMN charge_sessions.start_battery_pct IS
    'Human-verified battery % at charge start (0-100). NULL = nothing recorded. Never '
    'written by the nightly sync — the sync port has no field for it (design D6).';
COMMENT ON COLUMN charge_sessions.end_battery_pct IS
    'Human-verified battery % at charge end (0-100). Same NULL convention and the same '
    'sync-path protection as start_battery_pct.';
COMMENT ON COLUMN charge_sessions.battery_pct_source IS
    'Provenance of start/end_battery_pct: user_verified (a human entered them) or '
    'polled (a future measured-SOC path, not implemented). Required whenever either '
    'percentage is set (charge_sessions_pct_source_required). Never ''estimated'' — '
    'an estimate is computed on read and is never persisted here.';
COMMENT ON COLUMN charge_sessions.start_battery_pct_est IS
    'FROZEN write-once snapshot of the live estimate at the moment start_battery_pct '
    'was verified — a drift-log entry, not a cache. Never refreshed, including by a '
    'later improved model; staleness here is correct, not a bug.';
COMMENT ON COLUMN charge_sessions.end_battery_pct_est IS
    'FROZEN write-once snapshot of the live estimate at the moment end_battery_pct was '
    'verified. Same write-once, never-refreshed, never-a-cache semantics as '
    'start_battery_pct_est.';

-- Per-vehicle bounded-window read path — the ONE read this table is shaped for:
-- "which sessions finished delivering energy to this vehicle within [start, end]".
-- account_id leads (multi-tenant convention: every dashboard read scopes by account
-- first — ai/go-conventions.md §"Read optimization", ai/architecture.md §7.3);
-- tesla_id second satisfies the second WHERE predicate in the same range scan.
--
-- charge_stop_date_time, NOT charge_start_date_time — deliberately UNLIKE
-- idx_supercharger_sessions_vehicle_time, which leads to start-time. Roadmap D12:
-- energy is fully delivered at session STOP, which is exactly what end_battery_pct
-- corresponds to, so a session belongs to the window containing its stop instant even
-- when it started the day before. telemetry's own
-- SuperchargerSessionsByVehicleBetween already documents that its start-time index
-- does not fully serve that predicate; this table fixes that at the index level.
--
-- ASC (no DESC): the read is an ascending, oldest-first bounded window, so an
-- ascending index satisfies both the range predicate and the ORDER BY with no sort
-- step and no backward scan.
CREATE INDEX idx_charge_sessions_vehicle_stop
    ON charge_sessions (account_id, tesla_id, charge_stop_date_time);

-- One-time backfill of every Supercharger session already collected. A literal
-- column-to-column copy (design.md D1's naming decision exists to make it one) —
-- the only transformation anywhere in it is the battery_pct_source CASE below.
--
-- Guarded and idempotent by construction:
--   * to_regclass guard — returns early in a database where telemetry's migrations
--     were never applied (e.g. internal/charging's own module-scoped test
--     provisioning, whose //go:embed cannot reach ../telemetry). PL/pgSQL plans a
--     statement on first execution, so the INSERT below is never planned when the
--     guard returns first. In a real database the guard always passes: MIGRATIONS_DIRS
--     runs telemetry before charging. The dependency is SOFT — ordering affects data
--     completeness, never migration success (design.md D8a). Verified by probe: sqlc
--     parses this file without ever seeing the supercharger_sessions reference,
--     because a PL/pgSQL body is an opaque string to the SQL parser (design.md D8b).
--   * ON CONFLICT DO NOTHING — re-running this block can never overwrite a row, and
--     in particular can never overwrite a percentage verified after the first run, or
--     a fee figure a later mirror pass has already refreshed.
--
-- battery_pct_source: telemetry allowed a percentage with a NULL source, and exactly
-- one live row is in that state. COALESCE(..., 'user_verified') resolves it, guarded
-- on a percentage actually being present so a row with no percentages keeps its NULL
-- source instead of being handed a fabricated one. 'user_verified' is inference, not
-- invention: nothing in this repository can write these columns (query.sql:343 marks
-- them write-protected, service.go:805 skips them), so a percentage that exists was
-- entered by a human — which is what 'user_verified' denotes (design.md D5).
-- +goose StatementBegin
-- BACKFILL-BEGIN
DO $$
BEGIN
    IF to_regclass('public.supercharger_sessions') IS NULL THEN
        RAISE NOTICE 'charge_sessions backfill skipped: supercharger_sessions is not present in this database';
        RETURN;
    END IF;

    INSERT INTO charge_sessions (
        account_id, vin, tesla_id, session_id,
        charge_start_date_time, charge_stop_date_time,
        site_location_name, energy_kwh, total_cost, currency, is_paid,
        start_battery_pct, end_battery_pct, battery_pct_source,
        start_battery_pct_est, end_battery_pct_est,
        created_at, updated_at
    )
    SELECT
        s.account_id,
        s.vin,
        s.tesla_id,
        s.session_id,
        s.charge_start_date_time,
        s.charge_stop_date_time,
        s.site_location_name,
        s.energy_kwh,
        s.total_cost,
        s.currency,
        s.is_paid,
        s.start_battery_pct,
        s.end_battery_pct,
        CASE
            WHEN s.start_battery_pct IS NOT NULL OR s.end_battery_pct IS NOT NULL
                THEN COALESCE(s.battery_pct_source, 'user_verified')
            ELSE s.battery_pct_source
        END,
        s.start_battery_pct_est,
        s.end_battery_pct_est,
        s.created_at,   -- preserve first-seen; this row is not "new" data
        now()           -- but this mirror row was written now
    FROM supercharger_sessions s
    ON CONFLICT (account_id, session_id) DO NOTHING;
END
$$;
-- BACKFILL-END
-- +goose StatementEnd
```

### Down

```sql
-- +goose Down
-- Safe by construction TODAY: this tier is EXPAND-only (design.md D4), so every row
-- and every column dropped here still exists in telemetry.supercharger_sessions,
-- which this change never touched. Nothing is lost that is not still upstream.
--
-- (This stops being true once the deferred contract change drops telemetry's five
-- percentage columns — at that point charge_sessions is the only copy of them and
-- this Down becomes destructive. That change owns updating this comment; see
-- design.md D9, step 6. The mirrored session facts — site, energy, cost, currency,
-- is_paid — stay in supercharger_sessions permanently, so they are never at risk.)
DROP INDEX IF EXISTS idx_charge_sessions_vehicle_stop;
DROP TABLE IF EXISTS charge_sessions;
```

### The sync query (`internal/charging/db/query.sql`)

```sql
-- name: MirrorChargeSession :exec
-- Upsert one Supercharger session's mirrorable subset. Called once per session, in
-- one transaction, by SessionWriter.MirrorSessions.
--
-- LOAD-BEARING: start_battery_pct, end_battery_pct, battery_pct_source,
-- start_battery_pct_est and end_battery_pct_est are ABSENT from both the INSERT
-- column list and the ON CONFLICT DO UPDATE SET clause. They are human-owned; the
-- nightly sync must never write, clear or overwrite one. Unlike
-- telemetry.UpsertSuperchargerSession — which relies on this comment alone —
-- charging.SessionMirror has no field for them either, so binding one here would not
-- even compile (design.md D6). Do NOT "complete the pattern" by adding them.
--
-- THE REFRESH SET IS NOT A JUDGEMENT CALL. It is telemetry's own ON CONFLICT DO
-- UPDATE SET, minus raw_data (a column this table does not carry): energy_kwh,
-- total_cost, currency, is_paid, tesla_id, updated_at. Everything else mirrored —
-- charge_start_date_time, charge_stop_date_time, site_location_name, created_at — is
-- write-once at the source, so it is write-once here. site_location_name in
-- particular is NOT refreshed because telemetry does not refresh it, not because a
-- site name was judged unlikely to change (design.md D1's rule: a mirrored column
-- gets exactly its source column's write semantics).
--
-- NO WHERE PREDICATE on the DO UPDATE, deliberately (design.md D6). An earlier draft
-- carried WHERE charge_sessions.tesla_id IS DISTINCT FROM EXCLUDED.tesla_id so an
-- unchanged row was not rewritten. With five refreshable columns that predicate would
-- have to name all five, and a future column added to the SET but forgotten in the
-- WHERE would silently stop advancing updated_at, with nothing in this project able
-- to catch it. Telemetry's own upsert has no such predicate either. Consequence:
-- updated_at here means "the last mirror pass touched this row" — exactly what
-- supercharger_sessions.updated_at means — and is NOT a "this row's data changed"
-- signal on either side.
INSERT INTO charge_sessions (
    account_id, vin, tesla_id, session_id,
    charge_start_date_time, charge_stop_date_time,
    site_location_name, energy_kwh, total_cost, currency, is_paid
) VALUES (
    @account_id, @vin, @tesla_id, @session_id,
    @charge_start_date_time, @charge_stop_date_time,
    @site_location_name, @energy_kwh, @total_cost, @currency, @is_paid
)
ON CONFLICT (account_id, session_id) DO UPDATE SET
    energy_kwh = EXCLUDED.energy_kwh,
    total_cost = EXCLUDED.total_cost,
    currency   = EXCLUDED.currency,
    is_paid    = EXCLUDED.is_paid,
    tesla_id   = EXCLUDED.tesla_id,
    updated_at = now();
```

### Index Plan

Declared read patterns for this table, and the object serving each:

| # | Object | Read/write pattern served | Justification |
|---|---|---|---|
| 1 | `charge_sessions_account_session_unique` — the UNIQUE constraint's own index, `(account_id, session_id)` | `MirrorChargeSession`'s `ON CONFLICT` target (write, nightly, once per session); point lookup "this session" | A conflict target must be a unique index; account-scoped because every key in this platform is tenant-scoped (D3). Free — the constraint creates it. |
| 2 | `idx_charge_sessions_vehicle_stop` — `(account_id, tesla_id, charge_stop_date_time)` | The bounded-window per-vehicle read: "sessions for this vehicle whose energy finished landing in `[start, end]`" — the deferred re-point of `sumSuperchargerPctBetween` / `inferMissingChargingType` (D9), and any future verification-UI or charge-history listing | `account_id` leads per the multi-tenant convention (every read scopes by account first). `tesla_id` second satisfies the second equality predicate in the same range scan. `charge_stop_date_time` third turns the window predicate into a range scan on the index's trailing column and matches the ascending ORDER BY, so no sort step. |

**Why stop-time and not start-time.** Roadmap **D12**: energy is fully delivered at
session stop, which is what `end_battery_pct` corresponds to, so a session belongs to the
window containing its stop instant — including a session that spans midnight.
`telemetry`'s equivalent read is served today only by
`idx_supercharger_sessions_vehicle_time (account_id, tesla_id, charge_start_date_time
DESC)`, whose own query doc comment states plainly that it "does NOT fully serve this
query — it is sorted on `charge_start_date_time`, not `charge_stop_date_time`, so the
stop-time predicate cannot be satisfied as a pure index range scan", and that adding a
dedicated stop-time index there was deliberately deferred. This table is the right place
to fix that: it is new, it is small, and the binding `Performance-Profile` explicitly
licenses paying write cost for read performance.

**Deliberately NOT added, and the trigger to revisit each:**

- `(account_id, charge_stop_date_time)` — an account-wide, all-vehicles listing. No such
  read is declared for this table in this tier; index 2's `account_id` prefix already
  prunes to the tenant, leaving only an in-memory sort over one account's sessions.
  Revisit when an account-wide charge-session dashboard exists (it would be the direct
  analogue of `idx_supercharger_sessions_account_time`).
- `(account_id, vin, …)` — VIN-scoped reads. `vin` is the durable key, but every declared
  read is by `tesla_id`; a VIN filter is a cheap residual within index 2's `account_id`
  prefix. Revisit if an orphaned-vehicle (`tesla_id IS NULL`) view is ever built.
- A partial index on "has verified percentages". Speculative — the verification UI
  (backlog item 11) does not exist and its access pattern is unknown.
- Anything on `total_cost`, `energy_kwh` or `site_location_name`. No declared read filters
  or orders by them; they are payload for a row already located by index 2. Revisit if a
  "top charging sites" or spend-ranking read lands (backlog item 8 is the candidate).

**Write cost accepted, and it went up at the gate.** The nightly mirror maintains two
indexes over N sessions per account, and — with the `WHERE` guard dropped (D6) — now
rewrites every mirrored row on every pass rather than only changed ones. Per the binding
`Performance-Profile`, writes run at midnight from a poller and may pay for reads. At the
current 4 rows this is unmeasurable; the documented trigger for revisiting it is the same
one D7 gives for switching to an incremental mirror.

---

## Test Contract (expected values authored before implementation, per `ai/go-conventions.md`)

All tests are `DATABASE_URL`-gated integration tests in `internal/charging`'s existing
external test package (`package charging_test`), which reads rows back with direct SQL
over `testPool` — there is no reader port to assert through (D9). Fixtures use fresh
`uuid.New()` account ids and `session_id`s in the `920001`–`920099` range (collision-free
against real data), except where a test deliberately reproduces the live dataset.

### Group A — the backfill

**A1. The real 4-row dataset backfills to exactly 4 rows, one of them percentage-bearing.**
Seed `supercharger_sessions` with the live shape: **4 rows**, all with
`vin = 'LRWYGCFJ8TC495757'`, `tesla_id = 3744327027802250`, one account id,
`site_location_name = 'Medellín, Colombia'`, and all four carrying non-null `energy_kwh`,
non-null `total_cost`, `currency = 'COP'` and `is_paid = true`. Exactly one of them is
`session_id = 734860294` with `charge_start_date_time = 2026-08-07 18:28:09-05`,
`start_battery_pct = 29`, `end_battery_pct = 100`, `battery_pct_source = NULL`, both
`_est` columns NULL. The other three carry all five percentage columns as NULL. Execute
the extracted backfill block. Expected:

- `charge_sessions` holds exactly **4** rows for that account.
- For each row, all eleven mirrored columns equal the source row's, value for value:
  `session_id`, `vin`, `tesla_id`, `charge_start_date_time`, `charge_stop_date_time`,
  `site_location_name` (`'Medellín, Colombia'` — assert the non-ASCII round-trips),
  `energy_kwh`, `total_cost`, `currency` (`'COP'`), `is_paid` (`true`).
- The row for `session_id = 734860294` has `start_battery_pct = 29`,
  `end_battery_pct = 100`, **`battery_pct_source = 'user_verified'`** (resolved from
  NULL), `start_battery_pct_est` NULL, `end_battery_pct_est` NULL.
- The other three rows have all five percentage columns NULL — **including
  `battery_pct_source`**, which must NOT have been given a fabricated value.
- Every row's `created_at` equals its source row's `created_at`.

**A2. The backfill is idempotent and overwrites nothing on a re-run.** After A1: (a)
`UPDATE charge_sessions SET start_battery_pct = 55, end_battery_pct = 80,
battery_pct_source = 'user_verified' WHERE session_id = <one of the three NULL rows>`,
and (b) `UPDATE charge_sessions SET total_cost = 99999, is_paid = false WHERE session_id
= 734860294` (simulating a fee figure a later mirror pass already refreshed). Execute the
backfill block a second time. Expected: still exactly **4** rows; the row from (a) still
reads `55` / `80` / `'user_verified'`; the row from (b) still reads `99999` / `false`; no
row's `created_at` changed. (`ON CONFLICT DO NOTHING` means the backfill is a one-time
import, never a re-sync — re-syncing is `MirrorChargeSession`'s job.)

**A3. The backfill is a no-op, not an error, when the source table is absent.** Documented
expectation rather than an in-suite test (the suite's own provisioning always applies
telemetry's directory). Verified by the `charging`-only path: applying only
`internal/charging/db/migrations/` to an empty database succeeds and yields an empty
`charge_sessions`.

### Group B — `MirrorSessions`

Baseline fixture **M1** used by B1–B5:
`SessionMirror{VIN: "V920001", TeslaID: &920001, SessionID: 920001,
ChargeStartDateTime: 2026-08-01T10:00:00Z, ChargeStopDateTime: 2026-08-01T10:40:00Z,
SiteLocationName: "Medellín, Colombia", EnergyKWh: &35.5, TotalCost: &48250.0,
Currency: &"COP", IsPaid: &false}`.

**B1. A new session is inserted with exactly the eleven mirrored columns.** Mirror M1.
Expected: exactly 1 row; all eleven values round-trip identically (including the non-ASCII
site name and the `false` `is_paid`); **all five percentage columns are NULL**;
`created_at` and `updated_at` both non-zero.

**B2. Re-mirroring an unchanged session does not duplicate it, and DOES advance
`updated_at`.** Repeat B1's exact call after a ≥10 ms sleep. Expected: still exactly 1
row; every mirrored value unchanged; `created_at` byte-for-byte equal to B1's; and
`updated_at` **strictly later** than B1's. (This asserts D6's dropped-guard decision
directly — the pre-gate design expected `updated_at` to be *unchanged* here. `updated_at`
means "last mirror pass touched this row", matching `supercharger_sessions.updated_at`.)

**B3. Settled fees are refreshed.** After B1, mirror the same `SessionID` with
`EnergyKWh: &36.1`, `TotalCost: &49100.0`, `Currency: &"COP"`, `IsPaid: &true`. Expected:
still 1 row; `energy_kwh = 36.1`, `total_cost = 49100`, `is_paid = true`; `created_at`
unchanged; `updated_at` strictly later.

**B4. Write-once mirrored columns are NOT refreshed.** After B1, mirror the same
`SessionID` with a *different* `SiteLocationName` (`"Bogotá, Colombia"`),
`ChargeStartDateTime` (+1 h) and `ChargeStopDateTime` (+1 h). Expected: still 1 row, and
`site_location_name`, `charge_start_date_time` and `charge_stop_date_time` **all still
hold B1's original values** — they are absent from the conflict clause because they are
absent from telemetry's (D1's rule). This is the test that pins the refresh set.

**B5. `tesla_id` is refreshed, including to NULL.** After B1, mirror the same `SessionID`
with `TeslaID: &920099` → `tesla_id = 920099`. Then mirror again with `TeslaID: nil` →
`tesla_id IS NULL`, `vin` unchanged. `created_at` unchanged throughout; `updated_at`
strictly later after each.

**B6. The sync never touches a verified percentage.** After B1, write percentages
directly: `UPDATE charge_sessions SET start_battery_pct = 41, end_battery_pct = 88,
battery_pct_source = 'user_verified', start_battery_pct_est = 39, end_battery_pct_est = 90
WHERE session_id = 920001`. Then re-mirror that session twice — once unchanged, once with
different `TeslaID` **and** different fee figures. Expected after both: all five values
still `41` / `88` / `'user_verified'` / `39` / `90`, unchanged.

**B7. A mis-scoped entry rejects the WHOLE call, writing nothing.** Call
`MirrorSessions(ctx, accountA, []SessionMirror{ {AccountID: accountA, SessionID: 920010,
…}, {AccountID: accountB, SessionID: 920011, …} })`. Expected: a non-nil error, and
**zero** rows afterwards — under `accountA` *and* under `accountB` — including the entry
that was individually well-formed. (Mirrors `GapWriter.ReconcileWindow`'s all-or-nothing
contract.)

**B8. An empty slice is a successful no-op.** `MirrorSessions(ctx, accountA, nil)` and
`MirrorSessions(ctx, accountA, []SessionMirror{})`. Expected: nil error, zero rows
written, no existing row altered.

**B9. Nullable fee fields round-trip as NULL.** Mirror a session with `EnergyKWh: nil`,
`TotalCost: nil`, `Currency: nil`, `IsPaid: nil` (a session Tesla reported with no fees).
Expected: 1 row with all four columns NULL, and `site_location_name` still NOT NULL.

**B10. Two accounts may hold the same `session_id`.** Mirror `SessionID: 920020` under
account A and under account B. Expected: 2 rows, one per account, each with its own
`account_id`; neither call errors. (This is what account-scoped uniqueness buys over
telemetry's global `UNIQUE (session_id)`.)

**B11. Two vehicles in one account are independent.** Within one account mirror
`SessionID: 920030` (`tesla_id 920030`) and `SessionID: 920031` (`tesla_id 920031`) in one
call. Expected: 2 rows; re-mirroring only the first leaves the second's `updated_at`
byte-for-byte unchanged (the second session is not in the second call's statement at all).

### Group C — constraints

**C1. Provenance is required.** Direct `INSERT` of a row with `start_battery_pct = 50`
and `battery_pct_source = NULL` → violates `charge_sessions_pct_source_required`, error.
Same with `end_battery_pct = 50`. Same with both set.

**C2. A row with no percentages may have a NULL source.** Direct `INSERT` with all five
percentage columns NULL → succeeds. (The complement of C1 — the CHECK must not require a
source on an unverified row, which is every row the sync writes.)

**C3. Percentage range.** Direct `INSERT` with `start_battery_pct = 101` (source set) →
error; `-1` → error; `0` and `100` → succeed.

**C4. Provenance enum.** Direct `INSERT` with `battery_pct_source = 'estimated'` → error;
`'user_verified'` and `'polled'` → succeed.

**C5. Account-scoped uniqueness is enforced.** Two direct `INSERT`s with the same
`(account_id, session_id)` → the second violates
`charge_sessions_account_session_unique`.

**C6. `site_location_name` is NOT NULL; the fee columns are nullable.** Direct `INSERT`
omitting `site_location_name` → error. Direct `INSERT` with `energy_kwh`, `total_cost`,
`currency` and `is_paid` all omitted → succeeds.

**C7. No constraint stricter than the source exists.** Direct `INSERT` with
`charge_stop_date_time` *before* `charge_start_date_time`, and one with a negative
`total_cost` → both **succeed**. This pins D5's deliberate omissions so a future
"hardening" pass has to argue with a failing test rather than quietly adding a CHECK the
mirror cannot honour.

### What must NOT change

- `internal/charging`'s existing `manual_charge_entries` tests: not one assertion, fixture
  or name. The provisioning switch (D8) must leave every existing test passing unchanged.
- `internal/telemetry`'s tests: this change does not touch that module at all.

---

## Risks / Trade-offs

- **Five columns are now genuinely duplicated across a module boundary and genuinely
  drift** (D1, post-gate). The mitigation is structural — same-cycle reconciliation, an
  idempotent total re-mirror, and a mirror that originates no value — but it is a
  mitigation, not a guarantee. Concretely: if the nightly poller stops running, or its
  mirror step fails for an account repeatedly, `charge_sessions`' fee figures go stale
  silently, with nothing surfacing the divergence. No consumer reads them this tier
  (D9), so the blast radius today is zero; the deferred re-point is where a staleness
  signal (or a reconciliation report) has to be designed.
- **`total_cost` is `DOUBLE PRECISION`** — copied deliberately (D1), but float money
  accumulates error under summation, and this module's other money column is `NUMERIC`.
  Flagged for a backlog entry; not fixed here.
- **The module now speaks two vocabularies** (`energy_kwh` vs `energy_added_kwh`,
  `total_cost` vs `price`, `site_location_name` vs `location_label`). Accepted at the gate
  to keep the backfill literal; it makes backlog item 12 a rename as well as a merge, and
  it is a standing readability cost on every future agent that opens this module.
- **`internal/charging`'s test provisioning gains a dependency on `internal/telemetry`'s
  migration directory** (D8c). Sanctioned by `ai/go-conventions.md` and already done by
  `internal/analytics`, and it is a *path*, not an import — but a breaking change to
  telemetry's migrations can now fail charging's suite. Recorded in
  `internal/charging/AGENTS.md` so the next agent is not surprised.
- **The backfill's correctness on the *real* database is confirmed by the owner, not by
  the suite.** The suite proves the statement's semantics against a reproduction of the
  live 4-row dataset (A1/A2); only `make migrate-up` on the real database proves the real
  rows moved. The owner's verification step is in §"Verification signals".
- **`cmd/poller` has no tests of its own** and its wiring is invisible to `go build`,
  `go vet` and the suite alike — RM29 tier 3's review caught exactly this (a `Reconcile`
  call site that was never written while every signal stayed green). The mirror step must
  be *read*, not trusted, at review, and the owner's `cmd/poller --once` run is the
  acceptance signal for it.
- **Resolved, no longer a risk:** whether sqlc tolerates the `DO` block. Verified by probe
  before Apply (D8b) — `make sqlc` exits 0 and generates a correct model with no leak.

## Verification signals

Per the binding `Test-Execution-Policy`, the assistant runs and reports: `go build ./...`,
`go vet ./...`, `gofmt -l .`, `make build`, `make vet`, `make bins`, `make sqlc` (after the
migration and after `query.sql`), and the three standalone guards — `make ui-guard`
(no-op: no gateway markup), `make i18n-guard` (no-op: no user-facing string),
`make money-guard` (no-op: it greps gateway handlers for a hand-rolled
`fmt.Sprintf("%.Nf %s", …)` money string, and this change touches no gateway file). It
also runs `openspec validate --changes --strict`.

The owner alone runs the suite and the database steps:

```
make migrate-up
make migrate-status          # every directory clean, the new charging migration Applied
go test ./internal/charging/... ./cmd/...
```

or the full suite:

```
go test ./...
```

and, because `cmd/poller` has no tests:

```
go run ./cmd/poller --once
```

Then, to confirm the backfill against the real data (expected: 4 rows, all
`site_location_name = 'Medellín, Colombia'` / `currency = 'COP'` / `is_paid = true` with
non-null `energy_kwh` and `total_cost`, and `734860294` carrying `29` / `100` /
`user_verified`):

```
psql "$DATABASE_URL" -c "SELECT session_id, vin, tesla_id, charge_start_date_time, charge_stop_date_time, site_location_name, energy_kwh, total_cost, currency, is_paid, start_battery_pct, end_battery_pct, battery_pct_source FROM charge_sessions ORDER BY charge_stop_date_time;"
```

Until the owner runs these and reports the results, this tier's implementation status is
**awaiting-user-verification**, never "done".
