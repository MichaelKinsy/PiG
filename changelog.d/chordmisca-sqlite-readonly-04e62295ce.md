### Added

- Chord: the Node SQLite adapter opens a file read-only (`NodeSqliteDatabaseOptions.Mode = ReadOnly`, Pi main `ff33ef42c`): no directory is made, nothing is configured or checkpointed, and its transactions are deferred (`BEGIN`) and read one snapshot. A document's commit records a `chord.commit` event (documents, ops, table rows, duration) on its context's span and a `chord.commit.duration` histogram (ms) on the host's metrics, and a transaction that does not commit while its context is cancelled is reported to its owner (`CancelReporter`).

### Fixed

- Chord sync: the batch decoder scanned the whole unfinished line for a newline on every 64 KiB chunk (quadratic: 128 MiB took 9.4 s); it scans only the new bytes now. <!-- fix: 04e62295ce -->
