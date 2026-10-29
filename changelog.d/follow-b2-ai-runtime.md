### Fixed

- A context-overflow error from a LiteLLM proxy's pre-call check (`litellm.ContextWindowExceededError ... Context Window exceeded for given call`) is now recognised as context overflow, so compaction recovery runs instead of the turn failing (Pi #11492).
- A Zhipu/Z.ai usage-limit error (error code 1308, sent as HTTP 429) is no longer retried: the plan's limit resets after hours, not seconds (Pi #11537).
