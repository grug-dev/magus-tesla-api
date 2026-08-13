Source: MAG-8 — https://linear.app/magus-monitor/issue/MAG-8/i18n-translations-htmx
Roadmap: openspec/roadmaps/RM24-i18n-translations.md
Tier: 3 of 3 (gateway; depends on tier 2 `RM24-gateway-add-i18n-foundation`, archived — its
catalogue, per-request language resolution, navbar selector, and gold-standard nav shell are
consumed as-is and not reopened)

## Why

Tier 2 proved the whole i18n mechanism — catalogue format, per-request language resolution,
context-carried `i18n.T(ctx, key)` lookup, the navbar switcher — on exactly one surface (the
layout/nav shell). Everything else in the `gateway` module still renders hardcoded English:
every page body, every form, every table, every handler-produced flash/error message. This tier
is the mechanical sweep that applies tier 2's established vocabulary to all of it, plus the
verifiable "no hardcoded English remains" check MAG-8 requires as its acceptance bar.

No new design decision is made about the translation *mechanism* (catalogue shape, resolution,
context-carrying, the switch endpoint) — those are settled and closed. This tier's own decisions
(recorded in `design.md`) are narrower: how to verify completeness, how to name new keys, how to
handle the handful of strings that carry an interpolated value (a count, a date, a percentage),
and how to split ~20 files of work into independent, parallelizable sub-tasks without two workers
colliding on the single `catalog.go` file.

## Full inventory (real counts, not an estimate)

Every `.templ` file in `templates/pages/`, `templates/fragments/`, `templates/ui/`, and every
handler-produced string in `internal/gateway/handlers/*.go` (excluding `_test.go`) was read in
full. Counts below are **occurrences requiring an `i18n.T` call site** (a string already covered
by an existing tier-2 key still counts as an occurrence — it needs a new call site, just no new
catalogue entry).

| Surface | File(s) | Occurrences | Notes |
|---|---|---:|---|
| Home | `templates/pages/home.templ` | 5 | 2 reuse tier-2 keys (`Connect your Tesla`, `Log out`) |
| Login | `templates/pages/login.templ` | 6 | incl. 1 brand noun ("TESLA CORE" — cataloged ES=EN, see design.md D2) |
| Dashboard page | `templates/pages/dashboard.templ` | 12 | 3 reuse tier-2 keys; 2 are "prefix + `{ expr }`" interpolation ("Last updated …", "Range …") |
| Dashboard helper | `templates/pages/dashboard.go` | 1 | `dashSubtitle`'s "Awaiting first snapshot" |
| Dashboard history page | `templates/pages/dashboard_history.templ` | 0 | pure composition, no literal text |
| Charges page | `templates/pages/charges.templ` | 2 | "Charge log", "Back to dashboard" |
| Supercharger Stats page | `templates/pages/supercharger_stats.templ` | 0 | both strings already reuse tier-2 keys — **no change needed** |
| Charge create form | `templates/fragments/charge_create_form.templ` | 20 | field labels, option text, submit button |
| Charge row (static) | `templates/fragments/charge_row.templ` | 5 | Edit/Delete buttons + the 3 confirm-dialog attributes (1 is interpolated) |
| Charge row (inline edit) | `templates/fragments/charge_row_edit.templ` | 20 | mostly reuses create-form's field-label keys; 4 net-new (Vehicle, Location kind, Save, Cancel) |
| Charges list | `templates/fragments/charges_list.templ` | 3 | "Your entries", "Refresh", empty-state sentence |
| History chart | `templates/fragments/history.templ` | 3 | "Awaiting nightly snapshots", 2 card titles |
| History handler | `internal/gateway/handlers/history.go` | 2 | interpolated: day-count preset label, "no snapshot" tooltip suffix |
| Supercharger Stats fragment | `templates/fragments/supercharger_stats.templ` | ~13 | 11 distinct strings, 2 reused twice each (tile label + card title) |
| Vehicles (legacy fragment) | `templates/fragments/vehicles.templ` | 13 | **dead code** — not rendered by any handler today (see design.md, Discoveries); explicitly in scope per the roadmap's file list regardless |
| Nav header (gold standard, 1 straggler) | `templates/fragments/nav_header.templ` | 1 | "Last seen" prefix — the value itself (`relativeLastSeen`, pluralized "N days/hours/minutes ago") is the tier-3 item tier 2's D9 explicitly flagged |
| Base shell (gold standard, 1 bug) | `templates/layouts/base.templ` | 1 | `<html lang="en">` is hardcoded regardless of resolved language — a real bug, not a missing translation (see design.md, Discoveries) |
| Confirm dialog | `templates/ui/confirm_dialog.templ` | 5 | "Are you sure?", "Cancel", "Confirm", "Delete", "Close" |
| Other `ui/` components | `alert, badge, button, card, field, icon, input, join, select, stat_tile, table, textarea` | 0 | pure `Props`-driven wrappers — no literal text of their own |
| Vehicle/dashboard notices + status words | `internal/gateway/handlers/handlers.go` | 21 | 9 `Notice`/status strings (incl. "Charging"/"Parked" + "Software v" prefix) + 11 bare-text `c.String` 4xx/5xx bodies (excl. `/healthz`, allowlisted — see design.md) |
| Charges validation + errors | `internal/gateway/handlers/charges.go` | 23 | 11 validation messages + 4 notices + 1 interpolated suggestion + 7 bare-text `c.String` bodies |
| Language switch errors | `internal/gateway/handlers/lang.go` | 3 | 3 bare-text `c.String` bodies |

