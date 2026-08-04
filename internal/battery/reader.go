package battery

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/manualcharge"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// chargingSourceLimit bounds each of the two charging-cost source reads (design.md D6).
// Neither port supports a since filter; battery fetches this many newest-first rows and
// filters to the window in Go. Generous relative to any plausible 30-day session count.
const chargingSourceLimit = 200

// vehicleLookup is a narrow consumer interface over account.Service — this module needs
// only CarType resolution for one vehicle (ai/go-conventions.md "accept interfaces").
// Any account.Service implementation satisfies this automatically.
type vehicleLookup interface {
	RegisteredVehicles(ctx context.Context, accountID uuid.UUID) ([]account.Vehicle, error)
}

type reader struct {
	telemetry    telemetry.Reader
	supercharger telemetry.SuperchargerReader
	manual       manualcharge.Reader
	account      vehicleLookup
	window       time.Duration
	now          func() time.Time
}

// Compile-time assertion: *reader must satisfy the public Reader interface.
var _ Reader = (*reader)(nil)

// NewReader constructs a Reader over the three sibling ports it consumes plus a narrow
// account lookup, with window fixed at construction time (design.md D3).
func NewReader(telemetryReader telemetry.Reader, supercharger telemetry.SuperchargerReader, manual manualcharge.Reader, acct vehicleLookup, window time.Duration) Reader {
	return &reader{
		telemetry:    telemetryReader,
		supercharger: supercharger,
		manual:       manual,
		account:      acct,
		window:       window,
		now:          time.Now,
	}
}

// carTypeFor resolves the car_type code for teslaID within accountID's registered
// vehicles. Returns "" (never an error solely for "not found") when the vehicle is
// absent from the list or its CarType is nil — capacityFor("") naturally reports
// known=false, collapsing into the D1b "unknown" path with no special casing.
func carTypeFor(ctx context.Context, acct vehicleLookup, accountID uuid.UUID, teslaID int64) (string, error) {
	vehicles, err := acct.RegisteredVehicles(ctx, accountID)
	if err != nil {
		return "", err
	}
	for _, v := range vehicles {
		if v.TeslaID == teslaID && v.CarType != nil {
			return *v.CarType, nil
		}
	}
	return "", nil
}

// sumSuperchargerKWh sums EnergyKWh across sessions at or after since. Sessions
// before since are skipped — this is the D6 in-Go date filter, since
// SuperchargerSessionsByVehicle only supports a limit, not a since parameter.
func sumSuperchargerKWh(sessions []telemetry.SuperchargerSession, since time.Time) float64 {
	var total float64
	for _, s := range sessions {
		if s.ChargeStartDateTime.Before(since) {
			continue
		}
		if s.EnergyKWh != nil {
			total += *s.EnergyKWh
		}
	}
	return total
}

// sumManualKWh sums EnergyAddedKWh across entries at or after since — the D6 in-Go
// date filter for the manual-charge source (ListEntriesByVehicle also has no
// since parameter).
func sumManualKWh(entries []manualcharge.Entry, since time.Time) float64 {
	var total float64
	for _, e := range entries {
		if e.ChargedOn.Before(since) {
			continue
		}
		total += e.EnergyAddedKWh
	}
	return total
}

// RecentEfficiency implements Reader (design.md "Public Surface", D4 accountID scoping).
// It issues exactly one call per consumed port (telemetry, supercharger, manual,
// account), never a per-snapshot or per-session lookup (ai/architecture.md §7
// read-heavy profile).
func (r *reader) RecentEfficiency(ctx context.Context, accountID uuid.UUID, teslaID int64) (Efficiency, bool, error) {
	since := r.now().Add(-r.window)

	snapshots, err := r.telemetry.SnapshotsByVehicleSince(ctx, accountID, teslaID, since)
	if err != nil {
		return Efficiency{}, false, err
	}

	sessions, err := r.supercharger.SuperchargerSessionsByVehicle(ctx, accountID, teslaID, chargingSourceLimit)
	if err != nil {
		return Efficiency{}, false, err
	}

	entries, err := r.manual.ListEntriesByVehicle(ctx, accountID, teslaID, chargingSourceLimit)
	if err != nil {
		return Efficiency{}, false, err
	}

	carType, err := carTypeFor(ctx, r.account, accountID, teslaID)
	if err != nil {
		return Efficiency{}, false, err
	}

	kWhIn := sumSuperchargerKWh(sessions, since) + sumManualKWh(entries, since)
	capacity, known := capacityFor(carType)

	eff, ok := deriveEfficiency(snapshots, kWhIn, capacity, known)
	return eff, ok, nil
}
