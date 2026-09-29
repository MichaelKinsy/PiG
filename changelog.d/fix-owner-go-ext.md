### Changed

- Go SDK migration: `ContextUsage.Tokens` and `Percent` retain their nullable `*int` and `*float64` types. Unknown usage after compaction is not zero. Check for nil before dereferencing, or explicitly choose `usage.TokensOr(0)` / `usage.PercentOr(0)` after checking `usage != nil`. Replace `SendMessageOptions{TriggerTurn: true}` with `SendMessageOptions{TriggerTurn: sdk.Bool(true)}`. Use `sdk.Bool(false)` for explicit false and nil for Pi's default. See `extensions/sdk/README.md` for before/after examples and the distinction between absent usage, unknown counts, and known zero.

### Fixed

- Build legacy Go standalone extensions against the running binary's complete SDK module, including subpackages such as `sdk/json`, without changing authored files.
- Preserve registration identity for duplicate extension folders in native cells. Separate Go copies with the same module path so each runs its own code. Report capability conflicts after loading instead of rejecting the second copy's registration.
- Restrict native extension build notices to interactive mode. Print, JSON, and RPC remain silent during compilation even when stderr is a terminal.
