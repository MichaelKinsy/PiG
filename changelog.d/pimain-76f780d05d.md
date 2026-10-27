### Added

- `ai.StreamingJSONParser` and `ai.ToolCallArgumentsStream` read streamed JSON text once per piece: tolerant of incomplete and malformed text, with immutable snapshots, change reports and cost linear in the text. `ToolCall.AppendStreamedArguments` shows a call's partial arguments from a stream. Ports Pi's `StreamingJsonParser` and `ToolCallArgumentsStream`.

### Changed

- Partial messages no longer carry the provider's stream state: a tool call holds no `partialJson`, `partialArgs`, `customInput` or `streamIndex`, and an open Anthropic or Bedrock block holds no `index`; the providers, the proxy stream and the faux provider keep that state beside the block. The grammar tool input functions take a delta (`appendGrammarToolInputJSONDelta`) and a complete input (`closeGrammarToolInputJSON`).

### Fixed

- A tool call that streams many argument deltas no longer costs time and memory that grow with the square of its text: every provider (Anthropic Messages, Bedrock Converse Stream, Mistral, OpenAI Completions and Responses, Pi messages), the faux provider and the proxy stream read each delta once. A 500 KB `write` call streams in well under a second. <!-- fix: 76f780d05d -->
- A grammar (custom) tool input no longer copies the whole input so far on every delta. <!-- fix: 76f780d05d -->
