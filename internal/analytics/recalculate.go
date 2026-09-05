// File recalculate.go implements the Recalculator port (analytics.go) --
// this module's write path onto its own precomputed read model,
// vehicle_metrics (design.md D11 for Recalculate, D7/D8 for Reconcile). It
// is the only file in this module that writes analyticsdb; reader.go is the
// only file that reads it (module's existing analytics.go = "public port
// declarations" / reader.go|recalculate.go = "port implementations" split,
// design.md task 3.4).
package analytics

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	analyticsdb "github.com/cristianpena/magus-tesla-api/internal/analytics/db"
	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/clock"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// recalcOverlap is the D4 commit-skew guard (carries IO-4): every Reconcile
// read queries a source with updated_at >= cursor - recalcOverlap, so a
// transaction that commits after Reconcile's own cursor read is still picked
// up on the next run. Recalculate's UPSERT is idempotent on
// (account_id, tesla_id, metric_date), so a row this overlap re-reads
// unchanged produces a byte-identical write -- a correctness no-op paid for
// in a handful of extra read rows (design.md D4). Mirrors reader.go's own
// chargingSourceLimit named-constant convention.
const recalcOverlap = 24 * time.Hour

// The three independent watermark sources (design.md D3, carries IO-3),
// named after the physical table each source's data lives in --
// self-describing, mirrors MissingChargingType's 'MANUAL'/
// 'SUPERCHARGER' free-standing string-label convention. No FK: just a label
// (see vehicle_metric_watermarks' source CHECK vocabulary).
const (
	sourceVehicleSnapshots     = "vehicle_snapshots"
	sourceSuperchargerSessions = "supercharger_sessions"
	sourceManualChargeEntries  = "manual_charge_entries"
)

// recalculator is the concrete implementation of the Recalculator port
// (design.md D11). It is NOT part of the fake-testable seam reader.go's
// vehicleMetricsStore interface provides for the two SELECT-only Reader
// methods -- its own DB-integration tests (Wave 6) exercise it directly
// against a real database, mirroring this package's own gapWriter
// precedent (a write-path port not shaped for an offline fake).
type recalculator struct {
	pool         *pgxpool.Pool
	q            *analyticsdb.Queries
	telemetry    telemetry.Reader
	supercharger charging.SuperchargerSessionAnalyticsReader
	manual       charging.Reader
}

// Compile-time assertion: *recalculator must satisfy the public Recalculator interface.
var _ Recalculator = (*recalculator)(nil)

// NewRecalculator constructs a Recalculator over the analytics module's own
// database pool plus the three sibling ports it reads to derive each day's
// row (design.md D11).
func NewRecalculator(pool *pgxpool.Pool, telemetryReader telemetry.Reader, supercharger charging.SuperchargerSessionAnalyticsReader, manual charging.Reader) Recalculator {
	return &recalculator{
		pool:         pool,
		q:            analyticsdb.New(pool),
		telemetry:    telemetryReader,
		supercharger: supercharger,
		manual:       manual,
	}
}

