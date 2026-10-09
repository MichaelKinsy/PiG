# ai undecidable rows: decisions for the detector lane

Source: `make interface-gaps`, `build/interface-gaps/gaps.tsv`, reason `undecidable`, package ai. 247 rows at 467a1c22f plus the `EstimateContextTokens` port below. Each row is either a detector rule or rename (the detector lane decides it) or a real gap (ported here, tests first). Nothing here is hand-closed.

| Category | Rows | Decision |
|---|---:|---|
| T9 type equivalence (upstream type name differs from the Go type name) | 142 | rename: add the pairs below to the type-equivalence input of rule T9 |
| A3 object-union or intersection alias | 62 | rule: see "A3" |
| language mechanics (T4, T10, T12, T14, S5) | 35 | rule: see "Mechanics" |
| external type (`Static`, `TSchema`, `Type` from typebox) | 6 | designed out: not part of the Pi API |
| A4 declaration lookup (`ToolResultMessage`) | 2 | detector fix: the declaration is an interface plus a same-name alias in the pinned sources |
| real gap | 1 | ported: `EstimateContextTokens` |

## T9 equivalences (upstream type to Go type)

Each pair is a type the Go port spells differently. The evidence is the test or closure that exercises the Go type with the upstream shape.

| Upstream | Go | Rows | Evidence |
|---|---|---:|---|
| `Provider` | `ModelsProvider` | 21 | `TestCreateProviderCarriesNameBaseURLAndHeaders`, `ledger-ai-16-provider.json`; Go `Provider` is the per-model stream provider |
| `ProviderModel` | `AnyModel` | 11 | `ledger-ai-23-any-model.json`, `TestModelTypeDiscriminatorsMatchUpstreamLiterals` |
| `Tool` | `ToolSchema` | 26 | `ToolSchema` carries name, description, parameters, constrainedSampling (`ledger-ai-17-content-messages.json`) |
| `TranscriptMessages` | `[]Message` | 24 | `TranscriptContext.Messages()`; `ledger-ai-24-transcript.json` |
| `SimpleStreamOptions`, `ApiStreamOptions` | `StreamOptions` | 17 | one Go options struct; `reasoning` is `StreamOptions.Thinking` (now `ThinkingLevel`, see `TestThinkingLevelExcludesOffAndModelThinkingLevelIncludesIt`) |
| `MutableModels` | `Models` | 4 | `ledger-ai-14-models.json` |
| `OAuthCredential` | `Credential` (Type oauth) | 6 | `ledger-ai-15-auth-refresh-options.json` |
| `ProviderAuthInteraction` | `AuthInteraction` | 4 | `ledger-ai-30-auth-unions.json` |
| `Model` (as `Pick<Model, "baseUrl">`) | `string` | 3 | Azure functions take the base URL: pick of one property equals that property |
| `AnyModel` as an overload parameter of `getAuth` | `Models.GetModelAuth` | 6 | the two upstream overloads are `GetAuth(providerID)` and `GetModelAuth(model)` |
| `TextContent`, `ThinkingContent` | `FauxContentBlock` | 6 | `FauxText`, `FauxThinking`, `FauxToolCall` (`TestFauxResponseFactoryReceivesTranscriptOptionsStateAndModel`) |
| `Context` (compat `streamSimple`) | `TranscriptContext` | 1 | mapping question: the compat row maps to `StreamSimple`, which takes the normalized transcript; `Models.StreamSimple` takes a `Context` |
| `ResolvedGoogleThinkingLevel` | `ModelThinkingLevel` | 1 | `ledger-ai-07` (`resolveGoogleThinkingLevel` tests) |
| `Headers`, `URL`, `TSchema` | `http.Header`, `string`, `map[string]any` | 3 | platform types |
| `BuiltinImageModel`, `BuiltinImageProvider`, `ImagesApiProviderInternal` | `ImageModel`, `string`, `ImagesAPIProvider` | 3 | generic specializations |
| `Model<"pi-messages">` | `PiMessagesModel` | 1 | `GetRadiusModelsFromConfig` tests |
| `ProviderRequestOptions` | `DeferredCancelOptions` | 2 | Go flattens the base interface into each options struct |
| `ModelsStoreEntry.models` (`AnyModel[]`) | `[]json.RawMessage` | 2 | the store keeps raw JSON and decodes lazily; the wire is identical |

## A3: aliases of object unions and intersections

