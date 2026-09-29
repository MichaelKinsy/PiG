# Live provider test secrets

Live provider tests skip when their required environment variables are unset or empty. The skip message names the missing variables. Setting credentials enables the corresponding tests; authentication failures, network failures, and model assertion failures then fail the test. A skip is not evidence of provider behavior.

Use Pi's provider environment names from [`packages/ai/src/env-api-keys.ts`](../../.upstream/current/packages/ai/src/env-api-keys.ts), mirrored by [`ai/auth_env_keys.go`](../../ai/auth_env_keys.go). Do not use `PIG_LIVE_*_API_KEY`, `PIG_LIVE_VERTEX_API_KEY`, `PIG_LIVE_AZURE_OPENAI_BASE_URL`, or `PIG_LIVE_COPILOT_TOKEN`. Those aliases are not read by the live tests.

Codex has no provider key environment variable in Pi. `PIG_LIVE_CODEX_TOKEN` is a test-only input containing a resolved OAuth access token, not an OpenAI API key. Pi's equivalent tests resolve this token through `packages/ai/test/oauth.ts`. PiG's live tests do not read or refresh the developer's auth files.

## Run

Run from the repository root with Go 1.27.1. Supply only the credentials for the tests you intend to run. Live calls can incur charges.

```bash
# Select live tests as well as the ordinary hermetic tests in these packages.
go test -tags live ./ai ./coding -count=1 -v

# Select one OpenAI live test using an already exported OPENAI_API_KEY.
go test -tags live ./ai -run '^TestOpenAIResponsesCacheAffinityLiveUpstream$' -count=1 -v

# Select the credential-gated CLI, SDK, and subagent integration tests.
go test -tags 'integration live' ./test/integration -run '^(TestLive_|TestExt_Live_)' -count=1 -v

# Opt into requests against every Bedrock catalog model after configuring AWS auth.
BEDROCK_EXTENSIVE_MODEL_TEST=1 go test -tags live ./ai -run '^TestPortWave13BedrockModels$' -count=1 -v
```

The integration package also contains credential-gated tests under the `integration` tag alone. Run the Session directory cases with `-run 'TestSessionDir|TestRPCModeHonorsSessionFlags'`, the subagent parity cases with `-run '^TestParity_Sub'`, or the performance cases with `-run '^TestPerf_'`. Those tests require the same Copilot credential. Subagent tests additionally need an external subagent extension, agent definitions, and tmux. Paired tests need the pinned Pi installation. These are tool and fixture requirements, not additional provider secrets.

For a credential-free CI job, leave every credential in the table below unset and use an empty temporary `HOME`, `PIG_HOME`, `PIG_CODING_AGENT_DIR`, and `PI_CODING_AGENT_DIR`. Do not source a developer's auth environment. Preserve the installed toolchain paths and Go build caches when changing `HOME`.

A negative authentication probe is expected to exit nonzero, not skip:

```bash
OPENAI_API_KEY=invalid-live-test-key go test -tags live ./ai -run '^TestOpenAIResponsesCacheAffinityLiveUpstream$' -count=1 -v
```

## GitHub workflow

The `Live providers` workflow runs nightly at 04:23 UTC on `main`. It also accepts a manual dispatch on a protected branch. The `live-providers` GitHub environment restricts deployment branches and supplies the secrets in the table below. The workflow does not run on pull requests and is not a required check.

Set secret values in **Settings > Environments > live-providers**. Use the exact variable names in the table, including the optional configuration names. Leave unused entries unset. Credential-file and profile entries are paths or profile names, not file contents; a hosted runner has no developer ADC file or AWS profile. Prefer `GOOGLE_CLOUD_API_KEY`, an AWS bearer token, or a complete IAM pair unless the runner has the referenced files and role configuration.

Run the workflow with the GitHub CLI:

```bash
gh workflow run live-providers.yml -f packages=./ai/...
gh workflow run live-providers.yml -f packages=./ai/... -f run='^TestOpenAIResponsesCacheAffinityLiveUpstream$'
```

`packages` defaults to `./ai ./coding`. It accepts space-separated local Go package patterns beginning with `./`. `run` is an optional Go `-run` regular expression. The workflow executes the following command with those arguments and an isolated home directory:

```bash
go test -json -count=1 -tags 'integration live' -run "$RUN_FILTER" "${packages[@]}"
```

The `integration` tag also permits an explicit `packages=./test/integration` dispatch with `run=^(TestLive_|TestExt_Live_)`. The same external subagent fixture requirements apply on the runner.

The job summary lists each provider's passed, failed, skipped, and incomplete marked live tests. It derives those results from `go test -json`, not the presence of secrets. A shared replay test can reference more than one provider. Counts describe tests, not API requests. Unmarked hermetic tests and catalog assertions do not count as live acceptance. Invalid credentials fail the job. An all-unset run remains green when the hermetic tests pass, with live requests reported as skipped.

## Environment inventory

The test labels in this table expand to exact test names in the next section. Entries marked optional configure a request but do not enable a test by themselves.

