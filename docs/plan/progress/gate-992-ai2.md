# gate-992-ai2 progress

Slice: close the test-porting release gate for `packages/ai` (Pi 0.99.2), second half by path.

Pending `packages/ai/test/*` files in `test/parity/interfaces/test-mapping-v0.99.2.json` at base `f0a01af13`: 64 (`make test-porting-release` lists 63 as hot-path; `fireworks-model-generation.test.ts` is not tagged hot-path). Sorted by path; lane gate-992-ai owns indices 0-31, this lane owns 32-63.

## First half (gate-992-ai, not this lane)

0. `packages/ai/test/abort.test.ts`
1. `packages/ai/test/anthropic-adaptive-thinking-models.test.ts`
2. `packages/ai/test/anthropic-cache-write-1h-cost.test.ts`
3. `packages/ai/test/anthropic-eager-tool-input-compat.test.ts`
4. `packages/ai/test/anthropic-empty-thinking-signature-compat.test.ts`
5. `packages/ai/test/anthropic-federation-sdk.test.ts`
6. `packages/ai/test/anthropic-federation.test.ts`
7. `packages/ai/test/anthropic-oauth.test.ts`
8. `packages/ai/test/anthropic-sse-parsing.test.ts`
9. `packages/ai/test/anthropic-strict-tool-schema.test.ts`
10. `packages/ai/test/azure-openai-base-url.test.ts`
11. `packages/ai/test/bedrock-raw-stop-reason.test.ts`
12. `packages/ai/test/cache-retention.test.ts`
13. `packages/ai/test/classifier-models.test.ts`
14. `packages/ai/test/cloudflare-workers-ai-system-one.test.ts`
15. `packages/ai/test/context-overflow.test.ts`
16. `packages/ai/test/cross-provider-handoff.test.ts`
17. `packages/ai/test/empty.test.ts`
18. `packages/ai/test/fetch-option.test.ts`
19. `packages/ai/test/fireworks-model-generation.test.ts`
20. `packages/ai/test/fireworks-models.test.ts`
21. `packages/ai/test/google-raw-stop-reason.test.ts`
22. `packages/ai/test/image-model-data.test.ts`
23. `packages/ai/test/image-tool-result.test.ts`
24. `packages/ai/test/images-models.test.ts`
25. `packages/ai/test/images.test.ts`
26. `packages/ai/test/llama-cpp-classify.test.ts`
27. `packages/ai/test/max-thinking.test.ts`
28. `packages/ai/test/mistral-http-transport.test.ts`
29. `packages/ai/test/mistral-reasoning-mode.test.ts`
30. `packages/ai/test/model-data-validation.test.ts`
31. `packages/ai/test/model-types.test.ts`

## Second half (this lane)

32. `packages/ai/test/models-entry.test.ts`
33. `packages/ai/test/models-runtime.test.ts`
34. `packages/ai/test/oauth-auth.test.ts`
35. `packages/ai/test/oauth-callback-server.test.ts`
36. `packages/ai/test/openai-chatgpt-oauth.test.ts`
37. `packages/ai/test/openai-codex-oauth.test.ts`
38. `packages/ai/test/openai-codex-stream.test.ts`
39. `packages/ai/test/openai-completions-prompt-cache.test.ts`
40. `packages/ai/test/openai-completions-provider-stream-event.test.ts`
41. `packages/ai/test/openai-completions-tool-choice.test.ts`
42. `packages/ai/test/openai-responses-chatgpt-sign-in.test.ts`
43. `packages/ai/test/openai-responses-compat.test.ts`
44. `packages/ai/test/openai-responses-terminal-event.test.ts`
45. `packages/ai/test/openai-responses-usage-limit.test.ts`
46. `packages/ai/test/openrouter-images.test.ts`
47. `packages/ai/test/openrouter-oauth.test.ts`
48. `packages/ai/test/overflow.test.ts`
49. `packages/ai/test/pi-messages.test.ts`
50. `packages/ai/test/provider-error-body-passthrough.test.ts`
51. `packages/ai/test/providers.test.ts`
52. `packages/ai/test/radius-oauth.test.ts`
53. `packages/ai/test/retry.test.ts`
54. `packages/ai/test/sampling-options.test.ts`
55. `packages/ai/test/stream.test.ts`
56. `packages/ai/test/supports-xhigh.test.ts`
57. `packages/ai/test/telemetry-options.test.ts`
58. `packages/ai/test/together-models.test.ts`
59. `packages/ai/test/tokens.test.ts`
60. `packages/ai/test/tool-call-without-result.test.ts`
61. `packages/ai/test/total-tokens.test.ts`
62. `packages/ai/test/typesafe-system-one.test.ts`
63. `packages/ai/test/unicode-surrogate.test.ts`

## Phase 1 (port only; owner-approved method change)

Mapping edits: `test/parity/interfaces/test-mapping-v0.99.2.json`, 32 entries pending -> ported, each with evidence and a per-case rationale built on the reviewed 0.87.1 entry plus the 0.99.1/0.99.2 delta. Discovery: most of these files were already ported in the 0.99.1 lanes (family 3, f6*) and only lacked a ledger re-review after their hash changed. Ran `go run ./test/parity/cmd/testinventorycheck` (OK) and `make test-porting-release` (no `packages/ai` finding from this half remains).

