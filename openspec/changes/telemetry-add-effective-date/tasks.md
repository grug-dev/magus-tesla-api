## 1. DTO + read mapper

- [x] 1.1 Add `EffectiveDate time.Time` field to `telemetry.Snapshot` in `internal/telemetry/telemetry.go` with a doc comment stating: it is `CapturedAt − 1 calendar day` (the day the nightly snapshot represents), read-derived (no DB column, never persisted), full `time.Time` (no display formatting), and distinct from `CapturedDate` (dedupe) and `CapturedAt` (read instant). Place it adjacent to `CapturedAt`/`CapturedDate`.
- [x] 1.2 Populate `EffectiveDate` in `rowToSnapshot` (`internal/telemetry/mapping.go`) as `r.CapturedAt.Time.AddDate(0, 0, -1)`. Add a line to the mapping-rules doc comment above the function noting `EffectiveDate` is derived here (calendar-day arithmetic, not a 24h duration, DST-safe) and that the write path leaves it zero.

## 2. Tests

- [x] 2.1 Add a unit test in `internal/telemetry/reader_test.go` asserting that for a row with a known `CapturedAt`, every `Snapshot` returned by `LatestSnapshotsByAccount` and `SnapshotsByVehicleSince` has `EffectiveDate == CapturedAt.AddDate(0,0,-1)` and `CapturedAt` unchanged.
- [x] 2.2 Add a DST-boundary assertion (a `CapturedAt` on a spring-forward / fall-back day) confirming `EffectiveDate` steps one calendar day, not 24 hours.
- [x] 2.3 Add an integration assertion in `internal/telemetry/db_read_integration_test.go` (or the existing read test that exercises `rowToSnapshot`) that the returned `Snapshot.EffectiveDate` is non-zero and equals `CapturedAt − 1 day` for real DB rows.

## 3. Verification

- [x] 3.1 Run `go test ./internal/telemetry/...` (and `make check` if available) and confirm the read suite is green without a live Tesla call.
- [x] 3.2 Confirm `go vet`/`go build ./...` succeeds and no other call site of `telemetry.Reader` broke (the field is additive; grep callers to confirm none enumerate struct fields by count).