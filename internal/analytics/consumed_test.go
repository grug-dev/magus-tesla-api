package analytics

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// The consumed tests exercise deriveVehicleMetrics and its helpers fully OFFLINE and
// with zero fakes: all are pure functions over plain telemetry.Snapshot /
// SuperchargerSession / charging.Entry values (design.md D-B1 through D-B13, D9/D10),
// so there is no I/O seam to fake -- mirrors derive_test.go's own zero-fakes style.
// Expected values are hand-computed from design.md's Test Contract (authored before
// this implementation existed), never derived by reading consumed.go itself.
//
// Per the Test-Execution-Policy, these tests are written but NOT run by the worker; go
// vet ./... compiles them as a signature-drift signal. The owner runs
// `go test ./internal/analytics/...` and reports the result.
//
// Snapshot-fixture convention (design.md Test Contract, binding, changed by D18): every
// telemetry.Snapshot fixture below sets both CapturedAt (the instant) and CapturedDate
// (the zoned capture day, from which effectiveDay derives the bucket day). Where a
// scenario says "effective day D", the fixture's CapturedDate is D + 1 day. EffectiveDate
// is left at its zero value in every fixture EXCEPT scenario (m) / T4.10, which sets it
// deliberately wrong on purpose -- see that test's own comment.
//
// deriveVehicleMetrics rename (design.md D10, dense-table revision): every test below
// that ported from the pre-dense deriveConsumedByDay ALSO narrows its [start, end]
// window to exclude the fixture's own "prev" snapshot's effective day (previously d0,
// now the window's start moves to d1/cur's day). This is a CALL-SHAPE change only --
// no expected VALUE below differs from what deriveConsumedByDay produced for the same
// scenario. It is required because the dense loop now iterates every fetched index
// (i := 0), so a "prev" snapshot sitting at index 0 would itself produce a second,
// unrelated raw-observations-only row if its own effective day fell inside the window
// -- exactly mirroring how Recalculate always calls this function with the
// NON-widened [start, end] (design.md D11), never the widened fetch range that brought
// "prev" into the input slice in the first place. Two tests are deliberately NOT
// call-shape-only ports because they target the exact scenario the dense revision
// changed: TestDeriveVehicleMetrics_NilBatteryUsedPctCalc_RawObservationsOnly (renamed
// from ...Skipped) and the three new TestDeriveVehicleMetrics_FixtureA/B/C tests below,
// authored directly from design.md's Test Contract.

// day returns a bare calendar date at UTC midnight -- this platform's date representation
// (the pgtype.Date convention; see consumed.go's calendarDay doc comment).
func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// mustFloat dereferences a *float64 that a given scenario guarantees is non-nil
// (a day WITH a computable predecessor, design.md D9), failing the test immediately
// if it is nil -- every ported (a)-(n) scenario below asserts against a row that has
// a predecessor, so a nil here is the bug under test, not a case to tolerate.
func mustFloat(t *testing.T, v *float64) float64 {
	t.Helper()
	if v == nil {
		t.Fatal("want non-nil *float64, got nil")
	}
	return *v
}

// TestDeriveVehicleMetrics_SingleSessionSingleDay_MatchesRoadmapExample covers
// design.md Test Contract (a) -- the roadmap's own verified worked example: one
// Supercharger session inside a single day's window, expect ConsumedPct = 11.
func TestDeriveVehicleMetrics_SingleSessionSingleDay_MatchesRoadmapExample(t *testing.T) {
	d0 := day(2026, 8, 13)                              // prev's effective day (outside the window -- lookback pairing only)
	d1 := day(2026, 8, 14)                              // cur's effective day
	t0 := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC) // prev.CapturedAt
	t1 := time.Date(2026, 8, 15, 8, 30, 0, 0, time.UTC) // cur.CapturedAt

	prev := telemetry.Snapshot{CapturedAt: t0, CapturedDate: d0.AddDate(0, 0, 1)}
	cur := telemetry.Snapshot{
		CapturedAt:         t1,
		CapturedDate:       d1.AddDate(0, 0, 1),
		BatteryUsedPctCalc: intPtr(22 - 73), // -51
		DaysSpannedCalc:    intPtr(1),
	}

	sessions := []telemetry.SuperchargerSession{
		{ChargeStopDateTime: t0.Add(6 * time.Hour), StartBatteryPct: intPtr(18), EndBatteryPct: intPtr(80)},
	}

	got := deriveVehicleMetrics([]telemetry.Snapshot{prev, cur}, sessions, nil, d1, d1)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(got), got)
	}
	entry := got[0]

	if !entry.MetricDate.Equal(d1) {
		t.Errorf("MetricDate: want %v, got %v", d1, entry.MetricDate)
	}
	wantConsumed := -51.0 + (80.0 - 18.0) // 11
	if !approxEqual(mustFloat(t, entry.ConsumedPct), wantConsumed) {
		t.Errorf("ConsumedPct: want %v, got %v", wantConsumed, entry.ConsumedPct)
	}
	if entry.Flagged {
		t.Error("want Flagged=false")
	}
}

