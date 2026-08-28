# Tasks — RM31-charging-add-session-read-ports

Ownership legend: **[module: charging worker]** — inside `internal/charging/` only. This
change has **no leader-owned task**: it ships no migration, touches no `cmd/`, and its docs
live entirely inside `internal/charging/AGENTS.md`. See design.md D1–D7 for the rationale
behind each group.

**Ordering constraints:**

- Wave 1 (queries) before Wave 2 (Go): `sqlc` generates
  `chargingdb.ListSessionsByVehicleUpdatedSinceParams`/`ListSessionsByVehicleParams` from
  `query.sql` validated against the existing migration directory, so both queries must exist
  first. No migration is added or changed in this wave — design.md D2/§"Database Changes"
  already establishes the existing `idx_charge_sessions_vehicle_stop` serves both.
- Wave 2 before Wave 3 (tests): the `DATABASE_URL`-gated integration tests cannot compile
  until both new `SessionReader` methods exist (`ai/go-conventions.md` §Testing authoring
  order — their expected values are already fixed in design.md §Test Contract). There is no
  pure/offline-testable unit in this change (design.md D7), so there is no early test wave —
  every test in this change belongs in the final wave.
- `make sqlc` runs once, after task 1.1, before task 1.2.

---

## Wave 1 — queries (module: charging worker)

- [x] **1.1** **[module: charging worker]** `internal/charging/db/query.sql` — append
  `-- name: ListSessionsByVehicleUpdatedSince :many` exactly as specified in design.md
  §"Query 1", including its full doc comment (the residual-filter index reasoning, the
  "this is the only mechanism that carries a `VerifySession` edit into `Reconcile`" note,
  and the D1/D4 cross-references are part of the deliverable, not decoration — do not
  shorten them). The query filters `updated_at >= @since` with **no** `LIMIT`, ordered
  `charge_stop_date_time ASC`. Do **not** add or edit any migration file.
  `depends_on`: — · `parallel_ok`: with 1.2 (same file, disjoint appended blocks — land
  both together)

- [x] **1.2** **[module: charging worker]** `internal/charging/db/query.sql` — append
  `-- name: ListSessionsByVehicle :many` exactly as specified in design.md §"Query 2",
  including its full doc comment (the DESC-vs-ASC justification against the shared
  ASC-built index, the backward-scan explanation, the `limit_count` pre-clamped-by-Go note,
  and the D3/D4 cross-references — do not shorten them). The query has **no** `updated_at`
  filter, orders `charge_stop_date_time DESC`, and takes `@limit_count` as a plain
  `LIMIT`. Do **not** add or edit any migration file. After both 1.1 and 1.2 land, run
  `make sqlc` and report the result — this generates
  `chargingdb.ListSessionsByVehicleUpdatedSinceParams` (fields `AccountID`, `TeslaID`,
  `Since`) and `chargingdb.ListSessionsByVehicleParams` (fields `AccountID`, `TeslaID`,
  `LimitCount`) against the *existing* `ChargeSession` model (unchanged — no migration in
  this change).
  `depends_on`: — · `parallel_ok`: with 1.1 (same file, disjoint appended blocks — land
  both together, then run `make sqlc` once)

---

## Wave 2 — the Go port (module: charging worker)

