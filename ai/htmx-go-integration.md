# htmx ↔ Go Integration — magus-tesla-api

**Design/integration reference** (not code style): the contract between htmx in the browser
and the Go gateway that serves it. Read alongside [`architecture.md`](./architecture.md)
(boundaries), [`htmx-conventions.md`](./htmx-conventions.md) (how to write components), and
[`go-conventions.md`](./go-conventions.md).

> **Status:** contract is decided; the web layer is **not built yet**.

---

## The gateway's job (recap)

`internal/gateway/` is a **translator**: it receives an htmx request on `/ui/...`, calls the
relevant domain module **interface** (in-process — no HTTP, no JSON), pipes the returned
domain struct into a Templ component, and streams the resulting **HTML fragment** back to
the browser. It holds no domain state and never touches a database.

---

## Request flow (multi-tenant)

```
browser (htmx hx-get /ui/battery)
   └─> gateway handler
         ├─ account.TokensFor(ctx, userID)      -> Credentials      (identity module)
         ├─ tesla.GetVehicleData(ctx, creds)     -> ...Tesla DTO      (adapter)
         ├─ battery.StateFrom(...)               -> battery.State     (domain, clean model)
         └─ render Templ fragment(battery.State) -> HTML fragment  --> back to browser
```

Every arrow between modules is a **Go interface call**. The gateway orchestrates; it does
not reach into any module's internals.

---

## Full page vs. fragment (Templ fragments)

The same page component serves both the initial full-page load and the htmx partial update
— using Templ's fragment API, so there is no duplicate markup:

Define a swappable region in the `.templ` page:

```templ
templ BatteryPage(s battery.State) {
    @layouts.Base() {
        @templ.Fragment("battery") {
            @fragments.BatteryCard(s)
        }
    }
}
```

Render the whole page or just the fragment from the handler:

```go
// Initial load: render the entire page.
func (h *Handler) BatteryPage(w http.ResponseWriter, r *http.Request) {
    state := h.buildBatteryState(r)          // via account -> tesla -> battery interfaces
    templ.Handler(BatteryPage(state)).ServeHTTP(w, r)
}

// htmx swap: render ONLY the "battery" fragment.
func (h *Handler) BatteryFragment(w http.ResponseWriter, r *http.Request) {
    state := h.buildBatteryState(r)
    templ.Handler(BatteryPage(state), templ.WithFragments("battery")).ServeHTTP(w, r)
}
```

`templ.WithFragments("battery")` runs the whole template but returns only that fragment's
HTML — ideal for `hx-target`/`hx-swap`. Source of truth for this API: Context7 `/a-h/templ`
(verify before relying on signatures).

---

## Response conventions

- **Content type** is `text/html` (Templ's `templ.Handler` sets it). Fragments are HTML,
  never JSON — JSON belongs to the separate `/api/{version}` surface.
- **Status codes:** use `templ.WithStatus(...)` for non-200 responses.
- **Errors:** render an error *fragment* the target can swap in, or use
  `templ.WithErrorHandler(...)`; never leak a Go error string or a stack trace to the page.
- **Auth failures across the boundary:** a `vehicle`/`tesla` 401 (the `ErrUnauthorized`
  pattern in [`go-conventions.md`](./go-conventions.md)) surfaces as an htmx-friendly
  fragment prompting re-connect — not as a raw 500.

---

## Where this lives (boundary reminder)

All of the above is **gateway-only** code. Templ imports, `hx-*` knowledge, and fragment
logic never appear in a domain module — domain modules expose interfaces returning clean
structs, and know nothing about htmx or HTML.
