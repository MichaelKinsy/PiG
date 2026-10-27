### Upgrading

- Go API `ai`: the Amazon Bedrock provider moves to the new package `ai/providers/bedrock`, so a program that imports `ai` alone no longer links the AWS SDK. `ai.BedrockProvider`, `ai.NewBedrockProvider`, `ai.NewBedrockProviderWithName`, `ai.NewBedrockProviderWithModel` and `ai.NewBedrockAPIProvider` are now `bedrock.BedrockProvider`, `bedrock.NewBedrockProvider`, `bedrock.NewBedrockProviderWithName`, `bedrock.NewBedrockProviderWithModel` and `ai.NewOptionalAPIProvider(ai.APIBedrockConverseStream, model)`. Before: `ai.NewBedrockProvider(id, baseURL)`. After: `bedrock.NewBedrockProvider(id, baseURL)` with `import "github.com/MichaelKinsy/PiG/ai/providers/bedrock"`. A program that only builds models through `ai`, `agent` or `coding` and wants Bedrock imports the package for its side effect: `import _ "github.com/MichaelKinsy/PiG/ai/providers/bedrock"`, or `import _ "github.com/MichaelKinsy/PiG/ai/all"` for every provider package. Without the import, building a provider for an `amazon-bedrock` model fails with `Provider amazon-bedrock: API bedrock-converse-stream is not linked into this program; import github.com/MichaelKinsy/PiG/ai/providers/bedrock`. The `pig` command imports `ai/all`: its providers, models and errors are unchanged.

### Added

- `ai.RegisterOptionalAPI` and `ai.NewOptionalAPIProvider` register and build the provider of an API that lives in a package under `ai/providers`. `ai` also exports the shared stream machinery such a package builds on (`AssistantStreamBuilder`, `NewObservedProviderBuilder`, the continuation executor and Node readable emulation, `GetProviderEnvValue` and the like); it is for provider packages and not a stable surface for other callers.

### Changed

- A program that embeds PiG and links no Bedrock package is smaller (39.9 MB to 36.7 MB for `examples/headless` on linux/amd64, and 18.3 MB to 15.0 MB for a program that only imports `ai`): `import _ "github.com/MichaelKinsy/PiG/ai"` no longer links the AWS SDK and smithy (89 packages). The `pig_strip_bedrock_converse_stream` build tag still works: with it, `ai/providers/bedrock` compiles to the strip registration alone.
