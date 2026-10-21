### Fixed

- Extension API parity ledger: removed the stale `ctx.ui.custom(options.onHandle)` exception (Go, Rust and Python implement the overlay handle; `TestCustomOverlayHandleSDKsMatch` runs it on every SDK), reworded the keybinding-hint, highlight and `createAgentSession` rows that said "Node runtime only", and added a "Native package-export gaps" table naming the closing lane for every export Node serves and the native SDKs lack. <!-- fix: 1e8ec257e1 -->
