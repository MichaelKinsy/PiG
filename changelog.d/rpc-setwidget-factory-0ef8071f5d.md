### Fixed

- RPC mode no longer runs `ctx.ui.setWidget` component factories or emits `setWidget` requests carrying their rendered lines. Like Pi's `rpc-mode.ts`, it forwards string arrays and clears only; print and JSON modes ignore a factory too. <!-- fix: 0ef8071f5d, 4e473be630, 7429bbf718, b17c37f1db, af5c9c6234, f276957b8c -->
