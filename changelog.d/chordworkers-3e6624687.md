### Added

- Chord: a worker's state carries its `incarnation` (the run of the worker's host) from its link's handshake on, before its first call is handled. A route's `resolve` is told the peer's incarnation through its `opened` callback at the handshake, and the store gains `Settled`, which is ready once the commits queued before the call were adopted or rejected. Port of Pi f57cd9484.
- Chord: `LiveIn` returns a reference narrowed to the variant it is live in, and `TuiScope.Variant` reads one in a scope and follows a change of its variant (the React client's `useVariant` is vendored). Port of Pi f57cd9484.

### Fixed

- Chord: a worker's exit is published after its link closed and after the commits its calls queued in the server's store settled, and a stop resolves only once the exit was published; the app gives its workers the server's store for this. Before, an exit could be published while the commits of the worker's last reports were still queued, and a worker that reported and exited in one turn could have its reports committed after its exit. A worker process closes its link at its exit, before the exit is told, as neither a lost nor a closed link. <!-- fix: 3e66246870f63df797347f38a1280cc929724b74 --> <!-- fix: d3f63d62e9f6b41b4f72d0a368e73930d517c317 -->
