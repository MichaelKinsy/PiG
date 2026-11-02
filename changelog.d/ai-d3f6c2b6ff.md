### Fixed

- Anthropic non-strict tool schemas keep the root `$defs` and `definitions`, so property `$ref`s resolve. They were dropped, which left the `$ref`s pointing at nothing, made Claude produce wrong arguments for such tools (common in MCP servers), for example a JSON string instead of an object, and made some Anthropic-compatible gateways reject the request. Root `anyOf`, `oneOf` and `allOf` stay out, because Anthropic rejects them with a 400. Ports Pi's fix 4a6a58eb5 ([#9557](https://github.com/earendil-works/pi/issues/9557)). <!-- fix: d3f6c2b6ff -->
