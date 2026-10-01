# port-99-catalog-gen: catalog generators for upstream 0.99.1

Branch `port-99-catalog-gen`, base `staging/porter/pin-move` (6e8986038). Item 1 of `docs/plan/progress/pin-move.md`.

## What changed

- `cmd/gen-models` reads upstream 0.99.1's layout: barrel `models.generated.{js,ts}` -> `providers/<p>.models.{js,ts}` -> `providers/data/<p>.json` (`model-catalog.ts:57-81`, `providers/*.models.ts`). No provider or model literals in the emit path. Rows split by `type` (`chat`, `image`, `classifier`) in barrel order, then data insertion order, with `Object.fromEntries` integer-id ordering. Identity is validated as in `scripts/model-data.ts` (known type, `type:id` key, no key in two API groups, shard provider); fields a Go `ImageModel` or `ClassifierModel` cannot carry fail generation.
- One command emits `ai/models_generated.go` (1523 chat), `ai/image_models_generated.go` (57) and new `ai/classifier_models_generated.go` (12). `cmd/gen-image-models` is deleted; `automation/gen/generate-model-catalogs.sh` runs `gen-models` alone and checks the published pi-ai version. The 0.80/0.81 line parsers and flat-map layouts are removed (no compatibility path).
- `cmd/gen-models/openrouter_catalog.go` ports `scripts/openrouter-catalog.ts` (`buildOpenRouterCatalog`, `Number(toFixed(6))` cost rounding, `parseFloat` prefixes); `models_dev.go` gains the OpenRouter stage (`generate-models.ts:1306-1327`, three concurrent joined fetches) and the `models.all.json` / `providers/<id>.all.json` outputs (`:3486-3505`). Parity scenario `model-runtime-store-catalog/16-image-model-data` now compares `buildOpenRouterCatalog` with Pi (five cases; Go and Pi output identical, including NaN price -> null and toFixed ties).
- `cmd/check-model-data` moves to schema 6 (`model-data.ts:5,53-56,80-82,165-194,270-281`) and validates the real published 0.99.1 data (structure and file hashes agree).
- `ai/classifier_registry.go` (`GetClassifierModel`, `GetClassifierModels`, `GetClassifierProviders`) was removed in review: Pi has no such getters, the upstream readers are `getBuiltinClassifierModel` and `getBuiltinClassifierModels` in `providers/all.ts`, and family 3 (`ai/builtin_providers.go`) ports them over `GeneratedClassifierModels`.
- `internal/codingagent/default_models.go`: four defaults per `model-resolver.ts` at 0.99.1 (openai-codex `gpt-6.1-sol`, fireworks `kimi-k3`, together `Kimi-K3`, opencode-go `kimi-k3`). Forced by the regeneration (the old defaults left the catalog); outside the nominal item, four lines.
- Data-dependent tests ported to 0.99.1: supports-xhigh (Sonnet 5.5, GPT-6.1 Sol, off-less levels, K2.6 case removed), max-thinking, together-models (Kimi K3), fireworks-models (glm-5p3, deepseek-v4p1-flash, nemotron), abort / context-overflow / empty / image-tool-result / stream Together rows, model-resolver default rows, model-data-validation (schema 6 cases), image-model-data. Constants (1495 models, 55 images, 30 radius) replaced by counts derived from the published package (independent denominator). `ai/testdata/port-wave-13/anthropic_e2e_cases.json` and `ai/testdata/stream-cases.json` regenerated; `test/parity/testdata/generate-anthropic-e2e-cases.mjs` (referenced by the README, absent from the repo) added and reproduces the reviewed 0.87.1 snapshot exactly from the 0.87.1 data.

## Environment note

`npm ci --min-release-age=0 --ignore-scripts` in `extensions/sdk-ts` installed the published 0.99.1 package (no `~/.npmrc` cooldown on this host). The shared mirror `.upstream/v0.99.1/packages/ai/src/providers/data/*.json` was hydrated from it with the same copy `mirror-upstream.sh` does (`generate-models-strict` needs it). `.upstream/current` still points at v0.87.1 and was not changed. All commands ran with a temporary HOME.

## Red then green

- Red `405b708ad` (catalog stubs, 0.87.1 generated files): `TestSupportsXHighUpstream`, `TestMaxThinkingUpstream`, `TestAnthropicEagerToolInputCatalogDenominator`, `TestAnthropicLongCacheRetentionCatalogDenominator`, `TestRegistryHasModels`, `TestImageCatalogPin`, `TestImageModelRegistry_OpenRouterGeneratedModels`, `TestOpenRouterImagesUsageCostMatchesUpstream`, `TestCodegenByteIdentical`, `TestGeneratedCatalogsMatchPublishedPackage`, `TestClassifierRegistryMatchesPublishedCatalog`. gen-models red `6216d547c`/`46569f0d4` (every parse test failed on `unknown field "type"`), check-model-data red `13896920f` (5 subtests).
- Green `a099da249`: all of the above pass. Together/Fireworks rows passed even before regeneration because the old published data already carried Kimi K3 and glm-5p3; the matrix tests failed only once the regeneration removed K2.6 (fixed in the same change).
- Mutation checks (revert -> red -> restore): gen-models identity, provider, duplicate key, type routing, image routing, unsupported-field rejection (maxTokens, chat output), numeric id order, roundCost, modality dedupe, NaN->null, `.all.json`, first-wins merge; check-model-data image/chat output rules, classifier contextWindow, identity message, schema pin; generated data: cost change, image reorder, dropped classifier (all caught by `TestGeneratedCatalogsMatchPublishedPackage`). Three survivors found first (unsupported fields, modality dedupe, first-wins) got tests.

