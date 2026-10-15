### Upgrading

- Go API: `OutputBuffer`, `Progress`, `BoundOutput`, `SanitizeOutput`, `CharacterEnd`, `OutputLimits`, `OutputSlice`, `BoundedOutput` and their helpers move from `durable/harness` to the new package `chord/output`, as Pi 4a5d3729 moves them to `@earendil-works/chord/output`. Before: `harness.NewOutputBuffer(limits)`. After: `output.NewOutputBuffer(limits)`. `PushStringSkipping` and `PushBytesSkipping` take `output.OutputSkip`, which has the fields of `env.ShellOutputSkip`: `output.OutputSkip(skipped)` converts. `Progress`'s clock seam is now exported (`ProgressClock`, `NewProgressWithClock`).

### Added

- `chord/output`: the Go counterpart of `@earendil-works/chord/output`, with `OutputSkip` and `Utf8ByteLength`. `durable.Utf8ByteLength` calls it.

### Changed

- The `kimi-coding` provider uses Anthropic Messages at `https://api.kimi.com/coding` again. Pi reverts its switch to Chat Completions because the model catalog reaches released clients whose provider cannot use it.

### Fixed

- A call through a remote Chord reference with a `ctx` from another host, or one not made by Chord, fails with `invalid/foreign_ctx` before anything crosses the link, like a call through a local reference.
- Turning thinking off on Claude Haiku 5.5 and Opus 5 sends plain `thinking: {type: "disabled"}` without effort markers, which Anthropic rejects while thinking is disabled.
- The retry classifier treats "The service is temporarily unavailable" and Bedrock's "Service unavailable: ..." as retryable.
- A `Progress` pause longer than Node's timer maximum, or not a number, runs after 1 ms as a Node timer does.
