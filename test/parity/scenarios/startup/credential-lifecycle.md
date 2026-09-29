# Startup credential lifecycle evidence

## Reference contract

Reference: Pi 0.87.1, commit `f07218c4d4bbc12bef056a7058c3dd49dfe41abe`.

| Behavior | Pi source |
|---|---|
| Explicit startup model selection uses the catalog without resolving a credential. | `packages/coding-agent/src/core/model-resolver.ts:406-420` |
| Streaming awaits request authentication before calling the provider. | `packages/ai/src/models.ts:648-661,703-709` |
| Stored credentials take precedence; ambient auth applies only when none is stored. | `packages/ai/src/auth/resolve.ts:87-109` |
| A failed OAuth refresh propagates instead of selecting ambient auth. | `packages/ai/src/auth/resolve.ts:139-162` |
| Logout deletes the stored credential. | `packages/ai/src/models.ts:628-637` |
| Each Radius gateway owns its OAuth handler. | `packages/ai/src/providers/radius.ts:23-38` |

PiG retains the existing `coding/model.go:acceptsRequestAPIKey` boundary: API kinds without a per-request key callback resolve once during construction. This change does not add callbacks to those providers.

## Red and green evidence

The original startup path fails the lazy OAuth tests during construction, before any request. It fails Radius construction with `unknown OAuth provider: radius-dev`. It sends `stored-key` after deletion instead of `env-key`.

After the fix, these tests pass:

- `TestBuildModel_OAuthRefreshFailureSurfacesWhenNoFallback`: Anthropic, OpenAI Completions, and OpenAI Responses fail on the first request.
- `TestBuildModel_StoredOAuthRefreshFailureBlocksEnvFallback`: the first request preserves the refresh failure even with an environment key.
- `TestBuildModelContextCancelsOAuthRefresh`: startup does not capture its canceled context; canceling the active request cancels refresh and leaves the credential unchanged.
- `TestBuildModelLogoutFallsBackToEnv`: the same constructed model uses the environment key after deletion for stored API keys and OAuth credentials, across all three callback API kinds.
- `TestBuildModelResolvesStaticAPIKeyOnce`: all six non-callback API kinds preserve build-time failure and perform exactly one successful key derivation.
- `TestBuildModelRadiusStoredCredential`: startup makes no network request; the first stream refreshes through the configured Radius gateway and persists the rotated credential.

A compiling mutation reinstates eager `ResolveStoredAPIKeyFromStorageContext` and copies its result into `entry.APIKey`. All six regression tests fail behaviorally. Static API construction derives the key twice. The canonical parity scenario fails before its first RPC state response. Removing the mutation restores the passes.

`05-startup-credential-lifecycle.toml` drives each real CLI through RPC, using a temporary agent directory and a loopback HTTP server. Pi is probed before the production change. The scenario asserts model/API identity, zero startup HTTP requests, the Radius refresh error and absence of an ambient request, successful OpenAI completion, and stored-to-environment request headers after deleting the credential without rebuilding the running model. Both OpenAI API paths run. The comparator is `output_equal`, with three pairs and no normalization. This proves the credential-deletion effect of logout, not the logout selector UI.

## Reproduction

```sh
make parity-family FAMILY=startup
make parity-family FAMILY=model-resolver-selector
make parity-family FAMILY=oauth
make parity-family FAMILY=model-runtime-store-catalog
go test ./...
go test -race ./cmd/pig -run 'TestBuildModel(_OAuthRefresh|_StoredOAuthRefresh|ContextCancels|Logout|Radius|ResolvesStatic)' -count=5
go vet ./...
GOOS=windows go vet ./cmd/pig
go tool golangci-lint config verify
go tool golangci-lint run ./cmd/pig/...
make lint-changed LINT_BASE=public/main
make lint
make ci-contracts ci-drift
```

All commands pass. `public/main` is the lane base; this worktree has no local `main` ref. The generated Go interface inventory removes the deleted helper, and coverage generation adds this scenario without importing unrelated transient run results:

```sh
go run ./test/parity/cmd/gointerfaces -out test/parity/interfaces/pig-go.json
make coverage RESULTS=
```

## Resource disposition

No background task, cache, lock, or retained credential is added. Request callbacks retain the registry and configured/environment fallback, not the resolved stored key. Existing request contexts own refresh. The tests drain streams, join the cancellation probe, close loopback servers, and delete temporary credentials. The parity fixture joins its reader and server threads and treats forced CLI shutdown as failure.
