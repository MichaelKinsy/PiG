### Added

- A `user_bash` handler in any SDK can return Pi's `{ operations }` result. The `BashOperations` object stays in the extension, and the host runs the user's command through its `exec` with `onData`, `signal`, `timeout` and `env`, so an SSH-style extension runs `!` commands remotely. Node returns Pi's object as is, Go returns `sdk.BashOperations`, Python returns an object with an `exec` method, and Rust returns `ctx.bash_operations(...)`.

- `extension.API.RegisterNativeProvider` is the `registerProvider(provider)` overload and takes Pi's `Provider` (`ai.ModelsProvider`).

### Fixed

- `thinking_level_select` and `turn_start` handlers are covered through the production Session: a handler sees the clamped effective level only when it changes, and `turn_start` carries the per-run turn index and the time of the turn.

- A scripted faux response can carry `DurationMs`. It stays on every partial and the final message, as in Pi, and the stream measures a duration only when none is scripted.

- The experimental client takes Pi's `ByteTransportFactory` shape: `ClientOptions.TransportFactory` returns a `ByteTransport` whose `Send(ctx, chunk)` returns when the write settles. The unix transport uses it, and a transport that admits sends itself (`Submit`) is handed each frame before `Connection.Send` returns.

- Changing the thinking level in interactive mode (cycle key, `/thinking`, the settings selector) goes through the Session like Pi's `session.setThinkingLevel`: the level is clamped to the model, the `thinking_level_select` event carries the clamped level, and a handler that is slow to start no longer holds the input loop.
