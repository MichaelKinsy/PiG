### Changed

- PiG follows Pi 1.0.3. `pig --version` prints `<PiG release>+1.0.3`.
- **Breaking:** the Azure provider is `azure`, not `azure-openai-responses`, because it now serves Chat Completions as well as the Responses API. The `azure-openai-responses` API ID and the `AZURE_OPENAI_*` variables are unchanged. PiG does not migrate old entries, as Pi does not. Rename the provider key in `auth.json` (or run `/login` again), in `models.json`, and in `settings.json` (`defaultProvider`, `enabledModels` patterns and `modelThinkingLevels` keys). A session that used the old provider falls back to another model when resumed, and its prompt cache is not reused. See `docs/providers.md`.
- The model catalog follows Pi 1.0.3: the `azure` provider, Azure Foundry DeepSeek V4 Pro, Bedrock Claude Sonnet 5.5 regions, and price updates.

### Added

- Azure Foundry Chat Completions: `azure/deepseek-v4-pro` is built in, and other Foundry models can be added under the `azure` provider with `"api": "openai-completions"` and a `"baseUrl"`. `AZURE_OPENAI_DEPLOYMENT_NAME_MAP` applies to both Azure APIs.

### Fixed

- A subscription login such as Sign in with ChatGPT no longer fails with `refresh_token_invalidated` after a request is cancelled during an OAuth token refresh. The caller's cancellation now stops only the wait for the credential lock; a refresh that has started finishes and saves the rotated token, bounded by its 15 second timeout. An extension's OAuth refresh signal is no longer cancelled with the request.
- A foreign tool call replayed to an OpenAI Responses provider outside OpenAI, Codex and OpenCode keeps only the characters the API accepts in its ID, as Pi does.
