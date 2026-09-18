package analytics

import "testing"

// These tests exercise deriveMonthlyFigures fully offline, against the
// expected values authored in design.md's Test Contract before this file
// was written -- never derived by reading monthly_figures.go itself. day
// and floatPtr are shared package-level helpers already defined in
// consumed_test.go.
//
// Per the Test-Execution-Policy, this file is written but NOT run by the
// worker; go vet ./... compiles it as a signature-drift signal. The owner
// runs `go test ./internal/analytics/...` and reports the result.

// assertMonthlyFigures compares only the twelve vehicle_metrics-derived
// fields deriveMonthlyFigures actually sets -- every other
// VehicleMonthlyMetrics field stays at its Go zero value in this pure-math
// layer, and is not this function's concern.
func assertMonthlyFigures(t *testing.T, got, want VehicleMonthlyMetrics) {
	t.Helper()

	checks := []struct {
		name      string
		got, want float64
	}{
		{"AllDistanceKm", got.AllDistanceKm, want.AllDistanceKm},
		{"AllConsumedPct", got.AllConsumedPct, want.AllConsumedPct},
		{"AllKmPerPctCalc", got.AllKmPerPctCalc, want.AllKmPerPctCalc},
		{"WeekdayDistanceKm", got.WeekdayDistanceKm, want.WeekdayDistanceKm},
		{"WeekdayConsumedPct", got.WeekdayConsumedPct, want.WeekdayConsumedPct},
		{"WeekdayKmPerPctCalc", got.WeekdayKmPerPctCalc, want.WeekdayKmPerPctCalc},
		{"WeekendDistanceKm", got.WeekendDistanceKm, want.WeekendDistanceKm},
		{"WeekendConsumedPct", got.WeekendConsumedPct, want.WeekendConsumedPct},
		{"WeekendKmPerPctCalc", got.WeekendKmPerPctCalc, want.WeekendKmPerPctCalc},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}

	intChecks := []struct {
		name      string
		got, want int
	}{
		{"AllDayCount", got.AllDayCount, want.AllDayCount},
		{"WeekdayDayCount", got.WeekdayDayCount, want.WeekdayDayCount},
		{"WeekendDayCount", got.WeekendDayCount, want.WeekendDayCount},
	}
	for _, c := range intChecks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// wantTPure1 is the expected result shared by T-PURE-1 and T-PURE-2 -- the
// nil predecessor-less day T-PURE-2 adds must change nothing.
func wantTPure1() VehicleMonthlyMetrics {
	return VehicleMonthlyMetrics{
		AllDistanceKm: 285, AllConsumedPct: 54, AllDayCount: 5, AllKmPerPctCalc: 5.0,
		WeekdayDistanceKm: 165, WeekdayConsumedPct: 30, WeekdayDayCount: 3, WeekdayKmPerPctCalc: 5.0,
		WeekendDistanceKm: 120, WeekendConsumedPct: 24, WeekendDayCount: 2, WeekendKmPerPctCalc: 5.0,
	}
}

// TestDeriveMonthlyFigures_MixedWeekdayWeekend is T-PURE-1: a mixed
// weekday/weekend month where one weekday has zero consumption, proving
// both the weekday/weekend split and that a zero-consumption day is
// excluded from the efficiency ratio's sum but still counted in DayCount.
func TestDeriveMonthlyFigures_MixedWeekdayWeekend(t *testing.T) {
	days := []monthDay{
		{Date: day(2026, 3, 2), DistanceKm: floatPtr(100), ConsumedPct: floatPtr(20)}, // Mon
		{Date: day(2026, 3, 3), DistanceKm: floatPtr(50), ConsumedPct: floatPtr(10)},  // Tue
		{Date: day(2026, 3, 7), DistanceKm: floatPtr(80), ConsumedPct: floatPtr(16)},  // Sat
		{Date: day(2026, 3, 8), DistanceKm: floatPtr(40), ConsumedPct: floatPtr(8)},   // Sun
		{Date: day(2026, 3, 9), DistanceKm: floatPtr(15), ConsumedPct: floatPtr(0)},   // Mon, zero consumption
	}

	got := deriveMonthlyFigures(days)

	assertMonthlyFigures(t, got, wantTPure1())
}

// TestDeriveMonthlyFigures_SkipsPredecessorlessDay is T-PURE-2: a
// predecessor-less day (both fields nil) added to T-PURE-1's fixture must
// contribute to no sum and no count -- the result is identical to T-PURE-1.
func TestDeriveMonthlyFigures_SkipsPredecessorlessDay(t *testing.T) {
	days := []monthDay{
		{Date: day(2026, 3, 2), DistanceKm: floatPtr(100), ConsumedPct: floatPtr(20)},
		{Date: day(2026, 3, 3), DistanceKm: floatPtr(50), ConsumedPct: floatPtr(10)},
		{Date: day(2026, 3, 7), DistanceKm: floatPtr(80), ConsumedPct: floatPtr(16)},
		{Date: day(2026, 3, 8), DistanceKm: floatPtr(40), ConsumedPct: floatPtr(8)},
		{Date: day(2026, 3, 9), DistanceKm: floatPtr(15), ConsumedPct: floatPtr(0)},
		{Date: day(2026, 3, 10), DistanceKm: nil, ConsumedPct: nil}, // Tue, no predecessor
	}

	got := deriveMonthlyFigures(days)

	assertMonthlyFigures(t, got, wantTPure1())
}

// TestDeriveMonthlyFigures_EmptyMonth is T-PURE-3: an empty slice must
// produce every field at 0, including every *DayCount -- the case the
// DB-backed integration test wraps with a real SyncMonth call.
func TestDeriveMonthlyFigures_EmptyMonth(t *testing.T) {
	got := deriveMonthlyFigures(nil)

	assertMonthlyFigures(t, got, VehicleMonthlyMetrics{})
}
