# Interface ledger closure: ai package

Specs only: `test/parity/interface-closure/specs/ledger-ai-*.json`, applied with `close.py`. The regenerated `mapping-v1.0.4.json` is not committed. Apply the specs on top of the interface mapping (`close.py test/parity/interface-closure/specs/ledger-ai-*.json`, then `make interface-mapping-quality`).


Base `783250a36` (the `gap-interface-ledger` lane head). Specs only: `test/parity/interface-closure/specs/ledger-ai-*.json`, applied by `close.py`. The regenerated `mapping-v1.0.4.json` is not committed, so the integrator applies the specs on top of the `gap-interface-ledger` mapping (`close.py test/parity/interface-closure/specs/ledger-ai-*.json`, then `make interface-mapping-quality`).

## Result

704 ai rows move to `ported` (376 from `deferred`, 328 from `pending`): 609 production-reachable (`complete`), 95 `public-api-tested`. Applied to the base mapping, ai goes from 661 to 1365 closed rows. `audit.py` (reachability) lists 0, `make interface-mapping-quality` and `make check-contracts-fast` are green, `make lint-changed` is clean, and `runevidence.py` over the specs passes (the Pi oracle evidence needs `make parity-deps`).

| spec | rows | what |
|---|---:|---|
| 01 functions | 24 | `getImagesApiProvider`, `registerBuiltInImagesApiProviders`, `normalizeRadiusGatewayUrl`, `oauthErrorHtml`, `oauthSuccessHtml`, `resolveAzureBaseUrl`, `lazyApi` (public-api-tested), `contentText` with every re-export |
| 02, 10 aliases | 291 + 4 | `alias.py` closes a re-export ID (`compat#X`, `oauth#X`, `.#X`, defining module) from an already-ported sibling whose shape and every member shape are identical once `aliasTarget` is ignored (a re-export carries `aliasTarget`, so the shape hash differs) and whose members are all ported. The spec copies the sibling's targets, production, evidence and member mapping. |
| 03 types | 8 | `LazyApiCapabilities`, `OpenAICodexWebSocketDebugStats`, `NormalizedProviderError`, `ContextUsageEstimate` and their aliases |
| 04 functions with new tests | 25 ids | `assertChatModel`/`assertImageModel`/`assertClassifierModel`, `imageErrorResult`, `classifierErrorResult`, `estimateTextTokens`, `truncateErrorText`, `safeJsonStringify`, `createInitialSystemMessage`, `getInitialSystemMessage`, `withoutInitialSystemMessage`, `getDeclaredTools`, `resolveTranscriptTools`, `resolveAzureConfig`, `resolveDeploymentName`, `resolveSamplingParams` |
| 05 constants | 12 | the literal constants whose Go value is asserted: `MAX_PROVIDER_ERROR_BODY_CHARS`, `DEFAULT_MAX_AGENT_RETRY_DELAY_MS`, `OPENAI_PROMPT_CACHE_KEY_MAX_LENGTH`, `DEFAULT_RADIUS_GATEWAY`, `UNSUPPORTED_PROXY_PROTOCOL_MESSAGE`, three `ANTHROPIC_*_ENV`, four Cloudflare base URLs (public-api-tested: no production caller) |
| 06 catalogs | 126 | the 42 provider modules' `<P>_MODELS`, `<P>_IMAGE_MODELS`, `<P>_CLASSIFIER_MODELS` against `ListModels`, `GetImageModels`, `GetBuiltinClassifierModels` |
| 07 functions with existing tests | 14 ids | `appendGrammarToolInputJsonDelta`, `createGrammarToolInputProperties`, `getGrammarToolInput`, `getJsonSchemaToolParameters`, `getProviderEnvValue`, `repairJson` (+2 aliases), `requiresToolCallId`, `resolveGoogleThinkingLevel`, `resolveGrammarConstrainedSampling`, `resolveJsonSchemaStrictSampling`, `sanitizeSurrogates` |
| 08 unions | 31 ids | `StopReason`, `CacheRetention`, `Transport`, `SessionAffinityFormat`, `ImagesStopReason`, `ClassifierStopReason`, `ModelsErrorCode`, `AuthType`, `KnownApi`/`Api`, `KnownImageApi`/`ImageApi`, `KnownClassifierApi`/`ClassifierApi`, `ModelType`, `ModelThinkingLevel` |
| 09 stores | 68 | `CredentialStore`, `InMemoryCredentialStore`, `ModelsStore`, `InMemoryModelsStore` with every member and the constructors (options.signal is the `context.Context` parameter) |
| 11 | 2 | `ModelCostTier` |

