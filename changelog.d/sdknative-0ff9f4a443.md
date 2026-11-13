### Added

- A Provider object an extension registers (`pi.registerProvider(provider)` in TypeScript, `Provider.RefreshModelsAuth` in Go, `refresh_models_auth` in Python and Rust, `provider-refresh-models-auth` in Lisp) now carries Pi's `refreshModelsAuth`. With `"configured"`, a catalog refresh of that provider runs its network phase without a credential and no longer refreshes an expired OAuth login first, so a stale login (`invalid_grant`) no longer fails the catalog refresh. An extension that reads another extension's Provider (`getRegisteredNativeProvider`) sees its `refreshModelsAuth` in every SDK and in Node.

### Fixed

- A Provider object registered from an extension process ignored `refreshModelsAuth`: its network refresh always resolved (and so refreshed) the stored credential. <!-- fix: 0ff9f4a443 -->

### Changed

- Rust: `Provider` has the new public field `refresh_models_auth: Option<String>`, so a `Provider { .. }` struct literal must set it (`None` keeps Pi's default, `"credential"`). `Provider` has no `Default` (its `get_models`, `stream` and `stream_simple` have no meaningful default), so an extension that wants to avoid listing every optional field builds the value with `create_provider`, which sets it to `None`. <!-- feat: sdknative -->
