## Context

Roadmap `RM24-i18n-translations`, tier 3 of 3. Tier 2 (`RM24-gateway-add-i18n-foundation`,
archived) built and proved the entire translation mechanism — `internal/gateway/i18n`'s
`Key`/`entry{ES,EN}`/`catalog`/`WithLang`/`FromContext`/`T(ctx, key)`, the per-request
`languageMiddleware`, the navbar `ui.LangSwitcher`, and `POST /ui/lang/switch` — on one surface
(the layout/nav shell). This tier does not reopen any of that. Its job is mechanical repetition
of the established pattern across every remaining `.templ` file and every handler-produced
string, plus the verification check MAG-8 asks for as the acceptance bar.

**Full inventory:** see `proposal.md`'s table — ≈161 call sites across 20 files, every one read
in full before this document was written (not sampled, not estimated from filenames).

**Database: this tier touches no database object.** No migration, no `sqlc` query, no schema
change, no new table/column/index. The `database` design gate applies trivially — there is
nothing for it to review.

**Performance profile (read-heavy, `ai/architecture.md` §7):** `i18n.T` is unchanged — still a
single `map[Key]entry` lookup, O(1), read-only, built once at package init as a Go literal. This
tier roughly quintuples the map's key count (~20 → ~120), which has no measurable read-path cost
(a Go map lookup's cost does not scale meaningfully with a few hundred more entries at this
size). The ~10 interpolated strings (day counts, relative-time phrases, percentages) add one
`fmt.Sprintf` call each on an already-hot render path — the same cost class as the `fmt.Sprintf`
calls those same handlers already make today for km/°C/percentage formatting (see
`mapDashboardSnapshot`, `formatKm`). No new allocation pattern, no new lock, no new DB read.

## Goals / Non-Goals

**Goals:**
- Every hardcoded English string enumerated in `proposal.md`'s inventory routes through
  `i18n.T(ctx, key)` (or a `fmt.Sprintf(i18n.T(ctx, key), ...)` wrapper for the ~10 interpolated
  ones), with both `es` and `en` populated for every new key.
- A verifiable, runnable check (`make i18n-guard`) that fails when a new hardcoded user-facing
  string is introduced, low-noise enough to stay in `make check`'s default gate.
- Independent, parallelizable sub-tasks (`tasks.md`) that avoid two workers editing
  `internal/gateway/i18n/catalog.go` concurrently.
- Two real defects found while sweeping the gold-standard surface, fixed in the same change
  (below, "Discoveries").

