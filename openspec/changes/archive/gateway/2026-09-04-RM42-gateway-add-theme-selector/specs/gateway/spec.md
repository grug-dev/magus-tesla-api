## MODIFIED Requirements

### Requirement: Translation Catalogue and Per-Request Language Resolution

The gateway SHALL resolve an active language for every request, exactly once per request, and
SHALL make every user-facing string it renders resolvable through a closed translation catalogue
covering exactly `es` (default) and `en`.

For a signed-in request, the active language SHALL come from `account.Service.PreferencesFor`,
the SAME single call that also resolves the request's active theme (see "Per-Request Theme
Resolution" below) — the gateway SHALL NOT make a separate `account.Service.LanguageFor` call on
any request that also needs the theme, and SHALL NOT make more than one `account.Service`
preference call in total per request. For an anonymous request, the active language SHALL come
from a `lang` cookie, with no database call. In both cases, an absent, unrecognized, or
error-producing source SHALL resolve to `es` — the gateway SHALL NEVER fail or 500 a render
because of a missing or invalid language source.

A catalogue key with no `es`/`en` entry SHALL render a visible marker distinguishing it from a
translated string (never a blank string, never a raw untranslated fallback with no marker), so an
incomplete catalogue is visible in manual QA rather than silently shipping.

#### Scenario: Signed-in request resolves language from the account

- **GIVEN** a signed-in user whose account's stored language is `en`
- **WHEN** any authenticated page or htmx fragment is rendered
- **THEN** the gateway calls `account.Service.PreferencesFor` exactly once for that request
- **AND** every catalogue-driven string on the response renders in English

#### Scenario: Anonymous request resolves language from the cookie

- **GIVEN** an anonymous visitor whose browser carries a `lang=en` cookie
- **WHEN** an anonymous page (e.g. `/login`, `/`) is rendered
- **THEN** the gateway does not call `account.Service.PreferencesFor`
- **AND** every catalogue-driven string on the response renders in English

#### Scenario: Missing or unrecognized language source falls back to Spanish

- **GIVEN** either (a) an anonymous visitor with no `lang` cookie or a cookie value outside
  `{es, en}`, or (b) a signed-in user whose `account.Service.PreferencesFor` call errors
- **WHEN** a page is rendered
- **THEN** the gateway renders every catalogue-driven string in Spanish
- **AND** the render succeeds (no 500, no raw error)

#### Scenario: Language is resolved exactly once per request, in the same call that resolves theme

- **GIVEN** a signed-in user requesting a page whose render composes multiple nested components
- **WHEN** the page is rendered
- **THEN** `account.Service.PreferencesFor` is called exactly one time for that request
- **AND** every nested component renders in the same resolved language
- **AND** the same call's resolved theme is what the response's `data-theme` attribute carries
  (see "Per-Request Theme Resolution")

#### Scenario: A catalogue key with no entry renders a visible marker

- **GIVEN** a translation lookup for a key that has no catalogue entry
- **WHEN** the string is rendered
- **THEN** the output is a visibly marked placeholder distinct from any real translated string
- **AND** no panic or error is raised

### Requirement: Navigation Items

The navigation shell SHALL render its entries with translated labels resolved through the
translation catalogue. Live entries SHALL link to existing pages; a placeholder entry SHALL NOT
navigate to a real page and SHALL be visually marked as "soon" (translated). Each entry SHALL
render an icon. The Settings entry SHALL be a live entry linking to `/settings`, not a
placeholder.

#### Scenario: Live navigation entries link to existing pages, with translated labels

- **GIVEN** a signed-in user viewing the navigation shell
- **WHEN** the navigation list is rendered
- **THEN** a "Dashboard" entry (translated per the resolved language) links to `/dashboard`
- **AND** a "Manual Records" entry (translated) links to `/charges`
- **AND** a "Settings" entry (translated) links to `/settings`
- **AND** all three entries are active-highlighted when the current request path matches their
  target

#### Scenario: Placeholder navigation entries are marked "soon", translated

- **GIVEN** a signed-in user viewing the navigation shell
- **WHEN** the navigation list is rendered
- **THEN** any REMAINING placeholder entry (e.g. Vehicle Stats, Community Benchmark) is rendered
  as a placeholder link visually marked with a translated "Soon"/"Pronto" badge
- **AND** no placeholder navigates to a page not built by this change
- **AND** the Settings entry carries no such badge, since it is no longer a placeholder

#### Scenario: Navigation entries render icons without an external CDN

- **GIVEN** the navigation shell rendered with icons
- **WHEN** the entries are rendered
- **THEN** each entry's icon is an inline SVG owned by the `ui/` kit
  (a `ui.Icon` wrapper)
