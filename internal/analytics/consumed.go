// File consumed.go owns the D13 per-day battery-consumed derivation --
// combining a vehicle's snapshot history with both charging-cost sources
// (Supercharger sessions, manual entries) into a vehicleMetricRow per fetched
// snapshot day: the corrected per-day consumed percentage plus gap detection
// (D5/D5a) and inferred-source classification (D7a) for a day WITH a
// computable predecessor, or the day's raw observations alone (every derived
// field left NULL, flagged forced false) for a day WITHOUT one -- the
// dense-table revision (design.md D9/D10). Fully offline: no I/O, only plain
// telemetry.Snapshot / charging.Session / charging.Entry values
// in, []vehicleMetricRow out (mirrors derive.go's zero-I/O style).
package analytics

import (
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/clock"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// minFlagDistanceKm -- see design.md D-B8.
//
// minFlagDistanceKm is the D5 gap-detection threshold: a day whose consumed
// pct computes to exactly zero is flagged only when the vehicle demonstrably
// drove more than this many km that day. Below it, "zero consumed, zero
// driven" is a plausible parked day, not a data gap.
const minFlagDistanceKm = 10.0

// vehicleMetricRow is deriveVehicleMetrics' output shape: every
// vehicle_metrics column except id/created_at/updated_at. Recalculate
// (recalculate.go) is the only caller of deriveVehicleMetrics and the only
// place this type is converted to analyticsdb.UpsertVehicleMetricParams
// (mapping.go's pg* helpers).
type vehicleMetricRow struct {
	TeslaID    int64
	MetricDate time.Time

	// Duplicated raw observations -- always populated from the day's own
	// snapshot, independent of predecessor existence.
	BatteryLevelPct int
	OdometerKm      float64
	BatteryRangeKm  float64

	// The eight vehicle-status observations -- copied verbatim from cur,
	// populated in BOTH the predecessor-exists and no-predecessor branches,
	// exactly like the three raw observations above and UNLIKE the five
	// _calc columns below. Pointer-typed on this struct even though several
	// source fields on telemetry.Snapshot are non-pointer, because the
	// destination DB columns are nullable and this struct is the
	// pre-mapping shape (mapping.go's pg*FromPtr helpers expect a pointer).
	Locked            *bool
	SentryMode        *bool
	CarVersion        *string
	InsideTempC       *float64
	OutsideTempC      *float64
	ChargingState     *string
	ChargeLimitSocPct *int
	CapturedAt        *time.Time

	// MaxRangeChargeCounter is a ninth raw observation on exactly the same
	// terms as the eight above -- copied verbatim from cur in BOTH branches,
	// never derived. Already a *int on telemetry.Snapshot (nil = the vehicle
	// did not report it, or the snapshot predates telemetry's own extraction),
	// so unlike Locked/CarVersion it is assigned directly rather than
	// address-taken; that nil travels through to a SQL NULL untouched. A
	// reported 0 is a real value ("never charged to max range") and must stay
	// distinguishable from nil.
	MaxRangeChargeCounter *int

	// The four TPMS (tire-pressure) columns are a raw observation on exactly
	// the same terms as MaxRangeChargeCounter above -- copied verbatim from
	// cur in BOTH branches, never derived, never converted (telemetry.Snapshot
	// already stores them in PSI). Already *float64 on telemetry.Snapshot, so
	// assigned directly, not address-taken.
	TpmsPressureFLPSI *float64
	TpmsPressureFRPSI *float64
	TpmsPressureRLPSI *float64
	TpmsPressureRRPSI *float64

	// The five _calc columns -- computed by consumption.go's
	// deriveConsumption from this row's snapshot pair. This is the only
	// place they are produced: the derivation used to run inside telemetry
	// and be copied here verbatim, until this module took over both the
	// figures and the columns that hold them. The first three are nil iff
	// this row's day has no predecessor snapshot at all; the two efficiency
	// figures carry that condition plus the divisor guard -- nil also when
	// ConsumedPct <= 0 (the divisor used to be the raw battery drop, which
	// NULLed every day the vehicle drove AND charged).
	DistanceTraveledKmCalc      *float64
	BatteryUsedPctCalc          *int
	KmPerPctCalc                *float64
	EfficiencyRange100PctKmCalc *float64 // delta:allow: km_per_pct_calc projected to 100%, a ratio -- not a day-over-day delta
	DaysSpannedCalc             *int

	// The three travel-progress day-over-day deltas: each stores this row's
	// value of the named figure minus the value on the row the derivation
	// loop built immediately before it in the current pass (not necessarily
	// yesterday's stored row -- see deriveVehicleMetrics). nil when this row
	// is the first the current pass processed, or when either side of the
	// subtraction is itself nil.
	DistanceTraveledKmDeltaCalc *float64
	ConsumedPctDeltaCalc        *float64
	KmPerPctDeltaCalc           *float64

	// The four tyre-pressure day-over-day deltas, computed by
	// consumption.go's deriveConsumption from this row's snapshot pair. Same
	// nil rule as the five _calc columns above: nil iff this row's day has no
	// predecessor snapshot at all, OR either day's own raw wheel reading is
	// itself nil.
	TpmsPressureFLPSIDeltaCalc *float64
	TpmsPressureFRPSIDeltaCalc *float64
	TpmsPressureRLPSIDeltaCalc *float64
	TpmsPressureRRPSIDeltaCalc *float64

	// The charge-corrected consumption figure. ConsumedPct is nil under the
	// identical "no predecessor" condition as BatteryUsedPctCalc -- there is
	// no raw delta to correct. Flagged is NEVER a pointer: it is always
	// false (not unknown) on a predecessor-less row -- there is no
	// gap-detection question to ask about a day with no computed
	// consumption figure at all, so the flag comparison never runs for such
	// a row.
	ConsumedPct         *float64
	Flagged             bool
	MissingChargingType MissingChargingType // "" maps to SQL NULL (mapping.go)
}

// effectiveDay returns the calendar day snapshot s DESCRIBES: its own
// CapturedDate minus one calendar day. The nightly poller runs at ~03:30 and
// captures the state accumulated over the PRIOR day, so the row's day is one
// before its capture day.
//
// The zone is the poller's configured Config.Location (roadmap D6, owner
// ruling D18), and it arrives here already applied: telemetry stamps
// CapturedDate on the write path via clock.CalendarDay(capturedAt, loc)
// (internal/telemetry/service.go, RM35-telemetry-adopt-clock), which is the
// single place that zone decides a snapshot's calendar day. This module
// therefore needs no *time.Location of its own (design.md D-B12).
//
// clock.CalendarDay is called here with time.UTC, not clock.Zone() --
// CapturedDate already arrives as a bare pgtype.Date-shaped value (UTC
// midnight, no time-of-day component), so this call is NOT a timezone
// conversion and NOT a bucketing decision: it never moves the value across a
// day boundary, it only strips a stray time-of-day component that could not
// exist for a well-formed CapturedDate but is normalized defensively anyway
// (RM35-analytics-adopt-clock design.md). Passing clock.Zone() here would
// silently re-bucket an already-normalized day -- do not "improve" this.
//
// Deliberately does NOT read s.EffectiveDate: that field is derived as
// CapturedAt.AddDate(0,0,-1) with CapturedAt in UTC (telemetry/mapping.go), so
// its calendar day is a UTC answer, and a bare date cannot be re-zoned after
// the fact (design.md D-B7). AddDate is calendar-day arithmetic, not a 24h
// duration, so DST cannot shift it.
//
// Invariant, asserted by test (m): for any consecutive pair,
// effectiveDay(cur) - effectiveDay(prev) == *DaysSpannedCalc, because
// deriveConsumption (consumption.go) derives DaysSpannedCalc from the same
// two CapturedDate values.
func effectiveDay(s telemetry.Snapshot) time.Time {
	return clock.CalendarDay(s.CapturedDate, time.UTC).AddDate(0, 0, -1)
}

// sumSuperchargerPctBetween sums (EndBatteryPct - StartBatteryPct) across
// every session whose ChargeStopDateTime falls in [from, to) -- D12's
// interval rule, generalized to any predecessor/current CapturedAt pair
// (design.md D-B5). Sessions with either percentage NULL (D14: always true
// today) contribute 0 -- their absence is what inferMissingChargingType
// flags below, not something this function should estimate.
func sumSuperchargerPctBetween(sessions []charging.Session, from, to time.Time) float64 {
	var total float64
	for _, s := range sessions {
		if s.ChargeStopDateTime.Before(from) || !s.ChargeStopDateTime.Before(to) {
			continue
		}
		if s.StartBatteryPct != nil && s.EndBatteryPct != nil {
			total += float64(*s.EndBatteryPct - *s.StartBatteryPct)
		}
	}
	return total
}

// inferMissingChargingType implements D7a: SUPERCHARGER when a session in
// [from, to) exists with either battery percentage NULL (the exact record
// needing a fill is already known); MANUAL otherwise. Only called once a day
// is already known to be Flagged.
func inferMissingChargingType(sessions []charging.Session, from, to time.Time) MissingChargingType {
	for _, s := range sessions {
		if s.ChargeStopDateTime.Before(from) || !s.ChargeStopDateTime.Before(to) {
			continue
		}
		if s.StartBatteryPct == nil || s.EndBatteryPct == nil {
			return MissingChargingTypeSupercharger
		}
	}
	return MissingChargingTypeManual
}

// sumManualPctBetween sums BatteryDelta() across every manual entry whose
// clock.CalendarDay(ChargedOn, time.UTC) falls in (fromDay, toDay] --
// design.md D-B6's exclusive-start/inclusive-end range, generalized from the
// single-day D12 rule to cover multi-day spans without a second code path.
// fromDay/toDay are the caller's zoned effectiveDay values, not raw snapshot
// timestamps. time.UTC, not clock.Zone(), for the same reason effectiveDay
// above uses it: ChargedOn already arrives as a bare, UTC-midnight-normalized
// date (RM35-analytics-adopt-clock design.md). Entries with either battery
// percentage NULL contribute 0 (BatteryDelta() returns nil in that case).
func sumManualPctBetween(entries []charging.Entry, fromDay, toDay time.Time) float64 {
	var total float64
	for _, e := range entries {
		day := clock.CalendarDay(e.ChargedOn, time.UTC)
		if !day.After(fromDay) || day.After(toDay) {
			continue
		}
		if delta := e.BatteryDelta(); delta != nil {
			total += float64(*delta)
		}
	}
	return total
}

// deriveVehicleMetrics is the pure per-day derivation, fully offline: no
// I/O, only plain telemetry.Snapshot / charging.Session / charging.Entry
// values in, []vehicleMetricRow out. snapshots MUST be ordered
// chronologically ascending and MUST include the one-day lookback row
// before start when it exists (Recalculate, recalculate.go, guarantees
// both, widening every fetch by a day for the zone shift).
//
// start/end are inclusive bare calendar dates and are compared against each
// row's ZONED effective day (effectiveDay), which is why the caller may
// hand this function rows just outside [start, end] -- they are filtered
// here, against the same day definition the emitted MetricDate carries.
//
// preceding is the vehicle's true immediate predecessor for snapshots[0] --
// the row Recalculate fetched via telemetry.Reader.SnapshotPrecedingDay,
// nil exactly when the vehicle has no earlier stored snapshot at all. It is
// used as prev for i == 0 only; for i >= 1 the predecessor is
// snapshots[i-1], guaranteed to be the true immediate one because
// SnapshotsByVehicleBetween returns a contiguous window with nothing
// skipped inside it. Passing it in (rather than looking it up here) keeps
// this function pure and offline-testable -- the lookup is the caller's
// I/O, exactly as the three existing fetches are.
//
// The loop iterates EVERY fetched index i := 0 to len(snapshots)-1, so it
// visits snapshots[0] as cur too. The predecessor-less branch triggers on
// prev == nil alone -- this is the STRONGER of two possible signals,
// because prev == nil consults the database (via preceding) rather than
// trusting a field a past write path happened to leave nil.
//
// For a predecessor-less row the emitted row carries only the day's raw
// observations from cur, with flagged forced false and every
// derived/consumed field left nil -- a stored 0 would falsely flag a
// vehicle's first day as a suspected charge gap, since there is no prior
// reading to compare against. For every other row, the charge-event
// matching is against prev.CapturedAt/effectiveDay(prev), and the row
// carries cur.BatteryLevelPct, cur.OdometerKm, cur.BatteryRangeKm plus the
// figures deriveConsumption (consumption.go) computes from the (prev, cur)
// pair and that span's charge total.
//
// The charge-corrected percentage (chargePct) is matched and summed here,
// but the addition that turns it into consumed_pct happens once inside
// deriveConsumption, which needs that same number as the divisor for the
// efficiency ratio. This function reads the result back off
// calc.ConsumedPct rather than computing the identical sum a second time.
//
// The three travel-progress deltas (DistanceTraveledKmDeltaCalc,
// ConsumedPctDeltaCalc, KmPerPctDeltaCalc) are computed against
// out[len(out)-1] -- the row THIS pass built immediately before the
// current one -- never against whatever row happens to be stored for
// yesterday. When out is empty (this is the first row this pass
// processed), all three stay nil: there is nothing in this pass to
// subtract from yet, even if the database holds an earlier day. A later
// pass whose window includes that earlier day fills the delta in.
func deriveVehicleMetrics(preceding *telemetry.Snapshot, snapshots []telemetry.Snapshot, sessions []charging.Session, entries []charging.Entry, start, end time.Time) []vehicleMetricRow {
	out := make([]vehicleMetricRow, 0, len(snapshots))
	for i := 0; i < len(snapshots); i++ {
		cur := snapshots[i]
		day := effectiveDay(cur)
		if day.Before(start) || day.After(end) {
			continue
		}

		// The sole predecessor signal: snapshots[i-1] inside the fetched
		// window, the caller's SnapshotPrecedingDay result at its left edge.
		prev := preceding
		if i >= 1 {
			prev = &snapshots[i-1]
		}

		if prev == nil {
			// No predecessor for this row anywhere in storage -- persist the
			// day's raw observations only. Every derived/consumed field stays
			// nil (-> SQL NULL). flagged is forced false here, never left to a
			// stray zero-value comparison against distance.
			out = append(out, vehicleMetricRow{
				TeslaID:           cur.TeslaID,
				MetricDate:        day,
				BatteryLevelPct:   cur.BatteryLevelPct,
				OdometerKm:        cur.OdometerKm,
				BatteryRangeKm:    cur.BatteryRangeKm,
				Locked:            &cur.Locked,
				SentryMode:        cur.SentryMode,
				CarVersion:        &cur.CarVersion,
				InsideTempC:       &cur.InsideTempC,
				OutsideTempC:      &cur.OutsideTempC,
				ChargingState:     &cur.ChargingState,
				ChargeLimitSocPct: &cur.ChargeLimitSocPct,
				CapturedAt:        &cur.CapturedAt,

				MaxRangeChargeCounter: cur.MaxRangeChargeCounter,
				TpmsPressureFLPSI:     cur.TpmsPressureFLPSI,
				TpmsPressureFRPSI:     cur.TpmsPressureFRPSI,
				TpmsPressureRLPSI:     cur.TpmsPressureRLPSI,
				TpmsPressureRRPSI:     cur.TpmsPressureRRPSI,
				Flagged:               false,
			})
			continue
		}

		// The charge total for this day's span, matched here and summed here --
		// this file owns WHICH records count (the two interval rules below);
		// consumption.go owns what the total is then used for.
		chargePct := sumSuperchargerPctBetween(sessions, prev.CapturedAt, cur.CapturedAt) +
			sumManualPctBetween(entries, effectiveDay(*prev), day)

		calc := deriveConsumption(prev, cur, chargePct)

		// Never re-added here. calc.ConsumedPct IS BatteryUsedPctCalc +
		// chargePct, computed once in consumption.go, which also divides by
		// it. Non-nil whenever prev != nil, which the branch above
		// guarantees.
		consumed := *calc.ConsumedPct

		var distanceKm float64
		if calc.DistanceTraveledKmCalc != nil {
			distanceKm = *calc.DistanceTraveledKmCalc
		}

		flagged := consumed < 0 || (consumed == 0 && distanceKm > minFlagDistanceKm)

		var missingType MissingChargingType
		if flagged {
			missingType = inferMissingChargingType(sessions, prev.CapturedAt, cur.CapturedAt)
		}

		// The three travel-progress deltas, against the row this SAME pass
		// built immediately before this one -- not against whatever the
		// database holds for yesterday. Empty out (this is the first row
		// this pass processed) leaves all three nil: there is nothing yet
		// in this pass to subtract from.
		var distanceDelta, consumedDelta, kmPerPctDelta *float64
		if len(out) > 0 {
			prevRow := out[len(out)-1]
			distanceDelta = dayOverDayDelta(prevRow.DistanceTraveledKmCalc, calc.DistanceTraveledKmCalc)
			consumedDelta = dayOverDayDelta(prevRow.ConsumedPct, calc.ConsumedPct)
			kmPerPctDelta = dayOverDayDelta(prevRow.KmPerPctCalc, calc.KmPerPctCalc)
		}

		out = append(out, vehicleMetricRow{
			TeslaID:                     cur.TeslaID,
			MetricDate:                  day,
			BatteryLevelPct:             cur.BatteryLevelPct,
			OdometerKm:                  cur.OdometerKm,
			BatteryRangeKm:              cur.BatteryRangeKm,
			Locked:                      &cur.Locked,
			SentryMode:                  cur.SentryMode,
			CarVersion:                  &cur.CarVersion,
			InsideTempC:                 &cur.InsideTempC,
			OutsideTempC:                &cur.OutsideTempC,
			ChargingState:               &cur.ChargingState,
			ChargeLimitSocPct:           &cur.ChargeLimitSocPct,
			CapturedAt:                  &cur.CapturedAt,
			MaxRangeChargeCounter:       cur.MaxRangeChargeCounter,
			TpmsPressureFLPSI:           cur.TpmsPressureFLPSI,
			TpmsPressureFRPSI:           cur.TpmsPressureFRPSI,
			TpmsPressureRLPSI:           cur.TpmsPressureRLPSI,
			TpmsPressureRRPSI:           cur.TpmsPressureRRPSI,
			DistanceTraveledKmCalc:      calc.DistanceTraveledKmCalc,
			BatteryUsedPctCalc:          calc.BatteryUsedPctCalc,
			KmPerPctCalc:                calc.KmPerPctCalc,
			EfficiencyRange100PctKmCalc: calc.EfficiencyRange100PctKmCalc, // delta:allow: a ratio, not a delta
			DaysSpannedCalc:             calc.DaysSpannedCalc,
			DistanceTraveledKmDeltaCalc: distanceDelta,
			ConsumedPctDeltaCalc:        consumedDelta,
			KmPerPctDeltaCalc:           kmPerPctDelta,
			TpmsPressureFLPSIDeltaCalc:  calc.TpmsPressureFLPSIDeltaCalc,
			TpmsPressureFRPSIDeltaCalc:  calc.TpmsPressureFRPSIDeltaCalc,
			TpmsPressureRLPSIDeltaCalc:  calc.TpmsPressureRLPSIDeltaCalc,
			TpmsPressureRRPSIDeltaCalc:  calc.TpmsPressureRRPSIDeltaCalc,
			ConsumedPct:                 calc.ConsumedPct,
			Flagged:                     flagged,
			MissingChargingType:         missingType,
		})
	}
	return out
}
