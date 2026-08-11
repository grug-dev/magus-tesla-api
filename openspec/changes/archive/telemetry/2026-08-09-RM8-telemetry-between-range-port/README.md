# RM8-telemetry-between-range-port

Add `SnapshotsByVehicleBetween(ctx, accountID, teslaID, start, end)` to `telemetry.Reader` as a bounded, `[start, end]`-inclusive range scan over the existing `vehicle_snapshots` table (no schema change) — tier 1 of the `RM8-history-date-range` roadmap (Linear MAG-7). `SnapshotsByVehicleSince` is kept (additive / non-breaking).
