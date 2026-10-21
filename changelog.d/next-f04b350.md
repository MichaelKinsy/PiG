### Added

- `chord/telemetrysqlite` ports Pi's `@earendil-works/chord/telemetry-sqlite`: `CreateSqliteTelemetry(db, options)` records settled spans with inline events and links and metrics aggregated per series per ten seconds into any `chord/sqlite` database, flushes once a second with retention cleanup, bounds its queue (counting `DroppedSpans` and `DroppedMeasurements`), and serves `telemetry.TelemetryStore` queries over the committed rows.
