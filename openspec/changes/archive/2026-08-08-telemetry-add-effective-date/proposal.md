## Why

The nightly batch captures each vehicle's snapshot in the **morning** (≈03:30 local), so a
snapshot read "today" actually describes the vehicle's state over the **previous** calendar day.
Today the `Snapshot` DTO carries only `CapturedAt` (the read instant), so every history consumer
must re-derive "which day does this row represent" on its own — and the dashboard history graph
currently shows the morning's calendar date, which reads to the user as "today" for data that is
really yesterday's driving. MAG-6 asks the history graph to show the day the data represents. This
change adds that day to the `Snapshot` DTO once, so all read consumers (history graphs now,
battery-degradation curves / efficiency trends later) get a self-describing field instead of
re-implementing the offset.

## What Changes

- **Add `EffectiveDate time.Time` to `telemetry.Snapshot`.** It is the calendar day the snapshot
  represents — `CapturedAt.AddDate(0, 0, -1)` — preserving the full `time.Time` type and the
  time-of-day component (consistent with `CapturedAt`), so callers can format it however they wish
  (the MM-DD formatting the history graph needs is a gateway concern, intentionally NOT baked into
  this field).
- **Populate `EffectiveDate` in the single DB→domain mapper** (`rowToSnapshot` in
  `internal/telemetry/mapping.go`), the one place every read path already funnels through.
  **No new DB column, no migration, no schema change** — `EffectiveDate` is read-derived.
- **No breaking change.** `EffectiveDate` is an additive field; existing callers ignore it. The
  write path (`insertSnapshot`) does not set it (it has no DB column); it stays the zero value on
  a freshly-built write `Snapshot`, which is fine because no code reads it on the write path.
- **Read paths affected (hot reads named per the perf rule):**
  - `telemetry.Reader.LatestSnapshotsByAccount` — the dashboard card render path; every returned
    `Snapshot` now carries `EffectiveDate` (one `AddDate` call per row; negligible vs. the existing
    row scan).
  - `telemetry.Reader.SnapshotsByVehicleSince` — the dashboard history-chart path; same.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `telemetry`: the `Snapshot` returned by both read methods gains an `EffectiveDate` field
  representing the calendar day the nightly snapshot describes (= `CapturedAt` − 1 day), computed
  once at the DB→domain mapping boundary. Existing field semantics and the read-port method
  shapes are unchanged.

## Impact

- **Module:** `internal/telemetry` only. No gateway change in this tier (the gateway consumes
  `EffectiveDate` in tier 2 of the `history-graph-improvements` roadmap).
- **Files:** `internal/telemetry/telemetry.go` (add field to `Snapshot`), `internal/telemetry/
  mapping.go` (populate it in `rowToSnapshot`), tests in `internal/telemetry/reader_test.go` /
  `db_read_integration_test.go` (assert the new field).
- **APIs:** `telemetry.Reader` public interface is unchanged (same two method signatures); the
  returned `Snapshot` value gains a field. **Non-breaking** for existing callers.
- **Dependencies:** none added. No DB migration. No index change.
- **Breaking:** No. This change is additive.
- **Performance:** two hot read paths now do one `time.Time.AddDate` call per row. This is cheaper
  than the existing `pgtype`→`time.Time` conversions already happening per row on the same line,
  and runs only inside the mapper. Negligible.