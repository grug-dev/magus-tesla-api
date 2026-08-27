Source: MAG-30 — https://linear.app/magus-monitor/issue/MAG-30/supercharger-stats-reads
Roadmap: openspec/roadmaps/RM30-supercharger-stats-read-from-charging.md
Tier: 1 of 2 (`charging` gains a `SessionReader`; tier 2 is
`RM30-gateway-read-supercharger-stats-from-charging`, which swaps `/supercharger-stats`
onto it). Tier 1 has no dependency; tier 2 depends on tier 1.
Design gate: **not tripped.** This change creates or alters no database object — no
migration, no table, no column, no index, no constraint (roadmap D1: the owner chose to
drop `country_code`/`billing_type` from the page rather than add them to `charge_sessions`,
which is what keeps this tier schema-free). `openspec/config.yaml`'s blanket "design.md is
REQUIRED for any DB-touching change" still applies — this change adds a new read query — so
design.md documents the full read-path and index analysis below, without requiring the
owner's gate confirmation a schema change would need.
Unit tests: **integration-only**, matching RM29 tier 6's precedent for this same module.
The only new Go is a DB-boundary reader (`internal/charging/session_reader.go`) and its
row-mapping helpers, neither of which has a testable pure function; `Session`'s zero
value-receiver methods (unlike `Entry`'s `CostPerKWh`/`BatteryDelta`) are not part of this
tier's scope. The whole test surface is the `DATABASE_URL`-gated suite, whose expected
values are authored up front in design.md §Test Contract (`ai/go-conventions.md` §Testing).

## Why

RM29 tier 6 gave `internal/charging` a dense, one-row-per-session mirror of every
Supercharger charge session (`charge_sessions`) — including the human-verified
battery-percentage columns `internal/telemetry` can never write — but shipped **no reader**
for it, because no consumer needed one yet (that tier's design.md D9: "a reader on
`charge_sessions` would have no caller in this tier, so none is built"). The gateway's
`/supercharger-stats` page still reads Supercharger sessions from
`internal/telemetry.SuperchargerReader` over `supercharger_sessions` — the raw ingestion
buffer `charging` mirrors *from*, not the record `charging` owns.

MAG-30 names the consumer: `/supercharger-stats` should read the module that **owns**
charge-session data, through that module's own port, not another module's collection
buffer (`ai/architecture.md` §2's "no cross-module database leaks", applied at the
port-selection level, not just literal SQL). `charging.SessionReader` is the port that
makes that swap possible; this tier builds only the port. The swap itself — rewiring
`internal/gateway`'s `Deps.SuperchargerReader` and dropping the `CountryCode`/`BillingType`
columns `charge_sessions` never carried (roadmap D1) — is tier 2, a separate `gateway`
module change, per `openspec/config.yaml` §proposal's rule against one change spanning
two modules.

## What Changes

**Additive only — no schema change.**

- **ADDED** — `charging.Session`, the domain type for one full `charge_sessions` row
  (identity, time window, session facts, and the five charging-owned battery-percentage
  columns). Distinct from `charging.SessionMirror`: `SessionMirror` is deliberately the
  write-side mirrorable subset with **no** battery fields (RM29 design.md D6) — `Session`
  is not a superset built by adding fields to `SessionMirror`, it is a separate type, so
  the write path's compile-time protection is untouched (design.md D4).
- **ADDED** — `charging.SessionReader`, a one-method read port:
  `ListSessionsByVehicleBetween(ctx, accountID, teslaID, from, to) ([]Session, error)`,
  windowed on `charge_stop_date_time` and mirroring `Reader.ListEntriesByVehicleBetween`'s
  inclusive-both-bounds / no-limit / non-nil-empty-slice contract (design.md D1/D3).
- **ADDED** — `charging.NewSessionReader(pool *pgxpool.Pool) SessionReader`, declared in
  `charging.go`, implemented in the new `internal/charging/session_reader.go` (mirroring
  how `NewSessionWriter`/`session_writer.go` are split from `charging.go`/`service.go`).
- **ADDED** — `chargingdb.ListSessionsByVehicleBetween`, one new sqlc query in
  `internal/charging/db/query.sql`, regenerated via `make sqlc`. No migration accompanies
  it — the existing `idx_charge_sessions_vehicle_stop` already serves it (design.md
  §"Database Changes").
- **CHANGED** — `internal/charging/session_writer.go` gains the reverse pgtype→domain
  helpers this read path needs (`pgInt8ToInt64Ptr`, `pgFloat8ToFloat64Ptr`,
  `pgBoolToBoolPtr`), placed next to their existing forward pairs
  (`int64PtrToPgInt8`, `float64PtrToPgFloat8`, `boolPtrToPgBool`), and each of those three
  forward helpers' "no reverse pair exists — this module exposes no reader" doc-comment
  sentence is corrected, because it becomes false the moment this tier lands (design.md
  D6).
- **CHANGED** — `internal/charging/AGENTS.md` — corrects the now-stale "`charge_sessions`
  has no reader port in this tier; no consumer needs one yet (design.md D9)" line (§Public
  Interface) and the matching sentence under §Data Ownership → `charge_sessions`, and
  documents the new port (docs-track-structural-change, `CLAUDE.md` §Non-negotiables).
- **UNCHANGED** — the migration file, `SessionMirror`, `SessionWriter`,
  `MirrorChargeSession`, `manual_charge_entries`, and every file outside
  `internal/charging/`. No column, index, constraint, port or type is removed anywhere.

**Breaking:** no. Purely additive — a new type and a new interface method with no existing
caller yet. `SessionMirror` gains no field; the write path's five-percentage-column
protection is unaffected.

**Modules affected:** `charging` only (owns everything new). No other module's code
changes in this tier. **`cmd/web` wiring** (constructing `charging.NewSessionReader(pool)`
and injecting it somewhere) is explicitly **not** part of this tier or any tier — it has no
caller until tier 2's gateway swap exists, so it is deferred to that change's own
leader-owned integration step, not created speculatively here.

## Read paths affected

Per `openspec/config.yaml` §proposal ("performance-sensitive proposals must name the read
paths they affect"):

- **One read path is added, with no caller yet.** `ListSessionsByVehicleBetween` is new,
  but nothing in this tier calls it — tier 2 is the consumer. It reuses
  `idx_charge_sessions_vehicle_stop (account_id, tesla_id, charge_stop_date_time)` — the
  index RM29 tier 6 already built and left unused pending exactly this read (that tier's
  design.md §"Index Plan": *"the deferred re-point of `sumSuperchargerPctBetween` /
  `inferMissingChargingType`... and any future verification-UI or charge-history
  listing"*). No new index, no schema change (design.md §"Database Changes").
- **No existing read path changes.** `telemetry.SuperchargerReader` (still read by the
  gateway until tier 2), `charging.Reader` (`manual_charge_entries`), and
  `charging.SessionWriter`'s nightly mirror are all untouched — their query plans, indexes
  and results are identical before and after this change.

## Impact

- **Affected specs:** `charge-session-log` (existing capability; **ADDED** requirement for
  the new retrieval behavior only — nothing about the existing write/synchronization
  requirements changes).
- **Affected code:** `internal/charging/` (`charging.go`, `session_reader.go` (new),
  `session_writer.go`, `db/query.sql`, a new `db_session_reader_integration_test.go`,
  `AGENTS.md`).
- **Design gate:** not tripped — see header. design.md still carries the query, the index
  justification against the existing object, and the Test Contract, per
  `openspec/config.yaml`'s blanket design.md requirement for any DB-touching change.
- **Deferred, explicitly NOT in scope:** the gateway swap and the `CountryCode`/
  `BillingType` column removal from the page (tier 2, roadmap D1); `cmd/web` wiring
  (no caller until tier 2); any change to `SessionMirror`, `SessionWriter`, or the
  migration file.
