# Lane port-99-f6h-model-types: typed model registry and image/classifier operations for every SDK (upstream 0.99.1)

Branch `port-99-f6h-model-types`, base `porter/pi-0.99.1` (113a10ba9). Scope: `ctx.modelRegistry.{classify, findOfType, getAvailableOfType, getModelOfType, getModelsOfType, registerVirtualModel, unregisterVirtualModel}` and provider `images`/`classifiers` implementations in the Go host and the Go, Rust and Python SDKs. The Node runtime (`runtime-node/runtime.mjs`) belongs to lane `port-99-f6f-node`; the wire below is what it implements.

## Upstream behavior (`.upstream/v0.99.1/packages/coding-agent/src/core/`)

- `model-registry.ts:71-79` `findOfType`, `:135-161` `getModelsOfType`, `getAvailableOfType`, `getModelOfType`, `:170-177` `classify`, `:161-168` `registerVirtualModel`/`unregisterVirtualModel`.
- `model-runtime.ts:800-815` `classify`: assert a classifier model, prepare the request (provider lookup, request-time auth, options over resolved auth), call the provider's `classify`, and turn every failure into a `classifierErrorResult` (never rejects; aborted when the caller's signal is aborted).
- `provider-composer.ts:275,647-663` classifier model entries and `classify` composition (an extension's `classifiers[api]` wins, then the base provider's, else an error result "Provider X has no classifier implementation for "api""); `:632-647` the same for `images`.
- `extensions/types.ts:1896-1898` `ProviderConfig.images`/`classifiers`, `:1929-1991` the discriminated model config.
- Test twins: `test/model-runtime-classifiers.test.ts` (1 case), `test/model-runtime-images.test.ts:143-185` and `:319-322` (the cases 6B left pending on family 3's `Classify`).

## Wire (host <-> SDK), current shape, no version field

| what | frame |
|---|---|
| typed reads (`findOfType`, `getModelOfType`, `getModelsOfType`) | the reply of `getModelRegistryState`: `models` keeps the chat models (`getAll` is chat only, `model-registry.ts:51-53`); new `typedModels` lists every image and classifier model with its `type`. The SDK filters both lists by type (an entry without `type` is chat) and optional provider. Also published in `model_registry_update`. |
| `getAvailableOfType(type, provider?)` | SDK -> host call `getAvailableOfType` `{type, provider?}` -> array of model objects. Starts async (a Promise upstream). |
| `classify(model, context, options?)` | SDK -> host call `classify` `{model, context: {state, questions}, options?}` -> `ClassifierResult` JSON. The host resolves auth and never fails the call for a classification failure. The order of `questions` and `answers` is the caller's and the service's (JS object order), so both travel as raw JSON. |
| `registerVirtualModel`/`unregisterVirtualModel` on the facade | the existing calls of `pi.registerVirtualModel` (`loader.ts:480-495`); the facade methods are the same operation. |
| provider config `images`/`classifiers` | register payload `providers[].image_apis`/`classifier_apis` name the APIs whose callbacks run in the extension; the config carries no callbacks. Host -> extension request `provider_operation` `{kind: "images"\|"classifiers", api, model, context, options}` with `tool` = provider name; the result is the `AssistantImages`/`ClassifierResult` JSON; an error is the request's error. |
| Provider object `generateImages`/`classify` | listed in `native.methods`; run through the existing `provider_call` with `{method, params: {model, context, options}}`. |

## Red

Evidence: `docs/plan/evidence/port-99-f6h-model-types.red.txt`.

| file | new tests | red |
|---|---:|---:|
| `coding/model_runtime_classifiers_upstream_test.go` (twin of `model-runtime-classifiers.test.ts` and two cases of `model-runtime-images.test.ts`) | 3 | 3 |
| `coding/extension_model_types_wire_test.go` | 4 | 4 |
| `coding/extension/model_info_types_test.go` | 1 | 1 |
| `coding/extension/host/subprocess/model_types_calls_test.go` | 5 | 5 |
| `extensions/sdk/model_types_test.go` | 7 | 7 |
| `extensions/sdk-rs/tests/model_types_wire.rs` | 6 | 6 |
| `extensions/sdk-py/tests/test_model_types.py` | 7 | 7 |
| `test/extension-conformance/model_types_test.go`: 6 rows x 7 placements (Go fused/strict/packed, Rust isolated/packed, Python strict/shared-ok) | 42 | 42 |

Signature stubs (fail for the right reason: "not implemented"): `ModelRuntime.Classify`, `ModelRegistry.Classify`, `ModelOperationBindings.Classify`, the `getAvailableOfType` and `classify` actions and calls, `extension.AnyModelInfo`, the SDK methods of each language, `sdk.ProviderConfig` `images`/`classifiers`, `Provider.GenerateImages`/`Classify`, `extension.NativeProvider.GenerateImages`/`Classify`, `subprocess.ProviderDecl.ImageAPIs`/`ClassifierAPIs`. The Rust `Provider` gains two fields, so four struct literals (`proxy.rs`, `provider_stream_shutdown_tests.rs`, `testdata/provider-object-rust`) take `generate_images: None, classify: None`.

## Green

Commits: `9006b5298` (green: host, `ai` wire helpers, Go, Rust and Python SDKs) and `371682886` (classify aborted regression in each native SDK). Every red test passes: 3 + 4 + 1 + 5 + 7 + 6 + 7 + 42 = 75, plus 3 new `aborted` tests (Go, Rust, Python), 2 new `ai` wire tests.

### Ported tests edited in green

`extensions/sdk-rs/tests/model_types_wire.rs` `Host::start` kept the whole register frame, so its provider assertions read the wrong path. It now keeps `frame["register"]`. The mistake was in this lane's own harness, not an upstream assertion; every asserted expectation is unchanged.
`test/extension-conformance/testfixture/modeltypes/extension.go` takes `go fix` (`slices`) and `%w` (errorlint) for the gates.
`coding/codemode_session_harness_test.go` `registerScorerProvider`: the codemode lane left it a stub for the classifier family. It is implemented from `agent-session-codemode.test.ts:519-560`.

### Mutation checks (each red, then restored)

| mutation | red test |
|---|---|
| `ClassifierAnswers.UnmarshalJSON` reverses the entries | `ai` `TestClassifierContextAndResultRoundTripKeepOrder` |
| `extensionClassify` ignores the caller's `apiKey` | `TestExtensionClassifyAction` |
| composer ignores the config `Classifiers` | `TestModelRuntimeClassifiersUpstream`, `TestExtensionProviderConfigKeepsTypedModelsAndImplementations` |
| `handleClassify` re-encodes the context through a map | `TestClassifyCallPassesModelContextAndOptionsToTheAction` |
| Go SDK typed reads ignore `typedModels` | `TestModelRegistryTypedReadsUseTheHostState` |
| Python typed reads ignore `typedModels` | `test_typed_reads_answer_from_the_registry_state` |
| the Go, Rust and Python `classify` catch reports `error` for a cancelled request | the three `aborted` tests (the conformance rows did not catch it; found and closed here) |

### Load

`GOMAXPROCS=4 taskset -c 0-3 go test -race -count=24` with four CPU burners: Go SDK typed tests, `coding` typed tests, subprocess typed calls all pass. The conformance rows ran with `-race -count=24` after the burners (result in the READY report).

### Deferred and stubs for other lanes

- Node `runtime.mjs` (`port-99-f6f-node`): `classify`, `getAvailableOfType`, facade typed reads and provider `images`/`classifiers`. `knownCapabilityGaps` lists `classify` and `getAvailableOfType` for `node`; the lane deletes the entries when it lands them. The wire note is in `questions/port-99-f6f-node.md`.
- TypeScript SDK declarations (`port-99-f6f-ts`).
- Codemode `models` global (`TestUpstreamCodemodeModels`): fails on `ReferenceError: models is not defined`, which is the codemode lane's builtin. The scorer provider helper it needs is now implemented.
- Hydrated 0.99.1 catalog cases: none in this scope; the twins use fixtures.
- Ledger cells (`test/parity/sdk-surface*.toml`): the fix-99-j5-ledgers lane realizes them (notice sent).
- Tests that need `extensions/sdk-ts/node_modules` (the Pi comparison tests) fail in this worktree before and after this lane.

## TypeScript declarations (taken over from the retired port-99-f6f-ts lane)

`extensions/sdk-ts` re-exports Pi's declarations (`export type *`), so `ModelRegistry.{classify,findOfType,getAvailableOfType,getModelOfType,getModelsOfType,registerVirtualModel,unregisterVirtualModel}` (`model-registry.ts:135-177`) and `ProviderConfig.images`/`classifiers` (`types.ts:1385-1387`) already reach TypeScript through the 0.99.1 pin. No declaration was missing. `typecheck.ts` gains the rows that make the claim checkable: `typedModelRegistry` (each method, the type-argument narrowing, three `@ts-expect-error` misuses: a model type outside chat/image/classifier, `classify` of a chat model, an image model read as a classifier), `extensionRegistryFromContext` (`ctx.modelRegistry` in a command handler), `providerImagesAndClassifiers` (`ProviderConfig` with image and classifier models, `images` keyed by image API with `generateImages`, `classifiers` keyed by classifier API with `classify`), and adapter identity for `ModelRegistry` and `ProviderConfig`.

Red: not possible. The declarations exist in the pin, so the rows compile the first time (`tsc --noEmit -p tsconfig.json` clean). Mutation proof: deleting `export type *` from `index.d.ts` fails `tsc` on the four adapter names, two of them the new ones; restored. The `@ts-expect-error` rows fail as unused directives if the pinned declarations were loosened.

Runtime behavior of the Node SDK is `port-99-f6f-node`'s (`runtime.mjs`). The generated `docs/extension-sdk-surface.md` and `test/parity/sdk-surface*.toml` still describe 0.87.1 and belong to `fix-99-j5-ledgers`, which regenerates them at the pin; this lane does not hand-edit them.

## Codemode `models` global (moved from fix-99-iserror)

Red: `dafa89d43` (`models_global_test.go`: the `models` global against a fake registry and the extension default; `Options.DisableModels` stub). Evidence: `docs/plan/evidence/port-99-f6h-codemode-models.red.txt` (the unit tests fail with `ReferenceError: models is not defined` and no `models` declaration; the twin `TestUpstreamCodemodeModels` fails the same way).

Green: `coding/extension/builtin/codemode/models.go` ports `execute.ts:369-433` (`createModelGlobals`): `models.getModelsOfType`, `getAvailableOfType`, `getModelOfType`, `classify` over the Session's `ModelRegistry` (a consumer-owned `modelRuntime` interface, asserted on `ctx.ModelRegistry()`, because `coding` imports this package). `toModelType`/`toProvider` error texts are upstream's (`execute.ts:71-80`); catalog entries drop `headers` (`toModelInfo`, `execute.ts:82-86`) and the wire-only `modelId`; `classify` resolves the model by provider and id only, so a script's `baseUrl` or `headers` never receive credentials (`execute.ts:410-416`); a nested call row `<id>/models.classify/<n>` (`provider/id`, status by `stopReason`, `cost`); a FIFO limiter of four concurrent classifications (`MAX_CONCURRENT_MODEL_CALLS`, `createLimiter`); the calls' usage is summed (`agent/harness/utils.AddUsage`, the port of `combineUsage`) into `AgentToolResult.Usage`, so the Session's cost includes it. `Extension` serves `models` unless `Options.DisableModels` (`index.ts` `options.models ?? true`), the removed "PiG leaves it false" default. `Definition` keeps upstream's `models` default of false.

Mutation checks (red, then restored): concurrency limit 5 (`TestUpstreamCodemodeModels`: max concurrent 5, want 4), usage dropped from the result (usage nil, session cost 0), headers kept in the catalog entry (`TestModelsGlobalServesTheRegistryToScripts`). `-race -count=24` on the unit tests and `-race -count=6` on the twin pass.

`TestUpstreamCodemodeOptionsAndStore/resolves_bash_calls_to_structured_results...` still fails (bash structured results, not this scope).

## Parity family runs (comparator installed by `make parity-deps`)

`make parity-family FAMILY=model-runtime-store-catalog`: one failure, `18-images-runtime-auth`: the Pi side throws `builtinImagesModels is not a function` (the fixture `test/parity/testdata/images-runtime.mjs` imports an export the pinned 0.99.1 package lacks); the Pig side prints the expected `IMAGES_RUNTIME` line. The scenario belongs to the images family, not to this lane.
`make parity-family FAMILY=extensions-runtime`: 20 scenarios failed (fail-fast stops the run). The same scenarios on `staging/porter/pi-0.99.1` (`113a10ba9`, a separate worktree, `--runs=1`) fail as well, 22 there against 19 here, none only here (for example `26-extension-load-order` and `28-prompt-precedence`: `get_commands` source paths). Nothing in this scope changes those. This lane does not claim the family green.