## Round 2 (specs 12 to 20)

With specs 01 to 20 applied, ai goes from 661 closed rows to 1932; 643 rows stay pending, 1524 deferred, 2 designed out.

| spec | what |
|---|---|
| 12 options | `StreamOptions`, `SimpleStreamOptions` (folded into Go `StreamOptions`: `reasoning` is `Thinking`, deferred is `Deferred`), `DeferredFetchOptions`, `ProviderRequestOptions`, `ClassifierOptions` (879 rows; `signal` is the `context.Context` argument; `fetch` is the `*http.Client` proven by `fetch_option_upstream_test.go`) |
| 13 | `DeferredCancelOptions` (`StreamOptions` in Go) |
| 14 models | `Models`/`MutableModels` members `checkAuth`, `classify`, `complete`, `completeSimple`, `getAllAvailable`, `getAvailable`, `getAvailableOfType`, `logout`, `stream`, `streamDeferred`, `streamSimple` with their parent rows |
| 15 | `AuthInteraction`, `ModelsRefreshOptions` |
| 16 | `Provider` (the Go `ModelsProvider` struct; `ai.Provider` in Go is the older single-API interface) |
| 17 | `TextContent`, `ThinkingContent`, `ImageContent`, `ToolCall`, `UserMessage`, `SystemMessage`, `AssistantMessage`: the `type`/`role` discriminator is the literal the type emits through `MarshalJSON` |
| 18, 19 | the six classifier question/answer variants and the `ClassifierQuestion`/`ClassifierAnswer` unions |
| 20 | `ToolResultMessage`, `Message` |

Decision taken (lead had left the discriminator open): a discriminator property such as `type` or `role` maps to the Go type's `MarshalJSON`, which emits the upstream literal. `ai/wire_shape_upstream_test.go` marshals a fully populated value of every variant and compares the literal and the exact set of property names with upstream (red when a literal or JSON tag changes).

Source fix found on the way: Go `ImagesOptions.MaxRetries` was never applied and `MaxRetryDelayMs` did not exist, while upstream `openrouter-images.ts` passes both to `retryProviderRequest`. `MaxRetries` is now `*int` (nil is unset, as in TypeScript), `MaxRetryDelayMs *int` is added and both reach the OpenRouter request (`TestOpenRouterImagesHonorPerRequestRetryOptions`, red before the fix). This changes the exported field type.

Round 2b (specs 22 to 25, 82 more rows; ai closed 2024 with the specs applied, 571 pending): `ImageModel` and `ClassifierModel` (`type` is the `ModelType()` accessor, `TestModelTypeDiscriminatorsMatchUpstreamLiterals`), `AnyModel`, `TranscriptTools`, `TranscriptContext` (opaque branded wrapper), and `AssistantMessageEventStream` with its constructor (`push` is `Push`, `end` is `End`, `result` is `Result`, the async iterator is `Events(ctx)`; `Push` also returns an error for a payload no typed caller can build).

Round 2c (specs 26 to 30, 30 more rows; ai closed 2062 with the specs applied): `ProviderStreams` (public-api-tested: no production path reaches `CreateProvider`), `AssistantMessageFrameEncoder` and the `AssistantMessageFrame` union (public-api-tested), `GoogleApiThinkingLevel`, `AssistantMessageEvent` (twelve variants, `TestAssistantMessageEventVariantsMatchUpstreamShapes`), `AuthPrompt` and `AuthEvent` (`TestAuthPromptAndEventVariantsMatchUpstreamShapes`).

