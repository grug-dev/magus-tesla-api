package fragments

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
)

// renderNavHeader renders the NavHeader fragment under an explicit language,
// which is how the middleware supplies it at request time (i18n.WithLang on the
// request context). Rendering under context.Background() would silently resolve
// to Spanish — the platform default — so every case here sets the language
// explicitly rather than relying on the fallback.
func renderNavHeader(t *testing.T, lang string, vm NavHeaderVM) string {
	t.Helper()
	ctx := i18n.WithLang(context.Background(), lang)
	var buf bytes.Buffer
	if err := templ.Handler(NavHeader(vm)).Component.Render(ctx, &buf); err != nil {
		t.Fatalf("render NavHeader (%s): %v", lang, err)
	}
	return buf.String()
}

// TestNavHeader_StatusLabelTranslatesPerLanguage is the T6.4 coverage for
// design.md D9: the status word is derived from the Status enum through
// statusLabelKeyFor + i18n.T, NOT from a handler-computed English string field.
// It asserts every status kind renders its catalogue value in both languages and
// that the two languages actually differ — a regression that dropped the ctx
// lookup and hardcoded English would pass an ES-only assertion, so both halves
// matter.
func TestNavHeader_StatusLabelTranslatesPerLanguage(t *testing.T) {
	cases := []struct {
		name   string
		status NavHeaderStatusKind
		key    i18n.Key
	}{
		{"connected", NavStatusConnected, i18n.KeyNavHeaderStatusConnected},
		{"asleep", NavStatusAsleep, i18n.KeyNavHeaderStatusAsleep},
		{"awaiting", NavStatusAwaiting, i18n.KeyNavHeaderStatusAwaiting},
		{"unavailable", NavStatusUnavailable, i18n.KeyNavHeaderStatusUnavailable},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vm := NavHeaderVM{VehicleName: "Magus", Status: tc.status}

			es := renderNavHeader(t, account.LanguageES, vm)
			en := renderNavHeader(t, account.LanguageEN, vm)

			wantES := i18n.T(i18n.WithLang(context.Background(), account.LanguageES), tc.key)
			wantEN := i18n.T(i18n.WithLang(context.Background(), account.LanguageEN), tc.key)

			if !strings.Contains(es, wantES) {
				t.Errorf("ES render missing catalogue value %q for status %q", wantES, tc.status)
			}
			if !strings.Contains(en, wantEN) {
				t.Errorf("EN render missing catalogue value %q for status %q", wantEN, tc.status)
			}
			// The catalogue defines a distinct word per language for all four
			// statuses, so identical renders mean the ctx language was ignored.
			if es == en {
				t.Errorf("ES and EN renders are identical for status %q — the language is not reaching the template", tc.status)
			}
		})
	}
}

// TestNavHeader_ConnectPromptTranslates covers the other user-facing string this
// fragment owns: the "no vehicle connected" prompt shown when NeedsConnect is set.
func TestNavHeader_ConnectPromptTranslates(t *testing.T) {
	vm := NavHeaderVM{NeedsConnect: true}

	es := renderNavHeader(t, account.LanguageES, vm)
	en := renderNavHeader(t, account.LanguageEN, vm)

	if es == en {
		t.Fatal("ES and EN connect-prompt renders are identical — the language is not reaching the template")
	}
}
