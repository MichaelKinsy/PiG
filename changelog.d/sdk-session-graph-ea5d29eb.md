### Added

- The Go, Python and Rust extension SDKs can construct Pi's `SessionManager` (`create`, `open`, `continueRecent`, `inMemory`, `forkFrom`, `findById`, `list`, `listAll` and every log method), `SettingsManager` (`create`, `inMemory`, all 134 getters and setters, `flush`, `reload`, `drainErrors`, project trust) and `ProjectTrustStore` with `hasTrustRequiringProjectResources`, as the Node runtime already could. The host owns each object as a handle on the extension's connection (`sessionGraph.*` host calls). A conformance row runs the same script in every SDK and compares it with Pi's own classes run by the Node runtime.

### Changed

- Upgrading: the SDK type of `ctx.sessionManager` is now Pi's `ReadonlySessionManager`: Go `sdk.ReadonlySessionManager` (was `sdk.SessionManager`), Rust `ReadonlySessionManager<'a>` (was `SessionManager<'a>`), Python `ReadonlySessionManager` (was `SessionManager`). `SessionManager` is now Pi's class, which an extension constructs. Rename the type where you name it; calls through `ctx.SessionManager()` are unchanged. The `SetupSessionManager.SessionManager` field of the Go SDK is removed with the old type; the upgrade rule `context-session-manager-readonly` names both changes.
- The Go, Python and Rust extension SDKs can construct Pi's `ModelRuntime` (`ModelRuntime.create`, catalog and auth reads, runtime keys, `registerProvider` without functions, virtual models, `refresh`, availability, `stream`/`complete`, `classify`, `generateImages`, `login`, `logout`) and `SettingsManager.fromStorage` (a storage object of the extension, called back on every read and write). Provider objects (`getProviders`, `getProvider`, `registerNativeProvider`) and the deferred calls stay out of the runtime until the owner decides (D78).
- Upgrading: `SessionManager.list` and `listAll` take an `onProgress` argument in the Go (`SessionManagerList(c, cwd, dir, onProgress)`, `SessionManagerListAll(c, dir, onProgress)`; pass nil), Rust (`list(ctx, cwd, dir, on_progress)`, `list_all(ctx, dir, on_progress)`; pass `None`) and Python (`on_progress=None`, optional) SDKs.

### Fixed

- Removing a local package reports `start` and `complete` `remove` progress events, as Pi's `remove` does for every source kind. <!-- fix: aa9ff5b76e -->
- `registerProvider` of a model without an `api` fails with Pi's `Set it at model level or provider level.` wording. <!-- fix: 215bbf6c9b -->
- An extension's `SessionManager.appendContextEdit(id, null)` clears the content instead of failing, and `appendCompaction` without `fromHook` writes no `fromHook` member. <!-- fix: aa9ff5b76e -->
