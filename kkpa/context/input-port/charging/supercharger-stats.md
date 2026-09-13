# Supercharger Stats page — /supercharger-stats

> **Adapter side:** what the outside world calls, and where it forwards to. **No backend flow
> here** — that lives in the linked use-case files.
>
> "Input port" in this KB means the *driving adapter* — the page, route, or endpoint that
> triggers a use case. (Strict hexagonal reserves "port" for the interface the core exposes;
> this KB deliberately uses the looser sense. Do not go looking for a `Port` interface in the
> code because of this filename.)
>
> All KB links below are relative to `kkpa/context/`.

## Page

- **Route / URI:** `/supercharger-stats`
- **Description:** Tesla-billed Supercharger and DC fast-charging sessions for the selected
  vehicle — tiles, a per-month kWh bar chart, and a session table. The rows are **mirrored from
  Tesla**, not authored by the user: the only thing editable here is the pair of battery
  percentages, which Tesla's API does not supply and a human fills in.
- **Module:** `charging`

## Front-end component map

| File | Role |
|---|---|
| `internal/gateway/templates/pages/supercharger_stats.templ` | Page shell — tiles, chart, session table |
| `internal/gateway/templates/fragments/supercharger_stats.templ` | The swappable stats region |
| `internal/gateway/templates/fragments/supercharger_row.templ` | Static session row + the Edit link |
| `internal/gateway/templates/fragments/supercharger_row_edit.templ` | Inline two-input edit form (start/end battery %) |
| `internal/gateway/templates/fragments/supercharger_vm.go` | `SuperchargerStatsView` / `SuperchargerRowVM` / `SuperchargerTiles` |
| `internal/gateway/templates/fragments/supercharger_stats.go` | View-model helpers for the stats region |
| `internal/gateway/handlers/supercharger.go` | Every handler for this page, the tile/chart/row builders, and `recalculateAfterSessionVerify` |
| `internal/gateway/i18n/catalog.go` | Every user-facing string on this page, ES + EN |

## Endpoints

| Method + path | Purpose | Use case |
|---|---|---|
| `GET /supercharger-stats` | Full page load (`SuperchargerStatsPage`) | — read path, see `workflows/supercharger-stats-read.md` |
| `GET /ui/supercharger-stats` | Re-render the stats region — date filter and vehicle switch (`SuperchargerStatsFragment`) | — read path |
| `GET /ui/supercharger-stats/row/:id` | Cancel-edit — swap back to the static row (`SuperchargerRowStatic`) | — read path |
| `GET /ui/supercharger-stats/row/:id/edit` | Swap the static row for the inline edit form (`SuperchargerRowEditFragment`) | — read path |
| `PATCH /ui/supercharger-stats/row/:id` | Save corrected battery percentages | `use-case/charging/verify-session-battery.md` |

**There is deliberately no create and no delete route.** Sessions arrive through
`charging.SessionWriter.MirrorSessions` in the nightly cycle; the UI's only write channel is the
two-field correction above.

## Adapter-side conventions

- **CSRF token key:** `csrf_supercharger` — a *different* session key from the manual page's
  `csrf_externalcharge`. Issued by `SuperchargerStatsPage`.
- **No `RegisteredVehicles` ownership check on the write.** `VerifySession`'s own vehicle-scoped
  `WHERE` clause (`id` AND `tesla_id`) is the sole tenant boundary here — a documented, deliberate
  divergence from the manual page. Do not add one without reading
  `architecture/charge-record-mutation.md`.
- **"Today" is plain UTC on this page**, not `browserToday(c)` — `startOfDay(time.Now().UTC())`.
  The window is month-anchored and capped at 400 days by `parseSuperchargerRange`.
- **Window threading:** `superchargerWindowStrs` re-parses `?start=&end=` and echoes it onto every
  row action URL. `fetchSuperchargerRowVM` resolves a single row by re-listing **within that
  window** and matching in memory — so a row action carrying a bad window resolves to "not found",
  and every failure mode collapses to the same `(zero, false)` with no distinction.
- **The success response swaps only the edited row** — no list or tile refresh, unlike the manual
  page's out-of-band `#external-charges-list` update.
- **i18n:** every string resolves through `i18n.T(ctx, key)` with both ES and EN non-empty.

## Related KB

- `architecture/charge-record-mutation.md` — the shared write contract and its known divergences
- `workflows/supercharger-stats-read.md` — the read side, date filter, and chart axes
- `input-port/charging/external-charges.md` — the sibling page
- `architecture/nightly-cycle.md` — how these rows get here in the first place
