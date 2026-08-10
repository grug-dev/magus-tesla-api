## 1. Shift dashboard UI end date from today to yesterday

- [x] 1.1 In `internal/gateway/handlers/handlers.go`, `defaultHistoryHref()`: `end = today - 1day` (yesterday), `start = end - historyRangeWindowDays`. Same 6-day-wide window, shifted back one day. API unchanged.
- [x] 1.2 In `internal/gateway/handlers/history.go`, `buildHistoryPresets()`: `pEnd = today - 1day`, `pStart = pEnd - n`. Same 6/14/30-day-wide presets, shifted back one day. Active still compares against `(start, end)` — matches because the dashboard self-load now sends `end = today-1`.

## 2. Tests

- [x] 2.1 Update `TestBuildHistoryView_PresetsCarryAbsoluteHrefs` (end=today-1), `TestDashboardHistoryFragment_DefaultWindowActivatesSixDayPreset` (send explicit params from defaultHistoryHref), `TestDashboard_HistoryRegionInsideDashboardContent` (assert end=yesterday).

## 3. Verification

- [x] 3.1 `go test -count=1 ./internal/gateway/...` green; `go vet ./...` exit 0.
