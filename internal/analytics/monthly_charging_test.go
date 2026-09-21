package analytics

import (
	"testing"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// These tests exercise aggregateChargingMonth fully offline. Every expected value
// was worked out by hand before the code was written, never read back out of
// monthly_charging.go -- a test that copies the implementation proves nothing.
// day, floatPtr, and intPtr are shared package-level helpers already defined in
// consumed_test.go and reader_test.go.
//
// Per the Test-Execution-Policy, this file is written but NOT run by the worker; go
// vet ./... compiles it as a signature-drift signal. The owner runs
// `go test ./internal/analytics/...` and reports the result.

// stringPtr returns a pointer to v -- this file's *string fixture builder for
// charging.Entry.ChargingType, mirroring boolPtr/floatPtr/intPtr in the sibling
// _test.go files.
func stringPtr(v string) *string { return &v }

// assertTally compares one chargeTally's four fields against the hand-computed
// expected values -- energy, cost, count, and every bucket of dist.
func assertTally(t *testing.T, name string, got, want chargeTally) {
	t.Helper()

	if got.energyKWh != want.energyKWh {
		t.Errorf("%s.energyKWh = %v, want %v", name, got.energyKWh, want.energyKWh)
	}
	if got.cost != want.cost {
		t.Errorf("%s.cost = %v, want %v", name, got.cost, want.cost)
	}
	if got.count != want.count {
		t.Errorf("%s.count = %v, want %v", name, got.count, want.count)
	}

	buckets := []struct {
		bucket    string
		got, want int
	}{
		{"0-20", got.dist.Bucket0To20, want.dist.Bucket0To20},
		{"20-40", got.dist.Bucket20To40, want.dist.Bucket20To40},
		{"40-60", got.dist.Bucket40To60, want.dist.Bucket40To60},
		{"60-80", got.dist.Bucket60To80, want.dist.Bucket60To80},
		{"80-100", got.dist.Bucket80To100, want.dist.Bucket80To100},
	}
	for _, b := range buckets {
		if b.got != b.want {
			t.Errorf("%s.dist[%s] = %v, want %v", name, b.bucket, b.got, b.want)
		}
	}
}

// TestAggregateChargingMonth_ACDCSplitNilTypeSkipped checks the two skip rules.
// An entry with no ChargingType is dropped whole: it is neither AC nor DC, and
// there is no third column for it. An entry with no EnergyAddedKWh or no
// EndBatteryPct still counts, but adds zero to the sum and enters no bucket.
func TestAggregateChargingMonth_ACDCSplitNilTypeSkipped(t *testing.T) {
	entries := []charging.Entry{
		{ChargingType: stringPtr("AC"), EnergyAddedKWh: floatPtr(10.0), Price: 15000, EndBatteryPct: intPtr(45)},
		{ChargingType: stringPtr("AC"), EnergyAddedKWh: nil, Price: 5000, EndBatteryPct: nil},
		{ChargingType: stringPtr("DC"), EnergyAddedKWh: floatPtr(30.0), Price: 60000, EndBatteryPct: intPtr(85)},
		{ChargingType: nil, EnergyAddedKWh: floatPtr(100.0), Price: 99999, EndBatteryPct: intPtr(50)},
		{ChargingType: stringPtr("AC"), EnergyAddedKWh: floatPtr(5.0), Price: 8000, EndBatteryPct: intPtr(15)},
	}

	ac, dc, sc := aggregateChargingMonth(entries, nil, day(2026, 3, 1), day(2026, 3, 31))

	assertTally(t, "ac", ac, chargeTally{
		energyKWh: 15.0, cost: 28000, count: 3,
		dist: EndingBatteryDist{Bucket0To20: 1, Bucket40To60: 1},
	})
	assertTally(t, "dc", dc, chargeTally{
		energyKWh: 30.0, cost: 60000, count: 1,
		dist: EndingBatteryDist{Bucket80To100: 1},
	})
	assertTally(t, "sc", sc, chargeTally{})
}

// TestAggregateChargingMonth_BucketEdgesHalfOpen checks the bucket edges. They are
// half-open except the last, which is closed -- 80 and 100 both land in the final
// bucket. Closed edges everywhere would count 20, 40, 60 and 80 twice.
func TestAggregateChargingMonth_BucketEdgesHalfOpen(t *testing.T) {
	entries := []charging.Entry{
		{ChargingType: stringPtr("AC"), EndBatteryPct: intPtr(0)},
		{ChargingType: stringPtr("AC"), EndBatteryPct: intPtr(20)},
		{ChargingType: stringPtr("AC"), EndBatteryPct: intPtr(40)},
		{ChargingType: stringPtr("AC"), EndBatteryPct: intPtr(60)},
		{ChargingType: stringPtr("AC"), EndBatteryPct: intPtr(80)},
		{ChargingType: stringPtr("AC"), EndBatteryPct: intPtr(100)},
	}

	ac, _, _ := aggregateChargingMonth(entries, nil, day(2026, 3, 1), day(2026, 3, 31))

	assertTally(t, "ac", ac, chargeTally{
		count: 6,
		dist: EndingBatteryDist{
			Bucket0To20: 1, Bucket20To40: 1, Bucket40To60: 1, Bucket60To80: 1, Bucket80To100: 2,
		},
	})
}

// TestAggregateChargingMonth_SuperchargerPlatformZoneMembership checks which month
// a session belongs to. The platform-zone day of ChargeStopDateTime decides, not
// the UTC day. Bogota is UTC-5, so an evening session falls on the next UTC day,
// which is why the caller widens the fetch and this code drops the strays.
func TestAggregateChargingMonth_SuperchargerPlatformZoneMembership(t *testing.T) {
	monthStart, monthEnd := day(2026, 3, 1), day(2026, 3, 31)

	sessions := []charging.Session{
		// A: UTC March 1, Bogota Feb 28 -- excluded.
		{ChargeStopDateTime: time.Date(2026, 3, 1, 2, 0, 0, 0, time.UTC), EnergyKWh: floatPtr(50), TotalCost: floatPtr(90000), EndBatteryPct: intPtr(90)},
		// B: UTC April 1, Bogota March 31 -- included (proves the widened fetch).
		{ChargeStopDateTime: time.Date(2026, 4, 1, 2, 0, 0, 0, time.UTC), EnergyKWh: floatPtr(40), TotalCost: floatPtr(70000), EndBatteryPct: intPtr(95)},
		// C: UTC and Bogota both March 15 -- included.
		{ChargeStopDateTime: time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC), EnergyKWh: floatPtr(20), TotalCost: floatPtr(30000), EndBatteryPct: intPtr(55)},
		// D: UTC and Bogota both March 10, no source fields -- included, no bucket.
		{ChargeStopDateTime: time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC), EnergyKWh: nil, TotalCost: nil, EndBatteryPct: nil},
	}

	_, _, sc := aggregateChargingMonth(nil, sessions, monthStart, monthEnd)

	assertTally(t, "sc", sc, chargeTally{
		energyKWh: 60.0, cost: 100000, count: 3,
		dist: EndingBatteryDist{Bucket40To60: 1, Bucket80To100: 1},
	})
}
