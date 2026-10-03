# c1-login-fixes: Pi 1.0.0 product port (phase 1)

Area: Radius in `/login`, Anthropic copy code login, and the remaining 1.0.0 fixes that the codemode, tui and MCP OAuth lanes do not own. Tests come from `p1-login-fixes` (merged at `5dcc0176f`); see `docs/plan/progress/p1-login-fixes/PROGRESS.md` for the case list.

Upstream: `.upstream/v1.0.0` (tag `v1.0.0`, `a13d35a74`) against `.upstream/v0.99.2`. Base: `porter/pi-0.99.1` at `8669fa301`.

## Decision (owner)

`RADIUS_LOGIN_INTRO` (Pi 1.0.0 `interactive-mode.ts:367`) stays verbatim as the Radius method prompt description: "Radius is a service crafted for Pi by the builders of Pi, Earendil Works". The owner accepted it as Pi's attribution of its own service. No divergence is recorded, and `oauth/27-login-radius-method-intro` keeps `escaped_output_equal`.

## Commits

| SHA | Change | Pi 1.0.0 source |
|---|---|---|
| `52efff43e` | Anthropic login method select (browser / copy code), `LoginAnthropicCopyCode`, manual code prompt context; color OAuth page logo; Responses `fc_`/`ctc_` item id per call type | `ai/src/auth/oauth/anthropic.ts:191-291`, `utils/oauth-page.ts:1`, `openai-responses-shared.ts:296-305` |
| `5398e7afc` | `/login`: Radius top-level option with shimmer, Radius intro, back navigation on cancel, "account" labels, "not configured", "No account providers available.", Radius MCP offer | `interactive-mode.ts:367, 405-417, 5722-5925, 6168-6343`; `oauth-selector.ts:20-53`; `radius-login-selector.ts`; `core/radius.ts:5-6` |
| `aabe713c0` | system theme keeps the palette's chroma | `system-theme.ts` |
| `8b5d8458c` | `--provider` requires `--model` | `main.ts:469-474` |
| `11e4ef51b` | slash completion after leading whitespace; ANSI order at slice start | `tui/src/autocomplete.ts`, `tui/src/utils.ts` |
| `79219920a` | restored/reloaded tools stay pending until registered | `agent-session.ts:426-431, 1488-1505, 1762-1782, 3541-3542, 3631-3632` |
| `2014aa17d` | `/mcp login` OSC 8 link; reach-specific `mcp_servers` intro; shell schema text; Radius offer runs the reload handler | `extensions/mcp/index.ts:158-213, 929-938`; `core/tools/bash.ts:56-62`; `interactive-mode.ts:6334` |
| `fee09d644` | user message pads and colors in the Markdown | `components/user-message.ts:38-59` |
| `d132265d3` | PORT_MAP `radius-login-selector.ts` (partial); test-mapping rows ported | - |

## Red -> green (p1-login-fixes tests)

Go tests, all green now:

- `ai`: `TestAnthropicUpstreamOAuth/offers_browser_login_first_and_uses_the_selected_Anthropic_copy_code_flow`, `TestAnthropicCopyCodeLoginImplementation/{rejects_an_unknown_login_method,copy_code_login_shows_its_own_instructions}`, `TestOAuthCallbackServerUpstream/ignores_stray_requests_and_resolves_with_the_completed_code`, `TestOAuthPageMatchesThePinnedPackage`, `TestConstrainedSamplingGrammarReplayDropsForeignItemIDsUpstream`
- `tui`: `TestOAuthSelectorUpstreamStatuses/google`, `TestOAuthSelector_Render`, `TestOAuthSelector_StatusIndicators/oauth_not_configured`
- `internal/codingagent`: `TestRadiusLoginOffersTheRadiusMCPServerUpstream` (2), `TestLoginWithoutProvidersOfTheChosenAuthTypeSaysSoUpstream/oauth`
- `internal/codingagent/tools`: `TestShellToolOutputSchema/{bash,powershell}`
- `cmd/pig`: `TestCLIProviderRequiresModelUpstream` (3)
- `coding/mcpext`: `TestMcpLoginShowsTheSignInURLAsALinkInTheTerminalUI/tui`, `TestMCPServersSectionIntroExplainsOnlyTheReachesOfTheListedServers` (3)

Parity (declared runs, Pi 1.0.0 from `extensions/sdk-ts/node_modules`), green: `selectors/06`, `selectors/10`, `selectors/11`, `oauth/02`, `oauth/22`, `oauth/25`, `oauth/27`, `oauth/29`, `cli-utils/22`.

Still red, not this lane: `oauth/16`, `oauth/17`, `oauth/26`, `oauth/28`. Their text matches; the escaped capture differs only by where tmux places one SGR reset around the blank line under the selector title. Pi 1.0.0 defaults to fullscreen (`settings-manager.ts:1348-1350`) and its alt-screen renderer skips unchanged rows (`tui-alt-screen.ts:1732-1738`), while Pig still defaults to `regular`. With `"tuiMode": "fullscreen"` Pig's capture of the `oauth/16` flow matches Pi byte for byte (probed in tmux). They turn green when c1-tui lands the fullscreen default.

## Own regression tests

- `ai/anthropic_copy_code_test.go` (mutation-checked)
- `internal/codingagent/login_radius_menu_test.go`
- `coding/session_pending_tools_test.go#TestRestoredToolsActivateWhenTheyRegister` (registers/keeps cases fail without the pending append in `refreshTools`)
- `tui/user_message_block_padding_test.go#TestUserMessageBlockPadsInTheMarkdownWithTheBoxOutput`

## Gates

- `go vet` and `GOOS=windows go vet` on touched packages: clean. `golangci-lint` on touched packages: 0 issues (except the existing unused `newAnthropicTestProvider` in `ai`, in an untouched file).
- `go test ./ai ./tui ./tui/widthx ./coding/mcpext ./internal/codingagent ./internal/codingagent/tools`: pass. `./coding`: only the `TestRPC33*` and faux observation oracles fail; they pin the 0.99.2 oracle and fail on the base too.
- `make ci-drift`: lint-scenarios, port-map-drift, coverage-drift (local `make coverage RESULTS=`, restored), divergence-consistency and divergence-quality pass. divergence-guard fails on hits in `agent/harness/**` and `internal/codingagent/tools/harness_bash.go` whose upstream files 1.0.0 removed (the unowned pi-durable migration); none is in a file this lane touched. docs-drift fails on stale upstream-version claims in `docs/extension-sdk-surface.md:2933` and the p1 PROGRESS file.
- `make ci-contracts`: interface-go-drift passes after a local `gointerfaces` run (restored). correspondence-check (`quietStartup`/`tuiMode`, c1-tui), sdk-surface-drift (`generateImages`, c1-codemode) and test-porting-release/known-gaps-drift (other lanes' pending hot-path files) fail outside this lane.

## Not ported here

- `tui/src/components/{box,text,markdown}.ts` `flattenLines`: a V8 memory measure (rope strings) with no Go equivalent; output is unchanged.
- MCP per-server credentials (`index.ts:490-628`): c1-mcp-oauth. Fullscreen default, `quietStartup`, logo: c1-tui. `generateImages`, codemode docs: c1-codemode.
- The pi-durable experimental server migration: unowned (see the p1 PROGRESS file).
