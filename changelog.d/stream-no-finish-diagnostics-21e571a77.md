### Added

- Failed OpenAI-compatible completions streams retain bounded, redacted missing-`finish_reason` evidence in assistant-message diagnostics, including request identity, DONE presence and timing; error text and successful-stream behavior are unchanged (proposed D237).
