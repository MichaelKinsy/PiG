# gate-992-ai progress (phase 1: port only)

Base: staging porter/pi-0.99.1. Upstream pin 0.99.2; the `.upstream/v0.99.2` mirror is not materialized, so citations use `.upstream/v0.99.1` (the test files below are byte-identical between 0.99.1 and 0.99.2 by hash in `upstream-tests-v0.99.1.json` vs `-v0.99.2.json`).

## Split

`make test-porting-release` lists 64 pending `packages/ai/test/*` files. Six are owned by port-992-core (new or changed in 0.99.2: anthropic-federation-sdk, anthropic-federation, anthropic-strict-tool-schema, models-entry, anthropic-eager-tool-input-compat, overflow). The remaining 58 carried files, sorted by path, are split: gate-992-ai takes the first 29 (abort … models-runtime); gate-992-ai2 takes the second 29 (oauth-auth … unicode-surrogate). This lane does not touch the second half.

First half (this lane):

- abort.test.ts
- anthropic-adaptive-thinking-models.test.ts
- anthropic-cache-write-1h-cost.test.ts
- anthropic-empty-thinking-signature-compat.test.ts
- anthropic-oauth.test.ts
- anthropic-sse-parsing.test.ts
- azure-openai-base-url.test.ts
- bedrock-raw-stop-reason.test.ts
- cache-retention.test.ts
- classifier-models.test.ts
- cloudflare-workers-ai-system-one.test.ts
- context-overflow.test.ts
- cross-provider-handoff.test.ts
- empty.test.ts
- fetch-option.test.ts
- fireworks-model-generation.test.ts
- fireworks-models.test.ts
- google-raw-stop-reason.test.ts
- image-model-data.test.ts
- image-tool-result.test.ts
- images-models.test.ts
- images.test.ts
- llama-cpp-classify.test.ts
- max-thinking.test.ts
- mistral-http-transport.test.ts
- mistral-reasoning-mode.test.ts
- model-data-validation.test.ts
- model-types.test.ts
- models-runtime.test.ts

Second half (gate-992-ai2, not touched):

- oauth-auth.test.ts
- oauth-callback-server.test.ts
- openai-chatgpt-oauth.test.ts
- openai-codex-oauth.test.ts
- openai-codex-stream.test.ts
- openai-completions-prompt-cache.test.ts
- openai-completions-provider-stream-event.test.ts
- openai-completions-tool-choice.test.ts
- openai-responses-chatgpt-sign-in.test.ts
- openai-responses-compat.test.ts
- openai-responses-terminal-event.test.ts
- openai-responses-usage-limit.test.ts
- openrouter-images.test.ts
- openrouter-oauth.test.ts
- pi-messages.test.ts
- provider-error-body-passthrough.test.ts
- providers.test.ts
- radius-oauth.test.ts
- retry.test.ts
- sampling-options.test.ts
- stream.test.ts
- supports-xhigh.test.ts
- telemetry-options.test.ts
- together-models.test.ts
- tokens.test.ts
- tool-call-without-result.test.ts
- total-tokens.test.ts
- typesafe-system-one.test.ts
- unicode-surrogate.test.ts

## Method

Every file in the first half already had Go tests from earlier 0.99.1 port lanes (comments cite `.upstream/v0.99.1/...:line`), but the 0.99.2 mapping reset them to `pending` because the upstream hash changed since 0.87.1. For each file I diffed the 0.87.1 and 0.99.1 upstream test, checked the Go test carries every changed or added case with the same inputs and expectations, ran the Go tests (they pass), and recorded per-case evidence in `test/parity/interfaces/test-mapping-v0.99.2.json`. Titles were cross-checked mechanically (upstream case title vs `go test -v` subtest names) and the remaining cases by hand.

No production code was changed.

## Test changes in this phase

- `ai/empty_upstream_test.go`: `upstreamCaseSites` read `upstream-tests-v0.87.1.json`, so the abort, empty, context-overflow and image-tool-result matrices asserted the old Kimi-K2.6 titles. It now reads the inventory for `coding.UpstreamVersion`.
- `ai/models_typed_upstream_test.go`: ported three missing cases of `images-models.test.ts` and `model-types.test.ts`: "keeps existing built-in and compat model reads chat-only" (:373), "builtinModels exposes OpenRouter image models under the openrouter provider" (:390; both were deferred until the hydrated catalog existed) and "return model shapes that can be reassigned within one api" (model-types:92). All pass.
- Citation refresh `v0.87.1` to `v0.99.1` for the files whose line numbers are unchanged between the two versions (abort, anthropic-adaptive-thinking-models, context-overflow, cross-provider-handoff, empty, image-tool-result, max-thinking).

