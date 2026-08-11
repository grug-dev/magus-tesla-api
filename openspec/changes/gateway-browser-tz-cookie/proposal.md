Source: MAG-7 (follow-up) — https://linear.app/magus-monitor/issue/MAG-7/date-filters
Roadmap: none (standalone change)

## Why

The dashboard history default window is computed server-side (Templ + Go), so "yesterday" was derived in UTC — a user in PST opening the dashboard at 10pm local saw the *previous* UTC day, which diverged from their browser's "yesterday." The nightly telemetry batch's `EffectiveDate` derivation (`rowToSnapshot`) is also location-sensitive; keeping the dashboard's notion of "today" aligned with the user's local day (not UTC) makes "data loads tomorrow" mean the user's tomorrow, and the fixed-axis dates match what they expect.

The right bundling point is: the date math stays server-side (Templ server-rendered), the browser contributes only its local timezone once via a cookie. The server reads it in `parseHistoryRange` / `defaultHistoryHref` / `buildHistoryPresets` and computes "today" in that zone, falling back to UTC when the cookie is absent (direct API calls, no browser).

## What Changes

- **New `browser_tz` cookie** set by a single inline `<script>` in the `layouts.BaseAuth` base layout — the base shell already wrapping every authenticated page, so the cookie is set on every authenticated dashboard/connect/charges/etc. load and persists across navigation. The script runs once on first auth page load after nothing else; it does NOT run on public pages (`Base` layout — login). One cookie per browser, not per page.
- **`browserLocation(c *gin.Context) *time.Location` helper** in `internal/gateway/handlers/` (new, shared): reads the `browser_tz` cookie, parses it via `time.LoadLocation`, validates against a small allow-list of behaviors, and returns the browser's `*time.Location`; on any failure (missing cookie, malformed, not in the IANA database, noscript) returns `time.UTC`. Drains the parser of failures so callers never handle an error.
- **`browserToday(c) time.Time` helper** wraps `startOfDayIn(time.Now(), browserLocation(c))` — a new `startOfDayIn(t, loc)` generalizing `startOfDay` to a caller-supplied `*time.Location`. (`startOfDay` is retained as the UTC variant — it has other uses, and a future Supercharger Stats change will adopt the new helper.)
- **`parseHistoryRange(c)` uses `browserToday(c)`** for both the default-absent window's `end = today-in-browser-TZ` and the `end <= today` (end inclusive) cap. The `start`/`end` query params are still bare `YYYY-MM-DD` UTC-midnight-bounded strings — the cap compares to the browser's "today" so a user in PST can request `end=next-day-UTC` up to their local end of today without a spurious 400.
- **`defaultHistoryHref()` (handlers.go) and `buildHistoryPresets()` (history.go)** use `browserToday(c)` so the dashboard self-load + preset buttons send `end = browser-yesterday` (the user's local yesterday, not UTC yesterday).
- **One inline `<script>` in `layouts.BaseAuth`** sets the cookie — no library, no fetch, no Promise; one line in the base layout, hidden behind `<noscript>` fallback (the cookie just doesn't get set, the server falls back to UTC).
- **Tests** cover: TZ cookie honored (default window + end<=today cap use cookie TZ), malformed TZ falls back to UTC, cookie absent falls back to UTC, the inline script is present in the rendered BaseAuth shell.

## Breaking?

No. Direct API callers (no cookie) get the existing UTC behavior unchanged. The change only shifts "today"/"yesterday" from UTC to the browser's local zone for users with JS + a valid IANA timezone. Backward-compatible with the existing `parseHistoryRange` contract.