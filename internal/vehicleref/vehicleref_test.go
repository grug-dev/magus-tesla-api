package vehicleref_test

import (
	"reflect"
	"testing"

	"github.com/cristianpena/magus-tesla-api/internal/vehicleref"
)

func TestAuthorize(t *testing.T) {
	owned := []int64{111, 222, 333}

	t.Run("hit: want is in owned", func(t *testing.T) {
		ref, ok := vehicleref.Authorize(owned, 222)
		if !ok {
			t.Fatalf("Authorize(%v, 222) ok = false, want true", owned)
		}
		if ref.TeslaID() != 222 {
			t.Errorf("ref.TeslaID() = %d, want 222", ref.TeslaID())
		}
	})

	t.Run("miss: want is not in owned", func(t *testing.T) {
		_, ok := vehicleref.Authorize(owned, 999)
		if ok {
			t.Fatalf("Authorize(%v, 999) ok = true, want false", owned)
		}
	})

	t.Run("miss: owned is empty", func(t *testing.T) {
		_, ok := vehicleref.Authorize(nil, 222)
		if ok {
			t.Fatalf("Authorize(nil, 222) ok = true, want false")
		}
	})
}

func TestAll(t *testing.T) {
	t.Run("every owned id yields one ref, order preserved", func(t *testing.T) {
		owned := []int64{111, 222, 333}
		refs := vehicleref.All(owned)
		if len(refs) != 3 {
			t.Fatalf("len(All(%v)) = %d, want 3", owned, len(refs))
		}
		got := vehicleref.TeslaIDs(refs)
		want := []int64{111, 222, 333}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("TeslaIDs(All(%v)) = %v, want %v", owned, got, want)
		}
	})

	t.Run("nil owned yields no refs", func(t *testing.T) {
		refs := vehicleref.All(nil)
		if len(refs) != 0 {
			t.Errorf("len(All(nil)) = %d, want 0", len(refs))
		}
	})
}

func TestTeslaIDs(t *testing.T) {
	t.Run("round-trips with All, same order", func(t *testing.T) {
		owned := []int64{111, 222, 333}
		refs := vehicleref.All(owned)
		got := vehicleref.TeslaIDs(refs)
		if !reflect.DeepEqual(got, owned) {
			t.Errorf("TeslaIDs(All(%v)) = %v, want %v", owned, got, owned)
		}
	})

	t.Run("nil refs yields zero-length result", func(t *testing.T) {
		got := vehicleref.TeslaIDs(nil)
		if len(got) != 0 {
			t.Errorf("len(TeslaIDs(nil)) = %d, want 0", len(got))
		}
	})

	t.Run("built via Authorize, one per case", func(t *testing.T) {
		owned := []int64{111, 222, 333}
		var refs []vehicleref.Ref
		for _, want := range []int64{111, 333} {
			ref, ok := vehicleref.Authorize(owned, want)
			if !ok {
				t.Fatalf("Authorize(%v, %d) ok = false, want true", owned, want)
			}
			refs = append(refs, ref)
		}
		got := vehicleref.TeslaIDs(refs)
		want := []int64{111, 333}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("TeslaIDs(refs) = %v, want %v", got, want)
		}
	})
}
