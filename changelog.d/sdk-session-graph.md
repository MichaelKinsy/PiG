### Added

- The Go, Python and Rust extension SDKs can construct Pi's `SessionManager` (`create`, `open`, `continueRecent`, `inMemory`, `forkFrom`, `findById`, `list`, `listAll` and every log method), `SettingsManager` (`create`, `inMemory`, all 134 getters and setters, `flush`, `reload`, `drainErrors`, project trust) and `ProjectTrustStore` with `hasTrustRequiringProjectResources`, as the Node runtime already could. The host owns each object as a handle on the extension's connection (`sessionGraph.*` host calls). A conformance row runs the same script in every SDK and compares it with Pi's own classes run by the Node runtime. <!-- fix: 3d07afe9db -->

### Changed

- Upgrading: the SDK type of `ctx.sessionManager` is now Pi's `ReadonlySessionManager`: Go `sdk.ReadonlySessionManager` (was `sdk.SessionManager`), Rust `ReadonlySessionManager<'a>` (was `SessionManager<'a>`), Python `ReadonlySessionManager` (was `SessionManager`). `SessionManager` is now Pi's class, which an extension constructs. Rename the type where you name it; calls through `ctx.SessionManager()` are unchanged. <!-- fix: 3d07afe9db -->
