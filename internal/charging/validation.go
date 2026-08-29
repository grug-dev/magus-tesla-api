// validation.go — status normalization and required-field enforcement for manual
// charge entries (MAG-18/RM33 design.md D5/D8). Both normalizeStatus and
// missingFields are called from Writer.Create/Writer.Update in service.go, in that
// order, before any database call or the capacity-derivation seam (design.md D3).
package charging

import (
	"fmt"
)

// RequiredFieldsFor returns the fields an entry must carry to be stored with the
// given status. It is the SINGLE SOURCE OF TRUTH for that rule: Writer.Create and
// Writer.Update enforce exactly this, and internal/gateway (tier 2) drives which
// inputs render as required from exactly this. Change the sets here and both
// layers follow — deliberately NOT backed by a database CHECK, which would turn
// every future change to the skip set into a migration (design.md D5).
//
// Returns a fresh slice on every call, so a caller mutating the result cannot
// corrupt the rule for the next one.
//
// An unrecognized status returns the DONE (strictest) set — fail-closed. In
// practice unreachable: Writer rejects an unknown status via normalizeStatus
// before this is ever called with one (design.md D8).
func RequiredFieldsFor(s Status) []Field {
	switch s {
	case StatusInProgress:
		return []Field{FieldChargedOn, FieldLocationKind}
	case StatusDone:
		return []Field{FieldChargedOn, FieldLocationKind, FieldEndedAt, FieldEndBatteryPct}
	default:
		return []Field{FieldChargedOn, FieldLocationKind, FieldEndedAt, FieldEndBatteryPct}
	}
}

// normalizeStatus maps an empty Status ("" — Go's zero value) to StatusInProgress,
// the same value the status column's DEFAULT assigns, and rejects any other value
// that is neither StatusInProgress nor StatusDone. This is what makes this tier
// shippable on its own before the gateway sends a status at all (design.md D8):
// parseChargeForm builds an Entry with no Status field today, so it arrives as "".
// The database CHECK remains the backstop, not the error message.
func normalizeStatus(s Status) (Status, error) {
	switch s {
	case "":
		return StatusInProgress, nil
	case StatusInProgress, StatusDone:
		return s, nil
	default:
		return "", fmt.Errorf("charging: unrecognized status %q", string(s))
	}
}

// missingFields evaluates RequiredFieldsFor(e.Status) against e and returns every
// field that is absent, in the lookup's own order (so the resulting error message
// is deterministic). A Field counts as present iff:
//   - FieldChargedOn: !e.ChargedOn.IsZero()
//   - FieldLocationKind: e.LocationKind is non-nil and non-empty
//   - FieldEndedAt: e.EndedAt is non-nil
//   - FieldEndBatteryPct: e.EndBatteryPct is non-nil
//
// (design.md D5). Called with e.Status already normalized by normalizeStatus.
func missingFields(e Entry) []Field {
	var missing []Field
	for _, f := range RequiredFieldsFor(e.Status) {
		present := false
		switch f {
		case FieldChargedOn:
			present = !e.ChargedOn.IsZero()
		case FieldLocationKind:
			present = e.LocationKind != nil && *e.LocationKind != ""
		case FieldEndedAt:
			present = e.EndedAt != nil
		case FieldEndBatteryPct:
			present = e.EndBatteryPct != nil
		}
		if !present {
			missing = append(missing, f)
		}
	}
	return missing
}
