### Added

- Qwen Token Plan Individual (China) provider `qwen-token-plan-individual-cn`, using `QWEN_TOKEN_PLAN_CN_API_KEY`, with the Individual model list on the China endpoint, and `glm-5.3` on `qwen-token-plan-individual`. <!-- feat: c3a48aa340 -->
- GitHub Copilot lists the `microsoft-decision-1` classifier model, which classifies through the account's `/v1/systemone` endpoint. <!-- feat: c3a48aa340 -->
- `compat.requiresNonNullAssistantContent` sends `""` instead of `null` content for replayed assistant messages that carry no text. <!-- feat: 73e1cef071 -->
- `compat.promptCacheKeyMode` (`auto`, `enabled`, `disabled`) controls the `prompt_cache_key` request field on `openai-completions` and `openai-responses`; `enabled` sends it through compatible proxies whenever caching is active. <!-- feat: 7dd0dc1be1 -->
- `compat.requiresToolsWithToolHistory` restores `"tools": []` on `openai-completions` for proxies that need it. <!-- feat: 7dd0dc1be1 -->

### Changed

- `openai-completions` requests with no tools no longer send `"tools": []` when the conversation contains tool calls or tool results; some servers (vLLM 0.20+) rejected the empty array. Set `compat.requiresToolsWithToolHistory` for a proxy that needs it. <!-- fix: 7dd0dc1be1 -->
- The OpenAI Codex provider is named `OpenAI Codex`, without the `(legacy)` marker. <!-- fix: c3a48aa340 -->
- Azure's GPT-6 models (`gpt-6-astra`, `gpt-6-luna`, `gpt-6-sol`, `gpt-6.1-sol`, `gpt-6.1-terra`) use Azure Foundry's 1,050,000-token context window. <!-- fix: c3a48aa340 -->

### Fixed

- A connection failure with no status or body keeps its cause in the error message, for example `Connection error.: connect ECONNREFUSED 127.0.0.1:8080` on `openai-completions`, `openai-responses` and `anthropic-messages` (`fetch failed: connect ECONNREFUSED ...` elsewhere), instead of only the generic text. <!-- fix: 2d3121b71c -->
- A gateway's `cut short after the reply had begun` stream error is retried like other dropped streams. <!-- fix: 2d3121b71c -->
- A response stream dropped mid-run reports `terminated: other side closed (UND_ERR_SOCKET)` on `openai-completions`, `openai-responses`, `google` and `anthropic-messages`, as Pi does, instead of `terminated (cause: ...)`. <!-- fix: eb74fe4dc6 -->
- Requests through the built-in HTTP client refuse the ports the Fetch standard blocks (for example 1, 25, 6000) with `fetch failed: bad port`, as Node's fetch does. <!-- fix: eb74fe4dc6 -->
