package ui

import (
	"context"
	"testing"
)

// TestIsSupportedTheme asserts the closed Themes vocabulary check (Test Contract 2):
// true for all three codes, false for empty/unsupported/case-mismatched values.
func TestIsSupportedTheme(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"apex", true},
		{"graphite", true},
		{"halloween", true},
		{"", false},
		{"cyberpunk", false},
		{"APEX", false}, // case-sensitive, no fuzzy match
	}
	for _, c := range cases {
		if got := IsSupportedTheme(c.in); got != c.want {
			t.Errorf("IsSupportedTheme(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestNormalizeTheme locks in the mapping table Test Contract item 1 describes
// (normalizeTheme, handlers/preferences.go, added by T3): every supported code maps to
// itself, and "", "cyberpunk", "APEX" all map to DefaultTheme. handlers.normalizeTheme
// is a thin wrapper around exactly IsSupportedTheme + DefaultTheme — the two exported
// symbols this package (T1) owns — so this test exercises that same composition at the
// ui layer, ahead of T3 giving the wrapper itself its own dedicated test.
func TestNormalizeTheme(t *testing.T) {
	normalize := func(v string) string {
		if IsSupportedTheme(v) {
			return v
		}
		return DefaultTheme
	}
	cases := []struct {
		in   string
		want string
	}{
		{"apex", "apex"},
		{"graphite", "graphite"},
		{"halloween", "halloween"},
		{"", DefaultTheme},
		{"cyberpunk", DefaultTheme},
		{"APEX", DefaultTheme},
	}
	for _, c := range cases {
		if got := normalize(c.in); got != c.want {
			t.Errorf("normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestThemeFromContext_DefaultsAndPanicSafety mirrors i18n's
// TestFromContext_DefaultsAndPanicSafety exactly (design.md D1: "structurally IDENTICAL
// pair"): missing key, wrong type, and an unsupported value on ctx all default to
// DefaultTheme, and none of them panics.
func TestThemeFromContext_DefaultsAndPanicSafety(t *testing.T) {
	t.Run("no value on ctx defaults to DefaultTheme", func(t *testing.T) {
		if got := ThemeFromContext(context.Background()); got != DefaultTheme {
			t.Fatalf("ThemeFromContext(no value) = %q, want %q", got, DefaultTheme)
		}
	})

	t.Run("WithTheme(ctx, apex) resolves to apex", func(t *testing.T) {
		ctx := WithTheme(context.Background(), "apex")
		if got := ThemeFromContext(ctx); got != "apex" {
			t.Fatalf("ThemeFromContext(WithTheme apex) = %q, want %q", got, "apex")
		}
	})

	t.Run("unsupported value on ctx defaults to DefaultTheme", func(t *testing.T) {
		ctx := WithTheme(context.Background(), "cyberpunk")
		if got := ThemeFromContext(ctx); got != DefaultTheme {
			t.Fatalf("ThemeFromContext(unsupported value) = %q, want %q", got, DefaultTheme)
		}
	})

	t.Run("non-string value under an unrelated key does not panic", func(t *testing.T) {
		type unrelatedCtxKey struct{}
		ctx := context.WithValue(context.Background(), unrelatedCtxKey{}, 42)
		var got string
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("ThemeFromContext panicked: %v", r)
				}
			}()
			got = ThemeFromContext(ctx)
		}()
		if got != DefaultTheme {
			t.Fatalf("ThemeFromContext(unrelated non-string value) = %q, want %q", got, DefaultTheme)
		}
	})

	t.Run("wrong-type value under themeCtxKey does not panic", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), themeCtxKey, 42)
		var got string
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("ThemeFromContext panicked: %v", r)
				}
			}()
			got = ThemeFromContext(ctx)
		}()
		if got != DefaultTheme {
			t.Fatalf("ThemeFromContext(wrong-type value) = %q, want %q", got, DefaultTheme)
		}
	})
}
