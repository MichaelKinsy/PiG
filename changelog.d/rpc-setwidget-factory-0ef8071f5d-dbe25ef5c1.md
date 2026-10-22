### Fixed

- RPC mode no longer runs `ctx.ui.setWidget` component factories or emits `setWidget` requests carrying their rendered lines. Like Pi's `rpc-mode.ts`, it forwards string arrays and clears only; print and JSON modes ignore a factory too. <!-- fix: dbe25ef5c1 -->
