package handlers

import (
	"strings"
	"testing"

	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/fragments"
)

// This file pins the 2026-08-29 manual-charge form layout:
//
//   - location_kind is the LAST field of the main grid,
//   - the create and edit forms render the nine shared fields in the SAME order,
//   - the "(optional)" hint marks the unconditionally-optional main-grid fields
//     and stays OFF the conditionally-required ones.
//
// sharedFieldOrder is the single source of truth both forms are checked
// against, so a future reorder is one edit here plus one in each template —
// not a hunt through assertions.
var sharedFieldOrder = []string{
	"status",
	"charged_on",
	"energy_added_kwh",
	"price",
	"started_at",
	"ended_at",
	"start_battery_pct",
	"end_battery_pct",
	"location_kind", // moved to the END of the main grid
	// the optional-details section follows, in this order:
	"charging_type",
	"location_label",
	"odometer_km",
	"notes",
}

// fieldPositions returns the render offset of each control in order, failing
// the test if any is missing entirely.
func fieldPositions(t *testing.T, formName, body string, names []string) []int {
	t.Helper()
	pos := make([]int, len(names))
	for i, n := range names {
		idx := strings.Index(body, `name="`+n+`"`)
		if idx == -1 {
			t.Fatalf("%s form: control %q is missing from the rendered markup", formName, n)
		}
		pos[i] = idx
	}
	return pos
}

// TestChargeForms_FieldOrderIsSharedAndLocationLast pins requirements 1 and 3:
// Location sits at the end of the main grid, and the edit row renders the same
// field sequence the create form does. Asserting both forms against ONE list is
// what makes "the same order" checkable rather than aspirational — the two
// templates drifted before (location_kind and ended_at used to be buried in the
// edit row's collapse while sitting in the create form's main grid).
func TestChargeForms_FieldOrderIsSharedAndLocationLast(t *testing.T) {
	for _, form := range []struct {
		name string
		body string
	}{
		{"create", renderCreateForm(t, fragments.ChargesPageData{}, "")},
		{"edit", renderEditRow(t, fragments.ChargeEntryVM{}, "")},
	} {
		t.Run(form.name, func(t *testing.T) {
			pos := fieldPositions(t, form.name, form.body, sharedFieldOrder)
			for i := 1; i < len(pos); i++ {
				if pos[i] <= pos[i-1] {
					t.Errorf("%s form: %q must render after %q (offsets %d vs %d)",
						form.name, sharedFieldOrder[i], sharedFieldOrder[i-1], pos[i], pos[i-1])
				}
			}
		})
	}
}

// TestChargeForms_LocationIsLastInTheMainGrid pins requirement 1 specifically:
// location_kind is the final control of the main grid, i.e. it renders before
// the optional-details section opens. The order test above would still pass if
// location_kind slid down INTO that section, which would be wrong — it is
// required.
func TestChargeForms_LocationIsLastInTheMainGrid(t *testing.T) {
	for _, form := range []struct {
		name string
		body string
	}{
		{"create", renderCreateForm(t, fragments.ChargesPageData{}, "")},
		{"edit", renderEditRow(t, fragments.ChargeEntryVM{}, "")},
	} {
		t.Run(form.name, func(t *testing.T) {
			locIdx := strings.Index(form.body, `name="location_kind"`)
			sectionIdx := strings.Index(form.body, "<section id=")
			if locIdx == -1 || sectionIdx == -1 {
				t.Fatalf("%s form: missing location_kind or optional-details section", form.name)
			}
			if locIdx > sectionIdx {
				t.Errorf("%s form: location_kind is REQUIRED and must stay in the main grid, not the optional section (loc=%d section=%d)",
					form.name, locIdx, sectionIdx)
			}
		})
	}
}

// TestChargeForms_OptionalHintMarksOnlyUnconditionalOptionals pins requirement 4
// and the deliberate limit on it. The hint is a LABEL marker (ui.FieldProps
// .Optional) rather than a placeholder because browsers ignore `placeholder` on
// datetime-local and <select> has none at all — started_at and charging_type
// would have been silently unmarked.
//
// ended_at and end_battery_pct must NOT carry it: their required-ness follows
// the status select and is toggled client-side by RD13, so a server-rendered
// "(optional)" would go stale the moment the user picks DONE.
func TestChargeForms_OptionalHintMarksOnlyUnconditionalOptionals(t *testing.T) {
	// legendFor returns the fieldset legend text preceding the named control.
	legendFor := func(body, name string) string {
		ctrl := strings.Index(body, `name="`+name+`"`)
		if ctrl == -1 {
			return ""
		}
		head := body[:ctrl]
		open := strings.LastIndex(head, "<legend")
		close := strings.LastIndex(head, "</legend>")
		if open == -1 || close == -1 || close < open {
			return ""
		}
		return head[open:close]
	}

	for _, form := range []struct {
		name string
		body string
	}{
		{"create", renderCreateForm(t, fragments.ChargesPageData{}, "")},
		{"edit", renderEditRow(t, fragments.ChargeEntryVM{}, "")},
	} {
		t.Run(form.name, func(t *testing.T) {
			// ES is the fallback language when no lang is set on the context.
			const hint = "(opcional)"
			for _, name := range []string{"energy_added_kwh", "price", "started_at"} {
				if !strings.Contains(legendFor(form.body, name), hint) {
					t.Errorf("%s form: %q is unconditionally optional and must carry the %q hint; legend was %q",
						form.name, name, hint, legendFor(form.body, name))
				}
			}
			for _, name := range []string{"ended_at", "end_battery_pct", "status", "charged_on", "start_battery_pct", "location_kind"} {
				if strings.Contains(legendFor(form.body, name), hint) {
					t.Errorf("%s form: %q must NOT carry the %q hint — it is required, or conditionally required and toggled client-side",
						form.name, name, hint)
				}
			}
		})
	}
}
