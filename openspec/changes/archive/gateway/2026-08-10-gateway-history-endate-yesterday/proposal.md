Source: MAG-7 (follow-up) — https://linear.app/magus-monitor/issue/MAG-7/date-filters
Roadmap: none (standalone change)

## Why

The nightly telemetry batch captures snapshots at 03:30 local; today's data is not available
until tomorrow. The RM8 gateway tier defaulted the dashboard history self-load and the preset
buttons to `end = today` — so the last bar on both charts is always an empty "no snapshot" slot
(the fixed-axis renders it because the axis is inclusive). This follow-up shifts the **dashboard
UI only** to `end = today - 1day` (yesterday), so the last bar always has data. **The API
contract (`parseHistoryRange`: `end <= today`, inclusive, default `end = today` when both params
absent) is unchanged** — a direct API caller can still request `end = today`; the dashboard page
just no longer offers it as a default.

## What Changes

- **`defaultHistoryHref()`** (`internal/gateway/handlers/handlers.go`): `end = today - 1day`,
  `start = end - historyRangeWindowDays` (same 6-day-wide window, shifted back one day).
- **`buildHistoryPresets()`** (`internal/gateway/handlers/history.go`): `pEnd = today - 1day`,
  `pStart = pEnd - n` (same 6/14/30-day-wide presets, shifted back one day). `Active` compares
  against the requested `(start, end)` — still works because the dashboard self-load now sends
  `end = today-1`, which matches the shifted presets.
- **`parseHistoryRange` unchanged** — the API default (both-absent → `end = today`) and
  validation (`end <= today`, inclusive) stay as-is. The dashboard self-load never hits the
  default-absent path anymore (it always passes explicit `?start=&end=`), so the API default
  is only reached by direct/manual API calls.
- **Tests updated** to assert the new `end = today - 1day` on the dashboard self-load and the
  preset `Active` match.

## Breaking?

No. The HTTP API is unchanged. Only the dashboard page's computed default end date shifts from
`today` to `today - 1day` — a UI-only adjustment.