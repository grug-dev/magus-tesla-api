// vehicle_stats_gaps.go builds the Vehicle Stats page's missing-charge-records
// warning: the days inside the selected period whose battery use the stored
// charge records cannot account for.
//
// # Why this reads vehicle_metrics and NOT analytics.charge_gaps
//
// charge_gaps is the module's WORKLIST, not a live truth. A row is deleted only
// by GapWriter.ReconcileWindow, and only inside its 30-day window; the gateway's
// manual-charge write path calls Recalculate (which clears vehicle_metrics.flagged)
// but never ReconcileWindow. So a gap the user fixed more than 30 days ago keeps
// its charge_gaps row forever while vehicle_metrics correctly stops flagging the
// day.
//
// Measured on this repo's own data for vehicle 3744327027802250: charge_gaps held
// six days, of which 2026-07-19 and 2026-08-03 were already resolved — 08-03 has
// two AC entries and a normal 4.0% / 11.2 km day, and its gap row was last touched
// 2026-09-02, before falling out of the window. Warning about those two would send
// the user to fix work they had already done, which is worse than not warning at
// all.
//
// analytics.Reader.ConsumedByDay carries the live Flagged and MissingChargingType
// per day, and the gateway already holds that port — so this costs no new
// dependency and no new module surface.
package handlers

import (
	"context"
	"fmt"
	"strconv"

	"github.com/cristianpena/magus-tesla-api/internal/analytics"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/fragments"
)

// vehicleStatsGapsMax caps how many flagged days the warning lists. A whole-year
// period can flag more days than anyone will read through, and the point of the
// section is to prompt an action, not to be a complete ledger. The remainder is
// COUNTED rather than dropped, so the reader always knows the list is partial.
const vehicleStatsGapsMax = 10

// buildVehicleStatsGaps maps the flagged days in days onto the warning rows,
// resolving each to the page that can fix it. Returns the capped list and how
// many further flagged days were left out.
//
// days is whatever ConsumedByDay returned for the SAME window the tiles used, so
// the warning can never describe a different period from the figures it warns
// about. The slice is sparse and already ordered oldest-first by the query.
//
// A flagged day whose MissingChargingType is neither known value is SKIPPED: the
// row's whole value is the link to the page that fixes it, and without the type
// there is no way to choose that page. Showing a warning a reader cannot act on
// would just be noise.
func buildVehicleStatsGaps(ctx context.Context, days []analytics.DayConsumption) ([]fragments.VehicleStatsGap, int) {
	var gaps []fragments.VehicleStatsGap
	more := 0

	for _, d := range days {
		if !d.Flagged {
			continue
		}
		kindKey, actionKey, page := gapCopyFor(d.MissingChargingType)
		if page == "" {
			continue
		}
		if len(gaps) >= vehicleStatsGapsMax {
			more++
			continue
		}
		day := d.Date.Format("2006-01-02")
		gaps = append(gaps, fragments.VehicleStatsGap{
			// Split, not one string: the cell typesets the day number as its
			// anchor and the month beneath it, which a joined label could not
			// do. monthKeys is the same array the period control uses
			// (vehicle_stats_period.go) — one month vocabulary, not two.
			DayLabel:    strconv.Itoa(d.Date.Day()),
			MonthLabel:  i18n.T(ctx, monthKeys[d.Date.Month()]),
			KindLabel:   i18n.T(ctx, kindKey),
			ActionLabel: i18n.T(ctx, actionKey),
			// Both pages take a closed ?start=&end= window and both accept a
			// single day, so the reader lands on exactly the day that is
			// missing a record rather than on an unfiltered page.
			ActionHref: fmt.Sprintf("%s?start=%s&end=%s", page, day, day),
		})
	}
	return gaps, more
}

// gapCopyFor maps a missing-charge kind to its badge label, its action label and
// the page that fixes it. Both labels are SHORT by design: the badge cannot wrap
// (it only widens), and the full explanation is the section's own description,
// said once rather than repeated in every cell. A closed switch over the module's own two-value vocabulary;
// an unknown value returns an empty page, which the caller treats as "skip".
//
// The two kinds are deliberately worded differently because the work differs:
// MANUAL means the charge happened somewhere the Fleet API does not report, so a
// record must be CREATED on the external-charges page. SUPERCHARGER means the
// session is already stored and only its two battery percentages are NULL, so an
// existing record must be COMPLETED on the supercharger-stats page.
func gapCopyFor(kind analytics.MissingChargingType) (kindKey, actionKey i18n.Key, page string) {
	switch kind {
	case analytics.MissingChargingTypeManual:
		return i18n.KeyVehicleStatsGapManual, i18n.KeyVehicleStatsGapActionManual, "/external-charges"
	case analytics.MissingChargingTypeSupercharger:
		return i18n.KeyVehicleStatsGapSupercharger, i18n.KeyVehicleStatsGapActionSC, "/supercharger-stats"
	default:
		return "", "", ""
	}
}