| # | file | status | note |
|---|---|---|---|
| models-entry.test.ts | ported-passing | new test ai/models_entry_upstream_test.go; module-graph hook inapplicable (static linking) |
| models-runtime.test.ts | credited | 39 cases existing; new 0.99.1 case credited at models_typed_upstream_test.go |
| oauth-auth.test.ts | credited | 0.99.1 changes already ported |
| oauth-callback-server.test.ts | credited | 12/12 existing |
| openai-chatgpt-oauth.test.ts | credited | 6/6 existing |
| openai-codex-oauth.test.ts | credited | 9/9 existing |
| openai-codex-stream.test.ts | credited | 30/30 existing incl. raw provider events |
| openai-completions-prompt-cache.test.ts | credited | glm-5p3 rows existing |
| openai-completions-provider-stream-event.test.ts | credited | existing |
| openai-completions-tool-choice.test.ts | credited | kimi-k3 fixtures existing |
| openai-responses-chatgpt-sign-in.test.ts | credited | existing |
| openai-responses-compat.test.ts | credited | fast-tier rows existing |
| openai-responses-terminal-event.test.ts | credited | 12/12 existing |
| openai-responses-usage-limit.test.ts | credited | existing |
| openrouter-images.test.ts | credited | modalities image existing |
| openrouter-oauth.test.ts | credited | existing |
| overflow.test.ts | ported-FAILING | TestOverflowUpstream/detects_z.ai_CN_endpoint_prompt-exceeds-max-length_errors: IsContextOverflow = false, want true (ai/overflow.go lacks `prompt exceeds max length`, overflow.ts:39; port-992-core has the fix in b69fba184) |
| pi-messages.test.ts | credited | existing |
| provider-error-body-passthrough.test.ts | credited | existing |
| providers.test.ts | ported-passing | new "returns empty results for unknown provider ids" at ai/providers_unknown_upstream_test.go; rest credited |
| radius-oauth.test.ts | credited | existing |
| retry.test.ts | credited | existing |
| sampling-options.test.ts | credited | existing |
| stream.test.ts | credited | 233 rows match inventory; TestStreamUpstream FAILS here: extractor ERR_MODULE_NOT_FOUND (npm Pi 0.99.2 dist / sdk-ts node_modules not installed), environment not code |
| supports-xhigh.test.ts | credited | existing |
| telemetry-options.test.ts | ported-passing | 3 new tests ai/telemetry_options_upstream_test.go (0.87.1 designed-out rationale was stale); signature stub ImagesOptions.TelemetryContext in ff0a99ff7 |
| together-models.test.ts | credited | existing |
| tokens.test.ts | credited | existing |
| tool-call-without-result.test.ts | credited | existing |
| total-tokens.test.ts | credited | existing |
| typesafe-system-one.test.ts | credited | 9/9 existing |
| unicode-surrogate.test.ts | credited | existing |

Production changes (own commit): `ff0a99ff7` adds the signature field `ImagesOptions.TelemetryContext` (ai/images.go); it is the whole implementation, because Models and GenerateImages copy the options by value.

Failing tests in this half (phase-2 input):
- `TestOverflowUpstream/detects_z.ai_CN_endpoint_prompt-exceeds-max-length_errors`: `IsContextOverflow = false, want true`. Root cause: `ai/overflow.go` lacks the `prompt exceeds max length` pattern (overflow.ts:39). Same fix as port-992-core b69fba184.
- `TestStreamUpstream` (stream.test.ts): environment only. The drift check runs `test/parity/testdata/extract-stream-cases.mjs`, which needs `extensions/sdk-ts/node_modules` with typescript and Pi 0.99.2's pi-ai dist (cooldown-blocked). The 233 rows were compared against the inventory by line number and title: identical.
- Credit not taken for `TestProviderEmptyKeyFallbackMatchesPi` (providers.test.ts): it also needs the pi-ai dist.

Pre-existing ai failures at the base, not in this scope: oracle/lock/pin tests (TestAbortGoldenMatchesPi, TestAnthropicMicrotaskTrace, TestAnthropicPublishesMutationsWithoutAPush, TestAuthStorage*/TestFileModelsStore* lock tests, TestAzureResponsesTickOrder, TestCredentialExpiryMatchesPi, TestFauxObservationOracle, TestCopilotTokenRefreshWaitsForAnotherProcessUpstream, TestOpenAIHTTPErrorMatchesPi, TestBuiltinProviderLoginMatchesPi, TestCodegenByteIdentical, TestTestFauxParityOracleToolCallIDs, TestTestFauxObservationOracle, TestOpenCodeModelsSmokeUpstream).

## Review (rev-gate-992-ai2)

- `TestOverflowUpstream/detects_z.ai_CN_endpoint_prompt-exceeds-max-length_errors` passes: the review ported the `prompt exceeds max length` pattern from overflow.ts:39 into `ai/overflow.go`, byte-identical to port-992-core b69fba184.
- `ClassifierOptions` extends `ProviderRequestOptions` too (types.ts:324). The review added `ClassifierOptions.TelemetryContext` and the guard `TestTelemetryOptionsSurviveClassifierDispatch`.
- The remaining `ai` failures in this environment are Node-oracle tests that need the npm Pi 0.99.2 dist or `extensions/sdk-ts/node_modules`. `TestAPIKeyProvidersMatchPinnedProviderDefinitions` and `TestListProvidersMatchesPinnedBarrelKeys` fail only when the untracked `.upstream/current` link points at v0.87.1; both pass with it pointed at v0.99.2.
