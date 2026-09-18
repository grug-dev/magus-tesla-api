package handlers

import (
	"context"
	"testing"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
)

// dashNeutralTrend drives Distance travelled and Battery used. Four cases
// cover its whole vocabulary: positive, negative, exact zero, and nil.
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

// dashColoredTrend drives Efficiency and the four tyre wheels. The tyre
// behaviour must not move, so the same four cases guard both callers.
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

// dashDeltaDesc is where an exact zero and an absent delta stop looking
// alike: zero writes a line, nil writes none. formatSignedKm is the
// example formatter.
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
