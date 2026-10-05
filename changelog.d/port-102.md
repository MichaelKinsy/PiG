### Changed

- PiG follows Pi 1.0.2. `pig --version` prints `<PiG release>+1.0.2`.
- The model catalog follows Pi 1.0.2: NVIDIA Nemotron 3 Super, OpenRouter's Cloudflare Clef and Perplexity decider classifiers, and Llama 3.3 70B prices.
- llama.cpp models whose chat template supports thinking offer the off, minimal, low, medium and high thinking levels, and each level sends its own `thinking_budget_tokens`. Pi offers only off and medium (D90). See `docs/llama-cpp.md`.
- In fullscreen mode, `/arminsayshi` and `/pigsayhi` play Pi's 3D easter egg with the sprite's pig head in place of Armin: the screen dissolves, the pig grows out of the center, spins and slides its blocks around as a puzzle. Outside fullscreen mode they draw the inline pig head as before (D87).

### Added

- `samplingParamsByThinkingLevel` for custom models and `modelOverrides` in `models.json`: sampling parameters for each thinking level, merged between the model's `samplingParams` and the request's. OpenAI Completions, OpenAI Responses and Azure OpenAI Responses requests apply the entry for the request's effective thinking level. See `docs/models.md`.
- Durable conversations persist a provider session ID (`pi.provider` document) and send it with every generation and compaction request, for prompt caching and session affinity. Conversations created before this release get one before their next request.

### Fixed

- In interactive mode, an extension's `model_select` and `thinking_level_select` handlers see the newly selected model in `ctx.model` and in the subprocess SDKs' model state, so a custom footer no longer shows the previous model (#128).
- In interactive mode, an extension's model stream request without `reasoning` no longer thinks at the thinking level the session had when extensions loaded; it streams without thinking, as in Pi and in PiG's other modes.