// TestDeriveVehicleMetrics_TwoSessionsSameDay_SumsBoth_Not5 covers design.md Test
// Contract (b) -- the roadmap's own regression guard: two Supercharger sessions in the
// same day's window must be SUMMED (D13), not collapsed to the latest session's delta
// alone (-25) or to a first-start-to-last-end span (15). The correct result is 5.
func TestDeriveVehicleMetrics_TwoSessionsSameDay_SumsBoth_Not5(t *testing.T) {
	d0 := day(2026, 8, 13)
	d1 := day(2026, 8, 14)
	t0 := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 15, 8, 30, 0, 0, time.UTC)

	prev := telemetry.Snapshot{CapturedAt: t0, CapturedDate: d0.AddDate(0, 0, 1)}
	cur := telemetry.Snapshot{
		CapturedAt:         t1,
		CapturedDate:       d1.AddDate(0, 0, 1),
		BatteryUsedPctCalc: intPtr(30 - 75), // -45
		DaysSpannedCalc:    intPtr(1),
	}

	sessions := []telemetry.SuperchargerSession{
		{ChargeStopDateTime: t0.Add(4 * time.Hour), StartBatteryPct: intPtr(20), EndBatteryPct: intPtr(50)}, // earlier, +30
		{ChargeStopDateTime: t0.Add(8 * time.Hour), StartBatteryPct: intPtr(60), EndBatteryPct: intPtr(80)}, // later, +20
	}

	got := deriveVehicleMetrics([]telemetry.Snapshot{prev, cur}, sessions, nil, d1, d1)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(got), got)
	}

	gotConsumed := mustFloat(t, got[0].ConsumedPct)
	wantConsumed := -45.0 + (30.0 + 20.0) // 5 -- sum of both sessions
	wrongLatestOnly := -45.0 + 20.0       // -25 -- only the later session's delta
	wrongFirstToLastEnd := -45.0 + 60.0   // 15 -- first session's start to last session's end

	if !approxEqual(gotConsumed, wantConsumed) {
		t.Errorf("ConsumedPct: want %v (sum of both sessions), got %v", wantConsumed, gotConsumed)
	}
	if approxEqual(gotConsumed, wrongLatestOnly) {
		t.Errorf("ConsumedPct equals the latest-session-only value (%v) -- not summing every session", wrongLatestOnly)
	}
	if approxEqual(gotConsumed, wrongFirstToLastEnd) {
		t.Errorf("ConsumedPct equals the first-start-to-last-end value (%v) -- not summing every session", wrongFirstToLastEnd)
	}
}

// TestDeriveVehicleMetrics_NegativeFlagged covers design.md Test Contract (c): a
// negative corrected consumption with no charge events matched must flag, inferring
// MANUAL (no Supercharger session present in the interval).
func TestDeriveVehicleMetrics_NegativeFlagged(t *testing.T) {
	d0 := day(2026, 8, 13)
	d1 := day(2026, 8, 14)
	t0 := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 15, 8, 30, 0, 0, time.UTC)

	prev := telemetry.Snapshot{CapturedAt: t0, CapturedDate: d0.AddDate(0, 0, 1)}
	cur := telemetry.Snapshot{
		CapturedAt:         t1,
		CapturedDate:       d1.AddDate(0, 0, 1),
		BatteryUsedPctCalc: intPtr(50 - 60), // -10
		DaysSpannedCalc:    intPtr(1),
	}

	got := deriveVehicleMetrics([]telemetry.Snapshot{prev, cur}, nil, nil, d1, d1)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d", len(got))
	}
	entry := got[0]

	if !approxEqual(mustFloat(t, entry.ConsumedPct), -10) {
		t.Errorf("ConsumedPct: want -10, got %v", entry.ConsumedPct)
	}
	if !entry.Flagged {
		t.Error("want Flagged=true")
	}
	if entry.MissingChargingType != telemetry.MissingChargingTypeManual {
		t.Errorf("MissingChargingType: want MANUAL, got %v", entry.MissingChargingType)
	}
}

// TestDeriveVehicleMetrics_ZeroWithDistanceFlagged covers design.md Test Contract (d):
// a day whose corrected ConsumedPct computes to exactly 0 must flag when the vehicle
// demonstrably drove more than minFlagDistanceKm that day.
func TestDeriveVehicleMetrics_ZeroWithDistanceFlagged(t *testing.T) {
	d0 := day(2026, 8, 13)
	d1 := day(2026, 8, 14)
	t0 := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 15, 8, 30, 0, 0, time.UTC)

	prev := telemetry.Snapshot{CapturedAt: t0, CapturedDate: d0.AddDate(0, 0, 1)}
	cur := telemetry.Snapshot{
		CapturedAt:             t1,
		CapturedDate:           d1.AddDate(0, 0, 1),
		BatteryUsedPctCalc:     intPtr(-20),
		DistanceTraveledKmCalc: fp(50.0),
		DaysSpannedCalc:        intPtr(1),
	}

	entries := []charging.Entry{
		{ChargedOn: d1, StartBatteryPct: intPtr(30), EndBatteryPct: intPtr(50)}, // +20
	}

	got := deriveVehicleMetrics([]telemetry.Snapshot{prev, cur}, nil, entries, d1, d1)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d", len(got))
	}
	entry := got[0]

	if !approxEqual(mustFloat(t, entry.ConsumedPct), 0) {
		t.Errorf("ConsumedPct: want 0, got %v", entry.ConsumedPct)
	}
	if !entry.Flagged {
		t.Error("want Flagged=true: ConsumedPct=0 with DistanceKm=50.0 > minFlagDistanceKm")
	}
	if entry.MissingChargingType != telemetry.MissingChargingTypeManual {
		t.Errorf("MissingChargingType: want MANUAL, got %v", entry.MissingChargingType)
	}
}

