### Fixed

- Stored assistant messages of a durable conversation now carry `thinkingLevel`: a generation records the thinking level its request asked for on the response, also after polling a deferred one. A poll checkpoint written by an earlier version has no level and records none. <!-- fix: d7fbf0046b -->
