### Added

- A Provider object an extension registers (`pi.registerProvider(provider)` in TypeScript, `Provider.RefreshModelsAuth` in Go, `refresh_models_auth` in Python and Rust, `provider-refresh-models-auth` in Lisp) now carries Pi's `refreshModelsAuth`. With `"configured"`, a catalog refresh of that provider runs its network phase without a credential and no longer refreshes an expired OAuth login first, so a stale login (`invalid_grant`) no longer fails the catalog refresh.

### Fixed

- A Provider object registered from an extension process ignored `refreshModelsAuth`: its network refresh always resolved (and so refreshed) the stored credential. <!-- fix: 0ff9f4a443 -->
