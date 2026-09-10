// entry_status_test.go — offline unit tests (no DB) for the domain surface added
// by MAG-18/RM33 tier 1: RequiredFieldsFor's two required-field sets (design.md
// Test Contract A1-A5), the pure derivedEnergyKWh derivation helper (A7-A8), and
// the packCapacityKWh seam (A9).
//
// Package charging (NOT charging_test): A7 and A9 exercise derivedEnergyKWh and
// packCapacityKWh, which are deliberately unexported — implementation details of
// the write-path derivation seam, not part of this module's public surface
// (design.md D3/D7). A1-A5 exercise the exported RequiredFieldsFor and could live
// in charging_test, but are kept in this file so every Group A case lives
// together, per tasks.md 3.1's single-file scope.
//
// Every expected value below is copied VERBATIM from design.md Section "Test
// Contract", authored before this file existed (ai/go-conventions.md §Testing
// authoring order) — this file asserts that contract, not whatever the
// implementation happens to produce.
package charging

import (
	"math"
	"reflect"
	"testing"
	"time"
)

// --- A1/A2: RequiredFieldsFor's two sets, exact and in order ---

// TestRequiredFieldsFor_InProgress implements design.md A1: the IN_PROGRESS
// required set is exactly {charged_on, location_kind}, in that order.
func TestRequiredFieldsFor_InProgress(t *testing.T) {
	got := RequiredFieldsFor(StatusInProgress)
	want := []Field{FieldChargedOn, FieldLocationKind}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RequiredFieldsFor(StatusInProgress) = %v, want %v", got, want)
	}
}

// TestRequiredFieldsFor_Done implements design.md A2: the DONE required set is
// exactly {charged_on, location_kind, ended_at, end_battery_pct}, in that order.
func TestRequiredFieldsFor_Done(t *testing.T) {
	got := RequiredFieldsFor(StatusDone)
	want := []Field{FieldChargedOn, FieldLocationKind, FieldEndedAt, FieldEndBatteryPct}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RequiredFieldsFor(StatusDone) = %v, want %v", got, want)
	}
}

// --- A3: the DONE/IN_PROGRESS skip set, asserted as a SET DIFFERENCE ---

// TestRequiredFieldsFor_DoneSkipSetIsExactlyEndedAtAndEndBatteryPct implements
// design.md A3. It computes set(DONE) - set(IN_PROGRESS) from the two LIVE
// RequiredFieldsFor results, rather than restating two literal slices that
// could silently drift apart from the function they exist to check — this is
// what makes the test state roadmap D5's "skip set" rule itself, not merely two
// hardcoded slices that happen to agree with it today.
func TestRequiredFieldsFor_DoneSkipSetIsExactlyEndedAtAndEndBatteryPct(t *testing.T) {
	inProgress := RequiredFieldsFor(StatusInProgress)
	done := RequiredFieldsFor(StatusDone)

	inProgressSet := make(map[Field]bool, len(inProgress))
	for _, f := range inProgress {
		inProgressSet[f] = true
	}

	var diff []Field
	for _, f := range done {
		if !inProgressSet[f] {
			diff = append(diff, f)
		}
	}

	want := []Field{FieldEndedAt, FieldEndBatteryPct}
	if !reflect.DeepEqual(diff, want) {
		t.Errorf("set(DONE) - set(IN_PROGRESS) = %v, want %v", diff, want)
	}
}

// --- A4: the lookup returns a defensive copy ---

// TestRequiredFieldsFor_ReturnsDefensiveCopy implements design.md A4: mutating
// the slice returned by one call must not affect the next call's result.
func TestRequiredFieldsFor_ReturnsDefensiveCopy(t *testing.T) {
	first := RequiredFieldsFor(StatusInProgress)
	if len(first) == 0 {
		t.Fatal("RequiredFieldsFor(StatusInProgress): expected a non-empty slice")
	}
	first[0] = Field("TAMPERED")

	second := RequiredFieldsFor(StatusInProgress)
	if second[0] != FieldChargedOn {
		t.Errorf("second call was corrupted by mutating the first call's slice: got %v, want %v", second[0], FieldChargedOn)
	}
}

