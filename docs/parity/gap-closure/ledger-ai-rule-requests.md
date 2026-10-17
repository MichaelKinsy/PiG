# Ledger rule requests: package ai, bottom of `make interface-gaps` (integrate-042 9e25939e3)

Method: `make interface-gaps` lists 1,748 ai gaps (1,295 child-gap, 237 no-go-symbol, 151 undecidable, 33 member-missing, 19 type-mismatch, 11 signature-mismatch, 2 not-exercised). The bottom 70 non-child rows (everything below `providers/cerebras` plus the `utils/*` tail) were read against the Go source. No Go code is added for them: each Pi function either has a Go equivalent under another name or placement, or is a JS-runtime mechanism Go replaces with `context.Context`, and a new exported member would have no production caller. Every request below is for the ledger rules owner (`renames.json`, `renames-reviewed.json`, `holds.json`, rule files); IDs are the gap list's `pkg:ai/<module>#<name>` with every `::call:N` child.

## A. Rename or placement to an existing Go symbol (`renames.json`)
| Upstream | Go target | Note |
|---|---|---|
| `utils/pi-user-agent#getPiUserAgent` | `ai/user_agent.go#PiUserAgent` | same string `pi (<platform> <release>; <arch>)`; the browser branch has no Go equivalent. |
| `utils/validation#validateToolArguments`, `compat#validateToolArguments` | `agent/validate_arguments.go#ValidateToolArguments` | validation lives in package agent; request placement across packages. |
| `utils/validation#validateToolCall`, `compat#validateToolCall` | none: the agent loop validates inline | shard 4 (8770cc240) removed the caller-free Go `ValidateToolCall`; request designed-out (placement: agent loop). |
| `utils/event-stream#EventStream` (generic class) | `ai/event_stream.go#AssistantMessageEventStream` | the only instantiation Pi and Go use; request placement. |
| `utils/event-stream#AssistantMessageEventStream::property:[Symbol.asyncIterator]` (+ `::call:0`) | `ai/event_stream.go#AssistantMessageEventStream.Events` | `iter.Seq` with a context replaces the async iterator. |
| `utils/transcript#TranscriptMessages` / `TranscriptContext` | `ai/transcript.go#TranscriptContext` (`Messages()`, `Len()`, `At()`) | alias with a brand symbol: A3 has no rule for the object body. |
| `providers/faux#FauxProviderHandle`, `FauxProviderRegistration`, `RegisterFauxProviderOptions`, `createFauxCore`, `fauxProvider` | `ai/faux.go#FauxProviderState`, `FauxConfig`, `NewFauxProvider` | Go builds the provider from `FauxConfig`; the handle's state is `FauxProviderState`. |
| `providers/faux#fauxText`, `fauxThinking`, `fauxToolCall`, `fauxAssistantMessage` (`::call:0`) | `ai/faux.go#FauxText`, `FauxThinking`, `FauxToolCall`, `FauxAssistantMessage` | T9/T10/T12 mismatches: `TextContent`/`ThinkingContent`/`AssistantMessage` become `FauxContentBlock`/`FauxResponse`; options object becomes the `id` string. |
| `providers/faux#FauxContentBlock` (U5) | `ai/faux.go#FauxContentBlock` | discriminator `Type string` carries the literal union. |
| `providers/faux#FauxResponseFactory::call:0`, `FauxResponseStep` | `ai/faux.go#FauxResponseFactory`, `FauxResponseStep` (`FauxStaticStep`, `FauxFactoryStep`) | union alias becomes a struct with two constructors. |
| `providers/radius-config#RadiusGatewayConfig`, `RadiusGatewayModel`, `getRadiusModels`, `getRadiusModelsFromConfig`, `RadiusOAuthCredential` | the Radius provider options and `ai.PiMessagesModel` | A3/T9: object aliases map to structs; `Model` maps to `PiMessagesModel` for this provider. |
| `utils/headers#headersToRecord::call:0` | `ai/provider_response.go#headersToRecord` | T9: `Headers` is `http.Header`. |
| `utils/model-operations#assertChatModel::call:0`, `getModelType::call:0` | `ai/model_operations_export.go#AssertChatModel`, `ai/model_types.go#GetModelType` | T9 `AnyModel` is `ai.Model`; T12 `keyof ModelTypeMap` is `ai.ModelType`. |
| `utils/models-error#ModelsError::construct:0` | `ai/auth_resolve.go#NewModelsError` | the `cause` option is the error chain (`Unwrap`); T12b has no rule for it. |
| `providers/cloudflare-stream#resolveCloudflareModel` (N5) | the Cloudflare model resolution in `ai/auth_resolve.go`/`ai/node_http_proxy.go` | N5 matched three declarations; request a reviewed single target. |
| `providers/opencode-headers#withOpenCodeSessionHeader` | inline in `ai/anthropic.go` (`x-session-affinity`/`x-session-id` selection) | placement: no separate Go function. |

