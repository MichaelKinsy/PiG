# Lane port-99-f6b: model runtime, registry, catalog protocol, resolver, llama classifier (upstream 0.99.1)

Branch `port-99-f6b`, base staging `porter/pi-0.99.1` (15904a765). Scope: family-6-split section 3, lane 6B.

## Dependencies on other lanes

- Family 3 (`port-99-f3`, not merged into the base yet): `ai.ClassifierContext`, `ai.ClassifierResult`, `ProviderClassifier.Classify`, `Models.Classify`, the `typesafe` provider and `llama-cpp-classify`. Merging that branch onto this base fails today (`ai/openai_responses.go` conflicts with family 2, `classifier_models_generated.go` uses the removed `ImagesCost`, and a duplicate `TestOpenAIResponsesChatGPTSignInUpstream`), so this lane does not merge it. The cases that need it are listed under "Pending on family 3" and are not skipped.
- Family 6D (extension wire contract): the discriminated chat/image/classifier model entry on the wire (`coding/extension/provider.go`). This lane changes only the in-process `ProviderConfigInput` (`Models []ai.AnyModel`, `Images`, `Classifiers`, `RefreshModels` returning `[]ai.AnyModel`), mirroring `provider-composer.ts:97-106`.
- Family 6E: virtual models in `ModelRuntime` (`registerVirtualModel`, `resolveModel`, `streamSimple` routing, `withVirtualModels`, `model-runtime.ts:936-1019`) belong to the virtual-model lane, not to this one.

## Red (tests with signature stubs)