// --- A5: fail-closed on an unrecognized status ---

// TestRequiredFieldsFor_UnknownStatusFailsClosed implements design.md A5: an
// unrecognized Status value returns the DONE (strictest) set.
func TestRequiredFieldsFor_UnknownStatusFailsClosed(t *testing.T) {
	got := RequiredFieldsFor(Status("PAUSED"))
	want := RequiredFieldsFor(StatusDone)
	if !reflect.DeepEqual(got, want) {
		t.Errorf(`RequiredFieldsFor("PAUSED") = %v, want the DONE set %v`, got, want)
	}
}

// --- shared float helpers for this file ---

// almostEqual matches this module's Test Contract convention
// (math.Abs(got-want) < 1e-9), applied to plain float64 values.
func almostEqual(got, want float64) bool {
	return math.Abs(got-want) < 1e-9
}

// assertDerivedEnergy fails the test unless got and want are both nil, or both
// non-nil and within 1e-9 of each other.
func assertDerivedEnergy(t *testing.T, got, want *float64) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Errorf("got %v, want nil", *got)
		}
		return
	}
	if got == nil {
		t.Fatalf("got nil, want %v", *want)
	}
	if !almostEqual(*got, *want) {
		t.Errorf("got %v, want %v", *got, *want)
	}
}

func intPtr(v int) *int { return &v }

// --- A7: the derivation table ---

// TestDerivedEnergyKWh_Table implements design.md A7's eight-row derivation
// table verbatim, at the constant capacity 62.0.
func TestDerivedEnergyKWh_Table(t *testing.T) {
	cases := []struct {
		name  string
		start *int
		end   *int
		want  *float64
	}{
		{"headline: 50->100 => 31.00", intPtr(50), intPtr(100), ptrF64(31.00)},
		{"small delta: 64->74 => 6.20", intPtr(64), intPtr(74), ptrF64(6.20)},
		{"minimum positive delta: 0->1 => 0.62", intPtr(0), intPtr(1), ptrF64(0.62)},
		{"maximum delta: 0->100 => 62.00", intPtr(0), intPtr(100), ptrF64(62.00)},
		{"zero delta: 74->74 => nil", intPtr(74), intPtr(74), nil},
		{"negative delta: 74->64 => nil", intPtr(74), intPtr(64), nil},
		{"missing start => nil", nil, intPtr(80), nil},
		{"missing end => nil", intPtr(50), nil, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := derivedEnergyKWh(62.0, tc.start, tc.end)
			assertDerivedEnergy(t, got, tc.want)
		})
	}
}

// --- A8: rounding is half-away-from-zero at scale 2 ---

// TestDerivedEnergyKWh_RoundingHalfAwayFromZeroAtScale2 implements design.md A8.
// Deliberately uses capacity 62.35, NOT 62.0: with 62.0 the rounding is a no-op
// in exact arithmetic, so a test using it would pass even if the rounding step
// were deleted. This pins the rule before backlog #18 changes the constant.
func TestDerivedEnergyKWh_RoundingHalfAwayFromZeroAtScale2(t *testing.T) {
	cases := []struct {
		name  string
		start int
		end   int
		want  float64
	}{
		{"0->37: 23.0695 rounds to 23.07", 0, 37, 23.07},
		{"0->25: 15.5875 rounds to 15.59", 0, 25, 15.59},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end := tc.start, tc.end
			got := derivedEnergyKWh(62.35, &start, &end)
			assertDerivedEnergy(t, got, &tc.want)
		})
	}
}

func ptrF64(v float64) *float64 { return &v }

// --- RM51-charging-derive-status-and-price-source (MAG-58): promoteIfComplete ---
//
// The A1-A6 labels below refer to RM51's OWN design.md Test Contract, a
// different document from the RM33 design.md the A1-A9 labels above refer
// to. Both changes happened to number their first unit-test group "A" —
// the number after "A" is only unique within one change's own design.md.

