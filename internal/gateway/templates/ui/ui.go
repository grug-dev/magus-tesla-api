// Package ui holds the gateway's owned, typed Templ wrapper components. DaisyUI
// supplies the visual vocabulary (semantic class names + theme tokens); these
// components own the markup and their typed Props, so a page composes a
// compiler-checked component instead of re-authoring class soup — and a wrong field
// fails the build. Presentation only: no domain imports, no business logic, and zero
// client-side JavaScript (which keeps htmx fragment swaps safe to re-render).
//
// The tiny mapping helpers below keep variant→class switches out of the templates.
package ui

// sectionTitleClass and sectionDescClass are the SINGLE definition of how a
// section's title and its short description look — anywhere in the app, on every
// page, in a Card or in a bare SectionHeader. Every surface that renders a section
// heading reads these two constants: ui.Card (its Title/Desc), ui.SectionHeader
// (standalone sections) and ui.PageHeader (its Subtitle uses the desc class, so a
// page subtitle and a section description are visually the same thing).
//
// Why constants and not three hand-copied class strings: before MAG-46 there were
// THREE different heading treatments in the module — `card-title` inside ui.Card,
// `text-2xl font-semibold` in ui.PageHeader, and a hand-written
// `text-lg font-semibold` heading inlined in fragments/charges_list.templ that bypassed
// the kit entirely. Nothing kept them in step, so "the section title style" was not
// a thing that existed in one place. Now it is: change these two lines and every
// title and description in the app moves together. Same principle as navActiveClass
// (nav_shell.templ) and BatteryBandClass — name it once, look it up, never re-derive.
//
// Note the title deliberately does NOT use DaisyUI's `card-title`: that class is
// card-scoped, so a standalone `section` heading could never match it, which is the
// exact drift these constants exist to prevent.
const (
	sectionTitleClass = "text-lg font-semibold text-base-content"
	sectionDescClass  = "text-sm text-base-content/70"
)

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

// btnSize maps a Button size to its DaisyUI class. Empty → md, DaisyUI's default
// (no class emitted). Owning the size here keeps `btn-sm`/`btn-lg`/… out of call
// sites, so a DaisyUI size-class rename is a one-place edit.
func btnSize(size string) string {
	switch size {
	case "xs":
		return "btn-xs"
	case "sm":
		return "btn-sm"
	case "md":
		return "btn-md"
	case "lg":
		return "btn-lg"
	case "xl":
		return "btn-xl"
	default:
		return ""
	}
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
// "ghost" maps to DaisyUI's badge-ghost (used by the nav-header status dot for the
// awaiting/unavailable states — a neutral, low-emphasis pill). Add a kind here
// rather than inlining a raw badge-* class in a page/fragment (the ui/ kit is the
// anti-corruption adapter around DaisyUI component classes).
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
	case "ghost":
		return "badge-ghost"
	default:
		return "badge-neutral"
	}
}

// BatteryBandClass returns the theme token that colours a battery level, driving
// currentColor for BOTH a big "%" number and a Progress fill (DaisyUI's progress
// value reads currentColor). Bands are end-inclusive at the lower bound:
//
//	0–10  → text-battery-low  (red)
//	11–20 → text-battery-mid  (orange)
//	21–40 → text-battery-warn (yellow)
//	41+   → text-battery-full (green)
//
// hasValue false (no stored reading) returns "text-primary", so a placeholder
// keeps the brand look instead of going colourless.
//
// This is the SINGLE definition of the band switch. It lives in the kit because
// two surfaces now render it — the dashboard battery card (via
// pages.dashBatteryColorClass, a thin adapter over this) and the sidebar vehicle
// block. See internal/gateway/static/themes/_shared.css §"Battery-level metric
// colors" for the tokens themselves.
func BatteryBandClass(pct int, hasValue bool) string {
	if !hasValue {
		return "text-primary"
	}
	switch {
	case pct <= 10:
		return "text-battery-low"
	case pct <= 20:
		return "text-battery-mid"
	case pct <= 40:
		return "text-battery-warn"
	default:
		return "text-battery-full"
	}
}
