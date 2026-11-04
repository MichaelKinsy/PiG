### Fixed

- RPC mode now acts on an extension's `ctx.shutdown()` while idle, for example from a timer, instead of waiting for the next command. A request made during startup (for example from `session_start`) is handled once input is attached, and a shutdown requested by an extension command waits until the command's `prompt` response is written. Pi 1.5.0 behaves as before; PiG follows Pi main 1708b4788 (Pi #11599, D231). <!-- fix: 48a903ebee -->
