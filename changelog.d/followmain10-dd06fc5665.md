### Added

- chord: `Store.OnCommitted(listener, onError)` announces the new `LastSeq` after commits are durable and adopted (a commit, a group, a series, an applied batch), on a goroutine of its own, once for however many commits it covers, one call at a time and in order; a listener that panics is removed and its panic handed to `onError` (Pi 6268cc5cd).

### Fixed

- chord: `Plugins.Build` discovers again when a named plugin is unknown to discovery or a file discovery found for it is gone, so a plugin deleted (or written) just before a build is removed (or added) even when the watcher's events lag (Pi 822b37a27). <!-- fix: dd06fc5665 -->
- chord tutorial: chapter VI waits for the app's base code to be watched before it adds the Harbor plugin, so file events dropped while a watch is added no longer leave it waiting (Pi f2bd84a51). <!-- fix: dd06fc5665 -->
