### Fixed

- The OpenAI Codex provider (`openai-codex-responses`) now gets prompt cache hits for the system prompt on GPT-6.2 models instead of invalidating the cache on every turn: the system prompt is sent as the leading `developer` input item and top-level `instructions` is omitted (`ConvertResponsesMessagesOptions.IncludeSystemPrompt` is removed). <!-- fix: 9486493968 --> <!-- cause: Codex placed the system prompt in top-level instructions rather than as the leading developer input item, and GPT-6.2 appends instructions after input so every turn changed the prefix before it. -->
