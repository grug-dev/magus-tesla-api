// external_charges_tiles.go contains the External charges page's completeness predicate and
// aggregation-tiles builder (design.md §D-Dot/§D-Tiles,
// RM33-gateway-add-entries-dashboard).
package handlers

import (
	"fmt"
	"strconv"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/fragments"
)

// entryComplete reports whether e satisfies D9's completeness bar: every
// field RequiredFieldsFor(StatusDone) would require, PLUS energy present AND
// price present. Evaluated identically regardless of e.Status itself — an
// IN_PROGRESS entry is correctly yellow until it acquires everything a DONE
// entry needs, which is the whole point of the signal (D9: "makes 'DONE but
// missing a price' visible"). ChargedOn/LocationKind are deliberately
// omitted: ChargedOn is a non-pointer time.Time the DB makes NOT NULL
// (always present), and LocationKind is unconditionally required by the
// gateway's own form — including either can never turn a green dot yellow
// for any entry the gateway itself wrote (design.md §D-Dot).
func entryComplete(e charging.Entry) bool {
	return e.EndedAt != nil &&
		e.EndBatteryPct != nil &&
		e.EnergyAddedKWh != nil &&
		e.Price > 0
}

// buildExternalChargeTiles computes the four aggregation-tile values (design.md
// §D-Tiles) over entries. Must be called over the SAME []charging.Entry
// slice ListEntriesByVehicleBetween returned, before the
// externalChargeEntryVMFromEntry mapping loop, so the tiles and the table are
// provably the same data (D13). Sums Price unconditionally — manual entries
// are COP by construction (D13), so there is no per-currency map like
// Supercharger's CostLines. Reuses formatMoney (format.go) for the Cost
// string.
func buildExternalChargeTiles(entries []charging.Entry) fragments.ExternalChargeTiles {
	var energySum, costSum float64
	var energyCount int
	for _, e := range entries {
		if e.EnergyAddedKWh != nil {
			energySum += *e.EnergyAddedKWh
			energyCount++
		}
		costSum += e.Price // always COP by construction (D13) — no per-currency map
	}

	avg := "—"
	if energyCount > 0 {
		avg = fmt.Sprintf("%.1f kWh", energySum/float64(energyCount))
	}

	return fragments.ExternalChargeTiles{
		Sessions: strconv.Itoa(len(entries)),
		Energy:   fmt.Sprintf("%.1f kWh", energySum),
		Cost:     formatMoney(costSum, "COP"),
		AvgKWh:   avg,
	}
}
