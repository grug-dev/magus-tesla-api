package account

import (
	"testing"
	"time"
)

func TestNeedsRefresh(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	const margin = 60 * time.Second

	cases := []struct {
		name      string
		expiresAt time.Time
		want      bool
	}{
		{"valid far in the future", now.Add(2 * time.Hour), false},
		{"just outside the margin", now.Add(2 * time.Minute), false},
		{"within the safety margin", now.Add(30 * time.Second), true},
		{"exactly at expiry", now, true},
		{"already expired", now.Add(-time.Hour), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsRefresh(tc.expiresAt, now, margin); got != tc.want {
				t.Errorf("needsRefresh(expiresAt=%v, now=%v, margin=%v) = %v, want %v",
					tc.expiresAt, now, margin, got, tc.want)
			}
		})
	}
}

func TestAccessExpiry(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	// Tesla access tokens last 8 hours (28800s).
	got := accessExpiry(now, 28800)
	want := now.Add(8 * time.Hour)
	if !got.Equal(want) {
		t.Errorf("accessExpiry(%v, 28800) = %v, want %v", now, got, want)
	}
}

func TestTextFromString(t *testing.T) {
	if txt := textFromString(""); txt.Valid {
		t.Errorf(`textFromString("") should be NULL (Valid=false), got %+v`, txt)
	}
	if txt := textFromString("hi"); !txt.Valid || txt.String != "hi" {
		t.Errorf(`textFromString("hi") = %+v, want {String:"hi" Valid:true}`, txt)
	}
}

// TestNormalizeLanguage covers the DB->domain normalization boundary (design.md
// D3/D4): both supported codes pass through unchanged, and any unrecognized or
// empty stored value normalizes to LanguageES.
func TestNormalizeLanguage(t *testing.T) {
	cases := []struct {
		name string
		lang string
		want string
	}{
		{"supported es unchanged", LanguageES, LanguageES},
		{"supported en unchanged", LanguageEN, LanguageEN},
		{"unrecognized value normalizes to es", "fr", LanguageES},
		{"empty string normalizes to es", "", LanguageES},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeLanguage(tc.lang); got != tc.want {
				t.Errorf("normalizeLanguage(%q) = %q, want %q", tc.lang, got, tc.want)
			}
		})
	}
}

// TestNormalizeTheme covers the DB->domain normalization boundary (design.md
// D4/D6): all three supported codes pass through unchanged, and any
// unrecognized or empty stored value normalizes to ThemeGraphite. Mirrors
// TestNormalizeLanguage exactly.
func TestNormalizeTheme(t *testing.T) {
	cases := []struct {
		name  string
		theme string
		want  string
	}{
		{"supported apex unchanged", ThemeApex, ThemeApex},
		{"supported graphite unchanged", ThemeGraphite, ThemeGraphite},
		{"supported halloween unchanged", ThemeHalloween, ThemeHalloween},
		{"unrecognized value normalizes to graphite", "cyberpunk", ThemeGraphite},
		{"empty string normalizes to graphite", "", ThemeGraphite},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeTheme(tc.theme); got != tc.want {
				t.Errorf("normalizeTheme(%q) = %q, want %q", tc.theme, got, tc.want)
			}
		})
	}
}
