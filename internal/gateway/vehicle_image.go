// Package gateway owns the embedded static assets and builds the vehicle-image
// resolver that handlers consume (handlers cannot embed ../static — go:embed
// forbids "..", and handlers must not import gateway: gateway imports handlers).
// The resolver is constructed once in NewEngine from the embedded static/img
// subtree and injected into handlers.Deps, respecting the gateway→handlers
// dependency direction.
package gateway

import (
	"io/fs"
	"path"
	"strings"
)

const (
	vehicleImageDir     = "static/img"
	vehicleImageURLBase = "/static/img/"
	vehicleImageDefault = "/static/img/defaultCar.png"
)

// newVehicleImageResolver builds a resolver backed by the embedded static/img
// subtree of staticFS. The set of known filenames is enumerated once at
// construction (closed vocabulary) and looked up per call — deterministic, no
// runtime disk stat, identical in dev and production. Returns a plain func so
// it is directly assignable to handlers.VehicleImageResolver (the consumer's
// named type); no parallel named type lives here to avoid cross-package
// assignability friction.
func newVehicleImageResolver(staticFS fs.FS) func(carType, exteriorColor *string) string {
	known := knownVehicleImages(staticFS)
	return func(carType, exteriorColor *string) string {
		if carType == nil || exteriorColor == nil ||
			*carType == "" || *exteriorColor == "" {
			return vehicleImageDefault
		}
		name := *carType + *exteriorColor + ".png"
		if !known[name] {
			return vehicleImageDefault
		}
		return vehicleImageURLBase + name
	}
}

// knownVehicleImages enumerates the .png filenames (bare, no path) present in
// the embedded static/img directory. Missing directory degrades to an empty
// set — every call then falls back to defaultCar.png.
func knownVehicleImages(staticFS fs.FS) map[string]bool {
	set := make(map[string]bool)
	entries, err := fs.ReadDir(staticFS, vehicleImageDir)
	if err != nil {
		return set
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.ToLower(path.Ext(name)) == ".png" {
			set[name] = true
		}
	}
	return set
}
