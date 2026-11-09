### Changed

- Chord sync takes Pi's shapes: `LocalCopy` is a tagged struct (`Kind`), `FlushSchedulerOptions` times are milliseconds (`IntervalMs`, `UrgentMs`, `RetryMs`, `MaxRetryMs`, `SetTimer(callback, delayMs)`, `Now() int64`), `SyncFileSystem.OpenBinaryReader` takes options, `EncodeLogLine` returns the line as a string, and `sync.JsonObject` and `sync.NewLogFormatError` exist.
