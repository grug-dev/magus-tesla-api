package handlers

import (
	"context"
	"testing"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
)

// TestDashNeutralTrend covers design.md's Test Contract table for
// dashNeutralTrend (Distance travelled, Battery used): positive, negative,
// exact-zero, and nil.
func TestDashNeutralTrend(t *testing.T) {
	cases := []struct {
		name string
		v    *float64
		want string
	}{
		{"positive", ptrF64(42.0), "up-neutral"},
		{"negative", ptrF64(-17.0), "down-neutral"},
		{"exact zero", ptrF64(0.0), ""},
		{"nil", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dashNeutralTrend(tc.v)
			if got != tc.want {
				t.Errorf("dashNeutralTrend(%v) = %q, want %q", tc.v, got, tc.want)
			}
		})
	}
}

// TestDashColoredTrend covers design.md's Test Contract table for
// dashColoredTrend (Efficiency; also the four tyre wheels, unchanged):
// positive, negative, exact-zero, and nil.
func TestDashColoredTrend(t *testing.T) {
	cases := []struct {
		name string
		v    *float64
		want string
	}{
		{"positive", ptrF64(0.4), "up"},
		{"negative", ptrF64(-0.6), "down"},
		{"exact zero", ptrF64(0.0), ""},
		{"nil", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dashColoredTrend(tc.v)
			if got != tc.want {
				t.Errorf("dashColoredTrend(%v) = %q, want %q", tc.v, got, tc.want)
			}
		})
	}
}

// TestDashDeltaDesc covers design.md's Test Contract table for dashDeltaDesc,
// using formatSignedKm as the example formatter: positive, negative,
// exact-zero (rendered — a known value), and nil (no line at all).
func TestDashDeltaDesc(t *testing.T) {
	ctx := i18n.WithLang(context.Background(), account.LanguageEN)
	cases := []struct {
		name string
		v    *float64
		want string
	}{
		{"positive", ptrF64(42.0), "+42 vs prev. day"},
		{"negative", ptrF64(-17.0), "-17 vs prev. day"},
		{"exact zero", ptrF64(0.0), "0 vs prev. day"},
		{"nil", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dashDeltaDesc(ctx, tc.v, formatSignedKm)
			if got != tc.want {
				t.Errorf("dashDeltaDesc(ctx, %v, formatSignedKm) = %q, want %q", tc.v, got, tc.want)
			}
		})
	}
}