| Environment variable | Provider or purpose | Tests and requirement |
|---|---|---|
| `ANTHROPIC_API_KEY` | `anthropic` | Anthropic, Compatibility, Replay, Thinking; alternative to `ANTHROPIC_OAUTH_TOKEN` |
| `ANTHROPIC_OAUTH_TOKEN` | `anthropic` | Same cases; resolved OAuth token takes precedence over the API key |
| `OPENAI_API_KEY` | `openai` | Responses cache affinity, Replay, Images, Thinking |
| `AZURE_OPENAI_API_KEY` | `azure-openai-responses` | Images; also requires one Azure endpoint variable |
| `AZURE_OPENAI_BASE_URL` | `azure-openai-responses` | Images; alternative to `AZURE_OPENAI_RESOURCE_NAME` |
| `AZURE_OPENAI_RESOURCE_NAME` | `azure-openai-responses` | Images; alternative to `AZURE_OPENAI_BASE_URL` |
| `AZURE_OPENAI_DEPLOYMENT_NAME_MAP` | `azure-openai-responses` | Images; optional model-to-deployment mapping |
| `AZURE_OPENAI_API_VERSION` | `azure-openai-responses` | Images; optional API version override |
| `COPILOT_GITHUB_TOKEN` | `github-copilot` | Compatibility, Images, Integration; resolved Copilot bearer, used as supplied without refresh |
| `GEMINI_API_KEY` | `google` | Thinking |
| `GOOGLE_CLOUD_API_KEY` | `google-vertex` | Thinking; alternative to ADC |
| `GOOGLE_APPLICATION_CREDENTIALS` | `google-vertex` | Thinking via ADC; credential file path, optional when the default gcloud ADC file exists |
| `GOOGLE_CLOUD_PROJECT` | `google-vertex` | Thinking via ADC; alternative to `GCLOUD_PROJECT` |
| `GCLOUD_PROJECT` | `google-vertex` | Thinking via ADC; fallback project |
| `GOOGLE_CLOUD_LOCATION` | `google-vertex` | Thinking via ADC; required with either project variable |
| `OPENROUTER_API_KEY` | `openrouter` | Compatibility, Thinking |
| `OPENCODE_API_KEY` | `opencode`, `opencode-go` | Compatibility and `TestOpenCodeModelsLiveUpstream` |
| `XIAOMI_TOKEN_PLAN_AMS_API_KEY` | `xiaomi-token-plan-ams` | `TestPortWave13XiaomiEmptySignatureSmoke` |
| `CLOUDFLARE_API_KEY` | `cloudflare-ai-gateway` | Compatibility |
| `FIREWORKS_API_KEY` | `fireworks` | Compatibility |
| `KIMI_API_KEY` | `kimi-coding` | Compatibility |
| `MINIMAX_API_KEY` | `minimax` | Compatibility |
| `MINIMAX_CN_API_KEY` | `minimax-cn` | Compatibility |
| `AI_GATEWAY_API_KEY` | `vercel-ai-gateway` | Compatibility |
| `PIG_LIVE_CODEX_TOKEN` | `openai-codex` test input | Codex cache affinity and Images; resolved OAuth access token |
| `AWS_PROFILE` | `amazon-bedrock` | Bedrock; named profile alternative |
| `AWS_ACCESS_KEY_ID` | `amazon-bedrock` | Bedrock; requires `AWS_SECRET_ACCESS_KEY` |
| `AWS_SECRET_ACCESS_KEY` | `amazon-bedrock` | Bedrock; requires `AWS_ACCESS_KEY_ID` |
| `AWS_SESSION_TOKEN` | `amazon-bedrock` | Bedrock; optional session token for temporary IAM credentials |
| `AWS_BEARER_TOKEN_BEDROCK` | `amazon-bedrock` | Bedrock; bearer-token alternative |
| `AWS_CONTAINER_CREDENTIALS_RELATIVE_URI` | `amazon-bedrock` | Bedrock; ECS role alternative |
| `AWS_CONTAINER_CREDENTIALS_FULL_URI` | `amazon-bedrock` | Bedrock; ECS role alternative |
| `AWS_WEB_IDENTITY_TOKEN_FILE` | `amazon-bedrock` | Bedrock; web-identity alternative; configure its role through the AWS credential chain |
| `AWS_REGION`, `AWS_DEFAULT_REGION` | `amazon-bedrock` | Bedrock; optional region configuration |
| `BEDROCK_EXTENSIVE_MODEL_TEST` | Test opt-in, not a secret | `TestPortWave13BedrockModels` request subtests; any nonempty value enables the catalog sweep after AWS auth is configured |

For Anthropic key-taking tests, `ANTHROPIC_AUTH_TOKEN` is not an API key. Pi's `getEnvApiKey` excludes it because it belongs in an Authorization header. These tests accept the two Anthropic key/token inputs listed above.

Bedrock accepts any complete credential alternative in the table. A partial IAM pair does not enable a test. The AWS SDK still owns credential loading and validation. Vertex accepts a Cloud API key or project, location, and ADC. An explicitly supplied invalid credential file fails during authentication; it is not treated as an absent secret. Default gcloud ADC discovery uses `~/.config/gcloud/application_default_credentials.json`.

