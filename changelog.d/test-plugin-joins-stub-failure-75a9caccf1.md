### Fixed

- `TestPlugin` reports a Go stub's setup failure and the tested plugin's own failure together in one `testPlugin: setup failed: ...` message, the stub's first, as Pi's one `host.load` does; it returned the stub's alone and never loaded the plugin. <!-- fix: 75a9caccf1 --> <!-- cause: the stubs were loaded on a Go host of their own first and its failure returned at once, so the plugin's own failure was never collected. -->
