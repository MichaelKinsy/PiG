### Fixed

- Azure OpenAI models now keep OpenAI's long-context price tier, so requests above 272K input tokens are costed at Azure's long-context rates (Pi #11948). <!-- fix: 9624fc45422bb2d12b1be3f4ff1d8206e558d682 -->
- Anthropic and Bedrock no longer send `strict: "prefer"` tools strict again on every request after Claude rejected them for exceeding its strict tool limits, which cost a rejected request (400) per turn. Later requests with the same session id now send those tools non-strict right away, until `CleanupSessionResources(sessionID)` (Pi #11626). <!-- fix: 9624fc45422bb2d12b1be3f4ff1d8206e558d682 -->
