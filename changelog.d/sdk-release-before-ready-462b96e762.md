### Fixed

- A Python or Rust extension no longer fails to load when the host releases one of its listeners between registering it and marking it ready (`expected ready, got notify`). A listener that unsubscribes itself while the host dispatches to it during the load is released by an `events.release` notice once that dispatch ends, which can come before `ready`; the Go SDK already served it, and the Python and Rust SDKs now do the same. <!-- fix: 462b96e762 -->
