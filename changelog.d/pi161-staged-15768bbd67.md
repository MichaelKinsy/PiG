### Added

- `chord/sync` exports `SyncProtocol` (`1`), matching Pi `SYNC_PROTOCOL`; PiG has no HTTP sync remote, so nothing sends the `pi-sync-protocol` header in this tree.
- `mcp` exports `CreateMcpResultSchema`, `ToMcpModelContent`, and `McpModelContentOptions` so callers outside the coding agent can declare a tool's full `CallToolResult` output schema (with local `$ref`s relocated under `structuredContent`) and convert MCP results to model-facing content.

### Changed

- `chord/nextnode/plugins` and `chord/nextnode/workers` now use tagged unions for `RolloutHost` and worker reload replies: `RolloutReloaded` (`"reloaded"`) and `RolloutFailed` (`"failed"`) replace `RolloutReported` and `RolloutErrored` (kept as deprecated aliases through 0.8.x alongside deprecated `RolloutHost.Stopped()`, scheduled for removal in 0.9.0), and worker reload failures send `FromWorkerReloadFailed` (`"reload_failed"`).
- `chord/nextnode/plugins` installs require an explicit `Kind` tag on `plugins.PluginSource` (`PluginSourceGit` or `PluginSourceNpm`; untagged or unknown kinds are refused); `(*store.ChordStore).CommitSeries` returns a tagged `store.SeriesResult` (`SeriesStored` | `SeriesRefused`), and `(*store.OpenQuery).Compiled` returns a tagged `store.Compiled` (`CompiledPlan` | `CompiledFailed`).
- `durable` and `durable/harness` now use tagged unions for `ToolPark` (flat struct with `ToolParkKind`: `ToolParkSleep` | `ToolParkWait`), `FoundSlot` (`FindSlot` returning `FoundSlotModel` | `FoundSlotNested`), `CompactionDecision` (`CompactionDecline` | `CompactionSummary`), `SignalState` / `ErasedSignalState` (`SignalStateValue` | `SignalStateExpired`), `Wrap` (`WrapKind()`), `YieldResult` (`YieldKind()`), and `CommitResult` (`CommitStored` | `CommitRefused`, constructed via `StoredCommit` / `RefusedCommit`), and rename transactional submission admission from `SubmitIn` to `Admit`.

### Fixed

- Extension `ctx.ui.pasteToEditor()` and `ctx.ui.setEditorText()` calls now marshal onto the interactive owner loop and request a coalesced render instead of mutating `tui.Editor` and painting synchronously from the subprocess host-call goroutine, eliminating a data race and ensuring edits repaint immediately. <!-- fix: c44cf255bb --> <!-- cause: ExtUIContext.PasteToEditor and ExtUIContext.SetEditorText mutated tui.Editor and called synchronous tuiInst.Render() on the caller goroutine instead of marshalling onto the owner loop and calling requestRender(). -->
- Retry classification in `ai`, `durable/core/turn`, and all four extension SDKs (Go, Python, Rust, Common Lisp) now retries Cloudflare HTTP 525, gateway mid-stream provider-unavailable and partial-response errors, Anthropic streaming `api_error` events without an HTTP status, OpenRouter `finish_reason: "error"`, empty-response and stopped-before-completing errors, "Repeat the request later", and queue admission ceiling errors, while non-temporary "provider unavailable" errors remain non-retryable. <!-- fix: a58db60a0b --> <!-- cause: retryable error patterns omitted Cloudflare 525, temporary gateway/provider-unavailable phrasing, and temporary admission/stream-error messages added in Pi v1.6.1. -->
- Bash tool output and extension SDK `TruncateTail` helpers now show the end of an over-long line followed by blank lines instead of returning `(no output)`, naming the partial line in the truncation notice. <!-- fix: 70905f5ee2 --> <!-- cause: truncateTail only fell back to a partial line when zero lines were collected and OutputAccumulator only fell back when clipped text was empty rather than blank, hiding an over-long line behind trailing blank lines. -->
- OpenAI Decisions (`gpt-6-luna` classifier) usage now reports `input_tokens_details.cached_tokens` and `cache_write_tokens` as `CacheRead` and `CacheWrite` excluded from `Input`, and prices them at the input rate in the catalog entry so cost totals remain unchanged. <!-- fix: 19519cce7c --> <!-- cause: ParseClassifierUsage ignored usage.input_tokens_details.cached_tokens and cache_write_tokens and the gpt-6-luna catalog entry priced cacheRead and cacheWrite at 0 instead of the input rate. -->
- MCP `CreateMcpResultSchema` treats any JSON-falsy `outputSchema` (`null`, `false`, `0`, `""`) as absent (`structuredContent: {}`), and MCP `resource_link` byte sizes format fractional and large `float64` values with JavaScript `toFixed(1)` semantics instead of truncating or overflowing `int`. <!-- fix: 15768bbd67 --> <!-- cause: CreateMcpResultSchema checked only byte length rather than JS truthiness and blockToContent converted float64 sizes to int before formatting. -->

### Deprecated

- `coding/mcpext.CreateMcpResultSchema` and `coding/mcpext.ToModelContent` are deprecated wrappers for `mcp.CreateMcpResultSchema` and `mcp.ToMcpModelContent` (scheduled for removal in 0.9.0).
- `chord/nextnode/plugins.RolloutReported`, `plugins.RolloutErrored`, and `RolloutHost.Stopped()` are deprecated aliases for `RolloutReloaded`, `RolloutFailed`, and `h.Kind == plugins.RolloutStopped` (scheduled for removal in 0.9.0).

### Removed

- `durable.ToolParkSleep` and `durable.ToolParkWait` nested structs (replaced by flat `durable.ToolPark` with `Kind`) and `SubmitIn` methods on `durable/harness` / `durable/core/driver` conversation handles (renamed to `Admit`).
