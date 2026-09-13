package telemetry

import (
	"context"
	"log"
	"time"
)

// This file holds the four logging decorators instrumenting the module's
// existing read/write seams — store (3 of its 8 methods), Reader (all 5),
// SuperchargerHistoryReader (all 4), and RunWriter (its 1 method) — per
// RM44-telemetry-add-query-logging design.md D1/D2/D6/D7/D8. Grouped in one
// file by FEATURE ("instrument the module's existing seams") rather than one
// file per port, mirroring how reader.go already mixes two concrete types
// (design D7).
//
// Every decorator here implements its wrapped interface EXPLICITLY, never by
// embedding — the same shape as call_counter.go's callCounter, for the same
// reason: embedding (struct{ store }, overriding only today's methods) would
// let a future method added to the wrapped interface satisfy this type
// SILENTLY via promotion, so that call would never be logged. Explicit,
// no-embedding implementation turns a missed override into a COMPILE ERROR
// the moment the wrapped interface gains a method this file does not also
// gain.
//
// All log lines use stdlib log.Printf (no slog — roadmap D8) with the prefix
// "telemetry query:". None of store/Reader/SuperchargerHistoryReader/
// RunWriter's methods take a tesla.Credentials (or any token) argument, so
// these decorators have no credential value in scope to log, in any method,
// by construction (design D3) — the entire credential-leak risk is confined
// to call_counter.go's extension.

// --- loggingStore ---

// loggingStore wraps the unexported store seam and logs exactly its 3 write
// methods (insertSnapshot, insertPollAttempt, upsertSuperchargerHistory),
// before delegating to inner — the argument line is known upfront and must
// still appear if the write fails. Its other 5 methods (the read side) are
// SILENT pass-throughs: they exist only so loggingStore satisfies the full
// store interface (it must, to be assignable to service.store), but reading
// through this type never happens in production — loggingStore is wired only
// into NewService (service.go), never into NewReader, and the read side is
// logged separately by loggingReader, which wraps the whole Reader port
// instead (design D1/D2). A store-shaped decorator with only 3 of 8 methods
// actually logging can look like a bug at a glance; it is not — see
// loggingReader for the other 5 methods' log lines.
type loggingStore struct {
	inner store
}

// newLoggingStore constructs a loggingStore wrapping inner.
func newLoggingStore(inner store) *loggingStore {
	return &loggingStore{inner: inner}
}

// Compile-time assertion: *loggingStore must satisfy the full store
// interface. A future method added to store without a matching explicit
// override here fails to compile.
var _ store = (*loggingStore)(nil)

// insertSnapshot implements store, logging before delegating (design D6).
func (l *loggingStore) insertSnapshot(ctx context.Context, s Snapshot) error {
	log.Printf("telemetry query: insertSnapshot tesla_id=%d captured_at=%s raw_data_bytes=%d",
		s.TeslaID, s.CapturedAt.UTC().Format(time.RFC3339), len(s.RawData))
	return l.inner.insertSnapshot(ctx, s)
}

// insertPollAttempt implements store, logging before delegating (design D6).
func (l *loggingStore) insertPollAttempt(ctx context.Context, a Attempt) error {
	log.Printf("telemetry query: insertPollAttempt polled_by_account=%s tesla_id=%d attempted_at=%s outcome=%s reason=%s",
		a.PolledByAccountID, a.TeslaID, a.AttemptedAt.UTC().Format(time.RFC3339), a.Outcome, a.Reason)
	return l.inner.insertPollAttempt(ctx, a)
}

// latestSnapshotsByVehicles implements store as a silent pass-through — the
// read side is logged by loggingReader instead (design D1).
func (l *loggingStore) latestSnapshotsByVehicles(ctx context.Context, teslaIDs []int64) ([]Snapshot, error) {
	return l.inner.latestSnapshotsByVehicles(ctx, teslaIDs)
}

// snapshotsByVehicleSince implements store as a silent pass-through — the
// read side is logged by loggingReader instead (design D1).
func (l *loggingStore) snapshotsByVehicleSince(ctx context.Context, teslaID int64, since time.Time) ([]Snapshot, error) {
	return l.inner.snapshotsByVehicleSince(ctx, teslaID, since)
}

// snapshotsByVehicleBetween implements store as a silent pass-through — the
// read side is logged by loggingReader instead (design D1).
func (l *loggingStore) snapshotsByVehicleBetween(ctx context.Context, teslaID int64, start, end time.Time) ([]Snapshot, error) {
	return l.inner.snapshotsByVehicleBetween(ctx, teslaID, start, end)
}

