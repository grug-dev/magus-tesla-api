# Gateway Reader and Writer ports — the read-only default and its six exceptions

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.
> All KB links are relative to `kkpa/context/`.

## Glossary

- **Known as:** `Reader port`, `Writer port`, `read-only rule`, `read-only at request time`,
  `write exception`, `write aperture`, `read-only exception`, `D4 amendment`,
  `D8 amendment`, `CSRF`, `csrf_token`, `csrf_externalcharge`, `csrf_supercharger`,
  `csrf_theme`, `csrf_vehicle_select`, `checkCSRF`, `checkCSRFKey`, `tenant ownership check`,
  `SameSite`, `lang cookie`, `theme cookie`
- **Internal name:** the named handlers below. There is no shared "write" abstraction — each
  aperture is its own handler with its own guard set.

The rule these break lives in `internal/gateway/AGENTS.md` §"Read-only at request time":
a gateway handler calls **Reader** ports only. This guide holds the mechanism of every
handler that is allowed to do more, and the divergences between them that look like bugs
and are not.

## Component map

| Aperture | Handler / file | Route | Port called | Guards, in order |
|---|---|---|---|---|
| manual charge write (D4) | `ExternalChargeCreate`, `ExternalChargeRowUpdate`, `ExternalChargeRowDelete` — `internal/gateway/handlers/external_charges.go` | `POST` / `PUT` / `DELETE` under `/external-charges` | `charging.Writer` — `Create` / `Update` / `Delete` | auth → `RegisteredVehicles` ownership → `checkCSRF` (`csrf_externalcharge`) |
| session battery verify (D8) | `SuperchargerRowUpdate` — `internal/gateway/handlers/supercharger.go` | `PATCH /ui/supercharger-stats/row/:id` | `charging.SessionVerifier.VerifySession` | auth → `checkCSRFKey(csrf_supercharger)`. **No ownership check** |
| theme switch (D3/D8) | `handlers.ThemeSwitch` — `internal/gateway/handlers/preferences.go` | `POST /ui/theme/switch` | `account.Service.SetTheme` | auth → `checkCSRFKey(csrf_theme)`. No ownership check |
| language switch (D-lang) | `handlers.LangSwitch` — `internal/gateway/handlers/lang.go` | `POST /ui/lang/switch` | `account.Service.SetLanguage` | **none** — no auth, no ownership, no CSRF |
| login language sync | `syncLoginLanguageCookie` — `internal/gateway/handlers/lang.go` | none (runs inside `GoogleCallback`) | `account.Service.SetLanguage` | none — best-effort, inside the login flow |
| vehicle select | `VehicleSelect` — `internal/gateway/handlers/handlers.go` | `POST /ui/vehicle/select` | **no module port** — session only | auth → `checkCSRFKey(csrf_vehicle_select)` → `RegisteredVehicles` ownership |
| inactive-account refusal | `rejectIfInactive` — `internal/gateway/handlers/handlers.go` | none (runs inside `GoogleCallback`) | reads `account.Account.Status` | n/a — it is a refusal, not a write |

`VehicleSelect` writes the session, not a module. It is listed because it owns the fourth
CSRF key and because its ownership check is the D4 pattern reused — an agent counting
apertures needs to meet it.

## The CSRF helper

`checkCSRFKey(c, key)` (`internal/gateway/handlers/external_charges.go`) is the generic per-form check.
`checkCSRF(c)` is the one-line alias pinned to `csrfExternalChargeKey`.

- Reads `csrf_token` from the form body, falling back to the **`X-CSRF-Token` header**.
  The header path is what lets the delete handler check CSRF without parsing a body.
- Compares with `subtle.ConstantTimeCompare`.
- **Fail-closed:** an empty session token never matches. Comparing `""` against `""` would
  return equal, so a write attempted before the issuing `GET` was ever loaded would pass.
  Both sides must be a real token. Do not "simplify" that empty check away.
- Writes HTTP 403 itself on failure; the caller just returns.

Four session keys, all distinct, each minted by the `GET` that hosts its form and only read
afterwards. Grep the constant, not the literal:

| Constant | Value | Defined in |
|---|---|---|
| `csrfExternalChargeKey` | `"csrf_externalcharge"` | `internal/gateway/handlers/external_charges.go` |
| `csrfVehicleSelectKey` | `"csrf_vehicle_select"` | `internal/gateway/handlers/external_charges.go` |
| `csrfSuperchargerKey` | `"csrf_supercharger"` | `internal/gateway/handlers/supercharger.go` |
| `csrfThemeKey` | `"csrf_theme"` | `internal/gateway/handlers/preferences.go` |
A key is minted by the page handler that renders the form — `SettingsPage` mints
`csrf_theme`, `SuperchargerStatsPage` mints `csrf_supercharger`. Keep the mint and the check
in the same file.

## Aperture 1 — manual charge writes (D4, RM3-gateway-add-manual-charge-ui)

