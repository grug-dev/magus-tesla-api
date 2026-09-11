# Gateway client-side JavaScript — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.
> All KB links are relative to `kkpa/context/`.

## Glossary

- **Known as:** `client-side JS`, `zero-JS rule`, `app.js`, `RD8`, `sanctioned exception`,
  `confirm modal`, `confirmation dialog`, `browser_tz cookie`, `timezone cookie`,
  `theme instant apply`, `htmx listener`
- **Internal name:** `internal/gateway/static/app.js` — the gateway's only hand-written
  client script. Plus one inline `<script>` in `layouts.BaseAuth`.

## Component map

The gateway declares a **zero-JS** DaisyUI foundation. Every listener that exists is a
named exception with a written reason.

| Layer | File | Role |
|---|---|---|
| script | `internal/gateway/static/app.js` | Every delegated listener. Delegates on `document.body`. No library, no `fetch`. |
| layout | `internal/gateway/templates/layouts/base.templ` | `BaseAuth` holds the inline `browser_tz` script (RD9). `Base` mounts the dialog once (RD10). |
| component | `internal/gateway/templates/ui/confirm_dialog.templ` | `ui.ConfirmDialog` — the native `<dialog>` RD10 drives. |
| component | `internal/gateway/templates/ui/theme_switcher.templ` | The buttons RD15 matches. Each carries the `hx-vals` RD15 reads. |
| handlers | `internal/gateway/handlers/handlers.go` | `renderError` / `renderFragmentError` set the `HX-Error-Fragment` header. |
| fallback | `internal/clock` | `clock.Zone()` — the zone used when the `browser_tz` cookie is missing. |
| docs | `internal/gateway/AGENTS.md` §RD8 | The standing convention that requires this split. Keeps the rules only. |
| KB | `input-port/charging/external-charges.md` | RD12 / RD13 / RD14 detail. Not repeated here. |

## The sanctioned exceptions

| RD | Where | What it does |
|---|---|---|
| **RD9** | inline in `BaseAuth` | Sets the `browser_tz` cookie from the browser's IANA zone. |
| **RD10** | `app.js` | Intercepts `htmx:confirm` and drives `ui.ConfirmDialog`. |
| **RD12** | `app.js` | Rewrites the date half of `started_at` / `ended_at` on the charge form. |
| **RD13** | `app.js` | Toggles `required` on `ended_at` / `end_battery_pct` from the status. |
| **RD14** | `app.js` | Toggles `disabled` on `location_label` from the location kind. |
| **RD15** | `app.js` | Applies a theme to the DOM at once, and reverts if the save fails. |

RD12, RD13 and RD14 belong to `/external-charges`. Their full detail lives in
`input-port/charging/external-charges.md` §"Client-side JS on this page".

## How each exception works

### RD9 — the `browser_tz` cookie

One `<script>` block, inside `templ BaseAuth` only, wrapped in `try/catch`. It reads
`Intl.DateTimeFormat().resolvedOptions().timeZone` and writes:

```js
document.cookie = "browser_tz=" + encodeURIComponent(tz) + ";path=/;max-age=31536000;SameSite=Lax"
```

No library, no `fetch`, no event listener. One synchronous read plus one cookie write, on
every authenticated page load. Without it the dashboard's date helpers —
`parseHistoryRange`, `defaultHistoryHref`, `buildHistoryPresets` — compute the day in UTC.
In a `<noscript>` browser the cookie is simply never set.
_Added by `gateway-browser-tz-cookie` (MAG-7, shipped 2026-08-11)._

### RD10 — the confirmation modal

htmx fires a **cancelable** `htmx:confirm` event before every request carrying
`hx-confirm`. The event exposes the message as `detail.question` and a
`detail.issueRequest(skip)` callback. Its own default is `window.confirm()` — the
browser's unstyleable dialog. The listener calls `preventDefault()`, fills the dialog from
the element's attributes, and calls `issueRequest(true)` on confirm. htmx then resumes the
exact same request.