Compatibility rows come from `configured` and `forcedEager` in [`ai/testdata/port-wave-13/anthropic_e2e_cases.json`](../../ai/testdata/port-wave-13/anthropic_e2e_cases.json). Credential names come from the provider catalog, not a separate live-provider map. Update this table when the selected providers or live tests change.

## Test labels

All AI tests below require `-tags live`.

- **Anthropic:** `TestPortWave13AnthropicThinkingDisableE2E`, `TestPortWave13AnthropicThinkingBindingE2E`, `TestPortWave13AnthropicOpus48Smoke`, and the Anthropic rows of `TestInterleavedThinkingLiveUpstream` in `ai/`.
- **Compatibility:** `TestPortWave13AnthropicEagerToolInputE2E` and `TestPortWave13AnthropicLongCacheRetentionE2E` in `ai/`.
- **Replay:** `ai.TestResponsesReasoningReplayLiveUpstream`. Every row needs OpenAI; Anthropic replay rows need both providers.
- **Responses cache affinity:** `ai.TestOpenAIResponsesCacheAffinityLiveUpstream`.
- **Codex cache affinity:** `ai.TestOpenAICodexCacheAffinityLiveUpstream`.
- **Images:** provider rows of `ai.TestOpenAIResponsesToolResultImagesLiveUpstream`.
- **Thinking:** provider rows of `coding.TestThinkingDisableLiveUpstream`, also under `-tags live`.
- **Bedrock:** `TestPortWave13BedrockClaudeMaxTokensE2E`, request rows of `TestPortWave13BedrockModels`, and Bedrock rows of `TestInterleavedThinkingLiveUpstream` in `ai/`. Catalog-only assertions still run without credentials.
- **Integration:** `TestLive_*` in `test/integration/live_test.go` and `live_subagent_test.go` under `-tags 'integration live'`; `TestExt_Live_*` in `ext_test.go`, the auth-gated cases in `session_dir_test.go`, `subagent_parity_test.go`, and `perf_test.go` under `-tags integration`. These share `extIsolatedHome`, which writes only the supplied bearer to a temporary auth file. The paired Pi helper uses the same bearer.

## Shared skip convention

Import `github.com/MichaelKinsy/PiG/internal/testenv` and call:

```go
key := testenv.RequireLiveEnv(t, "OPENAI_API_KEY")
// Multiple names mean all are required. The return value is the first value.
testenv.RequireLiveEnv(t, "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY")
```

`RequireLiveEnv(t testing.TB, names ...string) string` checks for nonempty values. It reports every missing name in argument order and never reports values. It returns the first value when all requirements are present, or an empty string for no requirements. Call it before provider requests. For alternative credential groups, select a configured alternative first; if none is complete, explain the alternatives and pass the missing names to this helper. Do not skip after a request fails.

`TestRequireLiveEnv` covers unset, empty, partially configured, fully configured, and empty requirement lists. It checks the exact skip diagnostic and that configured values do not appear in it. `TestLiveAuthUsesOnlyExplicitEnv` proves that the integration credential helpers ignore ambient auth files.

A live test records `t.Logf("live provider: %s", providerID)` before its credential gate so the workflow can associate a skipped test with its provider. Put this marker in live entry paths, not hermetic tests of credential helpers. The existing AI provider-key and Bedrock wrappers, coding live auth wrapper, and integration live entry paths emit it. `automation/ci/test_summarize_live_tests.py` tests JSON-event aggregation, including parent passes, failures, shared providers, incomplete runs, and output privacy; it does not inspect workflow YAML.

## Inventory boundary

The inventory scans build tags, `Getenv`, `LookupEnv`, provider environment discovery, auth-file gates, and SDK-language environment reads across `ai/`, `coding/`, `internal/`, `cmd/`, `extensions/`, and `test/integration/`.

No live-provider test resides in `internal/`, `cmd/`, or the Go, Rust, Python, and TypeScript SDK test sources. Their environment reads configure hermetic fixtures, select subprocess helper entry points, or enable local probes and benchmarks. Do not route those through the live-secret helper. In particular, leave `PIG_RUNTIME_ORIGINAL_PROBE`, `PIG_RUNTIME_REPLACEMENT_PROBE`, `PIG_REPLACED_2860_PROBE`, `PIG_CUSTOM_SETTLED_PROBE`, `PIG_SDK_MANAGER_PROBE`, `PIG_PT_SHUTDOWN_PROBE`, `PIG_TEST_MODEL_INFO_CYCLE`, `PIG_PROBE_RENDER_RACE`, `PIG_NODE_CELL_MEMORY_PROBE`, `PIG_PARITY_PROBE`, `PIG_WAVE12_CATALOG`, and `PIG_BENCH_SESSION_*` behavior unchanged.

The integration files `live_render_test.go` and `markdown_live_parity_test.go` use faux-provider terminal fixtures, not remote credentials. Their names do not make them live-provider tests. The ordinary AI and coding companion tests likewise use faux endpoints; their inputs and assertions are unchanged.