// snapshotsByVehicleUpdatedSince implements store as a silent pass-through —
// the read side is logged by loggingReader instead (design D1).
func (l *loggingStore) snapshotsByVehicleUpdatedSince(ctx context.Context, teslaID int64, since time.Time) ([]Snapshot, error) {
	return l.inner.snapshotsByVehicleUpdatedSince(ctx, teslaID, since)
}

// upsertSuperchargerHistory implements store, logging before delegating
// (design D6).
func (l *loggingStore) upsertSuperchargerHistory(ctx context.Context, s SuperchargerHistory) error {
	log.Printf("telemetry query: upsertSuperchargerHistory tesla_id=%d session_id=%d charge_start=%s charge_stop=%s raw_data_bytes=%d",
		s.TeslaID, s.SessionID,
		s.ChargeStartDateTime.UTC().Format(time.RFC3339), s.ChargeStopDateTime.UTC().Format(time.RFC3339), len(s.RawData))
	return l.inner.upsertSuperchargerHistory(ctx, s)
}

// snapshotPrecedingDay implements store as a silent pass-through — the read
// side is logged by loggingReader instead (design D1).
func (l *loggingStore) snapshotPrecedingDay(ctx context.Context, teslaID int64, day time.Time) (*Snapshot, error) {
	return l.inner.snapshotPrecedingDay(ctx, teslaID, day)
}

// --- loggingReader ---

// loggingReader wraps the public Reader port and logs all 5 methods AFTER
// delegating to inner, so the logged rows/found value reflects the actual
// result — including on an error path, where inner returns a nil/empty
// result (design D6). It is wired only into NewReader (reader.go), wrapping
// a bare, undecorated *reader whose own store field is NOT a loggingStore —
// double-decorating would log every read twice (design D2).
type loggingReader struct {
	inner Reader
}

// newLoggingReader constructs a loggingReader wrapping inner.
func newLoggingReader(inner Reader) *loggingReader {
	return &loggingReader{inner: inner}
}

// Compile-time assertion: *loggingReader must satisfy the public Reader
// interface. A future method added to Reader without a matching explicit
// override here fails to compile.
var _ Reader = (*loggingReader)(nil)

// LatestSnapshotsByVehicles implements Reader, logging after delegating.
func (l *loggingReader) LatestSnapshotsByVehicles(ctx context.Context, teslaIDs []int64) ([]Snapshot, error) {
	result, err := l.inner.LatestSnapshotsByVehicles(ctx, teslaIDs)
	log.Printf("telemetry query: LatestSnapshotsByVehicles tesla_ids=%v rows=%d", teslaIDs, len(result))
	return result, err
}

// SnapshotsByVehicleSince implements Reader, logging after delegating.
func (l *loggingReader) SnapshotsByVehicleSince(ctx context.Context, teslaID int64, since time.Time) ([]Snapshot, error) {
	result, err := l.inner.SnapshotsByVehicleSince(ctx, teslaID, since)
	log.Printf("telemetry query: SnapshotsByVehicleSince tesla_id=%d since=%s rows=%d",
		teslaID, since.UTC().Format(time.RFC3339), len(result))
	return result, err
}

// SnapshotsByVehicleBetween implements Reader, logging after delegating.
func (l *loggingReader) SnapshotsByVehicleBetween(ctx context.Context, teslaID int64, start, end time.Time) ([]Snapshot, error) {
	result, err := l.inner.SnapshotsByVehicleBetween(ctx, teslaID, start, end)
	log.Printf("telemetry query: SnapshotsByVehicleBetween tesla_id=%d start=%s end=%s rows=%d",
		teslaID, start.UTC().Format("2006-01-02"), end.UTC().Format("2006-01-02"), len(result))
	return result, err
}

// SnapshotsByVehicleUpdatedSince implements Reader, logging after delegating.
func (l *loggingReader) SnapshotsByVehicleUpdatedSince(ctx context.Context, teslaID int64, since time.Time) ([]Snapshot, error) {
	result, err := l.inner.SnapshotsByVehicleUpdatedSince(ctx, teslaID, since)
	log.Printf("telemetry query: SnapshotsByVehicleUpdatedSince tesla_id=%d since=%s rows=%d",
		teslaID, since.UTC().Format(time.RFC3339), len(result))
	return result, err
}

