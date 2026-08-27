# Tasks — RM31-charging-add-session-verification-port

Ownership legend: **[module: charging worker]** — inside `internal/charging/` only. This
change has **no leader-owned task**: it ships no migration, touches no `cmd/`, and its
docs live entirely inside `internal/charging/AGENTS.md`. See design.md D1–D11 for the
rationale behind each group.

**Ordering constraints:**

- Wave 1 (query) before Wave 2 (Go): `sqlc` generates
  `chargingdb.VerifyChargeSessionParams`/regenerates `ChargeSession` handling from
  `query.sql` validated against the existing migration directory, so the query must exist
  first. No migration is added or changed in this wave — design.md D11/§"Database Changes"
  already establishes the table's own primary key serves it.
- Wave 2 before Wave 3 (tests): the `DATABASE_URL`-gated tests cannot compile until
  `charging.SessionVerifier`/`session_verifier.go` exist (`ai/go-conventions.md` §Testing
  authoring order — their expected values are already fixed in design.md §Test Contract).
- `make sqlc` runs once, after task 1.1.

---

## Wave 1 — query (module: charging worker)

- [x] **1.1** **[module: charging worker]** `internal/charging/db/query.sql` — append
  `-- name: VerifyChargeSession :one` exactly as specified in design.md §"The query",
  including its full doc comment. The `SET` clause touches **exactly** `start_battery_pct`,
  `end_battery_pct`, `battery_pct_source`, and `updated_at` — no other column, including
  `start_battery_pct_est`/`end_battery_pct_est`, may appear in it (design.md **D1** — this
  is the deliverable's central invariant, not a detail to trim for brevity). `WHERE id =
  @id AND account_id = @account_id`, `RETURNING *`. Do **not** add or edit any migration
  file. Run `make sqlc` and report the result — this generates
  `chargingdb.VerifyChargeSessionParams` (fields `ID`, `AccountID`, `StartBatteryPct`,
  `EndBatteryPct`, `BatteryPctSource`) and the `VerifyChargeSession` method against the
  *existing* `ChargeSession` model (already generated from the table's schema by RM29 tier
  6 — confirm it reappears unchanged in the diff, since no migration changed).
  `depends_on`: — · `parallel_ok`: no (blocks everything)

---

## Wave 2 — the Go port (module: charging worker)

- [x] **2.1** **[module: charging worker]** `internal/charging/charging.go` — add the
  `SessionVerifier` interface (one method, `VerifySession(ctx context.Context, accountID
  uuid.UUID, id uuid.UUID, startBatteryPct, endBatteryPct *int) (Session, error)`, with a
  doc comment stating plainly and in full: exactly which three columns plus `updated_at`
  the underlying query touches and that no other column, including the two `_est`
  columns, is reachable (design.md **D1**); that `battery_pct_source` is always computed
  by the port — `"user_verified"` when either percentage is non-nil, `NULL` when both are
  nil — and that the method takes no source parameter so `"polled"` cannot be written
  (design.md **D2**/**D7**); that a partial call (one percentage non-nil, the other nil)
  is legal and every call supplies both parameters' final values, not a delta (design.md
  **D6**); that each non-nil percentage is validated to `[0, 100]` before the query runs
  (design.md **D3**); that no ordering between the two percentages is enforced, and this
  is deliberate (design.md **D8**); and that `id`/`accountID` scope the update exactly
  like `Writer.Update`, with unknown-id and wrong-account indistinguishable (design.md
  **D5**)), and the forward-declaring constructor
  `func NewSessionVerifier(pool *pgxpool.Pool) SessionVerifier`. Follow this file's
  existing conventions: no `pgtype` anywhere in it, doc comments on every exported symbol.
  Does not compile until 2.2 supplies the constructor's body.
  `depends_on`: 1.1 · `parallel_ok`: with 2.2 (authoring only — they land together)

- [x] **2.2** **[module: charging worker]** `internal/charging/session_verifier.go` (new
  file) — implement the port, mirroring `session_writer.go`/`session_reader.go`'s shape
  exactly (design.md **D9**): an unexported `sessionVerifier` struct over `*pgxpool.Pool` +
  `*chargingdb.Queries`, an unexported `newSessionVerifier`, the compile-time `var _
  SessionVerifier = (*sessionVerifier)(nil)` assertion, the package-level constant
  `batteryPctSourceUserVerified = "user_verified"` (design.md **D10**), and
  `VerifySession` implementing exactly the range-validation-then-query shape in design.md
  §"Go-side call shape": validate `startBatteryPct`/`endBatteryPct` against `[0, 100]`
  first (returning a `fmt.Errorf("charging: start_battery_pct %d out of range [0,100]",
  ...)`-shaped error, naming the field and value, before any database call — design.md
  **D3**), compute `source` per **D2**/**D7**, build
  `chargingdb.VerifyChargeSessionParams` reusing `intPtrToPgInt2` and `stringPtrToPgText`
  (both already in `service.go` — do not duplicate them locally), call
  `v.q.VerifyChargeSession`, wrap any error as `fmt.Errorf("charging: verify session:
  %w", err)` with no `NotFound` special-casing (design.md **D5**), and map the returned
  row via the existing `rowToSession` (`session_reader.go` — do not write a second mapper).
  `depends_on`: 2.1 · `parallel_ok`: with 2.1

---

## Wave 3 — tests + module docs (module: charging worker)

- [ ] **3.1** **[module: charging worker]**
  `internal/charging/db_session_verifier_integration_test.go` (new file, `package
  charging_test`) — implement Test Contract **T1–T9** exactly as design.md states them,
  with those expected values. Seed each test's baseline session via
  `SessionWriter.MirrorSessions`, using fresh `uuid.New()` account ids and `session_id`s
  in the **950001–950099** range (disjoint from RM29 tier 6's 920001–920099, RM30 tier
  1's 940001–940099, and the real backfilled `734860294`). T8 in particular must capture
  every column via direct SQL both before and after the call and assert bit-identical
  equality on every column except the three target columns and `updated_at` — this is the
  test that proves design.md D1's structural claim, not just that the query text looks
  right. Assert against `charging.Session` domain fields and direct SQL column values
  only — **no `pgtype` in any assertion** (`internal/charging/AGENTS.md` §Testing Notes).
  For T7, assert `errors.Is(err, pgx.ErrNoRows)` after unwrapping, matching T6's error
  shape exactly.
  `depends_on`: 2.2 · `parallel_ok`: with 3.2

- [ ] **3.2** **[module: charging worker]** `internal/charging/AGENTS.md` — update for the
  module's new write surface (docs-track-structural-change, `CLAUDE.md` §Non-negotiables):
  - §Public Interface — add the `SessionVerifier`/`NewSessionVerifier` block (mirroring
    how `SessionWriter`/`NewSessionWriter` and `SessionReader`/`NewSessionReader` are
    already documented there), stating why it is a separate interface from `SessionWriter`
    rather than a method added to it (design.md D9).
  - §Data Ownership → `charge_sessions` — update the column-by-column list: the five
    charging-owned columns are no longer universally "never written by anything in this
    repository" — three of them (`start_battery_pct`, `end_battery_pct`,
    `battery_pct_source`) are now writable through `SessionVerifier`, while
    `start_battery_pct_est`/`end_battery_pct_est` remain unwritable by any code path
    (roadmap Decision 3 — no estimator exists).
  - §Testing Notes — note that `charge_sessions` now has a verification-port test file
    (`db_session_verifier_integration_test.go`) alongside the existing
    `db_session_integration_test.go`/`db_backfill_integration_test.go`/
    `db_session_reader_integration_test.go`, and that its reads still assert only against
    `charging.Session`/direct SQL, never `pgtype`.
  `depends_on`: 2.2 · `parallel_ok`: with 3.1

---

## Not in this change — do not do these

Listed so no worker "completes the pattern" and so review can reject them fast:

- **Any migration file, new or edited.** design.md D11 established the existing primary
  key already serves this query; adding an index or column anyway would trip the
  `database` design gate without the owner's sign-off.
- **Writing `start_battery_pct_est` or `end_battery_pct_est` from any code path.**
  Roadmap Decision 3: no estimator exists; fabricating a frozen snapshot value would be a
  fictitious drift log entry.
- **A `source` parameter on `VerifySession`, or any way for a caller to write
  `"polled"`.** Design.md D2 is explicit the method takes none.
- **Enforcing `start_battery_pct <= end_battery_pct` or any other cross-field ordering.**
  Design.md D8 decided against it, deliberately, matching the table's own schema.
- **Adding a method to `SessionWriter` instead of a new interface.** Design.md D9's whole
  point is keeping the nightly-sync surface and the human-write surface on separate types.
- **Wiring `charging.NewSessionVerifier` into `cmd/web` or `internal/gateway`.** That is
  tier 4's leader-owned integration step
  (`RM31-gateway-add-session-battery-edit`) — this port has no caller until that change
  lands, and that is expected, not a gap.
- **Any edit to `SessionMirror`, `SessionWriter`, `MirrorChargeSession`, or
  `SessionReader`.** The nightly sync path and the existing read path are untouched by
  this tier.
- **Root `README.md` edits.** Its existing charging-module description already covers
  `charge_sessions` without claiming the verification columns are unwritable, so nothing
  in it goes stale from this change.
- **Any `analytics` or `gateway` code.** Those are tiers 2–4's scope, not this module's.