**Total: ≈161 call sites across 20 files.** After deduping reused keys, this is roughly 100–115
net-new catalogue entries. `templates/pages/supercharger_stats.templ` needs **zero** changes.

Explicitly **out of scope**, named and deferred (design.md, Discoveries): the Supercharger
sessions table's `DateLabel` (`time.Format("Mon Jan 2, 2006")`, English weekday/month names) and
the vendor-passthrough `BillingType` column. Both require a genuine date-localization mechanism
(a weekday/month name table or a locale package) that does not exist yet — introducing one would
be a new design decision this tier's mandate explicitly excludes. Flagged for a future change.

## What Changes

- **Every hardcoded string enumerated above is replaced with an `i18n.T(ctx, key)` call** (or,
  for a plain Go helper invoked from a `.templ` block, an explicit `ctx` parameter mirroring the
  `navItems(ctx, active)` precedent tier 2 established — design.md D5), using tier 2's existing
  `internal/gateway/i18n` package exactly as-is: no change to `Key`, `entry`, `catalog`,
  `WithLang`, `FromContext`, or `T`.
- **~100–115 new catalogue keys** added to `internal/gateway/i18n/catalog.go` in one pass (design
  D4 — a single-writer sub-task, precisely to avoid two parallel workers editing the same map
  literal), following the existing `surface.thing` naming convention and reusing an existing key
  wherever the exact string already exists (`Connect your Tesla`, `Log out`, "Dashboard" nav
  label, "Supercharger Stats" nav label, "Back to dashboard").
- **Interpolated strings** (a day count, a percentage, a relative-time phrase) get a catalogue
  entry whose ES/EN value is itself a Go format string (`%d days`, `%d%%`, …); the call site wraps
  it `fmt.Sprintf(i18n.T(ctx, key), args...)` — no change to the catalogue's `entry{ES,EN}` shape
  or to `T`'s signature (design.md D3).
- **`relativeLastSeen`'s pluralized "N days/hours/minutes ago" phrasing is translated** — the
  explicit, named tier-3 item tier 2's design.md D9 flagged and deferred. Six catalogue entries
  (singular/plural × days/hours/minutes, plus "just now") cover it with the same
  format-string-in-catalogue pattern as above.
- **Two bugs found and fixed while sweeping the gold-standard surface** (not new translation work,
  genuine defects — design.md, Discoveries): `layouts.Base`'s `<html lang="en">` is hardcoded
  regardless of the resolved language (fix: `lang={ i18n.FromContext(ctx) }`); `nav_header.templ`'s
  "Last seen" prefix was left untranslated when tier 2 translated the rest of that fragment.
