# Tasks: telemetry-add-snapshot-history-read-port

> RM5 tier 1. Sub-tasks are grouped so they can be applied in one serial pass (they touch a small,
> overlapping file set). **Not started** — authored for review per the user's "do not implement".
> Verification gate: `make check` (build + vet + tests). No DB migration to apply.

## 1. SQL query (sqlc)

- [x] 1.1 Add the `SnapshotsByVehicleSince` query to `internal/telemetry/db/query.sql`: select the
  full column list (same as `LatestSnapshotsByAccount`) `WHERE account_id = @account_id AND
  tesla_id = @tesla_id AND captured_at >= @since ORDER BY captured_at ASC LIMIT 400`. Comment it
  with the index-reuse note (D3) and the `LIMIT` rationale (D4).
- [x] 1.2 Run `make sqlc` (or `sqlc generate`) to regenerate `telemetrydb`; confirm the generated
  method signature takes `(AccountID, TeslaID, Since)` params and returns the row slice.

## 2. Domain read port

- [x] 2.1 Add `SnapshotsByVehicleSince(ctx context.Context, accountID uuid.UUID, teslaID int64,
  since time.Time) ([]Snapshot, error)` to the `Reader` interface in
  `internal/telemetry/telemetry.go`, with the doc comment from the proposal (oldest-first,
  empty-non-nil, account+vehicle scoped).
- [x] 2.2 Implement it in `internal/telemetry/reader.go`: call the generated query, map each
  `telemetrydb` row to the domain `Snapshot` (reuse the existing row→Snapshot mapper used by
  `LatestSnapshotsByAccount` — do not duplicate the mapping), return an empty non-nil slice when no
  rows. Miles-native; no unit conversion here.

## 3. Fakes / compile fixes

- [x] 3.1 Add a one-line stub `SnapshotsByVehicleSince(...) ([]Snapshot, error) { return nil, nil }`
  to every in-tree fake implementing `telemetry.Reader` (at least `fakeReader` in
  `internal/gateway/handlers/handlers_test.go`); grep for other `Reader` fakes and fix each.

## 4. Tests + verification

- [x] 4.1 Unit test the mapping/empty contract with a fake `telemetrydb` querier (no DATABASE_URL):
  oldest-first ordering preserved, empty-non-nil on no rows, `account_id`/`tesla_id`/`since` passed
  through.
- [x] 4.2 Extend the DATABASE_URL-gated store test (`internal/telemetry/db_read_integration_test.go`)
  with a `SnapshotsByVehicleSince` case: seed 3 snapshots across days for one vehicle + one for a
  second account, assert only the first account/vehicle's rows return, oldest-first, and the
  `since` boundary is inclusive. (No `Raw*`/paid-API calls — this is store-level only.)
- [x] 4.3 `make check` passes (build + vet + tests). No migration to apply.

## 5. Docs

- [x] 5.1 No structural/module-surface doc change beyond the spec delta: this adds a method to an
  existing port, not a new module or a new public package. Confirm `internal/telemetry/README.md` /
  `AGENTS.md` "read ports" list mentions the history port if such a list exists there; add one line
  if it does. (No root README structure-tree change — no module added/removed/renamed.)