- **AND** no icon is loaded from an external CDN (no Google Fonts
  Material Symbols stylesheet)
- **AND** no inline client-side JavaScript is required to render icons

## ADDED Requirements

### Requirement: Per-Request Theme Resolution

The gateway SHALL resolve an active UI theme for every request, exactly once per request, using
the SAME `account.Service.PreferencesFor` call that resolves language for a signed-in request
(see the modified "Translation Catalogue and Per-Request Language Resolution" requirement above)
— the gateway SHALL NOT issue a separate database call to resolve theme. For an anonymous
request, the active theme SHALL come from a `theme` cookie, with no database call. In both cases,
an absent, unrecognized, or error-producing source SHALL resolve to the platform default theme
(`graphite`) — the gateway SHALL NEVER fail or 500 a render because of a missing or invalid theme
source. The resolved theme SHALL be available to every component in the render tree via the
request's context, mirroring how the resolved language is made available.

#### Scenario: Signed-in request resolves theme from the account, in the same call as language

- **GIVEN** a signed-in user whose account's stored theme is `apex`
- **WHEN** any authenticated page or htmx fragment is rendered
- **THEN** the response's `data-theme` attribute is `apex`
- **AND** no `account.Service` call beyond the single `PreferencesFor` call was needed to
  obtain it

#### Scenario: Anonymous request resolves theme from the cookie

- **GIVEN** an anonymous visitor whose browser carries a `theme=apex` cookie
- **WHEN** an anonymous page (e.g. `/login`, `/`) is rendered
- **THEN** the gateway does not call `account.Service.PreferencesFor`
- **AND** the response's `data-theme` attribute is `apex`

#### Scenario: Missing or unrecognized theme source falls back to the platform default

- **GIVEN** either (a) an anonymous visitor with no `theme` cookie or a cookie value outside the
  supported vocabulary, or (b) a signed-in user whose `account.Service.PreferencesFor` call
  errors
- **WHEN** a page is rendered
- **THEN** the response's `data-theme` attribute is `graphite`
- **AND** the render succeeds (no 500, no raw error)

#### Scenario: A stale theme cookie is refreshed to match the stored preference

- **GIVEN** a signed-in user whose stored theme is `halloween` and whose incoming `theme` cookie
  says `graphite` (or carries no cookie at all)
- **WHEN** any authenticated page is rendered
- **THEN** the response sets a fresh `theme=halloween` cookie
- **AND** a request whose incoming cookie already matches the stored value triggers no new
  `Set-Cookie` for `theme`

#### Scenario: A theme chosen before logout still renders after logout

- **GIVEN** a user who, while signed in, changed their theme to `apex` (the change succeeded and
  the `theme` cookie was refreshed to `apex`)
- **WHEN** that user signs out and then loads an anonymous page (e.g. `/`, `/login`)
- **THEN** the response's `data-theme` attribute is `apex`
- **AND** no `account.Service` call is made to obtain it — the value comes entirely from the
  `theme` cookie, which is the ONLY reason this cookie exists (there is no anonymous write path
  to this cookie; see "Theme Switch Endpoint")

### Requirement: Theme Presentation Vocabulary and Switcher

The gateway SHALL expose a single, closed, exported list of supported theme codes
(`apex`, `graphite`, `halloween`) that is the sole source both the theme dropdown control and its
own input-validation check consult. The gateway SHALL render a theme selector composed from the
typed `templates/ui/` kit, requiring no client-side JavaScript to open or display its options.
Theme names SHALL render as their proper-noun display form (`Apex`, `Graphite`, `Halloween`)
and SHALL NOT be resolved through the translation catalogue — only the selector's own label and
accessible name are translated.

#### Scenario: The switcher lists every supported theme, in vocabulary order

- **GIVEN** the theme selector is rendered
- **WHEN** its options are inspected
- **THEN** it lists exactly the three supported themes, in the same order as the gateway's
  closed vocabulary
- **AND** each option's visible text is that theme's proper-noun display form, unaffected by
  the resolved language

#### Scenario: The switcher's own chrome is translated; theme names are not

- **GIVEN** a signed-in user with English resolved
- **WHEN** the theme selector is rendered
- **THEN** the selector's label and accessible name render in English
- **AND** every theme option's own name still renders as its proper noun (e.g. `Graphite`, not
  a translated form)

#### Scenario: The selector requires no client-side JavaScript to display or open

- **GIVEN** the rendered theme selector
- **WHEN** its markup is inspected
- **THEN** opening the list of options uses only a CSS-driven DaisyUI pattern
- **AND** no JavaScript is required to display the list of options (selecting an option is
  covered separately by "Instant Client-Side Theme Apply")

### Requirement: Settings Page

