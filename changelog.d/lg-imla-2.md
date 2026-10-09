### Added
- `extension.API.OnCacheWarmingDecision` registers a typed `cache_warming_decision` handler (Pi's `ExtensionHandler<CacheWarmingDecisionEvent, CacheWarmingDecisionEventResult>`); the known-gap entries for it are gone.

### Changed
- `extension.API.OnTurnEnd` handlers return a `TurnEndEventResult` (an alias of `BoundaryResult`), so an in-process `turn_end` handler can propose boundary entries and a continuation as Pi's `ExtensionHandler<TurnEndEvent, TurnEndEventResult>` does.
- `extension.ProjectTrustContext` (cwd, mode, hasUI, and the select, confirm, input and notify of the UI) and `extension.ProjectTrustHandler` are Pi's types. `inproc.EmitProjectTrust` passes the context to every `project_trust` handler, and `API.OnProjectTrust` takes a `ProjectTrustHandler`. Outside an interactive run its UI never prompts and `notify` prints the message to stderr, colored as chalk would (`cli/project-trust.ts`).
- `ai.ConvertResponsesMessages`, `ai.ConvertResponsesTools` and their option structs are Pi's `openai-responses-shared` conversions as package-level functions; the Responses, Codex and Azure providers call the same code. The wire types are exported as `ResponsesInputItem`, `ResponsesTool` and `ResponsesToolFormat`.

### Fixed
- A Radius gateway model's `samplingParamsByThinkingLevel` now reaches the `pi-messages` model and the composed catalog entry (Pi's `getRadiusModelsFromConfig` spreads each gateway model); it was dropped before.
- `client.Client.Disconnect` takes the reason string as Pi's `Client.disconnect(reason)` does (it was an `error`); the connection fails with a `DisconnectedError` carrying it.
- OpenAI Responses prices a response that reports no `service_tier` at the requested tier, as Pi's `processResponsesStream` does (`response.service_tier ?? options.serviceTier`); only Codex did before, so a flex or priority request to OpenAI Responses was billed at the default rate.
