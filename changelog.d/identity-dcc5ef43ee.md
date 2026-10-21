### Added

- `ai.ClientIdentity`, as pi-ai's `ClientIdentity`, chooses a request's `User-Agent` (detailed with a version, short, or custom) and provider attribution headers for OpenRouter, NVIDIA NIM and OpenCode. `StreamOptions.Identity`, `ClassifierOptions.Identity`, `ImagesOptions.Identity` and `CreateModelsOptions.Identity` carry it, `ai.MergeClientIdentity` merges identities field by field, and `ai.WithClientIdentity` gives a models object a default identity. Attribution is off by default in `ai` and on by default in the agent loop (`agent.DefaultAgentIdentity`, `AgentOptions.Identity`) and the durable Harness (`HarnessOptions.Identity`).
- Models and the coding Model Runtime add `x-opencode-session` for custom providers pointing at OpenCode, not only for the built-in OpenCode providers.

### Changed

- The coding agent sets its identity from the install-telemetry setting instead of adding attribution headers to every provider request. Direct pi-ai calls from the extension bridge carry no attribution, as under Pi. Attribution reads `PiG` for OpenRouter, NVIDIA and OpenCode alike (the OpenCode client header was `pig`).
- Classifier, llama.cpp, OpenRouter image and pi-messages requests send the identity's `User-Agent`, and Bedrock appends the identity to the AWS SDK user agent.
- A `User-Agent` request header replaces the Claude Code identity of an Anthropic OAuth request, and a `null` request header removes a model header of the OpenRouter image API whatever its letter case, as under Pi. A durable Harness wraps a plain `Models` once, so tools and hooks share one view of it.

### Removed

- The Cloudflare `User-Agent: pig-coding-agent` attribution, which Pi removed, and `ai.PiUserAgent`, which `ai.FormatUserAgent` replaces.