**Non-Goals (tier 2's mechanism is closed, not reopened):**
- No change to `internal/gateway/i18n/i18n.go` (`Key`, `WithLang`, `FromContext`, `T`) or to the
  `entry{ES,EN}` catalogue shape.
- No change to `languageMiddleware`, the cookie sync, `ui.LangSwitcher`, or
  `POST /ui/lang/switch`.
- No third locale, no ICU plural rules, no locale-negotiation header — still exactly `{es, en}`.
- No date/time localization mechanism (weekday/month names). The Supercharger sessions table's
  `DateLabel` (`time.Format("Mon Jan 2, 2006")`) needs one to be genuinely correct in Spanish, but
  building it — a weekday/month lookup table or adopting a locale package — is itself a new
  mechanism decision, which this tier's mandate ("no new design decisions about the i18n
  mechanism") excludes. Named and deferred; see Discoveries and Risks.
- No translation of Tesla-vendor passthrough values (`BillingType`) — these are vendor-emitted
  codes displayed as-is, not application copy.

## Decisions

### D1 — Verification check: `make i18n-guard`, a grep-based static guard mirroring `make ui-guard`

The project already has exactly this class of check for a different closed-vocabulary rule:

```make
ui-guard: ## Fail if a raw DaisyUI component class is inlined in a page/fragment
	@if grep -rnE 'class="([^"]* )?(btn|card|input|...)"' \
		internal/gateway/templates/pages internal/gateway/templates/fragments --include='*.templ'; then \
		... exit 1; \
	fi
```

`i18n-guard` reuses the identical shape (a `grep -rnE` over the same three template directories,
plus the handler package), because the project's own AI-efficiency rule favors reusing a closed,
already-understood mechanism over inventing a second one (`CLAUDE.md`: "closed, small
vocabularies an agent looks up instead of re-inventing"). It is **two grep passes**, not a full
parser — deliberately, matching `ui-guard`'s own rigor bar (a heuristic that catches the
overwhelming majority of the real surface, not a 100%-precision AST tool).

**The `// i18n:allow: <reason>` marker — placement and matching (leader amendment at artifact
reconcile).** Placement differs by file type, and the guard's matching MUST accommodate both or
the escape hatch is inert:

- **`.go` files** — the marker is a trailing `//` comment on the **same line** as the literal.
  A plain "skip lines containing `i18n:allow`" filter is sufficient for pass 2.
- **`.templ` files** — templ has no comment syntax valid inside a markup region, and HTML
  comments (`<!-- -->`) are **forbidden project-wide** (`CLAUDE.md` → "HTML templates": they ship
  verbatim to the browser). So the marker goes on the **preceding line**, as a Go line comment.
  Pass 1 therefore CANNOT be a line-local filter: it must skip a matching line when **either that
  line or the line immediately above it** carries `i18n:allow`. Concretely, pipe the `grep -rnE`
  output through a filter that carries one line of lookbehind (e.g. `grep -rnE ... -B1` and drop a
  match whose preceding context line contains the marker, or an `awk` pass over each file holding
  `prev`), rather than a bare `grep -v i18n:allow`.

An implementation that filters pass 1 line-locally is **wrong** — the `.templ` escape hatch would
silently never fire, and a worker hitting a legitimate false positive would be left with a failing
guard and an explicit prohibition (T14.3) on weakening the pattern. Verify the hatch actually works
by adding a marker to one deliberately-flagged `.templ` line and confirming the guard goes green.

**Pass 1 — templates.** Scans `internal/gateway/templates/{pages,fragments,ui}/*.templ` for a
bare text node (letters that are not already inside an `i18n.T(...)` call) using two patterns,
because templ text frequently sits adjacent to a `{ ... }` Go expression on the same line rather
than being fully enclosed by `>...<`:

- Pattern A — a self-contained bare text node: `>[[:space:]]*[A-Za-z][^<{]*<` (catches
  `<h1>Magus</h1>`, `<summary>More details</summary>`).
- Pattern B — bare text immediately touching a `{ }` expression on either side, the common
  "prefix + value" shape (`Last updated { d.LastUpdated }`, `Range { dashStat(...) }`): a line
  matching `>[[:space:]]*[A-Za-z][^<{}]*\{` (leading text before an expression) or
  `\}[^<{}]*[A-Za-z][^<{}]*<` (trailing text after one).

Both patterns exclude lines already containing `i18n.T(` (the fixed-up form) and lines carrying
`i18n:allow`. This is a **heuristic, not exhaustive** static check — same honesty bar as
`ui-guard`, which is also a single-line regex, not a parser. It will not catch a violation split
across multiple lines in an unusual way; it does catch every actual instance found in this tier's
inventory (verified by running it, in draft form, against the pre-fix tree during this design's
authoring).

**Pass 2 — handlers.** Scans `internal/gateway/handlers/*.go` (excluding `_test.go`) for a
double-quoted string literal with letters landing on one of the known message-carrying sinks:

- `(Notice|Error):\s*"[A-Za-z]` — struct-literal field assignment (`fragments.VehiclesData{Notice: "..."}`
  ).
- `errs\[[^]]+\]\s*=\s*"[A-Za-z]` — the validation-error map pattern used throughout `charges.go`.
- `\.\w+\s*=\s*"[A-Za-z]` scoped to the tracked receiver names (`vm.`, `d.`) — direct field
  assignment (`vm.Notice = "..."`).
- `c\.String\(http\.Status[45]\d\d,\s*"[A-Za-z]` — a bare-text 4xx/5xx response body a caller
  might see directly (a non-htmx page load that errors, e.g. a failed OAuth callback). **Excludes**
  2xx `c.String` calls (`Healthz`'s `"ok"`) and is itself excluded on `Healthz`'s `"unhealthy: %v"`
  line via `i18n:allow` (that response is consumed by uptime monitoring, not rendered to a user —
  see Discoveries).
- The same four patterns additionally match when the literal is the first argument to
  `fmt.Sprintf(` / `fmt.Errorf(` instead of a bare literal, so `Label: fmt.Sprintf("%d days", n)`
  is flagged exactly like `Label: "%d days"` would be.

**Why not an AST-based (`go/parser`) checker.** A full AST walk would eliminate the templates
pass's known blind spots (a violation split unusually across lines) but the `.templ` source
itself is not valid Go — `go/parser` can only walk the **generated** `_templ.go` output, which
interleaves static-HTML `WriteString` calls with codegen scaffolding in a shape that changes
across `templ` versions (the exact opposite of a stable check to build against). The handler-side
Go source *could* be AST-walked for more precision, but doing so for only half the surface while
the template half stays regex-based buys little: it would be a second, differently-shaped
mechanism (new indirection cost per the project's own AI-efficiency rule) for a check whose job
is to catch a class of mistake, not to be a compiler. The two-pass grep is cheap to read, cheap to
extend (a third pattern is one more `grep -E` alternation), and matches the existing `ui-guard`
precedent exactly.

**Wiring:** `check: build vet ui-guard i18n-guard test` in the `Makefile` (i18n-guard added
alongside the existing `ui-guard`, same position in the chain — a static-analysis gate before the
test suite runs). Documented in `internal/gateway/AGENTS.md`'s existing "i18n" section (a
one-paragraph addendum, not a new section) and in the root `README.md`'s "Making a change" table
implicitly via `make check` (already the row-level instruction; no row needs new wording since
`check` now transitively includes the guard).

### D2 — Key-naming convention: `<surface>.<thing>`, reuse before minting

Follows tier 2's existing pattern exactly (`nav.dashboard`, `nav_header.no_tesla`,
`lang_switcher.aria`) — no new convention is introduced. Concretely for this tier's surfaces:
`home.*`, `login.*`, `dashboard.*` (page), `dashboard_status.*` (the Charging/Parked words,
namespaced separately from the page's own labels since they're handler-computed, not
template-literal), `charges_form.*` (shared by both the create form and the inline edit form —
see below), `charges_list.*`, `charges_page.*`, `history.*`, `supercharger.*`, `vehicles.*` (the
dead-code fragment — still gets real keys, see Discoveries), `confirm_dialog.*`,
`vehicles_notice.*` (handlers.go's `vehiclesFor` Notice strings), `dashboard_notice.*`
(handlers.go's `dashboardFor` Notice string — separate from `dashboard.*` which is the page's
static labels), `oauth_error.*` (handlers.go's bare-text OAuth/connect `c.String` bodies),
`charges_error.*` (charges.go's validation + notice + bare-text strings), `lang_switch_error.*`
(lang.go's bare-text strings).

**Reuse before minting a key — mandatory, checked at review time, not mechanically enforced.**
Several strings in this tier's inventory are **identical** to an existing tier-2 key and MUST
reuse it rather than mint a duplicate:

| String | Reuse this tier-2 key |
|---|---|
| "Connect your Tesla" (home.templ, dashboard.templ) | `i18n.KeyNavHeaderConnectLink` |
| "No Tesla connected." (dashboard.templ) | `i18n.KeyNavHeaderNoTesla` |
| "Log out" (home.templ) | `i18n.KeyNavLogout` |
| "Dashboard" (dashboard.templ's `PageHeader` title) | `i18n.KeyNavDashboard` |
| "Supercharger Stats" (supercharger_stats.templ page title) | `i18n.KeyNavSuperchargerStats` |

And within this tier's own new keys, `charges_form.*` is deliberately **one** namespace shared by
`charge_create_form.templ` and `charge_row_edit.templ` (they render the same field set — Date,
Energy added (kWh), Price, Currency, Started at, Ended at, Start/End battery %, Charging type,
Location label, Notes, the Home/Work/Other and AC/DC option text, "More details") — a genuine
reused vocabulary, not a coincidence, so one key per field label serves both forms. "Back to
dashboard" (charges.templ) is similarly reused verbatim by `supercharger_stats.templ`, which is
why that page needs **zero** new call sites (both its strings — page title and back-link — already
resolve through existing/this-tier keys once `charges_page.back_to_dashboard` exists).

**Brand/proper nouns get a real ES=EN catalogue entry, not an exemption.** "Magus" (base.templ,
home.templ) and "TESLA CORE" (login.templ) are cataloged with identical `ES`/`EN` values —
exactly the precedent tier 2 already set for the language names themselves
(`KeyLangSwitcherSpanish`/`KeyLangSwitcherEnglish`, both `"Español"`/`"English"` in both fields,
`TestCatalog_AllKeysHaveBothLanguages` only asserts non-empty, never that the two differ). This
is deliberately chosen over adding these two literals to the `i18n-guard` allowlist: routing them
through the catalogue means the guard's pass-1 regex needs **zero** brand-noun special-casing,
keeping the check's own logic simple (AI-efficiency: one mechanism, the catalogue, instead of two
overlapping ones — a catalogue entry and an allowlist entry — for the same class of "text that
doesn't change between languages").

### D3 — Interpolated strings: the catalogue entry IS the format string, `fmt.Sprintf` wraps it at the call site

No change to `entry{ES,EN}`'s shape or to `T`'s signature. A catalogue value MAY itself contain
`%d`/`%s`/`%.0f`/etc. verbs; the call site composes it exactly like any other `fmt.Sprintf` call
already does elsewhere in this codebase (`formatKm`, `mapDashboardSnapshot`):

```go
// catalog.go
KeyHistoryDaysPreset: {ES: "%d días", EN: "%d days"},

// history.go call site
Label: fmt.Sprintf(i18n.T(ctx, i18n.KeyHistoryDaysPreset), n),
```

This tier's ~10 interpolated strings, concretely:

| Key (namespace) | ES pattern | EN pattern | Call site |
|---|---|---|---|
| `history.days_preset` | `%d días` | `%d days` | `history.go`, preset button label |
| `history.no_snapshot_tooltip` | `%s · sin dato` | `%s · no snapshot` | `history.go`, bar tooltip |
| `dashboard_status.software_version` | `Software v%s` | `Software v%s` (identical — "Software" is conventionally left in English in both, like a version-string prefix; still a real catalogue entry, not an allowlist exemption) | `handlers.go`, `mapDashboardSnapshot` |
| `charges_error.battery_suggestion` | `Última: %d%%` | `Latest: %d%%` | `charges.go`, start-battery placeholder |
| `supercharger.months_preset` | `%d meses` | `%d months` | `supercharger_stats.templ`, preset button label |
| `nav_header.last_seen_days` (singular) | `hace 1 día` | `1 day ago` | `handlers.go`, `relativeLastSeen` |
| `nav_header.last_seen_days_plural` | `hace %d días` | `%d days ago` | same |
| `nav_header.last_seen_hours` (singular) | `hace 1 hora` | `1 hour ago` | same |
| `nav_header.last_seen_hours_plural` | `hace %d horas` | `%d hours ago` | same |
| `nav_header.last_seen_minutes_plural` | `hace %d minutos` | `%d minutes ago` | same |
| `nav_header.last_seen_just_now` | `justo ahora` | `just now` | same |

`relativeLastSeen` closes tier 2's D9-flagged, explicitly-named tier-3 item ("Tier 3 should also
make an explicit, named decision about translating `relativeLastSeen`'s pluralized phrasing").
Spanish and English both only distinguish singular/plural here (no dual, no complex agreement),
so six catalogue entries — three singular/plural pairs plus the zero-case "just now" — are
sufficient; no ICU plural-rules library is introduced (that would be the over-abstraction tier
2's D1 already rejected, restated here for the same reasoning).

**Argument-order caveat, stated for completeness though unused today:** none of this tier's
interpolated strings need a different argument order between ES and EN (each has exactly one
`%d`/`%s`). If a future string ever needs word-order reordering (uncommon between ES/EN, common
between e.g. EN/JA), Go's `fmt` already supports explicit positional verbs (`%[1]s`) inside the
catalogue string with zero change to this pattern — noted so a future worker does not think a new
mechanism is needed.

### D4 — Sub-task structure: one single-writer catalogue task first, then disjoint-file parallel waves

`catalog.go` is one file holding one `const (...)` block and one `map[Key]entry{...}` literal.
Two parallel workers each adding keys to it would produce a near-certain merge conflict (both
edits land in the same few lines) or, worse, a silent duplicate/renamed key if resolved
carelessly. The fix or is the same one tier 2 already used within its own T1 (a single package,
built once): **task 1 adds every new key this tier needs, in one pass, before any other task
starts.** Every other task's dependency is `T1` alone (not on each other), so they run in
parallel — each touches a **disjoint** file (or a disjoint, non-overlapping code region of a
shared handler file — see below), never `catalog.go` again.

This also directly serves the "no new design decisions" mandate: because T1 pre-declares every
key's exact name, every downstream task is purely mechanical ("replace this literal with
`i18n.T(ctx, i18n.KeyXxx)`") — no dispatched worker on a downstream task needs to *invent* a key
name, eliminating any risk of two workers independently minting differently-named duplicate keys
for the same string.

**Why handler files are NOT split further per-concern.** `handlers.go` carries strings for
several unrelated concerns (vehicle-seed notices, dashboard status, OAuth error bodies). These
are kept as **one** sub-task (not split three ways) purely because they share one file — two
workers editing the same `.go` file concurrently risks the same merge-collision class `catalog.go`
has, just smaller in blast radius. `charges.go` and `lang.go` are naturally already separate
files, so they get their own sub-tasks. `history.go` is bundled with `history.templ` (same
"history" feature surface, both small) rather than force-split for parallelism that buys nothing
at this size.

### D5 — Handler-produced strings call `i18n.T(ctx, key)` directly; no new enum types introduced where one isn't already justified

Tier 2's D9 introduced `NavHeaderStatusKind` (an enum) specifically because that value already had
an independent structural use (it also selects the status-dot's badge color via `navBadgeKind`) —
the enum was justified by that *second* consumer, not by the translation need alone. This tier's
handler-produced strings have no such second consumer, so none of them get a new enum:

- `dashStatus` (handlers.go, "Charging"/"Parked") stays a two-branch function; it is given an
  explicit `ctx context.Context` first parameter (threaded from `dashboardFor`, which already
  receives `ctx`) and returns `i18n.T(ctx, i18n.KeyDashboardStatusCharging)` /
  `i18n.T(ctx, i18n.KeyDashboardStatusParked)` directly — mirroring the `navItems(ctx, active)`
  precedent (a plain Go helper invoked *from* a `.templ` block, or in this case from another
  handler function, takes `ctx` explicitly; a `.templ` component receives it implicitly).
- `vehiclesFor`, `dashboardFor`, and every `c.String(...)` call site already receive `ctx` (as
  `c.Request.Context()` or a direct `ctx` parameter) — no plumbing is added anywhere; the fix is
  always "replace the literal with `i18n.T(ctx, key)` (or the `fmt.Sprintf` wrapper for D3's
  interpolated ones), using the `ctx` already in scope."
- `relativeLastSeen(capturedAt, now time.Time) string` gains an explicit `ctx` third parameter for
  the same reason — its one call site (inside `navHeaderFor`) already has `ctx` in scope.

This keeps every fix a same-shape, same-size diff (add/thread one `ctx` parameter if the function
doesn't already have one two levels up; replace the literal), which is exactly the "mechanical
repetition" this tier is scoped to be — no new abstraction, no new field, no new struct.

## Discoveries (found while reading every target file in full — not introduced by this tier, fixed in it)

1. **`layouts.Base`'s `<html lang="en" data-theme="apex">` is hardcoded to `"en"` regardless of
   the resolved language.** This predates this tier (tier 2 did not touch the `<html>` tag) and is
   a genuine accessibility/correctness bug, not a missing translation — the `lang` attribute tells
   assistive technology and search engines what language the page content is in, and today it
   always claims English even when every string on the page is rendering in Spanish. **Fix:**
   `lang={ i18n.FromContext(ctx) }` — a pure context read, the exact same call `LangSwitcher`
   already makes on the same file two lines below. Task T9 below.
2. **`nav_header.templ`'s "Last seen" prefix was left untranslated** when tier 2 translated the
   rest of that fragment (`{ i18n.T(ctx, statusLabelKeyFor(vm.Status)) }` was added, but the
   sibling `<span>• Last seen { vm.LastSeenLabel }</span>` line was not touched). One straggler in
   an otherwise-complete gold-standard surface. Bundled into the same task as fix #1 and D3's
   `relativeLastSeen` translation (same file, same small task).
3. **`templates/fragments/vehicles.templ`'s `VehiclesList` component is dead code** — grepping
   every caller of `fragments.VehiclesList`/`fragments.VehiclesData` shows the `VehiclesData`
   struct is still populated by `vehiclesFor` (called once, for its side effect: seeding the
   user's first Tesla vehicle on dashboard load — see `handlers.go` line 124,
   `_ = h.vehiclesFor(...)`), but the `VehiclesList` **template component itself is never rendered
   by any handler or route** — no `@fragments.VehiclesList` call exists anywhere in the current
   codebase. It is nonetheless **explicitly named in the roadmap's tier-3 scope** ("Cover all
   of ... `templates/fragments/` ... vehicles") and is cheap to translate, so this tier translates
   it anyway rather than special-casing an exclusion — keeping the whole `templates/fragments/`
   directory uniformly compliant with the AGENTS.md i18n rule regardless of current reachability,
   and avoiding a judgment call ("is it really dead, or does something dynamic reach it") that
   isn't this tier's to make. Flagged for the reviewer; not a blocker.
4. **The Supercharger sessions table's `DateLabel` and `BillingType` are out of scope**, named
   explicitly rather than silently skipped — see Non-Goals above. `DateLabel` needs a genuine
   date-localization mechanism this tier is not chartered to design; `BillingType` is a
   vendor-passthrough code value, not application copy.
5. **`Healthz`'s `c.String(http.StatusServiceUnavailable, "unhealthy: %v", err)` and
   `c.String(http.StatusOK, "ok")` are ops-only, not user-facing** — `/healthz` is consumed by
   uptime/monitoring tooling, never rendered as a page a signed-in user sees. The 503 line is
   marked `// i18n:allow: ops health-check response, not user-facing UI` so `i18n-guard`'s pass 2
   does not flag it (the 200 `"ok"` line is already outside the guard's `[45]\d\d` scope and needs
   no marker).

## Risks / Trade-offs

- **`i18n-guard` is a heuristic, not a parser** (D1). It will not catch every conceivable
  multi-line violation shape. Accepted at the same rigor bar as the pre-existing `ui-guard`, which
  has the identical limitation and has been the project's static-analysis pattern for a
  DaisyUI-class-leak rule since it was introduced. A missed case is still caught by
  `TestCatalog_AllKeysHaveBothLanguages` failing to compile against an undefined `Key` (if the
  worker forgot to add the catalogue entry) or, worst case, by manual QA (the existing `!!key`
  visible-marker backstop from tier 2's D2 still applies to every key in this tier).
- **`DateLabel`'s English weekday/month names are a known, named, deferred gap** (Discoveries #4).
  Flagged for `openspec/roadmaps/backlog.md` as a follow-up once this tier lands, per the roadmap's
  own "Future work" convention.
- **`vehicles.templ` is translated despite being unreachable** (Discoveries #3). Low cost, zero
  risk — flagged for the reviewer as a discovery, not treated as scope creep since the roadmap
  names the file explicitly.
- **Two pre-existing bugs are fixed as part of a translation-labeled change** (Discoveries #1, #2).
  Both are one-line fixes discovered by necessity while reading the exact files this tier's scope
  requires reading anyway; deferring them to a separate change would mean re-reading the same
  files a second time for no benefit. Flagged for the reviewer as bundled-but-justified.

## Implementation Plan

See `tasks.md` for the full, dependency-ordered, parallelizable breakdown. Summary:

1. `internal/gateway/i18n/catalog.go` — every new key (single-writer, blocks everything else).
2. Ten independent template/page sub-tasks (each a disjoint file or file pair) — all depend on
   step 1 only, run in parallel.
3. Three independent handler sub-tasks (`handlers.go`, `charges.go`, `lang.go`) — depend on step 1
   only, run in parallel with each other and with step 2.
4. `make i18n-guard` (Makefile target) — depends on step 1 (can be authored any time after keys
   exist), validated clean only once steps 2–3 are complete.
5. `make templ && make css`; commit `static/app.css` — depends on every template-touching task in
   step 2.
6. `internal/gateway/AGENTS.md` addendum — depends on step 1 (references the new guard + marker).
7. Full verification: `go build ./...`, `go vet ./...`, `go test ./...`, `make i18n-guard`,
   `openspec validate --strict` — depends on everything above.

## Open Questions

None blocking. The `DateLabel` date-localization mechanism (Discoveries #4) is the one
substantive follow-up; it is not a question this tier needs answered, since it is explicitly out
of scope, not ambiguous.
