// Package i18n is the gateway's closed translation vocabulary and per-request
// language carrier (RM24-gateway-add-i18n-foundation, design.md D1/D2/D5). It
// is a plain Go package — no templ import, no other domain module — so it can
// be used from any .templ file (via the implicit ctx) or any plain Go helper
// (via an explicit ctx argument). Boundary check: T8.5.
package i18n

import (
	"context"

	"github.com/cristianpena/magus-tesla-api/internal/account"
)

// ctxKey is an unexported type so this package's context key can never
// collide with a key set by another package importing the same
// context.Context (standard Go context-key idiom).
type ctxKey struct{}

var langCtxKey ctxKey

// WithLang returns a copy of ctx carrying lang as the active render language.
// It performs NO validation — callers normalize before calling (design.md D3:
// languageMiddleware resolves to exactly account.LanguageES/LanguageEN before
// ever calling this).
func WithLang(ctx context.Context, lang string) context.Context {
	return context.WithValue(ctx, langCtxKey, lang)
}

// FromContext returns the active language carried on ctx, defaulting to
// account.LanguageES on anything but an exact account.LanguageEN match: a
// missing key (no languageMiddleware ran — e.g. a bare context.Background()
// in a unit test), a value of the wrong type, or an explicit "es" all resolve
// the same way. The two-result (comma-ok) type assertion is required, not
// optional defensiveness: templ's own docs state that "accessing a
// non-existent key or performing an invalid type assertion on the context
// value will trigger a runtime panic" (design.md D5) — a bare `.(string)`
// assertion here would panic exactly where this function exists to prevent
// that.
func FromContext(ctx context.Context) string {
	lang, _ := ctx.Value(langCtxKey).(string)
	if lang == account.LanguageEN {
		return account.LanguageEN
	}
	return account.LanguageES
}

// Key identifies one catalog entry (see catalog.go for the vocabulary and the
// map literal it resolves against).
type Key string

// T resolves key in the language carried on ctx. This is the ONLY lookup
// surface any .templ file or Go helper should use — see
// internal/gateway/AGENTS.md "i18n" section for the binding "every new
// user-facing label needs BOTH es and en" rule this function's callers must
// follow.
func T(ctx context.Context, key Key) string {
	return translate(FromContext(ctx), key)
}
