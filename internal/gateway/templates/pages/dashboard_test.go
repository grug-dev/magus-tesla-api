package pages

import (
	"context"
	"testing"

	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
)

// ptrBool returns a pointer to b -- a small test-local helper, mirroring the
// ptrInt/ptrTime helpers already established in the handlers package's own test
// files (supercharger_test.go, external_charges_tiles_test.go). Added by
// RM38-gateway-read-dashboard-from-metrics for this package's first test file.
func ptrBool(b bool) *bool { return &b }

// TestDashLockedBadgeAndDashSentryBadge_BadgeMatrix covers all nine combinations
// of design.md's Test Contract badge matrix (RM38-gateway-read-dashboard-from-metrics,
// design.md D4/D7) for dashLockedBadge and dashSentryBadge -- pure function tests,
// no gin/httptest needed (tasks.md 2.1).
func TestDashLockedBadgeAndDashSentryBadge_BadgeMatrix(t *testing.T) {
	ctx := i18n.WithLang(context.Background(), "en")

	locked := map[string]*bool{"true": ptrBool(true), "false": ptrBool(false), "nil": nil}
	sentry := map[string]*bool{"true": ptrBool(true), "false": ptrBool(false), "nil": nil}
	order := []string{"true", "false", "nil"}

	type want struct {
		text, kind string
		show       bool
	}

	wantLocked := map[string]want{
		"true":  {i18n.T(ctx, i18n.KeyVehiclesLocked), "success", true},
		"false": {i18n.T(ctx, i18n.KeyVehiclesUnlocked), "error", true},
		"nil":   {"", "", false},
	}
	wantSentry := map[string]want{
		"true":  {i18n.T(ctx, i18n.KeyVehiclesSentryLabel) + " " + i18n.T(ctx, i18n.KeyVehiclesOn), "warning", true},
		"false": {i18n.T(ctx, i18n.KeyVehiclesSentryLabel) + " " + i18n.T(ctx, i18n.KeyVehiclesOff), "ghost", true},
		"nil":   {"", "", false},
	}

	for _, lk := range order {
		for _, sk := range order {
			name := "locked=" + lk + "/sentry=" + sk
			t.Run(name, func(t *testing.T) {
				lText, lKind, lShow := dashLockedBadge(ctx, locked[lk])
				wl := wantLocked[lk]
				if lText != wl.text || lKind != wl.kind || lShow != wl.show {
					t.Errorf("dashLockedBadge(locked=%s) = (%q, %q, %v), want (%q, %q, %v)",
						lk, lText, lKind, lShow, wl.text, wl.kind, wl.show)
				}

				sText, sKind, sShow := dashSentryBadge(ctx, sentry[sk])
				ws := wantSentry[sk]
				if sText != ws.text || sKind != ws.kind || sShow != ws.show {
					t.Errorf("dashSentryBadge(sentry=%s) = (%q, %q, %v), want (%q, %q, %v)",
						sk, sText, sKind, sShow, ws.text, ws.kind, ws.show)
				}
			})
		}
	}
}
