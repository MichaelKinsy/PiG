### Fixed

- Chord store: `Close` now waits for the `OnCommitted` announcement of the last commit before it closes the backend. A listener could be called after `Close` had returned, or not have been called yet when it returned. <!-- fix: 19f9ce245e -->