// SnapshotPrecedingDay implements Reader, logging after delegating.
func (l *loggingReader) SnapshotPrecedingDay(ctx context.Context, teslaID int64, day time.Time) (*Snapshot, error) {
	result, err := l.inner.SnapshotPrecedingDay(ctx, teslaID, day)
	log.Printf("telemetry query: SnapshotPrecedingDay tesla_id=%d day=%s found=%t",
		teslaID, day.UTC().Format("2006-01-02"), result != nil)
	return result, err
}

// --- loggingSuperchargerHistoryReader ---

// loggingSuperchargerHistoryReader wraps the public SuperchargerHistoryReader
// port and logs all 3 methods AFTER delegating to inner, so rows reflects the
// actual result (design D6). SuperchargerHistoryByVehicle logs the RESOLVED
// limit (via the existing resolveLimit helper in reader.go, reused as-is),
// not the caller's raw limit argument — a caller-supplied limit=0 must
// visibly show limit=2147483647, the number the query engine actually runs
// with.
type loggingSuperchargerHistoryReader struct {
	inner SuperchargerHistoryReader
}

// newLoggingSuperchargerHistoryReader constructs a
// loggingSuperchargerHistoryReader wrapping inner.
func newLoggingSuperchargerHistoryReader(inner SuperchargerHistoryReader) *loggingSuperchargerHistoryReader {
	return &loggingSuperchargerHistoryReader{inner: inner}
}

// Compile-time assertion: *loggingSuperchargerHistoryReader must satisfy the
// public SuperchargerHistoryReader interface. A future method added to that
// interface without a matching explicit override here fails to compile.
var _ SuperchargerHistoryReader = (*loggingSuperchargerHistoryReader)(nil)

// SuperchargerHistoryByVehicle implements SuperchargerHistoryReader, logging
// after delegating.
func (l *loggingSuperchargerHistoryReader) SuperchargerHistoryByVehicle(ctx context.Context, teslaID int64, limit int) ([]SuperchargerHistory, error) {
	result, err := l.inner.SuperchargerHistoryByVehicle(ctx, teslaID, limit)
	log.Printf("telemetry query: SuperchargerHistoryByVehicle tesla_id=%d limit=%d rows=%d",
		teslaID, resolveLimit(limit), len(result))
	return result, err
}

// SuperchargerHistoryByVehicleBetween implements SuperchargerHistoryReader,
// logging after delegating.
func (l *loggingSuperchargerHistoryReader) SuperchargerHistoryByVehicleBetween(ctx context.Context, teslaID int64, start, end time.Time) ([]SuperchargerHistory, error) {
	result, err := l.inner.SuperchargerHistoryByVehicleBetween(ctx, teslaID, start, end)
	log.Printf("telemetry query: SuperchargerHistoryByVehicleBetween tesla_id=%d start=%s end=%s rows=%d",
		teslaID, start.UTC().Format("2006-01-02"), end.UTC().Format("2006-01-02"), len(result))
	return result, err
}

// SuperchargerHistoryByVehicleUpdatedSince implements
// SuperchargerHistoryReader, logging after delegating.
func (l *loggingSuperchargerHistoryReader) SuperchargerHistoryByVehicleUpdatedSince(ctx context.Context, teslaID int64, since time.Time) ([]SuperchargerHistory, error) {
	result, err := l.inner.SuperchargerHistoryByVehicleUpdatedSince(ctx, teslaID, since)
	log.Printf("telemetry query: SuperchargerHistoryByVehicleUpdatedSince tesla_id=%d since=%s rows=%d",
		teslaID, since.UTC().Format(time.RFC3339), len(result))
	return result, err
}

// --- loggingRunWriter ---

// loggingRunWriter wraps the public RunWriter port and logs its single
// method BEFORE delegating (design D6) — this is a write with no row count
// to report, so there is nothing to gain from logging after, and logging
// before keeps the same "argument line always appears" guarantee as
// loggingStore's write methods.
type loggingRunWriter struct {
	inner RunWriter
}

// newLoggingRunWriter constructs a loggingRunWriter wrapping inner.
func newLoggingRunWriter(inner RunWriter) *loggingRunWriter {
	return &loggingRunWriter{inner: inner}
}

// Compile-time assertion: *loggingRunWriter must satisfy the public
// RunWriter interface. A future method added to RunWriter without a matching
// explicit override here fails to compile.
var _ RunWriter = (*loggingRunWriter)(nil)

// RecordRun implements RunWriter, logging before delegating.
func (l *loggingRunWriter) RecordRun(ctx context.Context, run PollRun) error {
	log.Printf("telemetry query: RecordRun run_id=%s triggered_by=%s", run.RunID, run.TriggeredBy)
	return l.inner.RecordRun(ctx, run)
}