// TestDeriveVehicleMetrics_ZeroWithLowDistanceNotFlagged covers design.md Test
// Contract (e): same zero-ConsumedPct setup as (d), but at or below minFlagDistanceKm
// must NOT flag. The threshold comparison is strict '>', so exactly 10.0 must NOT flag.
func TestDeriveVehicleMetrics_ZeroWithLowDistanceNotFlagged(t *testing.T) {
	d0 := day(2026, 8, 13)
	d1 := day(2026, 8, 14)
	t0 := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 15, 8, 30, 0, 0, time.UTC)

	cases := []struct {
		name       string
		distanceKm float64
	}{
		{"exactly at threshold (10.0) -- strict '>' means not flagged", 10.0},
		{"well under threshold (5.0)", 5.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prev := telemetry.Snapshot{CapturedAt: t0, CapturedDate: d0.AddDate(0, 0, 1)}
			cur := telemetry.Snapshot{
				CapturedAt:             t1,
				CapturedDate:           d1.AddDate(0, 0, 1),
				BatteryUsedPctCalc:     intPtr(-20),
				DistanceTraveledKmCalc: fp(tc.distanceKm),
				DaysSpannedCalc:        intPtr(1),
			}
			entries := []charging.Entry{
				{ChargedOn: d1, StartBatteryPct: intPtr(30), EndBatteryPct: intPtr(50)}, // +20 -> ConsumedPct = 0
			}

			got := deriveVehicleMetrics([]telemetry.Snapshot{prev, cur}, nil, entries, d1, d1)
			if len(got) != 1 {
				t.Fatalf("want exactly 1 entry, got %d", len(got))
			}
			if got[0].Flagged {
				t.Errorf("want Flagged=false at DistanceKm=%v", tc.distanceKm)
			}
		})
	}
}

// TestDeriveVehicleMetrics_NilBatteryUsedPctCalc_RawObservationsOnly covers design.md
// Test Contract (f), REVISED at the database design gate (D9/D10). Under the
// pre-dense deriveConsumedByDay this replaces, a row with no predecessor claim
// (BatteryUsedPctCalc = nil) was skipped entirely -- "no entry at all". Under the
// dense-table revision it is NOT skipped: a row is still written, carrying only cur's
// raw observations, every derived/consumed field left nil, and Flagged forced false
// (design.md D9's dedicated rationale -- a stored 0 would falsely flag the row as a
// suspected charge gap). This is the SAME production behavior Fixture C pins for a
// vehicle's true first-ever snapshot (TestDeriveVehicleMetrics_FixtureC below); this
// test instead exercises D10's SECOND, independent trigger for the identical "no
// predecessor" branch: cur.BatteryUsedPctCalc == nil even when a LOCAL array
// predecessor exists (i >= 1) -- telemetry's own signal takes precedence over local
// array position (design.md D10's "defense-in-depth" note).
//
// This expected value is DELIBERATELY DIFFERENT from the pre-revision test it
// replaces (len 0 -> len 1). That is not a weakened assertion: design.md D9/D10
// explicitly and extensively documents this exact scenario's new output as the
// database design gate's own revision, so this is the new, design-mandated contract
// for the case this test targets, not a code disagreement papered over.
func TestDeriveVehicleMetrics_NilBatteryUsedPctCalc_RawObservationsOnly(t *testing.T) {
	d0 := day(2026, 8, 13)
	d1 := day(2026, 8, 14)
	t0 := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 15, 8, 30, 0, 0, time.UTC)

	prev := telemetry.Snapshot{CapturedAt: t0, CapturedDate: d0.AddDate(0, 0, 1)}
	cur := telemetry.Snapshot{
		CapturedAt:         t1,
		CapturedDate:       d1.AddDate(0, 0, 1),
		BatteryUsedPctCalc: nil,
		BatteryLevelPct:    55,
		OdometerKm:         1200.0,
		BatteryRangeKm:     250.0,
		DaysSpannedCalc:    intPtr(1),
	}

	// Window excludes prev's own day (d0) so only cur (i=1, the case under test) is
	// evaluated -- prev's own i==0 raw-only row is Fixture C's scenario, tested
	// separately below with its own single-snapshot fixture.
	got := deriveVehicleMetrics([]telemetry.Snapshot{prev, cur}, nil, nil, d1, d1)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry (raw observations only, not skipped -- design.md D9/D10), got %d entries: %+v", len(got), got)
	}
	entry := got[0]

	if !entry.MetricDate.Equal(d1) {
		t.Errorf("MetricDate: want %v, got %v", d1, entry.MetricDate)
	}
	if entry.BatteryLevelPct != 55 {
		t.Errorf("BatteryLevelPct: want 55 (raw observation always populated), got %d", entry.BatteryLevelPct)
	}
	if entry.OdometerKm != 1200.0 {
		t.Errorf("OdometerKm: want 1200.0, got %v", entry.OdometerKm)
	}
	if entry.BatteryRangeKm != 250.0 {
		t.Errorf("BatteryRangeKm: want 250.0, got %v", entry.BatteryRangeKm)
	}
	if entry.DistanceTraveledKmCalc != nil {
		t.Errorf("DistanceTraveledKmCalc: want nil, got %v", *entry.DistanceTraveledKmCalc)
	}
	if entry.BatteryUsedPctCalc != nil {
		t.Errorf("BatteryUsedPctCalc: want nil, got %v", *entry.BatteryUsedPctCalc)
	}
	if entry.KmPerPctCalc != nil {
		t.Errorf("KmPerPctCalc: want nil, got %v", *entry.KmPerPctCalc)
	}
	if entry.EstimatedRangeKmCalc != nil {
		t.Errorf("EstimatedRangeKmCalc: want nil, got %v", *entry.EstimatedRangeKmCalc)
	}
	if entry.DaysSpannedCalc != nil {
		t.Errorf("DaysSpannedCalc: want nil, got %v", *entry.DaysSpannedCalc)
	}
	if entry.ConsumedPct != nil {
		t.Errorf("ConsumedPct: want nil, got %v", *entry.ConsumedPct)
	}
	if entry.Flagged {
		t.Error("want Flagged=false -- the D5/D5a comparison must not run for a no-predecessor row (design.md D9)")
	}
	if entry.MissingChargingType != "" {
		t.Errorf("MissingChargingType: want \"\" (maps to SQL NULL), got %v", entry.MissingChargingType)
	}
}

