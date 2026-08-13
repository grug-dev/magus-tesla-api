package i18n

import (
	"context"
	"strings"
	"testing"

	"github.com/cristianpena/magus-tesla-api/internal/account"
)

// unrelatedCtxKey is a different context key type from ctxKey, used to prove
// FromContext ignores a non-string value stashed under some other package's
// key without panicking.
type unrelatedCtxKey struct{}

func TestFromContext_DefaultsAndPanicSafety(t *testing.T) {
	t.Run("no value on ctx defaults to es", func(t *testing.T) {
		if got := FromContext(context.Background()); got != account.LanguageES {
			t.Fatalf("FromContext(no value) = %q, want %q", got, account.LanguageES)
		}
	})

	t.Run("WithLang(ctx, en) resolves to en", func(t *testing.T) {
		ctx := WithLang(context.Background(), account.LanguageEN)
		if got := FromContext(ctx); got != account.LanguageEN {
			t.Fatalf("FromContext(WithLang en) = %q, want %q", got, account.LanguageEN)
		}
	})

	t.Run("non-string value under an unrelated key does not panic", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), unrelatedCtxKey{}, 42)
		var got string
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("FromContext panicked: %v", r)
				}
			}()
			got = FromContext(ctx)
		}()
		if got != account.LanguageES {
			t.Fatalf("FromContext(unrelated non-string value) = %q, want %q", got, account.LanguageES)
		}
	})
}

func TestTranslate_KnownKey(t *testing.T) {
	if got := translate(account.LanguageES, KeyNavDashboard); got != "Panel" {
		t.Errorf("translate(es, KeyNavDashboard) = %q, want %q", got, "Panel")
	}
	if got := translate(account.LanguageEN, KeyNavDashboard); got != "Dashboard" {
		t.Errorf("translate(en, KeyNavDashboard) = %q, want %q", got, "Dashboard")
	}
}

func TestTranslate_UnknownKeyReturnsVisibleMarker(t *testing.T) {
	got := translate(account.LanguageES, Key("not.a.real.key"))
	if !strings.HasPrefix(got, missingKeyMarker) {
		t.Fatalf("translate(unknown key) = %q, want prefix %q", got, missingKeyMarker)
	}
}

// TestT_UsesContextLanguage verifies the public T() surface composes
// FromContext + translate exactly as callers (templ files, plain Go helpers)
// will use it.
func TestT_UsesContextLanguage(t *testing.T) {
	esCtx := context.Background()
	if got := T(esCtx, KeyNavLogout); got != "Cerrar sesión" {
		t.Errorf("T(no lang, KeyNavLogout) = %q, want %q (es default)", got, "Cerrar sesión")
	}

	enCtx := WithLang(context.Background(), account.LanguageEN)
	if got := T(enCtx, KeyNavLogout); got != "Log out" {
		t.Errorf("T(en, KeyNavLogout) = %q, want %q", got, "Log out")
	}
}
