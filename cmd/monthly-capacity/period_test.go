package main

import (
	"testing"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/clock"
)

// This file asserts design.md's Test Contract Group A (previousMonth,
// resolvePeriod's empty-string path) and Group B (parsePeriod,
// teslaIDPointer). The expected values come from that contract, not from
// reading the implementation.

// TestPreviousMonth covers Test Contract A1-A4.
func TestPreviousMonth(t *testing.T) {
	bogota := clock.Zone()

	cases := []struct {
		id   string
		now  time.Time
		loc  *time.Location
		want time.Time
	}{
		{
			id:   "A1 plain case: September's previous month is August",
			now:  time.Date(2026, 9, 15, 10, 0, 0, 0, bogota),
			loc:  bogota,
			want: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			id:   "A2 year boundary: January's previous month is December of the prior year",
			now:  time.Date(2027, 1, 10, 8, 0, 0, 0, bogota),
			loc:  bogota,
			want: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			// This UTC instant is already September in UTC, but still August in
			// Bogota (UTC-5). The previous month must come from the Bogota
			// calendar day (July), not the UTC one (August).
			id:   "A3 zone-aware: UTC day is Sept 1, Bogota day is still Aug 31",
			now:  time.Date(2026, 9, 1, 3, 0, 0, 0, time.UTC),
			loc:  bogota,
			want: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			// Same instant as A3, but loc is time.UTC: the result changes,
			// proving loc is a real parameter and not hardcoded to Bogota.
			id:   "A4 loc is a real parameter: time.UTC changes the result",
			now:  time.Date(2026, 9, 1, 3, 0, 0, 0, time.UTC),
			loc:  time.UTC,
			want: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			got := previousMonth(c.now, c.loc)
			if !got.Equal(c.want) {
				t.Fatalf("previousMonth(%v, %v) = %v, want %v", c.now, c.loc, got, c.want)
			}
		})
	}
}

// TestResolvePeriod covers Test Contract A5-A6.
func TestResolvePeriod(t *testing.T) {
	bogota := clock.Zone()
	now := time.Date(2026, 9, 15, 10, 0, 0, 0, bogota)

	t.Run("A5 empty raw delegates to previousMonth", func(t *testing.T) {
		// Written out, not obtained from previousMonth: a test whose oracle is
		// the function under test still passes when that function breaks.
		want := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		got, err := resolvePeriod("", now, bogota)
		if err != nil {
			t.Fatalf("resolvePeriod(\"\", now, loc) returned error: %v", err)
		}
		if !got.Equal(want) {
			t.Fatalf("resolvePeriod(\"\", now, loc) = %v, want %v", got, want)
		}
	})

	t.Run("A6 a given period is parsed directly, ignoring now and loc", func(t *testing.T) {
		want := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		got, err := resolvePeriod("2026-08", now, bogota)
		if err != nil {
			t.Fatalf("resolvePeriod(\"2026-08\", now, loc) returned error: %v", err)
		}
		if !got.Equal(want) {
			t.Fatalf("resolvePeriod(\"2026-08\", now, loc) = %v, want %v", got, want)
		}
	})
}

// TestParsePeriod covers Test Contract B1-B4.
func TestParsePeriod(t *testing.T) {
	cases := []struct {
		id      string
		raw     string
		want    time.Time
		wantErr bool
	}{
		{
			id:   "B1 plain case",
			raw:  "2026-08",
			want: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			id:      "B2 out-of-range month is rejected",
			raw:     "2026-13",
			wantErr: true,
		},
		{
			id:      "B3 single-digit month is rejected, layout is strict",
			raw:     "2026-8",
			wantErr: true,
		},
		{
			id:      "B4 empty string is rejected, not special-cased",
			raw:     "",
			wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			got, err := parsePeriod(c.raw)
			if c.wantErr {
				if err == nil {
					t.Fatalf("parsePeriod(%q) returned no error, want one", c.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsePeriod(%q) returned error: %v", c.raw, err)
			}
			if !got.Equal(c.want) {
				t.Fatalf("parsePeriod(%q) = %v, want %v", c.raw, got, c.want)
			}
		})
	}
}

// TestTeslaIDPointer covers Test Contract B5-B7.
func TestTeslaIDPointer(t *testing.T) {
	t.Run("B5 an explicitly-given id is used", func(t *testing.T) {
		got := teslaIDPointer(123, true)
		if got == nil || *got != 123 {
			t.Fatalf("teslaIDPointer(123, true) = %v, want pointer to 123", got)
		}
	})

	t.Run("B6 no flag given means every vehicle", func(t *testing.T) {
		got := teslaIDPointer(0, false)
		if got != nil {
			t.Fatalf("teslaIDPointer(0, false) = %v, want nil", got)
		}
	})

	t.Run("B7 load-bearing: an explicit -tesla-id 0 is honored as a real value", func(t *testing.T) {
		got := teslaIDPointer(0, true)
		if got == nil || *got != 0 {
			t.Fatalf("teslaIDPointer(0, true) = %v, want pointer to 0", got)
		}
	})
}