// TestDeriveVehicleMetrics_MultiDaySpan_OneEntry covers design.md Test Contract (g) /
// roadmap D8: a multi-day gap between snapshots produces exactly ONE bar, dated the
// later snapshot's own effective day (design.md D-B3), with every charge event inside
// the span summed regardless of which intervening day it falls on. No entry exists for
// any intervening day -- there is no snapshot row for them to attach to.
func TestDeriveVehicleMetrics_MultiDaySpan_OneEntry(t *testing.T) {
	prevDay := day(2026, 8, 10)
	curDay := day(2026, 8, 13)
	t0 := time.Date(2026, 8, 11, 8, 30, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC)

	prev := telemetry.Snapshot{CapturedAt: t0, CapturedDate: prevDay.AddDate(0, 0, 1)}
	cur := telemetry.Snapshot{
		CapturedAt:         t1,
		CapturedDate:       curDay.AddDate(0, 0, 1),
		BatteryUsedPctCalc: intPtr(-30),
		DaysSpannedCalc:    intPtr(3),
	}

	sessions := []telemetry.SuperchargerSession{
		{ChargeStopDateTime: t0.Add(24 * time.Hour), StartBatteryPct: intPtr(40), EndBatteryPct: intPtr(55)}, // +15
	}
	entries := []charging.Entry{
		{ChargedOn: day(2026, 8, 12), StartBatteryPct: intPtr(20), EndBatteryPct: intPtr(45)}, // +25
	}

	// start is the day AFTER prev's own effective day (2026-08-10) -- excludes prev's
	// i==0 raw-observation row from this window, matching Recalculate's real call
	// shape (D11: the [start, end] arg passed to deriveVehicleMetrics is never
	// widened, only the FETCH is). end stays broad to keep exercising the
	// no-entry-for-intervening-days assertion below.
	start := prevDay.AddDate(0, 0, 1) // 2026-08-11
	end := day(2026, 8, 31)

	got := deriveVehicleMetrics([]telemetry.Snapshot{prev, cur}, sessions, entries, start, end)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry (not one per intervening day), got %d: %+v", len(got), got)
	}
	entry := got[0]

	if !entry.MetricDate.Equal(curDay) {
		t.Errorf("MetricDate: want %v (the row's own effective day, never re-attributed), got %v", curDay, entry.MetricDate)
	}
	if entry.DaysSpannedCalc == nil || *entry.DaysSpannedCalc != 3 {
		t.Errorf("DaysSpannedCalc: want 3, got %v", entry.DaysSpannedCalc)
	}
	wantConsumed := -30.0 + 40.0 // -30 + 15 (Supercharger) + 25 (manual)
	if !approxEqual(mustFloat(t, entry.ConsumedPct), wantConsumed) {
		t.Errorf("ConsumedPct: want %v, got %v", wantConsumed, entry.ConsumedPct)
	}
	if entry.Flagged {
		t.Error("want Flagged=false")
	}

	for _, e := range got {
		if e.MetricDate.Equal(day(2026, 8, 11)) || e.MetricDate.Equal(day(2026, 8, 12)) {
			t.Errorf("unexpected entry for intervening day %v -- multi-day spans must not be special-cased into extra bars", e.MetricDate)
		}
	}
}

// TestSumSuperchargerPctBetween_IntervalBoundary_InclusiveStartExclusiveEnd covers
// design.md Test Contract (h) / roadmap D12: the matching interval is [from, to) --
// lower-inclusive, upper-exclusive -- for both sumSuperchargerPctBetween and
// inferMissingChargingType, which share the identical boundary predicate. Both helpers
// are unchanged by the dense-table revision (design.md D10 -- "every existing helper
// unchanged"), so this test is unmodified from the pre-revision file.
func TestSumSuperchargerPctBetween_IntervalBoundary_InclusiveStartExclusiveEnd(t *testing.T) {
	from := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC)
	to := time.Date(2026, 8, 15, 8, 30, 0, 0, time.UTC)

	sessionAtFrom := telemetry.SuperchargerSession{ChargeStopDateTime: from, StartBatteryPct: intPtr(10), EndBatteryPct: intPtr(30)} // +20, included (lower-inclusive)
	sessionAtTo := telemetry.SuperchargerSession{ChargeStopDateTime: to, StartBatteryPct: intPtr(40), EndBatteryPct: intPtr(90)}     // +50, excluded (upper-exclusive)

	got := sumSuperchargerPctBetween([]telemetry.SuperchargerSession{sessionAtFrom, sessionAtTo}, from, to)
	want := 20.0
	if !approxEqual(got, want) {
		t.Errorf("sumSuperchargerPctBetween: want %v (only the session exactly AT 'from'), got %v", want, got)
	}

	// inferMissingChargingType shares the identical [from, to) boundary (design.md D-B5):
	// a NULL-percentage session exactly at 'from' must be seen; one exactly at 'to' must
	// not.
	sessionAtFromNull := telemetry.SuperchargerSession{ChargeStopDateTime: from, StartBatteryPct: nil, EndBatteryPct: intPtr(30)}
	sessionAtToNull := telemetry.SuperchargerSession{ChargeStopDateTime: to, StartBatteryPct: nil, EndBatteryPct: intPtr(90)}

	gotType := inferMissingChargingType([]telemetry.SuperchargerSession{sessionAtToNull, sessionAtFromNull}, from, to)
	if gotType != telemetry.MissingChargingTypeSupercharger {
		t.Errorf("inferMissingChargingType: want SUPERCHARGER (the in-bound NULL session at 'from' must be seen), got %v", gotType)
	}

	gotTypeOnlyOutOfBound := inferMissingChargingType([]telemetry.SuperchargerSession{sessionAtToNull}, from, to)
	if gotTypeOnlyOutOfBound != telemetry.MissingChargingTypeManual {
		t.Errorf("inferMissingChargingType: want MANUAL (the only NULL session present is at 'to', excluded), got %v", gotTypeOnlyOutOfBound)
	}
}

