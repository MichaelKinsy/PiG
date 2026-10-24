### Added

- `ai.UsageCost.TotalFromProvider` (`usage.cost.totalFromProvider` on the wire) records whether `cost.total` is the billed cost the provider reported or an estimate from the model's catalog rates. Catalog-priced usage writes `false`, an absent value in a recorded session also means `false`, and a combined usage is provider-reported only when both parts are. Ports Pi's `Usage.cost.totalFromProvider`.

### Fixed

- OpenRouter turns (the built-in provider and custom providers on OpenRouter's endpoint) now take OpenRouter's reported total as `usage.cost.total` for OpenAI Completions and Anthropic Messages streams, plus the upstream inference cost for a BYOK request, instead of the catalog estimate, and mark it provider-reported. The export HTML header uses the recorded total. Replaces PiG's earlier D124 form of this fix with Pi's. <!-- fix: a9015090a5 -->
- An Anthropic request holds a mid-conversation system update until an assistant message that is actually sent, so an assistant message that converts to no blocks no longer moves the update before a user message. <!-- fix: e5a345eeba -->
