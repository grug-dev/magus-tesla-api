package telemetry

import (
	"math"
	"testing"
)

// TestTpmsPSI_NilIn_NilOut verifies that each PSI companion method returns nil
// when the corresponding bar field is nil (not reported / pre-migration row).
// No network or DATABASE_URL required — pure logic tests.
func TestTpmsPSI_NilIn_NilOut(t *testing.T) {
	s := Snapshot{
		TpmsPressureFL: nil,
		TpmsPressureFR: nil,
		TpmsPressureRL: nil,
		TpmsPressureRR: nil,
	}

	if got := s.TpmsPressureFLPSI(); got != nil {
		t.Errorf("TpmsPressureFLPSI: want nil for nil field, got *%v", *got)
	}
	if got := s.TpmsPressureFRPSI(); got != nil {
		t.Errorf("TpmsPressureFRPSI: want nil for nil field, got *%v", *got)
	}
	if got := s.TpmsPressureRLPSI(); got != nil {
		t.Errorf("TpmsPressureRLPSI: want nil for nil field, got *%v", *got)
	}
	if got := s.TpmsPressureRRPSI(); got != nil {
		t.Errorf("TpmsPressureRRPSI: want nil for nil field, got *%v", *got)
	}
}

// TestTpmsPSI_KnownBarValue_CorrectPSI verifies that a known non-nil bar value
// is correctly converted to PSI via barToPSI. Uses 2.5 bar as the reference:
// 2.5 * 14.503773773 ≈ 36.25943... PSI.
func TestTpmsPSI_KnownBarValue_CorrectPSI(t *testing.T) {
	const inputBar = 2.5
	const expectedPSI = inputBar * barToPSI
	const tol = 1e-6

	fl := inputBar
	fr := inputBar
	rl := inputBar
	rr := inputBar
	s := Snapshot{
		TpmsPressureFL: &fl,
		TpmsPressureFR: &fr,
		TpmsPressureRL: &rl,
		TpmsPressureRR: &rr,
	}

	tests := []struct {
		name string
		got  *float64
	}{
		{"TpmsPressureFLPSI", s.TpmsPressureFLPSI()},
		{"TpmsPressureFRPSI", s.TpmsPressureFRPSI()},
		{"TpmsPressureRLPSI", s.TpmsPressureRLPSI()},
		{"TpmsPressureRRPSI", s.TpmsPressureRRPSI()},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got == nil {
				t.Fatalf("%s: want non-nil *float64, got nil", tc.name)
			}
			if math.Abs(*tc.got-expectedPSI) > tol {
				t.Errorf("%s: want %v PSI (within %v), got %v", tc.name, expectedPSI, tol, *tc.got)
			}
		})
	}
}

// TestTpmsPSI_ZeroBar_NonNilResult verifies the D12/DSA3 invariant: a truthfully
// reported 0.0 bar is stored non-NULL (pointer-wrapped via ptr()), and the PSI
// companion of a *0.0 bar field returns a non-nil *0.0 PSI — never nil. A nil
// would falsely mean "not reported", which 0.0 bar is not (it is a real reading).
func TestTpmsPSI_ZeroBar_NonNilResult(t *testing.T) {
	zero := 0.0
	s := Snapshot{
		TpmsPressureFL: &zero,
		TpmsPressureFR: &zero,
		TpmsPressureRL: &zero,
		TpmsPressureRR: &zero,
	}

	tests := []struct {
		name string
		got  *float64
	}{
		{"TpmsPressureFLPSI", s.TpmsPressureFLPSI()},
		{"TpmsPressureFRPSI", s.TpmsPressureFRPSI()},
		{"TpmsPressureRLPSI", s.TpmsPressureRLPSI()},
		{"TpmsPressureRRPSI", s.TpmsPressureRRPSI()},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got == nil {
				t.Fatalf("%s: want non-nil *0.0 for a truthful 0.0 bar, got nil — D12 violated", tc.name)
			}
			if *tc.got != 0.0 {
				t.Errorf("%s: want *0.0, got *%v", tc.name, *tc.got)
			}
		})
	}
}
