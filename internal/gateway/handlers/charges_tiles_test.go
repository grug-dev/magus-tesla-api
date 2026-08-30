package handlers

import (
	"testing"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// ptrTime is a small pointer helper for building a nullable *time.Time field
// on a charging.Entry fixture — mirrors ptrF64/ptrInt (supercharger_test.go).
func ptrTime(t time.Time) *time.Time { return &t }

// --- entryComplete / buildChargeTiles Test Contract Group B
// (design.md §Test Contract Group B, RM33-gateway-add-entries-dashboard) ---

// completeEntry returns a charging.Entry satisfying entryComplete's bar:
// EndedAt, EndBatteryPct, EnergyAddedKWh all non-nil, Price > 0. Callers of
// B2 null out exactly one field at a time.
func completeEntry() charging.Entry {
	return charging.Entry{
		Status:         charging.StatusDone,
		EndedAt:        ptrTime(time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)),
		EndBatteryPct:  ptrInt(70),
		EnergyAddedKWh: ptrF64(10.0),
		Price:          1000,
	}
}

// TestEntryComplete_B1_AllFourPresent is Test Contract B1: all four
// completeness inputs present, Price > 0 -> entryComplete == true.
func TestEntryComplete_B1_AllFourPresent(t *testing.T) {
	if !entryComplete(completeEntry()) {
		t.Error("want entryComplete=true when EndedAt, EndBatteryPct, EnergyAddedKWh are non-nil and Price > 0")
	}
}

// TestEntryComplete_B2_EachMissingOneInput is Test Contract B2: four
// variants, each with exactly ONE of the four inputs missing/zero ->
// entryComplete == false in all four cases, proving the predicate is a
// strict AND, not "most fields present."
func TestEntryComplete_B2_EachMissingOneInput(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(e *charging.Entry)
	}{
		{"EndedAt nil", func(e *charging.Entry) { e.EndedAt = nil }},
		{"EndBatteryPct nil", func(e *charging.Entry) { e.EndBatteryPct = nil }},
		{"EnergyAddedKWh nil", func(e *charging.Entry) { e.EnergyAddedKWh = nil }},
		{"Price zero", func(e *charging.Entry) { e.Price = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := completeEntry()
			tc.mutate(&e)
			if entryComplete(e) {
				t.Errorf("want entryComplete=false when %s", tc.name)
			}
		})
	}
}

// TestEntryComplete_B3_InProgressNotSpecialCased is Test Contract B3: a
// normally-shaped IN_PROGRESS entry (EndedAt == nil) -> entryComplete ==
// false. Confirms the predicate does NOT special-case IN_PROGRESS -- it is
// evaluated identically regardless of e.Status (design.md §D-Dot).
func TestEntryComplete_B3_InProgressNotSpecialCased(t *testing.T) {
	e := charging.Entry{
		Status:          charging.StatusInProgress,
		ChargedOn:       time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC),
		StartBatteryPct: ptrInt(40),
		EndedAt:         nil,
		EndBatteryPct:   nil,
		EnergyAddedKWh:  nil,
		Price:           0,
	}
	if entryComplete(e) {
		t.Error("want entryComplete=false for a normally-shaped IN_PROGRESS entry")
	}
}

// TestBuildChargeTiles_B4_EmptySlice is Test Contract B4: buildChargeTiles
// over an empty []charging.Entry{} -> Sessions=="0", Energy=="0.0 kWh",
// Cost=="0.00 COP", AvgKWh=="—" (D13/D14's "0 and —" split, asserted
// per-field).
func TestBuildChargeTiles_B4_EmptySlice(t *testing.T) {
	tiles := buildChargeTiles([]charging.Entry{})
	if tiles.Sessions != "0" {
		t.Errorf("want Sessions=0, got %s", tiles.Sessions)
	}
	if tiles.Energy != "0.0 kWh" {
		t.Errorf("want Energy=0.0 kWh, got %s", tiles.Energy)
	}
	if tiles.Cost != "0.00 COP" {
		t.Errorf("want Cost=0.00 COP, got %s", tiles.Cost)
	}
	if tiles.AvgKWh != "—" {
		t.Errorf("want AvgKWh=—, got %s", tiles.AvgKWh)
	}
}

// TestBuildChargeTiles_B5_ThreeEntriesOneNilEnergy is Test Contract B5:
// three entries (one with nil EnergyAddedKWh) -> Sessions=="3",
// Energy=="15.0 kWh" (nil-skip), Cost=="1,500.00 COP" (summed regardless of
// nil energy), AvgKWh=="7.5 kWh" (divided by the non-nil-energy COUNT (2),
// not the entry count (3)).
func TestBuildChargeTiles_B5_ThreeEntriesOneNilEnergy(t *testing.T) {
	entries := []charging.Entry{
		{EnergyAddedKWh: ptrF64(10.0), Price: 1000},
		{EnergyAddedKWh: nil, Price: 500},
		{EnergyAddedKWh: ptrF64(5.0), Price: 0},
	}
	tiles := buildChargeTiles(entries)
	if tiles.Sessions != "3" {
		t.Errorf("want Sessions=3, got %s", tiles.Sessions)
	}
	if tiles.Energy != "15.0 kWh" {
		t.Errorf("want Energy=15.0 kWh, got %s", tiles.Energy)
	}
	// formatMoney applies commaGroup thousands separation (format.go) — the
	// contract originally authored this un-grouped, corrected after the owner's
	// first suite run. The summed VALUE (1000+500+0) is what B5 pins; only its
	// rendering was mis-authored.
	if tiles.Cost != "1,500.00 COP" {
		t.Errorf("want Cost=1,500.00 COP, got %s", tiles.Cost)
	}
	if tiles.AvgKWh != "7.5 kWh" {
		t.Errorf("want AvgKWh=7.5 kWh, got %s", tiles.AvgKWh)
	}
}
