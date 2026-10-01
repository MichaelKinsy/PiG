# fix-992-federation-auth: Anthropic federation as a first-class auth source

Base: `staging/porter/pi-0.99.1` at `94f45865e` (pins Pi 0.99.2). Source: audit-992-ai findings A (F1, F3, F6) and B (F2). The audit-992-coding report of F3 is covered here. Upstream: `.upstream/v0.99.2/packages/ai/src/{providers/anthropic.ts,api/anthropic-messages.ts,env-api-keys.ts,utils/provider-env.ts}`; the `.upstream/v0.99.1` copies of these files have no federation, so every behavior below is 0.99.2.

## Root cause

Pi has one rule: the anthropic auth resolver (`providers/anthropic.ts:47-69`) reports federation (`{auth: {}, env, source: "workload identity federation"}`) after `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_OAUTH_TOKEN` and `ANTHROPIC_API_KEY`, and the model runtime's configured-provider snapshot, auth status and request path all read that resolver. `env-api-keys.ts` is unchanged for federation. PiG had that rule in `ai/auth_providers.go` only; three other gates re-implemented "has auth" without it.

| finding | gate | fix |
|---|---|---|
| F1 | `ai/anthropic.go` injected ambient `ANTHROPIC_AUTH_TOKEN` as an Authorization header before the federation check (`authTokenHeaders`). Pi's API implementation never reads it (`anthropic-messages.ts:331-341` `PiAnthropic`, `:614-615`) | deleted `authTokenHeaders`; the resolver owns the variable |
| F2 | `ai/anthropic_federation.go` wrapped the `*url.Error` | `fetchRejectionText` gives `String(rejection)`: `TypeError: fetch failed` (`oidc-federation.mjs:47-49`) |
| F3 | `internal/codingagent` `hasEnvAuth`, `GetProviderAuthStatus`, `ExtensionProviderAuthStatus` ignored federation (so `--list-models`, default model, `-p`, picker, footer count) | `hasEnvAuth` and a shared `envAuthStatus` consult `ai.AnthropicFederationEnv` |
| F6 | `shims/pi-ai-bridge.mjs` rejected a federation-only anthropic request before the host | `hasAnthropicFederation` mirrors `getAnthropicFederation` (`anthropic-messages.ts:343-372`) in the gate; scoped env then process env as `provider-env.ts` |

One shared Go function, `ai.AnthropicFederationEnv(env)` (plus `AnthropicFederationAuthSource`), is now used by the auth resolver, `getAnthropicFederation`, and the coding registry. The JS gate is the one unavoidable second copy (it runs in the extension process before the host call) and is covered by `TestNodeBridgeFederationGate`.

## Commits (red, green order)

1. `fdd8a2f16` test(ai): port Pi 0.99.2 federation auth tests with signature stubs (red)
2. `70c80ff54` fix(ai): make Anthropic federation a first-class auth source (green)
3. docs/progress, then `chore(port-99): regenerate generated files`

## Red run (commit 1)

Failing for the right reason (stub returns nil / old behavior): `ai` TestAudit992DirectAnthropicStreamIgnoresAmbientAuthTokenForFederation (exchanges 0, `Bearer ambient-auth-token`), TestAudit992FederationUnreachableTokenEndpointMessage (`Post "...": fetch failed`), TestAnthropicDirectStreamDoesNotReadAmbientAuthToken, TestAnthropicFederationEnv (3 subtests); `coding` TestAudit992FederationEnvConfiguresAnthropic; `internal/codingagent` TestHasEnvAuthAnthropicFederation, TestAuthenticatedProvidersAnthropicFederation, TestProviderAuthStatusAnthropicFederation; `cmd/pig` TestPrintModelList_AnthropicFederationEnv_ListsAnthropic, TestSelectStartupModel_AnthropicFederationEnv_SelectsAnthropic, TestModelRuntimeStream_AnthropicFederationOrder/federation_only; `subprocess` TestAudit992NodeBridgeForwardsFederationOnlyAnthropicRequest, TestNodeBridgeFederationGate/federation_process_env. Negative gate cases (incomplete env, empty value, other provider, no env) and the AUTH_TOKEN / API-key ordering cases passed in red as guards.

