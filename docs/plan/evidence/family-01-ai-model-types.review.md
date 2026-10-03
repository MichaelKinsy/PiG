# Review: family 1 red port (ai model types), commit 49037dccc

Reviewer lane: `rev-port-f1-red`. Upstream mirrors: `.upstream/v0.87.1` and `.upstream/v0.99.1` (extracted with `automation/gen/mirror-upstream.sh --version v0.99.1`). Verdict: **ACCEPT-WITH-FIXES**. The ported cases are faithful after the fixes below. The family red set is incomplete: the cases in "Missing coverage" must get their own red commit before the green implementation of the behavior they cover.

## Fixes landed on top of 49037dccc

| commit subject | finding |
|---|---|
| `fix(red): compile remaining CreateProvider callers against []AnyModel` | `go vet ./...` failed in `agent/harness/runtime` (test package could not build). The pig sides of `model-runtime-store-catalog/19-models-runtime` and `providers-faux-streaming/23-models-provider-lifecycle` did not build. These were harness failures, not missing behavior. |
| `chore(red): regenerate Go interface inventory for the red API change` | `interface-go-drift`, `interface-recommendations-drift` and `interface-mapping-quality` failed because the exported API changed without `make interface-go` / `make interface-recommendations-generate`. |
| `fix(red): restore Pi's inputs and assertions in TestModelTypesUpstream` | Dropped assertion `getAuth(model)` is undefined (images-models.test.ts:274). Exact-equality where Pi uses `toContain` (:268). Changed fixture: chat api added to an images-only provider and an in-place header transform (:223-251). Bare `ImageModel` where Pi spreads the image model (:138). Id-only comparison where Pi uses `toEqual(faux.models)` (model-types.test.ts:74-75). Undocumented representation of the `type: "video"` image model (model-types.test.ts:133). Wrong citation lines. |
| `fix(red): drop the images.test.ts case upstream 0.99.1 removed` | `ai/images_upstream_test.go` kept "should handle text plus image output", which 0.99.1 removed, and cited 0.87.1. |
| `fix(red): point the 18-images-runtime-auth pig side at the renamed test` | `images-runtime.mjs` ran `-run ^TestImagesModelsAuthThroughOpenRouter$`, which matched nothing after the rename, so the scenario failed on a harness bug. |

## Coverage: ai tests changed or added 0.87.1 → 0.99.1 in this family

Ported by 49037dccc (with the fixes above):

- `images-models.test.ts`: 10 of 13 cases. Not ported, with a named reason: "rejects chat models at the image entry point at runtime" (unrepresentable, `Models.GenerateImages` takes `*ImageModel`); "keeps existing built-in and compat model reads chat-only" and "builtinModels exposes OpenRouter image models under the openrouter provider" (need the hydrated 0.99.1 model data, blocked by D-A).
- `model-types.test.ts`: 3 of 4 cases. "return model shapes that can be reassigned within one api" is a TypeScript compile-time check.
- `models-runtime.test.ts`: the one added case.
- `openrouter-images.test.ts`: type renames and the new `modalities` assertion.
- `openrouter-oauth.test.ts`: both changed cases.
- `fetch-option.test.ts`: type rename only.
- `images.test.ts`: case removal (fixed in this review).

Missing, with no recorded reason (source-only unless marked):

1. `providers.test.ts:79` "returns empty results for unknown provider ids" (new). Needs Go counterparts of `getBuiltinImageModel`, `getBuiltinClassifierModel`, `getBuiltinImageModels`, `getBuiltinClassifierModels`, `getAllBuiltinModels` and the compat getters.
2. `providers.test.ts:69` changed `models.getModels(provider.id)` to `models.getAllModels(provider.id)` (the `typesafe` provider has no chat models). `ai/providers_runtime_upstream_test.go#TestBuiltinModelsRuntimeRegistersCatalog` still cites 0.87.1 and uses chat models only. Data-dependent.
3. `image-model-data.test.ts`: 4 cases (`buildOpenRouterCatalog` from `scripts/openrouter-catalog.ts`: image-only, chat+image same id, ignored listings, decision models as System One classifiers). Current Go evidence `cmd/gen-image-models/openrouter_upstream_test.go` ports the removed 0.87.1 `parseOpenRouterImageModels` cases. Catalog pipeline (D-E).
4. `model-data-validation.test.ts`: 4 new cases (unknown type rejected, image output modalities, chat output rejected, classifier without chat limits) and the `chat:model-a` keyed fixture change. Catalog pipeline (D-E).
5. `fireworks-model-generation.test.ts`: changed fetch mock (models.dev `?type=decision`, OpenRouter `startsWith`). Catalog pipeline (D-E).
6. `classifier-models.test.ts` (new, 5 cases). Cases 1-2 are source-only and exercise the typed accessors this commit stubs (`getModelsOfType("classifier")`, `getAvailableOfType("classifier")`, `getAllModels` length); cases 3-5 need data. `ProviderClassifier` is an empty struct and `ModelsProvider` has no `Classify`, so no stub exists for them. If they belong to Phase 2 step 3, record that assignment.
7. Data-only changes (need 0.99.1 data, D-A): `fireworks-models.test.ts`, `together-models.test.ts`, `supports-xhigh.test.ts` (Sonnet 5.5, GPT-6.1 Sol), `anthropic-adaptive-thinking-models.test.ts`.

