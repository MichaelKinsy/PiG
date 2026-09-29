### Fixed

- Preserve runtime API-key overrides when credential deletion fails or is cancelled. Use the same context-aware deletion contract for file, in-memory, and read-only credential stores.
- Serialize same-provider Model Runtime login and logout through local synchronization. Report committed credential changes separately from synchronization failures, including availability errors and cancellation.
- Apply startup Session names before model validation and expose initial names in RPC. Use header-only exact-ID lookup, keep metadata-only Sessions in memory, preserve requested fork IDs, and reject existing fork targets.
- Keep the active Session visible in the picker and reject its deletion through symlink aliases. Clear confirmation before invoking deletion, and order threaded Sessions by the newest millisecond activity across each subtree.
- Share model-auth resolution between the Go and extension facades. Carry credential-scoped environment and authentication headers through each provider API without resolving command-backed credentials twice.
- Preserve custom cancellation reasons when the runtime credential overlay rejects an aborted operation.
- Allow queued file-backed credential mutations to cancel without waiting for another active callback on the same store.
- Expose raw automatic theme settings through `SettingsManager.GetThemeSetting` and keep fixed-theme lookup separate.
