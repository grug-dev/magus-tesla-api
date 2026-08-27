# Tasks — RM30-charging-add-session-read-port

Ownership legend: **[module: charging worker]** — inside `internal/charging/` only. This
change has **no leader-owned task**: it ships no migration, touches no `cmd/`, and its
docs live entirely inside `internal/charging/AGENTS.md` (root `README.md`'s existing
charging description already covers `charge_sessions` accurately and needs no edit — see
"Not in this change" below). See design.md D1–D7 for the rationale behind each group.

**Ordering constraints:**

- Wave 1 (query) before Wave 2 (Go): `sqlc` generates
  `chargingdb.ListSessionsByVehicleBetweenParams`/`ChargeSession` handling from
  `query.sql` validated against the existing migration directory, so the query must exist
  first. No migration is added or changed in this wave — design.md D2/§"Database Changes"
  already establishes the existing `idx_charge_sessions_vehicle_stop` serves it.
- Wave 2 before Wave 3 (tests): the `DATABASE_URL`-gated tests cannot compile until
  `charging.Session`/`SessionReader` exist (`ai/go-conventions.md` §Testing authoring
  order — their expected values are already fixed in design.md §Test Contract).
- `make sqlc` runs once, after task 1.1.

---

## Wave 1 — query (module: charging worker)

- [x] **1.1** **[module: charging worker]** `internal/charging/db/query.sql` — append
  `-- name: ListSessionsByVehicleBetween :many` exactly as specified in design.md
  §"The query" (revised), including its full doc comment. The query is a **half-open
  range**, not `BETWEEN`: `charge_stop_date_time >= @from_time AND charge_stop_date_time
  < @end_bound`, with `@end_bound` bound by the caller to `to.AddDate(0, 0, 1)` computed
  in Go (design.md **D5** — do not compute the +1-day translation in SQL). The
  index-column-mapping rationale, the half-open-vs-`BETWEEN` scan-shape note, the
  `tesla_id = @tesla_id` nullable-exclusion note, and the D1/D3/D5/D6 cross-references are
  part of the deliverable, not decoration — do not shorten them. Do **not** add or edit
  any migration file. Run `make sqlc` and report the result — this generates
  `chargingdb.ListSessionsByVehicleBetweenParams` (fields `AccountID`, `TeslaID`,
  `FromTime`, `EndBound` — **not** `ToTime`, since the query never binds `to` itself) and
  the `ListSessionsByVehicleBetween` method against the *existing* `ChargeSession` model
  (already generated from the table's schema by RM29 tier 6, before this tier's query even
  existed — confirm the model reappears unchanged in the diff, since no migration
  changed).
  `depends_on`: — · `parallel_ok`: no (blocks everything)

---

## Wave 2 — the Go port (module: charging worker)

- [x] **2.1** **[module: charging worker]** `internal/charging/charging.go` — add the
  `Session` domain type (19 fields, exactly as specified in design.md **D4**, including
  its doc comment explaining why it is a new type and not `SessionMirror` widened), the
  `SessionReader` interface (one method, `ListSessionsByVehicleBetween`, with a doc
  comment that states plainly: `from` and `to` are **whole UTC calendar days**, `to`
  **inclusive of its entire day** — mirroring `telemetry.SuperchargerSessionsByVehicleBetween`'s
  `end.AddDate(0,0,1)`/half-open contract exactly, **not** `ListEntriesByVehicleBetween`'s
  exact-value `BETWEEN` (design.md **D5**, revised) — ordered **ascending** by
  `ChargeStopDateTime`, matching `telemetry`'s own ordering (design.md **D3**, strengthened) —
  call out explicitly that the ASC-vs-`Reader`'s-DESC divergence is deliberate and must
  not be "corrected" to match `Reader` — no limit parameter, non-nil empty slice on no
  match, and that a session whose `tesla_id` is absent is never returned by any vehicle id
  (design.md **D6**)), and the forward-declaring constructor
  `func NewSessionReader(pool *pgxpool.Pool) SessionReader`. Follow this file's existing
  conventions: no `pgtype` anywhere in it, `*T` for optional values, doc comments on every
  exported symbol. Does not compile until 2.2 supplies the constructor's body.
  `depends_on`: 1.1 · `parallel_ok`: with 2.2 (authoring only — they land together)

- [x] **2.2** **[module: charging worker]** `internal/charging/session_reader.go` (new
  file) — implement the port, mirroring `session_writer.go`'s shape exactly (design.md
  **D7**): an unexported `sessionReader` struct over `*pgxpool.Pool` + `*chargingdb.Queries`,
  an unexported `newSessionReader`, the compile-time
  `var _ SessionReader = (*sessionReader)(nil)` assertion, and `ListSessionsByVehicleBetween`
  computing `endBound := to.AddDate(0, 0, 1)` **in Go** (design.md D5 — do not push this
  arithmetic into SQL) before building
  `chargingdb.ListSessionsByVehicleBetweenParams{AccountID, TeslaID: teslaIDToPgInt8(teslaID),
  FromTime: pgtype.Timestamptz{Time: from, Valid: true}, EndBound:
  pgtype.Timestamptz{Time: endBound, Valid: true}}` (mapping `teslaID int64` via the new
  `teslaIDToPgInt8` helper — design.md **D6**), and a `rowToSession` mapper converting each
  `chargingdb.ChargeSession` row to `Session`. Reuse `service.go`'s existing `pgTextToPtr`
  and `pgInt2ToIntPtr` for `Currency` and the four `SMALLINT` percentage columns — do not
  duplicate them locally.
  `depends_on`: 2.1 · `parallel_ok`: with 2.1

