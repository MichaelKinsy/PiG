### Fixed

- A Go, Python or Rust extension tool that awaits a host call such as `exec` before its first dialog no longer holds the first dialogs of the later calls of its parallel batch: the SDK passes the call's dialog place at its first host call of any kind, so the later calls' dialogs open while the `exec` runs and the call's own dialog opens after it, as Pi's single thread does (D187). A handler that blocks on something other than a host call before its first host call still holds them. <!-- fix: dbe25ef5c1 -->
