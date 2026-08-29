package ui

// dotClass maps a Dot variant to its DaisyUI background token. Unknown/empty
// → neutral. Mirrors badgeClass's exact lookup shape (ui.go) — same file
// convention, kept in its own companion file per dot.templ.
func dotClass(variant string) string {
	switch variant {
	case "success":
		return "bg-success"
	case "warning":
		return "bg-warning"
	case "error":
		return "bg-error"
	default:
		return "bg-neutral"
	}
}