// promoteTestEntry returns a base Entry with Status IN_PROGRESS and all four
// RequiredFieldsFor(StatusDone) fields present (ChargedOn, LocationKind,
// EndedAt, EndBatteryPct). Each RM51 A2-A5 case below clears exactly one
// field to prove that field alone blocks promotion.
func promoteTestEntry() Entry {
	lk := "HOME"
	end := time.Date(2026, 9, 9, 18, 0, 0, 0, time.UTC)
	return Entry{
		Status:        StatusInProgress,
		ChargedOn:     time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC),
		LocationKind:  &lk,
		EndedAt:       &end,
		EndBatteryPct: intPtr(90),
	}
}

// TestPromoteIfComplete_A1_AllFourSetPromotesToDone implements RM51 design.md
// Test Contract A1: the headline rule -- a complete IN_PROGRESS entry is
// promoted to DONE.
func TestPromoteIfComplete_A1_AllFourSetPromotesToDone(t *testing.T) {
	got := promoteIfComplete(promoteTestEntry())
	if got.Status != StatusDone {
		t.Errorf("A1: Status = %v, want DONE", got.Status)
	}
}

// TestPromoteIfComplete_A2_MissingEndedAtBlocksPromotion implements RM51
// design.md Test Contract A2: a missing ended_at blocks promotion.
func TestPromoteIfComplete_A2_MissingEndedAtBlocksPromotion(t *testing.T) {
	e := promoteTestEntry()
	e.EndedAt = nil
	got := promoteIfComplete(e)
	if got.Status != StatusInProgress {
		t.Errorf("A2: Status = %v, want IN_PROGRESS (unchanged)", got.Status)
	}
}

// TestPromoteIfComplete_A3_MissingEndBatteryPctBlocksPromotion implements
// RM51 design.md Test Contract A3: a missing end_battery_pct blocks
// promotion -- the symmetric case to A2.
func TestPromoteIfComplete_A3_MissingEndBatteryPctBlocksPromotion(t *testing.T) {
	e := promoteTestEntry()
	e.EndBatteryPct = nil
	got := promoteIfComplete(e)
	if got.Status != StatusInProgress {
		t.Errorf("A3: Status = %v, want IN_PROGRESS (unchanged)", got.Status)
	}
}

// TestPromoteIfComplete_A4_MissingChargedOnBlocksPromotion implements RM51
// design.md Test Contract A4: proves the helper reuses the FULL
// RequiredFieldsFor(StatusDone) set, not a hardcoded {ended_at,
// end_battery_pct} list -- a field required at every status still gates
// promotion.
func TestPromoteIfComplete_A4_MissingChargedOnBlocksPromotion(t *testing.T) {
	e := promoteTestEntry()
	e.ChargedOn = time.Time{}
	got := promoteIfComplete(e)
	if got.Status != StatusInProgress {
		t.Errorf("A4: Status = %v, want IN_PROGRESS (unchanged)", got.Status)
	}
}

// TestPromoteIfComplete_A5_MissingLocationKindBlocksPromotion implements
// RM51 design.md Test Contract A5: symmetric to A4, for location_kind.
func TestPromoteIfComplete_A5_MissingLocationKindBlocksPromotion(t *testing.T) {
	e := promoteTestEntry()
	e.LocationKind = nil
	got := promoteIfComplete(e)
	if got.Status != StatusInProgress {
		t.Errorf("A5: Status = %v, want IN_PROGRESS (unchanged)", got.Status)
	}
}

// TestPromoteIfComplete_A6_DoneStatusNeverDemoted implements RM51 design.md
// Test Contract A6: promoteIfComplete never demotes. Its first line returns
// immediately for any status other than IN_PROGRESS, so it cannot be talked
// into assigning IN_PROGRESS under any input -- proven here with a DONE
// entry missing both DONE-only fields, an inconsistent state constructed
// only to probe the guard.
func TestPromoteIfComplete_A6_DoneStatusNeverDemoted(t *testing.T) {
	e := promoteTestEntry()
	e.Status = StatusDone
	e.EndedAt = nil
	e.EndBatteryPct = nil
	got := promoteIfComplete(e)
	if got.Status != StatusDone {
		t.Errorf("A6: Status = %v, want DONE (unchanged)", got.Status)
	}
}