`ui.ConfirmDialog` is a native `<dialog>` mounted **once** in `layouts.Base`, so
`BaseAuth` inherits it. It is opened with `<dialog>.showModal()`. It pre-renders both
buttons, and the script only toggles `hidden` and sets `textContent` — no DaisyUI `btn-*`
string ever appears in JavaScript.
_Added by `gateway-add-confirm-dialog` (MAG-5, shipped 2026-08-12, PR #24)._

### RD15 — theme instant apply

A `click` + `htmx:afterRequest` listener pair on `document.body`, both matching
`button[hx-post="/ui/theme/switch"]`. The click listener reads the option's own `hx-vals`
JSON — `{"theme":"<t>","csrf_token":"<token>"}` — stashes the current theme in
`document.documentElement.dataset.themePrevious`, then runs
`document.documentElement.dataset.theme = theme`. This happens before the `hx-post`, which
carries `hx-swap="none"`, has resolved. The `htmx:afterRequest` listener clears the stash
on success and restores it on failure.
_Added by `RM42-gateway-add-theme-selector`, tier 2 of `RM42-settings-theme-selector`
(MAG-43, decision D6)._

## How maintenance works

**To add any client-side JS**, first write an RD entry in `internal/gateway/AGENTS.md`.
RD8 requires the rule, the reason, and the option you rejected. A module-wide rule stays
in `AGENTS.md`. Detail for one page or one control comes here or to that page's guide.

**To change the confirm modal's text**, set attributes on the triggering control. The
vocabulary is four attributes: `hx-confirm`, `data-confirm-title`, `data-confirm-label`,
and `data-confirm-variant="danger"`. Do not edit `app.js`.

**To add a confirmation to a new page**, add `hx-confirm` to the control. Nothing else.
The listener hooks htmx's own event, so any page gets the dialog, even a page not yet
written.

**To change the timezone fallback**, edit `internal/clock`. Do not edit the RD9 script.

**To change a theme option**, edit `theme_switcher.templ`. The theme value the server
receives and the value the DOM applies both come from one `hx-vals` literal per option.

## Conventions & gotchas

- **Never mount a second `ui.ConfirmDialog`.** `app.js` resolves it by `id`. A duplicate
  makes the wrong one open.
  _Source: `internal/gateway/static/app.js`; `templates/ui/confirm_dialog.templ`._

- **The dialog stays in the layout, outside every swappable region.** Otherwise an htmx
  swap can replace a dialog while it is open.
  _Source: `internal/gateway/templates/layouts/base.templ`._

- **RD9 lives in `BaseAuth` only, never in `Base`.** `Base` is the anonymous shell. An
  anonymous visitor has no dashboard, so there is no date math to fix.
  _Source: `internal/gateway/templates/layouts/base.templ`._

- **Why RD9 exists at all.** The server has no other way to learn the browser's IANA
  timezone. No HTTP header carries it, unlike `Accept-Language`. Without the cookie the
  dashboard computed "today" in UTC. A user in PST at 10pm saw the previous day.
  _Source: MAG-7, shipped 2026-08-11._

- **RD9's rejected option: move the date math to the client.** That would take date
  computation out of Templ and Go for every date-touching page. It breaks "no business
  logic in templates, no time math in markup". One cookie write hands the server the one
  fact it is missing, and keeps all arithmetic server-side.
  _Source: `ai/htmx-conventions.md`._

- **RD9 degrades to the platform zone.** The script is wrapped in `try/catch`. On any JS
  failure the cookie is never set, and the server falls back to `clock.Zone()` —
  `America/Bogota`. No error reaches the user. No render breaks.
  _Source: `internal/clock`; `browserLocation`'s fallback rule._

- **RD10 could not be done in CSS.** htmx offers the confirm decision only as a
  cancelable event. A CSS-only modal cannot gate a request that is already in flight —
  the request fires before the user answers.
  _Source: `internal/gateway/static/app.js` — the `htmx:confirm` listener._

- **RD10's second rejected option: a per-page modal.** That puts hand-rolled JS on every
  page that needs a confirmation. Hooking htmx's own event means the next confirmation
  costs one attribute, not a component.
  _Source: `internal/gateway/static/app.js`._

- **RD10 does not erode the `ui/` boundary.** The dialog pre-renders both buttons. The
  script only toggles the `hidden` property and sets `textContent`. No DaisyUI class
  string ever appears in JavaScript.
  _Source: `internal/gateway/templates/ui/confirm_dialog.templ`._

- **RD10 uses the native `<dialog>`.** So focus trapping, Esc to close, page inertness and
  top-layer stacking are the browser's job, not ours.
  _Source: `internal/gateway/static/app.js` — `showModal()`._

- **RD10 degrades to the browser's own dialog.** If the dialog is missing, or the browser
  has no `<dialog>` support, the script returns early and htmx falls back to `confirm()`.
  The styling is worse. The guard itself is never lost.
  _Source: `internal/gateway/static/app.js`._

- **RD13 and RD14 duplicate a server-rendered attribute on purpose.** Where the two
  disagree, the JS state wins in the live DOM, and the disagreement is inert.
  _Source: `input-port/charging/external-charges.md`._

- **RD15 writes the theme before the server answers.** The click listener stashes the
  current theme, then writes the new one into `document.documentElement.dataset.theme`.
  This happens synchronously, before the `hx-post` resolves.
  _Source: `internal/gateway/static/app.js` — the `click` listener._

- **RD15 reads the theme from the button's own `hx-vals`, not a second attribute.** So the
  value the server receives and the value the DOM applies come from one literal per
  option, written once.
  _Source: `internal/gateway/templates/ui/theme_switcher.templ`._

- **RD15 does not reload, and that is deliberate.** `HX-Location` is what the language
  switch uses. A theme is pure CSS with nothing to re-render, unlike server-rendered text.
  _Source: `internal/gateway/static/app.js`; `LangSwitch`._

- **RD15 reverts on failure instead of staying applied.** The server stays the source of
  truth for the next full page load. A theme that was never saved would silently flip back
  on the user's next navigation, with no explanation. Reverting at once, in the same
  interaction, explains itself.
  _Source: `internal/gateway/static/app.js` — the `htmx:afterRequest` listener._

- **RD15 degrades to "next navigation".** Without JS the click handler never fires. The
  `hx-post` still goes through. The choice is still saved. The user sees the new theme on
  the next full page load. Only the instant part is lost.
  _Source: `PreferencesMiddleware`; `internal/gateway/templates/layouts/base.templ`._

- **`app.js` has SEVEN listeners, but `AGENTS.md` names only six exceptions.** The
  seventh is the `htmx:beforeSwap` handler that honours the `HX-Error-Fragment` header.
  It has no RD number. It is described under `AGENTS.md` §"Non-2xx error fragments"
  instead. **This is a known contradiction, not a fact to copy.** Do not "fix" the count
  by deleting a listener. Raise it as its own decision: give it an RD number, or write
  down why it does not need one.
  _Source: `internal/gateway/static/app.js`; `internal/gateway/AGENTS.md`._

- **Why the seventh listener matters.** htmx never swaps a 4xx or 5xx response body by
  default. That default would throw away every validation form the handlers render. The
  handlers opt a response in with `HX-Error-Fragment: true`, and this listener honours it.
  It is opt-in per response, never a blanket "swap all 4xx".
  _Source: `internal/gateway/handlers/handlers.go` — `renderError`, `renderFragmentError`._

## Related KB

- `input-port/charging/external-charges.md` — RD12 / RD13 / RD14 in full.
- `entities/account-settings/guide.md` — where the theme RD15 applies is stored.
