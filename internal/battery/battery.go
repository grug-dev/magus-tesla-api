// Package battery is the platform's first derived-metrics module. It owns
// analytics computed FROM other modules' stored data, not the data itself: its
// one metric today is a rolling energy-per-kilometre (Wh/km) efficiency figure
// over a fixed window, derived from internal/telemetry's snapshot history plus
// the two charging-cost sources the platform stores (internal/telemetry's
// SuperchargerReader and internal/manualcharge), corrected for pack capacity via
// a small in-package reference table keyed on the vehicle's car_type.
//
// This module owns no database and no store — it is a pure read-side derivation
// reached exclusively through its sibling modules' public Reader ports (see
// reader.go). Domain types here carry NO vendor suffix — they are our own models
// (ai/architecture.md §6). Full design rationale:
// openspec/changes/battery-add-efficiency-metric/design.md.
package battery

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// DefaultWindow is the recommended NewReader window — 30 days, matching the
// dashboard's 30-day history cards (design.md D3). Deployment code may pass a
// different duration for a future analytics page without changing the port.
const DefaultWindow = 30 * 24 * time.Hour

// Reader is the battery module's public port (ai/go-conventions.md
// interface-first) — the only mandatory contract the gateway and sibling
// modules depend on.
type Reader interface {
	// RecentEfficiency returns the rolling energy-per-kilometre for the given
	// vehicle over the window NewReader was constructed with, derived from
	// stored telemetry plus the two charging-cost sources this platform
	// stores. It returns ok=false (no error) when there is not enough data to
	// compute a meaningful value (design.md D-ok) — never a fabricated
	// number.
	RecentEfficiency(ctx context.Context, accountID uuid.UUID, teslaID int64) (Efficiency, bool, error)
}

// Efficiency is one computed rolling-efficiency result — our own domain model,
// no vendor suffix (ai/architecture.md §6). FromKm/ToKm are read directly from
// telemetry.Snapshot.OdometerKm — already km-native at capture time
// (telemetry-store-display-units design D1/D3) — so this module performs no
// unit conversion and no further Km() companion applies here.
type Efficiency struct {
	// WhPerKm is the derived energy-per-kilometre figure in watt-hours,
	// raw and unrounded (design.md D5) — the gateway formats it for display.
	WhPerKm float64
	// FromKm is the odometer reading at the window's start, in km.
	FromKm float64
	// ToKm is the odometer reading at the window's end, in km.
	ToKm float64
	// BatteryDeltaPct is the net state-of-charge change over the window:
	// start − end. Negative means the vehicle net-charged over the window.
	BatteryDeltaPct float64
	// Approximate is true when the vehicle's pack capacity was unknown and
	// the SoC-drift correction term was dropped (design.md D1b) — the result
	// is still a real computed value, never a fabricated one.
	Approximate bool
}