- [x] **2.3** **[module: charging worker]** `internal/charging/session_writer.go` — add
  the three reverse pgtype→domain helpers design.md **D6** specifies
  (`pgInt8ToInt64Ptr`, `pgFloat8ToFloat64Ptr`, `pgBoolToBoolPtr`), each placed
  immediately after its existing forward pair (`int64PtrToPgInt8`, `float64PtrToPgFloat8`,
  `boolPtrToPgBool`). In the same edit, **delete** the now-false trailing sentence on each
  of those three forward helpers' doc comments ("no reverse pair exists — this module
  exposes no reader for `charge_sessions`") and replace it with a one-line pointer to the
  new reverse helper it now has. Do not touch `MirrorSessions` or any other method's
  behavior in this file.
  `depends_on`: 2.1 · `parallel_ok`: with 2.2 (both edit shared package state but disjoint
  file regions — 2.2 adds `session_reader.go`, 2.3 edits `session_writer.go`; land 2.3
  before or alongside 2.2 since 2.2's `rowToSession` calls the helpers 2.3 adds)

---

## Wave 3 — tests + module docs (module: charging worker)

- [x] **3.1** **[module: charging worker]** `internal/charging/db_session_reader_integration_test.go`
  (new file, `package charging_test`) — implement Test Contract **T1–T11** exactly as
  design.md states them (revised: T1–T4 are the day-boundary/half-open cases), with those
  expected values. Seed the baseline fixture **S1** (four sessions, `session_id`s
  **940001–940004**, one of them — 940004 — deliberately outside the `[from, to)` window)
  via `SessionWriter.MirrorSessions`, then write the battery percentages onto 940002
  directly by SQL (mirroring
  `db_session_integration_test.go`'s existing pattern of direct-SQL writes for the
  human-owned columns). Add whatever small helpers this file needs
  (e.g. `fetchSessionsByVehicleBetween` wrapping the port call) as this file's own test
  helpers. Assert against `charging.Session` domain fields only — **no `pgtype` in any
  assertion** (`internal/charging/AGENTS.md` §Testing Notes). Use fresh `uuid.New()`
  account ids per test group (T6/T7 need a second account/vehicle; do not reuse S1's ids
  for the isolation fixtures).
  `depends_on`: 2.2, 2.3 · `parallel_ok`: with 3.2

- [x] **3.2** **[module: charging worker]** `internal/charging/AGENTS.md` — update for the
  module's new read surface (docs-track-structural-change, `CLAUDE.md` §Non-negotiables):
  - §Public Interface — add the `Session` type and `SessionReader`/`NewSessionReader`
    block (mirroring how `SessionMirror`/`SessionWriter`/`NewSessionWriter` are already
    documented there), stating plainly that `Session` and `SessionMirror` are distinct
    types and why (design.md D4).
  - §Data Ownership → `charge_sessions` — delete "there is no reader port in this tier
    (design.md D9)" (that D9 was RM29 tier 6's, and this tier supersedes it) and replace
    with the new port's existence, citing this change.
  - §Testing Notes — note that `charge_sessions` now has a read-port test file
    (`db_session_reader_integration_test.go`) alongside the existing
    `db_session_integration_test.go`/`db_backfill_integration_test.go`, and that reads
    still assert only against `charging.Session`/direct SQL, never `pgtype`.
  `depends_on`: 2.2 · `parallel_ok`: with 3.1

---

## Not in this change — do not do these

Listed so no worker "completes the pattern" and so review can reject them fast:

- **Any migration file, new or edited.** design.md D2 established the existing index
  already serves this query; adding one anyway would trip the `database` design gate
  without the owner's sign-off.
- **Any edit to `SessionMirror`, `SessionWriter`, or `MirrorChargeSession`.** The write
  path is untouched by this tier.
- **Wiring `charging.NewSessionReader` into `cmd/web` or `internal/gateway`.** That is
  tier 2's leader-owned integration step (`RM30-gateway-read-supercharger-stats-from-charging`)
  — this port has no caller until that change lands, and that is expected, not a gap.
- **Root `README.md` edits.** Its existing charging-module description already covers
  `charge_sessions` without claiming "write-only" or "no reader" anywhere, so nothing in
  it goes stale from this change.
- **Value-receiver derived methods on `Session`** (e.g. a `BatteryDelta` mirroring
  `Entry`'s). Not asked for; out of this tier's declared scope.
- **Dropping `CountryCode`/`BillingType` from anything, or any gateway template change.**
  That is tier 2's scope (roadmap D1), not this module's.
