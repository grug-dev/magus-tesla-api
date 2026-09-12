// Package vehicleref proves that a caller owns a vehicle, as a value instead of a boolean.
//
// Many module ports across this platform will accept "the vehicles this account may see" as
// an argument. A boolean check is easy to skip by accident: a new handler can call a port with
// a raw vehicle id and compile cleanly even if it forgot to check ownership first. Wrapping the
// id in a type that only this package can construct turns that mistake into a compile error —
// a handler with no authorized Ref has nothing to pass, so the code simply does not build. This
// is worth a small package for a surface that is both repeated (every account-scoped read) and
// security-critical (a missed check leaks one user's car data to another).
//
// This package imports NOTHING project-local. It has no table, no query, no config, and no
// external state — it operates only on int64 vehicle ids.
package vehicleref

// Ref proves that a specific vehicle id was checked against a caller's owned list. The field is
// unexported so a Ref can only be built by Authorize or All, both of which take the caller's own
// resolved ownership list as input. A struct literal from outside this package (vehicleref.Ref{})
// still compiles — the zero value carries no proof and must never be treated as one.
type Ref struct {
	teslaID int64
}

// Authorize checks whether want appears in owned. On a hit it returns a Ref for want and true.
// On a miss — including an empty owned list — it returns the zero Ref and false. Callers must
// check the bool: the zero Ref's TeslaID is 0, which for one Tesla vehicle could in theory be a
// real vendor id, so the zero value alone is not a safe "no match" signal.
func Authorize(owned []int64, want int64) (Ref, bool) {
	for _, id := range owned {
		if id == want {
			return Ref{teslaID: id}, true
		}
	}
	return Ref{}, false
}

// All wraps every id in owned into a Ref, unchecked and in order. It exists for the case where
// the caller already holds its own full, resolved ownership list and needs a Ref per vehicle —
// there is nothing left to authorize against. A nil or empty owned returns a zero-length slice.
func All(owned []int64) []Ref {
	refs := make([]Ref, 0, len(owned))
	for _, id := range owned {
		refs = append(refs, Ref{teslaID: id})
	}
	return refs
}

// TeslaIDs unwraps refs back into a plain []int64, in order — the inverse of All. Callers use
// this to build a SQL "= ANY($1)" parameter without writing their own unwrap loop. A nil or
// empty refs returns a zero-length slice.
func TeslaIDs(refs []Ref) []int64 {
	ids := make([]int64, 0, len(refs))
	for _, r := range refs {
		ids = append(ids, r.teslaID)
	}
	return ids
}

// TeslaID returns the vehicle id this Ref proves ownership of.
func (r Ref) TeslaID() int64 {
	return r.teslaID
}