// Recalculate implements Recalculator (design.md D11). It fetches the
// identical 1-day/2-day-widened lookback window ConsumedByDay's reader.go
// used to fetch live before this tier (copied, not redesigned), looks up the
// exact predecessor for the window's first row (RM29 tier 4 D7 -- the module
// derives the five _calc figures itself now, so a multi-day capture gap needs
// a real predecessor rather than whatever happens to sit inside the window),
// widens both charge-source fetches back to that predecessor's day when it
// precedes the normal lookback (D8b), runs
// deriveVehicleMetrics (consumed.go, now dense per D10) over the fetched
// data, UPSERTs every produced row, then deletes any existing vehicle_metrics
// row in [start, end] whose metric_date is NOT among the rows just produced
// (self-healing symmetry with this package's own GapWriter.ReconcileWindow's
// UPSERT+DELETE shape for charge_gaps -- same shape, reused rather than
// invented). Under the dense-table revision, "stale" means "no snapshot
// exists for that day at all anymore".
//
// The UPSERTs and the DELETE run inside one transaction (mirrors
// internal/account's own transactional writer and this package's own gapWriter
// (gap_writer.go) -- the other two transactional writers in this codebase --
// and their shared Begin/WithTx/Commit convention) so a Recalculate call either
// fully applies or has no effect -- Reconcile's D4 overlap re-read makes a retry of a partially
// failed call a correctness no-op regardless, but the transaction avoids
// ever persisting a half-written window.
func (r *recalculator) Recalculate(ctx context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) error {
	lookbackStart := start.AddDate(0, 0, -1)

	snapshots, err := r.telemetry.SnapshotsByVehicleBetween(ctx, accountID, teslaID, lookbackStart, end.AddDate(0, 0, 1))
	if err != nil {
		return fmt.Errorf("fetching snapshots: %w", err)
	}

	// D7: the exact predecessor for snapshots[0]. One indexed single-row read,
	// issued UNCONDITIONALLY whenever the window returned rows -- deliberately
	// NOT skipped when snapshots[0] is merely the one-day lookback row whose
	// own predecessor goes unused. That branch could only ever save a
	// microsecond on a write path the Performance-Profile explicitly gives
	// latitude to, and mis-deriving its guard costs a silently-NULLed day
	// rather than an error.
	//
	// A query error ABORTS Recalculate and is never degraded to "no
	// predecessor": doing so would silently NULL out a real vehicle's figures
	// on a transient DB hiccup (telemetry.Reader.SnapshotPrecedingDay's own doc
	// comment draws the same distinction).
	var preceding *telemetry.Snapshot
	if len(snapshots) > 0 {
		preceding, err = r.telemetry.SnapshotPrecedingDay(ctx, accountID, teslaID, snapshots[0].CapturedDate)
		if err != nil {
			return fmt.Errorf("fetching preceding snapshot: %w", err)
		}
	}

	// D8b: widen BOTH charge-source fetches across a capture gap. Reaching
	// further back for the predecessor without reaching further back for that
	// span's charge events produces a wrong consumed_pct for exactly the gap
	// days the predecessor lookup exists to recover -- and because the error is
	// negative, the D5/D5a rule then fires a FALSE "missing charge record"
	// alarm on a day that was correctly charged.
	//
	// effectiveDay(*preceding) rather than clock.CalendarDay(preceding.CapturedAt, time.UTC) --
	// one day more generous than strictly required, matching tier 3 D8's
	// "coarse and generous, not pixel-exact" precedent. Over-fetching only
	// costs rows read; it can never change a result, because both sum*Between
	// helpers (consumed.go) re-filter to the exact interval in Go.
	//
	// Note what this actually does, because design.md D8b's prose first got it
	// wrong: effectiveDay is clock.CalendarDay(CapturedDate, time.UTC) - 1, so even the ordinary
	// one-day lookback row at start-1d yields start-2d, which is Before
	// lookbackStart. chargeStart therefore drops to start-2d on EVERY call, not
	// only across a gap -- the charge fetches are permanently one day wider than
	// they were before this decision. That is deliberate and harmless: both
	// sum...Between helpers re-filter to the exact interval in Go, so the extra
	// day can only cost rows read, never change a result. The gap widening on
	// top of that still fires only when a real gap exists.
	chargeStart := lookbackStart
	if preceding != nil {
		if d := effectiveDay(*preceding); d.Before(chargeStart) {
			chargeStart = d
		}
	}

	sessions, err := r.supercharger.ListSessionsByVehicleBetween(ctx, accountID, teslaID, chargeStart, end.AddDate(0, 0, 2))
	if err != nil {
		return fmt.Errorf("fetching supercharger sessions: %w", err)
	}

	entries, err := r.manual.ListEntriesByVehicleBetween(ctx, accountID, teslaID, chargeStart, end)
	if err != nil {
		return fmt.Errorf("fetching manual charge entries: %w", err)
	}

	rows := deriveVehicleMetrics(preceding, snapshots, sessions, entries, start, end)

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once committed

	qtx := r.q.WithTx(tx)

	metricDates := make([]pgtype.Date, 0, len(rows))
	for _, row := range rows {
		if err := qtx.UpsertVehicleMetric(ctx, upsertVehicleMetricParamsFrom(row)); err != nil {
			return fmt.Errorf("upserting vehicle metric for %s: %w", row.MetricDate, err)
		}
		metricDates = append(metricDates, dateFrom(row.MetricDate))
	}

	if err := qtx.DeleteVehicleMetricsInRangeExcept(ctx, analyticsdb.DeleteVehicleMetricsInRangeExceptParams{
		AccountID:   accountID,
		TeslaID:     teslaID,
		StartDate:   dateFrom(start),
		EndDate:     dateFrom(end),
		MetricDates: metricDates,
	}); err != nil {
		return fmt.Errorf("deleting stale vehicle metrics: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing tx: %w", err)
	}
	return nil
}