- A union of object types (`AssistantMessageEvent`, `AssistantMessageFrame`, `AuthEvent`, `AuthPrompt`, `Message`, `ClassifierQuestion`, `ClassifierAnswer`, `Credential`, `ConstrainedSamplingConfig`, `FauxContentBlock`, `FauxResponseStep`): the Go type is a closed sealed interface whose implementations are the variants. Proposed rule: accept when every variant has a Go type implementing the interface. Evidence: `TestAssistantMessageEventVariantsMatchUpstreamShapes`, `TestAuthPromptAndEventVariantsMatchUpstreamShapes`, `TestFrameJSONRoundTripsEveryVariant`, `TestWireDiscriminatorsMatchUpstreamLiterals`, `TestClassifierQuestionAndAnswerWireShapesMatchUpstream`.
- An intersection with `ModelsRequestTransforms` (`Models*Options`, `ProviderImagesOptions`): the Go type is a struct that embeds each constituent. Proposed rule: accept when the Go struct embeds a Go type for every constituent.
- An object type alias (`TranscriptContext`, `PiMessagesRewriteImpact`, `RadiusGatewayConfig`, `RadiusGatewayModel`, `JsonObject`, `AnyModel`, `ModelType`): a struct or map alias; `ModelType` is `keyof ModelTypeMap` (closed in `TestModelTypeDiscriminatorsMatchUpstreamLiterals`).

## Mechanics

- S5: a returned object `{ maxTokens, thinkingBudget }` (`adjustMaxTokensForThinking`) is two Go return values; `getImageModel` returns `(model, ok)` for an upstream `T | undefined`.
- T12: an options object with one optional member (`{ cause?: unknown }` for `ModelsError`, `{ id?: string }` for `fauxToolCall`) is that one Go parameter; `ModelTypeMap[TType]` is a generic result that Go states per call.
- T4: `SystemOneTransport.payload` returns `unknown`; Go returns `map[string]any`.
- T10: `estimateContextTokens` took `TranscriptContext | readonly Message[]` and Go took only the slice. Ported (below).
- T14: `lazyStream` `setup` returns an iterable of events; Go returns the stream (`ai/provider_streams.go#startLazyStream`).

## Real gap, ported

- `EstimateContextTokens` now takes `[]Message` or a `TranscriptContext` (generic `ContextTokensSource`). Test first: `TestEstimateContextTokensAcceptsATranscriptOrItsMessages`; the `TranscriptContext` branch is mutation-checked. Row `pkg:ai/utils/estimate#estimateContextTokens::call:0` leaves the gap list.

## Related change in this batch

`ThinkingLevel` and `ModelThinkingLevel` are now two Go types like Pi's (`ThinkingLevel` has no `off`; omitting `reasoning` means off). `agent.ThinkingLevel` is the agent's `off`-inclusive level (an alias of `ModelThinkingLevel`), used by `ModelCycleResult` and `ScopedModel`. The two coding-agent `thinkingLevel` T9 rows come from this and are resolved by that alias.

## After merging the detector rule families (996c5e80b)

ai undecidable rows fell from 247 to 154. The rest, by decision:

- 52 + 12 `A3` object-union and intersection aliases, 22 `T12` generic `ModelTypeMap[TType]` results, 6 typebox external types, 4 `T12b` options objects, 3 `T14`, 2 `A4`: rule or designed-out, as listed above.
- 6 `getAuth` overload rows: Go has the two overloads as `GetAuth(providerID)` and `GetModelAuth(model)`; a rule that maps an overload row to a differently named Go method.
- 9 `N5` rows (`ApiKeyCredential`, `BaseModel`, `ModelCostRates`, `getBuiltinImageModel`, `resolveCloudflareModel`, `raceWithAbortSignal`, `sleep`): the shape matches several Go declarations, so the detector refuses a unique match. Each needs a named rename.
- Faux helpers (`fauxText`, `fauxThinking`, `fauxToolCall`, `FauxResponseFactory`'s `AssistantMessage`): Go returns the `FauxContentBlock` struct and `FauxResponse`, not `TextContent`, `ThinkingContent`, `ToolCall` and `AssistantMessage`. Making them faithful changes the faux API owned by the faux lane, so no port here.
- `Tool.constrainedSampling` (`boolean | config`) and `SimpleStreamOptions.deferred` (`"15m" | "1h" | "24h"` union): Go splits them into a pointer plus a disabled flag and a `DeferredOption` struct; evidence is `TestConstrainedSampling...` and the deferred option tests; a rule for "union of scalar and object to pointer plus flag" would close them.

No further real gap is left among the ai undecidable rows.