## Gates run

`go build ./...`, `go vet` (ai, cmd, coding, codingagent), gofmt, `go fix -diff`, `GOOS=windows go vet` (ai, gen-models, check-model-data, codingagent), golangci-lint (0 issues) on ai, cmd/gen-models, cmd/check-model-data, internal/codingagent, cmd/pig. `go test ./ai` with `PI_SKIP_VERSION_CHECK=1`: only `TestAPIKeyProvidersMatchPinnedProviderDefinitions` (typesafe provider, family 3), `TestOpenAIHTTPErrorMatchesPi` and `TestOpenCodeModelsSmokeUpstream` (0.87.1 oracle guards, pin-move item 6) fail; all three failed at baseline. A wide run (`coding`, `internal`, `cmd`, `tui`, `test`) shows no failure caused by removed or changed models; the failures are ledgers for 0.99.1 (item 5), 0.87.1 oracles (6), missing python/rust toolchains, and the interface-extractor `typescript` module.

## Loop self-report

Family: catalog generators (ai model data)

Bugs found and fixed at the source (count: 4):
  - `check-model-data` still schema 3 (no type, no typed keys) -> schema 6 port @ cmd/check-model-data/model_data.go:20, model_data_json.go
  - `TestCodegenByteIdentical` resolved `.upstream/current` (0.87.1) and the removed image barrel -> published package / versioned mirror, three outputs @ ai/registry_test.go
  - `TestDefaultModelPerProviderMatchesPinnedUpstream` read `.upstream/current` -> `.upstream/v<UpstreamVersion>` @ cmd/pig/model_test.go
  - defaults pointed at models the regenerated catalog dropped -> 0.99.1 defaults @ internal/codingagent/default_models.go

Comparators tightened (count: 3):
  - `ai` catalog counts: hard-coded 1495/55/30 -> derived from the published package
  - `TestGeneratedCatalogsMatchPublishedPackage`: new full-field, ordered comparison of all 1592 models
  - scenario 16-image-model-data: image parser (deleted upstream) -> `buildOpenRouterCatalog`, output_equal, canonical key order both sides

Divergences numbered in docs/parity/DIVERGENCES.md (count: 1 candidate, not recorded):
  - generator rejects a key/type/id mismatch and cross-group duplicates where upstream `flattenModelCatalog` would silently drop or replace; upstream's own `check:model-data` rejects the same data before publication, so it is unreachable on published data. Owner call whether to record.

Lint suppressions added or changed (count: 0)

New file coverage (count: 0 PORT_MAP rows changed here; see below)

Band-aids consciously chosen (count: 0)

Scope expansion (count: 5 files outside the nominal item):
  - internal/codingagent/default_models.go, cmd/pig/model_test.go, cmd/pig/model_resolver_defaults_upstream_test.go, internal/codingagent/interactive_post_login_integration_test.go: defaults forced by the regeneration; call sites audited via the cmd/pig and codingagent runs
  - test/parity/scenarios/model-runtime-store-catalog/16-image-model-data.toml + testdata: its oracle imported the deleted upstream script

Cross-family scenarios re-probed/tightened (count: 1): model-runtime-store-catalog/16-image-model-data (oracle run by hand: identical output; `make parity` not run).

Smells investigated (count: 2):
  - `TestGeneratedCatalogsMatchPublishedPackage` passed first try -> mutation-checked three ways (cost, order, dropped model), all red
  - Together Kimi K3 / Fireworks rows were green before regeneration -> old published data already had them; recorded above

Smells deferred to user (count: 0)

## Left for the integrator (not in this item)

- PORT_MAP: delete rows `packages/ai/scripts/generate-image-models.ts`, `packages/ai/src/image-models.generated.ts` (upstream gone); map `scripts/openrouter-catalog.ts` -> `cmd/gen-models/openrouter_catalog.go`, `scripts/model-data.ts` -> `cmd/check-model-data`.
- `make interface-go interface-recommendations-generate` (new export `GeneratedClassifierModels`; removed `cmd/gen-image-models` has none).
- 0.99.1 ledgers: the `ai` matrix tests (abort, context-overflow, empty, image-tool-result) read case IDs from `upstream-tests-v0.87.1.json`; their `.upstream/v0.87.1` line citations refresh with `make test-inventory-generate`. `test-mapping-v0.87.1.json` still names the deleted `cmd/gen-image-models` tests.
- Not ported: the models.dev decision/classifier stage, AI Gateway and Radius stages of `generate-models.ts`; the Go generators consume the published data. `generate-models-strict` needs the hydrated mirror data (see environment note).
- `ai.GetImageModels` still sorts by id; upstream lists catalog order (family 1).