## Per-file result

Legend: credited = existing Go test covers all cases (evidence in mapping); ported-passing = case ported in this phase, passes; partial = case cannot be ported faithfully, needs a lead decision; ported-FAILING = none.

| upstream file (packages/ai/test/) | cases | result |
|---|---:|---|
| abort | 41 | credited |
| anthropic-adaptive-thinking-models | 1 | credited |
| anthropic-cache-write-1h-cost | 3 | credited |
| anthropic-empty-thinking-signature-compat | 7 | credited |
| anthropic-oauth | 4 | credited |
| anthropic-sse-parsing | 14 | credited |
| azure-openai-base-url | 17 | credited |
| bedrock-raw-stop-reason | 4 | credited 3, **partial** 1 (see below) |
| cache-retention | 19 | credited |
| classifier-models | 5 | credited (the chat-model-at-classifier-entry case through the extension classify action) |
| cloudflare-workers-ai-system-one | 4 | credited |
| context-overflow | 35 | credited |
| cross-provider-handoff | 2 | credited |
| empty | 120 | credited |
| fetch-option | 6 | credited |
| fireworks-model-generation | 4 | credited |
| fireworks-models | 19 | credited |
| google-raw-stop-reason | 7 | credited |
| image-model-data | 4 | credited |
| image-tool-result | 46 | credited |
| images-models | 13 | credited 10, ported-passing 2, **partial** 1 (see below) |
| images | 2 | credited |
| llama-cpp-classify | 15 | credited |
| max-thinking | 4 | credited |
| mistral-http-transport | 10 | credited |
| mistral-reasoning-mode | 11 | credited |
| model-data-validation | 15 | credited |
| model-types | 4 | credited 3, ported-passing 1 |
| models-runtime | 40 | credited |

## Open items needing a lead decision (both hot-path, both `partial` in the mapping)

1. `bedrock-raw-stop-reason.test.ts:91` "forwards SDK error items before reporting them". Pi's Bedrock loop forwards every SDK stream item to `onProviderStreamEvent` (`bedrock-converse-stream.ts:297`) and then throws the modeled exception item (`:318-327`), so the hook observes `[messageStart, exception]` and the result message is the exception's own message. The Go AWS SDK surfaces exception frames as a stream error rather than a union item, so the hook observes only `[messageStart]` and the message is `Internal server error: bedrock stream failed` (`TestBedrockProviderStreamEventsUpstream/forwards_SDK_stream_items_before_an_exception_frame_ends_the_request`, which asserts PiG's behavior and cites the difference). No approved divergence covers it. Options: fix (synthesize an item for the exception frame), or approve a numbered divergence.
2. `images-models.test.ts:311` "rejects chat models at the image entry point at runtime". The test casts a chat model to `ImageModel` and expects an error result containing "is not an image model". `ai.Models.GenerateImages` and `coding.ModelRuntime.GenerateImages` take `*ImageModel`, so no Go entry point can receive a chat model. The analogous classifier case is reachable through the extension classify action (`ai.AssertClassifierModel`) and is credited. Options: add an `AssertImageModel` on the extension image action path (if one exists), or approve a design-out.

## Environment notes (not failures of this scope)

`go test ./ai` in this worktree fails the Pi-oracle comparisons (`TestOpenAIHTTPErrorMatchesPi`, `TestBuiltinProviderLoginMatchesPi`, `TestProviderEmptyKeyFallbackMatchesPi`, `TestAbortGoldenMatchesPi` and similar) because `extensions/sdk-ts/node_modules` is not installed here (`ERR_MODULE_NOT_FOUND` for `openai/core/error.mjs`), and `cmd/gen-models` `TestPortWave12GenerateModelsStrict` fails with `stat .: no such file or directory`. None of these is in the first half. `TestStreamUpstream` (stream.test.ts) belongs to gate-992-ai2.

## Stubs for other families

None.
