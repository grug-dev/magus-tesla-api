package charging

import (
	"context"
	"strconv"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/logging"
)

// This file holds six logging decorators over this module's nightly-path
// seams: Reader (one of its 4 methods), SessionWriter (its single method),
// SuperchargerSessionAnalyticsReader (2 of its 3 methods),
// MirrorWatermarkStore (both methods), MonthlyCapacityCalculator (its
// single method), and MonthlyCapacityReader (its single method). Grouped in
// one file for the same reason
// internal/telemetry/query_log.go and internal/analytics/query_log.go each
// group their own decorators together — they instrument one feature, the
// nightly cycle's read/write path through this module.
//
// Every decorator here implements its wrapped interface EXPLICITLY, never by
// embedding. Embedding (struct{ Reader }, overriding only today's methods)
// would let a future method added to the wrapped interface satisfy this type
// SILENTLY through promotion, so a call through that new method would never
// be logged. Explicit, no-embedding implementation turns a missed override
// into a COMPILE ERROR the moment the wrapped interface gains a method this
// file does not also gain — see each type's "var _ ... = (*logging...)(nil)"
// assertion below.
//
// All log lines go through internal/logging.Note, the platform-wide
// "[Type] [Method] message" format, keeping the "charging query:" topic in
// the message so a reader can grep for it.

// --- loggingReader ---

// loggingReader wraps the public Reader port. Only
// ListEntriesByVehicleUpdatedSince — the one Reader method whose only caller
// anywhere is the nightly-triggered analytics.Recalculator.Reconcile — logs.
// The other three methods are silent pass-throughs: ListEntriesByVehicleBetween
// and ListEntriesByVehicles are dominated by live gateway reads (a page
// render, a conflict check, row lookups) that fire on every request, and
// ListEntriesByVehicle has no caller anywhere today.
type loggingReader struct {
	inner Reader
}

// newLoggingReader constructs a loggingReader wrapping inner.
func newLoggingReader(inner Reader) *loggingReader {
	return &loggingReader{inner: inner}
}

// Compile-time assertion: *loggingReader must satisfy the full Reader
// interface. A future method added to Reader without a matching explicit
// override here fails to compile.
var _ Reader = (*loggingReader)(nil)

// ListEntriesByVehicle implements Reader as a silent pass-through — it has
// no caller anywhere in the repo today.
func (l *loggingReader) ListEntriesByVehicle(ctx context.Context, teslaID int64, limit int) ([]Entry, error) {
	return l.inner.ListEntriesByVehicle(ctx, teslaID, limit)
}

// ListEntriesByVehicles implements Reader as a silent pass-through — its
// callers are gateway row-lookup helpers used by the edit/delete handlers,
// with no nightly-path caller.
func (l *loggingReader) ListEntriesByVehicles(ctx context.Context, teslaIDs []int64, limit int) ([]Entry, error) {
	return l.inner.ListEntriesByVehicles(ctx, teslaIDs, limit)
}

// ListEntriesByVehicleBetween implements Reader as a silent pass-through —
// its call volume is dominated by two live gateway reads (a page render and
// a same-day-conflict check) that fire on every request.
func (l *loggingReader) ListEntriesByVehicleBetween(ctx context.Context, teslaID int64, from, to time.Time) ([]Entry, error) {
	return l.inner.ListEntriesByVehicleBetween(ctx, teslaID, from, to)
}

// ListEntriesByVehicleUpdatedSince implements Reader, logging AFTER
// delegating so the logged row count reflects the actual result.
func (l *loggingReader) ListEntriesByVehicleUpdatedSince(ctx context.Context, teslaID int64, since time.Time) ([]Entry, error) {
	result, err := l.inner.ListEntriesByVehicleUpdatedSince(ctx, teslaID, since)
	logging.Note("Reader", "ListEntriesByVehicleUpdatedSince", "charging query: tesla_id=%d since=%s rows=%d",
		teslaID, since.UTC().Format(time.RFC3339), len(result))
	return result, err
}

// --- loggingSessionWriter ---

// loggingSessionWriter wraps the public SessionWriter port and logs its
// single method — the nightly cycle's only write to supercharger_sessions.
type loggingSessionWriter struct {
	inner SessionWriter
}

// newLoggingSessionWriter constructs a loggingSessionWriter wrapping inner.
func newLoggingSessionWriter(inner SessionWriter) *loggingSessionWriter {
	return &loggingSessionWriter{inner: inner}
}

// Compile-time assertion: *loggingSessionWriter must satisfy the full
// SessionWriter interface. A future method added to SessionWriter without a
// matching explicit override here fails to compile.
var _ SessionWriter = (*loggingSessionWriter)(nil)

// MirrorSessions implements SessionWriter, logging BEFORE delegating — the
// arguments are known upfront, and the line must still appear if the write
// itself fails.
func (l *loggingSessionWriter) MirrorSessions(ctx context.Context, sessions []SessionMirror) error {
	logging.Note("SessionWriter", "MirrorSessions", "charging query: tesla_id=%d sessions=%d",
		firstTeslaID(sessions), len(sessions))
	return l.inner.MirrorSessions(ctx, sessions)
}