- [x] **2.1** **[module: charging worker]** `internal/charging/charging.go` — **REOPENED by
  design.md D8 (owner, 2026-08-27).** The first pass added both methods TO `SessionReader`,
  which broke `internal/gateway`'s implementer and failed `go vet ./...`. Correct shape:
  **leave `SessionReader` exactly as it was** (one method, `ListSessionsByVehicleBetween`,
  untouched doc comment and signature) and declare a NEW interface
  `SuperchargerSessionAnalyticsReader` that **embeds `SessionReader`** and adds the two new
  methods. Add `NewSuperchargerSessionAnalyticsReader(pool) SuperchargerSessionAnalyticsReader`
  next to the existing `NewSessionReader`, returning the same underlying `*sessionReader`;
  `NewSessionReader`'s signature and return type must NOT change (`cmd/web` wires it into the
  gateway). The two new methods' doc comments below move onto the new interface unchanged:
  - `ListSessionsByVehicleUpdatedSince(ctx, accountID, teslaID, since) ([]Session, error)` —
    doc comment states: every session whose `updated_at` is at or after `since`; ordered
    **ascending** by `ChargeStopDateTime` (not by `updated_at`); no `limit` parameter,
    `since` bounds the result; non-nil empty slice on no match; a session whose `TeslaID` is
    nil is never returned, for any `teslaID` (design.md D1, D4). State explicitly that this
    is the mechanism by which a `SessionVerifier.VerifySession` edit becomes visible to
    `analytics.Recalculator.Reconcile`.
  - `ListSessionsByVehicle(ctx, accountID, teslaID, limit) ([]Session, error)` — doc comment
    states: the `limit` most recent sessions, ordered **descending** by `ChargeStopDateTime`
    — the opposite of `ListSessionsByVehicleBetween`'s ASC, and call out explicitly that this
    divergence is deliberate (design.md D3) and must not be "corrected" to match the
    sibling method; `limit <= 0` uses the server default (`defaultLimit`, 100), mirroring
    `Reader.ListEntriesByVehicle`'s identical contract; non-nil empty slice on no match; a
    session whose `TeslaID` is nil is never returned, for any `teslaID` (design.md D3, D4).
  - Also add a doc comment on the new interface stating (a) why it is separate from
    `SessionReader` rather than a widening of it — `internal/gateway` depends on
    `SessionReader` and calls only `ListSessionsByVehicleBetween`, so widening forced a
    second module to implement methods it never calls (design.md D8, mirroring tier 1's D9);
    (b) why it embeds rather than restates `SessionReader` — `internal/analytics` needs all
    three shapes; and (c) that these methods do not share a single sort-direction convention
    — sort direction is chosen per query against the shared index, not as a port-family rule
    (design.md D3, Context fact 3). Note in the comment that the `Supercharger` prefix is the
    owner's deliberate divergence from the module's `Session*` family, for call-site
    readability in `internal/analytics` (design.md D8) — do not "tidy" it into the family.
  Follow this file's existing conventions: no `pgtype` anywhere in it, doc comments on every
  exported symbol. Does not compile until 2.2 supplies both methods' bodies.
  Verify with `go vet ./...` (repo-wide, NOT just `./internal/charging/...`): it must come
  back clean, with `internal/gateway`'s `fakeSessionReader` left untouched. If gateway still
  fails, the interface split is wrong — stop and report rather than editing gateway.
  `depends_on`: 1.2 · `parallel_ok`: with 2.2 (authoring only — they land together)

- [x] **2.2** **[module: charging worker]** `internal/charging/session_reader.go` — add both
  new methods to the existing `sessionReader` struct, next to `ListSessionsByVehicleBetween`
  (design.md D6 — no new file, no new struct):
  - `ListSessionsByVehicleUpdatedSince` — call
    `r.q.ListSessionsByVehicleUpdatedSince(ctx, chargingdb.ListSessionsByVehicleUpdatedSinceParams{AccountID:
    accountID, TeslaID: teslaIDToPgInt8(teslaID), Since: pgtype.Timestamptz{Time: since,
    Valid: true}})`, mapping each row via the existing `rowToSession` — no new mapping code
    (design.md D5). Match `ListSessionsByVehicleBetween`'s existing error-wrapping style
    (`fmt.Errorf("charging: list sessions by vehicle updated since: %w", err)`) and its
    `make([]Session, 0, len(rows))` non-nil-empty-slice pattern.
  - `ListSessionsByVehicle` — clamp `if limit <= 0 { limit = defaultLimit }` **in Go, before
    calling the query** (design.md D3 — reuse the existing package-level `defaultLimit`
    constant from `service.go`, do not declare a new one), then call
    `r.q.ListSessionsByVehicle(ctx, chargingdb.ListSessionsByVehicleParams{AccountID:
    accountID, TeslaID: teslaIDToPgInt8(teslaID), LimitCount: int32(limit)})`, mapping each
    row via the existing `rowToSession`. Match the same error-wrapping and
    non-nil-empty-slice pattern as above.
  Reuse the existing `teslaIDToPgInt8` helper for both — do not add a new one.
  `depends_on`: 1.2, 2.1 · `parallel_ok`: with 2.1

---

## Wave 3 — tests + module docs (module: charging worker)

- [x] **3.1** **[module: charging worker]** New integration test file
  `internal/charging/db_session_reader_updated_since_integration_test.go`
  (`package charging_test`) — implement Test Contract **T1–T7** exactly as design.md states
  them, with those expected values. Seed baseline fixture **U1** (three sessions,
  `session_id`s **960001–960003**) via `SessionWriter.MirrorSessions`, then pin each row's
  `updated_at` via a direct-SQL `UPDATE` (mirroring `db_session_integration_test.go`'s
  existing pattern of direct-SQL writes for columns the public ports don't set directly).
  T1 additionally calls `SessionVerifier.VerifySession` to reproduce the exact RM31
  mechanism design.md D1 describes — construct the verifier via
  `NewSessionVerifier(pool)` (the same `*pgxpool.Pool` the test's other helpers already
  hold). Assert against `charging.Session` domain fields only — **no `pgtype` in any
  assertion**. Use fresh `uuid.New()` account ids per isolation case (T6 needs a second
  account).
  `depends_on`: 2.2 · `parallel_ok`: with 3.2, 3.3

- [x] **3.2** **[module: charging worker]** New integration test file
  `internal/charging/db_session_reader_by_vehicle_integration_test.go`
  (`package charging_test`) — implement Test Contract **T8–T14** and **T-Order2** exactly as
  design.md states them, with those expected values. Seed baseline fixture **L1** (four
  sessions, `session_id`s **960011–960014**, strictly increasing `ChargeStopDateTime`) via
  `SessionWriter.MirrorSessions`. For **T-Order2**, open a transaction, issue `SET LOCAL
  enable_seqscan = off`, then run a raw `EXPLAIN (FORMAT TEXT)
  SELECT * FROM charge_sessions WHERE account_id = $1 AND tesla_id = $2 ORDER BY
  charge_stop_date_time DESC LIMIT $3` on that same transaction with L1's real parameter
  values, concatenate the returned plan rows, and assert the text contains `"Index Scan
  Backward using idx_charge_sessions_vehicle_stop"` and does not contain `"Sort"`. The
  `SET LOCAL` is required, not optional: with only four fixture rows the planner would
  otherwise choose a Seq Scan and fail a correct design (design.md T-Order2 explains why
  the `Sort`-absence assertion keeps its full force under the setting). Do not drop it,
  and do not "fix" the test by deleting the `Sort` assertion instead. Assert
  every other case against `charging.Session` domain fields only — **no `pgtype` in any
  assertion**. Use fresh `uuid.New()` account ids per isolation case (T14 needs a second
  account and a second vehicle).
  `depends_on`: 2.2 · `parallel_ok`: with 3.1, 3.3

- [x] **3.3** **[module: charging worker]** `internal/charging/AGENTS.md` — update for the
  module's widened read surface (docs-track-structural-change, `CLAUDE.md`
  §Non-negotiables):
  - §Public Interface → "The Supercharger session read port" — add both new methods to the
    `SessionReader` code block (matching charging.go's final text from task 2.1), and add a
    sentence noting the three methods do not share a sort-direction convention (design.md
    D3).
  - No change needed to §Data Ownership → `charge_sessions` beyond confirming the existing
    "since RM30-charging-add-session-read-port, the `SessionReader` port" sentence still
    reads correctly with two more methods — reword only if it currently implies exactly one
    method.
  - §Testing Notes — add the two new integration test files
    (`db_session_reader_updated_since_integration_test.go`,
    `db_session_reader_by_vehicle_integration_test.go`) to the existing bulleted list
    alongside `db_session_reader_integration_test.go`, and note that reads still assert only
    against `charging.Session`/direct SQL, never `pgtype`.
  `depends_on`: 2.2 · `parallel_ok`: with 3.1, 3.2

---

## Not in this change — do not do these

Listed so no worker "completes the pattern" and so review can reject them fast:

- **Any migration file, new or edited.** design.md D2 established the existing index
  already serves both queries; adding one anyway would trip the `database` design gate
  without the owner's sign-off.
- **A second, `DESC`-built copy of `idx_charge_sessions_vehicle_stop`.** design.md D3's
  backward-scan proof is exactly why one is unnecessary — do not add it "to be safe."
- **Any edit to `SessionWriter`, `SessionMirror`, `SessionVerifier`, `VerifyChargeSession`,
  or `ListSessionsByVehicleBetween`.** None of those are touched by this tier.
- **Wiring either new method into `internal/analytics` or `internal/gateway`.** That is
  tier 3's scope (`RM31-analytics-read-sessions-from-charging`) — these two ports have no
  caller until that change lands, and that is expected, not a gap.
- **Root `README.md` edits.** Its existing charging-module description already covers
  `charge_sessions` without claiming a fixed method count, so nothing in it goes stale.
- **Any offline/pure-function unit test in `charging_test.go`.** design.md D7 — there is no
  separable pure logic in this change worth isolating from the integration suite.
- **Reordering `ListSessionsByVehicleUpdatedSince`'s result by `updated_at`.** Ascending by
  `ChargeStopDateTime` is the deliberate choice (design.md D1); do not "match" `telemetry`'s
  sibling method's `updated_at`-ordering convention.
