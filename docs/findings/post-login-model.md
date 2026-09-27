# Post-login model selection evidence

## Reference contract

The reference is Pi 0.87.1 (`f07218c4d4bbc12bef056a7058c3dd49dfe41abe`). Paths below are relative to its source root.

| Behavior | Pi source | PiG implementation |
|---|---|---|
| Provider default table and declaration order | `packages/coding-agent/src/core/model-resolver.ts:20` | `internal/codingagent/default_models.go`; startup and authentication use this one table |
| Unknown-model condition | `packages/coding-agent/src/modes/interactive/interactive-mode.ts:298-300` | `isUnknownModel`; PiG represents the initial unknown sentinel as nil |
| Immediate selection, deferred discovery, provider-specific guidance, status/error text | `packages/coding-agent/src/modes/interactive/interactive-mode.ts:5879-5949` | `completeProviderAuthentication`, `finishProviderAuthentication`, `postLoginModel` |
| Bounded refresh, warnings, model/session replacement guard | `packages/coding-agent/src/modes/interactive/interactive-mode.ts:5951-5975` | Owned background work, 15-second context, lossless owner-loop delivery and identity checks |
| API-key and OAuth callers | `packages/coding-agent/src/modes/interactive/interactive-mode.ts:6003-6049,6134-6166` | API-key setter, registered OAuth dialog, Codex dialog, Copilot dialog and llama.cpp dialog |
| Explicit persistence and nonempty scope | `packages/coding-agent/src/core/agent-session.ts:2108-2158` | `SessionHandle.SetModel(..., Persist: true)` and shared scope update |
| Submitted input becomes text | `packages/coding-agent/src/modes/interactive/components/login-dialog.ts:56-64,77-81` | `tui.LoginDialog.ShowInput` retains the prompt, placeholder, submitted text and hint |

The only provider-specific selection rules are Pi's own Radius catalog-order fallback and llama.cpp guidance. Authentication completion never picks a literal Copilot or Anthropic model. `TestDefaultModelPerProviderMatchesPinnedUpstream` derives the defaults and their order from the pinned TypeScript table.

A call-site audit found that the inline Copilot reauthentication implementation had no reachable production caller. The slash-context callback intercepted Copilot before `runOAuthLogin` could enter that branch. The duplicate implementation and its private cancellation state are removed. There is one Copilot dialog route. `TestOAuthLoginCancellationDoesNotCompleteAuthentication` replaces cancellation tests of the dead implementation with the real prompt, cancellation, credential and subsequent-login path.

## Translation and lifetime

Pi awaits local authentication completion before starting its unawaited catalog refresh. PiG runs potentially blocking model construction and Session extension callbacks off the input loop. It posts the completion to the owner loop before starting refresh. Catalog completion also returns through that loop. Refresh work uses the interactive lifetime's context and wait group. Shutdown cancels and drains it. Deferred selection checks both the current Session identity and the original model pointer. No login selection scans Session history.

`TestPostLoginModelDiscovery` ports the discovery cases from `packages/coding-agent/test/suite/regressions/7027-credential-refresh-hang.test.ts:128-219`. It uses the real registry and a controlled catalog store. Fake time proves the 15-second bound without longer test timeouts. It covers preferred default, catalog order, empty catalog, refresh error, replacement and shutdown. `TestPostLoginCompletesBeforeBackgroundRefresh` covers the known-model completion ordering from the same Pi test file.

## Red and mutation evidence

- Before the implementation, `TestAPIKeyLoginSelectsProviderDefault` failed for both OpenAI and Anthropic because the selected model remained empty.
- Before the implementation, `TestAPIKeyLoginWithoutDefaultReportsGuidance` received only `Configured API key for custom-provider` instead of Pi's status and guidance.
- Before the dialog change, `TestLoginDialog_SubmittedInputRemainsVisible` failed because the prompt, placeholder, submitted value and submit hint disappeared.
- `TestLoginDialog_BracketedPasteReachesInput` already passed. It is the requested platform-independent guard for the paste fix in #63, not a newly fixed defect.
- A compiling mutation that made `isUnknownModel` always false failed all four new paired scenarios. Real Pi selected and persisted its default; PiG did not reach the required selected-model status.
- A compiling mutation that removed the model/session identity checks failed both replacement cases in `TestPostLoginModelDiscovery` by selecting Radius's balanced model after replacement.

The new scenarios are `oauth/10-post-login-oauth-model`, `11-post-login-api-key-model`, `12-post-login-oauth-status` and `13-post-login-api-key-status`. Each passed three pairs against the installed real Pi binary. The state cases compare exact command-context and persisted-settings output. The status cases compare the complete notice, normalizing only the isolated credential-file path. Status and state are separate because Pi replaces consecutive status notices. The observer never selects a model.

The OAuth scenario uses the existing faux device/token transport and supplies an enabled catalog entry. The API-key scenario stores a synthetic key through `/login`. Neither makes an inference request. Every new scenario explicitly overrides both agent-directory variables so an inherited worker configuration cannot redirect its credential writes.

Run paired evidence with the qualified Go and Node toolchains and `rg`, `fd` and tmux available. Resolve tool-manager shims to installed tool binaries on PATH before changing HOME; this also applies to `uv` for Python extension tests. Keep compiler and package caches separate from the isolated credential directories.

```sh
isolation=$(mktemp -d)
mkdir -p "$isolation/home" "$isolation/pig-agent" "$isolation/pi-agent" "$isolation/pig-home"
env HOME="$isolation/home" PIG_HOME="$isolation/pig-home" \
  PIG_CODING_AGENT_DIR="$isolation/pig-agent" \
  PI_CODING_AGENT_DIR="$isolation/pi-agent" \
  PIG_PARITY_PI_BIN="$PWD/extensions/sdk-ts/node_modules/.bin/pi" \
  make parity-family FAMILY=oauth
```

The complete OAuth, selectors and model-resolver-selector families passed. Linux and Windows vet, touched-package and full-repository lint, TUI race tests, focused authentication race tests, `go fix -diff ./...`, and `make ci-contracts ci-drift` passed. The Go interface inventory and coverage report are regenerated. Coverage is generated with `RESULTS=` so another worktree's transient run file cannot supply verification claims.

## Resource measurement

`BenchmarkPostLoginModelSelection` uses the complete generated catalog. On the lane's Linux Xeon host it measured approximately 44 microseconds and 76 KB in four allocations per selection. The allocation profile attributes most allocation to the temporary catalog slice. These measurements describe the pure selection pass, not network or extension latency. The catalog refresh remains independently bounded and cancellation-tested.

```sh
env HOME="$isolation/home" PIG_HOME="$isolation/pig-home" \
  PIG_CODING_AGENT_DIR="$isolation/pig-agent" \
  PI_CODING_AGENT_DIR="$isolation/pi-agent" \
  go test ./internal/codingagent -run '^$' -bench BenchmarkPostLoginModelSelection \
  -benchmem -cpuprofile "$isolation/post-login.cpu" -memprofile "$isolation/post-login.mem"
```

## Integration boundaries

The changelog entries live under `## [Unreleased]`, matching the base branch's release convention. The release identity and `TestParseChangelog_RealFile` remain unchanged. The full `go test ./...` run passes with HOME, PIG_HOME and both agent-directory variables pointing at temporary directories and installed tool binaries on PATH.

An initial observer experiment also exposed independent spacing differences after a status when displaying string widgets or custom messages. Those rendering surfaces are outside authentication selection. The final scenarios use status notifications and compare completion notices separately; they do not normalize away the rendering differences. Those observations remain for the UI-surface owner.