## Green

All of the above pass. Existing tests touched in the green commit, with the upstream reason:

- `ai` TestAnthropicRequestAuthShapes (3 cases) and `coding` TestBuildModelAnthropicEnvAuthRequestShapes, `cmd/pig` TestBuildModelAnthropicAuthRequestShapes: they read `ANTHROPIC_AUTH_TOKEN` through a bare provider stream. Upstream's `anthropic-auth-token.test.ts` ("threads authContext ANTHROPIC_AUTH_TOKEN through request headers", "lets explicit request headers override") goes through `createModels().streamSimple`, because only the resolver reads the variable. The cases now go through `Models` / `ModelRuntime.Complete` with the same assertions. No assertion was weakened.
- `ai/anthropic_federation_auth_test.go`: one `maps.Copy` from `go fix`.
- `runtime-node.zip` and `runtime_node_digest_generated.go` are regenerated with `go generate` (the bridge shim is embedded); the lead may regenerate centrally.

Mutation checks (each reverted, each failed the named tests): optional variables dropped, organization not required, `TypeError:` prefix dropped, registry federation off, bridge gate off. Removing the F1 fix is the red run.

## Gates

- `go build ./...`, `go vet`, `GOOS=windows go vet` (ai, coding, cmd/pig, internal/codingagent, subprocess): clean. `gofmt`: clean for touched files. `go fix -diff`: empty after the test fix. golangci-lint on the five packages: one finding, `ai/openai.go:1084` goimports, present on the base and not touched here.
- Load: `-race -count=24` with `GOMAXPROCS=4`, `taskset -c 0-3` and 4 CPU burners (PIDs recorded, killed by PID): ai federation/auth tests, internal/codingagent, coding, cmd/pig all ok; subprocess bridge tests `-count=6` ok.
- `make parity-family`: providers-registry ok. model-resolver-selector fails only `21-post-login-default-model` and startup fails 4 scenarios; both pig and pi output are identical there (installed oracle Pi is 0.99.1: `v0.99.1` banner, `kimi-k3` default), so these are the unhydrated-0.99.2 oracle, not this change.
- Wider `go test` of ai, coding, internal, cmd, agent, extensions: every failing test is an oracle or environment test (missing Pi `node_modules` entries, `Xvfb`, proper-lockfile oracle); none is an auth, federation or registry test. `cmd/pig` hits the 10 minute package timeout in the oracle-bound tests; the targeted auth/model tests pass.
- `make generate` stops at `known-gaps` (`packages/ai/test/images-models.test.ts` policy mismatch, not this lane). `pig-go.json` regenerated in the last commit.

## Deferred / for other lanes

- A parity scenario of `--list-models` and `-p` with federation-only env against real Pi needs the Pi 0.99.2 npm package (the installed oracle is 0.99.1, which has no federation); add it when the oracle is hydrated.
- F4 (`Retry-After` Date.parse) and F5 (strict-reason key order) belong to `fix-992-ai-js-semantics`; their red tests were not cherry-picked here.
- Ledger rows (`upstream-sync/v0.99.2.toml`, `PORT_MAP.md` anthropic rows) belong to `fix-992-classify` and `fix-992-hygiene`; status for `anthropic-messages.ts` and `providers/anthropic.ts` can follow this lane once F2/F3/F6 merge.
- Stubs other families must fill: none.
- `AnthropicFederationEnv` and `AnthropicFederationAuthSource` are new exported helpers in `ai`. Pi has no equivalent export (Pi's registry reads the resolver through `Models`); they exist because the registry's availability check is synchronous Go. The lead should classify them in the interface mapping.