- **A new `make i18n-guard` target**, mirroring the existing `make ui-guard`'s shape and wired
  into `make check` (design.md D1) — a low-noise, grep-based static check across
  `templates/{pages,fragments,ui}/*.templ` (bare text nodes not routed through `i18n.T`) and
  `handlers/*.go` (literal strings assigned to known message-carrying sinks: `Notice`, `Error`,
  a validation-`errs` map, or a bare-text `c.String` 4xx/5xx body). An inline trailing
  `// i18n:allow: <reason>` comment is the sole, self-documenting escape hatch for a legitimate
  non-translatable literal (a brand noun gets a real ES=EN catalogue entry instead of an
  exemption — see design.md D2) — no separate allowlist file to keep in sync.
- **`internal/gateway/AGENTS.md`** gains a short addendum to the existing "i18n" section pointing
  at `make i18n-guard` as the mechanical enforcement companion to
  `TestCatalog_AllKeysHaveBothLanguages`, and documents the `// i18n:allow` marker.
- **`make templ && make css`**, and the regenerated `static/app.css` is committed (module CI
  guard).

**Not breaking.** No route, no Go interface, no database object changes. Every touched Go struct
field (`VehiclesData.Notice`, `DashboardData.Notice`/`StatusLabel`, `NavHeaderVM.LastSeenLabel`,
validation `errs` maps) keeps its existing type and shape — only the *value* assigned to it
changes from a literal to an `i18n.T(...)` result; all of these types are gateway-internal
(`templates/fragments`, never exported outside the module), so no external consumer is affected.
`internal/account` and every other module are untouched.

## Capabilities

### New Capabilities

(none — this proposal extends the existing `gateway` capability only)

### Modified Capabilities

- `gateway`: new requirement for full-application translation coverage (every page, fragment, and
  handler-produced string); new requirement for the automated hardcoded-string verification check;
  modified requirements for the Authenticated Navigation Shell and Navigation Vehicle Header (the
  `<html lang>` attribute and the relative "Last seen" phrasing now render through the catalogue).

## Impact

- `internal/gateway/i18n/catalog.go` — ~100–115 new `Key` constants + catalogue entries. No
  change to `i18n.go` (the `Key`/`WithLang`/`FromContext`/`T` mechanism is untouched).
- `internal/gateway/templates/pages/{home,login,dashboard,charges}.templ`,
  `internal/gateway/templates/pages/dashboard.go` — literal strings replaced with `i18n.T` calls.
- `internal/gateway/templates/fragments/{charge_create_form,charge_row,charge_row_edit,
  charges_list,history,supercharger_stats,vehicles,nav_header}.templ` — literal strings replaced.
- `internal/gateway/templates/ui/confirm_dialog.templ` — literal strings replaced.
- `internal/gateway/templates/layouts/base.templ` — `<html lang>` attribute fix.
- `internal/gateway/handlers/{handlers.go,charges.go,lang.go,history.go}` — `Notice`/`Error`/
  validation-map string literals and bare-text `c.String` bodies replaced with `i18n.T(ctx, ...)`
  (handlers already receive `ctx` — no new plumbing).
- `Makefile` — new `i18n-guard` target, added to the `check` chain.
- `internal/gateway/AGENTS.md` — short addendum documenting `make i18n-guard` and the
  `// i18n:allow` marker.
- `internal/gateway/static/app.css` — regenerated, committed (no new Tailwind/DaisyUI class is
  expected from this tier since no new markup structure is introduced — text-only changes — but
  the guard runs regardless per module convention).
- **Database: none.** This tier touches no schema, no migration, no `sqlc` query, no table. See
  design.md's explicit statement for the design gate.
- **Read path affected: none beyond what tier 2 already accepted.** `i18n.T` remains a single
  `map[Key]entry` lookup — O(1), no allocation beyond the returned string, no lock, no rebuild.
  This tier adds ~100 more entries to that same map (a compile-time literal, built once at
  package init) and, for the ~10 interpolated strings, one `fmt.Sprintf` call per render on an
  already-hot path element (nav-header's "Last seen", the dashboard's "Software v…", the history
  preset buttons) — the same cost class as the `fmt.Sprintf` calls those handlers already make
  today for km/°C/percentage formatting. No new DB read, no new per-request work beyond string
  formatting that was already happening in English.