// upsertVehicleMetricParamsFrom converts one derived vehicleMetricRow
// (consumed.go) to the generated UPSERT's params, mapping every nullable
// domain pointer/zero-value through mapping.go's pg* helpers.
func upsertVehicleMetricParamsFrom(row vehicleMetricRow) analyticsdb.UpsertVehicleMetricParams {
	return analyticsdb.UpsertVehicleMetricParams{
		AccountID:              row.AccountID,
		TeslaID:                row.TeslaID,
		MetricDate:             dateFrom(row.MetricDate),
		BatteryLevelPct:        int32(row.BatteryLevelPct),
		OdometerKm:             row.OdometerKm,
		BatteryRangeKm:         row.BatteryRangeKm,
		DistanceTraveledKmCalc: pgFloat8FromPtr(row.DistanceTraveledKmCalc),
		BatteryUsedPctCalc:     pgInt4FromPtr(row.BatteryUsedPctCalc),
		KmPerPctCalc:           pgFloat8FromPtr(row.KmPerPctCalc),
		EstimatedRangeKmCalc:   pgFloat8FromPtr(row.EstimatedRangeKmCalc),
		DaysSpannedCalc:        pgInt4FromPtr(row.DaysSpannedCalc),
		ConsumedPct:            pgFloat8FromPtr(row.ConsumedPct),
		Flagged:                row.Flagged,
		MissingChargingType:    pgTextFromMissingType(row.MissingChargingType),
		Locked:                 pgBoolFromPtr(row.Locked),
		SentryMode:             pgBoolFromPtr(row.SentryMode),
		CarVersion:             pgTextFromPtr(row.CarVersion),
		InsideTempC:            pgFloat8FromPtr(row.InsideTempC),
		OutsideTempC:           pgFloat8FromPtr(row.OutsideTempC),
		ChargingState:          pgTextFromPtr(row.ChargingState),
		ChargeLimitSocPct:      pgInt4FromPtr(row.ChargeLimitSocPct),
		CapturedAt:             pgTimestamptzFromPtr(row.CapturedAt),
		MaxRangeChargeCounter:  pgInt4FromPtr(row.MaxRangeChargeCounter),
	}
}

