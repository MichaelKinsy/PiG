### Fixed

- Bedrock requests no longer fail when `strict: "prefer"` tools exceed Claude's strict tool limits, such as more than 16 optional parameters across all strict tools. The request is sent once more with the preferred tools non-strict while `strict: "require"` tools stay strict, as the Anthropic provider already does, and the reply carries a `bedrock_strict_tools_fallback` diagnostic. Ports Pi's #11416. <!-- fix: d2dafd770b -->
