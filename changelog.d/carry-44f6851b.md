### Fixed

- OpenRouter's `~anthropic/claude-*-latest` aliases (`~anthropic/claude-fable-latest`, `-haiku-`, `-opus-` and `-sonnet-latest`) no longer lose Claude's earlier thinking after each tool call. They used the Chat Completions API, where the thinking signature was dropped and OpenRouter then ignored the unsigned thinking; they now use the Anthropic Messages API with adaptive thinking, like the versioned Claude models. Carried forward from Pi 44f6851b (earendil-works/pi#9534), past the 1.4.0+b3a29f11 pin. <!-- fix: 44f6851b -->
