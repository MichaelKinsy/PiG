### Changed

- A message kept from an OpenAI-compatible completions stream no longer holds the line buffers its response id, finish reason and tool call ids and names were read from: the message keeps its own copy of those four short strings. In the audit's 20-prompt session that was 280 buffers, 61 KB of the memory in use at rest.
