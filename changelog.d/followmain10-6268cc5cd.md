### Added

- chord: `Store.OnCommitted(listener, onError)` announces the new `LastSeq` after commits are durable and adopted (a commit, a group, a series, an applied batch), on a goroutine of its own, once for however many commits it covers, one call at a time and in order; a listener that panics is removed and its panic handed to `onError` (Pi 6268cc5cd).

### Fixed

<<<<<<<< HEAD:changelog.d/followmain10-6268cc5cd.md
- chord: `Plugins.Build` discovers again when a named plugin is unknown to discovery or a file discovery found for it is gone, so a plugin deleted (or written) just before a build is removed (or added) even when the watcher's events lag (Pi 822b37a27).
|||||||| parent of a12f925100 (durable codemode: changelog fragment names and fix markers, gap baseline, go fix (Pi 5e24c6b1f)):changelog.d/followmain9-822b37a27.md
- chord: `Plugins.Build` discovers again when a named plugin is unknown to discovery or a file discovery found for it is gone, so a plugin deleted (or written) just before a build is removed (or added) even when the watcher's events lag (Pi 822b37a27).
- chord tutorial: chapter VI waits for the app's base code to be watched before it adds the Harbor plugin, so file events dropped while a watch is added no longer leave it waiting (Pi f2bd84a51).
========
- chord: `Plugins.Build` discovers again when a named plugin is unknown to discovery or a file discovery found for it is gone, so a plugin deleted (or written) just before a build is removed (or added) even when the watcher's events lag (Pi 822b37a27). <!-- fix: 75b1d96226 -->
- chord tutorial: chapter VI waits for the app's base code to be watched before it adds the Harbor plugin, so file events dropped while a watch is added no longer leave it waiting (Pi f2bd84a51). <!-- fix: 75b1d96226 -->
>>>>>>>> a12f925100 (durable codemode: changelog fragment names and fix markers, gap baseline, go fix (Pi 5e24c6b1f)):changelog.d/followmain9-75b1d96226.md
