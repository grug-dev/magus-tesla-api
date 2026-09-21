package analytics

import (
	"context"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/logging"
	"github.com/cristianpena/magus-tesla-api/internal/vehicleref"
)

// This file holds the three logging decorators instrumenting this module's
// nightly-cycle seams: Reader (one of its 4 methods), Recalculator (both of
// its methods), and GapWriter (its single method). Grouped in one file
// because they instrument one feature — the nightly poller's read/write
// path through this module — the same reason internal/telemetry/query_log.go
// groups its own decorators together.
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
// "[Type] [Method] message" format, keeping the "analytics query:" topic in
// the message so a reader can grep for it.

// --- loggingReader ---

// loggingReader wraps the public Reader port. Only ConsumedByDay — the one
// Reader method the nightly poller actually calls — logs. The other three
// methods (OdometerDeltaByDay, BatteryLevelByDay, LatestMetricsForVehicles)
// are silent pass-throughs: they have no poller caller, every caller of them
// serves a gateway dashboard or history page on a live HTTP request, and
// this module's read path there is performance-sensitive (ai/architecture.md
// §7's read-heavy profile). Logging them would add a line to every such page
// load for information nobody asked for.
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

// ConsumedByDay implements Reader, logging AFTER delegating to inner so the
// logged row and flagged counts reflect the actual result, including on an
// error path where inner returns a nil/empty slice.
func (l *loggingReader) ConsumedByDay(ctx context.Context, teslaID int64, start, end time.Time) ([]DayConsumption, error) {
	result, err := l.inner.ConsumedByDay(ctx, teslaID, start, end)

	flaggedCount := 0
	for _, d := range result {
		if d.Flagged {
			flaggedCount++
		}
	}

	logging.Note("Reader", "ConsumedByDay", "analytics query: tesla_id=%d start=%s end=%s rows=%d flagged=%d",
		teslaID, start.UTC().Format("2006-01-02"), end.UTC().Format("2006-01-02"), len(result), flaggedCount)
	return result, err
}

// OdometerDeltaByDay implements Reader as a silent pass-through — a
// dashboard-only read with no poller caller, off this tier's scope.
func (l *loggingReader) OdometerDeltaByDay(ctx context.Context, teslaID int64, start, end time.Time) ([]DayDistance, error) {
	return l.inner.OdometerDeltaByDay(ctx, teslaID, start, end)
}

// BatteryLevelByDay implements Reader as a silent pass-through — a
// dashboard-only read with no poller caller, off this tier's scope.
func (l *loggingReader) BatteryLevelByDay(ctx context.Context, teslaID int64, start, end time.Time) ([]DayBattery, error) {
	return l.inner.BatteryLevelByDay(ctx, teslaID, start, end)
}

// LatestMetricsForVehicles implements Reader as a silent pass-through — a
// dashboard-only read with no poller caller, off this tier's scope.
func (l *loggingReader) LatestMetricsForVehicles(ctx context.Context, refs []vehicleref.Ref) ([]VehicleStatus, error) {
	return l.inner.LatestMetricsForVehicles(ctx, refs)
}

// --- loggingRecalculator ---

// loggingRecalculator wraps the public Recalculator port and logs both of
// its methods. Unlike Reader, every Recalculator method already sits on the
// nightly/manual-write path, so there is no off-path method to keep silent.
type loggingRecalculator struct {
	inner Recalculator
}

// newLoggingRecalculator constructs a loggingRecalculator wrapping inner.
func newLoggingRecalculator(inner Recalculator) *loggingRecalculator {
	return &loggingRecalculator{inner: inner}
}

// Compile-time assertion: *loggingRecalculator must satisfy the full
// Recalculator interface. A future method added to Recalculator without a
// matching explicit override here fails to compile.
var _ Recalculator = (*loggingRecalculator)(nil)

// Recalculate implements Recalculator, logging BEFORE delegating — the
// arguments are known upfront, and the line must still appear if the write
// itself fails.
func (l *loggingRecalculator) Recalculate(ctx context.Context, teslaID int64, start, end time.Time) error {
	logging.Note("Recalculator", "Recalculate", "analytics query: tesla_id=%d start=%s end=%s",
		teslaID, start.UTC().Format("2006-01-02"), end.UTC().Format("2006-01-02"))
	return l.inner.Recalculate(ctx, teslaID, start, end)
}

// Reconcile implements Recalculator, logging BEFORE delegating.
//
// This line does not repeat when Reconcile internally re-derives a window
// and calls Recalculate directly on the concrete type — that call never
// passes through this decorator, so it prints no second line here. The
// window it used is still visible: it appears as the derived start/end on
// the telemetry query line immediately below this one in the log, printed by
// internal/telemetry's own decorator.
func (l *loggingRecalculator) Reconcile(ctx context.Context, teslaID int64) error {
	logging.Note("Recalculator", "Reconcile", "analytics query: tesla_id=%d", teslaID)
	return l.inner.Reconcile(ctx, teslaID)
}

// --- loggingGapWriter ---

// loggingGapWriter wraps the public GapWriter port and logs its single
// method — the nightly cycle's only write to charge_gaps.
type loggingGapWriter struct {
	inner GapWriter
}

// newLoggingGapWriter constructs a loggingGapWriter wrapping inner.
func newLoggingGapWriter(inner GapWriter) *loggingGapWriter {
	return &loggingGapWriter{inner: inner}
}

// Compile-time assertion: *loggingGapWriter must satisfy the full GapWriter
// interface. A future method added to GapWriter without a matching explicit
// override here fails to compile.
var _ GapWriter = (*loggingGapWriter)(nil)

// ReconcileWindow implements GapWriter, logging BEFORE delegating — the line
// must still appear even if the pre-transaction validation inside
// ReconcileWindow rejects the call.
func (l *loggingGapWriter) ReconcileWindow(ctx context.Context, teslaID int64, start, end time.Time, flagged []ChargeGap) error {
	logging.Note("GapWriter", "ReconcileWindow", "analytics query: tesla_id=%d start=%s end=%s flagged=%d",
		teslaID, start.UTC().Format("2006-01-02"), end.UTC().Format("2006-01-02"), len(flagged))
	return l.inner.ReconcileWindow(ctx, teslaID, start, end, flagged)
}

// --- loggingMonthlySyncer ---

// loggingMonthlySyncer wraps the public MonthlySyncer port and logs its
// single method -- the nightly cycle's only write to
// vehicle_monthly_metrics.
type loggingMonthlySyncer struct {
	inner MonthlySyncer
}

// newLoggingMonthlySyncer constructs a loggingMonthlySyncer wrapping inner.
func newLoggingMonthlySyncer(inner MonthlySyncer) *loggingMonthlySyncer {
	return &loggingMonthlySyncer{inner: inner}
}

// Compile-time assertion: *loggingMonthlySyncer must satisfy the full
// MonthlySyncer interface. A future method added to it without a matching
// explicit override here fails to compile.
var _ MonthlySyncer = (*loggingMonthlySyncer)(nil)

// SyncMonth implements MonthlySyncer, logging AFTER delegating so the
// logged AllDayCount/CapacityMeasured reflect the row actually stored.
func (l *loggingMonthlySyncer) SyncMonth(ctx context.Context, teslaID int64, period time.Time) (VehicleMonthlyMetrics, error) {
	result, err := l.inner.SyncMonth(ctx, teslaID, period)
	logging.Note("MonthlySyncer", "SyncMonth",
		"analytics query: tesla_id=%d period=%s all_day_count=%d capacity_measured=%t",
		teslaID, period.Format("2006-01"), result.AllDayCount, result.CapacityMeasured)
	return result, err
}
