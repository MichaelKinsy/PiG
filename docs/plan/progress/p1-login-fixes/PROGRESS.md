# p1-login-fixes: Pi 1.0.0 test port (phase 1, tests only)

Status: READY. All new and changed upstream tests of this area are ported (test commit `5dcc0176f`).

Area: Radius in `/login`, Anthropic copy code login, and the remaining 1.0.0 fixes that the codemode, tui and MCP OAuth lanes do not own.

Upstream: `.upstream/v1.0.0` (tag `v1.0.0`, `a13d35a74`) against `.upstream/v0.99.2`. Base: `porter/pi-0.99.1` at `8669fa301` (Pi 1.0.0 pin). `.upstream/current` links to `v1.0.0`; Pi 1.0.0 is installed locally in `extensions/sdk-ts/node_modules` (`npm ci --ignore-scripts`, not committed) for the oracle probes and parity runs.

Phase 1 rule: no product fixes and no loosened tests. This lane changes no production file. Red is expected until the fix lane lands.

Status values: `ported-red` fails on the current product for the 1.0.0 behavior; `ported-green` passes unchanged; `harness-only` means the upstream change touches only the test harness.

## Upstream diff v0.99.2..v1.0.0 for this area

### `packages/ai/test/anthropic-oauth.test.ts` (changed)

| upstream case (v1.0.0 line) | change | Go test | status |
|---|---|---|---|
| :42 keeps the localhost redirect_uri for manual callback login | prompt answers `select` with `browser` | `ai/anthropic_oauth_upstream_test.go#TestAnthropicUpstreamOAuth/keeps_the_localhost_redirect_uri_for_manual_callback_login` (`OnSelect: selectAnthropicBrowserLogin`) | ported-green |
| :80 offers browser login first and uses the selected Anthropic copy code flow | new | `...#TestAnthropicUpstreamOAuth/offers_browser_login_first_and_uses_the_selected_Anthropic_copy_code_flow` | ported-red |
| :132 cancels when Anthropic login method selection is cancelled | new | `...#TestAnthropicUpstreamOAuth/cancels_when_Anthropic_login_method_selection_is_cancelled` | ported-green (every prompt rejects with "Login cancelled", so the 0.99.2 browser flow fails the same way; it stays a guard) |
| :144 omits scope from refresh token requests | unchanged (line moved) | `...#TestAnthropicUpstreamOAuth/omits_scope_from_refresh_token_requests` | ported-green |
| :176 anthropicOAuth.login resolves through the manual_code prompt and aborts it after settling | prompt answers `select` with `browser` | `...#TestAnthropicUpstreamOAuth/anthropicOAuth.login_resolves_...` | ported-green |
| :212 completes login through the browser callback and shows the sign-in page | prompt answers `select` with `browser` | `...#TestAnthropicUpstreamOAuth/completes_login_through_the_browser_callback_and_shows_the_sign-in_page` | ported-green |

The Go callbacks split Pi's single `prompt` into `OnSelect` and `OnManualCodeInput[Context]`; each port answers both the way the upstream `prompt` does.

### `packages/ai/test/oauth-callback-server.test.ts` (changed)

| upstream case | change | Go test | status |
|---|---|---|---|
| :52 ignores stray requests and resolves with the completed code | asserts the color logo fills `#F09082`, `#4D9ABF`, `#F1BE58` (:74-76) | `ai/oauth_callback_server_upstream_test.go#TestOAuthCallbackServerUpstream/ignores_stray_requests_and_resolves_with_the_completed_code` | ported-red |
| :80-:237 (11 other cases) | unchanged; lines moved by 3 | same file, citations updated to v1.0.0 lines | ported-green |

The existing Pi oracle `ai/oauth_page_upstream_test.go#TestOAuthPageMatchesThePinnedPackage` (renders through the installed Pi 1.0.0) also fails now; it was not changed.

### `packages/coding-agent/test/oauth-selector.test.ts` (changed)

| upstream case | change | Go test | status |
|---|---|---|---|
| :74 renders an option without compiled auth status as not configured | `unconfigured` → `not configured` | `tui/oauth_selector_upstream_test.go#TestOAuthSelectorUpstreamStatuses/google` | ported-red |
| :99 shows environment API key auth as configured | excludes `not configured` | `...#TestOAuthSelectorUpstreamStatuses/openai` | ported-green (still green: the exclusion is weaker than before) |
| :18, :87, :112, :130 | unchanged | existing evidence | ported-green |

