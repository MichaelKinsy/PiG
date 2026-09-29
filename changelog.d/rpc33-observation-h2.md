### Fixed

- Keep provider scratch fields out of compact assistant-message frames and their reduced results while preserving independent snapshots of tool arguments.
- Preserve OpenAI Completions and Responses tool scratch fields while streaming, and remove them when tools finish or requests fail. Keep partial tool arguments current across initial Responses arguments and final replacements. Apply Completions stop state in the chunk that reports it.
- Emit OpenAI stream start after successful response setup and before waiting for body data. Preserve grammar-tool input and namespace finalization, including empty-input errors, without adding tool-end events on failed requests.
