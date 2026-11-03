### Fixed

- A tool schema built with `ai.StringEnum`, and the schemas of the eval harness's custom tools, reach every provider with their members in the order the author wrote them, as Pi sends them: `{"type":"string","enum":[...]}` and properties in declaration order, where PiG sorted them by name for the Anthropic, OpenAI (completions and responses), Azure, Mistral, Google and Bedrock requests. `ai.StringEnum` returns an ordered schema object and shares the caller's enum slice, as Pi does. <!-- fix: 5d4c43241b -->
