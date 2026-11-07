### Fixed

- RPC mode: a Go, Rust, Python or Lisp extension's `agent_settled` or `agent_before_settle` handler, command or input handler that waits on the network or another blocking call no longer holds every later RPC command, `get_state` included, until it returns. The host takes such a handler to be waiting once it has run for 250 ms, as Pi's event loop serves stdin while a handler awaits; handlers that answer sooner keep their order. <!-- fix: a94c58f2279d7231792d6a98e8900260ab165e5b -->
