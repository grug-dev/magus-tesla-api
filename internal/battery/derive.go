package battery

import "github.com/cristianpena/magus-tesla-api/internal/telemetry"

// socReadings returns the state-of-charge percent at the window's start and
// end, choosing UsableBatteryLevel for BOTH endpoints when both are non-nil,
// else BatteryLevel for both — never a mixed pair (design.md D2). A
// per-endpoint fallback would risk manufacturing a phantom ΔSoC of a few
// percent whenever UsableBatteryLevel and BatteryLevel diverge (cold-weather
// derating), so the field family is chosen once for the whole computation.
func socReadings(start, end telemetry.Snapshot) (socStart, socEnd float64) {
	if start.UsableBatteryLevel != nil && end.UsableBatteryLevel != nil {
		return float64(*start.UsableBatteryLevel), float64(*end.UsableBatteryLevel)
	}
	return float64(start.BatteryLevel), float64(end.BatteryLevel)
}

// deriveEfficiency computes the Wh/km result from a window's ordered
// (oldest-first) snapshots, the summed measured charging energy (kWh)
// observed in the window, and the vehicle's pack capacity
// (capacityKnown=false when unknown — design.md D1b). It returns ok=false
// (design.md D-ok), never an error, when: fewer than 2 snapshots; distance <=
// 0; or the derived energy <= 0. Unknown pack capacity is explicitly NOT an
// ok=false case (design.md D1b) — the SoC-drift correction term is simply
// dropped and Approximate is set true.
func deriveEfficiency(snapshots []telemetry.Snapshot, kWhIn float64, capacityKWh float64, capacityKnown bool) (Efficiency, bool) {
	if len(snapshots) < 2 {
		return Efficiency{}, false
	}
	start, end := snapshots[0], snapshots[len(snapshots)-1]

	distance := end.OdometerKm() - start.OdometerKm()
	if distance <= 0 {
		return Efficiency{}, false
	}

	socStart, socEnd := socReadings(start, end)
	deltaSoC := socEnd - socStart // positive = net charge, per design.md D1's formula

	var energy float64
	approximate := false
	if capacityKnown {
		energy = kWhIn - capacityKWh*deltaSoC/100
	} else {
		energy = kWhIn
		approximate = true
	}
	if energy <= 0 {
		return Efficiency{}, false
	}

	return Efficiency{
		WhPerKm:         energy * 1000 / distance,
		FromKm:          start.OdometerKm(),
		ToKm:            end.OdometerKm(),
		BatteryDeltaPct: socStart - socEnd, // negative = net charge (Efficiency doc comment)
		Approximate:     approximate,
	}, true
}