// firstTeslaID reads the vehicle id off the first session in the slice. The
// port makes no promise that every session in one call shares a vehicle, but
// its only caller always builds the slice from one vehicle's sessions per
// call, so this reports the actual usage pattern rather than a per-session
// breakdown nobody needs. An empty slice — an edge case the caller never
// actually produces, since it skips the call on a zero-row read — reports 0.
func firstTeslaID(sessions []SessionMirror) int64 {
	if len(sessions) == 0 {
		return 0
	}
	return sessions[0].TeslaID
}

// --- loggingSuperchargerSessionAnalyticsReader ---

// loggingSuperchargerSessionAnalyticsReader wraps the public
// SuperchargerSessionAnalyticsReader port. ListSessionsByVehicleBetween and
// ListSessionsByVehicleUpdatedSince log; ListSessionsByVehicle is a silent
// pass-through with no caller anywhere today.
//
// This decorator is wired ONLY into NewSuperchargerSessionAnalyticsReader,
// never into NewSessionReader. Both constructors build the same underlying
// concrete type, but only this one is reached exclusively through
// internal/analytics — the sibling constructor stays undecorated so the
// gateway's own Supercharger-stats page gains no new log line.
type loggingSuperchargerSessionAnalyticsReader struct {
	inner SuperchargerSessionAnalyticsReader
}

// newLoggingSuperchargerSessionAnalyticsReader constructs a
// loggingSuperchargerSessionAnalyticsReader wrapping inner.
func newLoggingSuperchargerSessionAnalyticsReader(inner SuperchargerSessionAnalyticsReader) *loggingSuperchargerSessionAnalyticsReader {
	return &loggingSuperchargerSessionAnalyticsReader{inner: inner}
}

// Compile-time assertion: *loggingSuperchargerSessionAnalyticsReader must
// satisfy the full SuperchargerSessionAnalyticsReader interface. A future
// method added to it without a matching explicit override here fails to
// compile.
var _ SuperchargerSessionAnalyticsReader = (*loggingSuperchargerSessionAnalyticsReader)(nil)

// ListSessionsByVehicleBetween implements the interface (satisfying its
// embedded SessionReader), logging AFTER delegating so the logged row count
// reflects the actual result.
func (l *loggingSuperchargerSessionAnalyticsReader) ListSessionsByVehicleBetween(ctx context.Context, teslaID int64, from, to time.Time) ([]Session, error) {
	result, err := l.inner.ListSessionsByVehicleBetween(ctx, teslaID, from, to)
	logging.Note("SuperchargerSessionAnalyticsReader", "ListSessionsByVehicleBetween", "charging query: tesla_id=%d start=%s end=%s rows=%d",
		teslaID, from.UTC().Format("2006-01-02"), to.UTC().Format("2006-01-02"), len(result))
	return result, err
}

// ListSessionsByVehicleUpdatedSince implements the interface, logging AFTER
// delegating so the logged row count reflects the actual result.
func (l *loggingSuperchargerSessionAnalyticsReader) ListSessionsByVehicleUpdatedSince(ctx context.Context, teslaID int64, since time.Time) ([]Session, error) {
	result, err := l.inner.ListSessionsByVehicleUpdatedSince(ctx, teslaID, since)
	logging.Note("SuperchargerSessionAnalyticsReader", "ListSessionsByVehicleUpdatedSince", "charging query: tesla_id=%d since=%s rows=%d",
		teslaID, since.UTC().Format(time.RFC3339), len(result))
	return result, err
}

// ListSessionsByVehicle implements the interface as a silent pass-through —
// it has no caller anywhere in the repo today.
func (l *loggingSuperchargerSessionAnalyticsReader) ListSessionsByVehicle(ctx context.Context, teslaID int64, limit int) ([]Session, error) {
	return l.inner.ListSessionsByVehicle(ctx, teslaID, limit)
}

// --- loggingMirrorWatermarkStore ---

// loggingMirrorWatermarkStore wraps the public MirrorWatermarkStore port and
// logs both of its methods. Both share one caller and one nightly-only trust
// model, so there is no off-path method to keep silent.
type loggingMirrorWatermarkStore struct {
	inner MirrorWatermarkStore
}

// newLoggingMirrorWatermarkStore constructs a loggingMirrorWatermarkStore
// wrapping inner.
func newLoggingMirrorWatermarkStore(inner MirrorWatermarkStore) *loggingMirrorWatermarkStore {
	return &loggingMirrorWatermarkStore{inner: inner}
}

// Compile-time assertion: *loggingMirrorWatermarkStore must satisfy the full
// MirrorWatermarkStore interface. A future method added to it without a
// matching explicit override here fails to compile.
var _ MirrorWatermarkStore = (*loggingMirrorWatermarkStore)(nil)

