### Fixed

- GitHub Copilot models on the Responses API (GPT, Grok, MAI-Code) no longer fill omitted optional tool arguments with empty strings or invented values. Their tools were sent without `strict`, which Copilot treats as strict mode; they now send `strict: false`, or `strict: true` for tools with JSON-schema constrained sampling. Affects 0.2.0 through 0.7.0. <!-- fix: 81f580b72d -->