Pig tests that pinned the removed 0.99.2 text now pin the 1.0.0 text: `tui/oauth_selector_test.go#TestOAuthSelector_Render` and `#TestOAuthSelector_StatusIndicators/oauth_not_configured` (ported-red, `oauth-selector.ts:37`).

### `packages/ai/test/constrained-sampling.test.ts` (changed)

| upstream case | change | Go test | status |
|---|---|---|---|
| :260 drops foreign item ids when replaying grammar calls as custom Responses items (radius#115) | new | `ai/constrained_sampling_upstream_test.go#TestConstrainedSamplingGrammarReplayDropsForeignItemIDsUpstream` | ported-red (`id` is `fc_…`, want omitted) |

### `packages/coding-agent/test/suite/regressions/5943-session-start-notify.test.ts` (changed)

| upstream case | change | Go test | status |
|---|---|---|---|
| :220 renders loaded resources before restored messages without stale entries | the fake `this` stubs `shouldShowStartupDetails: () => true` (:35, :212) because `showLoadedResources` now calls it | `internal/codingagent/session_start_notify_upstream_test.go#TestSessionStartNotifyOriginalLoadedResources` | harness-only, green. The Go port drives the real `InteractiveMode` with `Verbose: true`, which makes Pi's `shouldShowStartupDetails` true (`interactive-mode.ts:1415`). Citation updated. |
| :240-:439 (6 other cases) | unchanged; lines moved by 2 | existing evidence; `reload_ui_upstream_test.go` citation updated | green |

### `packages/coding-agent/test/suite/harness.ts` (changed)

The `sessionManager` option supports the new `agent-session-mcp.test.ts` resume cases. The p1-mcp-oauth lane ported those cases; not ported again here.

## Implementation-derived tests (no upstream test)

These 1.0.0 changes have no upstream test. Each test cites the Pi source and, where visible, a Pi 1.0.0 probe.

| Behavior | Pi 1.0.0 source | Go test / scenario | status |
|---|---|---|---|
| Unknown Anthropic login method fails before a flow starts | `anthropic.ts:286-288` | `ai/anthropic_oauth_upstream_test.go#TestAnthropicCopyCodeLoginImplementation/rejects_an_unknown_login_method` | red |
| Copy code login instructions | `anthropic.ts:203-207` | `...#TestAnthropicCopyCodeLoginImplementation/copy_code_login_shows_its_own_instructions` | red |
| Copy code login rejects a mismatched pasted state | `anthropic.ts:216` | `...#TestAnthropicCopyCodeLoginImplementation/copy_code_login_rejects_a_mismatched_state` | green (the browser flow checks the state too) |
| Anthropic `/login` shows the method selector | `anthropic.ts:273-291`, `interactive-mode.ts:6168-6206` | `test/parity/scenarios/oauth/25-anthropic-login-method-selector.toml` | red (Pig opens the browser flow at once) |
| `/login` offers "Sign in with Radius" with its status as the last top-level option | `interactive-mode.ts:5809-5831`, `oauth-selector.ts:36-53` | `selectors/06-oauth-selector.toml`, `oauth/16-login-provider-back.toml` (re-probed, see below), `oauth/27-login-radius-method-intro.toml`, `oauth/28-login-radius-cancel-returns-to-menu.toml` | red |
| The Radius method prompt shows `RADIUS_LOGIN_INTRO` | `interactive-mode.ts:367`, `6168-6200` | `oauth/27-login-radius-method-intro.toml` | red |
| Cancelling a login returns to the menu it was started from | `interactive-mode.ts:5850-5853`, `5912-5914`, `6283-6286` | `oauth/26-login-cancel-returns-to-provider-list.toml`, `oauth/28-login-radius-cancel-returns-to-menu.toml` | red |
| After a Radius sign-in, `/login` offers to configure the Radius MCP server in the global `mcp.json` | `interactive-mode.ts:6276`, `6296-6343`; `core/radius.ts:5-6` `RADIUS_MCP_URL`; `extensions/mcp/config.ts:156-166` | `internal/codingagent/radius_login_mcp_offer_upstream_test.go#TestRadiusLoginOffersTheRadiusMCPServerUpstream` (new server; existing server keeps its name, `auth` replaces `oauth`) | red |
| `/logout` labels a non-subscription OAuth sign-in "account" | `interactive-mode.ts:5747-5759`, `oauth-selector.ts:27-33` | `oauth/29-logout-radius-account-label.toml` (one differing line: `Radius [subscription]` vs `Radius [account]`) | red |
| `/login <provider>` completion says "account" for non-subscription OAuth | `interactive-mode.ts:405-417` | `oauth/22-login-scoped-methods.toml` (wait step re-probed: `OpenRouter · account/API key`) | red. The upstream case in `interactive-mode-status.test.ts` is ported by p1-tui. |
| "No account providers available." | `interactive-mode.ts:5886-5895` | `internal/codingagent/login_no_providers_upstream_test.go#TestLoginWithoutProvidersOfTheChosenAuthTypeSaysSoUpstream` | `oauth` red, `api_key` green |
| `--provider` without `--model` is an error (#10236) | `main.ts:469-474` | `cmd/pig/provider_requires_model_upstream_test.go#TestCLIProviderRequiresModelUpstream` (built-in, built-in with message, unknown provider); `cli-utils/22-cli-provider-requires-model.toml` | red |
| `/mcp login` shows the sign-in URL as an OSC 8 link and a "Ctrl+click to open" line in the TUI (#10186) | `extensions/mcp/index.ts:929-938`, `tui/src/terminal-image.ts:700-702` | `coding/mcpext/sign_in_link_internal_test.go#TestMcpLoginShowsTheSignInURLAsALinkInTheTerminalUI` | `tui` red; `print` and `rpc` green |
| The `mcp_servers` section intro names only the reaches its servers use | `extensions/mcp/index.ts:158-164`, `193-197` | `coding/mcpext/servers_section_intro_upstream_test.go#TestMCPServersSectionIntroExplainsOnlyTheReachesOfTheListedServers` | red (3 subtests) |
| Bash and PowerShell output schema descriptions | `core/tools/bash.ts:56-62` | `internal/codingagent/tools/shell_structured_test.go#TestShellToolOutputSchema` now pins the schema recorded from npm `@earendil-works/pi-coding-agent@1.0.0` | red (2 subtests) |

## Parity scenarios

Probed against Pi 1.0.0 (`extensions/sdk-ts/node_modules/.bin/pi`). Every Pi side reaches all steps; every Pig failure is the 1.0.0 behavior gap named below.

| Scenario | Change | Pig result |
|---|---|---|
| `selectors/06-oauth-selector` | waits for and asserts `Sign in with Radius • not configured` | red |
| `selectors/10-login-subscription-providers` | `unconfigured` → `not configured` in crop end, wait and assertions | red |
| `selectors/11-login-api-key-providers` | `unconfigured` → `not configured` | red |
| `oauth/16-login-provider-back` | asserts the Radius row | red |
| `oauth/17-login-method-search` | asserts `• not configured` rows | red |
| `oauth/22-login-scoped-methods` | completion wait `OpenRouter · account/API key` | red |
| `oauth/02-oauth-anthropic` | the Pi harness (`test/parity/testdata/oauth-harness.ts`) answers the new `select` prompt with `browser`, as the upstream tests do | green |
| `oauth/25-anthropic-login-method-selector` | new | red |
| `oauth/26-login-cancel-returns-to-provider-list` | new | red |
| `oauth/27-login-radius-method-intro` | new | red |
| `oauth/28-login-radius-cancel-returns-to-menu` | new | red |
| `oauth/29-logout-radius-account-label` | new (fixture `oauth/testdata/logout-radius-account`) | red |
| `cli-utils/22-cli-provider-requires-model` | new | red |

`test/parity/normalization-inventory.json` is regenerated for the new crops.

## Failure list by suspected root cause

1. **Anthropic login has no method prompt and no copy code flow.** `AnthropicOAuthProvider.LoginContext` calls `LoginAnthropic` directly (`ai/oauth_anthropic.go:225`); Pi 1.0.0 first prompts "Select Anthropic login method:" and runs `loginAnthropicCopyCode` with redirect `https://platform.claude.com/oauth/code/callback` (`anthropic.ts:191-226, 273-291`).
   - `TestAnthropicUpstreamOAuth/offers_browser_login_first_and_uses_the_selected_Anthropic_copy_code_flow`
   - `TestAnthropicCopyCodeLoginImplementation/rejects_an_unknown_login_method`, `/copy_code_login_shows_its_own_instructions`
   - `oauth/25-anthropic-login-method-selector`
   - Fix notes: `runLoginRegisteredOAuth.selectMethod` (`internal/codingagent/interactive_auth.go:291-301`) rejects an interactive select prompt unless a method was preselected; Pi shows it in the editor slot (`showAuthSelect`). The Pig parity probe `probeOAuthAnthropic` (`internal/codingagent/parity_harness.go:267`) calls `ai.LoginAnthropic` directly; route it through the provider with `OnSelect` → `browser` when the flow changes. `ai/anthropic_oauth_lifecycle_test.go` calls `LoginAnthropic` (the browser flow) directly; keep that function as the browser flow or route those tests through the provider with `OnSelect`.
2. **OAuth pages use the white logo.** `ai/oauth_page.go:10` `logoSVG` is the 0.99.2 SVG (`utils/oauth-page.ts:1`).
   - `TestOAuthCallbackServerUpstream/ignores_stray_requests_and_resolves_with_the_completed_code`, `TestOAuthPageMatchesThePinnedPackage`
3. **"unconfigured" label.** `tui/oauth_selector.go:143, 161` (`oauth-selector.ts:37`).
   - `TestOAuthSelectorUpstreamStatuses/google`, `TestOAuthSelector_Render`, `TestOAuthSelector_StatusIndicators/oauth_not_configured`
   - `selectors/10`, `selectors/11`, `oauth/17`; `oauth/26` also captures `• not configured` rows after its return-path failure is fixed.
4. **Every OAuth sign-in is labeled "subscription".** `tui.FormatAuthSelectorProviderType(authType)` and `authSelectorIndicator`'s `configuredOtherLabel` take no subscription flag (`tui/oauth_selector.go:53, 120-127`); `tui.OAuthProvider` has no `subscription` field and `getLogoutProviderOptions`/`getLoginProviderOptions` do not set it from `auth.oauth.isSubscription` (`internal/codingagent/interactive_login.go:33-137`). Pi: `oauth-selector.ts:20-33`, `interactive-mode.ts:5722-5759`.
   - `oauth/29-logout-radius-account-label`, `oauth/22-login-scoped-methods`; p1-tui's `TestInteractiveLoginArgumentCompletionUpstream` has the same cause.
5. **No top-level Radius option, no Radius intro, no return to the previous menu on cancel.** `showLoginAuthTypeSelector` offers two options (`internal/codingagent/interactive_login.go:182-198`); `handleLoginCommand` has no Radius branch and returns after a cancelled login (`internal/codingagent/slash_auth.go:13-61`); `selectOAuthLoginMethod` shows the Radius prompt without `RADIUS_LOGIN_INTRO` (`internal/codingagent/interactive_auth.go:215-230`). The shimmering selected Radius row (`radius-login-selector.ts`, PORT_MAP ⬜) is not ported.
   - `selectors/06`, `oauth/16`, `oauth/26`, `oauth/27`, `oauth/28`
6. **No Radius MCP server offer after a Radius sign-in.** Pig has no `offerRadiusMcpServer` and no `RADIUS_MCP_URL` (`core/radius.ts:5-6`, `interactive-mode.ts:6296-6343`).
   - `TestRadiusLoginOffersTheRadiusMCPServerUpstream` (2 subtests)
7. **"No subscription providers available."** `internal/codingagent/slash_auth.go:46-52` (`interactive-mode.ts:5890`).
   - `TestLoginWithoutProvidersOfTheChosenAuthTypeSaysSoUpstream/oauth`
8. **Grammar replay keeps foreign item ids.** The Responses converter keeps a normalized foreign `fc_…` id on a `custom_tool_call`; Pi 1.0.0 drops every id when the model differs or the prefix does not match the replayed item type (`openai-responses-shared.ts:296-305`).
   - `TestConstrainedSamplingGrammarReplayDropsForeignItemIDsUpstream`
9. **`--provider` without `--model` is ignored.** `cmd/pig` resolves the default model of another provider (`main.ts:469-474`).
   - `TestCLIProviderRequiresModelUpstream` (3 subtests), `cli-utils/22-cli-provider-requires-model`
10. **`/mcp login` prints a plain URL in the TUI.** `coding/mcpext/command.go:267-270` (`index.ts:929-938`, #10186).
    - `TestMcpLoginShowsTheSignInURLAsALinkInTheTerminalUI/tui`
11. **Old `mcp_servers` intro.** `coding/mcpext/extension.go:208` is the 0.99.2 constant; Pi 1.0.0 builds it from the reaches in use and sizes the section with it (`index.ts:158-164, 193-213`).
    - `TestMCPServersSectionIntroExplainsOnlyTheReachesOfTheListedServers` (3 subtests). The codemode lane noted this as an MCP-lane item; the MCP OAuth lane did not take it.
12. **Old shell output schema descriptions.** `internal/codingagent/tools/shell_tool.go` (`bash.ts:56-62`).
    - `TestShellToolOutputSchema/bash`, `/powershell`

## Not ported: unassigned 1.0.0 area

The experimental server's move onto the new `@earendil-works/pi-durable` package is not a login or misc fix and belongs to no phase-1 lane. It removes the agent harness from `pi-agent-core` and rewrites the experimental session worker, transcript, plugin reload and server testing host on `pi-durable` conversations. A dedicated lane should own it.

| Upstream test file | Change | Cases (v1.0.0) |
|---|---|---|
| `coding-agent/test/experimental-agent-controller.test.ts` | changed | 3 |
| `coding-agent/test/experimental-client-tui.test.ts` | changed | describe-level |
| `coding-agent/test/experimental-plugin-reload.test.ts` | changed (uses `openFauxConversation`) | 1 |
| `coding-agent/test/experimental-remote-runtime.test.ts` | changed (2 renamed, 1 replaced, prompt waits) | 26 |
| `coding-agent/test/experimental-session-worker-lifecycle.test.ts` | changed (`setHarnessActive`) | 12 |
| `coding-agent/test/experimental-session-worker-manager.test.ts` | changed (`SessionCatalogMetadata`) | 9 |
| `coding-agent/test/experimental-transcript-provider.test.ts` | rewritten | 1 |
| `coding-agent/test/experimental-{session,durable}-support.ts`, `fixtures/faux-session-worker.ts` | support | - |
| `server/test/conformance.test.ts`, `server/src/testing/host.ts` | changed (`metadata` instead of `session`) | 20 |
| `durable/test/*.test.ts` | 27 changed or new files (`harness-lifecycle`, `harness-task-graph`, `spec-usage`, `chord-guide` new) | about 600 |

`coding-agent/test/mcp-conformance/` is an npx network harness, not a vitest file; p1-mcp-oauth recorded it.

## Ledger follow-ups for the pin move

`test/parity/interfaces/test-mapping-v1.0.0.json` lists these files as `pending`; they need the evidence above and `partial` until the fixes land: `ai/test/anthropic-oauth.test.ts` (add `#TestAnthropicCopyCodeLoginImplementation`), `ai/test/oauth-callback-server.test.ts`, `ai/test/constrained-sampling.test.ts` (add `#TestConstrainedSamplingGrammarReplayDropsForeignItemIDsUpstream`), `coding-agent/test/oauth-selector.test.ts`, `coding-agent/test/suite/regressions/5943-session-start-notify.test.ts` (no case change; can return to `ported`). PORT_MAP: `components/radius-login-selector.ts` is ⬜.

## Gates

Commands ran with `HOME`, `PIG_CODING_AGENT_DIR` and `PI_CODING_AGENT_DIR` set to temp directories.

- `go vet` and `GOOS=windows go vet` on `./ai ./tui ./internal/codingagent ./internal/codingagent/tools ./cmd/pig ./coding/mcpext`: clean.
- `go tool golangci-lint run` on the same packages: 1 finding, pre-existing and in an untouched file (`ai/provider_payload_helpers_test.go:22` unused `newAnthropicTestProvider`).
- `make lint-scenarios`: pass.
- `make ci-drift`: lint-scenarios, port-map-drift, coverage-drift (after a local `make coverage RESULTS=`, restored before commit), divergence-consistency and divergence-quality pass; divergence-guard fails on 3 pre-existing `magic-literal` hits in untouched files (`agent/harness/utils/adaptive_publisher.go:69`, `output_capture.go:18`, `internal/codingagent/tools/harness_bash.go:107`); docs-drift fails on a pre-existing claim of the old pin (`docs/extension-sdk-surface.md:2933`).
- `make ci-contracts`: fails on the pin, not on this lane: correspondence-check (`quietStartup`/`tuiMode`, p1-tui), test-porting-release and known-gaps-drift (every changed 1.0.0 test file is `pending`), sdk-surface-drift (`ctx.modelRegistry.generateImages`, codemode).
- `go test ./ai ./tui`: only the tests listed above, plus p1-tui's `TestGenerateSystemThemeColorsMatchesUpstream`, fail.
- `go test ./internal/codingagent ./cmd/pig`: besides the tests above, Python/Rust SDK fixture tests fail because the temp `HOME` hides the toolchains, and `cmd/pig` reaches its 10-minute package timeout in `TestRPCBedrockConverseStreamObservation`. Neither involves a file this lane changed.
- Parity (`-pig-parity.runs=1`): `oauth/02` passes; the 12 other listed scenarios fail on Pig only.
