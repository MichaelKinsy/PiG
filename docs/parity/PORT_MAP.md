# PORT_MAP

Minimal upstream to pig file map.

- `docs/parity/PORT_MAP.md` answers where code lives
- `test/parity/coverage.md` answers what is verified and at what quality
- status here means code mapping only, not parity proof

Status: `✅` ported, `🟡` partial, `⬜` not started, `⏸` deferred, `🔴` broken, `n/a` not applicable.

Verification quality is tracked by the parity system, not by this status column. A row can be `✅` and still weak-only or untested in `test/parity/coverage.md`. Boot-only, smoke-only, and deferred scenarios do not count as behavioral verification. Registration-only scenarios count only for registry/catalog/auth-wiring paths. Provider implementation rows (for example `providers/openai-completions.ts`, `providers/openai-responses*.ts`, `providers/transform-messages.ts`) require stream/payload/history behavior tests; `--list-models` can only cover registry/catalog wiring.

## Version and gates

`.upstream/pin` is the sole version pin ([upstream-pin.md](upstream-pin.md)); `internal/coding/pigversion/pigversion.go` is generated from it and `coding/upstream.go` re-exports it as `coding.UpstreamVersion`, `coding.UpstreamPackageVersion` and `coding.UpstreamCommit`. `test/upstream-parity/mirror_version_test.go` rejects a `.upstream/current` mirror with a different package version. This map intentionally does not copy the version number.

`make port-map-drift` rejects source files missing from the map and any row, whatever its status, whose upstream file disappeared. `make upstream-delta` rejects changed source files without an explicit disposition and durable evidence. `make async-contracts` rejects Promise/async source files without a reviewed ordering, cancellation, error, concurrency, and loop-ownership contract. `make coverage-strict` rejects partial, broken, not-started, weak-only, and untested intended ports. `make family-gaps STRICT=1` rejects intended ports with no behavior family and family entries without acceptable behavioral evidence. `make foundation-check` runs those completeness gates after the normal parity gate.

Release history and open work belong in the tracked sync plan and generated coverage report, not this map.

## Package scope

PORT_MAP maps every source file of every package in the upstream monorepo: the fifteen packages listed in `test/parity/upstreampackages/packages.json`, which a test requires to equal the package directories of the pinned mirror. A package that upstream adds fails `make port-map-drift` until it is listed and mapped. The coverage report counts each package and the sums in `test/parity/coverage.md`; PiG's experimental remote Session stack (`packages/client`, `packages/protocol` and `packages/server`) and the experimental parts of `packages/coding-agent` are reported separately as the part of the monorepo that Pi does not ship in its npm package.

Package scope, one line each:

- `packages/agent`, `packages/ai`, `packages/coding-agent` and `packages/tui` are the four core packages.
- `packages/chord` is the application-composition runtime; `packages/durable` is the detached durable Session runtime; `packages/env` runs a Durable execution environment on another machine over SSH.
- `packages/client`, `packages/protocol` and `packages/server` are the experimental remote Session stack.
- `packages/codemode` is the sandboxed code-execution runtime and `packages/mcp` is the Model Context Protocol client library; both back built-in coding-agent extensions.
- `packages/evals` is the evaluation tooling and `packages/telemetry` is the standalone telemetry library.
- `packages/schema` is the eval-free JSON Schema validator (drafts 2020-12 and 7, compiled to closures); the Go package is `schema` (with `schema/formats`).

The drift gate also rejects explicit production comments claiming to port an
upstream file while its row remains not-started, deferred, or n/a. Such conflicts
require reviewed mappings; code presence alone never automatically earns ✅.

**Windows port: windows/amd64 implementation mapping.** The following entries identify platform-specific code. They do not establish native execution or full Windows parity:
- `ai/auth.go`: auth.json lock via `LockFileEx`/`UnlockFileEx` (upstream `flock` equivalent).
- `tui/terminal.go`: raw mode over `CONIN$`/`CONOUT$`; ANSI via
  `ENABLE_VIRTUAL_TERMINAL_PROCESSING | DISABLE_NEWLINE_AUTO_RETURN`; terminal resize by polling
  `GetConsoleScreenBufferInfo` (a pig-internal mechanism under upstream's observable resize→re-render;
  windows has no SIGWINCH); `DrainInput` is a documented no-op.
- `internal/codingagent/interactive.go`: SIGHUP not registered (Go maps console-close to SIGTERM);
  resize watcher polls; Ctrl+Z suspend shows upstream's "not supported" status.
- `internal/codingagent/keybindings.go`: `ctrl+z` unbound on windows (upstream
  `win32 ? [] : "ctrl+z"`).
- `internal/codingagent/tools/bash_executor.go`: process-tree kill via `taskkill /F /T`
  (upstream `killProcessTree`).
- `internal/codingagent/tools/shell_config.go`: windows bash resolution: Git Bash at
  `%ProgramFiles%\Git\bin\bash.exe` (then x86), then `bash.exe` on PATH, else a helpful
  "install Git for Windows" error (upstream `getShellConfig` win32 branch).
- `coding/extension/exec.go`: `CREATE_NEW_PROCESS_GROUP` for detach.

The tmux scenario harness cannot drive a Windows `.exe`. Windows verification requires tests executed on Windows for auth locking, terminal start/stop and resize, keybindings, local-socket extension hosting, and child-process cancellation, plus an interactive pass covering render, input, resize, Ctrl+C, Ctrl+Z, and clean exit. Cross-compilation proves build compatibility only. Record native execution evidence against the tested commit; this map does not assert that those checks have passed. Unix scenarios cover only their exercised shared behavior.

## `packages/ai/scripts/`

| upstream | pig | status |
|---|---|---|
| `packages/ai/scripts/check-model-data.ts` | `cmd/check-model-data/main.go (validation command over internal/modelgen/modeldata)` | ✅ |
| `packages/ai/scripts/model-data.ts` | `internal/modelgen/modeldata/model_data.go + model_data_json.go (identity, shard, manifest, hash and metadata validation; Node supplies Date.parse and JSON syntax diagnostics; moved from cmd/check-model-data so cmd/gen-models shares it)` | ✅ |
| `packages/ai/scripts/hydrate-model-catalog.ts` | `internal/modelgen/modeldata/hydrate.go (HydrateModelCatalog and GroupProviderModelData; command in cmd/check-model-data -hydrate <models.all.json>; hydration cases of model-data-validation.test.ts in hydrate_upstream_test.go)` | ✅ |

## `packages/ai/catalog/`

| upstream | pig | status |
|---|---|---|
| `packages/ai/catalog/build.ts` | `internal/modelgen/build.go` | ✅ |
| `packages/ai/catalog/define.ts` | `internal/modelgen/define.go (+ internal/modelgen/obj.go: the ordered JavaScript-object value the models are built as)` | ✅ |
| `packages/ai/catalog/families/anthropic.ts` | `internal/modelgen/family_anthropic.go` | ✅ |
| `packages/ai/catalog/families/deepseek.ts` | `internal/modelgen/family_deepseek.go` | ✅ |
| `packages/ai/catalog/families/google.ts` | `internal/modelgen/family_google.go` | ✅ |
| `packages/ai/catalog/families/kimi.ts` | `internal/modelgen/family_kimi.go` | ✅ |
| `packages/ai/catalog/families/openai.ts` | `internal/modelgen/family_openai.go` | ✅ |
| `packages/ai/catalog/generate.ts` | `cmd/gen-models/generate.go (flags, staging, validation, data and public JSON output; `-data-output` is the Go form of Pi's `src/providers/data`, `-data-only` takes its provider ids from that directory) + cmd/gen-models/main.go + cmd/gen-models/fixtures.go (-record/-replay)` | ✅ |
| `packages/ai/catalog/providers/amazon-bedrock.ts` | `internal/modelgen/providers/amazon_bedrock.go` | ✅ |
| `packages/ai/catalog/providers/ant-ling.ts` | `internal/modelgen/providers/ant_ling.go` | ✅ |
| `packages/ai/catalog/providers/anthropic.ts` | `internal/modelgen/providers/anthropic.go` | ✅ |
| `packages/ai/catalog/providers/azure.ts` | `internal/modelgen/providers/azure.go` | ✅ |
| `packages/ai/catalog/providers/baseten.ts` | `internal/modelgen/providers/baseten.go` | ✅ |
| `packages/ai/catalog/providers/cerebras.ts` | `internal/modelgen/providers/cerebras.go` | ✅ |
| `packages/ai/catalog/providers/cloudflare-ai-gateway.ts` | `internal/modelgen/providers/cloudflare_ai_gateway.go` | ✅ |
| `packages/ai/catalog/providers/cloudflare-workers-ai.ts` | `internal/modelgen/providers/cloudflare_workers_ai.go` | ✅ |
| `packages/ai/catalog/providers/deepseek.ts` | `internal/modelgen/providers/deepseek.go` | ✅ |
| `packages/ai/catalog/providers/fireworks.ts` | `internal/modelgen/providers/fireworks.go` | ✅ |
| `packages/ai/catalog/providers/github-copilot.ts` | `internal/modelgen/providers/github_copilot.go` | ✅ |
| `packages/ai/catalog/providers/google-vertex.ts` | `internal/modelgen/providers/google_vertex.go` | ✅ |
| `packages/ai/catalog/providers/google.ts` | `internal/modelgen/providers/google.go` | ✅ |
| `packages/ai/catalog/providers/groq.ts` | `internal/modelgen/providers/groq.go` | ✅ |
| `packages/ai/catalog/providers/huggingface.ts` | `internal/modelgen/providers/huggingface.go` | ✅ |
| `packages/ai/catalog/providers/index.ts` | `internal/modelgen/providers/index.go` | ✅ |
| `packages/ai/catalog/providers/kimi-coding.ts` | `internal/modelgen/providers/kimi_coding.go` | ✅ |
| `packages/ai/catalog/providers/meta.ts` | `internal/modelgen/providers/meta.go` | ✅ |
| `packages/ai/catalog/providers/minimax-cn.ts` | `internal/modelgen/providers/minimax_cn.go` | ✅ |
| `packages/ai/catalog/providers/minimax.ts` | `internal/modelgen/providers/minimax.go` | ✅ |
| `packages/ai/catalog/providers/mistral.ts` | `internal/modelgen/providers/mistral.go` | ✅ |
| `packages/ai/catalog/providers/moonshotai-cn.ts` | `internal/modelgen/providers/moonshotai_cn.go` | ✅ |
| `packages/ai/catalog/providers/moonshotai.ts` | `internal/modelgen/providers/moonshotai.go` | ✅ |
| `packages/ai/catalog/providers/nvidia.ts` | `internal/modelgen/providers/nvidia.go` | ✅ |
| `packages/ai/catalog/providers/openai-codex.ts` | `internal/modelgen/providers/openai_codex.go` | ✅ |
| `packages/ai/catalog/providers/openai.ts` | `internal/modelgen/providers/openai.go` | ✅ |
| `packages/ai/catalog/providers/opencode-go.ts` | `internal/modelgen/providers/opencode_go.go` | ✅ |
| `packages/ai/catalog/providers/opencode.ts` | `internal/modelgen/providers/opencode.go` | ✅ |
| `packages/ai/catalog/providers/openrouter.ts` | `internal/modelgen/providers/openrouter.go` | ✅ |
| `packages/ai/catalog/providers/qwen-token-plan-cn.ts` | `internal/modelgen/providers/qwen_token_plan_cn.go` | ✅ |
| `packages/ai/catalog/providers/qwen-token-plan-individual.ts` | `internal/modelgen/providers/qwen_token_plan_individual.go` | ✅ |
| `packages/ai/catalog/providers/qwen-token-plan-individual-cn.ts` | `internal/modelgen/providers/qwen_token_plan_individual_cn.go` | ✅ |
| `packages/ai/catalog/providers/qwen-token-plan.ts` | `internal/modelgen/providers/qwen_token_plan.go` | ✅ |
| `packages/ai/catalog/providers/radius.ts` | `internal/modelgen/providers/radius.go` | ✅ |
| `packages/ai/catalog/providers/together.ts` | `internal/modelgen/providers/together.go` | ✅ |
| `packages/ai/catalog/providers/typesafe.ts` | `internal/modelgen/providers/typesafe.go` | ✅ |
| `packages/ai/catalog/providers/vercel-ai-gateway.ts` | `internal/modelgen/providers/vercel_ai_gateway.go` | ✅ |
| `packages/ai/catalog/providers/xai.ts` | `internal/modelgen/providers/xai.go` | ✅ |
| `packages/ai/catalog/providers/xiaomi-token-plan-ams.ts` | `internal/modelgen/providers/xiaomi_token_plan_ams.go` | ✅ |
| `packages/ai/catalog/providers/xiaomi-token-plan-cn.ts` | `internal/modelgen/providers/xiaomi_token_plan_cn.go` | ✅ |
| `packages/ai/catalog/providers/xiaomi-token-plan-sgp.ts` | `internal/modelgen/providers/xiaomi_token_plan_sgp.go` | ✅ |
| `packages/ai/catalog/providers/xiaomi.ts` | `internal/modelgen/providers/xiaomi.go` | ✅ |
| `packages/ai/catalog/providers/zai-coding-cn.ts` | `internal/modelgen/providers/zai_coding_cn.go` | ✅ |
| `packages/ai/catalog/providers/zai.ts` | `internal/modelgen/providers/zai.go` | ✅ |
| `packages/ai/catalog/reasoning.ts` | `internal/modelgen/reasoning.go` | ✅ |
| `packages/ai/catalog/sources/index.ts` | `internal/modelgen/sources.go (+ the Radius gateway config reader of packages/ai/src/providers/radius-config.ts)` | ✅ |
| `packages/ai/catalog/sources/models-dev.ts` | `internal/modelgen/models_dev.go` | ✅ |
| `packages/ai/catalog/sources/openrouter.ts` | `internal/modelgen/source_openrouter.go (+ openRouterCost of src/providers/openrouter-api.ts, kept equal to ai.OpenRouterCost by internal/modelgen/crosscheck_test.go)` | ✅ |
| `packages/ai/catalog/sources/vercel-ai-gateway.ts` | `internal/modelgen/source_vercel.go (replaces scripts/ai-gateway-pricing.ts)` | ✅ |
| `packages/ai/catalog/README.md` | `internal/modelgen/README.md` (the build order it documents: `internal/modelgen/build.go`; the `ModelsDevModels` helper it documents: `internal/modelgen/define.go`) | ✅ |

## `packages/agent/src/`

| upstream | pig | status |
|---|---|---|
| `packages/agent/src/agent.ts` | `agent/agent.go + agent/lifecycle.go + agent/queue.go (StreamFunction exposes the caller override; nil denotes the stock path, whose DefaultStreamFn is stored separately because Go cannot compare functions)`, `agent/agent_prompt.go`, `agent/agent_state.go` | ✅ |
| `packages/agent/src/agent-loop.ts` | `agent/agent_loop.go + agent/standalone_loop.go + agent/run_tool_call.go + agent/agent.go + agent/tool_execution.go + agent/system_state.go; shallow stream views use ai/assistant_observation.go and ai/stream_observation.go (all 35 agent-loop.test.ts cases, the standalone loop API and runToolCall are ported; docs/parity/gap-closure/gap-agent-tui.md)` | ✅ |
| `packages/agent/src/types.ts` | `agent/types.go + agent/agent.go + agent/message_json.go`, `agent/tool_call_hook_funcs.go`, `agent/standalone_loop.go`, `agent/agent_state.go`, `agent/run_tool_call.go`, `agent/queue.go`, `ai/types.go` | ✅ |
| `packages/agent/src/proxy.ts` | `agent/proxy.go (request and event reconstruction, tool-call member merge, abort and timeout messages, unknown-event warning; differential tests agent/proxy_oracle_test.go and agent/proxy_sparse_oracle_test.go run Pi's proxy.js against the same response bodies as StreamProxy; D182 and D183 record the two approved divergences)` | ✅ |
| `packages/agent/src/index.ts` | `(Go has no barrel exports)` | n/a |

## `packages/ai/src/`