The gateway SHALL serve an authenticated `/settings` page rendering the theme selector, seeded
with the request's already-resolved theme. The page SHALL require an authenticated session, and
SHALL NOT issue any preference read beyond the one already performed for the request. The page
SHALL issue a fresh per-session CSRF token for the theme switch endpoint on every load.

#### Scenario: An authenticated user views their current theme on the Settings page

- **GIVEN** a signed-in user whose resolved theme for this request is `apex`
- **WHEN** they load `/settings`
- **THEN** the response renders the theme selector showing `apex` as the current selection
- **AND** rendering the page issues no `account.Service` preference call beyond the one already
  made for the request by the per-request resolution requirement above
- **AND** a fresh CSRF token for the theme switch endpoint is issued for this session

#### Scenario: An anonymous visitor cannot view the Settings page

- **GIVEN** an anonymous visitor
- **WHEN** they request `/settings`
- **THEN** they are redirected to `/login`
- **AND** no page content is rendered
- **AND** no CSRF token is issued

### Requirement: Theme Switch Endpoint

The gateway SHALL expose an endpoint that persists a theme change for the calling user. This
endpoint SHALL require an authenticated session — there is no anonymous path to it, since the
only control capable of submitting to it is rendered on the authenticated Settings page. The
endpoint SHALL require a valid per-session CSRF token issued by the Settings page, matching the
same write-protection pattern the platform already applies to other authenticated writes
(e.g. the Supercharger session-verification endpoint). On a successful persist, the endpoint
SHALL refresh the `theme` cookie to the newly persisted value; on any rejected or failed attempt,
the existing `theme` cookie SHALL be left unchanged. Submitting a value outside the supported
vocabulary SHALL be rejected without persisting anything. The endpoint's response SHALL NOT
instruct the client to reload or re-navigate — a theme change never triggers a page re-render.

#### Scenario: An anonymous caller cannot reach the endpoint

- **GIVEN** an anonymous visitor
- **WHEN** they submit any value to the theme switch endpoint
- **THEN** they are redirected to `/login`
- **AND** no theme preference is persisted
- **AND** the `theme` cookie is not changed

#### Scenario: A signed-in user changes their theme

- **GIVEN** a signed-in user who has loaded the Settings page (and therefore holds a valid CSRF
  token for this endpoint)
- **WHEN** they submit a supported theme value together with that CSRF token
- **THEN** the account's stored theme preference is updated to the submitted value
- **AND** only after that update succeeds is the `theme` cookie refreshed to the submitted value
- **AND** the response carries no reload/redirect instruction

#### Scenario: A request without a valid CSRF token is refused

- **GIVEN** a signed-in user
- **WHEN** they submit a supported theme value with a missing or incorrect CSRF token (including
  the case where no token was ever issued for this session)
- **THEN** the request is rejected
- **AND** no theme preference is persisted
- **AND** the `theme` cookie is not changed

#### Scenario: An unsupported theme value is rejected

- **GIVEN** a signed-in user submitting with a valid CSRF token
- **WHEN** they submit a value outside the supported theme vocabulary
- **THEN** the request is rejected
- **AND** neither the `theme` cookie nor any stored account preference is changed

#### Scenario: A failed persistence attempt leaves the cookie unchanged

- **GIVEN** a signed-in user submitting with a valid CSRF token and a supported theme value,
  whose underlying preference write fails
- **WHEN** the write fails
- **THEN** the request fails with a server error
- **AND** the `theme` cookie is left unchanged — it is never advanced to a value the write never
  actually reached

### Requirement: Instant Client-Side Theme Apply

Selecting a theme option SHALL apply that theme to the page's root element immediately, without
waiting for the persistence request to complete and without reloading or re-rendering the page.
If the background persistence request fails, the applied theme SHALL revert to the value the page
held immediately before the selection.

#### Scenario: Selecting a theme applies it before the network request resolves

- **GIVEN** a page with the theme selector rendered
- **WHEN** a user selects a different theme
- **THEN** the page's root element reflects the newly selected theme immediately
- **AND** this visual change does not wait for the background persistence request to complete
- **AND** the page does not reload or navigate

#### Scenario: A failed persistence attempt reverts the applied theme

- **GIVEN** a page whose theme was just changed via the selector, with the background
  persistence request about to fail
- **WHEN** that persistence request fails
- **THEN** the page's root element reverts to the theme it displayed before the selection
- **AND** no page reload or navigation occurs

#### Scenario: Without JavaScript, the theme still applies on the next page load

- **GIVEN** a browser with JavaScript disabled or the selector's script otherwise not running
- **WHEN** a user selects a theme option
- **THEN** the persistence request still completes normally
- **AND** the newly selected theme is reflected the next time any page is fully loaded (not
  instantly, since the instant-apply behavior itself requires JavaScript)
