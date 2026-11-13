### Changed

- Google providers: `StreamOptions.Fetch` now reaches the `google-generative-ai` and `google-vertex` adapters (API key and Application Default Credentials) instead of failing with `Custom fetch is not supported`; Vertex's ADC token request still uses its own HTTP client. A `CONTINUATION` finish reason ends the turn with stop reason `length`, like `MAX_TOKENS`. <!-- fix: f72f799a4c -->