| upstream | pig | status |
|---|---|---|
| `packages/ai/src/types.ts` | `ai/types.go + ai/model_compat_json.go + ai/provider_streams.go + ai/deferred_option.go + coding/extension/model_stream.go + coding/extension/host/subprocess/model_fetch.go + coding/extension/host/subprocess/runtime-node/model-fetch.mjs (per-request fetch carrier)`, `agent/standalone_loop.go`, `agent/agent.go`, `agent/proxy.go`, `ai/model_types.go`, `ai/images.go`, `ai/messages.go`, `ai/events.go`, `ai/model_utils.go`, `ai/types_unions.go`, `ai/classifier.go`, `ai/transcript.go`, `ai/models_runtime.go`, `ai/known_provider.go`, `ai/provider_env.go`, `ai/models_provider.go`, `ai/openai_responses.go`, `ai/simple_options.go`, `ai/azure_config.go` | ✅ |
| `packages/ai/src/models.generated.ts` | `ai/models_generated.go (MODELS) + ai/image_models_generated.go (IMAGE_MODELS) + ai/classifier_models_generated.go (CLASSIFIER_MODELS) + ai/providers_generated.go (the provider keys of the three records)` | ✅ |
| `packages/ai/src/models.ts` | `ai/models_runtime*.go + ai/models_provider.go + ai/models_catalog_codec.go + ai/models_options.go + ai/model_utils.go (native provider collection, auth, publication, streams and model helpers; complete models-runtime.test.ts cases)`, `ai/classifier.go`, `ai/types.go` | ✅ |
| `packages/ai/src/models-store.ts` | `ai/models_store.go (ModelsStoreEntry, ModelsStore, InMemoryModelsStore; ai/models_store_test.go, ai/radius_test.go TestRadiusProviderRestoresStoredCatalogOffline)`, `ai/operation_options.go` | ✅ |
| `packages/ai/src/model-catalog.ts` | `ai/model_catalog.go (ChatModelCatalog, ImageModelCatalog and ClassifierModelCatalog, the ordered per-provider records) + cmd/gen-models/main.go (collectRows: the flatten of the API-grouped catalog, run at generation time into ai/models_generated.go where upstream runs flattenModelCatalog per provider at module load; the Go catalog holds the same records in the same order); cmd/gen-models/main_test.go (TestCollectCatalogSplitsTypesInBarrelOrder, TestCollectCatalogOrdersNumericIDsLikeObjectFromEntries, TestCollectCatalogListsEveryBarrelProviderInBarrelOrder) and ai/builtin_catalog_upstream_test.go (TestBuiltinCatalogsMatchPinnedProviderDataModules compares every generated record with the pinned provider data)` | ✅ |
| `packages/ai/src/providers/all.ts` | `ai/builtin_providers.go` (builtinModels, builtinProviders, getBuiltinModels, getAllBuiltinModels, getBuiltinClassifierModel and getBuiltinClassifierModels), `ai/images_registry.go` (getBuiltinImageModel and getBuiltinImageModels), `ai/compat.go` (getBuiltinModel), `ai/registry.go` (getBuiltinProviders and getBuiltinModelDataGeneratedAt); the radiusProvider re-export is ai/radius NewRadiusProvider and the register_builtins wiring holds no member of this file | ✅ |
| `packages/ai/src/providers/compat-schema.ts` | `internal/codingagent/configschema/compat_a.go + internal/codingagent/configschema/compat_b.go (schema trees; the typed Go compat structs carry the runtime shape); byte-equal to the pinned Pi package schemas by TestRenderedSchemasAreByteEqualToThePinnedPiPackage`, `ai/types.go`, `ai/openai.go`, `ai/types_unions.go`, `ai/system_one.go` | ✅ |
| `packages/ai/src/providers/model-schema.ts` | `internal/codingagent/configschema/model.go (schema trees); TestRenderedSchemasAreByteEqualToThePinnedPiPackage`, `ai/types.go` | ✅ |
| `packages/ai/src/providers/schema-builders.ts` | `internal/codingagent/configschema/pischema.go (OpenObject, StringLiteral, StringRecord; OneOf)` | ✅ |
| `packages/ai/src/api/anthropic-messages.ts` | `ai/direct_simple.go + ai/anthropic.go + ai/anthropic_stream.go + ai/anthropic_client.go + ai/anthropic_federation.go + ai/provider_request_options.go (createClient auth/header branches including the ai.FormatUserAgent(nil) default User-Agent (D65), betas, OAuth Claude Code identity, tool-name conversion, strict JSON-schema tools across current, initial, and deferred declarations, toolChoice, metadata.user_id, complete stop-reason and incomplete-stream errors, thinking_tokens usage, usage retention on error, explicit-zero temperature compatibility, managed mid-conversation effort with historical/current effort markers, adaptive drop-block binding, beta selection, and providerThinkingLevel, and native mid-conversation tool changes with tool_addition/tool_removal blocks, deferred placeholder/later tools, beta selection, and safe fallback to the current tool list; the executor-turn body pipeline with its Node 24.19/26.7 tick-order differential in ai/anthropic_microtask_trace_test.go and ai/anthropic_stream_test.go over coding/testdata/rpc33-observation/providers/anthropic-messages; upstream 0.99.2 workload identity federation in ai/anthropic_federation.go (getAnthropicFederation, token exchange, expiry refresh, the per-config federation client cache; ai/anthropic_federation_upstream_test.go, ai/anthropic_federation_sdk_upstream_test.go, ai/anthropic_token_cache_test.go) and the strict-tool keyword fallback (ai/anthropic.go anthropicStrictUnsupportedKeyword; ai/anthropic_strict_tool_schema_upstream_test.go, ai/anthropic_strict_keywords_test.go); tests in ai/anthropic_oauth_test.go, ai/anthropic_test.go, ai/anthropic_contract_0861_test.go, ai/anthropic_effort_strict_test.go, ai/constrained_sampling_test.go, ai/transcript_tool_changes_test.go, and agent/native_tool_changes_test.go); server-side fallback (allowedFallbackModels fallbacks param, server-side-fallback-2026-07-01 beta, fallback content block and cost) in ai/anthropic_fallback_request_upstream_test.go`, `ai/types.go`, `ai/compat.go` | ✅ |
| `packages/ai/src/api/constrained-sampling.ts` | `ai/constrained_sampling.go + ai/provider_kit.go (the exported getJsonSchemaToolParameters, prefersJsonSchemaStrictSampling and resolveJsonSchemaStrictSampling entry points over constrained_sampling.go; imported schema-key order, kept by the object-order helper beside it;grammar + canonical JSON-schema strict constrained sampling; wired into Anthropic, OpenAI Completions, OpenAI Responses, Mistral, Bedrock, and Google request paths; unit and provider-wire tests in ai/constrained_sampling_test.go, ai/anthropic_test.go, ai/openai_test.go, ai/openai_responses_constrained_test.go, ai/mistral_test.go, ai/providers/bedrock/bedrock_test.go, and ai/google_test.go)` | ✅ |
| `packages/ai/src/api/azure-openai-config.ts` | `ai/azure_config.go (ResolveAzureBaseURL, ResolveAzureConfig, ResolveAzureDeploymentName)`, `ai/types.go` | ✅ |
| `packages/ai/src/api/azure-openai-responses.ts` | `ai/direct_simple.go + ai/azure_openai_responses.go (logical model versus deployment identity and reasoning replay)`, `ai/types.go`, `ai/compat.go` | ✅ |
| `packages/ai/src/api/bedrock-converse-stream.ts` | `ai/providers/bedrock/bedrock.go + ai/direct_simple.go (simple API dispatch preserves the selected model and delegates optional/keyless authentication to Bedrock)`, `ai/types.go`, `ai/compat.go` | ✅ |
| `packages/ai/src/api/google-generative-ai.ts` | `ai/google.go + ai/direct_simple.go (SDK system-instruction user role and optional/strict/explicit function-calling mode; request guards in ai/provider_wire_defaults_test.go)`, `ai/types.go`, `ai/compat.go` | ✅ |
| `packages/ai/src/api/google-vertex.ts` | `ai/google_vertex.go`, `ai/types.go`, `ai/compat.go`, `ai/direct_simple.go` | ✅ |
| `packages/ai/src/api/mistral-conversations.ts` | `ai/mistral.go + ai/mistral_reader.go + ai/mistral_response_body.go + ai/direct_simple.go + internal/nodeurl/resolve.go + internal/nodeurl/serialize.go (SDK payload lowering, WHATWG request URLs, request lifetime, keyed tool fragments, selected-model replay through TransformMessages and reasoning controls; ai/mistral_http_transport_upstream_test.go + ai/mistral_reasoning_mode_upstream_test.go + ai/mistral_regression_test.go + ai/mistral_replay_transform_test.go + ai/mistral_ticks_test.go + ai/mistral_oracle_test.go + coding/cli/rpc_mistral_oracle_test.go + coding/mistral_replay_metadata_test.go)`, `ai/types.go`, `ai/compat.go` | ✅ |
| `packages/ai/src/api/anthropic-messages.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/azure-openai-responses.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/bedrock-converse-stream.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/google-generative-ai.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/google-vertex.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/mistral-conversations.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/openai-codex-responses.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/openai-completions.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/openai-responses.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/openrouter-images.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/pi-messages.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/auth/context.ts` | `ai/auth_resolve.go` | ✅ |
| `packages/ai/src/auth/credential-store.ts` | `ai/in_memory_credential_store.go (literal credentials, insertion order, per-provider mutation chains and independent cancellation)` | ✅ |
| `packages/ai/src/auth/helpers.ts` | `ai/auth_login.go + ai/auth_providers.go`, `ai/auth_lazy.go` | ✅ |
| `packages/ai/src/auth/resolve.ts` | `ai/auth_resolve.go` | ✅ |
| `packages/ai/src/auth/types.ts` | `ai/auth.go + ai/auth_store.go + ai/auth_resolve.go + ai/auth_interaction.go + ai/credential_json.go + ai/oauth_types.go`, `ai/operation_options.go` | ✅ |
| `packages/ai/src/auth/oauth/anthropic.ts` | `ai/oauth_anthropic.go` | ✅ |
| `packages/ai/src/auth/oauth/device-code.ts` | `ai/oauth_device_code.go` | ✅ |
| `packages/ai/src/auth/oauth/github-copilot.ts` | `ai/githubcopilot.go + ai/copilot_verification_uri.go + ai/oauth_registry.go + ai/auth_providers.go (device notifications, shared RFC 8628 polling, account catalog refresh and enterprise credential routing)` | ✅ |
| `packages/ai/src/auth/oauth/load.ts` | `ai/oauth_registry.go` | ✅ |
| `packages/ai/src/auth/oauth/kimi-coding.ts` | `ai/oauth_kimi.go (device flow and refresh ported; caller-path parity pending)` | ✅ |
| `packages/ai/src/auth/oauth/openrouter.ts` | `ai/oauth_openrouter.go (OpenRouter OAuth login/exchange; registered in oauth_registry.go; unit-tested + oauth/08 behavioral scenario)` | ✅ |
| `packages/ai/src/auth/oauth/openai-codex.ts` | `ai/oauth_openai_codex.go` | ✅ |
| `packages/ai/src/auth/oauth/pkce.ts` | `ai/pkce.go` | ✅ |
| `packages/ai/src/auth/oauth/radius.ts` | `ai/oauth_radius.go (ai/oauth_radius_test.go)` | ✅ |
| `packages/ai/src/auth/oauth/trusted-url.ts` | `ai/oauth_trusted_url.go trustedHTTPURL (shared by ai/githubcopilot.go via ai/copilot_verification_uri.go, ai/oauth_kimi.go, ai/oauth_meta.go and ai/oauth_radius.go; ai/trusted_url_oracle_test.go compares it with Pi's trustedHttpUrl over the verification-URI corpus)` | ✅ |
| `packages/ai/src/auth/oauth/xai.ts` | `ai/oauth_xai.go (device-code login, polling, refresh, response validation, and owner-context cancellation; ai/oauth_xai_test.go including LoginContext/RefreshTokenContext cancellation, coding/cli/model_oauth_refresh_test.go caller cancellation, oauth/09 behavioral scenario)` | ✅ |
| `packages/ai/src/bun-oauth.ts` | `(Bun-only OAuth flow registration; Go links OAuth implementations directly)` | n/a |
| `packages/ai/src/compat.ts` | `ai/compat.go`, `ai/builtin_providers.go`, `ai/registry.go`, `ai/faux.go` | ✅ |
| `packages/ai/src/compat/extension-oauth-types.ts` | `ai/oauth_types.go (type-only legacy OAuth callback and prompt contracts; runtime OAuth behavior belongs to the auth/provider rows; the file re-exports OAuthCredentials, whose JSON member order and tags are the observable wire contract, so the red-proof mutants edit that credential shape in ai/oauth_types.go)` | ✅ |
| `packages/ai/src/legacy-api-aliases.ts` | `ai/api_streams.go (all 16 deprecated streamX/streamSimpleX aliases as exported Go functions, bound to their API; ai/compat_registry_test.go TestLegacyAPIAliasesAreBoundToTheirAPI. A setup failure such as a missing API key ends the returned stream with an error event as lazy.ts lazyStream does (TestLegacyAPIAliasesDeliverSetupFailuresOnTheStream); an alias builds its own API's request from the model's fields with no model.api check (TestLegacyAPIAliasesSendTheirAPIsRequestWhateverTheModelsApi); the plain and simple forms differ by the model token limit (TestAPIStreamsSendPlainAndSimpleOptions); the reply carries the model's api for the anthropic, mistral, openai-completions, openai-responses and pi-messages modules and the module's own api for azure, google, google-vertex, bedrock and codex (apiModuleOutputAPI; TestLegacyAPIAliasesStampTheReplyLikeTheirModule))` | ✅ |
| `packages/ai/src/providers/openai-codex.ts` | `ai/provider_constructors.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/openai.ts` | `ai/provider_constructors.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/utils/error-body.ts` | `ai/errors.go + internal/jsonstringify/json.go (shared normalization, SDK extraction, bounded text and JSON fallback; consumed by the OpenAI Completions and Responses error paths in ai/openai_error_test.go and by the Bedrock provider; ai/error_body_upstream_test.go)` | ✅ |
| `packages/ai/src/utils/estimate.ts` | `ai/estimate.go (EstimateContextTokens, EstimateMessageTokens, CalculateContextTokens; estimate_test.go)`, `agent/context_tokens.go (CalculateContextTokens)` | ✅ |
| `packages/ai/src/utils/retry.ts` | `ai/assistant_retry.go (RetryAssistantCall, RetryDelayMs, IsRetryableAssistantError; assistant_retry_test.go)` | ✅ |
| `packages/ai/src/utils/provider-retry.ts` | `ai/provider_retry.go` | ✅ |
| `packages/ai/src/utils/openrouter-cost.ts` | `ai/openrouter_cost.go (isOpenRouterModel, validOpenRouterCost for isValidCost, applyOpenRouterReportedCost; ai/openrouter_cost_test.go)` | ✅ |
| `packages/ai/src/providers/amazon-bedrock.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/ant-ling.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/anthropic.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/azure.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/cerebras.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/cloudflare-ai-gateway.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/cloudflare-workers-ai.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/deepseek.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/fireworks.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/github-copilot.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/google-vertex.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/google.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/groq.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/huggingface.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/kimi-coding.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/minimax-cn.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/minimax.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/mistral.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/moonshotai-cn.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/moonshotai.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/nvidia.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/openai-codex.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/openai.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/opencode-go.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/opencode.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/openrouter.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/qwen-token-plan.models.ts` | `ai/models_generated.go (0.81 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/qwen-token-plan-cn.models.ts` | `ai/models_generated.go (0.81 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/together.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/vercel-ai-gateway.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/xai.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/xiaomi-token-plan-ams.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/xiaomi-token-plan-cn.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/xiaomi-token-plan-sgp.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/xiaomi.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/zai-coding-cn.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/zai.models.ts` | `ai/models_generated.go (0.80 catalog shard input)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/ant-ling.ts` | `ai/provider_constructors.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/cerebras.ts` | `ai/provider_constructors.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/cloudflare-ai-gateway.ts` | `ai/provider_constructors.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/cloudflare-auth.ts` | `ai/auth_providers.go` (cloudflareWorkersAIAuth and cloudflareAIGatewayAuth: key, account and gateway resolution per field, login prompts); the login prompt plumbing (provider_login), catalog metadata (models_generated) and runtime registration (codingagent model_registry) are consumers, not ports of this file | ✅ |
| `packages/ai/src/providers/cloudflare-stream.ts` | `ai/cloudflare_streams.go` (ResolveCloudflareModel, CloudflareStreams: wraps the cloudflare-workers-ai and cloudflare-ai-gateway API streams via builtinStreamWrappers) + `ai/system_one.go` (CloudflareClassifier); provider_wrap (WrapBuiltinProvider) applies the same wrappers to the per-model providers the coding runtime builds | ✅ |
| `packages/ai/src/providers/cloudflare-workers-ai.ts` | `ai/builtin_providers.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/data-json.d.ts` | `(TypeScript JSON import declaration only)` | n/a |
| `packages/ai/src/providers/deepseek.ts` | `ai/provider_constructors.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/fireworks.ts` | `ai/provider_constructors.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/github-copilot.ts` | `ai/github_copilot_filter.go (filterModels, githubCopilotSystemOne: the /v1 prefix of the Microsoft-Decision-1 classifier; classifier-models.test.ts cases in TestClassifierModelsUpstream)`, `ai/builtin_providers.go (builtinClassifiers)` (provider metadata and catalog from the generated model table ai/models_generated, registered through the coding-agent model registry internal/codingagent/model_registry), `ai/provider_constructors.go` | ✅ |
| `packages/ai/src/providers/groq.ts` | `ai/provider_constructors.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/huggingface.ts` | `ai/builtin_providers.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/kimi-coding.ts` | `ai/provider_constructors.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/minimax-cn.ts` | `ai/provider_constructors.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/minimax.ts` | `ai/provider_constructors.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/moonshotai-cn.ts` | `ai/provider_constructors.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/moonshotai.ts` | `ai/provider_constructors.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/nvidia.ts` | `ai/provider_constructors.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/opencode-go.ts` | `ai/builtin_providers.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/opencode.ts` | `ai/provider_constructors.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/openrouter.ts` | (provider metadata and catalog from the generated model table ai/models_generated, registered through the coding-agent model registry internal/codingagent/model_registry), `ai/provider_constructors.go`, `ai/openrouter_user_models.go` | ✅ |
| `packages/ai/src/providers/qwen-token-plan.ts` | `ai/provider_constructors.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/qwen-token-plan-cn.ts` | `ai/provider_constructors.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/radius-config.ts` | `ai/radius_config.go (TestNormalizeRadiusGatewayURL, TestSanitizeRadiusGatewayConfigKeepsOnlyUpstreamShapedModels, TestGetRadiusModelsBindsLegacyCredentialCatalog and the TestLoadRadiusGatewayConfig* tests; test/parity/scenarios/model-runtime-store-catalog/11-radius-metadata-import.toml)`, `ai/auth.go` | ✅ |
| `packages/ai/src/providers/radius.ts` | `ai/radius.go (NewRadiusProvider, RadiusProviderOptions: id/name/gateway defaults, the shipped catalog for the default gateway, refreshed and stored catalogs replacing it; radius-provider.test.ts cases: TestRadiusProviderShipsPublicCatalogForDefaultGateway, TestRadiusProviderCustomGatewayHasNoPublicCatalog, TestRadiusProviderOverlaysRefreshedModels, TestRadiusProviderExposesNoModelsWhenTheOrganizationDisabledAll, TestRadiusProviderRestoresStoredCatalogOffline; the coding-agent composition of models.json Radius gateways belongs to the model-runtime and provider-composer rows)` | ✅ |
| `packages/ai/src/providers/together.ts` | `ai/builtin_providers_google_openai_together.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/vercel-ai-gateway.ts` | `ai/provider_constructors.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/xai.ts` | `ai/provider_constructors.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/xiaomi-token-plan-ams.ts` | `ai/provider_constructors.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/xiaomi-token-plan-cn.ts` | `ai/provider_constructors.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/xiaomi-token-plan-sgp.ts` | `ai/provider_constructors.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/xiaomi.ts` | `ai/builtin_providers.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/zai-coding-cn.ts` | `ai/provider_constructors.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/ai/src/providers/zai.ts` | `ai/provider_constructors.go` (provider metadata; the catalog rows come from the generated model table and register through the coding-agent model registry) | ✅ |
| `packages/coding-agent/src/modes/interactive/components/easter-egg-3d.lazy.ts` | `internal/codingagent/pig_logo_animation_play.go + internal/codingagent/startup_header.go + internal/codingagent/interactive_easter_eggs.go (header click and fullscreen /arminsayshi; D87: PiG's pig models; the dynamic import is designed out, Go links the module)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/easter-egg-3d.ts` | `internal/codingagent/pig_egg3d.go + internal/codingagent/pig_logo_animation.go + internal/codingagent/pig_logo_animation_play.go (D87: the Pi-logo kind with the pig of the PiG mark and its running pig; the Armin kind with the sprite's pig head)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/radius-login-selector.ts` | `internal/codingagent/radius_login_selector.go (Radius top-level login option shimmer; static rows proven by oauth/27-28, shimmer frames compared with the pinned Pi by TestRadiusLoginMenuRendersLikePi)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/status-indicator.ts` | `internal/codingagent/interactive_status.go + interactive_events.go + tui/status_indicator.go + tui/editor_status.go (working/compaction/retry border, branchSummary, summarization retry replacement and session-clear lifecycle)` | ✅ |
| `packages/coding-agent/src/rpc-entry.ts` | `(npm package export wrapper for --mode rpc; pig exposes the CLI mode directly)` | n/a |
| `packages/coding-agent/src/utils/image-process.ts` | `internal/imageprocessing/images.go` | ✅ |
| `packages/ai/src/api/lazy.ts` | `ai/provider_streams.go + ai/models_runtime_stream.go + coding/model_stream_forwarding.go (owned setup, promise reactions on the shared continuation executor, ordered reference forwarding, and result settlement; dynamic module loading is statically linked in Go)` | ✅ |
| `packages/agent/src/stream-fn.ts` | `agent/stream_fn.go (configured host stream fallback; Agent and the standalone loop read it through GetDefaultStreamFn, and NewAgent returns an error where Pi's constructor throws)` | ✅ |
| `packages/ai/src/env-api-keys.ts` | `ai/auth_env_keys.go` | ✅ |
| `packages/ai/src/session-resources.ts` | `ai/session_resources.go` | ✅ |
| `packages/ai/src/oauth.ts` | `ai/oauth_types.go (OAuthCredentials, OAuthAuthInfo, OAuthDeviceCodeInfo, OAuthLoginCallbacks, OAuthPrompt, OAuthSelectOption, OAuthSelectPrompt: the type-only re-exports)` | ✅ |
| `packages/ai/src/index.ts` | `ai/doc.go` | n/a |
| `packages/ai/src/cli.ts` | `coding/cli/auth_commands.go (login/logout)` | ✅ |
| `packages/ai/src/bedrock-provider.ts` | `ai/providers/bedrock/bedrock.go` | ✅ |
| `packages/ai/src/api/openai-completions.ts` | `ai/openai_error.go + ai/direct_simple.go + ai/openai.go + ai/provider_request_options.go (tool-choice/stream/cache/thinking-budget upstream tests, native source options, and SDK iterator boundaries; Kimi tool additions: ai/transcript_tool_changes_test.go)`, `ai/types.go`, `ai/compat.go` | ✅ |
| `packages/ai/src/api/openai-responses.ts` | `ai/openai_error.go + ai/openai_responses.go + ai/direct_simple.go + ai/provider_request_options.go`, `ai/types.go`, `ai/compat.go` | ✅ |
| `packages/ai/src/api/openai-responses-shared.ts` | `ai/openai_responses.go (SDK iterator and scratch lifecycle live in the shared OpenAI SDK stream decoder and native body iterator; additional_tools and synthetic tool_search tool additions: ai/transcript_tool_changes_test.go)` | ✅ |
| `packages/ai/src/api/openai-codex-responses.ts` | `ai/direct_simple.go + ai/openai_codex_responses.go + ai/openai_codex_http.go + ai/openai_codex_frames.go + ai/openai_codex_websocket.go + ai/openai_responses.go (native retries, source response observation and pointer timeouts, selected-model metadata and pricing, account/session scoped continuation; all 30 streaming case sites/39 rows, Model Runtime caller tests and exact Pi scenarios; live cache-affinity acceptance remains credential-gated)`, `ai/types.go`, `ai/compat.go` | ✅ |
| `packages/ai/src/api/openai-prompt-cache.ts` | `ai/openai_prompt_cache.go` | ✅ |
| `packages/ai/src/api/pi-messages.ts` | `ai/pi_messages.go + ai/pi_messages_events.go + ai/pi_messages_reader.go (ai/pi_messages_test.go, ai/pi_messages_oracle_test.go, ai/pi_messages_ticks_test.go, coding/cli/rpc_pi_messages_oracle_test.go, coding/model_test.go TestPiMessagesModelsJSONProviderStreamsThroughModelRuntime)`, `ai/types.go`, `ai/compat.go`, `ai/direct_simple.go` | ✅ |
| `packages/ai/src/providers/anthropic.ts` | `ai/auth_providers.go (anthropicAPIKeyAuth resolver)` (catalog and request paths ai/anthropic, ai/anthropic_client, ai/oauth_anthropic, internal/codingagent/model_registry, coding/model, coding/cli/model; the ai resolver (ai/auth_providers.go anthropicAPIKeyAuth) reads ANTHROPIC_AUTH_TOKEN as an Authorization bearer header, then ANTHROPIC_OAUTH_TOKEN, then ANTHROPIC_API_KEY, then upstream 0.99.2 workload identity federation from the five ANTHROPIC_* federation variables (ai/auth_env_keys, exchanged by ai/anthropic_federation); subscription OAuth login; stored OAuth and api_key credentials outrank ambient auth and reach serialized requests on both SDK and CLI model paths; tests TestAnthropicAuthTokenEnvResolution, TestAnthropicRequestAuthShapes, TestAnthropicEnvAuthStatus, TestAnthropicWorkloadIdentityFederation, TestAnthropicAuthTreatsEmptyContextValuesAsAbsent, TestAnthropicEnvAuthResolution, TestAnthropicEnvAuthAbsent, TestBuildModelAnthropicEnvAuthRequestShapes, TestBuildModelAnthropicAuthRequestShapes); federation-only env counts as anthropic auth on the registry and CLI paths and in the Node builtin-API bridge (TestHasEnvAuthAnthropicFederation, TestProviderAuthStatusAnthropicFederation, TestNodeBridgeFederationGate), `ai/provider_constructors.go` | ✅ |
| `packages/ai/src/providers/google.ts` | `ai/builtin_providers_google_openai_together.go` (GoogleProvider: id, name, base URL, Gemini API key auth, google-generative-ai catalog; compared with the pinned module by TestBuiltinProvidersMatchPinnedProviderModules) | ✅ |
| `packages/ai/src/api/google-shared.ts` | `ai/google.go` | ✅ |
| `packages/ai/src/providers/google-vertex.ts` | `ai/auth_providers.go` (vertexAuth: API key or ADC login and resolution), `ai/provider_constructors.go` (GoogleVertexProvider); the Vertex stream implementation (google_vertex) ports packages/ai/src/api/google-vertex.ts, not this file | ✅ |
| `packages/ai/src/providers/amazon-bedrock.ts` | `ai/auth_providers.go` (bedrockAuth: bearer token, AWS profile or credential-chain login and resolution), `ai/provider_constructors.go` (AmazonBedrockProvider); the Converse stream implementation (providers/bedrock) ports packages/ai/src/api/bedrock-converse-stream.ts, not this file | ✅ |
| `packages/ai/src/api/cloudflare.ts` | `ai/cloudflare.go` | ✅ |
| `packages/ai/src/providers/azure.ts` | `ai/azure.go` (azureStreams: Azure Chat Completions endpoint and deployment resolution over the shared OpenAI provider), `ai/provider_constructors.go` (AzureProvider); the Responses api (azure_openai_responses) ports packages/ai/src/api/azure-openai-responses.ts | ✅ |
| `packages/ai/src/providers/mistral.ts` | `ai/provider_constructors.go` (MistralProvider: id, name, base URL, Mistral API key auth, mistral-conversations catalog; compared with the pinned module by TestBuiltinProvidersMatchPinnedProviderModules) | ✅ |
| `packages/ai/src/api/github-copilot-headers.ts` | `ai/githubcopilot.go` | ✅ |
| `packages/ai/src/providers/faux.ts` | `ai/faux.go + ai/faux_deferred.go (queued streaming, canonical models, model-aware factories, caller-supplied timestamps, cache and deferred submit/fetch/cancel, fauxAssistantMessage and the cost and input-limit model options)`, `ai/faux_core.go` | ✅ |
| `packages/ai/src/api/transform-messages.ts` | `ai/transcript.go + agent/transform.go + ai/tool_flow.go + ai/transform_messages.go (shared provider tool-flow pass and OpenAI replay normalization; Agent retains D48 recovery)` | ✅ |
| `packages/ai/src/providers/images/register-builtins.ts` | `ai/openrouter_images.go (RegisterBuiltInImagesAPIProviders, GenerateImagesOpenRouter; the lazy import and its load-error result are JavaScript module loading)` | ✅ |
| `packages/ai/src/api/openrouter-images.ts` | `ai/openrouter_images.go`, `ai/images.go` | ✅ |
| `packages/ai/src/api/simple-options.ts` | `ai/simple_options.go` | ✅ |
| `packages/ai/src/utils/event-stream.ts` | `ai/event_stream.go + ai/event_stream_continuation.go + ai/assistant_observation.go + ai/assistant_publication.go + ai/stream_observation.go + ai/stream_continuation.go`, `ai/event_stream_generic.go` | ✅ |
| `packages/ai/src/utils/provider-env.ts` | `ai/provider_env.go (Bun empty process.env /proc fallback n/a in Go)` | ✅ |
| `packages/ai/src/images.ts` | `ai/images.go` | ✅ |
| `packages/ai/src/image-models.ts` | `ai/images_registry.go` | ✅ |
| `packages/ai/src/images-api-registry.ts` | `ai/images.go` | ✅ |
| `packages/ai/src/utils/hash.ts` | `ai/openai.go ShortHash (UTF-16 code units with uint32 Math.imul wrap), used by the Completions normalizeCompletionsToolCallID, the Responses foreign fc_ ids and the Mistral deriveMistralToolCallID (upstream oracles in ai/openai_responses_foreign_toolcall_test.go and ai/mistral_test.go)` | ✅ |
| `packages/ai/src/utils/headers.ts` | `ai/provider_response.go`, `ai/classifier_http.go` | ✅ |
| `packages/ai/src/utils/json-parse.ts` | `ai/json_repair.go (repairJSON, ParseJSONWithRepair) + ai/streaming_json.go (ParseStreamingJson; ai/json_parse_upstream_test.go)` | ✅ |
| `packages/ai/src/utils/streaming-json.ts` | `ai/streaming_json_parser.go (StreamingJSONParser, ToolCallArgumentsStream; ai/streaming_json_parser_oracle_test.go, ai/streaming_tool_arguments_upstream_test.go)` | ✅ |
| `packages/ai/src/utils/diagnostics.ts` | `ai/diagnostics.go (FormatThrownValue, ExtractDiagnosticError, CreateAssistantMessageDiagnostic, AppendAssistantMessageDiagnostic; ai/diagnostics_upstream_test.go) + ai/types.go (DiagnosticErrorInfo, AssistantMessageDiagnostic)` | ✅ |
| `packages/ai/src/utils/node-http-proxy.ts` | `ai/node_http_proxy.go` | ✅ |
| `packages/ai/src/utils/overflow.ts` | `ai/overflow.go (IsContextOverflow, IsRecoverableLength, GetOverflowPatterns; overflow_test.go)`, `internal/codingagent/auto_recovery.go`, `durable/core/turn/patterns.go (generated)` | ✅ |
| `packages/ai/src/utils/text.ts` | `ai/text.go + ai/transcript.go`, `ai/estimate.go` | ✅ |
| `packages/ai/src/utils/uuid.ts` | `ai/uuid.go (UUIDv7, uuidv7Generator: the process-wide last-ordinary-timestamp and 41-bit sequence state with an injected clock and random source; internal/codingagent session ids, the Codex WebSocket request id, the compaction summary routing session id and the bug-report id call it)` | ✅ |
| `packages/ai/src/utils/abort-signals.ts` | `(stdlib context.Context combines signals)` | n/a |
| `packages/ai/src/utils/sanitize-unicode.ts` | `ai/sanitize_unicode.go` | ✅ |
| `packages/ai/src/utils/string-enum.ts` | `ai/string_enum.go (StringEnum only, as an ordered pi-schema object)` | ✅ |
| `packages/ai/src/utils/validation.ts` | `ai/validate.go + ai/validate_arguments.go (pi-schema parse with coercion, errors and dotted paths)` | ✅ |
| `packages/ai/src/utils/version.ts` | `ai/user_agent.go (ProductVersion: PiG's composite release version stands for PI_AI_VERSION as the default version of a detailed User-Agent, D65; TestClientIdentityVersionDefaultsToTheProductVersion)` | ✅ |
| `packages/ai/src/providers/baseten.models.ts` | `(not ported: Baseten provider declined)` | n/a |
| `packages/ai/src/providers/baseten.ts` | `(not ported: Baseten provider declined)` | n/a |
| `packages/ai/src/utils/abort.ts` | `(AbortSignal/Promise racing: raceWithAbortSignal/operationSignal; pig uses context.Context cancellation + select (D3): runtime mechanics, designed out)` | n/a |
| `packages/ai/src/api/cloudflare-ai-binding.ts` | `ai/cloudflare_ai_binding.go (AIBinding, CreateAIBindingFetch and the auth sentinel as a library fetch adapter; no PiG product path builds a Workers binding, so it has no in-product caller; cloudflare-ai-binding.test.ts cases in ai/cloudflare_ai_binding_test.go; HTTPS AI Gateway stays in the Cloudflare base-URL constants)` | ✅ |
| `packages/ai/src/auth/oauth/meta.ts` | `ai/oauth_meta.go (device flow + Muse Code key mint, registered in ai/oauth_registry.go; meta-oauth.test.ts cases in ai/oauth_meta_test.go)` | ✅ |
| `packages/ai/src/providers/meta.models.ts` | `ai/models_generated.go (0.87.1 catalog shard; TestMetaCatalogMatchesPinnedProviderDefinition)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/meta.ts` | `ai/provider_constructors.go` (MetaProvider: id, name, base URL, META_API_KEY auth, Muse subscription OAuth, openai-responses catalog; TestMetaCatalogMatchesPinnedProviderDefinition, TestBuiltinProviderAuthMetaAndRadiusMethods and the pinned module comparison TestBuiltinProvidersMatchPinnedProviderModules) | ✅ |
| `packages/ai/src/providers/opencode-headers.ts` | `ai/opencode_headers.go` (WithOpenCodeSessionHeader: x-opencode-session from StreamOptions.SessionID for stream and streamSimple, case-insensitive caller override incl. null; opencode-provider-headers.test.ts cases: TestWithOpenCodeSessionHeaderPiCases, TestWithOpenCodeSessionHeaderLikeUpstream; the coding-agent model path adds the same header in its request-options wrapper, TestOpenCodeSessionHeaderMapsSessionIDWithoutCacheRetention) | ✅ |
| `packages/ai/src/providers/qwen-token-plan-individual.models.ts` | `ai/models_generated.go (0.87.1 catalog shard; qwen-token-plan-models.test.ts cases: TestQwenTokenPlanIndividualExposesExactlyDocumentedTextModels, TestQwenTokenPlanExposesTextModelsAndOmitsImageModels)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/qwen-token-plan-individual-cn.models.ts` | `ai/models_generated.go (Pi main c23b9ec1 catalog shard; qwen-token-plan-models.test.ts case: TestQwenTokenPlanIndividualCnExposesExactlyDocumentedTextModels)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/providers/qwen-token-plan-individual.ts` | `ai/provider_constructors.go` (QwenTokenPlanIndividualProvider: id, name, base URL, QWEN_TOKEN_PLAN_API_KEY auth, openai-completions catalog; qwen-token-plan-models.test.ts cases: TestQwenTokenPlanIndividualExposesExactlyDocumentedTextModels, TestQwenTokenPlanExposesTextModelsAndOmitsImageModels, and the pinned module comparison TestBuiltinProvidersMatchPinnedProviderModules) | ✅ |
| `packages/ai/src/providers/qwen-token-plan-individual-cn.ts` | `ai/provider_constructors.go` (QwenTokenPlanIndividualCnProvider: id, name, base URL, QWEN_TOKEN_PLAN_CN_API_KEY auth, openai-completions catalog; qwen-token-plan-models.test.ts cases: TestQwenTokenPlanIndividualCnExposesExactlyDocumentedTextModels, TestEnvAPIKeysQwenIndividualCnUsesChinaTokenPlanVariable) | ✅ |
| `packages/ai/src/providers/radius.models.ts` | `ai/models_generated.go (0.87.1 radius shard, served for the default gateway by the radius provider: ai/registry_test.go TestRuntimeDiscoveryIncludesRadiusCatalog, ai/radius_test.go TestRadiusProviderShipsPublicCatalogForDefaultGateway)`, `ai/builtin_providers.go`, `ai/images_registry.go` | ✅ |
| `packages/ai/src/utils/identity.ts` | `ai/identity.go (ClientIdentity, UserAgentMode, ClientAttribution, MergeClientIdentity, FormatUserAgent, GetAttributionHeaders, GetOpenCodeSessionHeaders, PiAttribution) and ai/identity_models.go (WithClientIdentity over ClientIdentityModels: Go has no Proxy); every API module applies the identity headers below model and request headers (Bedrock adds the product to the AWS SDK user agent); PiG's product and attribution values are D65 and D26 (the release in the user agent comes from uname on unix and RtlGetVersion on Windows); ai/identity_test.go, ai/identity_oracle_test.go TestProviderIdentityHeadersMatchPi and ai/identity_images_oracle_test.go TestOpenRouterImagesHeadersMatchPi compare the headers with the pinned Pi; every identity.test.ts case is ported (test-mapping-v1.3.0+bd3c9b82.json)` | ✅ |
| `packages/coding-agent/src/core/client-identity.ts` | `coding/client_identity.go (getClientIdentity: PiG's attribution while install telemetry is on, none otherwise; set per request in Session.buildRequestOptions); coding/client_identity_test.go, sdk-client-identity.test.ts ported (D26)` | ✅ |
| `packages/ai/src/utils/sleep.ts` | `ai/abort_utils.go (Sleep: Node setTimeout delay semantics, immediate rejection when already aborted, rejection with the cancellation cause on abort mid-wait; ai/abort_utils_oracle_test.go)` | ✅ |
| `packages/ai/src/utils/transcript.ts` | `ai/transcript.go (system-message-replay.test.ts cases and provider fold payloads for anthropic-messages, openai-responses, openai-completions in ai/system_message_replay_test.go; resolveTranscriptTools and the native transcript-tool-changes.test.ts cases in ai/transcript_tool_changes_test.go)`, `ai/types_unions.go` | ✅ |
| `packages/ai/src/api/cloudflare-workers-ai-system-one.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/cloudflare-workers-ai-system-one.ts` | `ai/system_one.go (Cloudflare Workers AI system-one classifier API)` | ✅ |
| `packages/ai/src/api/llama-cpp-classify.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/llama-cpp-classify.ts` | `ai/llama_cpp_classify.go` | ✅ |
| `packages/ai/src/api/system-one-shared.ts` | `ai/system_one.go` | ✅ |
| `packages/ai/src/api/classifier-shared.ts` | `ai/classifier_http.go (ClassifierHTTPError, PostClassifierRequest, ParseClassifierUsage, IsRecord; RequiredNumber is ai/system_one.go; the retry and timeout seams of classifierPost)` | ✅ |
| `packages/ai/src/api/openai-decisions.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked; the API itself is ai/openai_decisions.go OpenAIDecisionsAPI)` | n/a |
| `packages/ai/src/api/openai-decisions.ts` | `ai/openai_decisions.go (ClassifyOpenAIDecisions: request input, choice, score and predicate decisions, probabilities, answer parsing)` | ✅ |
| `packages/ai/src/api/typesafe-system-one.lazy.ts` | `(lazy dynamic-import wrapper; Go providers are statically linked)` | n/a |
| `packages/ai/src/api/typesafe-system-one.ts` | `ai/system_one.go (typesafe system-one classifier API)` | ✅ |
| `packages/ai/src/auth/oauth/callback-server.ts` | `ai/oauth_callback_server.go` | ✅ |
| `packages/ai/src/auth/oauth/openai-chatgpt.ts` | `ai/oauth_openai_chatgpt.go` | ✅ |
| `packages/ai/src/providers/typesafe.models.ts` | `ai/classifier_models_generated.go (the generated jev-latest classifier shard)`, `ai/images_registry.go`, `ai/registry.go` | ✅ |
| `packages/ai/src/providers/typesafe.ts` | `ai/builtin_providers.go (TypesafeProvider)` | ✅ |
| `packages/ai/src/utils/model-operations.ts` | `ai/model_types.go (ModelType and the per-type Models operations)`, `ai/model_operations_export.go` | ✅ |
| `packages/ai/src/utils/models-error.ts` | `ai/auth_resolve.go (ModelsErrorCode, ModelsError, NewModelsError)` | ✅ |
| `packages/ai/src/utils/oauth-page.ts` | `ai/oauth_page.go (upstream 0.99.1 moved the file unchanged from auth/oauth/oauth-page.ts)` | ✅ |
| `packages/ai/src/providers/openrouter-api.ts` | `ai/openrouter_api.go (OpenRouterModel, OpenRouterCost and the rest of the row's exported members), cmd/gen-models/` | ✅ |
| `packages/ai/src/telemetry.ts` | `ai/telemetry.go` | ✅ |
| `packages/ai/src/lazy-bedrock-provider.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/ai/src/lazy-oauth.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |

## `packages/coding-agent/src/`

| upstream | pig | status |
|---|---|---|
| `packages/coding-agent/src/cli.ts` | `coding/cli/main.go` | ✅ |
| `packages/coding-agent/src/main.ts` | `coding/cli/main.go + coding/cli/startup_session.go + coding/cli/extensions.go (mode/metadata routing) + coding/cli/auth_run.go (runAuthCommand); deleted working directory check (coding/cli/main.go)` | ✅ |
| `packages/coding-agent/src/index.ts` | `coding/extension/host/subprocess/runtime-node/shims/pi-coding-agent.mjs (pinned Node barrel; independent SDK objects; main-process identity remains D73)` | ✅ |
| `packages/coding-agent/src/config.ts` | `internal/codingagent/paths.go + internal/codingagent/selfupdate_package_command.go + internal/codingagent/selfupdate_tier.go (D2 directory opt-in; D39 native update ownership); the 1.0.3 quickJSWasmPath cache and in-memory codemode worker URL are designed out: PiG embeds its code in one native executable, as Pi's Bun binary does; detectInstallChange is ported as the additive D95 in internal/installchange/installchange.go`, `internal/codingagent/windows_self_update.go`, `coding/upstream.go` | ✅ |
| `packages/coding-agent/src/migrations.ts` | `internal/codingagent/migrations.go` | ✅ |
| `packages/coding-agent/src/package-manager-cli.ts` | `coding/cli/package_commands.go + coding/cli/cli_error.go + coding/cli/package_command_trust.go + coding/cli/package_model_catalogs.go + coding/cli/config_command.go + coding/packagecontent/packagecontent.go; Pi 1.5.0 pruneManagedReleases (#10392, keeps the new and the previous release) has no managed release directory to prune under D39: a standalone replacement keeps only the new executable, locked by internal/codingagent/selfupdate_test.go TestSelfReplaceAtWithCommitKeepsOnlyTheNewExecutable)` | ✅ |
| `packages/coding-agent/src/cli/args.ts` | `coding/cli/args.go (the PI_SELF_UPDATE_COMMAND help line is in the generated coding/cli/help_upstream.txt)` | ✅ |
| `packages/coding-agent/src/cli/initial-message.ts` | `coding/cli/main.go` | ✅ |
| `packages/coding-agent/src/cli/file-processor.ts` | `internal/codingagent/file_processor.go` | ✅ |
| `packages/coding-agent/src/cli/list-models.ts` | `coding/cli/model.go (printModelList, called from coding/cli/main.go for --list-models)` | ✅ |
| `packages/coding-agent/src/cli/config-selector.ts` | `coding/cli/config_command.go + tui/config_selector.go` | ✅ |
| `packages/coding-agent/src/cli/credential-print.ts` | `coding/cli/credential_print.go + internal/codingagent/request_auth_runtime.go (request-auth surface: getProviders/getAuth/listCredentials with OAuth refresh+persist) + ai/auth_resolve.go` | ✅ |
| `packages/coding-agent/src/cli/session-picker.ts` | `internal/codingagent/startup_ui.go (SelectStartupSession, hosting the SessionSelectorComponent of internal/codingagent/session_selector.go; the --resume call site is main.ts's, in the cli package)` | ✅ |
| `packages/coding-agent/src/cli/project-trust.ts` | `coding/cli/project_trust.go + coding/cli/project_trust_context.go` | ✅ |
| `packages/coding-agent/src/cli/startup-ui.ts` | `internal/codingagent/startup_ui.go (ShowStartupSelector/ShowStartupInput/SelectStartupSession; startStartupTui color-scheme + OSC 11 background query with reply consumption). First-time setup: upstream shouldRunFirstTimeSetup (startup-ui.ts:122-140, called from main.ts:674) returns false unless package @earendil-works/pi-coding-agent, app pi and .pi config dir; PiG's own gate (D88) is shouldRunFirstTimeSetup in the cli package's extensions source and showFirstTimeSetup is ShowFirstTimeSetup, mapped under the first-time-setup component's row, run on its own startup screen before runtime services as main.ts:674 does. The four shouldRunFirstTimeSetup cases in test/first-time-setup.test.ts stay designedOutCases (their real-CLI inputs assert PiG's D88 gate) and its four analytics cases stay ported` | ✅ |
| `packages/coding-agent/src/core/sdk.ts` | `coding/session.go + coding/resource_loader.go (resourceLoader option; DefaultResourceLoader resolves skills, prompt templates, context files and SYSTEM.md/APPEND_SYSTEM.md from the default and settings-configured locations, ~/.agents/skills, ancestor .agents/skills, configured Packages (installing missing npm and git ones) and additional paths under project trust, with Pi's resource metadata and option and override callbacks; extensions, themes, extension-discovered resources and inline extension factories stay with the Extension Host and the theme controller) + coding/session_tool_registry.go + coding/session_extension_hooks.go + coding/session_cache_warming.go + coding/runtime.go + coding/services.go + coding/extension/host/subprocess/runtime-node/shims/independent-session.mjs (independent Node Session composition and owned shutdown)`, `coding/create_agent_session.go`, `coding/runtime_replacement.go; Session.runtimeStream set by CreateAgentSession (markRuntimeStreamFunction)` | ✅ |
| `packages/coding-agent/src/core/agent-session.ts` | `coding/session.go + coding/session_boundaries.go + coding/session_bind.go + coding/session_replaced.go + coding/session_custom_messages.go + coding/session_prompt_resources.go + internal/codingagent/skills.go + coding/session_summarization_auth.go + coding/session_model_cycle.go + coding/session_prompt.go + coding/session_scoped_models.go + coding/session_preflight.go + coding/session_retry_scheduler.go + coding/session_transcript.go + coding/session_extension_hooks.go + coding/session_tools.go + coding/session_tool_registry.go + internal/codingagent/prompts/tool_definitions.go + internal/codingagent/auto_recovery.go (SDK tool-registry, abort/admission and next-turn tool prompt regressions ported; every covering test file is ported in the reviewed test mapping; the forced-prompt request shape and the JSON round-trip case of system-prompt-updates.test.ts are ported in coding/session_system_prompt_updates_upstream_test.go)`, `coding/session_run_state.go`, `coding/session_state.go`, `coding/session_cache_warming.go`, `coding/session_loadout.go`, `coding/session_recovery.go`, `coding/virtual_models.go`, `coding/session_manager.go`, `coding/bug_report.go`, `coding/services.go`, `coding/runtime_replacement.go`, `agent/agent.go`, `internal/codingagent/session_handle.go; Session.runtimeStream (markRuntimeStreamFunction)` | ✅ |
| `packages/coding-agent/src/core/agent-session-runtime.ts` | `coding/runtime.go, coding/runtime_replacement.go, coding/session.go, coding/session_ops.go, coding/session_extension_replacement.go, coding/session_import.go, and internal/codingagent/session_selectors.go; native factory/replacement callbacks and retirement implemented; CLI/extension factory ownership and subprocess callback transport follow D30 and D61` | ✅ |
| `packages/coding-agent/src/core/agent-session-services.ts` | `coding/services.go`, `internal/codingagent/settings_diagnostics.go`, `coding/create_agent_session.go` | ✅ |
| `packages/coding-agent/src/core/auth-storage.ts` | `ai/auth.go + ai/auth_reload.go + ai/auth_store.go + ai/auth_mutex.go + internal/pilock/lock.go (shared directory locks and reader-owned reloads; CredentialStore, ReadOnlyAuthStorage, in-memory store; Pi shared-file round-trip/concurrent-writer tests)`, `coding/auth_storage.go` | ✅ |
| `packages/coding-agent/src/core/auth-guidance.ts` | `internal/codingagent/auth_guidance.go` | ✅ |
| `packages/coding-agent/src/core/bash-executor.ts` | `internal/codingagent/tools/bash_executor.go` | ✅ |
| `packages/coding-agent/src/core/cache-stats.ts` | `internal/codingagent/cache_stats.go + internal/codingagent/session_stats.go + internal/codingagent/model_cache_price.go` | ✅ |
| `packages/coding-agent/src/core/defaults.ts` | `internal/codingagent/defaults.go (DEFAULT_THINKING_LEVEL; THINKING_LEVEL_OPTIONS is designed out: its only Pi uses are agent-session.ts:2658, unreachable because agent.state.model is the unknown/unknown/unknown sentinel (agent/src/agent.ts:58), and the settings-selector availableThinkingLevels field that settings-selector.ts:75 declares and never reads, so a Go member would have no caller)` | ✅ |
| `packages/coding-agent/src/core/diagnostics.ts` | `coding/extension/diagnostics.go (ResourceCollision, ResourceDiagnostic)` | ✅ |
| `packages/coding-agent/src/core/trust-manager.ts` | `internal/codingagent/trust_manager.go + internal/pilock/lock.go (unit-tested + project-trust/01; Pi shared-file concurrent writers)` | ✅ |
| `packages/coding-agent/src/core/project-trust.ts` | `coding/cli/project_trust.go + internal/codingagent/trust_manager.go` | ✅ |
| `packages/coding-agent/src/core/experimental.ts` | `internal/codingagent/experimental.go` | ✅ |
| `packages/coding-agent/src/core/event-bus.ts` | `coding/extension/eventbus.go` | ✅ |
| `packages/coding-agent/src/core/http-dispatcher.ts` | `ai/http_transport.go + ai/transport_error.go + ai/http_proxy.go + internal/codingagent/settings.go` | ✅ |
| `packages/coding-agent/src/core/exec.ts` | `coding/extension/exec.go` | ✅ |
| `packages/coding-agent/src/core/footer-data-provider.ts` | `internal/codingagent/status_line.go + internal/codingagent/git_watcher.go + internal/codingagent/footer_data_provider.go`, `coding/extension/ui_factories.go` | ✅ |
| `packages/coding-agent/src/core/keybindings.ts` | `internal/codingagent/keybindings.go`, `internal/codingagent/app_keybinding.go` | ✅ |
| `packages/coding-agent/src/core/key-id-schema.ts` | `internal/codingagent/key_id_schema.go (KeyIDPattern, isKeyID) + internal/codingagent/keybindings_schema.go; TestKeybindingsSchemaValidatesKeySyntax` | ✅ |
| `packages/coding-agent/src/core/keybindings-schema.ts` | `internal/codingagent/keybindings_schema.go (tui.TUIKeybindingOrder + KeybindingsManager.KeybindingIDs)` | ✅ |
| `packages/coding-agent/src/core/messages.ts` | `agent/messages.go` | ✅ |
| `packages/coding-agent/src/core/model-config.ts` | `internal/codingagent/model_registry.go` | ✅ |
| `packages/coding-agent/src/core/pi-ai-schemas.ts` | `internal/codingagent/configschema/pi_ai_schemas.go (PartialModelCostSchema; the Type.Unsafe wrappers only change TypeBox's static type and are designed out)` | ✅ |
| `packages/coding-agent/src/core/model-registry.ts` | `internal/codingagent/model_registry.go + internal/codingagent/extension_model_registry.go + coding/extension/host/subprocess/provider_object.go + internal/codingagent/native_model_runtime.go + coding/model_runtime_native.go + coding/model_registry_facade.go + internal/codingagent/registry_request_auth.go`, `coding/services.go`, `coding/model_runtime_classify.go`, `coding/model.go`, `internal/codingagent/model_catalog_data.go`, `coding/virtual_models.go` | ✅ |
| `packages/coding-agent/src/core/model-runtime.ts` | `internal/codingagent/model_registry.go + internal/codingagent/provider_registration_validation.go + internal/codingagent/provider_registration_namespace.go + internal/codingagent/extension_model_registry.go + internal/codingagent/native_provider.go + coding/native_provider.go + internal/codingagent/native_model_runtime.go + internal/codingagent/native_credential_operations.go + coding/services.go + coding/model_availability.go + internal/codingagent/model_availability.go + internal/codingagent/model_refresh_background.go + internal/codingagent/model_tasks.go + coding/model_runtime_create.go + coding/model_catalog_backend.go + coding/registered_stream_provider.go + internal/codingagent/model_catalog_data.go + internal/codingagent/registry_request_auth.go + coding/model_runtime_native.go + coding/extension/host/subprocess/native_provider.go + internal/codingagent/request_auth_runtime.go (request-auth surface)`, `coding/model_runtime_classify.go`, `coding/model_runtime_typed.go`, `coding/model_runtime_members.go`, `coding/virtual_models.go`, `ai/auth_resolve.go` | ✅ |
| `packages/coding-agent/src/core/models-store.ts` | `ai/models_store.go + ai/models_store_read.go + internal/pilock/lock.go (FileModelsStore; upstream read-cache cases and Pi directory-lock/concurrent-writer regressions) + internal/codingagent/model_registry.go + internal/codingagent/radius_models.go (<agentDir>/models-store.json)` | ✅ |
| `packages/coding-agent/src/core/provider-composer.ts` | `coding/extension/provider.go + coding/registered_stream_provider.go + coding/model_stream_forwarding.go + internal/codingagent/provider_registration_namespace.go + internal/codingagent/model_registry.go + internal/codingagent/native_provider_composer.go + internal/codingagent/model_data.go + internal/codingagent/registry_request_auth.go + internal/codingagent/native_provider_oauth.go + internal/codingagent/request_auth_runtime.go (auth composition)` | ✅ |
| `packages/coding-agent/src/core/radius.ts` | `internal/codingagent/radius.go (internal/codingagent/radius_test.go)` | ✅ |
| `packages/coding-agent/src/core/remote-catalog-provider.ts` | `internal/codingagent/remote_catalog_provider.go (WithRemoteCatalog: TestRemoteCatalogProviderUpstream) + internal/codingagent/remote_catalog_registry.go (built-in provider wiring: TestRegistryAppliesTheRemoteCatalogOverlayToBuiltInProviders; TestModelCatalogProtocolWithTheCurrentClient); D64 routes the endpoint to pi-in-go.dev. The bundled catalog's generation time comes from cmd/gen-models, so a stored remote catalog applies only when newer (TestRemoteCatalogProviderUpstream)` | ✅ |
| `packages/coding-agent/src/core/runtime-credentials.ts` | `ai/runtime_credentials.go + internal/codingagent/model_registry.go` | ✅ |
| `packages/coding-agent/src/core/usage-totals.ts` | `internal/codingagent/session_stats.go + internal/usagetotals/usagetotals.go (combineUsage)` | ✅ |
| `packages/coding-agent/src/core/model-resolver.ts` | `coding/cli/model.go + coding/cli/startup_model.go + coding/cli/resolve_cli_model.go + coding/extension/scoped_models.go + internal/codingagent/default_models.go + internal/codingagent/model_pattern.go + internal/codingagent/model_scope.go`, `coding/model_resolver.go` | ✅ |
| `packages/coding-agent/src/core/output-guard.ts` | `internal/codingagent/output_guard.go` | ✅ |
| `packages/coding-agent/src/core/package-manager.ts` | `coding/packagecontent/packagecontent.go, coding/packagecontent/glob.go, coding/packagecontent/autoload_patterns.go, internal/resolvepath/resolvepath.go, internal/packagemanager/doc.go, internal/packagemanager/package_manager.go, internal/packagemanager/resolve.go, internal/codingagent/resource_paths.go, internal/packagemanager/manager.go, internal/packagemanager/git.go, internal/packagemanager/npm_metadata.go, internal/packagemanager/process.go, internal/packagemanager/progress.go, internal/packagemanager/capture_unix.go, internal/packagemanager/capture_windows.go, internal/packagemanager/temporary.go, internal/packagemanager/updates.go, internal/packagemanager/resources.go, internal/packagemanager/autoload.go, internal/packagemanager/package_npm_metadata.go, internal/packagemanager/package_update_operations.go, internal/packagemanager/package_updates.go, internal/packagemanager/package_update_sources.go, coding/cli/package_progress.go, coding/cli/package_resource_selection.go, coding/cli/configured_resources.go; changelog path: coding/packagecontent/pi_manifest.go ResolvePackageChangelogPath; unchanged-version warning: internal/packagemanager/package_update_operations.go reportUnchangedNpmUpdates; the ancestor .agents/skills exclusion of the user's ~/.agents/skills compares with resolvepath.PathsEqual (pathsEqual)` | ✅ |
| `packages/coding-agent/src/core/prompt-templates.ts` | `internal/codingagent/prompt_templates.go + prompt_diagnostics.go (file/YAML warnings reach interactive startup/reload and RPC; differential tests against the pinned Pi loader pass, frontmatter parsed by internal/yaml12, a port of the yaml 2.9.0 engine; CI-pending: the host zone of a date used as a mapping key on Windows (region-first CLDR mapping, TZ handling) is proven only by the native Windows test internal/yaml12/zonelocal_windows_test.go)`, `coding/session_prompt_resources.go` | ✅ |
| `packages/coding-agent/src/core/resolve-config-value.ts` | `internal/configvalue/configvalue.go + internal/configvalue/configured_shell.go + internal/configvalue/shell_unix.go (the win32 dispatch is built only on Windows, so its evidence runs in Windows CI and the Linux coverage run cannot cite it)` | ✅ |
| `packages/coding-agent/src/core/resource-loader.ts` | `coding/resource_loader.go (native DefaultResourceLoader for skills, prompt templates, context files and system prompt text) + internal/codingagent/resources.go + internal/codingagent/context_files.go + coding/cli/configured_resources.go (resolved skills precede additional --skill paths) + coding/cli/resource_prompts.go + internal/codingagent/prompt_templates.go + coding/cli/reload_resources.go + coding/cli/resource_source_info.go + internal/codingagent/extension_host_info.go (skill provenance projection) + coding/cli/headless_resources.go + internal/codingagent/reload_resources.go + internal/codingagent/extension_diagnostics.go (theme load diagnostics and first-name-wins deduplication)`, `coding/resource_loader_extend.go` | ✅ |
| `packages/coding-agent/src/core/session-cwd.ts` | `internal/codingagent/session_cwd.go + coding/runtime_replacement.go (persisted-only missing-CWD diagnostics and factory preflight)` | ✅ |
| `packages/coding-agent/src/core/session-manager.ts` | `internal/codingagent/session_manager.go + internal/codingagent/session_manager_accessors.go + coding/session_manager.go + internal/codingagent/session.go + internal/codingagent/session_resume.go + internal/codingagent/extension_session_read.go + internal/codingagent/session_restore.go + internal/codingagent/session_branch.go + internal/codingagent/session_branch_runtime.go + internal/codingagent/session_context.go + internal/codingagent/session_listing.go + internal/codingagent/session_file.go (entry identity, collision checks, clone label reconstruction, assistant-gated writes, migration, discovery, cancellable listing, explicit opening and settings-only branch restoration)`, `internal/sessionentry/entry.go`, `internal/codingagent/session_manager_projection.go`, `internal/sessionentry/union.go`, `internal/sessionentry/projection.go`, `coding/session_ops.go`, `coding/rpcclient/types.go` | ✅ |
| `packages/coding-agent/src/core/settings-manager.ts` | `coding/settings.go + internal/codingagent/settings.go + internal/codingagent/settings_compaction.go + internal/codingagent/file_lock.go + internal/pilock/lock.go (file and in-memory settings; shared-file round-trip/concurrent writers)`, `coding/services.go`; extensionChangelogVersions (GetExtensionChangelogVersion/SetExtensionChangelogVersion)` | ✅ |
| `packages/coding-agent/src/core/settings-defaults.ts` | `internal/codingagent/configschema/settings.go (settingsDefaults); the runtime defaults stay with the settings managers of coding and internal/codingagent` | ✅ |
| `packages/coding-agent/src/core/settings-schema.ts` | `internal/codingagent/configschema/settings.go (settingsSchema); TestSettingsSchemaRejectsWhatRuntimeAccessorsReject`, `internal/codingagent/settings.go`, `coding/services.go`, `tui/tui_mode.go; extensionChangelogVersions` | ✅ |
| `packages/coding-agent/src/core/skills.ts` | `internal/codingagent/skills.go + skills_loader.go + internal/codingagent/prompts/coding.go (differential tests against the pinned Pi loader pass; frontmatter parsed by internal/yaml12; the Windows zone item is recorded on prompt-templates.ts)` | ✅ |
| `packages/coding-agent/src/core/slash-commands.ts` | `internal/codingagent/slash_commands.go`, `internal/codingagent/extension_host_info.go`, `coding/extension/opaque_types.go` | ✅ |
| `packages/coding-agent/src/core/source-info.ts` | `internal/codingagent/resource_source_info.go + coding/cli/resource_source_info.go`, `internal/codingagent/extension_host_info.go`, `coding/source/source_info.go; coding/source/source_info.go SourceInfo.ChangelogPath, GetSourcePackageKey` | ✅ |
| `packages/coding-agent/src/core/system-prompt.ts` | `internal/codingagent/prompts/coding.go (default/explicit-empty tools and opaque forced prompt state) + internal/codingagent/prompts/custom_sections.go + coding/extension/system_prompt_options.go (collection-complete option normalization)` | ✅ |
| `packages/coding-agent/src/core/telemetry.ts` | `internal/codingagent/settings.go (IsInstallTelemetryEnabled, isTruthyTelemetryEnvFlag, TestSettingsManager_IsInstallTelemetryEnabled) gates the attribution headers (D26) and internal/codingagent/install_telemetry.go (reportInstallTelemetry, recordChangelogVersionAndMaybeReportInstall), wired at the same two call sites as interactive-mode.ts:1265-1307 by the interactive mode's Run, now sends the install/update ping to PiG's own endpoint (D64) instead of pi.dev; tests in internal/codingagent/install_telemetry_test.go` | ✅ |
| `packages/coding-agent/src/core/timings.ts` | `internal/codingagent/timings/timings.go` | ✅ |
| `packages/coding-agent/src/core/index.ts` | `(barrel)` | n/a |
| `packages/coding-agent/src/core/compaction/compaction.ts` | `internal/codingagent/compaction/compaction.go (internal/codingagent/compaction/compaction_summary_reasoning_test.go; coding/session_summarization_test.go)`, `agent/context_tokens.go`, `coding/session.go`, `internal/codingagent/session_handle.go`, `coding/compaction_api.go`, `internal/codingagent/compaction/estimate.go` | ✅ |
| `packages/coding-agent/src/core/compaction/branch-summarization.ts` | `internal/codingagent/compaction/branch_summarization.go (internal/codingagent/compaction/branch_summarization_test.go; coding/session_branch_summary_upstream_test.go verifies current Agent stream selection, request auth, usage and cancellation through NavigateTree)`, `ai/types.go` | ✅ |
| `packages/coding-agent/src/core/compaction/utils.ts` | `internal/codingagent/compaction/utils.go (file operations, extractFileOpsFromMessage including nested tool calls)`, `internal/compactiontypes/types.go` | ✅ |
| `packages/coding-agent/src/core/compaction/index.ts` | `(barrel)` | n/a |
| `packages/coding-agent/src/core/extensions/types.ts` | `coding/extension/api.go + coding/extension/command.go + coding/extension/context.go + coding/extension/context_actions.go + coding/extension/custom.go + coding/extension/events.go + coding/extension/extension.go + coding/extension/extension_error.go + coding/extension/extension_event.go + coding/extension/flag.go + coding/extension/handler.go + coding/extension/host_actions.go + coding/extension/keybinding_hints.go + coding/extension/mode.go + coding/extension/opaque_types.go + coding/extension/project_trust_context.go + coding/extension/provider.go + coding/extension/provider_runtime.go + coding/extension/renderer.go + coding/extension/replaced_session_context.go + coding/extension/runtime_registrations.go + coding/extension/runtime_state.go + coding/extension/scoped_models.go + coding/extension/shortcut.go + coding/extension/tool.go + coding/extension/tool_context.go + coding/extension/tool_loadout.go + coding/extension/tool_registry.go + coding/extension/ui.go + coding/extension/ui_factories.go + coding/extension/virtual_model.go + coding/session.go (Layer 0, including synchronized late tool registrations in coding/extension/tool_registry.go); setEditorComponent factories work in the Go, Rust, Python and Node SDKs (the delegated base is a fresh stock editor per factory), and addAutocompleteProvider uses retained current-provider factories across all SDKs; tracked in docs/extension-api-parity.md`, `coding/resource_loader_extend.go`, `coding/extension/markdowntransform/markdowntransform.go`, `internal/codingagent/tool_renderer_factory.go`, `coding/define_tool.go` | ✅ |
| `packages/coding-agent/src/core/extensions/runner.ts` | `coding/extension/host/inproc/runner.go + coding/extension_runner.go + coding/extension/host/inproc/tool_context.go + coding/extension/host/inproc/runner_accessors.go + coding/extension/host/inproc/mcp_servers.go + coding/extension/provider_runtime.go + coding/extension/host/inproc/cache_warming.go + coding/extension/host/subprocess/runtime-node/runtime.mjs (no-op UI); SDK abort/scoped-model binding, live UI/mode getters, user-bash validation, all-extension snapshots and prompt scopes are covered; every extensions-runner.test.ts, extensions-input-event.test.ts and extensions-discovery.test.ts case is ported (test-mapping-v1.0.4.json) and the runner's observable behaviour is mutation-checked in coding/extension/host/inproc (command-name suffixing, shortcut conflicts, tool_call block short-circuit, before_agent_start chaining and options edits, stale message, UI/mode getters, shutdown); subprocess dialogs use the subprocess host's ui_prompt; emitBeforeAgentStart's live prompt getters (runner.ts:1421-1438) follow every options edit (coding/extension/host/inproc/runner.go EmitBeforeAgentStart; TestBeforeAgentStartPromptFollowsEveryOptionsEdit, TestRenderCurrentSystemPromptAcrossSDKs); reported item: the Rust SDK reassigns whole options (docs/findings/fx-selected-tools.md)` | ✅ |
| `packages/coding-agent/src/core/extensions/loader.ts` | `coding/extension/provider_runtime.go (shared ordered provider queue and bind actions) + coding/extension/host/subprocess/builder_node.go (resolveNodeEntrypoint: resolveExtensionEntries) + coding/extension/host/subprocess/runtime-node/jiti-loader.mjs + coding/extension/host/subprocess/runtime-node/runtime.mjs + coding/extension/host/subprocess/host.go + coding/extension/host/subprocess/tool_registration.go (synchronous late registration updates; flag defaults, tool-schema and command validation, failed Node factory API rejection, factory-time action stubs and awaited exec, rollback and concurrent-provider preservation are covered by Pi-differential and mutation-checked tests (TestNodeLoadTimeActionsMatchPiLoader, TestUpstreamFactoryFailure*, TestNodeRunnerRejectsInvalidCommandRegistration); the module-level factory cache (loadExtensionsCached, clearExtensionCache) is the recorded divergence D70, and discoverAndLoadExtensions has no production caller in Pi, which discovers through the package manager (coding/packagecontent); reload and Session replacement re-invoke factories in the retained process through the Node host's retention and runtime-node/generations.mjs)`, `coding/discover_extensions.go` | ✅ |
| `packages/coding-agent/src/core/extensions/wrapper.ts` | `coding/extension_bridge.go + coding/session_tool_registry.go` | ✅ |
| `packages/coding-agent/src/core/extensions/index.ts` | `(barrel)` | n/a |
| `packages/coding-agent/src/core/extensions/pi-ai-extension-view.ts` | `coding/extension/host/subprocess/runtime-node/shims/pi-ai.mjs (compat plus TypeBox Type and TypeBox StringEnum, served to Node extensions); extensions/sdk/type_schema.go (Type in the Go SDK)` | 🟡 |
| `packages/coding-agent/src/extensions/index.ts` | `coding/cli/builtin_extensions.go + coding/cli/llama.go (built-in llama.cpp provider and /llama registered natively in every mode; Stock PiG has no inline-extension runtime)` | ✅ |
| `packages/coding-agent/src/extensions/llama/client.ts` | `internal/codingagent/llama/client.go + internal/codingagent/llama/fetch.go` | ✅ |
| `packages/coding-agent/src/extensions/llama/huggingface.ts` | `internal/codingagent/llama/huggingface.go` | ✅ |
| `packages/coding-agent/src/extensions/llama/index.ts` | `internal/codingagent/llama/index.go + internal/codingagent/llama/extension.go (the /llama command; the slash-command catalog and the RPC get_commands list it through the runner)` | ✅ |
| `packages/coding-agent/src/extensions/llama/provider.ts` | `internal/codingagent/llama/provider.go + internal/codingagent/llama/native_provider.go` | ✅ |
| `packages/coding-agent/src/extensions/llama/ui.ts` | `internal/codingagent/llama/ui.go` | ✅ |
| _(downstream: subprocess host bridge)_ | `coding/extension/host/subprocess/*.go` | 🔀 D19 |
| _(downstream: packed runtime cells)_ | `coding/extension/host/subprocess/cell_plan.go`, `coding/extension/host/runtimecell/*.go`, `coding/extension/host/cellpack/*.go`, `coding/extension/host/fusepack/*.go` | 🔀 D20 |
| _(downstream: Go SDK bridge)_ | `extensions/sdk/...` | 🔀 D19 |
| _(downstream: Rust SDK bridge)_ | `extensions/sdk-rs/...` | 🔀 D19 |
| _(downstream: Python SDK bridge)_ | `extensions/sdk-py/...` | 🔀 D19 |
| `packages/coding-agent/src/core/tools/bash.ts` | `internal/codingagent/tools/bash.go + internal/codingagent/tools/bash_operations.go + internal/codingagent/tools/shell_tool.go`, `coding/extension/bash_operations.go`, `coding/extension/events.go`, `coding/extension/tool_inputs.go`, `internal/codingagent/tools/tools.go`, `internal/codingagent/tools/tool_definition.go` | ✅ |
| `packages/coding-agent/src/core/tools/output-accumulator.ts` | `internal/codingagent/tools/output_accumulator.go + truncate.go` | ✅ |
| `packages/coding-agent/src/core/tools/read.ts` | `internal/codingagent/tools/read.go + internal/codingagent/tools/context_cwd.go`, `internal/codingagent/tools/tools.go`, `coding/tool_definitions.go`, `coding/extension/events.go`, `coding/extension/tool_inputs.go` | ✅ |
| `packages/coding-agent/src/core/tools/write.ts` | `internal/codingagent/tools/write.go (WriteOperations; mutation_queue_upstream_test.go)`, `internal/codingagent/tools/tools.go`, `coding/tool_definitions.go`, `coding/extension/tool_inputs.go` | ✅ |
| `packages/coding-agent/src/core/tools/edit.ts` | `internal/codingagent/tools/edit.go (EditOperations; mutation_queue_upstream_test.go)`, `internal/codingagent/tools/tools.go`, `coding/tool_definitions.go`, `internal/codingagent/tools/details.go`, `coding/extension/tool_inputs.go` | ✅ |
| `packages/coding-agent/src/core/tools/grep.ts` | `internal/codingagent/tools/grep.go`, `internal/codingagent/tools/tools.go`, `coding/tool_definitions.go`, `coding/extension/events.go`, `coding/extension/tool_inputs.go` | ✅ |
| `packages/coding-agent/src/core/tools/find.ts` | `internal/codingagent/tools/find.go (FindOperations and root-relativization tests; native Windows validation pending)`, `internal/codingagent/tools/tools.go`, `coding/tool_definitions.go`, `coding/extension/events.go`, `coding/extension/find_tool_input.go` | ✅ |
| `packages/coding-agent/src/core/tools/ls.ts` | `internal/codingagent/tools/ls.go`, `internal/codingagent/tools/tools.go`, `coding/tool_definitions.go`, `coding/extension/events.go`, `coding/extension/tool_inputs.go` | ✅ |
| `packages/coding-agent/src/core/tools/edit-diff.ts` | `internal/codingagent/tools/edit_diff.go + unified_patch.go (GenerateUnifiedPatch) + diff_string.go (GenerateDiffString); coding/extension/host/subprocess/runtime-node/shims/pi-coding-agent.mjs (vendored Node diff helpers)`, `internal/codingagent/tools/edit_preview.go` | ✅ |
| `packages/coding-agent/src/core/tools/file-mutation-queue.ts` | `internal/codingagent/tools/mutation_queue.go` | ✅ |
| `packages/coding-agent/src/core/tools/render-utils.ts` | `tui/tool_path_render.go (shortenPath, linkPath, renderToolPath) + tui/tool_call_args.go (FormatToolCallWithArgs) + tui/shell_renderers.go (invalidArgText) + internal/codingagent/tools/render_text_output.go (GetTextOutput) + internal/codingagent/tool_render_shell.go (shellTextOutput and toolResultTextOutput: getTextOutput for the built-in shell, read and list renderers; normalizeDisplayText is inline in the write renderer's writeRenderCall) + internal/codingagent/tool_render.go (replaceTabs)`, `tui/compact_read_header.go` | ✅ |
| `packages/coding-agent/src/core/tools/truncate.ts` | `internal/codingagent/tools/truncate.go + internal/truncate/tail.go (truncateTail's walk, kept as a stream)`, `internal/codingagent/tools/tools.go`, `tui/truncation_result.go` | ✅ |
| `packages/coding-agent/src/core/tools/tool-definition-wrapper.ts` | `coding/extension_bridge.go + coding/session_tool_registry.go` | ✅ |
| `packages/coding-agent/src/core/tools/path-utils.ts` | `internal/codingagent/tools/path_utils.go` | ✅ |
| `packages/coding-agent/src/core/tools/index.ts` | `(barrel)` | n/a |
| `packages/coding-agent/src/core/export-html/ansi-to-html.ts` | `internal/codingagent/export/ansi_html.go` | ✅ |
| `packages/coding-agent/src/core/export-html/tool-renderer.ts` | `internal/codingagent/export/ansi_html.go + internal/codingagent/export/tool_renderer.go + internal/codingagent/slash_session_handlers.go` | ✅ |
| `packages/coding-agent/src/core/export-html/index.ts` | `internal/codingagent/export/export.go + internal/codingagent/export/assets/` | ✅ |
| `packages/coding-agent/src/modes/interactive/interactive-mode.ts` | `internal/codingagent/interactive.go (formatResumeCommand preserves TTY/persistence gates and the default-directory decision), internal/codingagent/auth_warnings.go, internal/codingagent/interactive_input.go, internal/codingagent/startup_input.go, internal/codingagent/interactive_turn.go, internal/codingagent/interactive_user_bash.go, internal/codingagent/interactive_commands.go, internal/codingagent/interactive_reload.go, internal/codingagent/interactive_rebind.go, internal/codingagent/model_command.go, internal/codingagent/scoped_models_selection.go, internal/codingagent/interactive_auth.go, internal/codingagent/interactive_login.go, internal/codingagent/interactive_api_key_login.go, internal/codingagent/slash_auth.go, internal/codingagent/interactive_post_login.go, internal/codingagent/interactive_events.go, internal/codingagent/interactive_tui.go, internal/codingagent/startup_header.go, internal/codingagent/ext_ui_context.go, internal/codingagent/interactive_signals_unix.go, internal/codingagent/interactive_signals_windows.go, internal/codingagent/suspend.go, internal/codingagent/session_selectors.go, internal/codingagent/interactive_theme_modal.go, internal/codingagent/interactive_theme_watcher.go, internal/codingagent/interactive_models.go, internal/codingagent/interactive_extensions.go, internal/codingagent/interactive_chat.go, internal/codingagent/interactive_changelog.go, internal/codingagent/interactive_layout.go, internal/codingagent/interactive_editor.go, internal/codingagent/remote_editor.go, internal/codingagent/interactive_transcript.go, internal/codingagent/interactive_compaction.go, internal/codingagent/interactive_thinking.go, internal/codingagent/interactive_helpers.go, internal/codingagent/interactive_hotkeys.go, internal/codingagent/loaded_resources.go, internal/codingagent/extension_diagnostics.go (quiet resource diagnostics), internal/codingagent/project_trust_warning.go (startup transcript warning), internal/codingagent/resource_diagnostics.go, internal/codingagent/autocomplete_wrappers.go, internal/codingagent/extension_ui_phase.go, coding/extension/autocomplete.go, coding/extension/host/subprocess/runtime-node/widget-component.mjs`, `internal/codingagent/interactive_lifecycle.go`, internal/codingagent/ext_ui_dialog_stack.go (extension dialog stack: select, confirm, input, editor and editor-slot custom dialogs stack, a hidden dialog's abort removes only itself, session replacement and /reload dispose every dialog; Pi 38847563); getModelScopeNotice/showFullscreenModelScopeNotice: internal/codingagent/model_scope_line.go; getExtensionChangelogUpdates/readPackageJson: internal/codingagent/interactive_changelog.go | ✅ |
| `packages/coding-agent/src/modes/interactive/external-editor.ts` | `internal/codingagent/external_editor.go (opened by the interactive editor, the extension UI context and the session selectors)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/trust-selector.ts` | `internal/codingagent/trust_selector.go; internal/codingagent/slash_session_handlers.go (trustHandler)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/first-time-setup.ts` | `internal/codingagent/first_time_setup.go (FirstTimeSetupComponent; D88 adds the sprite step and the pig-head logo)` | ✅ |
| `packages/coding-agent/src/modes/interactive/theme/theme.ts` | `tui/theme.go + tui/theme_loader.go + tui/theme_watcher.go (indexed-color load/var/ANSI/CSS/export coverage in TestLoadThemeFileIndexedColors; complete optional-color fallbacks and authored key order in scrollbar_theme_test.go) + exact pinned dark/light JSON; tui/component_themes.go (getMarkdownTheme, getSelectListTheme, getSettingsListTheme against an explicit theme); internal/codingagent/extension_diagnostics.go (getCustomThemeInfos availability)`, `tui/highlight.go` (HighlightCode; CodeHighlighter reuses its last result until the code, theme or grammars change; Pi 0f21a331), `tui/markdown_style.go`, `tui/select_list.go`, `tui/settings_list.go`, `coding/cli/theme_init.go`, `tui/theme_tokens.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/theme/theme-controller.ts` | `internal/codingagent/presentation_theme.go + interactive_theme.go + interactive_theme_modal.go + startup_ui.go (terminal color query, live scheme notifications, selection, preview, persistence and lifecycle); theme_controller_native_upstream_test.go; interactive_theme_autoswitch_test.go; interactive-rendering/36–37` | ✅ |
| `packages/coding-agent/src/modes/interactive/model-search.ts` | `tui/model_search.go (ModelSearchItem, GetModelSearchText, GetModelSelectorSearchText); GetModelSelectorSearchText feeds tui/model_select.go applyFilter over ModelSelectorItem.Name (raw model name), GetModelSearchText feeds tui/scoped_models_list.go refresh and internal/codingagent/interactive_commands.go modelArgCompletions. tui/model_search_test.go proxy-provider fixture (openrouter/vercel-ai-gateway openai/gpt-5 ids) asserts the upstream 0.99.1 fuzzyFilter order for both functions and the selector` | ✅ |
| `packages/coding-agent/src/modes/print-mode.ts` | `coding/cli/print_mode.go (one function runs text and json, as upstream does; json streams the session event stream as JSONL while the turn runs; text still prints only the final assistant message). Behavioral coverage: print/01-print-mode-arithmetic, json/01-json-mode-streams-events` | ✅ |
| `packages/coding-agent/src/modes/rpc/rpc-mode.ts` | `coding/cli/rpc_mode.go + coding/cli/rpc_dispatch.go + coding/cli/rpc_admission.go + coding/cli/rpc_json_diagnostic.go + coding/cli/rpc_ui.go` | ✅ |
| `packages/coding-agent/src/modes/rpc/rpc-types.ts` | `coding/cli/rpc_types.go`, `coding/cli/rpc_ui.go`, `coding/rpcclient/types.go` | ✅ |
| `packages/coding-agent/src/modes/rpc/rpc-client.ts` | `coding/rpcclient/rpc_client.go + coding/rpcclient/commands.go + coding/rpcclient/types.go + coding/rpcclient/process_unix.go + coding/rpcclient/process_windows.go (RpcClient spawns the pig executable in --mode rpc where upstream spawns node cliPath; typed wire structs replace the TypeScript imports, with raw JSON kept for entries, messages, and events). coding/rpcclient/rpc_client_test.go ports rpc-client-clear-queue/clone/process-exit tests against scripted child processes; rpc_mode_test.go ports rpc.test.ts against a pig binary built in TestMain, using test-faux in place of the live Anthropic model` | ✅ |
| `packages/coding-agent/src/modes/rpc/jsonl.ts` | `coding/rpcclient/jsonl.go (the RPC types of coding/cli and internal/text/quote_utf16 serve its framing)` | ✅ |
| `packages/coding-agent/src/modes/index.ts` | `(barrel)` | n/a |
| `packages/coding-agent/src/utils/changelog.ts` | `internal/codingagent/changelog.go + internal/codingagent/changelog_links.go (the bundled CHANGELOG.md is embedded by the root embed package)` | ✅ |
| `packages/coding-agent/src/utils/output-files.ts` | `internal/outputfiles/outputfiles.go (internal/outputfiles/outputfiles_test.go); callers: the bash executor, the output accumulator, the MCP tools and the codemode execute tool` | ✅ |
| `packages/coding-agent/src/utils/ansi.ts` | `internal/codingagent/tools/sanitize.go (StripANSI, SplitIncompleteAnsiSuffix); internal/text/strip_ansi.go (the private-parameter CSI regex tools.StripANSI delegates to)` | ✅ |
| `packages/coding-agent/src/utils/child-process.ts` | `internal/crossspawn/crossspawn.go + internal/crossspawn/windows_plan.go (spawnProcess/spawnProcessSync: Node's spawn off Windows, Windows shebang routing and cross-spawn escaping); internal/childwait/wait.go (waitForChildProcess post-exit stdio grace, EXIT_STDIO_GRACE_MS, used by core/exec.ts and the bash tool)` | ✅ |
| `packages/coding-agent/src/utils/clipboard.ts` | `internal/codingagent/clipboard_copy.go (copyToClipboard; the text reader lives in clipboard and clipboard_text: readClipboardText command fallbacks, then the getNativeClipboard getText reader on every platform, and interactive Ctrl+V/right-click paths; internal/codingagent/clipboard_read_text_test.go)` | ✅ |
| `packages/coding-agent/src/utils/clipboard-image.ts` | `internal/codingagent/clipboard.go + internal/codingagent/clipboard_paste.go (backend read and paste caller; unsupported formats are normalized to PNG; temp file name, umask mode, extensions and WSL temp names match Pi; command timeouts (1 s, 5 s, 3 s), the native octet-stream label, the PowerShell ok check and the empty-Wayland no-fallthrough are mutation-checked; the native Windows and macOS helper reads an image the desktop copied: internal/nativeplatform TestNativeClipboardImageRoundTrip)` | ✅ |
| `packages/coding-agent/src/utils/exif-orientation.ts` | `internal/imageprocessing/images.go` | ✅ |
| `packages/coding-agent/src/utils/frontmatter.ts` | `internal/codingagent/frontmatter/frontmatter.go + internal/codingagent/prompt_diagnostics.go + internal/yaml12 (a mapping with non-string keys keeps its frontmatter as in Pi, parsed by the yaml 2.9.0 port; CI-pending: the host zone of a date used as a mapping key on Windows (region-first CLDR mapping, TZ handling) is proven only by the native Windows test internal/yaml12/zonelocal_windows_test.go)` | ✅ |
| `packages/coding-agent/src/utils/fs-watch.ts` | `internal/codingagent/git_watcher.go` | ✅ |
| `packages/coding-agent/src/utils/git.ts` | `coding/source/gitparse.go + coding/source/hostedgit.go (parseGitUrl and the hosted-git-info lookup it reads; ParseSource in coding/source/ref.go and the package commands call it; parseGitURL cases in coding/source/ref_test.go and coding/source/parse_git_oracle_test.go)` | ✅ |
| `packages/coding-agent/src/utils/html.ts` | `tui/highlight.go (decodeHTMLEntity and decodeHTMLEntityAt, the only callers being syntax-highlight.ts renderHighlightedHtml; TestSyntaxHighlightRendererUpstream decodes highlight.js character references)` | ✅ |
| `packages/coding-agent/src/utils/image-convert.ts` | `internal/codingagent/image_convert.go + internal/imageprocessing/image_convert.go (ConvertImageBytesToPng = decodeAutoOriented + encodePNG in place of Photon, also behind the imageprocessing convertToolResultImageOnly as upstream normalizeImage; ConvertToPng with Node Buffer base64 decoding; maybeConvertImagesForKitty drives the tui ToolExecution PendingKittyImageConversions/ApplyConvertedImage and the Kitty non-PNG render skip from the tool-end and transcript-replay paths). Unit-tested by internal/codingagent/image_convert_test.go (upstream image-processing.test.ts convertToPng cases, every decodable format, failure, EXIF locators) and tui/tool_execution_kitty_convert_test.go (upstream tool-execution-component.test.ts late-conversion race) under a faked Kitty capability; tmux cannot report Kitty, so clipboard-images/02 stays deferred` | ✅ |
| `packages/coding-agent/src/utils/image-resize.ts` | `internal/imageprocessing/images.go` | ✅ |
| `packages/coding-agent/src/utils/mime.ts` | `internal/imageprocessing/images.go` | ✅ |
| `packages/coding-agent/src/utils/paths.ts` | `internal/resolvepath/resolvepath.go; internal/codingagent/paths.go; internal/noderealpath/noderealpath.go, realpath_other.go, win32.go (IsLocalPath is consumed by the CLI resource-flag resolver; ResolvePath is shared by the skills loader; CanonicalizePath preserves Node volume-mount identity and resolves rooted link targets on the link's device through the Node realpathSync walk; the native Windows build adds its own canonical-path walk over the Win32 API, proven only by the native Windows tests)` | ✅ |
| `packages/coding-agent/src/utils/pi-user-agent.ts` | `internal/codingagent/bug_report.go (codingAgentUserAgent supplies the owner-approved Q4/D65 pig/<coding.Version> identity to exported report metadata; TestBugReportEnvironmentUsesPiGUserAgent). Other upstream call sites are accounted by their owning rows: version-check.ts and package-manager-cli.ts self-update are replaced by D39, remote-catalog-provider.ts sends the same User-Agent through the remote catalog provider (see its row), and the interactive report-install call is ported under core/telemetry.ts (the install telemetry), which sends this same User-Agent (ai.FormatUserAgent(nil)) with the ping.` | ✅ |
| `packages/coding-agent/src/utils/photon.ts` | `(not ported: WASM)` | n/a |
| `packages/coding-agent/src/utils/shell.ts` | `internal/codingagent/tools/shell_config.go + bash_session_env.go (getShellEnv) + sanitize.go (sanitizeBinaryOutput) + bash_group_*.go + powershell.go + taskkill.go (killProcessTree; native Windows execution unproven; detached-child tracking and the signal and crash kills are ported)`, `internal/shellconfig/shellconfig.go + find.go + default_*.go (isLegacyWslBashPath, findExecutableOnPath, the platform default shell)`, `internal/nodefs/access_*.go (accessSync F_OK, shellPathExists)` | ✅ |
| `packages/coding-agent/src/utils/syntax-highlight.ts` | `tui/highlight.go (highlight, renderHighlightedHtml, supportsLanguage, loadAllHighlightLanguages; theme.ts highlightCode, getCliHighlightTheme and getLanguageFromPath; utils/html.ts decodeHtmlEntityAt) + tui/internal/hljs/ (highlight.js 10.7.3 lib/core.js compiler, highlighter and HTML emitter, JavaScript RegExp semantics over UTF-16 code units, V8 Array.prototype.sort, the language callbacks; grammars snapshotted from the exact Pi package by automation/gen/generate-highlight-grammars.mjs) ; interactive-mode init, owned by the interactive-mode.ts row, calls loadAllHighlightLanguages after the startup frame and repaints. tui/highlight_oracle_test.go replays Pi's highlightCode, Markdown highlightCode and highlight.js HTML for the highlight.js 10.7.3 test inputs before and after every language loads, and mutated samples in every language` | ✅ |
| `packages/coding-agent/src/utils/sleep.ts` | `ai/assistant_retry.go (sleepContext is the awaited, context-cancellable backoff used by the Session-owned retry loop; TestRetryAssistantCallAbortsBackoffSleepViaContext covers cancellation before the timer wait)` | ✅ |
| `packages/coding-agent/src/utils/tools-manager.ts` | `internal/codingagent/tools/tools.go + tools_manager.go (release redirects, fetchWithRetry, extraction per D14, same-manager additive download coalescing per D81, silent grep/find, bounded distinct ToolStatus causes); the interactive-mode consumer of the ToolStatus callback, interactive_tools_status (live owner-thread status, ordered initial reports, joined cancellation); internal/codingagent/tools/tools_manager_upstream_test.go + tools_manager_latest_test.go + tools_manager_status_test.go + the interactive_tools_status tests + tools/12-managed-tool-status; Linux musl asset selection and the pinned darwin/x64 fd lookup bypass ported` | ✅ |
| `packages/coding-agent/src/utils/version-check.ts` | `internal/codingagent/selfupdate.go (FetchUpdateManifest is getLatestPiRelease over the configured signed manifest of D39 instead of the pi.dev API, with Retry for the explicit check, the ten-second budget, and the identity and accept headers; CheckForBinaryUpdate is checkForNewPiVersion with PI_SKIP_VERSION_CHECK; ComparePackageVersions and IsNewerPackageVersion follow node-semver valid() with the non-semver string-inequality fallback; formatVersionCheckError is folded into the D39 update-failure message); tests in internal/codingagent/version_check_upstream_test.go` | ✅ |
| `packages/coding-agent/src/utils/deprecation.ts` | `(designed out: the pinned upstream helper has no imports or callers; Go warning sites retain their caller-specific output policy)` | n/a |
| `packages/coding-agent/src/utils/image-resize-core.ts` | `internal/imageprocessing/images.go` | ✅ |
| `packages/coding-agent/src/utils/image-resize-worker.ts` | `(Node worker thread: Go uses goroutines in-process; Pi 1.5.0 tags worker replies and ignores Node's own channel messages under node --watch (#10527), which has no counterpart because Go resizes with no worker message channel)` | n/a |
| `packages/coding-agent/src/utils/json.ts` | `internal/codingagent/model_registry.go (stripJSONComments; strip_json_comments_oracle_test.go)` | ✅ |
| `packages/coding-agent/src/utils/open-browser.ts` | `internal/codingagent/interactive_auth.go (openBrowser passes Windows URLs directly to rundll32; native TestOpenBrowserPassesURLToWindowsHandlerWithoutShell); detached launcher ported` | ✅ |
| `packages/coding-agent/src/utils/windows-self-update.ts` | `internal/codingagent/windows_self_update.go (the running pig.exe is the loaded native image an npm update quarantines; TestQuarantineNativeDependenciesMovesLoadedImagesAndCopiesThemBack, TestWindowsNpmSelfUpdateReplacesTheRunningInstallation)` | ✅ |
| `packages/coding-agent/src/bun/cli.ts` | `(Bun-only entrypoint)` | n/a |
| `packages/coding-agent/src/bun/restore-sandbox-env.ts` | `(Bun-only sandbox helper)` | n/a |
| `packages/coding-agent/src/modes/interactive/components/assistant-message.ts` | `tui/assistant_message_block.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/bash-execution.ts` | `tui/bash_execution.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/bordered-loader.ts` | `tui/bordered_loader.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/branch-summary-message.ts` | `tui/branch_summary.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/compaction-summary-message.ts` | `tui/compaction_summary.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/config-selector.ts` | `tui/config_selector.go (formatBaseDir matches whole path segments); coding/cli/config_command.go configItemKey keys the inherited state by codingagent.ResourcePathKey (getResourcePathKey)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/countdown-timer.ts` | `tui/countdown_timer.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/custom-editor.ts` | `internal/codingagent/custom_editor.go, tui/editor.go and tui/editor_status.go hold the CustomEditor members. InteractiveMode mounts that CustomEditor as its default editor and registers the app handlers on it (defaultCustomEditor and setupKeyHandlers in the codingagent interactive_input source), with the CustomEditor app-keybinding precedence: extension shortcuts -> paste-image -> interrupt with autocomplete/empty guards -> exit-on-empty -> app.session actions -> editor fallthrough. Ctrl+D exits only when the editor has zero-length text; nonempty text, including whitespace, falls through to tui.editor.deleteCharForward. Conflicting paste-image/interrupt/exit bindings use CustomEditor order rather than registry declaration order. Guarded by mutation-proven TestCustomEditorHighPriorityBindingOrder, TestCustomEditorDispatchPrecedence, and test/parity/scenarios/custom-editor-precedence.toml.` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/custom-entry.ts` | `internal/codingagent/interactive.go addCustomEntryToChat (rebuild + live-append, Spacer(1) + expand-toggle) over the entry-renderer wire in coding/extension/host/subprocess/{protocol.go,host.go,render_proxy.go} + RegisterEntryRenderer in extensions/sdk{,-rs,-py} + runtime-node. Cross-SDK verified by extension-conformance (inproc-go reference vs subprocess go/rust/python) + parity scenario extensions-runtime/18-subprocess-entry-renderer (pi 0.84.0 vs pig byte-identical, mutation-proven)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/custom-message.ts` | `tui/custom_message.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/diff.ts` | `tui/diff_render.go (renderDiff, RenderDiffOptions) + tui/diff_component.go (the transcript component that renders it)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/dynamic-border.ts` | `tui/dynamic_border.go`, `tui/tui.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/extension-editor.ts` | `internal/codingagent/extension_editor.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/extension-input.ts` | `tui/extension_input.go (hosted by the extension UI context)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/extension-selector.ts` | `tui/extension_selector.go + internal/codingagent/ext_ui_context.go (dialogTimeout, the selector's timeout option)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/footer.ts` | `internal/codingagent/status_line.go`, `internal/codingagent/footer_data_provider.go`, `tui/tui.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/index.ts` | `(barrel)` | n/a |
| `packages/coding-agent/src/modes/interactive/components/keybinding-hints.ts` | `tui/keybinding_hints.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/login-dialog.ts` | `tui/login_dialog.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/auth-url.ts` | `tui/auth_url.go (AuthURL; used by the login dialog and the MCP manager RedirectURL)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/model-selector.ts` | `tui/model_select.go + tui/model_selector_runtime.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/oauth-selector.ts` | `tui/oauth_selector.go (driven by the interactive login and slash auth code)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/scoped-models-selector.ts` | `tui/scoped_models_list.go (driven by the interactive mode)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/session-selector.ts` | `internal/codingagent/session_selector.go (the component, hosted by startup_ui, the interactive mode and its theme modal; its unexported helpers session_selector_load (owned scope loads, progress and cancellation), session_selector_status (setStatusMessage) and session_selector_delete with its _unix and _windows variants (deleteSessionFile) await storage with UI-task pumping; canonicalSessionPath is getSessionPathKey: canonicalizePath through resolvepath.ComparisonKey)`, `internal/codingagent/session_selector_load.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/session-selector-search.ts` | `internal/codingagent/session_selector.go + tui/fuzzy.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/settings-selector.ts` | `internal/codingagent/settings_selector.go (no share-destination item, D64)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/show-images-selector.ts` | `tui/show_images_selector.go (layout and onSelect/onCancel dispatch match upstream (TestShowImagesUpstreamRender, TestShowImagesUpstreamCallbacks); Pi 1.5.0 has no ordinary interactive entry point either)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/skill-invocation-message.ts` | `tui/skill_invocation.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/theme-selector.ts` | `tui/theme_selector.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/thinking-selector.ts` | `internal/codingagent/thinking_selector.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/tool-execution.ts` | `tui/tool_execution.go (shell elapsed Text wrapping covered for bash/powershell output, no-output, and collapsed paths by TestToolExecutionComponent_ShellElapsedFooterRows), tui/tool_execution_definition.go (registered-definition renderers, shells and fallbacks covered by TestDefinitionCard* and extensions-runtime/24-25 tool-renderer scenarios)` | ✅ |
| `packages/coding-agent/src/modes/interactive/program-status-reporter.ts` | `internal/codingagent/program_status_reporter.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/tree-selector.ts` | `tui/tree_select.go`, `internal/codingagent/tree_selector.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/user-message.ts` | `tui/user_message_block.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/user-message-selector.ts` | `tui/user_message_selector.go (D66 bounds list rows at unusually narrow widths instead of emitting fatal over-wide rows)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/visual-truncate.ts` | `tui/visual_truncate.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/armin.ts` | `internal/codingagent/armin.go (D87: a pig head labeled pigsayhi; /pigsayhi is an alias)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/earendil-announcement.ts` | `internal/codingagent/earendil_announcement.go (shown by the interactive easter-egg code)` | ✅ |
| `packages/coding-agent/src/core/pi-manifest.ts` | `coding/packagecontent/pi_manifest.go ReadPiManifest (BOM strip, object-only package and "pi", per-field string arrays), read by the Node extension builder's piPackageEntrypoints; coding/packagecontent/packagecontent.go readPackageManifest applies the same parse for package discovery; PiManifest.ChangelogPath` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/markdown-transform.ts` | `coding/extension/markdowntransform/markdowntransform.go, internal/codingagent/interactive_chat.go, tui/markdown_async.go (chain order and context, a throwing transformer keeping the current Markdown, stale-generation publication, and a body that outlives an extension disconnect publishing nothing are covered and mutation-checked; D56: a body the renderer inactivity boundary abandoned disables its transformer in later chains while it runs)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/mermaid.ts` | `internal/codingagent/mermaid_transform.go + internal/codingagent/mermaid_fence_parser.go + internal/codingagent/mermaid_definition_parser.go + internal/codingagent/mermaid_html_parser.go + internal/codingagent/mermaid_heading_parser.go (natural-layout raw fallback and display-only partial warnings: mermaid_fallback_upstream_test.go; exact transform corpus: mermaid_transform_test.go)` | ✅ |
| `packages/coding-agent/src/modes/json-event.ts` | `coding/cli/rpc_events.go + coding/cli/rpc_object.go (ordered JSON/RPC conversion; field-only live projections exclude partials before serialization and read toolcall_start identity)`, `coding/rpcclient/types.go` | ✅ |
| `packages/coding-agent/src/utils/abort.ts` | `(AbortSignal/Promise racing: raceWithAbortSignal/operationSignal; pig uses context.Context cancellation + select (D3): runtime mechanics, designed out)` | n/a |
| `packages/coding-agent/src/utils/management-http.ts` | `internal/managementhttp/managementhttp.go FetchWithRetry (management-http.test.ts cases in managementhttp_test.go), used by the tools manager's getLatestVersion and downloadFile; its other upstream callers are remote-catalog-provider.ts, whose Go port routes through the same FetchWithRetry (see its row), and version-check.ts, which is not ported (see its row)` | ✅ |
| `packages/coding-agent/src/utils/tool-result-images.ts` | `internal/imageprocessing/images.go + coding/session_images.go: ordered text/image content, adjacent hints, unchanged-slice identity, model profile after hooks; internal/imageprocessing/tool_result_images_upstream_test.go ports all seven tests` | ✅ |
| `packages/coding-agent/src/bun/runtime-setup.ts` | `(Bun-only runtime setup)` | n/a |
| `packages/coding-agent/src/bun/sandbox-env-setup.ts` | `(Bun-only sandbox helper)` | n/a |
| `packages/coding-agent/src/cli/auth-check.ts` | `coding/cli/auth_check.go (the main.ts runAuthCommand dispatch is mapped under main.ts)` | ✅ |
| `packages/coding-agent/src/cli/auth-command.ts` | `coding/cli/auth_command.go (resolveCliModel for --model is mapped under core/model-resolver.ts)` | ✅ |
| `packages/coding-agent/src/cli/auth-login.ts` | `coding/cli/auth_login.go (RunAuthLogin; every auth-login.test.ts case in coding/cli/auth_login_test.go; the main.ts runAuthLoginCommand dispatch is coding/cli/auth_run.go)` | ✅ |
| `packages/coding-agent/src/cli/auth-logout.ts` | `coding/cli/auth_logout.go (RunAuthLogout; every auth-logout.test.ts case in coding/cli/auth_logout_test.go; the main.ts runAuthLogoutCommand dispatch is coding/cli/auth_run.go)` | ✅ |
| `packages/coding-agent/src/cli/auth-terminal.ts` | `coding/cli/auth_terminal.go (LoginTerminal, CreateProcessLoginTerminal; auth-terminal.test.ts cases in coding/cli/auth_terminal_test.go)` | ✅ |
| `packages/coding-agent/src/core/provider-matching.ts` | `internal/codingagent/provider_matching.go (FindMatchingProviders; provider_matching_test.go)` | ✅ |
| `packages/coding-agent/src/core/radius-mcp.ts` | `internal/codingagent/radius_mcp.go (PlanRadiusMcpServer; radius_mcp_stripped.go for a build without built-in MCP)` | ✅ |
| `packages/coding-agent/src/utils/terminal-question.ts` | `internal/terminalquestion/terminalquestion.go (Reader.Question; terminalquestion_test.go)` | ✅ |
| `packages/coding-agent/src/utils/web-url.ts` | `internal/weburl/weburl.go (WebURL, DisplayURL)` | ✅ |
| `packages/coding-agent/src/cli/setup.ts` | `coding/cli/setup_cli.go (PI_CODING_AGENT/AI_AGENT process markers; process.title is the executable name, Node warning suppression has no Go analogue, configureHttpDispatcher is mapped under core/http-dispatcher.ts)` | ✅ |
| `packages/coding-agent/src/core/bug-report-upload.ts` | `(designed out: D62, PiG never uploads bug reports)` | n/a |
| `packages/coding-agent/src/core/bug-report.ts` | `internal/codingagent/bug_report.go (export subset: metadata, redaction, diagnostics, archive; D62) + internal/codingagent/compaction/bug_report_summary.go (coding/bug_report.go; internal/codingagent/compaction/bug_report_summary_test.go)` | ✅ |
| `packages/coding-agent/src/core/cache-warmer.ts` | `internal/codingagent/cache_warmer.go + coding/extension/cache_warming.go (retention, lifecycle, scheduling and awaited extension decisions; the sdk.ts streamFn and agent-session.ts wiring that drives the warmer lives in coding session_cache_warming, cited by core/sdk.ts)` | ✅ |
| `packages/coding-agent/src/core/crash-log.ts` | `internal/codingagent/crash_log.go + interactive_crash.go (crash persistence, startup notice, /bug attachment and cleanup, fatal create/resume/import reporting, and loaded-extension stack attribution; D98 adds the Go goroutine trace form; TestCrashLogMatchesPi compares the pinned Pi's module over generated crash records)` | ✅ |
| `packages/coding-agent/src/core/extensions/jiti-loader.ts` | `(barrel re-export)` | n/a |
| `packages/coding-agent/src/core/extensions/jiti-static-loader.ts` | `(barrel re-export)` | n/a |
| `packages/coding-agent/src/core/extensions/virtual-modules.ts` | `coding/extension/host/subprocess/node_runtime_on.go` + `coding/extension/host/subprocess/node_runtime_cache.go` (the embedded runtime archive and its materialization carry the shims to every launcher; TestMaterializedNodeRuntimeServesPiVirtualModules and TestMaterializedNodeRuntimeExposesPiAiModelsToExtensions run the Pi comparison on the materialized tree), `coding/extension/host/subprocess/runtime-node/loader.mjs shim table + runtime-node/shims/pi-ai-oauth.mjs serve all 22 specifiers of Pi's VIRTUAL_MODULES (typebox, @sinclair/typebox, pi-agent-core, pi-tui, pi-ai, pi-ai/compat, pi-ai/models, pi-ai/oauth, pi-ai/providers/all and pi-coding-agent, under both package scopes); TestNodeRuntimeVirtualModulesMatchPiNamespaces compares each namespace's export names and value kinds with the pinned Pi package in both directions, and TestNodeRuntimeLoaderCoversPiVirtualModules, TestNodeRuntimeLoaderServesPiAiCompatAndOAuth and TestNodeRuntimeShimsExportEveryPinnedPiValue fail when a shim is dropped or points at the wrong module` | ✅ |
| `packages/coding-agent/src/core/session-export.ts` | `internal/codingagent/session_export.go (/export <file>.jsonl, /bug transcript)` | ✅ |
| `packages/coding-agent/src/core/settings-diagnostics.ts` | `internal/codingagent/settings_diagnostics.go (collected from the startup and runtime settings managers and deduplicated in coding/cli/cli_runtime_build.go, main.ts:684/811/887; the CLI entry reports them: stderr for print/json/rpc/--list-models, chat warnings in interactive)` | ✅ |
| `packages/coding-agent/src/core/tools/powershell.ts` | `internal/codingagent/tools/powershell.go + shell_tool.go (shared createShellToolDefinition) + tools.go (CreateAllTools, BuiltinToolActive: registered everywhere, active only when named)`, `coding/tool_definitions.go`, `coding/extension/events.go`, `coding/extension/tool_inputs.go` | ✅ |
| `packages/coding-agent/src/core/tools/renderers/bash.ts` | `tui/shell_renderers.go (formatShellCall, formatDuration) + tui/tool_execution.go (live elapsed Text wrapping) + internal/codingagent/tool_render_shell.go (rebuildBashResultRenderComponent); width-boundary coverage in TestToolExecutionComponent_ShellElapsedFooterRows` | ✅ |
| `packages/coding-agent/src/core/tools/renderers/edit.ts` | `internal/codingagent/tool_builtin_renderers.go (editRenderCall, editRenderResult: the call box with the async argsComplete preview diff and its pending/success/error header background, the invalid-argument marker, error text de-duplication against the preview error) + tui/tool_execution.go (FormatEditHeader) + internal/codingagent/tool_render.go (renderDiffString); TestBuiltInRenderersMatchPi104 compares 20 edit cases row by row with the recorded output of Pi's built renderer (testdata/pi104-render-probe, recorded from the 1.0.4 build; read.ts is unchanged in 1.1.0)` | ✅ |
| `packages/coding-agent/src/core/tools/renderers/find.ts` | `tui/tool_execution.go (FormatFindHeader) + internal/codingagent/tool_render_list.go` | ✅ |
| `packages/coding-agent/src/core/tools/renderers/grep.ts` | `tui/tool_execution.go (FormatGrepHeader) + internal/codingagent/tool_render_list.go` | ✅ |
| `packages/coding-agent/src/core/tools/renderers/index.ts` | `tui/tool_renderers.go (createAllToolRenderers by name, withBuiltInRenderers fallback) + tui/tool_execution.go FormatBuiltinToolHeader + internal/codingagent/tool_render.go toolBodyRenderer` | ✅ |
| `packages/coding-agent/src/core/tools/renderers/ls.ts` | `tui/tool_execution.go (FormatLsHeader) + internal/codingagent/tool_render_list.go` | ✅ |
| `packages/coding-agent/src/core/tools/renderers/read.ts` | `internal/codingagent/tool_builtin_renderers.go (readRenderCall, readRenderResult) + tui/compact_read_header.go (compact read call for SKILL.md, AGENTS/CLAUDE files and Pi docs, line ranges, invalid-argument marker) + internal/codingagent/tool_render.go (renderReadLines, truncation warnings); TestBuiltInRenderersMatchPi104 compares 124 read cases, collapsed and expanded, row by row with the recorded output of Pi's built renderer (testdata/pi104-render-probe, recorded from the 1.0.4 build; read.ts is unchanged in 1.1.0)` | ✅ |
| `packages/coding-agent/src/core/tools/renderers/write.ts` | `internal/codingagent/tool_builtin_renderers.go + internal/codingagent/write_highlight_cache.go (WriteHighlightCache: incremental highlight while the content streams) (writeRenderCall, writeRenderResult: the content preview while arguments stream, the invalid-content marker, error-only results) + tui/tool_execution.go (FormatWriteHeader) + internal/codingagent/tool_render.go (renderWriteContent) + internal/codingagent/tool_builtin_renderers.go (builtInRenderState.highlighter: a complete call highlights through the row's tui.CodeHighlighter; reusableText keeps the row's *tui.Text; Pi 0f21a331); TestBuiltInRenderersMatchPi104 compares 50 write cases row by row with the recorded output of Pi's built renderer (testdata/pi104-render-probe, recorded from the 1.0.4 build; write.ts is unchanged in 1.1.0)` | ✅ |
| `packages/coding-agent/src/experimental/source-resolver.ts` | `(Node-runtime-only: a node:module registerHooks resolve hook that maps tsconfig @earendil-works/* path aliases to .ts sources so internal processes run from a source checkout; Go links packages at build time)` | n/a |
| `packages/coding-agent/src/modes/interactive/bug-report.ts` | `internal/codingagent/slash_bug.go, internal/codingagent/interactive_bug.go (consent flow, export-only; D62)` | ✅ |
| `packages/coding-agent/src/modes/interactive/chat-viewport.ts` | `internal/codingagent/chat_viewport.go (CreateChatViewport; mounted by buildChatViewport in internal/codingagent/interactive_tui.go)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/settings-submenu.ts` | `tui/select_submenu.go (SelectSubmenu) + internal/codingagent/settings_submenu.go (SteppedSubmenu)` | ✅ |
| `packages/coding-agent/src/modes/interactive/model-catalog-refresh.ts` | `internal/codingagent/model_catalog_refresh.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/session-share.ts` | `internal/codingagent/session_share.go (createShareTrailingEntries, ExportSessionForShare, explicit /share upload with privacy notice; sessionShareDestination does not change the destination, D64); hosted counterpart: private PiG-platform worker/src/share.ts (bounded R2 artifact route, 30-day expiry, unlisted viewer)` | ✅ |
| `packages/coding-agent/src/modes/interactive/theme/theme-schema.ts` | `tui/theme_json.go (ValidateThemeJSON, wired in tui/theme_loader.go LoadThemeFile; live Pi oracle TestThemeJSONValidationMatchesPi; goldens in tui/testdata/theme_json_cases.json re-recorded by testdata/regenerate_theme_json_cases.mjs; real-loader union coverage in TestLoadThemeFileIndexedColors)` | ✅ |
| `packages/coding-agent/src/modes/interactive/theme/theme-tokens.ts` | `tui/theme_tokens.go (token metadata, slots and fallbacks; live Pi oracle theme_tokens_oracle_test.go)` | ✅ |
| `packages/coding-agent/src/modes/interactive/tui-renderer.ts` | `internal/codingagent/presentation_tui.go (CreateInteractiveTui, fullscreenTuiOptions, scrollToEndIndicatorLabel), internal/codingagent/interactive_tui.go (createInteractiveTui wiring), internal/codingagent/clipboard_text.go (right-click paste); createInteractiveTuiReference designed out: the driver reads m.tuiInst at call time under rendererMu, so no proxy is needed` | ✅ |
| `packages/coding-agent/src/utils/clipboard-command.ts` | `internal/codingagent/clipboard_copy.go (shared bounded command runner for copy and clipboard.go image reads)`, internal/codingagent/clipboard_pipe_other.go (helpers run in their own session on unix so Ctrl+Z does not stop them; Pi f8ab362c) | ✅ |
| `packages/coding-agent/src/utils/highlight-js.d.ts` | `(TypeScript ambient declarations; no runtime behavior)` | n/a |
| `packages/coding-agent/src/utils/text.ts` | `internal/text/text.go (BOM helpers; auth, settings, models, trust, frontmatter, context, file attachments and edit callers)` | ✅ |
| `packages/coding-agent/src/utils/wsl.ts` | `internal/codingagent/wsl.go` | ✅ |
| `packages/coding-agent/src/utils/zip.ts` | `(Go archive/zip, used by internal/codingagent/bug_report.go)` | n/a |
| `packages/coding-agent/src/bun/quickjs-wasm.d.ts` | `(Bun-only type declaration for the QuickJS WASM import; not compiled by Go)` | n/a |
| `packages/coding-agent/src/core/mcp-servers.ts` | `coding/extension/mcp_servers.go (registry and change events; mcp_servers_test.go)` | ✅ |
| `packages/coding-agent/src/core/nested-tool-calls.ts` | `coding/nested_tool_calls.go` | ✅ |
| `packages/coding-agent/src/core/virtual-models.ts` | `coding/extension/virtual_model.go (routing types) + coding/virtual_models.go (IsVirtualModel, FindLatestResponse, GetBranchSelection, GetVirtualModelState, CreateVirtualModel; ModelRuntime.RegisterVirtualModel, UnregisterVirtualModel, ResolveModel, GetPhysicalModel and the withVirtualModels catalog overlay; Session.routeRequest and the unrouted-stream failure); tests: coding/virtual_models_upstream_test.go (virtual-models.test.ts) and coding/virtual_models_suite_upstream_test.go (suite/virtual-models.test.ts)` | ✅ |
| `packages/coding-agent/src/extensions/codemode/execute.lazy.ts` | `(designed out: the sandbox is created on the first script by construction, coding/extension/builtin/codemode/execute.go)` | n/a |
| `packages/coding-agent/src/extensions/codemode/execute.ts` | `coding/extension/builtin/codemode/execute.go + coding/extension/builtin/codemode/run.go (nested calls, discovery globals, store/load, output truncation, header, process-wide worker admission); models.* ported`, coding/extension/builtin/codemode/execute.go reserve/admit/release (scripts at the worker limit wait up to 60 s in arrival order; PI_CODEMODE_MAX_WORKERS; Pi 885a564b) + codemode/workerlauncher.go (a launch resuming after its result settled starts no worker) | ✅ |
| `packages/coding-agent/src/extensions/codemode/index.ts` | `coding/extension/builtin/codemode/tool.go (Extension, settings-driven mode and budget; the CLI lists it among its built-in extensions in coding/cli builtin_extensions)` | ✅ |
| `packages/coding-agent/src/extensions/codemode/renderer.ts` | `internal/codingagent/tool_codemode_renderer.go (CodemodeRenderers, carried by the codemode tool definition; the script's tui.CodeHighlighter lives in builtInRenderState, as Pi's scriptHighlighter lives in context.state)` | ✅ |
| `packages/coding-agent/src/extensions/codemode/tool.ts` | `coding/extension/builtin/codemode/tool.go + coding/extension/builtin/codemode/description.go + coding/extension/builtin/codemode/description_text.go + coding/extension/builtin/codemode/execute.go (description catalog, loadout, definition, ToolDetails and StoreEntryData)` | ✅ |
| `packages/coding-agent/src/extensions/codemode/worker.ts` | `(designed out: the sandbox runs in-process on wazero, codemode/sandbox.go; there is no worker entry)` | n/a |
| `packages/coding-agent/src/extensions/mcp/cli.lazy.ts` | `(lazy dynamic-import wrapper; Go links the command statically)` | n/a |
| `packages/coding-agent/src/extensions/mcp/cli.ts` | `coding/mcpext/cli.go` | ✅ |
| `packages/coding-agent/src/extensions/mcp/config.ts` | `coding/mcpext/config.go (config_test.go)`, UTF-8 BOM stripped when reading and editing mcp.json (Pi bdd9f039) | ✅ |
| `packages/coding-agent/src/extensions/mcp/index.ts` | `coding/mcpext/extension.go + coding/mcpext/register.go (extension_test.go)` | ✅ |
| `packages/coding-agent/src/extensions/mcp/log.ts` | `coding/mcpext/log.go` | ✅ |
| `packages/coding-agent/src/extensions/mcp/oauth.ts` | `coding/mcpext/oauth.go + coding/mcpext/storage.go (oauth_refresh_test.go)` | ✅ |
| `packages/coding-agent/src/extensions/mcp/resources.ts` | `coding/mcpext/resources.go` | ✅ |
| `packages/coding-agent/src/extensions/mcp/runtime.lazy.ts` | `(lazy dynamic-import wrapper; Go links the runtime statically)` | n/a |
| `packages/coding-agent/src/extensions/mcp/runtime.ts` | `coding/mcpext/runtime.go (connection_test.go)` | ✅ |
| `packages/coding-agent/src/extensions/mcp/tools.ts` | `coding/mcpext/tools.go (tools_test.go)` | ✅ |
| `packages/coding-agent/src/extensions/mcp/ui.ts` | `coding/mcpext/manager.go` | ✅ |
| `packages/coding-agent/src/extensions/tool-search/index.ts` | `coding/extension/builtin/toolsearch/tool.go (Extension)` | ✅ |
| `packages/coding-agent/src/extensions/tool-search/tool.ts` | `coding/extension/builtin/toolsearch/toolsearch.go + coding/extension/builtin/toolsearch/tool.go (Bm25Ranker, tokenize, documents, description, tool_search)` | ✅ |
| `packages/coding-agent/src/modes/interactive/components/pi-logo.ts` | `(designed out: D2 draws the PiG mark, coding/piglogin, instead of Pi's logo)` | n/a |
| `packages/coding-agent/src/modes/interactive/components/themed-text.ts` | `tui/themed_text.go (ThemedText, NewDynamicBorderToken); tui/themed_text_upstream_test.go; internal/codingagent/themed_notices_upstream_test.go` | ✅ |
| `packages/coding-agent/src/modes/interactive/theme/system-theme.ts` | `tui/system_theme.go; tui/theme_terminal_colors.go; tui/system_theme_upstream_test.go; tui/system_theme_oracle_test.go` | ✅ |

## `packages/tui/src/`

| upstream | pig | status |
|---|---|---|
| `packages/tui/src/tui.ts` | `tui/tui.go, tui/terminal_color_query.go (FIFO background-query lifetime), tui/overlay_command.go (input-boundary focus restoration), tui/cell_size.go (CSI 16t query and response consumption), tui/mouse.go (mouse event API, Container/Box dispatch, overlay bounds and hit testing), internal/codingagent/interactive_input.go + interactive_theme.go + startup_ui.go (driver-owned terminal input routing and theme detection)`, `tui/bash_execution.go`, `tui/login_dialog.go`, `tui/theme_selector.go`, `tui/tree_select.go`, `tui/user_message_block.go`, `tui/stack.go`, `tui/widthx/cursor_marker.go`, `tui/tui_alt_screen.go`, `tui/overlay_compositor.go`, `tui/overlay_spec.go`, `tui/renderer.go`, `tui/tui_input.go`, `tui/renderer_terminal.go`, `tui/tui_mode_debug.go`, `tui/tui_mode.go; tui/selection_navigation.go ViewportInputCapturer, CapturesViewportInput (Component.capturesViewportInput)` | ✅ |
| `packages/tui/src/editor-component.ts` | `tui/editor_component.go (EditorComponent and its optional-member interfaces)`, `tui/editor.go` | ✅ |
| `packages/tui/src/autocomplete.ts` | `tui/autocomplete.go + tui/file_autocomplete.go`, `tui/editor_async_provider.go` | ✅ |
| `packages/tui/src/fuzzy.ts` | `tui/fuzzy.go` | ✅ |
| `packages/tui/src/keybindings.ts` | `tui/keybindings.go`, `internal/codingagent/keybindings.go`, `tui/alt_screen_keybindings.go; tui/selection_navigation.go GetSelectPageIndex (replaces the PR-7680 PageSelectionIndex)` | ✅ |
| `packages/tui/src/keys.ts` | `tui/key_match.go + tui/key_parse.go + tui/keys_decode.go + tui/keybindings.go`, `tui/terminal.go`, `tui/key_helper.go` | ✅ |
| `packages/tui/src/kill-ring.ts` | `tui/kill_ring.go` | ✅ |
| `packages/tui/src/stdin-buffer.ts` | `tui/stdin_buffer.go (shared UTF-16 parser; the owner timer and modal routing live in internal/codingagent); native unit tests pass, full cross-boundary qualification remains partial`, `tui/stdin_buffer_events.go` | ✅ |
| `packages/tui/src/terminal.ts` | `tui/terminal.go + tui/terminal_input.go + tui/terminal_reader.go + tui/input_timer.go + tui/terminal_refresh.go + tui/terminal_unix.go + tui/terminal_windows.go + tui/terminal_native_shift_enter.go + tui/terminal_write_log.go (PI_TUI_WRITE_LOG: only Write and the renderers' frame output are logged, cursor, title, progress, start and stop sequences are not, as upstream; a directory value gives one file per process); internal/codingagent/interactive_input.go; all 22 upstream cases are ported, and the Linux pty, macOS pty and Windows console tests run on the CI native jobs (tui/terminal_pty_test.go, pty_darwin_test.go, terminal_native_windows_test.go)`, tui/terminal_exit_restore.go ProcessTerminal.restoreOnExit (Terminal.restoreOnExit; also restores cooked mode) | ✅ |
| `packages/tui/src/program-status.ts` | `tui/program_status.go (ProgramStatus, ProgramStatusQuery, IsProgramStatusReply, FormatProgramStatus; all 5 cases of tui/test/program-status.test.ts in tui/program_status_test.go); Terminal.SetProgramStatus (terminal.ts:576) is ledgered under terminal.ts, its terminal.test.ts program-status cases run in tui/terminal_program_status_upstream_test.go` | ✅ |
| `packages/tui/src/terminal-colors.ts` | `tui/terminal_colors.go` (QueryTerminalColors and ConsumeTerminalColorResponse implement tui.ts and are cited under that row) | ✅ |
| `packages/tui/src/terminal-image.ts` | `tui/terminal_image.go`, Rex reports Kitty images (Pi 396ecd7b) | ✅ |
| `packages/tui/src/undo-stack.ts` | `tui/undo_stack.go` | ✅ |
| `packages/tui/src/native-modifiers.ts` | `tui/native_modifiers.go (isNativeModifierPressed) over internal/nativeplatform/nativeplatform.go (IsModifierPressed) and modifiers_other.go (no helper: false); the macOS implementation (CGO-free lazy dlopen of CoreGraphics CGEventSourceFlagsState via libSystem trampolines) and the Windows implementation (user32 GetAsyncKeyState) are build-tagged sources, so the Linux coverage run cannot execute them: the macOS and Windows native CI jobs run them (internal/nativeplatform/modifiers_darwin_test.go)` | ✅ |
| `packages/tui/src/word-navigation.ts` | `tui/word_navigation.go + internal/wordsegmenter (shared UTF-16 word helpers; ICU 78.3 CJK and Southeast Asian dictionary engines and rule tailoring; Node differential corpus and byte-exact Editor scenario)` | ✅ |
| `packages/tui/src/utils.ts` | `tui/widthx/grapheme_range.go + tui/widthx/slice.go + tui/widthx/truncate.go + tui/widthx/widthx.go + tui/widthx/wrap.go + tui/widthx/tracker.go + tui/widthx/grapheme_iter.go + tui/widthx/pi_width.go + internal/wordsegmenter/segments.go + internal/wordsegmenter/cjk.go + internal/wordsegmenter/sea.go + internal/wordsegmenter/word_rules.go (shared ICU word dependency)` | ✅ |
| `packages/tui/src/index.ts` | `coding/extension/host/subprocess/runtime-node/shims/pi-tui.mjs re-exports pinned pi-tui/index.js; TestPiTuiPublicExportsAreRealImplementations checks the exact runtime namespace and restored exports; the Go package exports Pi's names (CompositeTuiLine, IsKeyRepeat, GetFocusedComponent, ShowOverlay, the input-listener and color-scheme methods); the barrel has no Go file of its own (a Go package is its own barrel), so its proof is tui/index_exports_oracle_test.go: it loads Pi's index.ts under Node, requires every runtime export to have a compile-time Go counterpart (or a recorded reason it has none: Marked), and compares the sampled pure exports (key decoding and matching, fuzzy, color scheme and program status, oklab and color parsing, hyperlinks, image rows, widthx text helpers) with Pi's results; native Windows and macOS execution is covered with native-platform.ts and terminal.ts` | ✅ |
| `packages/tui/src/components/box.ts` | `tui/box.go`, `tui/mouse.go` | ✅ |
| `packages/tui/src/components/cancellable-loader.ts` | `tui/cancellable_loader.go` | ✅ |
| `packages/tui/src/components/editor.ts` | `tui/editor.go + tui/editor_segments.go + tui/editor_state.go + tui/editor_autocomplete_units.go + tui/editor_async_provider.go + tui/editor_autocomplete_request.go (native UTF-16 state with byte-provider adapters: replayed against Pi by a seeded oracle (700 key sequences))`, `tui/tui.go` | ✅ |
| `packages/tui/src/components/image.ts` | `tui/image.go` | ✅ |
| `packages/tui/src/components/input.ts` | `tui/text_input.go (lossless UTF-16 via internal/jsstring)`, `tui/tui.go` | ✅ |
| `packages/tui/src/components/loader.ts` | `tui/loader.go` | ✅ |
| `packages/tui/src/components/markdown.ts` | `tui/markdown.go + tui/markdown_options.go + tui/markdown_style.go + tui/markdown_blocks.go (renderToken, renderList, renderQuote, trimPartialClosingFences) + tui/markdown_marked.go + tui/markdown_marked_lexer.go (marked 18.0.11 Lexer.blockTokens and the block tokenizers) + tui/markdown_marked_block.go (marked 18.0.11 block rules) + tui/markdown_cjk.go (marked-cjk-friendly 0.1.2 emStrong: the CJK class and the previous-character recovery; Pi b2d0da6f)`, tui/markdown_latex.go containsTableStart (display math never spans a GFM table; Pi 2ed3118b); markedURL CJK-punctuation cut (Pi fa168515); tui/markdown_block_reuse_upstream_test.go (Pi 0f21a331 Block reuse: highlighting reused by language and code until Invalidate; rendered-block reuse keyed by raw is not ported, see its sync row) | ✅ |
| `packages/tui/src/components/select-list.ts` | `tui/select_list.go (SelectList) + tui/select_filterable.go (shared row layout and truncation)`, `tui/select_submenu.go`, `tui/tui.go` | ✅ |
| `packages/tui/src/components/settings-list.ts` | `tui/settings_list.go`, `tui/tui.go` | ✅ |
| `packages/tui/src/components/spacer.ts` | `tui/spacer.go`, `tui/tui.go` | ✅ |
| `packages/tui/src/components/text.ts` | `tui/text.go (SetText with the same text keeps the wrapped lines; tui/text_set_text_upstream_test.go)`, `tui/loader.go` | ✅ |
| `packages/tui/src/components/truncated-text.ts` | `tui/truncated_text.go (D66 clamps horizontal padding when the full padding cannot fit instead of emitting a fatal over-wide row)`, `tui/tui.go` | ✅ |
| `packages/tui/src/components/alt-screen-flash.ts` | `tui/alt_screen_flash.go (ported; default/explicit duration, timer removal, rendering, truncation, disposal, and ordering unit-tested in tui/alt_screen_flash_test.go, including TestAltScreenFlashExplicitNonPositiveDurationExpiresImmediately; composite-render wired in the alt-screen renderer (TuiAltScreen, mapped by the tui-alt-screen.ts row); flash("Copied!") production trigger wired via app.message.copy/Ctrl+X → confirmMessageCopied in InteractiveMode (internal/codingagent), unit-tested TestConfirmMessageCopied_FullscreenFlashesInsteadOfStatusLine)` | ✅ |
| `packages/tui/src/components/h-stack.ts` | `tui/stack.go (HStack, ported + unit-tested side-by-side/align compositing; Stack-base parity)` | ✅ |
| `packages/tui/src/components/scroll-view.ts` | `tui/scroll_view.go; coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-tui/components/scroll-view.js (pinned Pi for Node extensions)`, `tui/tui.go` | ✅ |
| `packages/tui/src/components/stack.ts` | `tui/stack.go` | ✅ |
| `packages/tui/src/components/v-stack.ts` | `tui/stack.go` | ✅ |
| `packages/tui/src/latex.ts` | `internal/latex/latex.go, symbols.go; upstream_test.go` | ✅ |
| `packages/tui/src/layout-node.ts` | `tui/layout_node.go` | ✅ |
| `packages/tui/src/layout.ts` | `tui/layout.go` | ✅ |
| `packages/tui/src/tui-alt-screen.ts` | `tui/tui_alt_screen.go, tui/tui_alt_screen_input.go, tui/tui_alt_screen_mouse.go, tui/tui_alt_screen_selection.go`, `tui/tui.go`, tui/terminal_exit_restore.go (exit prefix and suffix of the alternate screen) | ✅ |
| `packages/tui/src/tui-main-screen.ts` | `tui/tui.go, tui/bounded_terminal_writer.go, tui/redraw_debug.go, tui/render_overflow.go`, `tui/tui_mode_debug.go; OSC 133 strip of the regular-mode renderer (Pi 19ce8059): tui/tui.go stripOsc133ZonePrefixes, tui/layout.go stripOsc133ZonePrefix` | ✅ |
| `packages/tui/src/alt-screen-search.ts` | `tui/alt_screen_search.go` | ✅ |
| `packages/tui/src/components/mouse-region.ts` | `tui/mouse_region.go` | ✅ |
| `packages/tui/src/native-module-path.ts` | `(designed out: locates the Node native addon for pi-tui; the Go host has no Node module lookup or native addon, and its replacement is `internal/nativeplatform`, which makes direct dlopen, system DLL and X11 calls. Node-side evidence: TestNodeVendoredTuiUpstreamTests/native-module-path runs the upstream lookup cases against coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-tui/native-module-path.js for Node extensions)` | n/a |
| `packages/tui/src/native-platform.ts` | `tui/native_platform.go + internal/nativeplatform/clipboard*.go + coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-tui/native-platform.js and native/ (pinned Node helpers); Linux clipboard behavior is covered in Go and the shipped Node helper; the Windows and macOS helpers run the upstream cases, a text round trip (non-ASCII, empty, repeated writes) and an image round trip on the CI native jobs (internal/nativeplatform TestNativeClipboardTextRoundTrip, TestNativeClipboardImageRoundTrip, native_platform_upstream_windows_test.go, modifiers_darwin_test.go)` | ✅ |
| `packages/tui/src/colors.ts` | `tui/colors.go` | ✅ |
| `packages/tui/src/oklab.ts` | `tui/oklab.go` | ✅ |
| `packages/tui/src/wheel-scroll.ts` | `tui/wheel_scroll.go (the alt-screen wiring is part of tui-alt-screen.ts)` | ✅ |

## `packages/mcp/src/`

| upstream | pig | status |
|---|---|---|
| `packages/mcp/src/auth-provider.ts` | `mcp/auth_provider.go` | ✅ |
| `packages/mcp/src/client.ts` | `mcp/client.go` | ✅ |
| `packages/mcp/src/index.ts` | `(Go has no barrel exports: the exports are the mcp package)` | n/a |
| `packages/mcp/src/oauth/callback.ts` | `mcp/oauth/callback.go` | ✅ |
| `packages/mcp/src/oauth/discovery.ts` | `mcp/oauth/discovery.go` | ✅ |
| `packages/mcp/src/oauth/errors.ts` | `mcp/oauth/errors.go` | ✅ |
| `packages/mcp/src/oauth/flow.ts` | `mcp/oauth/flow.go` | ✅ |
| `packages/mcp/src/oauth/index.ts` | `(Go has no barrel exports: the exports are the mcp/oauth package)` | n/a |
| `packages/mcp/src/oauth/provider.ts` | `mcp/oauth/provider.go` | ✅ |
| `packages/mcp/src/oauth/types.ts` | `mcp/oauth/types.go; mcp/oauth/jsonextra.go` | ✅ |
| `packages/mcp/src/protocol/content.ts` | `mcp/content.go` | ✅ |
| `packages/mcp/src/protocol/jsonrpc.ts` | `mcp/jsonrpc.go`, `mcp/jsonrpc_notification.go` | ✅ |
| `packages/mcp/src/protocol/types.ts` | `mcp/types.go` | ✅ |
| `packages/mcp/src/testing/index.ts` | `(Go has no barrel exports: the exports are the mcp/mcptest package)` | n/a |
| `packages/mcp/src/transports/in-memory.ts` | `mcp/mcptest/inmemory.go` | ✅ |
| `packages/mcp/src/transports/stdio.ts` | `mcp/stdio.go; mcp/stdio_unix.go; mcp/stdio_windows.go` | ✅ |
| `packages/mcp/src/transports/streamable-http.ts` | `mcp/streamable_http.go; mcp/sse.go` (Send(ctx) carries McpTransportSendOptions.signal as its context) | ✅ |
| `packages/mcp/src/transports/transport.ts` | `mcp/transport.go` (Send(ctx) carries McpTransportSendOptions.signal as its context) | ✅ |
| `packages/mcp/src/config.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/mcp/src/input-schema.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/mcp/src/naming.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/mcp/src/oauth/auth-provider.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/mcp/src/oauth/client-settings.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/mcp/src/oauth/loopback.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/mcp/src/oauth/sign-in.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/mcp/src/output-limit.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/mcp/src/transports/lines.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/mcp/src/transports/pipe.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |

## `packages/codemode/src/`

| upstream | pig | status |
|---|---|---|
| `packages/codemode/src/cloudflare-modules.d.ts` | `(typing artefact: declares Wrangler's CompiledWasm import for the TypeScript entry; Go has no module import rule)` | n/a |
| `packages/codemode/src/cloudflare.ts` | `codemode/cloudflare/cloudflare.go (QuickJSWasm, CreateInlineSandbox, SandboxDurableObject); durable/core/host/cf/src/codemode/ (the Worker module and the Durable Object class)` | ✅ |
| `packages/codemode/src/declarations.ts` | `codemode/declarations.go (SchemaToType, RenderToolSignature, RenderToolSample, RenderDeclarations; a $ref with malformed percent-encoding returns the URIError as an error); TestSchemaToTypeMatchesPi, TestToolSignatureAndSampleMatchPi and TestRenderDeclarationsMatchesPi compare Pi's declarations.js on a deterministic corpus and seeded-random schemas (codemode/testdata/upstream-oracle.mjs), and every declarations.test.ts case is ported (test-mapping-v1.0.4.json)` | ✅ |
| `packages/codemode/src/identifier.ts` | `codemode/identifier.go` | ✅ |
| `packages/codemode/src/index.ts` | `(Go has no barrel exports: the exports are the codemode package)` | n/a |
| `packages/codemode/src/portable.ts` | `(Go has no barrel exports: the exports are the codemode package)` | n/a |
| `packages/codemode/src/runtime/execution.ts` | `codemode/execution.go (Execution, the host's deadline and result handling); codemode/vmlauncher.go (VmLauncher, VmChannel, VmEvents); codemode/sandbox.go (CodemodeSandboxBase); codemode/run.go (CodemodeSandboxBase.start); codemode/remote.go (RemoteCodemodeSandbox.close)` | ✅ |
| `packages/codemode/src/runtime/host.ts` | `codemode/sandbox.go; codemode/workerlauncher.go (workerLauncher: acquireWorker admission, onWorkerStarted exit reporting, stop without waiting for exit)` | ✅ |
| `packages/codemode/src/runtime/inline.ts` | `codemode/inline.go; codemode/inlinechannel.go; codemode/inline_consts.go; the halt instrumentation behind the interrupt budget (haltcheck, halt_grace) and the wasm loader option (SandboxOptions.Wasm) live in the same package` | ✅ |
| `packages/codemode/src/runtime/prelude-source.ts` | `codemode/assets/prelude.js; codemode/assets.go` | ✅ |
| `packages/codemode/src/runtime/protocol.ts` | `codemode/vmdriver.go (WorkerToHostMessage, HostToWorkerMessage as the host/VM seam); codemode/remote_protocol.go (VmStartData and the remote wire forms)` | ✅ |
| `packages/codemode/src/runtime/remote.ts` | `codemode/remote.go (RemoteCodemodeSandbox, remoteLauncher); codemode/remote_serve.go; codemode/remote_serve_blocking.go (serveCodemodeRemote); codemode/remote_protocol.go; codemode/remote_types.go` | ✅ |
| `packages/codemode/src/runtime/vm.ts` | `codemode/vmdriver.go (startVm, VmSession); codemode/engine.go; codemode/engine_wazero.go; codemode/deterministic.go (deterministic mode, prng, fnv1a, VirtualClock)` | ✅ |
| `packages/codemode/src/runtime/worker.ts` | `(no worker thread: QuickJS runs on wazero in the host process, codemode/engine.go)` | n/a |
| `packages/codemode/src/source.ts` | `codemode/source.go` | ✅ |
| `packages/codemode/src/types.ts` | `codemode/types.go`, `codemode/sandbox.go`, `codemode/run.go`, `codemode/remote.go` | ✅ |
| `packages/codemode/src/wasm.ts` | `codemode/assets_quickjs.go`, `codemode/wasm.go` | ✅ |

## `packages/chord/src/`

| upstream | pig | status |
|---|---|---|
| `packages/chord/src/json.ts` | `internal/chord/chordjson/chordjson.go:70 Copy, Stored, IsValue; chord/json.go:12`, `chord/delta/delta.go`, `durable/json.go CopyJson` | ✅ |
| `packages/chord/src/context/index.ts` | `chord/context/chordctx.go Background, TODO, NewKey, WithValue, WithAbortSignal, WithoutAbortSignal, WithCancel, Await, Telemetry, WithTelemetry, TraceIn (Pi 92ab5360 moves traceIn's context last, traceIn(options, fn, context); TraceIn keeps the context first, as every Go function does)`, `chord/next/host/host_ctx.go` | ✅ |
| `packages/chord/src/delta/index.ts` | `chord/delta/delta.go:317 AssertValidOpValue, apply.go:227 Apply, ApplyImmutable, apply.go:255 ApplyImmutableBatchesSeq, codec.go:17 Encoder/Decoder, overlap.go:17; ownedSlice is a V8 string-view detail that Go strings.Clone covers where an op or value keeps a cut of a larger string` | ✅ |
| `packages/chord/src/delta/pending.ts` | `chord/delta/pending.go (PendingOps, PendingTake, MaxPendingOps; chord/delta/pending_test.go)` | ✅ |
| `packages/chord/src/delta/diff.ts` | `chord/delta/diff.go:611 DiffRevisions` | ✅ |
| `packages/chord/src/delta/tracker.ts` | `chord/delta/tracker.go + chord/delta/handles.go (overlay with Object/Array draft handles, the Proxy draft; array roots through TrackArray, Change.Elements, Tracker.Root, Prepared.ValueRoot, PrepareReplaceRoot; AppendText, the draft's append of text to a string member)`; second mapped port: durable/core/internal/delta/tracker.go, the core copy over jv values with no registry: staleness is by revision (assertOpen, Change.Prepare, Tracker.Adopt refuse a draft whose baseRevision differs), which is what Pi a9228305's #open/#competitors registry computes, so the registry mechanics are designed out (core purity rules forbid sync, reflect and runtime) and the competing-change cases run against it in durable/core/internal/delta/tracker_competing_test.go; an abandoned draft is reclaimed at once instead of at the next change, a GC-timing difference no caller observes) | ✅ |
| `packages/chord/src/delta/draft.ts` | `TypeScript mapped type; the Go draft is chord/delta Object/Array` | n/a |
| `packages/chord/src/delta/apply-immutable-trusted.ts` | `self-produced-op fast path of the tracker; the Go trackers materialize candidates themselves and ApplyImmutable covers the checked path (apply_immutable_upstream_test.go)` | n/a |
| `packages/chord/src/delta/revision-validator.ts` | `WeakSet memo of validated JS container identities; a decoded Go revision has no shared identity and is validated by chordjson.Validate (state.go:917)` | n/a |
| `packages/chord/src/output.ts` | `chord/output/output.go OutputBuffer, Progress, BoundOutput, SanitizeOutput, CharacterEnd, Utf8ByteLength, OutputLimits, OutputSlice, BoundedOutput, OutputSkip` | ✅ |
| `packages/chord/src/sqlite/cloudflare.ts` | `chord/sqlite/cloudflare/cloudflare.go` | ✅ |
| `packages/chord/src/sqlite/database.ts` | `chord/sqlite/database.go` | ✅ |
| `packages/chord/src/sqlite/index.ts` | `(re-export barrel: the exports are the Go package surface)` | n/a |
| `packages/chord/src/sqlite/migrations.ts` | `chord/sqlite/migrations.go` | ✅ |
| `packages/chord/src/sqlite/node.ts` | `chord/sqlite/node/node.go` | ✅ |
| `packages/chord/src/sqlite/schema.ts` | `chord/sqlite/schema.go` | ✅ |
| `packages/chord/src/watch.ts` | `chord/watch.go WatchEnd, WatchEndReason, WatchHandle, Watchable, NativeWatchable (Durable's package re-exports them as type aliases, as Pi's durable index does); chord/watch_impl.go Watch, NativeWatch, NativeWatchHandle, DeliveryScheduler (the delivery goroutines that stand for queueMicrotask); chord/next/documents/handles.go SingletonHandle (Pi's `SingletonHandle<T> extends Watchable<T>`: its Value and Watch are the two members watch.ts's interface contributes to the next layer)` | ✅ |
| `packages/chord/src/telemetry-sqlite/index.ts` | `chord/telemetrysqlite/index.go` | ✅ |
| `packages/chord/src/telemetry-sqlite/store.ts` | `chord/telemetrysqlite/store.go` | ✅ |
| `packages/chord/src/contracts/accepts.ts` | `chord/next/contracts/accepts.go` | ✅ |
| `packages/chord/src/contracts/contract.ts` | `chord/next/contracts/contract.go` | ✅ |
| `packages/chord/src/contracts/declare.ts` | `chord/next/contracts/declare.go` | ✅ |
| `packages/chord/src/contracts/evolve.ts` | `chord/next/contracts/evolve.go` | ✅ |
| `packages/chord/src/contracts/fingerprint.ts` | `chord/next/contracts/fingerprint.go` | ✅ |
| `packages/chord/src/contracts/history.ts` | `chord/next/contracts/history.go` | ✅ |
| `packages/chord/src/contracts/host-kind.ts` | `chord/next/contracts/host_kind.go` | ✅ |
| `packages/chord/src/contracts/index.ts` | `(re-export barrel; Go packages export names directly)` | n/a |
| `packages/chord/src/contracts/internal.ts` | `(re-export barrel for Chord's layers)` | n/a |
| `packages/chord/src/contracts/names.ts` | `chord/next/contracts/names.go` | ✅ |
| `packages/chord/src/contracts/params.ts` | `chord/next/contracts/params.go` | ✅ |
| `packages/chord/src/contracts/schema.ts` | `chord/next/contracts/schema.go` | ✅ |
| `packages/chord/src/contracts/snapshot.ts` | `chord/next/contracts/snapshot.go` | ✅ |
| `packages/chord/src/data/args.ts` | `chord/next/data/args.go` | ✅ |
| `packages/chord/src/data/errors.ts` | `chord/next/data/errors.go` | ✅ |
| `packages/chord/src/data/failure.ts` | `chord/next/data/failure.go` | ✅ |
| `packages/chord/src/data/index.ts` | `(re-export barrel)` | n/a |
| `packages/chord/src/data/internal.ts` | `(re-export barrel)` | n/a |
| `packages/chord/src/data/params.ts` | `chord/next/data/params.go` | ✅ |
| `packages/chord/src/data/principal.ts` | `chord/next/data/principal.go` | ✅ |
| `packages/chord/src/data/validator.ts` | `chord/next/data/validator.go` | ✅ |
| `packages/chord/src/index.ts` | `(aggregate barrel of every layer; Go imports the layer packages)` | n/a |
| `packages/chord/src/registries/contribution.ts` | `chord/next/registries/contribution.go` | ✅ |
| `packages/chord/src/registries/entries.ts` | `chord/next/registries/entries.go` | ✅ |
| `packages/chord/src/registries/index.ts` | `(re-export barrel)` | n/a |
| `packages/chord/src/registries/internal.ts` | `chord/next/registries/internal.go` | ✅ |
| `packages/chord/src/registries/record.ts` | `chord/next/registries/record.go` | ✅ |
| `packages/chord/src/registries/registry.ts` | `chord/next/registries/registry.go` | ✅ |
| `packages/chord/src/registries/value.ts` | `chord/next/registries/value.go` | ✅ |
| `packages/chord/src/state/batch.ts` | `chord/next/state/batch.go` | ✅ |
| `packages/chord/src/state/derive.ts` | `chord/next/state/derive.go` | ✅ |
| `packages/chord/src/state/index.ts` | `(re-export barrel)` | n/a |
| `packages/chord/src/state/internal.ts` | `(re-export barrel)` | n/a |
| `packages/chord/src/state/native-source.ts` | `chord/next/state/native_source.go` | ✅ |
| `packages/chord/src/state/watchable.ts` | `chord/next/state/watchable.go, chord/next/state/set.go` | ✅ |
| `packages/chord/src/state/writable.ts` | `chord/next/state/writable.go` | ✅ |
| `packages/chord/src/node/compression.ts` | `chord/nextnode/compression.go` | ✅ |
| `packages/chord/src/link/binary.ts` | `chord/next/link/binary.go` | ✅ |
| `packages/chord/src/link/channel.ts` | `chord/next/link/channel.go` | ✅ |
| `packages/chord/src/link/errors.ts` | `chord/next/link/errors.go` | ✅ |
| `packages/chord/src/link/index.ts` | `(re-export barrel; Go packages export names directly)` | n/a |
| `packages/chord/src/link/internal.ts` | `(re-export barrel for Chord's layers)` | n/a |
| `packages/chord/src/link/ipc.ts` | `chord/next/link/ipc.go` | ✅ |
| `packages/chord/src/link/link.ts` | `chord/next/link/link.go` | ✅ |
| `packages/chord/src/link/message.ts` | `chord/next/link/message.go` | ✅ |
| `packages/chord/src/link/part.ts` | `chord/next/link/part.go` | ✅ |
| `packages/chord/src/link/text.ts` | `chord/next/link/text.go` | ✅ |
| `packages/chord/src/link/values.ts` | `chord/next/link/values.go` | ✅ |
| `packages/chord/src/link/websocket.ts` | `chord/next/link/websocket.go` | ✅ |
| `packages/chord/src/store/table-writes.ts` | `chord/next/store/table_writes.go` | ✅ |
| `packages/chord/src/store/table-changes.ts` | `chord/next/store/table_changes.go` | ✅ |
| `packages/chord/src/store/table-handle.ts` | `chord/next/store/table_handle.go` | ✅ |
| `packages/chord/src/store/engine.ts` | `chord/next/store/engine.go` | ✅ |
| `packages/chord/src/store/outbox.ts` | `chord/next/store/outbox.go` | ✅ |
| `packages/chord/src/store/store.ts` | `chord/next/store/store.go (group commit S18 as enlist/runGroup; Pi's d7704516e int() key typing is TypeScript typing: designed out)` | ✅ |
| `packages/chord/src/store/applier.ts` | `chord/next/store/applier.go` | ✅ |
| `packages/chord/src/store/apply-batch.ts` | `chord/next/store/apply_batch.go` | ✅ |
| `packages/chord/src/store/index.ts` | `(export barrel: the Go package exports the names directly. Each exported name of the list exists in package store: ArchivePart (ArchiveManifest is ManifestPart), Backend/SqliteBackend, expression builders (Add And ColumnRef Condition Coalesce Concat Expression Eq Gt Gte InArray IsNull Lt Lte Max Min Ne Not Or Prefix ReplacePrefix SetOf Sub When), ImportStore, JsonColumnOptions/Json, DecodeJsonl/EncodeJsonl, Outbox/OutboxCommit, ColumnRefs/Query, OpenStore/Store/StoreOptions, DefineTable/TableDefinition/TableOptions, EvolveTable/TableEditor, Page/PageOptions, CompareText, StoreWrite. Type-level only, designed out by the other D3 agents: Columns PutRowOf RowOf KeyOf PatchOf AddedColumn TableTailEditor TableOf Table)` | n/a |
| `packages/chord/src/store/archive.ts` | `chord/next/store/archive.go` | ✅ |
| `packages/chord/src/store/export.ts` | `chord/next/store/export.go` | ✅ |
| `packages/chord/src/store/import.ts` | `chord/next/store/import.go` | ✅ |
| `packages/chord/src/store/text.ts` | `chord/next/store/text.go` | ✅ |
| `packages/chord/src/store/allocator.ts` | `chord/next/store/allocator.go` | ✅ |
| `packages/chord/src/store/claims.ts` | `chord/next/store/claims.go` | ✅ |
| `packages/chord/src/store/refusal.ts` | `chord/next/store/refusal.go` | ✅ |
| `packages/chord/src/store/columns.ts` | `chord/next/store/columns.go` | ✅ |
| `packages/chord/src/store/json-column.ts` | `chord/next/store/json_column.go` | ✅ |
| `packages/chord/src/store/rows.ts` | `chord/next/store/rows.go` | ✅ |
| `packages/chord/src/store/sql.ts` | `chord/next/store/sql.go` | ✅ |
| `packages/chord/src/store/jsonl.ts` | `chord/next/store/jsonl.go` | ✅ |
| `packages/chord/src/store/catalog.ts` | `chord/next/store/catalog.go` | ✅ |
| `packages/chord/src/store/expression.ts` | `chord/next/store/expression.go` | ✅ |
| `packages/chord/src/store/evaluate.ts` | `chord/next/store/evaluate.go` | ✅ |
| `packages/chord/src/store/query.ts` | `chord/next/store/query.go`, `chord/next/store/expression.go` | ✅ |
| `packages/chord/src/store/table.ts` | `chord/next/store/table.go` | ✅ |
| `packages/chord/src/store/table-history.ts` | `chord/next/store/table_history.go` | ✅ |
| `packages/chord/src/store/table-evolve.ts` | `chord/next/store/table_evolve.go` | ✅ |
| `packages/chord/src/store/table-migration.ts` | `chord/next/store/table_migration.go` | ✅ |
| `packages/chord/src/store/adapter.ts` | `chord/next/store/adapter.go` | ✅ |
| `packages/chord/src/store/backend.ts` | `chord/next/store/backend.go` | ✅ |
| `packages/chord/src/store/schema.ts` | `chord/next/store/schema.go` | ✅ |
| `packages/chord/src/store/step.ts` | `chord/next/store/step.go` | ✅ |
| `packages/chord/src/store/writes.ts` | `(type-only: the StoreWrite union; chord/next/store/writes.go StoreWrite interface)` | n/a |
| `packages/chord/src/documents/address-source.ts` | `chord/next/documents/address_source.go` | ✅ |
| `packages/chord/src/documents/definition.ts` | `chord/next/documents/definition.go` | ✅ |
| `packages/chord/src/documents/documents.ts` | `chord/next/documents/documents.go` | ✅ |
| `packages/chord/src/documents/fence.ts` | `chord/next/documents/fence.go` | ✅ |
| `packages/chord/src/documents/handles.ts` | `chord/next/documents/handles.go` | ✅ |
| `packages/chord/src/documents/index.ts` | `(re-export barrel; the app-facing set is listed in doc.go)` | n/a |
| `packages/chord/src/documents/internal.ts` | `(re-export barrel for Chord's layers)` | n/a |
| `packages/chord/src/documents/keyed-slot.ts` | `chord/next/documents/keyed_slot.go` | ✅ |
| `packages/chord/src/documents/line.ts` | `chord/next/documents/line.go` | ✅ |
| `packages/chord/src/documents/persistence.ts` | `chord/next/documents/persistence.go` | ✅ |
| `packages/chord/src/documents/residency.ts` | `chord/next/documents/residency.go` | ✅ |
| `packages/chord/src/documents/slot.ts` | `chord/next/documents/slot.go` | ✅ |
| `packages/chord/src/documents/transaction.ts` | `chord/next/documents/transaction.go, chord/next/documents/job.go` | ✅ |
| `packages/chord/src/host/backoff.ts` | `chord/next/host/host_backoff.go` | ✅ |
| `packages/chord/src/host/deadline.ts` | `chord/next/host/host_deadline.go` | ✅ |
| `packages/chord/src/host/sleep.ts` | `chord/next/host/host_sleep.go` | ✅ |
| `packages/chord/src/host/uuid.ts` | `chord/next/host/host_uuid.go` | ✅ |
| `packages/chord/src/host/activity.ts` | `chord/next/host/host_activity.go` | 🟡 |
| `packages/chord/src/host/revocation.ts` | `chord/next/host/host_revocation.go` | 🟡 |
| `packages/chord/src/host/unbound.ts` | `chord/next/host/host_unbound.go` | ✅ |
| `packages/chord/src/host/retry-on.ts` | `chord/next/host/host_retry_on.go` | ✅ |
| `packages/chord/src/host/ref-state.ts` | `chord/next/host/host_ref_state.go` | ✅ |
| `packages/chord/src/host/dependencies.ts` | `chord/next/host/host_dependencies.go` | ✅ |
| `packages/chord/src/host/parked.ts` | `chord/next/host/host_parked.go` | 🟡 |
| `packages/chord/src/host/native.ts` | `chord/next/host/host_native.go` | 🟡 |
| `packages/chord/src/host/follow.ts` | `chord/next/host/host_follow.go` | ✅ |
| `packages/chord/src/host/members.ts` | `chord/next/host/host_members.go` | ✅ |
| `packages/chord/src/host/ctx.ts` | `chord/next/host/host_ctx.go` | 🟡 |
| `packages/chord/src/host/root.ts` | `chord/next/host/host_root.go` | ✅ |
| `packages/chord/src/host/routines.ts` | `chord/next/host/host_routines.go` | ✅ |
| `packages/chord/src/host/facet.ts` | `chord/next/host/host_facet.go` | ✅ |
| `packages/chord/src/host/presence.ts` | `chord/next/host/host_presence.go` | 🟡 |
| `packages/chord/src/host/route.ts` | `chord/next/host/host_route.go` | ✅ |
| `packages/chord/src/host/destination.ts` | `chord/next/host/host_destination.go` | ✅ |
| `packages/chord/src/host/index.ts` | `(barrel: Go imports the package (DESIGN-go-layout.md section 2); what it exports is exported by the files above (Ctx, Facet, FacetHost, Host..., Sleep, UntilAborted, Presence, RefState, RevokedBy, RootOptions, RouteOptions, RoutineDeclarations, Native*, Follow*, GetOptions, HostError, Position; Host, HostOptions, LinkOptions, createHost, Factory, Self, StoreSelf, Held, Reference, RefIn are D5a's))` | n/a |
| `packages/chord/src/host/internal.ts` | `(barrel for jobs/remote: one package, identifiers are unexported where Pi keeps them out of index.ts (DESIGN-go-layout.md sections 1-2))` | n/a |
| `packages/chord/src/host/host.ts` | `chord/next/host/host_host.go, chord/next/host/host_error.go` | ✅ |
| `packages/chord/src/host/attempt.ts` | `chord/next/host/host_attempt.go` | ✅ |
| `packages/chord/src/host/object.ts` | `chord/next/host/host_object.go` | ✅ |
| `packages/chord/src/host/reference.ts` | `chord/next/host/host_reference.go` | ✅ |
| `packages/chord/src/jobs/definition.ts` | `chord/next/host/jobs_definition.go` | ✅ |
| `packages/chord/src/jobs/persisted.ts` | `chord/next/host/jobs_persisted.go` | ✅ |
| `packages/chord/src/jobs/records.ts` | `chord/next/host/jobs_records.go` | ✅ |
| `packages/chord/src/jobs/run-states.ts` | `chord/next/host/jobs_run_states.go` | ✅ |
| `packages/chord/src/jobs/run.ts` | `chord/next/host/jobs_run.go, chord/next/documents/job.go` | ✅ |
| `packages/chord/src/jobs/running.ts` | `chord/next/host/jobs_running.go` | ✅ |
| `packages/chord/src/jobs/stored.ts` | `chord/next/host/jobs_stored.go` | ✅ |
| `packages/chord/src/jobs/index.ts` | `(barrel: Go imports the package (DESIGN-go-layout.md section 2); what it exports is exported by the files above: DefineJob, JobAccepts, JobDefinition, JobError, JobOptions, Outcome, JobCtx, JobHandle, JobImplementation, RunState, StartOptions, BlockedReason, Run; RunningEntry is type-level (jobs_running.go))` | n/a |
| `packages/chord/src/jobs/internal.ts` | `(barrel for host/remote: one package, Job, JobRecords, JobContext, Staging, ... are unexported where Pi keeps them out of index.ts (DESIGN-go-layout.md sections 1-2))` | n/a |
| `packages/chord/src/remote/hold.ts` | `chord/next/host/remote_hold.go` | ✅ |
| `packages/chord/src/remote/remote-object.ts` | `chord/next/host/remote_remote_object.go` | ✅ |
| `packages/chord/src/remote/host-link.ts` | `chord/next/host/remote_host_link.go` | ✅ |
| `packages/chord/src/remote/path.ts` | `chord/next/host/remote_path.go` | ✅ |
| `packages/chord/src/remote/calls.ts` | `chord/next/host/remote_calls.go` | ✅ |
| `packages/chord/src/remote/provider.ts` | `chord/next/host/remote_provider.go` | ✅ |
| `packages/chord/src/remote/consumer.ts` | `chord/next/host/remote_consumer.go` | ✅ |
| `packages/chord/src/remote/relay.ts` | `chord/next/host/remote_relay.go` | ✅ |
| `packages/chord/src/remote/replica.ts` | `chord/next/host/remote_replica.go` | ✅ |
| `packages/chord/src/remote/internal.ts` | `(barrel for Chord's own layers: Go imports the package; what it exported (Path, Upstream, HostLink, HoldRequest, actingFor, ...) is unexported in Go (DESIGN-go-layout.md section 2))` | n/a |
| `packages/chord/src/jobs/job.ts` | `chord/next/host/jobs_job.go` | ✅ |
| `packages/chord/test/examples/01-objects.ts` | `chord/next/host/host_example_01_test.go`, `chord/next/host/host_host.go`, `chord/next/host/host_object.go`, `chord/next/data/args.go` | ✅ |
| `packages/chord/test/examples/02-documents.ts` | `chord/next/host/host_example_02_test.go`, `chord/next/documents/transaction.go`, `chord/next/documents/slot.go`, `chord/next/documents/handles.go` | ✅ |
| `packages/chord/test/examples/03-derives.ts` | `chord/next/host/host_example_03_test.go`, `chord/next/state/derive.go` | ✅ |
| `packages/chord/test/examples/04-watches.ts` | `chord/next/host/host_example_04_test.go`, `chord/next/state/derive.go`, `chord/next/documents/address_source.go` | ✅ |
| `packages/chord/test/examples/05-errors.ts` | `chord/next/host/host_example_05_test.go`, `chord/next/host/host_dependencies.go`, `chord/next/host/host_unbound.go`, `chord/next/data/validator.go` | ✅ |
| `packages/chord/test/examples/06-natives.ts` | `chord/next/host/host_example_06_test.go`, `chord/next/host/host_native.go` | ✅ |
| `packages/chord/test/examples/07-failures.ts` | `chord/next/host/host_example_07_test.go`, `chord/next/host/host_backoff.go`, `chord/next/host/host_dependencies.go` | ✅ |
| `packages/chord/test/examples/08-remote.ts` | `chord/next/host/host_example_08_test.go`, `chord/next/host/host_destination.go`, `chord/next/host/remote_remote_object.go`, `chord/next/host/remote_replica.go` | ✅ |
| `packages/chord/test/examples/09-calls.ts` | `chord/next/host/host_example_09_test.go`, `chord/next/link/values.go`, `chord/next/link/errors.go`, `chord/next/host/host_ctx.go`, `chord/next/host/remote_consumer.go`, `chord/next/host/remote_provider.go` | ✅ |
| `packages/chord/test/examples/10-drops.ts` | `chord/next/host/host_example_10_test.go`, `chord/next/host/host_parked.go`, `chord/next/host/host_reference_source.go`, `chord/next/host/host_reference.go` | ✅ |
| `packages/chord/test/examples/11-linger-and-retry.ts` | `chord/next/host/host_example_11_test.go`, `chord/next/host/host_object.go`, `chord/next/host/host_backoff.go` | ✅ |
| `packages/chord/test/examples/12-routes.ts` | `chord/next/host/host_example_12_test.go`, `chord/next/host/host_route.go`, `chord/next/host/host_ctx.go` | ✅ |
| `packages/chord/test/examples/13-relay.ts` | `chord/next/host/host_example_13_test.go`, `chord/next/link/websocket.go`, `chord/next/link/ipc.go`, `chord/next/host/remote_relay.go` | ✅ |
| `packages/chord/test/examples/14-flow-control.ts` | `chord/next/host/host_example_14_test.go`, `chord/next/host/remote_hold.go`, `chord/next/host/remote_provider.go` | ✅ |
| `packages/chord/test/examples/15-revocation.ts` | `chord/next/host/host_example_15_test.go`, `chord/next/host/host_revocation.go`, `chord/next/host/host_host.go`, `chord/next/host/remote_host_link.go` | ✅ |
| `packages/chord/test/examples/16-presence-and-activity.ts` | `chord/next/host/host_example_16_test.go`, `chord/next/host/host_activity.go`, `chord/next/host/host_presence.go` | ✅ |
| `packages/chord/test/examples/17-limits-and-compression.ts` | `chord/next/host/host_example_17_test.go`, `chord/next/link/part.go`, `chord/nextnode/compression.go`, `chord/next/link/channel.go` | ✅ |
| `packages/chord/test/examples/18-routines.ts` | `chord/next/host/host_example_18_test.go`, `chord/next/host/host_routines.go`, `chord/next/host/host_sleep.go` | ✅ |
| `packages/chord/test/examples/19-jobs.ts` | `chord/next/host/host_example_19_test.go`, `chord/next/host/jobs_job.go`, `chord/next/host/jobs_run_states.go`, `chord/next/host/jobs_records.go`, `chord/next/host/jobs_running.go`, `chord/next/host/jobs_run.go` | ✅ |
| `packages/chord/test/examples/20-tables.ts` | `chord/next/host/host_example_20_test.go`, `chord/next/store/query.go`, `chord/next/store/table_handle.go`, `chord/next/store/rows.go`, `chord/next/store/sql.go`, `chord/next/store/columns.go`, `chord/next/store/table_writes.go`, `chord/next/store/table_changes.go` | ✅ |
| `packages/chord/test/examples/21-persisted-documents.ts` | `chord/next/host/host_example_21_test.go`, `chord/next/store/engine.go`, `chord/next/documents/persistence.go`, `chord/next/documents/keyed_slot.go` | ✅ |
| `packages/chord/test/examples/22-migrations.ts` | `chord/next/host/host_example_22_test.go`, `chord/next/store/table_evolve.go`, `chord/next/store/table_migration.go`, `chord/next/store/catalog.go` | ✅ |
| `packages/chord/test/examples/23-keyed-watches.ts` | `chord/next/host/host_example_23_test.go`, `chord/next/documents/keyed_slot.go`, `chord/next/documents/address_source.go` | ✅ |
| `packages/chord/test/examples/24-keyed-members.ts` | `chord/next/host/host_example_24_test.go`, `chord/next/host/host_reference_keys.go`, `chord/next/host/host_members.go` | ✅ |
| `packages/chord/test/examples/25-export-import.ts` | `chord/next/host/host_example_25_test.go`, `chord/next/store/export.go`, `chord/next/store/import.go`, `chord/next/store/jsonl.go`, `chord/next/store/applier.go`, `chord/next/store/archive.go` | ✅ |
| `packages/chord/test/examples/28-persisted-jobs.ts` | `chord/next/host/host_example_28_test.go`, `chord/next/host/jobs_persisted.go`, `chord/next/host/jobs_stored.go`, `chord/next/host/jobs_run_states.go`, `chord/next/host/jobs_records.go` | ✅ |
| `packages/chord/test/examples/29-registries.ts` | `chord/next/host/host_example_29_test.go`, `chord/next/registries/registry.go`, `chord/next/registries/contribution.go`, `chord/next/registries/value.go`, `chord/next/registries/entries.go`, `chord/next/registries/record.go` | ✅ |
| `packages/chord/test/examples/30-host-root.ts` | `chord/next/host/host_example_30_test.go`, `chord/next/host/host_root.go`, `chord/next/host/host_ctx.go` | ✅ |
| `packages/chord/test/examples/31-parked-dependencies.ts` | `chord/next/host/host_example_31_test.go`, `chord/next/host/host_dependencies.go`, `chord/next/host/host_unbound.go` | ✅ |
| `packages/chord/test/examples/32-startup.ts` | `chord/next/host/host_example_32_test.go`, `chord/next/host/remote_remote_object.go`, `chord/next/host/host_host.go` | ✅ |
| `packages/chord/test/support.ts` | `(Pi test scaffolding; ported as the shared helpers in chord/next/host/host_next_support_test.go, tested by host_next_support_helpers_test.go; its fidelity is evidenced by the ported slice and example tests built on it. Forced difference: holdNextCommit holds the next commit step at the statements that close its writes, since the store type is concrete)` | n/a |
| `packages/chord/src/node/http/admission.ts` | `chord/nextnode/http/admission.go` | ✅ |
| `packages/chord/src/node/http/authenticate.ts` | `chord/nextnode/http/authenticate.go` | ✅ |
| `packages/chord/src/node/http/cookies.ts` | `chord/nextnode/http/cookies.go` | ✅ |
| `packages/chord/src/node/http/file.ts` | `chord/nextnode/http/file.go` | ✅ |
| `packages/chord/src/node/http/headers.ts` | `chord/nextnode/http/headers.go` | ✅ |
| `packages/chord/src/node/http/http.ts` | `chord/nextnode/http/http.go` | ✅ |
| `packages/chord/src/node/http/incoming.ts` | `chord/nextnode/http/incoming.go` | ✅ |
| `packages/chord/src/node/http/index.ts` | `(barrel: Go imports the package; exported names are those of Pi's index.ts, the rest is unexported)` | n/a |
| `packages/chord/src/node/http/node.ts` | `chord/nextnode/http/node.go, chord/nextnode/http/url.go` | ✅ |
| `packages/chord/src/node/http/request.ts` | `chord/nextnode/http/request.go` | ✅ |
| `packages/chord/src/node/http/response.ts` | `chord/nextnode/http/response.go, chord/nextnode/http/statuscodes.go` | ✅ |
| `packages/chord/src/node/http/router.ts` | `chord/nextnode/http/router.go` | ✅ |
| `packages/chord/src/node/http/upgrades.ts` | `chord/nextnode/http/upgrades.go, chord/nextnode/http/websocket.go` | ✅ |
| `packages/chord/src/node/plugins/build.ts` | `chord/nextnode/plugins/plugins_build.go, chord/nextnode/plugins/plugins_sourcemap.go` | ✅ |
| `packages/chord/src/node/plugins/discover.ts` | `chord/nextnode/plugins/plugins_discover.go` | ✅ |
| `packages/chord/src/node/plugins/evaluate.ts` | `chord/nextnode/plugins/plugins_runtime.go; Pi's file runs verbatim in the Node subprocess: chord/nextnode/plugins/node/pi/chord/src/next-node/plugins/evaluate.ts, node/host.mjs` | ✅ |
| `packages/chord/src/node/plugins/index.ts` | `(barrel: Go imports the package)` | n/a |
| `packages/chord/src/node/plugins/install.ts` | `chord/nextnode/plugins/plugins_install.go` | ✅ |
| `packages/chord/src/node/plugins/plugins.ts` | `chord/nextnode/plugins/plugins_plugins.go` | ✅ |
| `packages/chord/src/node/plugins/runtime.ts` | `chord/nextnode/plugins/plugins_runtime.go, chord/nextnode/plugins/plugins_node.go; Pi's file runs verbatim in the Node subprocess: chord/nextnode/plugins/node/pi/chord/src/next-node/plugins/runtime.ts` | ✅ |
| `packages/chord/src/node/plugins/stack.ts` | `Pi's file runs verbatim in the Node subprocess: chord/nextnode/plugins/node/pi/chord/src/next-node/plugins/stack.ts; Go sees *NodeError.Stack (chord/nextnode/plugins/plugins_node.go)` | ✅ |
| `packages/chord/src/node/plugins/watcher.ts` | `chord/nextnode/plugins/plugins_watcher.go` | ✅ |
| `packages/chord/src/host/adapted.ts` | `chord/next/host/host_adapted.go` | ✅ |
| `packages/chord/src/host/reference-keys.ts` | `chord/next/host/host_reference_keys.go` | ✅ |
| `packages/chord/src/host/reference-source.ts` | `chord/next/host/host_reference_source.go` | ✅ |
| `packages/chord/src/host/staging.ts` | `chord/next/host/host_staging.go, chord/next/host/host_host.go` | ✅ |
| `packages/chord/src/node/workers/control.ts` | `chord/nextnode/workers/workers_control.go` | ✅ |
| `packages/chord/src/node/workers/index.ts` | `(re-export barrel; the exported set is the package's (CrossedError, ProcessLock, WorkerMain, WorkerStart, WorkerPlugins, WorkerResult, WorkerMainOptions, CreateWorkers, WorkerState, Workers, WorkersError, WorkersOptions))` | n/a |
| `packages/chord/src/node/workers/lock.ts` | `chord/nextnode/workers/workers_lock.go, workers_lock_unix.go` (the Windows named-pipe variant workers_lock_windows is built only on Windows and is run by the native Windows CI job) | ✅ |
| `packages/chord/src/node/workers/main.ts` | `chord/nextnode/workers/workers_main.go` | ✅ |
| `packages/chord/src/node/workers/process.ts` | `chord/nextnode/workers/workers_process.go, workers_process_unix.go, workers_channel.go, workers_channel_unix.go` (the Windows variants workers_process_windows and workers_channel_windows are built only on Windows and are run by the native Windows CI job) | ✅ |
| `packages/chord/src/node/workers/workers.ts` | `chord/nextnode/workers/workers_workers.go, workers_outcome.go` | ✅ |
| `packages/chord/src/host/old-work.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/host/readiness.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/host/reload-cycle.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/host/unbound-clock.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/app/app.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/app/archive.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/app/blobs.ts` | `chord/nextnode/app/blobs.go` | ✅ |
| `packages/chord/src/node/app/data.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/app/ephemeral-sessions.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/app/ephemeral.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/app/files-copy.ts` | `chord/nextnode/app/files_copy.go` | ✅ |
| `packages/chord/src/node/app/files-entry.ts` | `(barrel: Go imports the package; the files-entry exports are FilesFacet, Files, FilesFolder, CopyFiles, PrivateDirectory, PrivateStore and the types)` | n/a |
| `packages/chord/src/node/app/files-index.ts` | `chord/nextnode/app/files_index.go` | ✅ |
| `packages/chord/src/node/app/files.ts` | `chord/nextnode/app/files.go (handlers suspend before their first wait and `turn` is a mutex, D430)` | ✅ |
| `packages/chord/src/node/app/folder-views.ts` | `chord/nextnode/app/folder_views.go` | ✅ |
| `packages/chord/src/node/app/index.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/app/info.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/app/invite.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/app/layout.ts` | `chord/nextnode/app/layout.go` | ✅ |
| `packages/chord/src/node/app/owner.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/app/pairing.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/app/secrets.ts` | `chord/nextnode/app/secrets.go` | ✅ |
| `packages/chord/src/node/app/sessions.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/app/tar.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/app/uploads.ts` | `chord/nextnode/app/uploads.go (Accept is the synchronous half of Pi's append, D430)` | ✅ |
| `packages/chord/src/node/app/users.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/app/worker.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/plugins/base-inputs.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/plugins/css-modules.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/plugins/testing.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/surfaces/bundle.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/surfaces/current.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/surfaces/index.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/surfaces/published.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/surfaces/served.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/surfaces/tui.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/surfaces/web.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/tui/actions.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/tui/credentials.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/tui/filter.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/tui/index.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/tui/launcher.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/tui/pipe.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/tui/sandbox.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/workers/base.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/node/workers/child.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/react/hooks.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/react/index.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/react/stores.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/store/live-query.ts` | `chord/next/store/live_query.go (D431)` | ✅ |
| `packages/chord/src/surfaces/active-language.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/surfaces/active-theme.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/surfaces/client.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/surfaces/connection.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/surfaces/format.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/surfaces/index.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/surfaces/language.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/surfaces/loaded.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/surfaces/locale-choice.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/surfaces/messages.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/surfaces/page-state.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/surfaces/preference.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/surfaces/registered.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/surfaces/scripts.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/surfaces/theme-choice.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/surfaces/themes.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/surfaces/translations.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/sync/batch.ts` | `chord/sync/batch.go (D432)` | ✅ |
| `packages/chord/src/sync/client.ts` | `chord/sync/client.go, chord/sync/scheduler.go` | ✅ |
| `packages/chord/src/sync/files.ts` | `chord/sync/files.go (errors, not FileResult; not-found is fs.ErrNotExist)` | ✅ |
| `packages/chord/src/sync/filesystem.ts` | `chord/sync/filesystem.go` | ✅ |
| `packages/chord/src/sync/index.ts` | `(barrel: Go imports the package)` | n/a |
| `packages/chord/src/sync/lines.ts` | `chord/sync/lines.go` | ✅ |
| `packages/chord/src/sync/remote.ts` | `chord/sync/remote.go` | ✅ |
| `packages/chord/src/sync/state.ts` | `chord/sync/state.go` | ✅ |
| `packages/chord/src/telemetry-ingest/batch.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/telemetry-ingest/facet.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/telemetry-ingest/index.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/telemetry-ingest/recorder.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/telemetry-ingest/sender.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/telemetry-sqlite/node.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/telemetry-sqlite/schema.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/telemetry.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/tui/bridge.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/tui/channel.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/tui/frames.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/tui/index.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/tui/runtime.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/tui/scope.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/tui/terminal.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/tui/theme.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/web/environment.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/web/index.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/web/language.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/web/page.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/web/scripts.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/web/styles.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/chord/src/web/themes.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |

## `packages/protocol/src/`

| upstream | pig | status |
|---|---|---|

## `packages/client/src/`

| upstream | pig | status |
|---|---|---|

## `packages/server/src/`

| upstream | pig | status |
|---|---|---|

## `packages/durable/src/`

| Upstream | PiG | Status |
|---|---|---|
| `packages/durable/src/documents.ts` | `durable/documents.go` | ✅ |
| `packages/durable/src/entries.ts` | `durable/entries.go` | ✅ |
| `packages/durable/src/env/decode.ts` | `durable/env/decode.go` | ✅ |
| `packages/durable/src/env/index.ts` | `durable/env/env.go` (`FileSystem.ListFiles` is a member of the FileSystem and ExecutionEnv interfaces as in Pi, and `NodeExecutionEnv.ListFiles` answers not_supported as Pi's Node environment leaves `listFiles` undefined) | ✅ |
| `packages/durable/src/env/line-scan.ts` | `durable/env/line_scan.go` | ✅ |
| `packages/durable/src/env/node-watch.ts` | `durable/env/node/watch.go` | ✅ |
| `packages/durable/src/env/node.ts` | `durable/env/node/binary_reader.go + durable/env/node/exec.go + durable/env/node/node.go`, `durable/env/node/watch.go` | ✅ |
| `packages/durable/src/errors.ts` | `durable/errors.go` | ✅ |
| `packages/durable/src/harness/agent.ts` | `durable/harness/agent.go`, `durable/harness_types.go` | ✅ |
| `packages/durable/src/harness/compaction.ts` | `durable/harness/compaction.go` | ✅ |
| `packages/durable/src/harness/context.ts` | `durable/harness/context.go` | ✅ |
| `packages/durable/src/harness/define.ts` | `durable/harness/define.go` | ✅ |
| `packages/durable/src/harness/generation.ts` | `durable/harness/generation.go` | ✅ |
| `packages/durable/src/harness/harness.ts` | `durable/harness/harness.go`, `durable/harness/types.go` | ✅ |
| `packages/durable/src/harness/inbox.ts` | `durable/harness/inbox.go` | ✅ |
| `packages/durable/src/harness/json.ts` | `durable/harness/json.go` | ✅ |
| `packages/durable/src/harness/partial-changes.ts` | `durable/harness/partial_changes.go (PartialChanges; durable/harness/partial_changes_upstream_test.go)` | ✅ |
| `packages/durable/src/harness/live.ts` | `durable/harness/live.go` | ✅ |
| `packages/durable/src/harness/prompt.ts` | `durable/harness/prompt.go` | ✅ |
| `packages/durable/src/harness/provider.ts` | `durable/harness/provider.go` | ✅ |
| `packages/durable/src/harness/registry-reader.ts` | `durable/harness/registry_reader.go (durable/harness/registry_reader_test.go, durable/harness/registry_reader_upstream_test.go)` | 🟡 |
| `packages/durable/src/harness/registry.ts` | `durable/harness/registry.go` | ✅ |
| `packages/durable/src/harness/scheduler.ts` | `durable/harness/scheduler.go`, `durable/types.go` | ✅ |
| `packages/durable/src/harness/submissions.ts` | `durable/harness/submissions.go` | ✅ |
| `packages/durable/src/harness/task-graph.ts` | `durable/harness/task_graph.go` | ✅ |
| `packages/durable/src/harness/tool.ts` | `durable/harness/tool.go`, `durable/harness/tool_nested.go` | ✅ |
| `packages/durable/src/harness/transcript.ts` | `durable/harness/transcript.go` | ✅ |
| `packages/durable/src/harness/types.ts` | `durable/harness/types.go`, `durable/harness_types.go`, `durable/harness/agent.go`, `durable/tasks.go`, `durable/harness/registry.go` | ✅ |
| `packages/durable/src/harness/usage.ts` | `durable/harness/usage.go` | ✅ |
| `packages/durable/src/harness/util.ts` | `durable/harness/util.go` | ✅ |
| `packages/durable/src/harness/view.ts` | `durable/harness/view.go` | ✅ |
| `packages/durable/src/ids.ts` | `durable/ids.go` | ✅ |
| `packages/durable/src/images/cloudflare.ts` | `durable/images/cloudflare/cloudflare.go` | ✅ |
| `packages/durable/src/images/exif.ts` | `durable/images/exif.go` | ✅ |
| `packages/durable/src/images/index.ts` | `durable/images/images.go` | ✅ |
| `packages/durable/src/images/node.ts` | `durable/images/node/node.go` | ✅ |
| `packages/durable/src/images/photon-wasm.d.ts` | `(TypeScript declaration of the WebAssembly import; Go has no compiled module to import)` | n/a |
| `packages/durable/src/index.ts` | `(re-export barrel: the exports are the Go package surface)` | n/a |
| `packages/durable/src/session/cached-storage.ts` | `durable/session/cached_storage.go` | 🟡 |
| `packages/durable/src/session/conversation-tails.ts` | `durable/session/conversation_tails.go` | 🟡 |
| `packages/durable/src/session/forks.ts` | `durable/session/forks.go` | ✅ |
| `packages/durable/src/session/keys.ts` | `durable/session/keys.go` | 🟡 |
| `packages/durable/src/session/session.ts` | `durable/session/session.go`; the Durable core's native Go host of one Session, which the SQLite engine drives through the core ABI: `durable/core/sqlhost/host.go` (declares `// Ports packages/durable/src/session/session.ts`; `Host.Close` is `Session.close`) | ✅ |
| `packages/durable/src/session/transaction.ts` | `durable/session/transaction.go` | ✅ |
| `packages/durable/src/session/writer.ts` | `durable/session/writer.go` | 🟡 |
| `packages/durable/src/storage/indexeddb.ts` | `(browser-only Storage backed by window IndexedDB: the file opens with /// <reference lib="dom" /> and every method drives IDBDatabase/IDBTransaction, which exist only in a browser; PiG is a Go process with no browser or WebAssembly build, so no caller can reach it. The Storage contract it implements is the one durable/storage/memory.go, jsonl, sqlite and the Cloudflare SQLite driver already satisfy; Pi 16f5f0af4d)` | n/a |
| `packages/durable/src/storage/jsonl/index.ts` | `(re-export barrel: the exports are the Go package surface)` | n/a |
| `packages/durable/src/storage/jsonl/node.ts` | `durable/storage/jsonl/node/node.go` | ✅ |
| `packages/durable/src/storage/jsonl/storage.ts` | `durable/storage/jsonl/storage.go` | ✅ |
| `packages/durable/src/storage/memory.ts` | `durable/storage/memory.go` | ✅ |
| `packages/durable/src/storage/scan.ts` | `durable/storage/internal/scan/scan.go` | ✅ |
| `packages/durable/src/storage/sqlite/database.ts` | `(re-export barrel: re-exports chord/sqlite's facade types; durable/storage/sqlite/database.go aliases them)` | n/a |
| `packages/durable/src/storage/sqlite/index.ts` | `(re-export barrel: the exports are the Go package surface)` | n/a |
| `packages/durable/src/storage/sqlite/migrations.ts` | `durable/storage/sqlite/migrations.go` | ✅ |
| `packages/durable/src/storage/sqlite/schema.ts` | `durable/storage/sqlite/schema.go` (tests in durable/storage/sqlite/schema_test.go) | ✅ |
| `packages/durable/src/storage/sqlite/node.ts` | `durable/storage/sqlite/node/node.go` (re-exports chord/sqlite/node; OpenNodeSqliteStorage) | ✅ |
| `packages/durable/src/storage/sqlite/storage.ts` | `durable/storage/sqlite/storage.go` | ✅ |
| `packages/durable/src/storage/sqlite/cloudflare.ts` | `durable/storage/sqlite/cloudflare/cloudflare.go` (re-exports chord/sqlite/cloudflare; OpenDurableObjectSqliteStorage) | ✅ |
| `packages/durable/src/storage/store/commit.ts` | `durable/storage/store/commit.go` | 🟡 |
| `packages/durable/src/storage/store/documents.ts` | `durable/storage/store/documents.go` | 🟡 |
| `packages/durable/src/storage/store/index.ts` | `(re-export barrel: the exports are the Go package surface)` | n/a |
| `packages/durable/src/storage/store/queries.ts` | `durable/storage/store/queries.go` | 🟡 |
| `packages/durable/src/storage/store/storage.ts` | `durable/storage/store/storage.go` | 🟡 |
| `packages/durable/src/storage/store/tables.ts` | `durable/storage/store/tables.go` | 🟡 |
| `packages/durable/src/tasks.ts` | `durable/tasks.go` | ✅ |
| `packages/durable/src/testing/assertions.ts` | `durable/durabletest/durabletest.go` | ✅ |
| `packages/durable/src/testing/commit-writes.ts` | `durable/durabletest/commit_writes.go` | 🟡 |
| `packages/durable/src/testing/env-conformance.ts` | `durable/durabletest/env_conformance.go` | ✅ |
| `packages/durable/src/testing/index.ts` | `(re-export barrel: the exports are the Go package surface)` | n/a |
| `packages/durable/src/testing/runner.ts` | `durable/durabletest/durabletest.go + durable/durabletest/env_conformance.go` | ✅ |
| `packages/durable/src/testing/storage-benchmark.ts` | `durable/durabletest/storage_benchmark.go` | ✅ |
| `packages/durable/src/testing/storage-conformance.ts` | `durable/durabletest/storage_conformance.go` | ✅ |
| `packages/durable/src/testing/types.ts` | `durable/durabletest/durabletest.go + durable/durabletest/env_conformance.go` | ✅ |
| `packages/durable/src/timers.ts` | `durable/timers.go` | ✅ |
| `packages/durable/src/tools/bash.ts` | `durable/tools/bash.go` | ✅ |
| `packages/durable/src/tools/edit-diff.ts` | `durable/tools/diff_string.go + durable/tools/edit_diff.go + durable/tools/unified_patch.go` | ✅ |
| `packages/durable/src/tools/edit.ts` | `durable/tools/edit.go` | ✅ |
| `packages/durable/src/tools/env.ts` | `durable/tools/env.go` | ✅ |
| `packages/durable/src/tools/file-mutation-queue.ts` | `durable/tools/file_mutation_queue.go` | ✅ |
| `packages/durable/src/tools/image-processor.ts` | `durable/tools/image_processor.go` | ✅ |
| `packages/durable/src/tools/image.ts` | `durable/tools/image.go` | ✅ |
| `packages/durable/src/tools/index.ts` | `durable/tools/index.go` | ✅ |
| `packages/durable/src/tools/path-utils.ts` | `durable/tools/path_utils.go` | ✅ |
| `packages/durable/src/tools/read.ts` | `durable/tools/read.go` | ✅ |
| `packages/durable/src/tools/write.ts` | `durable/tools/write.go` | ✅ |
| `packages/durable/src/truncate.ts` | `durable/truncate.go` | ✅ |
| `packages/durable/src/types.ts` | `durable/types.go`, `durable/documents.go`, `durable/entries.go`, `durable/tasks.go` | ✅ |
| `packages/durable/src/codemode/codemode.ts` | `durable/codemode/tool.go (createCodemodeTool, createCodemodeExtension); durable/codemode/docs.go (the documents and stored shapes); durable/codemode/call.go (CodemodeCall)` | ✅ |
| `packages/durable/src/codemode/describe.ts` | `durable/codemode/describe.go` | ✅ |
| `packages/durable/src/codemode/index.ts` | `(barrel re-exports; Go has no barrel exports)` | n/a |
| `packages/durable/src/codemode/models.ts` | `durable/codemode/models.go` | ✅ |
| `packages/durable/src/codemode/output.ts` | `durable/codemode/output.go` | ✅ |
| `packages/durable/src/images/node-worker.ts` | `durable/images/node/node.go` (the worker goroutine; tests in durable/images/node/worker_test.go) | ✅ |
| `packages/durable/src/search.ts` | `durable/internal/search/search.go` (tests in durable/internal/search/search_test.go) | ✅ |
| `packages/durable/src/tool-search/index.ts` | `(barrel re-exports; Go has no barrel exports)` | n/a |
| `packages/durable/src/tool-search/tool-search.ts` | `durable/toolsearch/` | ✅ |
| `packages/durable/src/harness/telemetry.ts` | `durable/harness/telemetry.go`; the Durable core's use of the vocabulary: `durable/core/telemetry.go` (notices), `durable/core/session/telemetry.go` (phase spans, metrics), `durable/core/host/cf/src/facade/telemetry.ts` (spans and metrics in the TypeScript host) | ✅ |
| `packages/durable/src/storage/sqlite/commit.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/durable/src/storage/sqlite/rows.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |
| `packages/durable/src/storage/sqlite/statements.ts` | `(not started: Pi main c23b9ec1, no Go counterpart yet)` | ⬜ |

## `packages/env/src/`

| Upstream | PiG | Status |
|---|---|---|
| `packages/env/src/connection.ts` | `env/connection.go` | ✅ |
| `packages/env/src/errors.ts` | `env/errors.go` | ✅ |
| `packages/env/src/index.ts` | `(re-export barrel: the exports are the Go package surface)` | n/a |
| `packages/env/src/remote-env.ts` | `env/remote_env.go + env/paths.go` | ✅ |
| `packages/env/src/ssh.ts` | `env/ssh.go` | ✅ |
| `packages/env/src/terminal.ts` | `env/terminal.go` | ✅ |
| `packages/env/src/process.ts` | `env/process.go` (the tests of packages/env/test/process.test.ts are in env/process_test.go and run against Pi's own daemon as oracle once the pin has the `process` operation) | ✅ |
| `packages/env/src/watch.ts` | `env/watch.go` | ✅ |
| `packages/env/src/list-files.ts` | `env/list_files.go` | ✅ |

## `packages/env/daemon/`

The daemon is a Go program with Pi's command line and wire protocol (D96); the Rust files map to the Go package `env/daemon`. A 🟡 row is exercised through the `env` package's tests, some of which also run against Pi's own daemon, but has no marker test with a mutant yet.

| Upstream | PiG | Status |
|---|---|---|
| `packages/env/daemon/build.rs` | `(Cargo build script: the daemon's version is env/cmd/pi-env's pigversion.UpstreamPackageVersion)` | n/a |
| `packages/env/daemon/src/decode.rs` | `durable/env/decode.go` (StreamDecoder; tests in env/daemon/decode_test.go) | ✅ |
| `packages/env/daemon/src/errors.rs` | `env/daemon/errors.go` (tests in env/daemon/errors_test.go) | ✅ |
| `packages/env/daemon/src/exec.rs` | `env/daemon/exec.go`, `env/daemon/control.go` (tests in env/daemon/exec_test.go) | ✅ |
| `packages/env/daemon/src/frame.rs` | `env/daemon/frame.go` | ✅ |
| `packages/env/daemon/src/fs.rs` | `env/daemon/fs.go` (tests in env/daemon/fs_test.go) | ✅ |
| `packages/env/daemon/src/main.rs` | `env/daemon/server.go`, `env/cmd/pi-env/main.go` (tests in env/daemon/server_test.go and env/cmd/pi-env/main_test.go) | ✅ |
| `packages/env/daemon/src/output.rs` | `env/daemon/output.go` (tests in env/daemon/output_test.go) | ✅ |
| `packages/env/daemon/src/pty.rs` | `env/daemon/pty.go + env/daemon/pty_unix.go + env/daemon/pty_linux.go (Linux, Android and macOS terminals through /dev/ptmx and x/sys/unix, no cgo, macOS in pty_darwin; other systems answer ENOTSUP from pty_other; the client tests in env/terminal_test.go and env/terminal_linux_test.go run against Pi's own daemon as oracle)` | ✅ |
| `packages/env/daemon/src/process.rs` | `env/daemon/process.go + env/daemon/process_start.go + env/daemon/process_group.go + env/daemon/process_group_unix.go + env/daemon/process_group_windows.go + env/daemon/process_wait_linux.go + env/daemon/process_wait_darwin.go + env/daemon/process_unix.go + env/daemon/process_windows.go` (stdin queue and target lookup shared with pty in env/daemon/pty.go; tests in env/process_test.go and env/daemon/process_unix_test.go) | ✅ |
| `packages/env/daemon/src/scan.rs` | `durable/env/line_scan.go` (LineScanner; tests in env/daemon/scan_test.go) | ✅ |
| `packages/env/daemon/src/sys/mod.rs` | `(Rust module barrel: Go selects sys_unix.go or sys_windows.go by build constraint)` | n/a |
| `packages/env/daemon/src/sys/unix.rs` | `env/daemon/sys_unix.go` | ✅ |
| `packages/env/daemon/src/sys/windows.rs` | `env/daemon/sys_windows.go (commands run through durable/env/node, whose spawn path builds the Windows command line with nodespawn.SetCommandLine, libuv's quote_cmd_arg, so quote_argument has no Go function; its test is ported as TestExecDeliversPiQuoteArgumentVectorsThroughTheSpawnPath in env/daemon/sys_windows_test.go, which sends each vector through the daemon's exec to a helper child and compares the command line and the received argv; Windows only, proven green and mutation-killed on windows-latest, but the ledger's coverage run is Linux and executes no statement of sys_windows.go, so the row stays partial on this host)` | 🟡 |
| `packages/env/daemon/src/watch.rs` | `env/daemon/watch.go` | ✅ |
| `packages/env/daemon/src/window.rs` | `env/daemon/window.go (tests in env/daemon/window_test.go)` | ✅ |

## `packages/evals/src/`

| Upstream | PiG | Status |
|---|---|---|
| `packages/evals/src/cli.ts` | `internal/evals/cli.go + cmd/pig-evals/main.go` | ✅ |
| `packages/evals/src/docker.ts` | `internal/evals/docker.go` | ✅ |
| `packages/evals/src/harness.ts` | `internal/evals/harness.go + internal/evals/harness_run.go + internal/evals/agent_process.go + internal/evals/bridge + cmd/pig-eval-extension` | ✅ |
| `packages/evals/src/plan.ts` | `internal/evals/plan.go` | ✅ |
| `packages/evals/src/report.ts` | `internal/evals/report.go` | ✅ |

## `packages/evals/evals/`

| Upstream | PiG | Status |
|---|---|---|
| `packages/evals/evals/documentation-audit.eval.ts` | `internal/evals/evalsuites/audit.go` | ✅ |

## `packages/schema/src/`

| Upstream | PiG | Status |
|---|---|---|
| `packages/schema/src/annotations.ts` | `schema/annotations.go` | ✅ |
| `packages/schema/src/builders.ts` | `schema/builders.go (Recursive; unsafe and the bigint builder are designed out: unsafe only types a schema, and Go has no bigint value; Pi main af79b0c3d)` | ✅ |
| `packages/schema/src/checks.ts` | `schema/checks.go (a number is finite, D303; Equal is deepEqual; the leaf codes of Pi main 20c637b1a that object and array loops run inline are a JavaScript closure-call optimization with no observable effect, designed out: Go's closures keep their one shape and TestPiMainLeafChecksAgreeWithTheStandaloneCheck ports the test that pins the results; bigint and undefined checks have no Go value)` | ✅ |
| `packages/schema/src/coerce.ts` | `schema/coerce.go (CompileCoercing returns the distinct type CoercingValidator where Pi has compile(schema, {coerce:true}); the base of an unindexed node goes through baseFor, D302; a number is finite, D303; the bigint and undefined conversions have no Go value)` | ✅ |
| `packages/schema/src/compiler.ts` | `schema/compiler.go` | ✅ |
| `packages/schema/src/explain.ts` | `schema/explain.go` | ✅ |
| `packages/schema/src/formats/index.ts` | `schema/formats/index.go` | ✅ |
| `packages/schema/src/formats/typebox/date.ts` | `schema/formats/typebox_date.go` | ✅ |
| `packages/schema/src/formats/typebox/date_time.ts` | `schema/formats/typebox_date_time.go` | ✅ |
| `packages/schema/src/formats/typebox/duration.ts` | `schema/formats/typebox_duration.go` | ✅ |
| `packages/schema/src/formats/typebox/email.ts` | `schema/formats/typebox_email.go` | ✅ |
| `packages/schema/src/formats/typebox/hostname.ts` | `schema/formats/typebox_hostname.go` | ✅ |
| `packages/schema/src/formats/typebox/idn_email.ts` | `schema/formats/typebox_idn_email.go` | ✅ |
| `packages/schema/src/formats/typebox/idn_hostname.ts` | `schema/formats/typebox_idn_hostname.go` | ✅ |
| `packages/schema/src/formats/typebox/idna/format/bidi.ts` | `schema/formats/internal/idna/format_bidi.go` | ✅ |
| `packages/schema/src/formats/typebox/idna/format/puny.ts` | `schema/formats/internal/idna/format_puny.go` | ✅ |
| `packages/schema/src/formats/typebox/idna/hostname.ts` | `schema/formats/internal/idna/hostname.go` | ✅ |
| `packages/schema/src/formats/typebox/idna/idn-hostname.ts` | `schema/formats/internal/idna/idn_hostname.go` | ✅ |
| `packages/schema/src/formats/typebox/idna/index.ts` | `(re-export of idna/hostname.ts and idna/idn-hostname.ts; Go package idna exports them directly)` | n/a |
| `packages/schema/src/formats/typebox/idna/label/ascii.ts` | `schema/formats/internal/idna/label_ascii.go` | ✅ |
| `packages/schema/src/formats/typebox/idna/label/puny.ts` | `schema/formats/internal/idna/label_puny.go` | ✅ |
| `packages/schema/src/formats/typebox/idna/label/unicode.ts` | `schema/formats/internal/idna/label_unicode.go` | ✅ |
| `packages/schema/src/formats/typebox/idna/pattern/pattern.ts` | `schema/formats/internal/idna/pattern.go` | ✅ |
| `packages/schema/src/formats/typebox/ipv4.ts` | `schema/formats/typebox_ipv4.go` | ✅ |
| `packages/schema/src/formats/typebox/ipv6.ts` | `schema/formats/typebox_ipv6.go` | ✅ |
| `packages/schema/src/formats/typebox/iri.ts` | `schema/formats/typebox_iri.go` | ✅ |
| `packages/schema/src/formats/typebox/iri_reference.ts` | `schema/formats/typebox_iri_reference.go` | ✅ |
| `packages/schema/src/formats/typebox/json_pointer.ts` | `schema/formats/typebox_json_pointer.go` | ✅ |
| `packages/schema/src/formats/typebox/json_pointer_uri_fragment.ts` | `schema/formats/typebox_json_pointer_uri_fragment.go` | ✅ |
| `packages/schema/src/formats/typebox/regex.ts` | `schema/formats/typebox_regex.go` | ✅ |
| `packages/schema/src/formats/typebox/relative_json_pointer.ts` | `schema/formats/typebox_relative_json_pointer.go` | ✅ |
| `packages/schema/src/formats/typebox/time.ts` | `schema/formats/typebox_time.go` | ✅ |
| `packages/schema/src/formats/typebox/uri.ts` | `schema/formats/typebox_uri.go` | ✅ |
| `packages/schema/src/formats/typebox/uri_reference.ts` | `schema/formats/typebox_uri_reference.go` | ✅ |
| `packages/schema/src/formats/typebox/uri_template.ts` | `schema/formats/typebox_uri_template.go` | ✅ |
| `packages/schema/src/formats/typebox/url.ts` | `schema/formats/typebox_url.go` | ✅ |
| `packages/schema/src/formats/typebox/uuid.ts` | `schema/formats/typebox_uuid.go` | ✅ |
| `packages/schema/src/index.ts` | `(re-export barrel: the Go exports are the package-level identifiers of schema)` | n/a |
| `packages/schema/src/resolver.ts` | `schema/resolver.go` | ✅ |
| `packages/schema/src/shared.ts` | `schema/shared.go, schema/internal/ecmaregex` | ✅ |
| `packages/schema/src/typebox-static.ts` | `(type-level only: TypeBoxStatic has no value; Go has no type-level programming)` | n/a |
| `packages/schema/src/types.ts` | `schema/types.go` | ✅ |
| `packages/schema/src/validator.ts` | `schema/validator.go` | ✅ |

## `packages/telemetry/src/`

| Upstream | PiG | Status |
|---|---|---|
| `packages/telemetry/src/index.ts` | `telemetry/context.go + telemetry/schema.go + telemetry/store.go + chord/telemetrysqlite/index.go (SqliteTelemetry, the TelemetryStore the package implements)` | ✅ |
| `packages/telemetry/src/memory.ts` | `telemetry/memory.go` | ✅ |
| `packages/telemetry/src/metrics.ts` | `telemetry/metrics.go` | ✅ |
| `packages/telemetry/src/noop.ts` | `telemetry/context.go` | ✅ |
| `packages/telemetry/src/testing/conformance.ts` | `telemetry/telemetrytest/conformance.go` | ✅ |
| `packages/telemetry/src/testing/index.ts` | `(re-export barrel: the exports are the Go package surface)` | n/a |
| `packages/telemetry/src/testing/types.ts` | `telemetry/telemetrytest/types.go` | ✅ |
| `packages/telemetry/src/render.ts` | `telemetry/render.go` | ✅ |