The gateway MAY call `charging.Writer` (`Create` / `Update` / `Delete`) from the three
`/external-charges` form handlers and **only** those. Every such write needs, in order:

1. the `currentUID(c)` auth guard — else redirect to `/login`;
2. a `RegisteredVehicles` tenant-ownership check on the submitted `(tesla_id, vin)` pair —
   403 if the vehicle is not the caller's;
3. `checkCSRF(c)` against `csrf_externalcharge` — 403 on mismatch.

**Why the gateway carries the ownership check at all.** No cross-module FK exists in the
database, so tenant scoping for this write is enforced at the application layer. Keep it in
the handler; do not push it into `charging`.

Per-route detail — which handler does what, the recalculation window, the response shapes —
lives in `workflows/manual-charge-crud.md` and `input-port/charging/external-charges.md`.

## Aperture 2 — Supercharger session battery verification (D8, RM31)

`charging.SessionVerifier.VerifySession` from `SuperchargerRowUpdate`. Nothing else passes
through this aperture. `SessionVerifier` is a **different port** from D4's `charging.Writer`:
this amendment names its own aperture rather than stretching D4's language, and it changes
nothing about D4.

Auth guard first, then `checkCSRFKey(c, csrfSuperchargerKey)`.

**The one divergence you must not "fix": there is deliberately NO `RegisteredVehicles`
ownership check here.** `VerifySession`'s own `WHERE id = @id AND account_id = @account_id`
is the sole tenant boundary. The port has no vehicle predicate at all, so a `TeslaID` check
in the gateway would test a predicate the write itself never applies. A session on another
vehicle of the **same** account stays writable through this route by design; another
account's row is unreachable regardless of the id supplied.

Full flow, DB effects and the recalculation window: `use-case/charging/verify-session-battery.md`.

## Aperture 3 — theme switch (D3/D8, RM42-gateway-add-theme-selector)

`account.Service.SetTheme` from `ThemeSwitch`, subject to auth + CSRF — mirroring the
Supercharger/D8 aperture, **NOT** the language switch. This is a settled, user-confirmed
decision: an earlier draft proposed reusing the language switch's no-CSRF shape, and the
user explicitly declined it.

**Why the analogy to language breaks on exactly one point.** The language exception's whole
cost argument rests on `LangSwitcher` mounting on EVERY page, including anonymous ones
(`Base`) — a session CSRF token cannot even exist for an anonymous visitor, so requiring one
there would have meant a much larger redesign. `ThemeSwitcher` mounts on exactly ONE page,
`/settings`, which is already authenticated. There never was an anonymous write path here,
so the "nowhere to mint a token" problem that earned language its exception does not exist.
Every other axis of language's reasoning (own-account-only mutation, reversible, low stakes)
is still true of theme. CSRF is the single axis that does not transfer. Do not "simplify"
this endpoint by copying `lang.go`'s shape.

Mechanism:

1. **Auth guard first** — `currentUID(c)` or redirect to `/login`. No CSRF check and no
   write happen without it.
2. **CSRF** — `checkCSRFKey(c, csrfThemeKey)`. Minted once per `GET /settings` by
   `SettingsPage`, in the same file; checked, never re-issued, by `ThemeSwitch`. 403 on a
   missing, stale or mismatched token, and no write proceeds.
3. **No separate tenant-ownership check** — same divergence the Supercharger aperture
   documents: `SetTheme(ctx, uid, theme)` targets the caller's OWN session `uid`, so there
   is no submitted resource identifier for a forged request to redirect at another account.
4. **Only `SetTheme`** is permitted. This does not open general write access.

**Cookie ordering — the `theme` cookie is set only AFTER a successful `SetTheme`, never
unconditionally first like `lang`'s.** `theme` has no anonymous caller at all: its cookie is
a mirror of what the `account.settings` row already holds, kept only so a logged-out or
pre-login page (no session, hence no `PreferencesFor` call) still renders the account's
last-known theme. Setting it first would let the cookie claim a value the database write
never reached. A future agent must not "fix" this ordering to match `lang.go`.

Palettes, fonts and the CSS file layout: `architecture/gateway-theming.md`.
The stored value: `entities/account-settings/guide.md`.

## Aperture 4 — language switch (D-lang, RM24-gateway-add-i18n-foundation)

`account.Service.SetLanguage` from `LangSwitch`. This one does not transplant to any other
handler, because it must work for **anonymous callers too**.

1. **No auth guard, no redirect-to-login.** It always sets the `lang` cookie; it calls
   `SetLanguage` only when a session `uid` is present. The cookie is the anonymous path's
   entire job, so it is written first, unconditionally.
2. **No tenant-ownership check.** `SetLanguage(ctx, uid, lang)` always targets the caller's
   own session `uid` — there is no user-submitted resource identifier (unlike the vehicle
   `(TeslaID, VIN)` pair D4 validates) for a forged request to point at another account.