Round 3 (specs 31 and 32, 10 more rows): `SamplingParams`, `SamplingParamsByThinkingLevel`, and the three classifier-module `classify` functions (Cloudflare Workers AI, llama.cpp, Typesafe System One). Not closed: the matching lazy `...Api` constructors (no test drives them), the 20 per-API `stream` and `streamSimple` functions (Go streams through a provider value built from a per-API config struct, not a stateless `(model, context, options)` function), and `ModelPromptCache` (Go's `map[string]int` also accepts keys upstream's `Partial<Record<"short" | "long", number>>` rejects).

### Still pending after round 2

- **`ImagesOptions`**: `TimeoutMs int` cannot express an explicit `0` (upstream arms a zero timeout; Go treats `0` as unset). Fix needs `*int` and a change to `TestOpenRouterImages...` callers that pass `0` for "unset".
- **`Model`, `BaseModel`, `ModelTypeMap`**: Go `Model` nests provider metadata and capabilities instead of the flat upstream members; no Go base type or type map. **`AzureEndpointOptions`**: upstream has 25 members (StreamOptions plus Azure fields), Go keeps five. **`EventStream<T, R>`**: Go has only the assistant-message stream. **`ModelsError`**: Go has no `name`/`stack`; the `models#` re-export records them as designed-out but `utils/models-error` still lists them pending.
- **`OAuthLoginCallbacks`**: Go has parallel plain and `...Context` callbacks, `OnSelect` returns `string` where upstream returns `string | undefined`. **`ApiKeyCredential`/`OAuthCredential`**: Go has one `Credential` struct.
- Faux provider API (about 150 rows, owned by `gap-ai`), per-provider options types (about 800 rows), compat types, provider factories (about 80 rows), event-stream classes, `ModelsError`, `ApiKeyAuth`, and the helper functions listed below.

## New PiG tests (each red under mutation)

- `ai/interface_ledger_test.go`: model-type assertions and error results, `EstimateTextTokens` (UTF-16 units), `TruncateErrorText`, `SafeJsonStringify`, the transcript helpers, `ResolveAzureConfig`, `ResolveAzureDeploymentName`, `ResolveSamplingParams`, the literal constants, `InMemoryCredentialStore` delete/order/cancellation. Ten source mutations (for example a changed default API version, anchoring on a non-additive history, HTML escaping on, a delete that does not remove the key) each failed these tests.
- `ai/builtin_catalog_upstream_test.go`: every pinned `providers/data/<id>.json` (42 providers) against the Go catalogs: model keys, name, API, base URL, reasoning, context window, max tokens, input, price (with tiers), input limits and headers for chat models; the corresponding fields for image and classifier models. Red when one Go price changed. It found no catalog drift.
- `ai/union_constants_upstream_test.go`: every ledger-closed string union parsed from the pinned source and compared with the Go constants.

## Not closed, and why (real gaps for a port lane)