// Reconcile implements Recalculator (design.md D7/D8). It reads each of the
// three independent per-source watermarks (a missing watermark treated as
// the epoch, D7 -- so a vehicle's first-ever Reconcile backfills that
// source's entire history in one pass), queries each source for data
// updated at or after (cursor - recalcOverlap) (D4's 24h commit-skew guard),
// derives the union affected date range across every source that returned
// at least one row (each source's own naive per-row date, min/max across
// ALL returned rows, widened +/-1 day, clamped to not exceed yesterday --
// D8's coarse-and-generous rule), calls Recalculate once for that single
// window (skipped entirely when no source returned rows at all), then
// advances each source's watermark whose query returned rows to the max
// UpdatedAt/updated_at observed on this run -- a source with zero returned
// rows leaves its own watermark row untouched (Reconcile's own idempotence
// contract, design.md's Test Contract).
func (r *recalculator) Reconcile(ctx context.Context, accountID uuid.UUID, teslaID int64) error {
	snapCursor, err := r.watermark(ctx, accountID, teslaID, sourceVehicleSnapshots)
	if err != nil {
		return fmt.Errorf("reading %s watermark: %w", sourceVehicleSnapshots, err)
	}
	scsCursor, err := r.watermark(ctx, accountID, teslaID, sourceSuperchargerSessions)
	if err != nil {
		return fmt.Errorf("reading %s watermark: %w", sourceSuperchargerSessions, err)
	}
	manualCursor, err := r.watermark(ctx, accountID, teslaID, sourceManualChargeEntries)
	if err != nil {
		return fmt.Errorf("reading %s watermark: %w", sourceManualChargeEntries, err)
	}

	snapshots, err := r.telemetry.SnapshotsByVehicleUpdatedSince(ctx, accountID, teslaID, snapCursor.Add(-recalcOverlap))
	if err != nil {
		return fmt.Errorf("fetching updated snapshots: %w", err)
	}
	sessions, err := r.supercharger.ListSessionsByVehicleUpdatedSince(ctx, accountID, teslaID, scsCursor.Add(-recalcOverlap))
	if err != nil {
		return fmt.Errorf("fetching updated supercharger sessions: %w", err)
	}
	entries, err := r.manual.ListEntriesByVehicleUpdatedSince(ctx, accountID, teslaID, manualCursor.Add(-recalcOverlap))
	if err != nil {
		return fmt.Errorf("fetching updated manual charge entries: %w", err)
	}

	if len(snapshots) == 0 && len(sessions) == 0 && len(entries) == 0 {
		return nil // nothing changed since the last reconciliation -- no-op
	}

	// clock.Now() supplies the instant (RM35-analytics-adopt-clock design.md
	// "recalculate.go:268" decision); the bucketing zone stays time.UTC, not
	// clock.Zone() -- this module owns no *time.Location of its own (D-B12),
	// and the effective-day values being widened against below (widen(...),
	// derived via effectiveDay/clock.CalendarDay(_, time.UTC)) are themselves
	// UTC-bucketed, so mixing in a different zone here would desynchronize
	// the clamp from the range it clamps. See design.md for the full
	// reasoning on why this is the behavior-preserving choice.
	yesterday := clock.CalendarDay(clock.Now(), time.UTC).AddDate(0, 0, -1)

	var minDay, maxDay time.Time
	widen := func(day time.Time) {
		if minDay.IsZero() || day.Before(minDay) {
			minDay = day
		}
		if maxDay.IsZero() || day.After(maxDay) {
			maxDay = day
		}
	}

	var maxSnapUpdated time.Time
	for _, s := range snapshots {
		widen(clock.CalendarDay(s.EffectiveDate, time.UTC))
		if s.UpdatedAt.After(maxSnapUpdated) {
			maxSnapUpdated = s.UpdatedAt
		}
	}
	var maxSessionUpdated time.Time
	for _, s := range sessions {
		widen(clock.CalendarDay(s.ChargeStopDateTime, time.UTC))
		if s.UpdatedAt.After(maxSessionUpdated) {
			maxSessionUpdated = s.UpdatedAt
		}
	}
	var maxEntryUpdated time.Time
	for _, e := range entries {
		widen(clock.CalendarDay(e.ChargedOn, time.UTC))
		if e.UpdatedAt.After(maxEntryUpdated) {
			maxEntryUpdated = e.UpdatedAt
		}
	}

	start := minDay.AddDate(0, 0, -1)
	end := maxDay.AddDate(0, 0, 1)
	if end.After(yesterday) {
		end = yesterday
	}

	if err := r.Recalculate(ctx, accountID, teslaID, start, end); err != nil {
		return fmt.Errorf("recalculating [%s, %s]: %w", start, end, err)
	}

	if len(snapshots) > 0 {
		if err := r.advanceWatermark(ctx, accountID, teslaID, sourceVehicleSnapshots, maxSnapUpdated); err != nil {
			return err
		}
	}
	if len(sessions) > 0 {
		if err := r.advanceWatermark(ctx, accountID, teslaID, sourceSuperchargerSessions, maxSessionUpdated); err != nil {
			return err
		}
	}
	if len(entries) > 0 {
		if err := r.advanceWatermark(ctx, accountID, teslaID, sourceManualChargeEntries, maxEntryUpdated); err != nil {
			return err
		}
	}

	return nil
}

// watermark reads one source's cursor for one vehicle. No stored row is
// treated as the epoch (time.Time{}, design.md D7) -- Reconcile's caller
// then queries "since epoch - recalcOverlap", which matches every row the
// source has ever stored, backfilling the vehicle's entire history for that
// source on its first-ever Reconcile call.
func (r *recalculator) watermark(ctx context.Context, accountID uuid.UUID, teslaID int64, source string) (time.Time, error) {
	ts, err := r.q.GetVehicleMetricWatermark(ctx, analyticsdb.GetVehicleMetricWatermarkParams{
		AccountID: accountID,
		TeslaID:   teslaID,
		Source:    source,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	return ts.Time, nil
}

// advanceWatermark upserts one source's cursor for one vehicle to the max
// updated_at observed on this Reconcile run (design.md D2/D3/D4). Only
// called for a source whose ...UpdatedSince query returned at least one row
// -- see Reconcile above.
func (r *recalculator) advanceWatermark(ctx context.Context, accountID uuid.UUID, teslaID int64, source string, observed time.Time) error {
	if err := r.q.UpsertVehicleMetricWatermark(ctx, analyticsdb.UpsertVehicleMetricWatermarkParams{
		AccountID:       accountID,
		TeslaID:         teslaID,
		Source:          source,
		SourceUpdatedAt: pgtype.Timestamptz{Time: observed, Valid: true},
	}); err != nil {
		return fmt.Errorf("advancing %s watermark: %w", source, err)
	}
	return nil
}
