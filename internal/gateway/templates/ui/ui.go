// Package ui holds the gateway's owned, typed Templ wrapper components. DaisyUI
// supplies the visual vocabulary (semantic class names + theme tokens); these
// components own the markup and their typed Props, so a page composes a
// compiler-checked component instead of re-authoring class soup — and a wrong field
// fails the build. Presentation only: no domain imports, no business logic, and zero
// client-side JavaScript (which keeps htmx fragment swaps safe to re-render).
//
// The tiny mapping helpers below keep variant→class switches out of the templates.
package ui

// btnClass maps a Button variant to its DaisyUI class. Unknown/empty → primary.
func btnClass(variant string) string {
	switch variant {
	case "secondary":
		return "btn btn-secondary"
	case "accent":
		return "btn btn-accent"
	case "ghost":
		return "btn btn-ghost"
	case "outline":
		return "btn btn-outline"
	case "error":
		return "btn btn-error"
	default:
		return "btn btn-primary"
	}
}

// btnType maps a Button type to a valid HTML button type. Unknown/empty → button.
func btnType(t string) string {
	if t == "submit" {
		return "submit"
	}
	return "button"
}

// inputType maps an Input type to a valid HTML input type. Empty → text.
func inputType(t string) string {
	if t == "" {
		return "text"
	}
	return t
}

// alertClass maps an Alert kind to its DaisyUI class. Unknown/empty → info.
func alertClass(kind string) string {
	switch kind {
	case "success":
		return "alert-success"
	case "warning":
		return "alert-warning"
	case "error":
		return "alert-error"
	default:
		return "alert-info"
	}
}

// badgeClass maps a Badge kind to its DaisyUI class. Unknown/empty → neutral.
func badgeClass(kind string) string {
	switch kind {
	case "primary":
		return "badge-primary"
	case "success":
		return "badge-success"
	case "warning":
		return "badge-warning"
	case "error":
		return "badge-error"
	default:
		return "badge-neutral"
	}
}
