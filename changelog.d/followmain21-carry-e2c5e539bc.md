### Changed

- Chord hosts that run Pi's TypeScript (a plugin's Node subprocess, a page, the terminal client) follow Pi 6268cc5cd and 7ae9ff8c7: a plugin of the Node host that opens a store of its own (`openStore` of the Chord API) has `store.onCommitted(listener, onError)`, which announces the last sequence number once the commits it covers are durable; and the vendored telemetry ingest facet takes `admit(principal)`, as the Go facet does. No shipped page or terminal bundle changes.
