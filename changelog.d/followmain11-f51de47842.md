### Fixed

- ai: a non-strict Anthropic tool schema keeps its root `additionalProperties` (false, true or a schema), so Claude is told whether extra arguments are allowed; root `anyOf`, `oneOf` and `allOf` stay out (Pi aa47cedce, #12100). <!-- fix: f51de47842 -->
