### Fixed

- A Node extension with a `prepareLoadout` tool no longer hangs PiG when it, or another extension in the same process, registers a tool after loading (in a `session_start` handler, as pi-extensible-workflows does) or calls `setActiveTools` and then a synchronous API such as `getActiveTools`. The hook now runs inside that call, as in Pi. <!-- fix: f9b8dbada8 -->
