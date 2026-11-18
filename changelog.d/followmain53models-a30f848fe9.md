### Added

- Follows Pi e56d35ce5ece: GPT-6.2 Astra (`gpt-6.2-astra`) and GPT-6.2 Sol (`gpt-6.2-sol`) join the `openai`, `azure` and `openai-codex` providers, with Pi's official pricing (Astra: $8 input, $0.40 cached input, $10 cache writes, $40 output; Sol: $2, $0.10, $2.50, $10; long-context tier above 272k), tool search, additional tools and mid-conversation effort changes. Both reject reasoning effort `none` and `minimal`, so `off` is unavailable for them. Azure serves them with a 1.05M context window; Codex uses its 272k default.

### Changed

- Follows Pi e56d35ce5ece: the default model for the `openai`, `azure` and `openai-codex` providers is now GPT-6.2 Sol (`gpt-6.2-sol`) instead of GPT-6.1 Sol, at the same price.