// TestDeriveVehicleMetrics_Stateless_ResolvesOnRecompute covers design.md Test
// Contract (i) / D2: deriveVehicleMetrics is a pure function of its inputs with no
// memory between calls -- the precondition that makes cmd/poller's D7b
// delete-on-resolve reconciliation work for free (a resolved day simply stops
// appearing flagged on the next recompute).
func TestDeriveVehicleMetrics_Stateless_ResolvesOnRecompute(t *testing.T) {
	d0 := day(2026, 8, 13)
	d1 := day(2026, 8, 14)
	t0 := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 15, 8, 30, 0, 0, time.UTC)

	prev := telemetry.Snapshot{CapturedAt: t0, CapturedDate: d0.AddDate(0, 0, 1)}
	cur := telemetry.Snapshot{
		CapturedAt:         t1,
		CapturedDate:       d1.AddDate(0, 0, 1),
		BatteryUsedPctCalc: intPtr(-10),
		DaysSpannedCalc:    intPtr(1),
	}
	snapshots := []telemetry.Snapshot{prev, cur}

	firstCall := deriveVehicleMetrics(snapshots, nil, nil, d1, d1)
	if len(firstCall) != 1 || !firstCall[0].Flagged {
		t.Fatalf("first call: want one flagged entry, got %+v", firstCall)
	}

	// The user backfills the missing manual record; recomputed from the SAME snapshots
	// with no state carried over from the first call (D2).
	resolvingEntries := []charging.Entry{
		{ChargedOn: d1, StartBatteryPct: intPtr(30), EndBatteryPct: intPtr(40)}, // +10
	}
	secondCall := deriveVehicleMetrics(snapshots, nil, resolvingEntries, d1, d1)
	if len(secondCall) != 1 {
		t.Fatalf("second call: want exactly 1 entry, got %d", len(secondCall))
	}
	if secondCall[0].Flagged {
		t.Errorf("second call: want Flagged=false after backfill, got Flagged=true (ConsumedPct=%v) -- suggests memory between calls", secondCall[0].ConsumedPct)
	}
	wantConsumed := 0.0 // -10 + 10
	if !approxEqual(mustFloat(t, secondCall[0].ConsumedPct), wantConsumed) {
		t.Errorf("second call ConsumedPct: want %v, got %v", wantConsumed, secondCall[0].ConsumedPct)
	}
}

// TestDeriveVehicleMetrics_BucketsInPollerZone_NotEffectiveDate covers design.md Test
// Contract (m) -- the SINGLE BINDING REGRESSION TEST for the owner's roadmap D18
// ruling. A nominal 03:30-Bogota fixture passes under EITHER the zoned rule or the
// overruled UTC rule, so it proves nothing about D18. This fixture is instead the D-B7
// scenario-3 edge: cur was captured in the Bogota EVENING (20:00 Aug 13 = 01:00Z Aug
// 14), where the zoned day and the UTC day disagree by exactly one calendar day.
//
// cur.EffectiveDate is deliberately set to the wrong (UTC, overruled) value -- the ONE
// fixture in this file that populates it -- specifically so a regression to reading it
// is caught by the negative assertion below. Do NOT weaken either assertion (leader
// dispatch instruction, design.md Test Contract (m)).
func TestDeriveVehicleMetrics_BucketsInPollerZone_NotEffectiveDate(t *testing.T) {
	prev := telemetry.Snapshot{
		CapturedAt:   time.Date(2026, 8, 12, 8, 30, 0, 0, time.UTC), // 03:30 Bogota Aug 12
		CapturedDate: day(2026, 8, 12),
	}
	cur := telemetry.Snapshot{
		CapturedAt:   time.Date(2026, 8, 14, 1, 0, 0, 0, time.UTC), // 20:00 Bogota Aug 13
		CapturedDate: day(2026, 8, 13),
		// The value internal/telemetry/mapping.go:119 really computes: CapturedAt - 1 day,
		// in UTC -- i.e. the day the OVERRULED D-B7 would have bucketed this row under.
		EffectiveDate:      time.Date(2026, 8, 13, 1, 0, 0, 0, time.UTC),
		BatteryUsedPctCalc: intPtr(20),
		DaysSpannedCalc:    intPtr(1),
	}

	// Window is exactly cur's zoned effective day (2026-08-12), excluding prev's own
	// effective day (2026-08-11) -- call-shape adjustment only, mirrors the (a)-(e)/(i)
	// pattern above.
	wantZonedDate := day(2026, 8, 12)
	got := deriveVehicleMetrics([]telemetry.Snapshot{prev, cur}, nil, nil, wantZonedDate, wantZonedDate)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(got), got)
	}
	entry := got[0]

	if !approxEqual(mustFloat(t, entry.ConsumedPct), 20) {
		t.Errorf("ConsumedPct: want 20, got %v", entry.ConsumedPct)
	}
	if entry.Flagged {
		t.Error("want Flagged=false")
	}

	wrongUTCDate := day(2026, 8, 13) // EffectiveDate's own day: the overruled UTC answer
	if !entry.MetricDate.Equal(wantZonedDate) {
		t.Errorf("MetricDate: want %v (CapturedDate - 1 day, the zoned answer), got %v", wantZonedDate, entry.MetricDate)
	}
	if entry.MetricDate.Equal(wrongUTCDate) {
		t.Errorf("MetricDate must NOT equal %v -- that is the overruled UTC EffectiveDate answer; bucketing has regressed to reading EffectiveDate", wrongUTCDate)
	}

	// D-B12 identity: effectiveDay(cur) - effectiveDay(prev) == *cur.DaysSpannedCalc, in
	// whole days. This is a SECOND, independent guard on the same regression: under the
	// overruled UTC rule the same fixtures give 2 days != 1.
	gotSpanDays := int(effectiveDay(cur).Sub(effectiveDay(prev)).Hours() / 24)
	if gotSpanDays != *cur.DaysSpannedCalc {
		t.Errorf("effectiveDay(cur)-effectiveDay(prev): want %d day(s) (matching DaysSpannedCalc), got %d", *cur.DaysSpannedCalc, gotSpanDays)
	}
}