| upstream test | Go test | notes |
|---|---|---|
| `test/model-runtime-images.test.ts` (7) | `coding/model_runtime_images_upstream_test.go` | 5 subtests ported now; two cases wait for family 3 (see below) |
| `test/model-resolver.test.ts` (changed, 2 cases) | `cmd/pig/model_resolver_defaults_upstream_test.go` | codex default, "built-in chat providers have defaults" |
| `test/llama-extension.test.ts` (11 to 13) | `internal/codingagent/llama/provider_test.go` | 3 changed assertions, 1 new case (#10077); classify case pending |
| `test/model-runtime-cloudflare-compat.test.ts` (2) | `coding/cloudflare_compat_upstream_test.go` (existing) | see per-case note |
| `test/remote-catalog-provider.test.ts` (7 to 8), `test/model-catalog-protocol.test.ts` (1) | none | designed out per case, see below |

Signature stubs: `coding/model_runtime_typed.go` (`GetProvider`, `GetModelsOfType`, `GetModelOfType`, `GetAllModels`, `GetAvailableOfType`, `GetModelAuth`, `GenerateImages`), `llama.AnyModel`, `llama.ClassifierModel`, `Provider.GetAllModels`. Mechanical edits forced by the `ProviderConfigInput` type change: `Models: []ai.AnyModel{...}`, `RefreshModels` returning `[]ai.AnyModel` in existing tests and in `test/parity/testdata/native-provider-compat-go/main.go`; `internal/codingagent/registry_request_auth.go` reads chat definitions only until green.

Red run: `docs/plan/evidence/port-99-f6b.red.txt`.

### Per-case dispositions

- `model-runtime-images.test.ts:222` "rejects image models at every chat entry point": the TypeScript test casts an image model to a chat model. Go builds a `*ai.Model` whose `Type` is image. This case already passes on a natively registered provider (`ai.Models` asserts the chat type since family 1); green adds the same assertion to the legacy backend path and a regression test for a built-in provider.
- `remote-catalog-provider.test.ts`, `model-catalog-protocol.test.ts`: PiG has no pi.dev catalog client (`docs/parity/PORT_MAP.md` row `remote-catalog-provider.ts` is not implemented: owner-approved remote catalog refresh through PiG's own endpoint). The new cases (`?types=chat,image,classifier` request, image/classifier overlay, unknown-type drop, catalog protocol negotiation through `scripts/model-catalog-protocol.ts`) stay designed out with that reason; `model-catalog-protocol.test.ts` additionally imports pi.dev's `scripts/model-catalog-protocol.ts`, which is outside the four tracked packages. The `types=` request and the unknown-type filter (`remote-catalog-provider.ts:14-30,48-52`) must be ported when the PiG catalog endpoint exists.
- `model-runtime-cloudflare-compat.test.ts`: upstream replaced its `vi.mock("openai")` harness with a capturing `fetch` and asserts the exact request URL and headers. The Go tests already assert the exact URL and header set at the HTTP boundary (`coding/cloudflare_compat_upstream_test.go`), so no case changes.
- `llama-extension.test.ts:46` (`"<inline:llama.cpp>"` to `"builtin:llama.cpp"`): source naming is lane 6C's (`builtin:` in source info); no llama Go case reads it.

## Pending on family 3 (written in the green step that follows the family 3 merge)

- `model-runtime-images.test.ts:129` "registers extension image and classifier models with their implementations" and the two `provider.classify`/`typesafe` assertions at `:295`.
- `model-runtime-classifiers.test.ts` (1): typesafe Jev listed separately, unconfigured error, runtime-resolved auth, `runtime.classify`.
- `llama-extension.test.ts:352` "classifies with selectable models through llama-server", and `Provider.Classify` for `llama-cpp-classify`.

## Data-dependent

`TestModelResolverDefaultsUpstream/built-in_chat_providers_have_defaults...` fails for `openai-codex`, `fireworks`, `together`, `opencode-go` until the 0.99.1 model catalogs are regenerated centrally (family 1 open issue 1): the new defaults name `gpt-6.1-sol` and Kimi K3, which the 0.87.1 catalog lacks.

## Green

Commit `5a246a33d` (after the red commit `7d9065d14`; the ported tests are unchanged since red). Implementation, with the upstream line each piece follows (`.upstream/v0.99.1/packages/coding-agent/src/...`):

- `coding/model_runtime_typed.go`: `GetProvider`, `GetAllModels`, `GetModelsOfType`, `GetModelOfType`, `GetAvailableOfType`, `GetModelAuth`, `GenerateImages` (`core/model-runtime.ts:446-476,780-796`), and the chat guard on `Stream`/`StreamSimple` (`core/model-runtime.ts:695-782`, `assertChatModel`); `coding/model_registry_facade.go`: `FindOfType`, `GetModelsOfType`, `GetAvailableOfType`, `GetModelOfType` (`core/model-registry.ts:71-79,145-161`).
- `internal/codingagent/native_provider_composer.go`: typed `ProviderConfigInput`, `currentAllModels` (`GetAllModels`), `extensionNonChatModel` (`core/provider-composer.ts:229-283`), image implementation composition (`core/provider-composer.ts:632-647`); a model list replaces every model type of the provider (`provider-composer.ts:325-332`).
- `internal/codingagent/model_typed_provider.go`: the built-in provider with its image models (`builtinTypedBase`, shared image model pointers), `GetTypedProvider`, `ResolveRegistryTypedModelAuth` (type-aware `rawModelHeaders`, `provider-composer.ts:489-505`).
- `ai/model_operations_export.go`: `AssertChatModel`, `ImageErrorResult`, the two `utils/model-operations.ts` exports `model-runtime.ts` imports.
- `internal/codingagent/default_models.go`: four defaults (`core/model-resolver.ts:19-59`).
- `internal/codingagent/llama/provider.go`: classifier models per chat model, stored beside the chat models, restored by type and API; `contextWindowOf` order runtime, `--ctx-size`/`-c`/`-ctx`, cached, training, 128000 (`extensions/llama/provider.ts:58-103,136-256`).
- Mechanical test edits forced by the changes: `interactive_post_login_integration_test.go` (opencode-go default is `kimi-k3`), `cmd/pig/model_test.go` (together default is `Kimi-K3`).

Regression tests added beyond the ports (`coding/model_runtime_typed_test.go`): non-chat rejection on three built-in providers (red before the guard: legacy path reported "unknown provider"), image-generation failure results, available models per type across provider shapes (built-in with image models, chat-only built-in, models.json OpenAI-compatible provider), extension image models with implementation, headers and base URL on three provider shapes, the registry facade.

Mutation checks (revert, see red, restore): keep model headers on extension image models (3 subtests fail), ignore extension image implementations (3), skip configured typed headers (3), llama `--ctx-size` (1), chat guard removed (3), drop extension non-chat models from `GetAllModels` (3).

Load test: `go test -race -c` binaries, `GOMAXPROCS=4 taskset -c 0-3` with 4 CPU burners on the same cores, `-count=24`: the typed runtime tests and the llama package pass.

Gates run on this tree (Go 1.27.1, temporary HOME/PIG_HOME/PIG_CODING_AGENT_DIR): `go build ./...`, `go vet ./...` (also `./test/parity/testdata/*` fixtures through `TestParityFixtureProgramsCompile`; the `native-provider-compat-go` fixture needed the `[]ai.AnyModel` edit), `gofmt`, `golangci-lint` on `./coding ./internal/codingagent ./internal/codingagent/llama ./ai` (0 issues), `go fix -diff` (empty), `GOOS=windows go vet` on the touched packages (`./cmd/pig` fails on the baseline `waitForRPCTermination`), `go test -race ./coding ./internal/codingagent/... ./ai ./cmd/pig`. Parity: `model-runtime-store-catalog` (26 scenarios) and `providers-registry` pass three times in a row against the Pi 0.87.1 comparator, run through the runner directly (`go test -tags=parity ./test/parity/runner -args -pig-parity.dir=...`) because `make parity-family` refuses to install through the shared `extensions/sdk-ts/node_modules` symlink; env: `PIG_PARITY_PI_BIN`, `PI_PACKAGE_ROOT`, `PI_PACKAGE_DIR` set to the sdk-ts install.

Failures that exist on the unchanged base (`staging/porter/pi-0.99.1`, checked in a scratch worktree): `TestRPC33AzureTickOrder`, `TestRPC33GoogleObservationMatrix`, `TestRPC33ObservationMatrix`, `TestRPC33ObservationMatrixAzure` (oracle version guard), `TestFauxAgentObservationOracle`, `TestTestFauxAgentObservationOracle`, `TestFauxRPCObservation`, `TestTestFauxRPCObservation`, `TestRPCPiMessagesMatchesPi`, `TestRPCBedrockConverseStreamObservation`, `TestRPCStdoutBackpressureLetsProviderFinishBufferedBody`, `TestJSONModeStdoutBackpressureMatchesPi`.

## Shared-file touches and stubs for other lanes

- `coding/services.go` (6D): two four-line hunks at the top of `ModelRuntime.Stream` and `StreamSimple` call `assertChat` and `failedStream` (defined in `coding/model_runtime_typed.go`).
- `internal/codingagent/registry_request_auth.go`: the chat header lookup skips non-chat definitions; typed auth is in `model_typed_provider.go`.
- 6D: the wire `extension.ProviderConfig` still describes chat models only. `provider_registration_validation.go` and `provider_registration_namespace.go` build `ProviderConfigInput` from it; a discriminated model entry on the wire lands there, and the llama host (`llama/host.go SyncRegistration`) then publishes its classifier models through it. Until then the llama classifier models exist on the provider (`GetAllModels`) and in the store, not in the registry.
- 6E: virtual models in `ModelRuntime` (`registerVirtualModel`, `resolveModel`, `virtualModels` map, `withVirtualModels` in `recomposeProvider`, virtual routing in `streamSimple`), and the facade `registerVirtualModel`/`unregisterVirtualModel`.
- Family 3, after it merges: `ModelRuntime.Classify` (`model-runtime.ts:797-812`) and `ModelRegistry.Classify` (`model-registry.ts:170-177`), `builtinTypedBase` classifier models and implementations (typesafe, cloudflare-workers-ai, openrouter, opencode and vercel-ai-gateway classifiers), classifier composition in the composer (`provider-composer.ts:648-663`), `llama.Provider.Classify`, and the pending cases listed above.
- 6C: `builtin:llama.cpp` source name; `--no-extensions` also disabling the llama.cpp provider.

## Ledger notes for the integrator

- `test-mapping-v0.99.1`: `model-runtime-images.test.ts` partial (2 of 7 cases wait for family 3), `model-runtime-classifiers.test.ts` pending, `model-resolver.test.ts` ported (2 changed cases; line citations in the other Go files still cite 0.87.1 lines, content unchanged), `llama-extension.test.ts` partial (classify case), `model-runtime-cloudflare-compat.test.ts` ported (HTTP boundary substitution), `remote-catalog-provider.test.ts` and `model-catalog-protocol.test.ts` designed-out per case (see above).
- PORT_MAP: `core/model-runtime.ts`, `core/provider-composer.ts`, `core/model-registry.ts` stay partial (virtual models, classifiers); `core/model-resolver.ts` ported; `core/remote-catalog-provider.ts` not implemented; `extensions/llama/provider.ts` partial (classify).
- Regenerate `pig-go.json` and the recommendations after the exported Go changes (`ProviderConfigInput`, `ModelRuntime` typed methods, `ai.AssertChatModel`, `ai.ImageErrorResult`, `llama.AnyModel`, `llama.ClassifierModel`).

## Open items and known red until the central steps

- `TestModelResolverDefaultsUpstream/built-in_chat_providers_have_defaults.../openai-codex` and `TestPostLoginOAuthUsesSessionPersistenceAndThinking/openai-codex` fail until the model catalogs carry `gpt-6.1-sol` (family 1 open issue 1; the other three new defaults are in the 0.87.1 catalog already).
- `TestDefaultModelPerProviderMatchesPinnedUpstream` (`cmd/pig/model_test.go`) reads the pinned upstream table and fails until the pin moves to 0.99.1.
- `GOOS=windows go vet ./cmd/pig` fails on the baseline (`waitForRPCTermination`), unrelated to this lane.
