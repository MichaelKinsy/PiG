### Fixed

- A Go extension that reorders, filters or sorts an array of an event in place (the content blocks of a message, a provider request's `tools`, a list of messages or entries) and hands it back no longer re-sorts the keys of the moved objects, so tool-call arguments and tool schemas reach the session and the provider in the order the host sent them. `Context.ObjectKeys` lists integer-like keys first and ascending, as `Object.keys` does. <!-- fix: 3de4b8e877c8fc4d3b7bedafa5e8ae8cbad7161f -->
