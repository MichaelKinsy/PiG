### Fixed

- A terminal write that fails for a reason other than a lost terminal (not `EIO`, `EPIPE`, `ENOTCONN` or `ENOTTY`) now restores the terminal, records a crash and exits 1 as Pi's uncaught-exception handler does, instead of being dropped (#397). <!-- fix: f2aed24c0d -->
- An extension's `ctx.ui.setTitle` replaces control characters in the title with spaces, as Pi's terminal title does, so a BEL or ESC in it no longer ends the sequence early and sends the rest to the terminal as escapes (#397). <!-- fix: f2aed24c0d -->