- **Per-provider options types have no Go declaration** (`AnthropicOptions`, `BedrockOptions`, `AzureOpenAIResponsesOptions`, `GoogleOptions`, `GoogleVertexOptions`, `MistralOptions`, `OpenAICodexResponsesOptions`, `OpenAICompletionsOptions`, `OpenAIResponsesOptions`, `PiMessagesOptions`, about 800 rows): Go carries every provider option on one `StreamOptions`, so upstream's provider-specific members (`thinkingEnabled`, `region`, `toolChoice` variants, ...) have no named Go field to bind. Also `SimpleStreamOptions` (no `reasoning`-typed struct) and the compat types (`OpenAICompletionsCompat`, `OpenAIResponsesCompat`, `AnthropicMessagesCompat`, `BedrockCompat`, `MistralConversationsCompat`): Go merges them into `OpenAICompat` with `map[string]any` routing members instead of `OpenRouterRouting` and `VercelGatewayRouting`.
- **Per-provider factory functions** (`groqProvider`, `anthropicProvider`, ... about 80 rows): Go assembles every provider in `builtinProvider(id)` from a table, and only `TypesafeProvider` and `CloudflareWorkersAIProvider` exist by name. No test compares a provider's id, name, base URL, auth and API set with the pinned `providers/<id>.ts`. The catalogs are proven (spec 06); the factories are not.
- **`getPiUserAgent`**: Go reports `pig/<version> (...)` instead of `pi (...)` (D65 in `docs/parity/DIVERGENCES.md`), so the row is a `divergence` to be closed with `D65`, not `ported`.
- **`ThinkingLevel`**: upstream excludes `"off"`, Go's `ThinkingLevel` includes `ThinkingOff`. `ModelThinkingLevel` (off + levels) closed.
- **`Models`/`MutableModels`**: closed in round 2 (spec 14). Historical note, the first-round gap was: `stream`, `complete`, `streamSimple`, `completeSimple` (options types `ModelsApiStreamOptions`, `ModelsSimpleStreamOptions` have no Go type of that shape), `streamDeferred`, `classify` (`ModelsClassifierOptions` omits `signal` fields), and `checkAuth`, `getAvailable`, `getAllAvailable`, `getAvailableOfType`, `logout` (options are `{signal}` only and map to the context argument, but the parent closes only with all members). `Provider` has the same shape gaps for `stream`, `streamSimple`, `classify`, `generateImages`.
- **`StreamOptions`** (`fetch` is `*http.Client`, upstream a function), **`ProviderStreams`** (`streamSimple` takes `StreamOptions`, upstream `SimpleStreamOptions`), **`AzureEndpointOptions`** (Go keeps four fields; upstream extends `StreamOptions`).
- **Discriminated unions**: closed in round 2 for content blocks, messages and classifier variants (discriminator = `MarshalJSON` literal); `ClassifierModel`, `ImageModel` and `Model` stay open (see round 2).
- **Faux provider API** (`FauxProviderRegistration`, `FauxProviderHandle`, `FauxModelDefinition`, `RegisterFauxProviderOptions`, `fauxText`, `fauxThinking`, `fauxToolCall`, `fauxAssistantMessage`, about 150 rows): `faux.ts` is partial in PORT_MAP and owned by `gap-ai`; Go helpers return `FauxContentBlock`, upstream the content types.
- **Event streams and cross-cutting helpers**: `EventStream`/`AssistantMessageEventStream` classes (iterator and `result` members), `ModelsError` (`name`, `stack`), `OAuthLoginCallbacks`/`AuthInteraction`/`ApiKeyAuth` (`signal` is the context parameter and the credential/interaction types differ), `getOverflowPatterns` (copy semantics untested), `combineAbortSignals`, `raceWithAbortSignal`, `operationSignal`, `sleep`, `shortHash`, `retryProviderRequest`, `validateToolArguments`, `validateToolCall`, `parseJsonWithRepair`, `StringEnum`: no Go function of that name and shape, or no direct test.

## Method notes

- `finalize.py`-style attribution: production is the first caller that go/types finds and `prodreach` marks reachable, with a valid Go fragment (a method on a generic receiver is not a valid fragment); a symbol without a reachable caller that is exported from a public package with tests is `public-api-tested`.
- Re-export aliases must be compared with `aliasTarget` removed; a plain `shapeHash` comparison finds only a handful.
- Do not re-point an alias row by name: `stream`, `streamSimple` and `classify` exist in every provider module with different shapes.

## Phase 2: gap-detector driven ports (`make interface-gaps`)

The deterministic detector is the source of truth. For the ai package the detector reported 2506 gap rows at the merge of the autobind lane (343 no-go-symbol, 128 member-missing, 27 not-exercised, 17 signature-mismatch, 153 type-mismatch, 1591 child-gap, 247 undecidable). After the ports below it reports 2430 (323, 119, 8, 13, 153, 1566, 248); the whole-repository total went from 5751 to 5675.

