# The `charges` gold-standard vertical-slice recipe

Source of truth for `scaffold <concept> [module]`. Every new UI slice imitates the existing
**charge log** slice. Read these real files first, then mirror their shape:

| Layer | Reference file |
|---|---|
| View model (VM) | `internal/gateway/templates/fragments/charges_vm.go` |
| Page (fragment regions) | `internal/gateway/templates/pages/charges.templ` |
| Swappable fragments | `internal/gateway/templates/fragments/charges_list.templ`, `charge_row*.templ`, `charge_create_form.templ` |
| Handler | `internal/gateway/handlers/charges.go` |
| Shared render helpers | `internal/gateway/handlers/handlers.go` (`render`, `renderFragment`, `currentUID`) |
| Routes | `internal/gateway/gateway.go` (route table in `NewEngine`) |
| Handler tests | `internal/gateway/handlers/charges_test.go`, `handlers_test.go` |

---

## Step 1 — Resolve the domain module + ports (CodeGraph, not grep)

`codegraph_context` on the concept to find its owning module and the **interfaces** the
gateway will call (e.g. `telemetry.Reader`, `manualcharge.Reader`/`Writer`,
`account.Service`). The gateway calls interfaces **in-process only** — never a DB, never a
module's internals, never a vendor `…Tesla` DTO. If the concept has no owning module, stop
and ask the user which module owns it (or whether one must be created first — out of this
skill's scope).

Ports are injected via `handlers.Deps` / `gateway.Deps` (see `handlers.go` `Deps` struct).
If the slice needs a port not yet injected, add it to both `Deps` structs and the `cmd/web`
wiring — that is a structural change, so update docs too.

## Step 2 — View model (`fragments/<concept>_vm.go`)

A pure-Go, **logic-free** presentation struct. Follow `charges_vm.go`:

- Only pre-computed display strings/bools/ints. **No** domain types, **no** `pgtype`, **no**
  `time.Duration`, **no** arithmetic that belongs in the handler.
- Nil-safe: a `*string`/`*int` domain field becomes `""`/`"—"` or a formatted string here.
- **Miles → km**: never show miles. Call the domain's existing value-receiver
  `…Km()`/`…Kmh()` methods and format the result (see `mapVehicles` in `handlers.go`, e.g.
  `fmt.Sprintf("%.1f km", snap.BatteryRangeKm())`).
- Group per-row VM + a `…PageData` struct holding the slice + page-level flags
  (`EmptyState`, `Error`, `CSRFToken`, …), mirroring `ChargeEntryVM` / `ChargesPageData`.

## Step 3 — Templates (`pages/` + `fragments/`)

- `pages/<concept>.templ`: wrap in `@layouts.BaseAuth("<Title> — Magus")`; compose the page
  from `ui/` components (`ui.PageHeader`, `ui.Card`, `ui.StatTile`, `ui.Table`, …) — do not
  hand-write DaisyUI class soup in the page.
- Mark each htmx-swappable region with `@templ.Fragment("<region-id>")` so the same page
  component renders either the full page (initial load) or just that region (htmx swap).
  Give the swap target a stable `id` matching the fragment name.
- `fragments/<concept>_*.templ`: the individually swappable units (list, row, form).
- **No business logic in templates** — only formatting, conditionals, loops over VM data.

Example region (mirror `charges.templ`):

```templ
templ BatteryPage(d fragments.BatteryPageData) {
	@layouts.BaseAuth("Battery — Magus") {
		@ui.PageHeader(ui.PageHeaderProps{Title: "Battery"})
		<div id="battery-panel">
			@templ.Fragment("battery-panel") {
				@fragments.BatteryPanel(d)
			}
		</div>
	}
}
```

## Step 4 — Handler (`handlers/<concept>.go`)

Mirror `charges.go`:

- **Auth-guard** every handler: `uid, ok := currentUID(c); if !ok { redirect /login }`.
- A **gin-free** `build<Concept>Page(ctx, uid, …) fragments.<Concept>PageData` helper that
  calls the module ports and maps domain → VM. Keeping it gin-free makes it unit-testable
  with fake ports (see `buildChargesPage`).
- Full page: `render(c, http.StatusOK, pages.<Concept>Page(d))`.
- htmx swap: `renderFragment(c, http.StatusOK, pages.<Concept>Page(d), "<region-id>")`
  (thin wrapper over `templ.Handler(..., templ.WithFragments(...))`).
- **Graceful degradation**: on a port error, log server-side and return a VM with a
  user-facing `Error`/`Notice` string — never leak a Go error or stack to the page. A
  `tesla`/`account` auth failure surfaces as a reconnect-prompt state, not a 500.
- **Writes (only if the concept has user-initiated writes)**: CSRF-protect with the
  `generateCSRFToken` (issue on GET) + `checkCSRF` (constant-time compare, fail-closed)
  pattern from `charges.go`, and validate tenant ownership before calling a `Writer`. Most
  read-only dashboard panels need none of this.

## Step 5 — Routes (`gateway.go`)

Register in `NewEngine`'s route table:

- `GET /<concept>` → the full page handler.
- `GET /ui/<concept>/…` → fragment refresh handlers (htmx targets).
- `POST|PUT|DELETE /ui/<concept>/…` → write/action handlers (only if the slice writes).

Keep `/ui/*` for htmx HTML fragments; JSON belongs to the separate `/api/{version}` surface,
never here.

## Step 6 — Codegen

`make templ` (regenerate `*_templ.go`) and `make css` if the slice introduced new DaisyUI/
Tailwind classes. Both are Claude-authorized in this repo.

## Step 7 — Tests

Add handler tests mirroring `charges_test.go` / `handlers_test.go`: fake port
implementations, exercise the gin-free `build…Page` helper and the full-page + fragment
render paths, assert graceful-degradation states.

**Exception:** do NOT write tests for `tesla-exploration` (`internal/tesla/raw.go`,
`cmd/explore-tesla-api/`) and never call its `Raw*` methods from a test — it hits the live,
paid Fleet API and wakes the car (see `CLAUDE.md`).

## Step 8 — Docs

If the slice adds a new public surface (a new injected port, a new module interface method),
update the root `README.md` "Project Structure"/"Architecture" and the relevant module
`README.md`/`AGENTS.md` in the same change.

---

## Boundary checklist (every slice)

- [ ] HTML only in `internal/gateway/` — no HTML in any domain module.
- [ ] Clean domain structs in; never a vendor `…Tesla` DTO in a component/VM.
- [ ] No business logic in templates (formatting/conditionals/loops only).
- [ ] Gateway calls module **interfaces**, never a DB or a module's internals.
- [ ] `/ui/*` routes for htmx fragments; JSON stays on `/api/{version}`.
- [ ] Semantic theme tokens only — never hex / raw palette utilities.
- [ ] Any distance/speed field shows km/kmh via the domain's `…Km()`/`…Kmh()` methods.
- [ ] Writes are auth-guarded, CSRF-protected, tenant-ownership-validated.
