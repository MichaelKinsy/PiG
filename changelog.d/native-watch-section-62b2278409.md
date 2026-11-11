### Fixed

- Chord: a registry value's watch that ended through its context's cancellation could race with the host's close (a data race under `-race`); the watch now ends as a section of its host, like the rest of the code that touches the registry. <!-- fix: 62b2278409, e163ba898d -->