Ported from Pi, tests first (expected values from the pinned 1.0.4 sources run under Node):

- `ShortHash` (was the unexported `shortHash32`), `GetSystemMessageText` (was `systemMessageText`), `ParseJSONWithRepair[T]` (replaces the test-only `unmarshalJSONWithRepair`), `EstimateTextAndImageContentTokens`.
- `FormatThrownValue`, `ExtractDiagnosticError`, `CreateAssistantMessageDiagnostic`, `AppendAssistantMessageDiagnostic` with the `DiagnosticCoder` interface for `error.code`; the Pi Messages error path now uses them (an empty message falls back to the error name like upstream).
- `RetryDelayMs(policy, attempt)` now takes the policy like upstream `retryDelayMs`; `MakeStrictJSONSchema(schema, isUnsupportedKeyword)` is the exported two-parameter function.
- Faux: `FauxModelDefinition.Cost` and `.InputLimits`, and `FauxProviderState.CancelledDeferred`.
- Tests that exercise previously unreferenced symbols against Pi: `GetOverflowPatterns` (compared with the pinned pattern list), `CreateProviderOptions.BaseURL`/`Headers`, `TypesafeProvider`, `FauxResponseFactory`.

Left for other lanes or decisions (detector classification in brackets):

- `validateToolArguments` and `validateToolCall` live in `agent/validate_arguments.go`, not in the ai package [no-go-symbol].
- The discriminator properties (`type`, `role`) are `MarshalJSON` literals, not Go fields [member-missing]; a detector rule for `MarshalJSON` literals or recorded renames would clear about 40 rows.
- `Model` stores provider metadata in `ProviderMeta` and capabilities, not upstream's flat properties [member-missing, 14 rows plus `BaseModel`/`ModelTypeMap`].
- `AzureEndpointOptions` has 5 of upstream's 25 members; the per-provider options types and compat types are not ported [no-go-symbol].
- The 20 per-API `stream` and `streamSimple` functions have no stateless Go equivalent; `registerImagesApiProvider`'s `sourceId` and the mismatched-api wrapper are not ported (no Go reader).
- `combineAbortSignals`, `raceWithAbortSignal`, `operationSignal`, `sleep` are Node signal helpers; Go uses `context.Context`.
- `ModelsError.stack` and `PiMessagesResponseError.stack`/`cause`: Go errors carry no stack, and upstream's PiMessagesResponseError sets no cause [member-missing]. `Name()` is ported on both and `ExtractDiagnosticError` reads it.
- The 126 `<P>_MODELS`, `<P>_IMAGE_MODELS` and `<P>_CLASSIFIER_MODELS` rows are values upstream and provider-parameterized functions in Go (`ListModels`, `GetImageModels`, `GetBuiltinClassifierModels`) [type-mismatch V3]; 45 per-provider factory functions would be caller-free (`BuiltinProviders` builds them by id) [no-go-symbol].

Later in phase 2: `TextSignatureV1` is exported and used by the Responses signature encode and parse (wire shape and legacy handling tested), `NewAssistantMessageFrameEncoder` exists, and `ClassifierFunction` and `ImagesFunction` are exercised by tests (the three `Classify*` functions are bound as `ClassifierFunction`; image dispatch passes the request through unchanged).

Rows left that name a rename rather than a port: running `go run ./test/parity/interface-closure/autobind -seed-renames` on a tree with the ledger-ai specs applied seeds 774 renames (before these ports: ai 2426 rows to 2231). I did not commit that seed: it is the detector lane's file and it records the hand-closed attributions, which should be reviewed there. `FetchFunction` (Go uses `*http.Request`/`RoundTrip`), `Provider.stream` (Go's per-model `Provider` binds the model) and the `Provider.*` rows (upstream `Provider` is Go `ModelsProvider`) are design differences.