## B. Provider factories (about 40 rows, `providers/<id>#<id>Provider`, `providers/all#getBuiltinProviders`, `BuiltinProvider`, `cloudflareStreams`)
Go builds every provider from data (`ai/builtin_providers.go#BuiltinProviders`, `ai/register_builtins.go#LookupBuiltInProvider`, the generated model catalog). A per-provider Go function would hard-code a provider in shared code (AGENTS.md "Faithful, general implementations"). Request one placement rule: `providers/<id>#<camelId>Provider` to `ai/builtin_providers.go#BuiltinProviders` entry `<id>`, with the construction evidence being the catalog test for that provider id.

## C. JS runtime mechanisms replaced by `context.Context` (designed-out, cite the TypeScript/Promise rule in AGENTS.md)
`utils/abort#operationSignal`, `raceWithAbortSignal`, `utils/abort-signals#combineAbortSignals`, `CombinedAbortSignal`, `utils/sleep#sleep` (N5 matches `abortableSleep`, `abortableDeviceSleep`, `sleepContext`), `utils/provider-retry#retryProviderRequest` (retry runs inside each provider's request code through `WithProviderRequestRetry`, `ProviderMaxRetries`, `providerRetryDelay`), `utils/models-error#ModelsError::property:stack` (Go errors carry no stack), `utils/typebox-helpers#StringEnum` (a TypeBox schema builder for extension authors; Go tool schemas are JSON schema maps and the SDKs are separate modules, so an `ai.StringEnum` would have no caller).

## D. Not addressed here (needs the rules owner's decision first)
`utils/assistant-message-frame#AssistantMessageFrame` (U4: discriminated union with no Go type for the event discriminator; Go uses `AssistantMessageEvent`).

## E. Second tranche: member-missing, type-mismatch and signature-mismatch rows (about 55 rows, each also under `compat#`)
Go paths below were checked against `ai/types.go` at 9e25939e3.

### E1. `Model` property placement (nested Go struct, flat Pi interface)
`Model::property:*` rows are member-missing because Go nests Pi's flat fields. Request a nested-path rename for each (and the same under `compat#Model`):
| Pi property | Go member |
|---|---|
| `name` | `ai/types.go#Model.DisplayName` |
| `provider` (T1) | `ai/types.go#Model.Provider` (`ai.Provider` is a string type: T1 should accept a named string type) |
| `api` | `ai/types.go#Model.ProviderMeta` field `API` (`ProviderMetadata.API`) |
| `baseUrl` | `ProviderMetadata.BaseURL` |
| `headers` | `ProviderMetadata.Headers` |
| `compat` | `ProviderMetadata.Compat` |
| `reasoning` | `ProviderMetadata.Reasoning` |
| `contextWindow` | `Model.Capabilities` field `ContextWindow` |
| `maxTokens` | `ModelCapabilities.MaxOutputTokens` |
| `cost` (`input`, `output`, `cacheRead`, `cacheWrite`, `tiers`) | `ModelCapabilities.InputCostPer1M`, `OutputCostPer1M`, `CacheReadCostPer1M`, `CacheWriteCostPer1M`, `CostTiers` (`ai/types.go` `CostTier`) |
`ImageModel::property:type` and `ClassifierModel::property:type` map to the `ModelType`/`Type` discriminator of `ai/model_types.go`; `KnownProvider` (A1) maps to `ai.Provider` (open string type, catalog-driven; a closed literal set would hard-code providers).

### E2. Signature rules (S4 and T12)
- `ApiKeyAuth::property:check`, `::resolve::call:0`: Pi's `signal` becomes the `context.Context` argument, not a field of `APIKeyAuthInput` (async/cancellation rule).
- `FetchFunction::call:0/1`, `registerImagesApiProvider::call:0`: Pi's second argument (`init`, `sourceId`) has a Go counterpart only where a production caller needs it; `registerImagesApiProvider` `sourceId` was already listed as a real gap by gap-interface-ledger round 7. Not closed here.
- `api/google-shared#convertMessages::call:0` (Pi 2 parameters, Go 3) and `api/openai-completions#convertMessages::call:0` (Pi 4, Go 3): Go takes `model` already flattened (`providerID`, `modelID`, `supportsImages`, or `completionsConvertOptions` which carries Pi's `compat` and `options`). Targets: `ai/google.go#geminiConvertMessages`, `ai/openai.go#convertCompletionsMessages`. Request an argument-mapping rule, not a signature change.

### E3. Type rules
- T6 (Pi `Record<K,V>` against a Go slice of key/value structs): `ClassifierChoiceAnswer.probabilities` (`[]ClassifierProbability`), `ClassifierChoiceQuestion.criteria` (`[]ClassifierChoiceCriterion`), `SystemOneWireRequest.questions` (`systemOneWireQuestions`). Go keeps order-significant pairs; the wire form is checked by the classifier tests. Request a reviewed T6 exception for ordered record slices.
- U5: `ConstrainedSamplingConfig`, `FauxContentBlock`, `PiMessagesEvent` discriminator `Type string` carries the literal union; request a reviewed exception where a test asserts every literal.
- `SimpleStreamOptions.toolChoice` (U1): Go `ToolChoice` is `interface{}`/`any` because Pi's value is string or object.
- `PiMessagesResponseError.cause`/`.stack`: Go errors expose `Unwrap`; no stack (designed-out as in section C).
- `GrammarVariants` (not-exercised): no test and no cmd/pig caller. Not a port gap; it needs a test that drives `GrammarVariants` through the grammar path or removal as caller-free. Left open.

## F. Third tranche: `api/*` no-go-symbol rows (checked against the Go source at 9e25939e3)
### F1. Rename or placement to an existing Go symbol
| Upstream | Go target | Note |
|---|---|---|
| `api/cloudflare#CLOUDFLARE_WORKERS_AI_REST_BASE_URL` | `ai/cloudflare.go#CloudflareWorkersAIBaseURL` | same literal URL template. |
| `api/llama-cpp-classify#llamaServerRoot`, `labelProbabilities`, `peakConfidence`, `classify` | `ai/llama_cpp_classify.go#LlamaCppServerRoot`, `LlamaCppLabelProbabilities`, `LlamaCppPeakConfidence`, `ClassifyLlamaCpp` | Go prefixes the module name; N1/N2 do not add it. |
| `api/typesafe-system-one#classify`, `api/cloudflare-workers-ai-system-one#classify` | `ai/system_one.go#ClassifyTypesafeSystemOne`, `ClassifyCloudflareWorkersAISystemOne` | same prefix pattern. |
| `api/google-shared#mapStopReason`, `mapStopReasonString` | `ai/google.go#mapGoogleFinishReason` | one Go function covers both Pi overloads. |
| `api/google-shared#toGoogleSdkThinkingLevel`, `usesGoogleThinkingLevel`, `GoogleApiThinkingLevel` | `ai/google.go#ToGoogleThinkingLevel`, `resolveGoogleThinkingLevel`, `GoogleThinkingLevel` | Go has no SDK enum conversion step; the string constants are the wire values. |
| `api/google-shared#getDisabledGoogleThinkingConfig` | `ai/google.go#buildGeminiThinkingConfig` (the disabled branches at 651 and 655 return `ThinkingBudget: 0`) | placement: inline in the thinking-config builder. |
| `api/google-shared#resolveGoogleFunctionCallingMode`, `mapToolChoice` | inline in `ai/google.go` request building (`NONE`/`ANY`/`VALIDATED`/`AUTO`, `ToolConfig`) | placement: not an extractable Go function; precedence matches Pi (explicit none/any, then strict mode, then the mapped choice). |
| `api/google-shared#retryGoogleRequest` | `ai/google_sdk_pipeline.go#googleRetryGoogleRequest` | unexported Go name differs only by prefix. |
| `api/google-shared#convertTools` | `ai/google.go#geminiConvertTools` | |
| `api/openai-responses-shared#convertResponsesMessages`, `ConvertResponsesMessagesOptions`, `convertResponsesTools`, `ConvertResponsesToolsOptions`, `processResponsesStream`, `OpenAIResponsesStreamOptions` | `ai/openai_responses.go` methods `convertMessages`/`convertAnchoredMessages`, `convertTools`, `parseResponses`/`parseResponsesSSE` on `openAIResponsesProvider` | Go keeps the provider's config as the receiver, so Pi's options objects have no struct; request an argument-mapping rule (receiver fields). |
| `api/openai-completions#ConvertCompletionsMessagesOptions` | `ai/openai.go#completionsConvertOptions` | |
| `api/<id>#stream` (anthropic-messages, azure-openai-responses, bedrock-converse-stream, google-generative-ai, google-vertex, mistral-conversations, openai-codex-responses, openai-completions, openai-responses, pi-messages) and each `<id>.lazy#<id>Api` | the provider's `Stream` method (`ai/openai_responses.go#openAIResponsesProvider.Stream` and its siblings) | placement: Go registers one `Provider` per API through `ai/register_builtins.go#LookupBuiltInProvider`; there are no separate lazy-loaded modules. |
| `api/<id>#<Name>Options` (AnthropicOptions, AzureOpenAIResponsesOptions, BedrockOptions, GoogleOptions, GoogleVertexOptions, MistralOptions, OpenAICodexResponsesOptions, OpenAIResponsesOptions, PiMessagesOptions) | `ai/types.go#StreamOptions` plus the provider config (`OpenAIResponsesConfig`, ...) | Go has one `StreamOptions`; request a per-property map for each, since the properties (for example `thinkingEnabled`, `effort`) are fields of `StreamOptions` or `Model`. |

### F2. JS-runtime only (designed-out proposals, need the owner's approval and a numbered divergence)
`api/cloudflare-ai-binding#AiBinding`, `CLOUDFLARE_GATEWAY_BINDING_AUTH_SENTINEL`, `createAiBindingFetch`, and `bedrock-converse-stream.lazy#setBedrockProviderModule`/`bedrockProviderModule`: they wrap the Cloudflare Workers `env.AI` binding and the browser-safe lazy module hook. Go has no Workers runtime and no browser bundle. I did not find a Go counterpart and did not write one; the proposal is "designed out: Workers-runtime binding", to be recorded by the owner as a divergence. I did not invent an ID.
