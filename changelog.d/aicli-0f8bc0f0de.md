### Fixed

- `ctx.reload()` in print and RPC mode, and `Session.Reload` for SDK callers, now fail with `Wait for the current response to finish before reloading.` while a response is running, and with `Wait for compaction to finish before reloading.` during compaction or tree navigation, instead of shutting the extensions down under a running tool, which then failed with a stale-context error. A refused reload leaves the extensions and the running tool's context untouched, and the extension sees the error in every SDK. <!-- fix: 0f8bc0f0de -->
