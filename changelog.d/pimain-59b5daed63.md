### Fixed

- OpenCode and OpenCode Go requests go to the protocol-specific endpoints instead of the shared base: OpenAI-compatible models at `/inference/openai/v1`, Anthropic models at `/inference/anthropic`, Google models at `/inference/google/v1beta` and Jev at `/inference/systemone/v1` (the same paths under `/inference/go` for OpenCode Go). Ports Pi's #11434. <!-- fix: 59b5daed63 -->
