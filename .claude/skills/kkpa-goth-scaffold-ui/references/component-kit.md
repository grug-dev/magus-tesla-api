# The `templates/ui/` component kit

The owned, typed wrapper components `init` seeds into `internal/gateway/templates/ui/`
(package `ui`). DaisyUI supplies the look; **we** own the components and their `Props`, so
pages/agents compose typed components instead of re-authoring class soup, and the compiler
catches a wrong field.

## Why the kit exists: it's an adapter for a churny dependency

The kit is an **anti-corruption layer** around DaisyUI. DaisyUI is an external UI library
and, like most, it ships **breaking changes** across majors (v4→v5 removed `form-control` /
`label-text`, made `input-bordered` a no-op, and restructured some component markup). By
funnelling every DaisyUI **component class** (`btn`, `card`, `input`, `select`, `fieldset`,
`table`, `alert`, `badge`, …) through a `ui.*` wrapper, a version bump becomes a **one-file
edit per component** — not a sweep across every page and fragment.

Split the DaisyUI surface by churn:

- **Component classes** (`btn`, `input`, `fieldset`, `card-title`, …) — **volatile**; they
  get renamed/restructured across majors. **Own these behind a `ui.*` wrapper.** Never inline
  them in a page/fragment. If a repeated element has no wrapper yet, **add one to `ui/`**
  rather than inlining the class.
- **Semantic theme tokens** (`primary`, `bg-base-100`, `text-base-content`, `text-error`,
  `success`) and **Tailwind layout utilities** (`grid`, `flex`, `gap-4`, `sm:col-span-2`) —
  **stable**; use them **inline** anywhere. Wrapping these would be over-abstraction and would
  defeat the look-it-up-don't-invent-it goal.

An adapter **bounds** the blast radius of an upgrade; it does not make one free — a major bump
can still change a wrapper's internal markup, so re-verify the `ui/` internals and smoke-test.

Rules for every component here (also in `daisyui-templ-conventions.md` §6):
- Presentation only — **no** domain imports, **no** business logic.
- Accept primitives / VM-ready strings, plus a `{ children... }` slot where a body makes sense.
- Optional `Class string` field for extra **layout** utilities (append, never for color).
- **Semantic theme tokens only — never hex / raw palette utilities.**
- Zero client-side JS (keeps htmx swaps safe).

Use `templ`'s class-list form to merge an optional override: `class={ "card bg-base-100", p.Class }`.

---

## Kit inventory

### `PageHeader`
Page title + optional action slot; sits at the top of a page body.

```templ
type PageHeaderProps struct {
	Title    string
	Subtitle string
	Class    string
}

templ PageHeader(p PageHeaderProps) {
	<div class={ "flex items-center justify-between mb-4", p.Class }>
		<div>
			<h1 class="text-2xl font-semibold text-base-content">{ p.Title }</h1>
			if p.Subtitle != "" {
				<p class="text-base-content/70">{ p.Subtitle }</p>
			}
		</div>
		<div class="flex gap-2">{ children... }</div>
	</div>
}
```

### `Card`
Surface container. Body slot; optional title.

```templ
type CardProps struct {
	Title string
	Class string
}

templ Card(p CardProps) {
	<div class={ "card bg-base-100 shadow", p.Class }>
		<div class="card-body">
			if p.Title != "" {
				<h2 class="card-title">{ p.Title }</h2>
			}
			{ children... }
		</div>
	</div>
}
```

### `StatTile`
Single metric (DaisyUI `stat`). All values are pre-formatted VM strings (e.g. "72%",
"340.5 km"). Wrap several in `<div class="stats ...">` or a responsive grid.

```templ
type StatTileProps struct {
	Label string
	Value string // pre-formatted (km/kmh/%/°C already applied by the VM)
	Desc  string // optional secondary line
	Class string
}

templ StatTile(p StatTileProps) {
	<div class={ "stat", p.Class }>
		<div class="stat-title">{ p.Label }</div>
		<div class="stat-value">{ p.Value }</div>
		if p.Desc != "" {
			<div class="stat-desc">{ p.Desc }</div>
		}
	</div>
}
```

### `Button`
Semantic button/link. `Variant` maps to DaisyUI modifiers; add `hx-*` via `Attrs`.

```templ
type ButtonProps struct {
	Variant string           // "primary" | "secondary" | "accent" | "ghost" | "outline" | "error" (default: primary)
	Size    string           // "xs" | "sm" | "md" | "lg" | "xl" (default md — no class)
	Outline bool             // adds btn-outline (combine with a color Variant, e.g. error)
	Type    string           // "button" | "submit" (default button)
	Href    string           // if set, render <a> instead of <button>
	Class   string           // extra layout utilities only — never a btn-* class
	Attrs   templ.Attributes // hx-get/hx-target/hx-swap etc.
}

templ Button(p ButtonProps) {
	if p.Href != "" {
		<a href={ templ.SafeURL(p.Href) } class={ btnClass(p.Variant), p.Class } { p.Attrs... }>
			{ children... }
		</a>
	} else {
		<button type={ btnType(p.Type) } class={ btnClass(p.Variant), p.Class } { p.Attrs... }>
			{ children... }
		</button>
	}
}
```

