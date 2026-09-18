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

// TestFormatSignedKm covers design.md's Test Contract table: positive,
// negative, exact zero, and the rounding case (42.6 -> "+43", matching
// formatKm's own rounding rule).
func TestFormatSignedKm(t *testing.T) {
	cases := []struct {
		name string
		v    float64
		want string
	}{
		{"positive", 42.0, "+42"},
		{"negative", -17.0, "-17"},
		{"exact zero", 0.0, "0"},
		{"rounds up like formatKm", 42.6, "+43"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatSignedKm(tc.v)
			if got != tc.want {
				t.Errorf("formatSignedKm(%v) = %q, want %q", tc.v, got, tc.want)
			}
		})
	}
}

// TestFormatSignedPct covers design.md's Test Contract table: one decimal,
// same rule as the existing formatSignedPSI.
func TestFormatSignedPct(t *testing.T) {
	cases := []struct {
		name string
		v    float64
		want string
	}{
		{"positive", 3.2, "+3.2"},
		{"negative", -1.5, "-1.5"},
		{"exact zero", 0.0, "0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatSignedPct(tc.v)
			if got != tc.want {
				t.Errorf("formatSignedPct(%v) = %q, want %q", tc.v, got, tc.want)
			}
		})
	}
}

// TestFormatSignedKmPerPct covers design.md's Test Contract table: one
// decimal, same rule as the existing formatSignedPSI.
func TestFormatSignedKmPerPct(t *testing.T) {
	cases := []struct {
		name string
		v    float64
		want string
	}{
		{"positive", 3.2, "+3.2"},
		{"negative", -1.5, "-1.5"},
		{"exact zero", 0.0, "0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatSignedKmPerPct(tc.v)
			if got != tc.want {
				t.Errorf("formatSignedKmPerPct(%v) = %q, want %q", tc.v, got, tc.want)
			}
		})
	}
}