Outside Phase 2 step 1 but in the same scenario families (plan Phase 2 step 6): coding-agent `model-runtime-images.test.ts`, `model-runtime-classifiers.test.ts`, `model-catalog-protocol.test.ts` (new), `remote-catalog-provider.test.ts` (changed, designed-out).

## Red for the right reason

`go test ./ai -count=1` after the fixes: 13 `TestModelTypesUpstream` subtests, `TestOpenRouterOAuthProvidersUpstream/resolves_the_same_stored_OAuth_key_for_chat_and_image_models` and `TestModelsGenerateImagesAuthThroughOpenRouter` fail on the stubs (zero values or "not implemented"). No failure comes from a compile error or a fixture.

Tests that pass on the stubs:

- `keeps chat reads independent from the all-model catalog`: vacuous in red. The stub `GetAllModels` returns nil, which equals the expected `[]`. It discriminates in green: an implementation that falls back to `GetModels` when `GetAllModels` errors fails it (models.ts:446-454 returns `[]` from the catch). Mutation-check it in green.
- `is exposed alongside API-key auth`, `TestOpenRouterImagesUpstream` (including the new `modalities` assertion) and `TestImagesUpstream`: behavior unchanged in 0.99.1 (`api/openrouter-images.ts` diff is type renames only).

Other packages: `go vet ./...` is clean. `go test ./...` shows only the pre-existing environment failures (`internal/nativeplatform` X11, `subprocess` native-clipboard-linux), which also fail at 5f7365546. `cmd/pig` exceeds the default 10-minute test timeout under a full parallel run and passes alone with `-timeout 45m` (612 s).

## Stubs

Signatures follow the plan's API list (D-E). Findings for the green phase:

- `Models.GetModelAuth` returns the provider auth unchanged for non-chat models and skips the `modelAuth` hook for them. Pi's `getAuth(model)` merges `model.headers` for every model type (models.ts:746-753). The stub pre-empts a behavior that differs from Pi, and no ported test covers it.
- `CreateProvider` keeps only chat models (`chatModels`) as a compile adaptation. It is replaced in green.
- `HasApi` and `ModelsAreEqual` carry their 0.99.1 bodies. A signature change to `AnyModel` requires a body, and neither makes a red test pass.
- `requires at least one concrete operation implementation` encodes a Pi `throw` as a Go panic in `CreateProvider`. That is acceptable while every production caller passes static definitions. Revisit if an extension path calls `CreateProvider`.
- `ModelsProvider.GenerateImages` and `ProviderImages.GenerateImages` return `(AssistantImages, error)`. Pi's provider-level `generateImages` "never rejects" (models.ts:220). Green must convert errors to error results at the `Models` boundary.
- No Go counterpart exists for `openrouterProvider()`/`builtinModels()` with image models. The red commit deleted `BuiltinImagesModels` and `OpenrouterImagesProvider` (the 0.87.1 production path). `TestModelsGenerateImagesAuthThroughOpenRouter` and the openrouter-oauth port now use hand-built providers, so no test proves that the production OpenRouter provider exposes image models. The data-deferred images-models cases must bind this.

## Ledger and gate state left for the owner

These reference the deleted 0.87.1 port and fail or overclaim. Coordinate the change with the lane owner.

- `make test-inventory` and `make test-porting-release` fail: `test-mapping-v0.87.1.json` evidence for `packages/ai/test/images-models.test.ts` names deleted `images_models_upstream_test.go`, `images_models_async_test.go` and `TestImagesModelsAuthThroughOpenRouter`.
- `make async-contracts` has two new failures for `packages/ai/src/images-models.ts` (same deleted tests). The `agent.ts` failure is pre-existing.
- `docs/parity/PORT_MAP.md` rows for `src/images-models.ts` and `src/providers/openrouter-images.ts` claim ✅ against the deleted `ai/images_models.go`. `port-map-drift` does not detect it.
- `model-runtime-store-catalog/18-images-runtime-auth.toml` says Go drives the same production path, which is no longer true. Its pi side uses 0.87.1 `builtinImagesModels` and must be rederived from 0.99.1 `builtinModels()` after the pin moves.