`btnClass`/`btnType`/`btnSize` are tiny helpers in a sibling `ui.go` mapping the variant to
`btn btn-<variant>` (default `btn btn-primary`), the type to a valid value, and the size to
`btn-<size>` (empty → md, no class); `Outline` adds `btn-outline` via `templ.KV`. All DaisyUI
button classes are owned here — call sites never write a `btn-*` class.

### Form controls: `Field` + `Input` / `Select` / `Textarea`

The form adapter. `Field` owns the DaisyUI `fieldset` + `fieldset-legend` label chrome and the
inline error slot; `Input`/`Select`/`Textarea` own the `input`/`select`/`textarea` classes.
Compose them so a form has **zero** raw DaisyUI classes. `Required`/`Disabled` are typed
booleans (native `required?=`/`disabled?=`); the less-common attributes (`min`, `max`, `step`,
`placeholder`) go in `Attrs` as string values. `<option>`s are data-driven, so they stay with
the caller as the `Select` slot.

```templ
type FieldProps struct {
	Label string
	Error string // themed inline message below the control; empty = none
	Class string // extra layout utilities (e.g. "sm:col-span-2")
}

type InputProps struct {
	Type     string           // "text"|"number"|"date"|"datetime-local"|… (default text)
	Name     string
	Value    string
	Required bool
	Class    string
	Attrs    templ.Attributes // min/max/step/placeholder (string values)
}

type SelectProps struct {
	Name     string
	Required bool
	Disabled bool
	Class    string
	Attrs    templ.Attributes
}

type TextareaProps struct {
	Name  string
	Class string
	Attrs templ.Attributes
}
```

Usage — one labeled field:

```templ
@ui.Field(ui.FieldProps{Label: "Energy added (kWh)", Error: errs["energy_added_kwh"]}) {
	@ui.Input(ui.InputProps{Type: "number", Name: "energy_added_kwh", Required: true,
		Attrs: templ.Attributes{"step": "0.01", "min": "0.01"}})
}
@ui.Field(ui.FieldProps{Label: "Location", Error: errs["location_kind"]}) {
	@ui.Select(ui.SelectProps{Name: "location_kind", Required: true}) {
		<option value="HOME">Home</option>
		<option value="WORK">Work</option>
	}
}
```

`inputType` is a tiny `ui.go` helper defaulting an empty type to `text` — same pattern as
`btnType`. See the `charges` create/edit forms for the full gold-standard composition.

### `Alert`
Inline status message (`info`/`success`/`warning`/`error`). Ideal for the graceful-
degradation `Notice`/`Error` VM strings.

```templ
type AlertProps struct {
	Kind  string // "info" | "success" | "warning" | "error" (default info)
	Class string
}

templ Alert(p AlertProps) {
	<div class={ "alert", alertClass(p.Kind), p.Class } role="alert">
		{ children... }
	</div>
}
```

### `Badge`
Small status pill (e.g. charging state, sentry on/off).

```templ
type BadgeProps struct {
	Kind  string // "neutral" | "primary" | "success" | "warning" | "error" (default neutral)
	Text  string
	Class string
}

templ Badge(p BadgeProps) {
	<span class={ "badge", badgeClass(p.Kind), p.Class }>{ p.Text }</span>
}
```

### `Table`
Themed table shell + a header row from string headers; rows are provided as the slot (each a
`<tr>`), so the caller controls row markup while the chrome stays consistent.

```templ
type TableProps struct {
	Headers []string
	Class   string
}

templ Table(p TableProps) {
	<div class="overflow-x-auto">
		<table class={ "table", p.Class }>
			<thead>
				<tr>
					for _, h := range p.Headers {
						<th>{ h }</th>
					}
				</tr>
			</thead>
			<tbody>
				{ children... }
			</tbody>
		</table>
	</div>
}
```

### `NavShell` (+ `NavItem`)
The drawer sidebar menu, so `BaseAuth` and pages don't re-author nav markup. `BaseAuth`
passes the current path so the active item can be highlighted.

```templ
type NavItem struct {
	Label  string
	Href   string
	Active bool
}

templ NavShell(items []NavItem) {
	<ul class="menu bg-base-200 min-h-full w-64 p-4">
		for _, it := range items {
			<li>
				<a href={ templ.SafeURL(it.Href) } class={ templ.KV("menu-active", it.Active) }>
					{ it.Label }
				</a>
			</li>
		}
	</ul>
}
```

---

## Composition example (a read-only panel)

```templ
templ BatteryPanel(d fragments.BatteryPageData) {
	if d.Error != "" {
		@ui.Alert(ui.AlertProps{Kind: "error"}) { { d.Error } }
	} else {
		<div class="grid grid-cols-1 md:grid-cols-3 gap-4">
			@ui.Card(ui.CardProps{Title: "Battery"}) {
				@ui.StatTile(ui.StatTileProps{Label: "Level", Value: d.Level})
				@ui.StatTile(ui.StatTileProps{Label: "Range", Value: d.RangeKm})
			}
		</div>
	}
}
```

Layout (`grid`, `gap`, breakpoints) = Tailwind utilities; component look = DaisyUI via the
kit; data = the logic-free VM. That separation is what keeps output consistent across pages
and across pipeline agents.

> Verify `templ` helpers (`templ.Attributes`, `templ.KV`, `templ.SafeURL`, class-list
> `class={ a, b }`) against Context7 `/a-h/templ` before relying on exact signatures.