// TestDeriveVehicleMetrics_ZoneShiftedRowAtWindowEnd_Emitted covers design.md Test
// Contract (n) / D-B13: a row whose ZONED effective day lands exactly on the window's
// 'end' -- while its UTC EffectiveDate day would fall one day past it -- must still be
// emitted. This is the derivation half of the widened snapshot fetch; T5.4 in
// reader_test.go asserts the fetch itself widens far enough to bring such a row back.
func TestDeriveVehicleMetrics_ZoneShiftedRowAtWindowEnd_Emitted(t *testing.T) {
	prev := telemetry.Snapshot{
		CapturedAt:   time.Date(2026, 8, 20, 8, 30, 0, 0, time.UTC),
		CapturedDate: day(2026, 8, 20), // zoned effective day 2026-08-19
	}
	cur := telemetry.Snapshot{
		// 21:00 Bogota Aug 21 = 02:00Z Aug 22.
		CapturedAt:         time.Date(2026, 8, 22, 2, 0, 0, 0, time.UTC),
		CapturedDate:       day(2026, 8, 21), // zoned effective day 2026-08-20 == end
		BatteryUsedPctCalc: intPtr(5),
		DaysSpannedCalc:    intPtr(1),
	}

	// Window is exactly cur's zoned effective day (2026-08-20), excluding prev's own
	// effective day (2026-08-19) -- call-shape adjustment only, keeping the
	// substantive assertion (end-inclusive emission) unchanged.
	wantDate := day(2026, 8, 20)
	got := deriveVehicleMetrics([]telemetry.Snapshot{prev, cur}, nil, nil, wantDate, wantDate)
	if len(got) != 1 {
		t.Fatalf("want the zone-shifted row to be emitted at the window's upper edge, got %d entries: %+v", len(got), got)
	}
	if !got[0].MetricDate.Equal(wantDate) {
		t.Errorf("MetricDate: want %v (the row's zoned effective day, equal to 'end'), got %v", wantDate, got[0].MetricDate)
	}
}

// --- Fixtures A, B, C (design.md "Test Contract" section, authored BEFORE this
// implementation existed, per ai/go-conventions.md "author expected values up front")
// ---
//
// Every expected value below is copied verbatim from design.md's Test Contract, never
// derived by reading consumed.go. Fixture C is new at the database design gate,
// pinning the dense-table revision's predecessor-less-row representation (D9/D10/D13).

