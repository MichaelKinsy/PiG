### Fixed

- `ctx.compact` answers `onComplete` with the compaction result's members in Pi's order (`summary`, `firstKeptEntryId`, `tokensBefore`, `estimatedTokensAfter`, `usage`, `details`); they were written alphabetically.

### Added

- `tools.CreateReadToolDefinition`, `CreatePowerShellToolDefinition`, `CreateEditToolDefinition`, `CreateWriteToolDefinition`, `CreateGrepToolDefinition`, `CreateFindToolDefinition` and `CreateLsToolDefinition` return the built-in tools as extension `ToolDefinition`s, as Pi's `createXToolDefinition` do.

### Changed

- `agent.ConvertToLLM(messages)` is Pi's `convertToLlm`; the provider normalization (dropping errored assistant messages, filling orphaned tool calls) is `agent.ConvertToLLMForModel(messages, model)`.
- A `before_agent_start` handler's edit to `systemPromptOptions` (a section, the selected tools) is in the system prompt the next handler reads from `event.systemPrompt` and `ctx.getSystemPrompt()`; the prompt is rendered from the shared options each time, as Pi does.
- `imageprocessing.ResizedImage.Data` is the image's base64 text, as Pi's `ResizedImage.data`; it was the raw bytes.
