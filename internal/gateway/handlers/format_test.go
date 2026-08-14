package handlers

import "testing"

// TestFormatMoney covers design.md D5's required table: zero, negative,
// just-below-1000, just-at-1000 (comma boundary), and the ticket's own
// worked 58000 example.
func TestFormatMoney(t *testing.T) {
	cases := []struct {
		name     string
		amount   float64
		currency string
		want     string
	}{
		{"zero", 0, "COP", "0.00 COP"},
		{"negative", -1500.5, "COP", "-1,500.50 COP"},
		{"below thousand boundary", 999, "COP", "999.00 COP"},
		{"at thousand boundary", 1000, "COP", "1,000.00 COP"},
		{"ticket worked example", 58000, "COP", "58,000.00 COP"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatMoney(tc.amount, tc.currency)
			if got != tc.want {
				t.Errorf("formatMoney(%v, %q) = %q, want %q", tc.amount, tc.currency, got, tc.want)
			}
		})
	}
}
