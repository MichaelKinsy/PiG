### Fixed

- A plugin view is sent only to a client that draws every node kind it holds: the registry now reads the client's `nodes` list when it decides what a client is sent. A view with a kind outside the list does not fit that client (code `unsupported` when no attached client draws it) and a client the content does not fit is sent the fallback, as for a limit; an empty or absent `nodes` list draws no kind. <!-- fix: 9e348106ed -->
