### Added

- Chord host (internal): a consumer that is not a link can take a Go host's event flushes as a link's provider side sends them: one message per flush with the entries in order, under the host's event window (32 unacknowledged messages by default, the next flush's events dropped and counted `chord.events.dropped{reason=window}`), acknowledged by count. It is the Go host's part of carrying events across the plugins bridge; nothing is added to the public API of `chord/next/host`, and no type with an event member crosses the bridge yet.
