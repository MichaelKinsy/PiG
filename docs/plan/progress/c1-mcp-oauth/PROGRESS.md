# c1-mcp-oauth: Pi 1.0.0 MCP OAuth code port (phase 1, product code)

READY. Every test that p1-mcp-oauth ported is green with this branch (merged at `e142c92ff`).

Upstream: `.upstream/v1.0.0` against `.upstream/v0.99.2`. Base: `porter/pi-0.99.1` at `8669fa301`.

## Red to green (sibling tests)

| Go test | Before | After | Fix |
|---|---|---|---|
| `mcp/client_test.go#TestClientPaginatesToolsAndPreservesProtocolToolDefinitions` | red | green | `mcp/client.go` validateListPage: `nextCursor` `null` or `""` ends pagination (client.ts) |
| `mcp/oauth/oauth_test.go#TestMCPOAuthDiscoversRegistersAuthorizesWithPKCEAndRefreshesOn401` | red | green | `mcp/oauth/types.go` absent(); `flow.go` withScope and the `\|\|` scope fallback |
| `mcp/oauth/oauth_test.go#TestMCPOAuthAsksForAuthorizationInsteadOfRefreshingWhenTheServerNeedsMoreScope` | red | green | `flow.go` StepUpScope in oauthAdapter.OnUnauthorized |
| `mcp/oauth/oauth_test.go#TestMCPOAuthUsesAConfiguredAuthorizationServerMetadataDocumentAsIs` | red | green | `flow.go` runFlow secure metadata URL, no discovery cache; `discovery.go` DiscoverOAuthServerInfo |
| `mcp/oauth/oauth_test.go#TestMCPOAuthExchangesACodeOnlyWhenItsIssParameterNamesTheAuthorizationServer` | red | green | `flow.go` RFC 9207 check; `types.go` authorization_response_iss_parameter_supported |
| `coding/mcpext/config_test.go#TestMCPConfigValidatesTheOAuthCallbackURLScopeAndClientName` | red | green | `coding/extension/mcp_servers.go` validateOAuth authServerMetadataUrl |
| `coding/mcpext/oauth_refresh_test.go#TestMCPOAuthSignInRejectsAnAuthorizationResponseFromAnotherIssuer` | red | green | `coding/mcpext/oauth.go` waitForAuthorizationResponse passes `iss` |
| `coding/mcpext/oauth_refresh_test.go#TestMCPOAuthSignInKeepsTheGrantedScopeWhenTheServerAsksForMore` | red | green | `oauth.go` SignInMcpServer step-up scope; `flow.go` withScope |
| `coding/mcpext/oauth_refresh_test.go#TestMCPOAuthSignInUsesTheConfiguredAuthorizationServerMetadataURL` | red | green | `oauth.go` SignInMcpServer and refreshLocked pass the metadata URL |
| `coding/mcpext/oauth_store_test.go#TestMCPOAuthCredentialStoreKeepsSeparateCredentialsForServersSharingAServerURL` | red | green | `oauth.go` storeKeys `mcp__<name>\|<url>` |
| `coding/mcpext/oauth_store_test.go#TestMCPOAuthCredentialStoreMovesCredentialsStoredByServerURLToTheFirstServerThatLoadsThem` | red | green | `oauth.go` serverStore.Load takes legacy state over; Tokens falls back to it |
| `coding/mcp_session_resume_upstream_test.go#...DeclaresToolsToolSearchLoadedAgainOnResumeOnceTheirServerConnects` | red | green | `coding/session_tools.go` pending tools (`_pendingToolNames`), restore on construction over a supplied session manager |
| `coding/mcp_session_resume_upstream_test.go#...RestoredToolsWhenAnExtensionSetsTheLoadoutBeforeTheyRegister/keeps` | red | green | `SetActiveToolsByName` keeps pending tools for an additive loadout |
| `coding/mcp_session_resume_upstream_test.go#...DeclaresToolsToolSearchLoadedAgainAfterReload` | red | green | `coding/session_tool_registry.go` RefreshToolsAfterReload marks active tools pending; `refreshTools` activates registered pending tools |

The cases the sibling reported green before the fixes stay green: the concurrent-401 refresh with invalid resource metadata, the two refresh cases, signing out of legacy credentials, the "drops" loadout case, and the next-prompt case. Mutation checks: each pending-tools step and each OAuth fix fails at least one of these tests when removed.

## PiG regression tests added

- `mcp/client_cursor_test.go`: `null`, `""` and numeric cursors.
- `mcp/oauth/absent_test.go`: absent token, registration and metadata fields; invalid URL message; `received none`; StepUpScope (JavaScript `\s`); a present empty `iss`; empty `scopes_supported`; a configured metadata URL leaves cached discovery alone.
- `coding/extension/mcp_auth_server_metadata_test.go`: authServerMetadataUrl validation table.
- `coding/mcpext/oauth_metadata_refresh_test.go`: the refresh uses the configured metadata URL; OAuthSettings carries it; legacy state replaces a `null` entry.
- `coding/mcpext/oauth_signin_internal_test.go`: `iss` from a pasted redirect URL; the `/mcp login` TUI hyperlinks (index.ts loginCommand, #10186).
- `coding/session_pending_tools_test.go`: pending tools survive a reload and interactive mode's ReapplyActiveTools, not a deactivating loadout.

## Not in this branch

- `packages/coding-agent/src/extensions/mcp/index.ts` servers-section intro (only the reaches in use): part of the codemode prompt change, left to c1-codemode. Its upstream-sync row stays pending.
- `test/mcp-conformance/` (npx conformance runner): see p1-mcp-oauth PROGRESS.
- Generated files (`pig-go.json`, coverage) are left for the integrator per lane rules; `make interface-go` regenerates them cleanly.

## Gates

`go vet` (linux and GOOS=windows) and golangci-lint on `mcp/...`, `coding`, `coding/mcpext`, `coding/extension`, `internal/codingagent`, `extensions/sdk`: clean. `go test -race -count=3` on `mcp/...`, `coding/mcpext` and the session MCP tests: green. `coding`, `cmd/pig` and `internal/codingagent` show only failures that the base commit shows too (oracles recorded from upstream 0.99.2, Node-comparison tests). `make ci-drift`/`ci-contracts` fail only on pre-existing 1.0.0 pin findings (divergence-guard markers on removed harness files, docs-drift claim, correspondence gaps, sdk-surface generateImages, pending hot-path tests of other areas); `make upstream-delta` and `make test-inventory` accept the rows this branch adds.
