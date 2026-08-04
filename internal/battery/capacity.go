package battery

// packCapacityKWh maps a Fleet API vehicle_config.car_type code to that model's
// approximate usable pack capacity in kWh. Human-maintained from public Tesla
// spec sheets — this is a reference constant, not a database object
// (design.md D1b / "Database Changes"; not subject to the `database` design
// gate). It is model-coarse, not trim-exact: the live vehicle_config payload
// carries trim_badging (~47 keys), but internal/tesla's VehicleConfigTesla does
// not extract it, so e.g. a "Model 3 Standard Range" and a "Model 3 Long Range"
// share car_type "model3" despite different real pack capacities (design.md D1b
// "IMPORTANT — model-coarse, not trim-exact"; trim-exact capacity is tracked as
// backlog item 7). Add a new entry when a model this platform's users own is
// missing — until then, that model's efficiency is still computed
// (Efficiency.Approximate=true), never blanked.
var packCapacityKWh = map[string]float64{
	"model3": 75,
	"modely": 75,
	"models": 100,
	"modelx": 100,
}

// capacityFor returns the usable pack capacity in kWh for carType, and whether
// it was found. An empty carType (a nil account.Vehicle.CarType, or a vehicle
// not found in the account's registered list) naturally returns known=false —
// "" is never a map key, so this collapses into the same D1b "unknown" path
// with no special casing needed.
func capacityFor(carType string) (kWh float64, known bool) {
	kWh, known = packCapacityKWh[carType]
	return kWh, known
}
