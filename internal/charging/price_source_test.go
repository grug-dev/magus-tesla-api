// price_source_test.go — offline unit tests (no DB) for resolvePriceSource,
// added by RM51-charging-derive-status-and-price-source tier 1 (MAG-58).
// Covers design.md Test Contract A7-A10. Package charging (NOT
// charging_test): resolvePriceSource is deliberately unexported (design.md
// D3), mirroring entry_status_test.go's own package choice for
// promoteIfComplete.
//
// Every expected value below is copied VERBATIM from design.md's Test
// Contract, authored before this file existed (ai/go-conventions.md
// §Testing authoring order) -- this file asserts that contract, not
// whatever the implementation happens to produce.
package charging

import "testing"

// TestResolvePriceSource_Table implements design.md Test Contract A7-A10:
// the three RD3 rule-table branches plus the precedence case (A10, D5) --
// a positive price wins regardless of PriceConfirmed.
func TestResolvePriceSource_Table(t *testing.T) {
	cases := []struct {
		id             string
		price          float64
		priceConfirmed bool
		want           PriceSource
	}{
		// A7: a positive price is always USER (RD3 row 1).
		{"A7", 100.00, false, PriceSourceUser},
		// A8: a confirmed zero is USER (RD3 row 2).
		{"A8", 0, true, PriceSourceUser},
		// A9: an unconfirmed zero is UNCONFIRMED (RD3 row 3).
		{"A9", 0, false, PriceSourceUnconfirmed},
		// A10: precedence (D5) -- a positive price wins regardless of the
		// confirmation flag, the same outcome as A7, proving the flag is not
		// even consulted.
		{"A10", 100.00, true, PriceSourceUser},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			e := Entry{Price: tc.price, PriceConfirmed: tc.priceConfirmed}
			got := resolvePriceSource(e)
			if got != tc.want {
				t.Errorf("%s: resolvePriceSource(Price=%v, PriceConfirmed=%v) = %v, want %v",
					tc.id, tc.price, tc.priceConfirmed, got, tc.want)
			}
		})
	}
}
