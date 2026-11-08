### Fixed

- A model request that is cancelled while its setup runs, for example the request after an abort during a tool call, ends as aborted instead of as an error. <!-- fix: 563f896e3f -->
