### Changed

- Chord telemetry ingest: `telemetryingest.IngestOptions` takes `Admit`, which decides whose pages may send spans; a page of a refused principal fails `forbidden` (`chord.telemetry.page: not admitted`) and nothing it sends is written. The Chord app kit admits every signed-in page. <!-- feat: 7ae9ff8c7b -->
