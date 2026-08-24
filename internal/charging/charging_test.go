// Package charging_test contains offline unit tests for the charging domain
// type's derived value-receiver methods. No database, no Tesla API call — tests
// construct Entry literals directly and verify computed results.
package charging_test

import (
	"testing"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// --- CostPerKWh ---

// TestCostPerKWh_Normal verifies that CostPerKWh returns the correct ratio when
// both Price and EnergyAddedKWh are set to non-zero values.
// 8000 COP / 15.5 kWh ≈ 516.129... COP/kWh (within float64 tolerance).
func TestCostPerKWh_Normal(t *testing.T) {
	e := charging.Entry{
		Price:          8000,
		EnergyAddedKWh: 15.5,
	}
	got := e.CostPerKWh()
	if got == nil {
		t.Fatal("CostPerKWh: expected non-nil result, got nil")
	}
	want := 8000.0 / 15.5 // ≈ 516.129...
	const tolerance = 1e-9
	if diff := *got - want; diff > tolerance || diff < -tolerance {
		t.Errorf("CostPerKWh: got %v, want %v (diff %v)", *got, want, diff)
	}
}

// TestCostPerKWh_ZeroEnergy verifies that CostPerKWh returns nil when
// EnergyAddedKWh is zero — defensive nil-guard (the DB CHECK prevents zero, but
// the method must not panic on zero input). Design D2j / tasks.md T5.1(b).
func TestCostPerKWh_ZeroEnergy(t *testing.T) {
	e := charging.Entry{
		Price:          8000,
		EnergyAddedKWh: 0,
	}
	got := e.CostPerKWh()
	if got != nil {
		t.Errorf("CostPerKWh: expected nil when EnergyAddedKWh=0, got %v", *got)
	}
}

// --- BatteryDelta ---

// TestBatteryDelta_Normal verifies that BatteryDelta returns EndBatteryPct -
// StartBatteryPct when both are non-nil. Tasks.md T5.2(a): start=20, end=80 → 60.
func TestBatteryDelta_Normal(t *testing.T) {
	start := 20
	end := 80
	e := charging.Entry{
		StartBatteryPct: &start,
		EndBatteryPct:   &end,
	}
	got := e.BatteryDelta()
	if got == nil {
		t.Fatal("BatteryDelta: expected non-nil result, got nil")
	}
	if *got != 60 {
		t.Errorf("BatteryDelta: got %d, want 60", *got)
	}
}

// TestBatteryDelta_NilStart verifies that BatteryDelta returns nil when
// StartBatteryPct is nil. Tasks.md T5.2(b).
func TestBatteryDelta_NilStart(t *testing.T) {
	end := 80
	e := charging.Entry{
		StartBatteryPct: nil,
		EndBatteryPct:   &end,
	}
	got := e.BatteryDelta()
	if got != nil {
		t.Errorf("BatteryDelta: expected nil when StartBatteryPct is nil, got %d", *got)
	}
}

// TestBatteryDelta_NilEnd verifies that BatteryDelta returns nil when
// EndBatteryPct is nil. Tasks.md T5.2(c).
func TestBatteryDelta_NilEnd(t *testing.T) {
	start := 20
	e := charging.Entry{
		StartBatteryPct: &start,
		EndBatteryPct:   nil,
	}
	got := e.BatteryDelta()
	if got != nil {
		t.Errorf("BatteryDelta: expected nil when EndBatteryPct is nil, got %d", *got)
	}
}

// --- SessionDuration ---

// TestSessionDuration_Normal verifies that SessionDuration returns the correct
// elapsed time when both StartedAt and EndedAt are set. Tasks.md T5.3(a).
func TestSessionDuration_Normal(t *testing.T) {
	start := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 18, 11, 30, 0, 0, time.UTC)
	e := charging.Entry{
		StartedAt: &start,
		EndedAt:   &end,
	}
	got := e.SessionDuration()
	if got == nil {
		t.Fatal("SessionDuration: expected non-nil result, got nil")
	}
	want := 90 * time.Minute
	if *got != want {
		t.Errorf("SessionDuration: got %v, want %v", *got, want)
	}
}

// TestSessionDuration_NilStart verifies that SessionDuration returns nil when
// StartedAt is nil. Tasks.md T5.3(b).
func TestSessionDuration_NilStart(t *testing.T) {
	end := time.Date(2026, 7, 18, 11, 30, 0, 0, time.UTC)
	e := charging.Entry{
		StartedAt: nil,
		EndedAt:   &end,
	}
	got := e.SessionDuration()
	if got != nil {
		t.Errorf("SessionDuration: expected nil when StartedAt is nil, got %v", *got)
	}
}

// TestSessionDuration_NilEnd verifies that SessionDuration returns nil when
// EndedAt is nil. Tasks.md T5.3(c).
func TestSessionDuration_NilEnd(t *testing.T) {
	start := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	e := charging.Entry{
		StartedAt: &start,
		EndedAt:   nil,
	}
	got := e.SessionDuration()
	if got != nil {
		t.Errorf("SessionDuration: expected nil when EndedAt is nil, got %v", *got)
	}
}
