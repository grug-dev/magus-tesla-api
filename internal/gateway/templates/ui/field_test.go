package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

// renderField renders Field(p) with an empty child slot to a string, mirroring
// theme_switcher_test.go's render-to-bytes.Buffer pattern. The child slot is
// irrelevant to every assertion here (Help/Error render around it, not inside
// it), so templ.NopComponent stands in for a real control.
func renderField(t *testing.T, p FieldProps) string {
	t.Helper()
	var buf bytes.Buffer
	ctx := templ.WithChildren(context.Background(), templ.NopComponent)
	if err := Field(p).Render(ctx, &buf); err != nil {
		t.Fatalf("render Field: %v", err)
	}
	return buf.String()
}

// TestField_Help_RendersFieldsetLabel covers design.md Test Contract Group A —
// Field's new Help prop renders a muted fieldset-label line under the control,
// distinct from Error, and empty renders nothing.
func TestField_Help_RendersFieldsetLabel(t *testing.T) {
	t.Run("A1_HelpSetNoError", func(t *testing.T) {
		body := renderField(t, FieldProps{Help: "x"})
		if !strings.Contains(body, `<p class="fieldset-label">x</p>`) {
			t.Errorf("want fieldset-label paragraph with %q, got:\n%s", "x", body)
		}
	})

	t.Run("A2_HelpEmpty", func(t *testing.T) {
		body := renderField(t, FieldProps{Help: ""})
		if strings.Contains(body, "fieldset-label") {
			t.Errorf("want no fieldset-label element when Help is empty, got:\n%s", body)
		}
	})

	t.Run("A3_HelpAndError_BothRender_HelpFirst", func(t *testing.T) {
		body := renderField(t, FieldProps{Help: "x", Error: "y"})
		helpTag := `<p class="fieldset-label">x</p>`
		errTag := `<p class="text-error text-sm">y</p>`
		if !strings.Contains(body, helpTag) {
			t.Errorf("want %q in output, got:\n%s", helpTag, body)
		}
		if !strings.Contains(body, errTag) {
			t.Errorf("want %q in output, got:\n%s", errTag, body)
		}
		if strings.Index(body, helpTag) > strings.Index(body, errTag) {
			t.Errorf("want Help to appear before Error, got:\n%s", body)
		}
	})
}