// TestDeriveVehicleMetrics_FixtureA covers design.md's Test Contract Fixture A -- a
// plain day, no charge events.
func TestDeriveVehicleMetrics_FixtureA(t *testing.T) {
	accountID := uuid.New()
	const teslaID = int64(42)

	prevDay := day(2026, 8, 10)
	curDay := day(2026, 8, 11)

	prev := telemetry.Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 10, 3, 30, 0, 0, time.UTC),
		CapturedDate:    prevDay,
		OdometerKm:      1000.0,
		BatteryLevelPct: 80,
		BatteryRangeKm:  300.0,
	}
	cur := telemetry.Snapshot{
		AccountID:              accountID,
		TeslaID:                teslaID,
		CapturedAt:             time.Date(2026, 8, 11, 3, 30, 0, 0, time.UTC),
		CapturedDate:           curDay,
		OdometerKm:             1050.0,
		BatteryLevelPct:        65,
		BatteryRangeKm:         280.0,
		DistanceTraveledKmCalc: fp(50.0),              // 1050.0 - 1000.0
		BatteryUsedPctCalc:     intPtr(15),            // 80 - 65
		KmPerPctCalc:           fp(50.0 / 15.0),       // 3.3333...
		EstimatedRangeKmCalc:   fp(50.0 / 15.0 * 100), // 333.333...
		DaysSpannedCalc:        intPtr(1),
	}

	// Recalculate(A, 42, 2026-08-10, 2026-08-10). effectiveDay = CapturedDate - 1,
	// so prev (CapturedDate 08-10) lands on 08-09, outside this window, and cur
	// (CapturedDate 08-11) lands on 08-10, inside it -- only cur's row is produced.
	start := day(2026, 8, 10)
	end := day(2026, 8, 10)

	got := deriveVehicleMetrics([]telemetry.Snapshot{prev, cur}, nil, nil, start, end)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(got), got)
	}
	entry := got[0]

	if entry.AccountID != accountID {
		t.Errorf("AccountID: want %v, got %v", accountID, entry.AccountID)
	}
	if entry.TeslaID != teslaID {
		t.Errorf("TeslaID: want %d, got %d", teslaID, entry.TeslaID)
	}
	wantDate := day(2026, 8, 10)
	if !entry.MetricDate.Equal(wantDate) {
		t.Errorf("MetricDate: want %v, got %v", wantDate, entry.MetricDate)
	}
	if entry.BatteryLevelPct != 65 {
		t.Errorf("BatteryLevelPct: want 65, got %d", entry.BatteryLevelPct)
	}
	if entry.OdometerKm != 1050.0 {
		t.Errorf("OdometerKm: want 1050.0, got %v", entry.OdometerKm)
	}
	if entry.BatteryRangeKm != 280.0 {
		t.Errorf("BatteryRangeKm: want 280.0, got %v", entry.BatteryRangeKm)
	}
	if !approxEqual(mustFloat(t, entry.DistanceTraveledKmCalc), 50.0) {
		t.Errorf("DistanceTraveledKmCalc: want 50.0, got %v", entry.DistanceTraveledKmCalc)
	}
	if entry.BatteryUsedPctCalc == nil || *entry.BatteryUsedPctCalc != 15 {
		t.Errorf("BatteryUsedPctCalc: want 15, got %v", entry.BatteryUsedPctCalc)
	}
	if !approxEqual(mustFloat(t, entry.KmPerPctCalc), 50.0/15.0) {
		t.Errorf("KmPerPctCalc: want %v, got %v", 50.0/15.0, entry.KmPerPctCalc)
	}
	if !approxEqual(mustFloat(t, entry.EstimatedRangeKmCalc), 50.0/15.0*100) {
		t.Errorf("EstimatedRangeKmCalc: want %v, got %v", 50.0/15.0*100, entry.EstimatedRangeKmCalc)
	}
	if entry.DaysSpannedCalc == nil || *entry.DaysSpannedCalc != 1 {
		t.Errorf("DaysSpannedCalc: want 1, got %v", entry.DaysSpannedCalc)
	}
	if !approxEqual(mustFloat(t, entry.ConsumedPct), 15.0) {
		t.Errorf("ConsumedPct: want 15.0, got %v", entry.ConsumedPct)
	}
	if entry.Flagged {
		t.Error("want Flagged=false")
	}
	if entry.MissingChargingType != "" {
		t.Errorf("MissingChargingType: want \"\" (NULL), got %v", entry.MissingChargingType)
	}
}

// TestDeriveVehicleMetrics_FixtureB covers design.md's Test Contract Fixture B --
// negative odometer clamp + flagged/missing-charge day. The clamp itself
// (OdometerDeltaByDay's math.Max(0, ...)) is a Reader-level concern asserted in
// reader_test.go (task 4.2); this test only asserts the RAW, unclamped stored value
// deriveVehicleMetrics produces.
func TestDeriveVehicleMetrics_FixtureB(t *testing.T) {
	accountID := uuid.New()
	const teslaID = int64(42)

	prevDay := day(2026, 8, 12)
	curDay := day(2026, 8, 13)

	prev := telemetry.Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 12, 3, 30, 0, 0, time.UTC),
		CapturedDate:    prevDay,
		OdometerKm:      2000.0,
		BatteryLevelPct: 40,
	}
	cur := telemetry.Snapshot{
		AccountID:              accountID,
		TeslaID:                teslaID,
		CapturedAt:             time.Date(2026, 8, 13, 3, 30, 0, 0, time.UTC),
		CapturedDate:           curDay,
		OdometerKm:             1998.0,
		BatteryLevelPct:        85,
		DistanceTraveledKmCalc: fp(-2.0), // 1998.0 - 2000.0, stored RAW, unclamped
		BatteryUsedPctCalc:     intPtr(-45),
		DaysSpannedCalc:        intPtr(1),
		// KmPerPctCalc/EstimatedRangeKmCalc left nil -- telemetry's own divisor
		// guard (BatteryUsedPctCalc <= 0) already produced NULL for these before
		// this row was ever fetched; deriveVehicleMetrics only copies them
		// verbatim, never re-derives (design.md D9).
	}

	start := day(2026, 8, 12)
	end := day(2026, 8, 12)

	got := deriveVehicleMetrics([]telemetry.Snapshot{prev, cur}, nil, nil, start, end)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(got), got)
	}
	entry := got[0]

	if entry.AccountID != accountID {
		t.Errorf("AccountID: want %v, got %v", accountID, entry.AccountID)
	}
	if entry.TeslaID != teslaID {
		t.Errorf("TeslaID: want %d, got %d", teslaID, entry.TeslaID)
	}
	wantDate := day(2026, 8, 12)
	if !entry.MetricDate.Equal(wantDate) {
		t.Errorf("MetricDate: want %v, got %v", wantDate, entry.MetricDate)
	}
	if entry.BatteryLevelPct != 85 {
		t.Errorf("BatteryLevelPct: want 85, got %d", entry.BatteryLevelPct)
	}
	if entry.OdometerKm != 1998.0 {
		t.Errorf("OdometerKm: want 1998.0, got %v", entry.OdometerKm)
	}
	if !approxEqual(mustFloat(t, entry.DistanceTraveledKmCalc), -2.0) {
		t.Errorf("DistanceTraveledKmCalc: want -2.0 (raw, unclamped), got %v", entry.DistanceTraveledKmCalc)
	}
	if entry.BatteryUsedPctCalc == nil || *entry.BatteryUsedPctCalc != -45 {
		t.Errorf("BatteryUsedPctCalc: want -45, got %v", entry.BatteryUsedPctCalc)
	}
	if entry.KmPerPctCalc != nil {
		t.Errorf("KmPerPctCalc: want nil (divisor -45 <= 0), got %v", *entry.KmPerPctCalc)
	}
	if entry.EstimatedRangeKmCalc != nil {
		t.Errorf("EstimatedRangeKmCalc: want nil (same guard), got %v", *entry.EstimatedRangeKmCalc)
	}
	if entry.DaysSpannedCalc == nil || *entry.DaysSpannedCalc != 1 {
		t.Errorf("DaysSpannedCalc: want 1, got %v", entry.DaysSpannedCalc)
	}
	if !approxEqual(mustFloat(t, entry.ConsumedPct), -45.0) {
		t.Errorf("ConsumedPct: want -45.0 (no matched charge event), got %v", entry.ConsumedPct)
	}
	if !entry.Flagged {
		t.Error("want Flagged=true (ConsumedPct < 0)")
	}
	if entry.MissingChargingType != telemetry.MissingChargingTypeManual {
		t.Errorf("MissingChargingType: want MANUAL (no Supercharger session matched at all), got %v", entry.MissingChargingType)
	}
}

