### Added

- `chord/nextnode/plugins`: the plugins' Node subprocess takes a store (`StoreOptions`), a spare process (`Warm`, `KeepWarm`) and a stop grace (`StopGrace`, 5 s), reports how it ended (`NodeExit` `Code`, `Signal`, `Reason`), and `host.DeliveryAwaiter` and `link.Channel.Idle` let a link to another process wait for the host that took it (D223).

### Fixed

- `chord/nextnode/plugins`: the references and watches held for the bridged proxies are released when the Node process ends; a `Close` past its deadline still stops a starting spare; a process running its host's close is not killed before the host's `closeTimeout`. <!-- fix: 3b9bd165af -->
- `chord/nextnode/plugins`: finding Node stops a version probe that does not answer (10 s) and ends at `ReadyTimeout`; fnm's default directories (including `~/Library/Application Support/fnm` on macOS and `%APPDATA%\fnm` on Windows), and Volta's and mise's installs, are searched, and their shims are never run (D221). <!-- fix: 3b9bd165af -->
- `chord/next/link`: `Channel.Idle` returns when the channel closes. <!-- fix: 3b9bd165af -->
