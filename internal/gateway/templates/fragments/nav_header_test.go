package fragments

import (
	"bytes"
	"context"
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

// TestNavHeader_ConnectPromptTranslates covers the only translated string this
// fragment still owns: the "no vehicle connected" prompt shown when NeedsConnect
// is set. MAG-44 deleted the status vocabulary this file also used to cover — the
// block now renders stored numbers (battery, range) and an aria-label, not words.
func TestNavHeader_ConnectPromptTranslates(t *testing.T) {
	vm := NavHeaderVM{NeedsConnect: true}

	es := renderNavHeader(t, account.LanguageES, vm)
	en := renderNavHeader(t, account.LanguageEN, vm)

	if es == en {
		t.Fatal("ES and EN connect-prompt renders are identical — the language is not reaching the template")
	}
}