// TestDeriveVehicleMetrics_FixtureC covers design.md's Test Contract Fixture C -- a
// vehicle's true first-ever snapshot (no predecessor at all), the dense-table
// revision's own scenario (D9/D10). A row IS written -- unlike the pre-revision
// deriveConsumedByDay, which skipped this day entirely -- with every derived/consumed
// field NULL, flagged == false (NOT true, NOT left to a stray zero-value comparison),
// and missing_charging_type == "" (SQL NULL). See
// TestDeriveVehicleMetrics_NilBatteryUsedPctCalc_RawObservationsOnly above for D10's
// sibling trigger of the identical branch.
func TestDeriveVehicleMetrics_FixtureC(t *testing.T) {
	accountID := uuid.New()
	const teslaID = int64(42)

	cur := telemetry.Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 5, 3, 30, 0, 0, time.UTC),
		CapturedDate:    day(2026, 8, 5),
		OdometerKm:      500.0,
		BatteryLevelPct: 90,
		BatteryRangeKm:  320.0,
		// No BatteryUsedPctCalc / DistanceTraveledKmCalc / etc -- telemetry itself
		// recorded no predecessor for this row (it is the vehicle's true
		// first-ever capture).
	}

	// snapshots is the single-element slice telemetry.SnapshotsByVehicleBetween
	// would return for this fetch (design.md: "returns exactly this one row --
	// nothing exists before it") -- cur is index 0, no local prev.
	start := day(2026, 8, 4)
	end := day(2026, 8, 4)

	got := deriveVehicleMetrics([]telemetry.Snapshot{cur}, nil, nil, start, end)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry (dense table -- a row is written even with no predecessor, design.md D9), got %d: %+v", len(got), got)
	}
	entry := got[0]

	if entry.AccountID != accountID {
		t.Errorf("AccountID: want %v, got %v", accountID, entry.AccountID)
	}
	if entry.TeslaID != teslaID {
		t.Errorf("TeslaID: want %d, got %d", teslaID, entry.TeslaID)
	}
	wantDate := day(2026, 8, 4)
	if !entry.MetricDate.Equal(wantDate) {
		t.Errorf("MetricDate: want %v, got %v", wantDate, entry.MetricDate)
	}
	if entry.BatteryLevelPct != 90 {
		t.Errorf("BatteryLevelPct: want 90, got %d", entry.BatteryLevelPct)
	}
	if entry.OdometerKm != 500.0 {
		t.Errorf("OdometerKm: want 500.0, got %v", entry.OdometerKm)
	}
	if entry.BatteryRangeKm != 320.0 {
		t.Errorf("BatteryRangeKm: want 320.0, got %v", entry.BatteryRangeKm)
	}
	if entry.DistanceTraveledKmCalc != nil {
		t.Errorf("DistanceTraveledKmCalc: want nil, got %v", *entry.DistanceTraveledKmCalc)
	}
	if entry.BatteryUsedPctCalc != nil {
		t.Errorf("BatteryUsedPctCalc: want nil, got %v", *entry.BatteryUsedPctCalc)
	}
	if entry.KmPerPctCalc != nil {
		t.Errorf("KmPerPctCalc: want nil, got %v", *entry.KmPerPctCalc)
	}
	if entry.EstimatedRangeKmCalc != nil {
		t.Errorf("EstimatedRangeKmCalc: want nil, got %v", *entry.EstimatedRangeKmCalc)
	}
	if entry.DaysSpannedCalc != nil {
		t.Errorf("DaysSpannedCalc: want nil, got %v", *entry.DaysSpannedCalc)
	}
	if entry.ConsumedPct != nil {
		t.Errorf("ConsumedPct: want nil, got %v", *entry.ConsumedPct)
	}
	if entry.Flagged {
		t.Error("want Flagged=false (NOT NULL -- never true, never left to a stray zero-value comparison, design.md D9)")
	}
	if entry.MissingChargingType != "" {
		t.Errorf("MissingChargingType: want \"\" (maps to SQL NULL), got %v", entry.MissingChargingType)
	}
}