// MirrorWatermark implements MirrorWatermarkStore, logging AFTER delegating
// — the cursor value returned is the point of the line.
func (l *loggingMirrorWatermarkStore) MirrorWatermark(ctx context.Context, teslaID int64) (time.Time, error) {
	cursor, err := l.inner.MirrorWatermark(ctx, teslaID)
	logging.Note("MirrorWatermarkStore", "MirrorWatermark", "charging query: tesla_id=%d cursor=%s",
		teslaID, cursor.UTC().Format(time.RFC3339))
	return cursor, err
}

// AdvanceMirrorWatermark implements MirrorWatermarkStore, logging BEFORE
// delegating — the argument is known upfront, and the line must still
// appear if the write itself fails.
func (l *loggingMirrorWatermarkStore) AdvanceMirrorWatermark(ctx context.Context, teslaID int64, observed time.Time) error {
	logging.Note("MirrorWatermarkStore", "AdvanceMirrorWatermark", "charging query: tesla_id=%d observed=%s",
		teslaID, observed.UTC().Format(time.RFC3339))
	return l.inner.AdvanceMirrorWatermark(ctx, teslaID, observed)
}

// --- loggingMonthlyCapacityCalculator ---

// loggingMonthlyCapacityCalculator wraps the public MonthlyCapacityCalculator
// port and logs its single method — the nightly step-4 measurement, also
// callable on demand from the cmd/monthly-capacity tool.
type loggingMonthlyCapacityCalculator struct {
	inner MonthlyCapacityCalculator
}

// newLoggingMonthlyCapacityCalculator constructs a
// loggingMonthlyCapacityCalculator wrapping inner.
func newLoggingMonthlyCapacityCalculator(inner MonthlyCapacityCalculator) *loggingMonthlyCapacityCalculator {
	return &loggingMonthlyCapacityCalculator{inner: inner}
}

// Compile-time assertion: *loggingMonthlyCapacityCalculator must satisfy the
// full MonthlyCapacityCalculator interface. A future method added to it
// without a matching explicit override here fails to compile.
var _ MonthlyCapacityCalculator = (*loggingMonthlyCapacityCalculator)(nil)

// Calculate implements MonthlyCapacityCalculator, logging AFTER delegating
// so the line can report the report's own counts. This does not duplicate
// internal/app's existing "monthly capacity: ..." narration line: that line
// tells the orchestration outcome, this one records the port call itself —
// the same relationship every other decorator in this file has to its own
// internal/app narration line.
func (l *loggingMonthlyCapacityCalculator) Calculate(ctx context.Context, period time.Time, teslaID *int64) (MonthlyCapacityReport, error) {
	result, err := l.inner.Calculate(ctx, period, teslaID)

	scope := "all"
	if teslaID != nil {
		scope = strconv.FormatInt(*teslaID, 10)
	}

	logging.Note("MonthlyCapacityCalculator", "Calculate", "charging query: period=%s tesla_id=%s found=%d measured=%d thin=%d",
		period.Format("2006-01"), scope, result.VehiclesFound, result.Measured, result.Thin)
	return result, err
}

// --- loggingMonthlyCapacityReader ---

// loggingMonthlyCapacityReader wraps the public MonthlyCapacityReader port.
// Its only caller is the nightly cycle, the same class of caller
// MirrorWatermarkStore and MonthlyCapacityCalculator are already logged for
// in this file.
type loggingMonthlyCapacityReader struct {
	inner MonthlyCapacityReader
}

// newLoggingMonthlyCapacityReader constructs a loggingMonthlyCapacityReader
// wrapping inner.
func newLoggingMonthlyCapacityReader(inner MonthlyCapacityReader) *loggingMonthlyCapacityReader {
	return &loggingMonthlyCapacityReader{inner: inner}
}

// Compile-time assertion: *loggingMonthlyCapacityReader must satisfy the full
// MonthlyCapacityReader interface. A future method added to it without a
// matching explicit override here fails to compile.
var _ MonthlyCapacityReader = (*loggingMonthlyCapacityReader)(nil)

// CapacityForMonth implements MonthlyCapacityReader, logging AFTER
// delegating so the logged outcome reflects the actual result.
func (l *loggingMonthlyCapacityReader) CapacityForMonth(ctx context.Context, teslaID int64, month time.Time) (*float64, bool, error) {
	capacityKWh, found, err := l.inner.CapacityForMonth(ctx, teslaID, month)
	// %v on a *float64 prints the pointer address, not the number, so the
	// value is unwrapped here. A nil capacity logs as measured=false.
	measured := capacityKWh != nil
	value := 0.0
	if measured {
		value = *capacityKWh
	}
	logging.Note("MonthlyCapacityReader", "CapacityForMonth",
		"charging query: tesla_id=%d month=%s found=%t measured=%t capacity_kwh=%.2f",
		teslaID, month.Format("2006-01"), found, measured, value)
	return capacityKWh, found, err
}