3. **No CSRF check — a deliberate divergence from D4, not an oversight.** A forged switch
   can only change the caller's own display language: no data mutation, nothing to
   exfiltrate, reversible in one click. Requiring CSRF here would mean minting a session
   CSRF token on every page in the module — including `Home`, `Dashboard` and
   `SuperchargerStats`, which mint none today — for a control mounted on every page, to
   protect against a cosmetic annoyance.
4. **Scope stays narrow.** Only `SetLanguage` is permitted under this exception.

**The actual defence is the `lang` cookie's `SameSite=Lax` attribute**, which makes modern
browsers refuse to attach it to a cross-site `POST`. This is MANDATORY, not incidental:
dropping it voids the decision and re-opens the CSRF question.

- `setLangCookie` (`internal/gateway/handlers/lang.go`) MUST call `c.SetSameSite(http.SameSiteLaxMode)`
  **before** `c.SetCookie(...)`. gin's `SetCookie` has no SameSite parameter, so the
  separate call is the only way the attribute is ever set. Reordering it silently produces
  a cookie with no SameSite attribute.
- `TestLangSwitch_CookieIsSameSiteLax` (`internal/gateway/handlers/lang_test.go`) asserts this specifically.
  It is mandatory and may not be dropped or weakened. **If it fails, fix the cookie, never
  the test.**
- The cookie is `HttpOnly` — no JS reads or writes it, unlike `browser_tz`.

**This trade-off was put to the user and explicitly approved on 2026-08-13, conditional on
`SameSite=Lax`.** It is not a worker's unilateral call. Do not add a CSRF check here without
re-reading this rationale and getting the user's sign-off.

### The second `SetLanguage` call site

`syncLoginLanguageCookie` (`internal/gateway/handlers/lang.go`) also calls `SetLanguage`, from inside
`GoogleCallback`. It carries the pre-login `lang` cookie into the new account's settings. A
**present** cookie triggers the write, not merely a resolved language that happens to be
`es`. It is best-effort: a failure logs and continues, and never fails the login.

It is split out as its own function so it can be unit-tested without a full HTTP round trip
through `GoogleCallback` — `h.google` is a concrete `*googleauth.Client`, not an interface,
so there is no seam to fake the OAuth exchange from inside this module.

## The inactive-account refusal (RM34-gateway-block-inactive-login)

Not a write, but it lives in the same request-guard family and its ordering is the same kind
of trap.

`GoogleCallback` refuses a session to any account whose `account.Account.Status` is not
`account.StatusActive`. `rejectIfInactive(c, acct)` runs **after** `h.acct.UpsertFromOAuth`
resolves the account and **strictly before** `h.syncLoginLanguageCookie` or
`sess.Set("uid", ...)`, rendering `pages.AccountBlocked()` at HTTP 403 via `renderError`.

**That ordering IS the security property** — a check placed after a session write leaves a
usable session behind on the refusal path. The check is fail-closed: anything that is not
exactly `Active` is refused, never "reject when Inactive".

**One render site, no route.** There is no `GET /account-blocked` and no status-gate
middleware — an earlier `AccountActiveGate` design was withdrawn before implementation.
Do not add either. This shape does not generalize to revoking an **already-established**
session mid-flight; that is a different problem and needs its own design.

Everything else — why `rejectIfInactive` is a free function, why the page always renders in
the visitor's pre-login language, the hardcoded contact address, and the account module's
own half of the gate — lives in `architecture/account-activation-gate.md`.

## Conventions & gotchas

- **Adding an aperture is not a worker's call.** Every row in the table above was put to the
  user. Stop and ask before adding one, and record the guard set you chose and why.
- **Do not copy one aperture's guard set onto another.** Three of them differ on purpose and
  each divergence is documented above with the reason it does not transfer. Every "why is
  this one different?" has an answer here; if you cannot find it, you found a real gap.
- **Each aperture names one port.** "The gateway may write" is never the rule; "the gateway
  may call `X` from `Y`" is.
- **Ownership checks belong in the handler**, not pushed down into the module — there is no
  cross-module FK to lean on.

## Known contradiction

`internal/gateway/AGENTS.md` historically described the write exceptions as a list of four
named amendments. The code has **six** call sites that write something plus one refusal, as
the component map shows. `VehicleSelect` (session-only write, fourth CSRF key) and
`syncLoginLanguageCookie` (second `SetLanguage` call site) were never written into that
list. Neither is a defect — both are guarded — but an agent trusting the old count would
miss them. Recorded here rather than silently renumbering a user-approved decision list.

## Related KB

- `workflows/manual-charge-crud.md` — the D4 write flow end to end
- `input-port/charging/external-charges.md` — the `/external-charges` page detail
- `use-case/charging/verify-session-battery.md` — the D8 write flow
- `architecture/account-activation-gate.md` — the cross-module Active/Inactive rule
- `architecture/gateway-theming.md` — palettes, fonts, the CSS file layout
- `entities/account-settings/guide.md` — where `language` and `theme` are stored
- `architecture/gateway-client-side-js.md` — the zero-JS rule and its exceptions
