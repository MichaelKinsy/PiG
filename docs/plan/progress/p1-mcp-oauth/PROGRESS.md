# p1-mcp-oauth: Pi 1.0.0 MCP OAuth test port (phase 1, tests only)

Area: MCP OAuth. This covers `authServerMetadataUrl`, the RFC 9207 `iss` check, per-server credentials, step-up scopes, and empty or `null` OAuth fields read as absent. The changed MCP tests in the same files are also ported here: list pagination with an empty `nextCursor`, and MCP tools after resume and `/reload`.

Upstream: `.upstream/v1.0.0` (tag a13d35a74) against `.upstream/v0.99.2`. The branch starts at the 1.0.0 pin commit `8669fa301` on `porter/pi-0.99.1`. `.upstream/current` links to `v1.0.0`.

Phase 1 rule: no product fixes and no loosened tests. Production edits are signature stubs that keep the 0.99.2 behavior. The new tests compile and fail until the fixes land.

## Upstream test inventory (v0.99.2..v1.0.0, this area)

| Upstream test file | Change | Case | Go test | Status |
|---|---|---|---|---|
| `mcp/test/oauth.test.ts` | changed | :94 discovers, registers, authorizes with PKCE, and refreshes on 401. New inputs: `client_secret: ""` in the registration, `scope: ""` in the code exchange response, `refresh_token: ""` and `expires_in: null` in the refresh response, and `scope=""` in the 401 challenge. The final token set must equal `{refreshed-token, refresh-token, Bearer, scope "org:read"}`. | `mcp/oauth/oauth_test.go#TestMCPOAuthDiscoversRegistersAuthorizesWithPKCEAndRefreshesOn401` | ported, red |
| same | changed | :280 shares one refresh between concurrent 401s. The protected resource metadata now names `authorization_servers: ["not a url"]`, and discovery must fall back to the server origin. | `...#TestMCPOAuthSharesOneRefreshBetweenConcurrent401sWhenRefreshTokensRotate` | ported, green. Go `ParseProtectedResourceMetadata` already rejects `not a url` with `Invalid URL`, and `DiscoverOAuthServerInfo` falls back to the origin (`mcp/oauth/discovery.go:235-255`). A probe confirmed this. |
| same | changed | :349 asks for authorization instead of refreshing when the server needs more scope. The granted token now has `scope: "repo read:org"`, and the expected scope is `"repo read:org admin"`. | `...#TestMCPOAuthAsksForAuthorizationInsteadOfRefreshingWhenTheServerNeedsMoreScope` | ported, red |
| same | new | :432 uses a configured authorization server metadata document as is (#10172), and an http metadata URL fails with `OAuthInsecureEndpointError` | `...#TestMCPOAuthUsesAConfiguredAuthorizationServerMetadataDocumentAsIs` | ported, red |
| same | new | :469 exchanges a code only when its iss parameter names the authorization server (RFC 9207) | `...#TestMCPOAuthExchangesACodeOnlyWhenItsIssParameterNamesTheAuthorizationServer` | ported, red |
| `mcp/test/client.test.ts` | changed | :94 paginates tools. The last page ends with `nextCursor: ""`. | `mcp/client_test.go#TestClientPaginatesToolsAndPreservesProtocolToolDefinitions` | ported, red |
| `coding-agent/test/mcp-oauth-refresh.test.ts` | changed | :52 and :75 use `forServer("test", server.url)` | `coding/mcpext/oauth_refresh_test.go#TestMCPOAuthRefresh*` (2 cases) | ported, green (the name argument is a stub) |
| same | new | :111 rejects an authorization response from another issuer | `...#TestMCPOAuthSignInRejectsAnAuthorizationResponseFromAnotherIssuer` | ported, red |
| same | new | :115 keeps the granted scope when the server asks for more | `...#TestMCPOAuthSignInKeepsTheGrantedScopeWhenTheServerAsksForMore` | ported, red |
| same | new | :155 uses the configured authorization server metadata URL (#10172) | `...#TestMCPOAuthSignInUsesTheConfiguredAuthorizationServerMetadataURL` | ported, red |
| `coding-agent/test/mcp-oauth-store.test.ts` | new file | :17 keeps separate credentials for servers sharing a SERVER_URL (#10252) | `coding/mcpext/oauth_store_test.go#TestMCPOAuthCredentialStoreKeepsSeparateCredentialsForServersSharingAServerURL` | ported, red |
| same | new file | :30 moves credentials stored by SERVER_URL to the first server that loads them | `...#TestMCPOAuthCredentialStoreMovesCredentialsStoredByServerURLToTheFirstServerThatLoadsThem` | ported, red |
| same | new file | :46 signs out of credentials stored by SERVER_URL | `...#TestMCPOAuthCredentialStoreSignsOutOfCredentialsStoredByServerURL` | ported, green. Today the URL is the only key, so the legacy key is the current key. The test guards the legacy removal for the fix. |
| `coding-agent/test/mcp-extension.test.ts` | changed | :128 validates the OAuth callback URL, scope, and client name. Adds `metadata` (https `authServerMetadataUrl`, accepted) and `plainMetadata` (http, rejected with `oauth.authServerMetadataUrl must be an https URL`). | `coding/mcpext/config_test.go#TestMCPConfigValidatesTheOAuthCallbackURLScopeAndClientName` | ported, red |
| `coding-agent/test/suite/mcp-oauth-server.ts` | changed (helper) | `startOAuthMcpServer({ iss })` sends `iss` in the authorization redirect | `coding/mcpext/oauthserver_test.go#startOAuthMcpServer(t, oauthMcpServerOptions{Iss})` | ported |
| `coding-agent/test/suite/agent-session-mcp.test.ts` | changed | :905 "servers registered by extensions" setup binds with `uiContext: createTestUiContext()` | `coding/mcp_session_registered_upstream_test.go#(*mcpSession).bind` | ported, green (4 cases unchanged) |
| same | new | :1053 declares tools tool_search loaded again on resume once their server connects | `coding/mcp_session_resume_upstream_test.go#TestAgentSessionMCPToolsAfterResumeAndReloadDeclaresToolsToolSearchLoadedAgainOnResumeOnceTheirServerConnects` | ported, red |
| same | new | :1073 `it.each` "drops" restored tools when an extension sets the loadout before they register | `...#TestAgentSessionMCPToolsAfterResumeAndReloadRestoredToolsWhenAnExtensionSetsTheLoadoutBeforeTheyRegister/drops_...` | ported, green. Nothing is restored today, so the assertion "not active" holds. The case guards the fix from activating too much. |
| same | new | :1073 `it.each` "keeps" restored tools when an extension sets the loadout before they register | `.../keeps_...` | ported, red |
| same | new | :1095 does not activate restored tools that register after the next prompt starts | `...#TestAgentSessionMCPToolsAfterResumeAndReloadDoesNotActivateRestoredToolsThatRegisterAfterTheNextPromptStarts` | ported, green. This is the same guard as "drops". |
| same | new | :1110 declares tools tool_search loaded again after /reload | `...#TestAgentSessionMCPToolsAfterResumeAndReloadDeclaresToolsToolSearchLoadedAgainAfterReload` | ported, red |

All new and changed upstream test cases in this area are ported. The table has 21 rows: 14 red, 6 green and one helper row. 14 Go tests fail.

Harness notes:

- `harnessOptions.sessionManager` (`coding/session_recovery_test.go`) is the suite harness's `options.sessionManager`. A second harness opens the first harness's in-memory session to resume it.
- `(*mcpResumeSession).reload` runs `AgentSession.reload` (agent-session.ts:3612-3650) at the `coding` boundary. It emits `session_shutdown(reload)`, calls `ReloadSettings`, calls `ReloadExtensions` over freshly loaded built-ins, binds the UI context, emits `session_start(reload)` and calls `ReportUnhandledMcpServers`. It follows the steps of `cmd/pig/headless_reload.go` `reloadHeadless`. Go's reload lives in `cmd/pig`, so the `coding` package has no `Session.Reload`.
- The extension that sets the loadout calls `extension.FromContext(ctx).SetActiveTools` from `session_start`. It is the Go form of `pi.setActiveTools` in that handler.

Not ported (out of this area or not a vitest file):

- `coding-agent/test/mcp-conformance/` (new: `run.ts`, `client.ts`, `baseline.json`). This harness runs the official `@modelcontextprotocol/conformance@0.2.0-alpha.11` suite through `npx` and needs network access. It is not a vitest file and is not in `upstream-tests-v1.0.0.json`. A Go port needs a decision on driving `pig`'s MCP client from the conformance runner. Follow-up for the lead.
- `coding-agent/test/oauth-selector.test.ts`, `ai/test/{anthropic-oauth,oauth-callback-server}.test.ts`: `/login` and Anthropic OAuth (p1-login-fixes).

## Upstream changes without an upstream test

| Behavior | Upstream source | Status |
|---|---|---|
| `stepUpScope(granted, challenged)` exported | `mcp/src/oauth/flow.ts:270-279`, `index.ts:34` | Covered indirectly by the step-up cases. No Go signature stub was added; the fix lane chooses the Go form for `undefined`. |
| `OAuthIssuerMismatchError` message `received none` when `iss` is missing | `mcp/src/oauth/errors.ts:15-22` | No test asserts the message. The Go `Received` field is `string`. |
| `nextCursor: null` ends pagination | `mcp/src/client.ts:103-106` | The upstream test covers only `""`. |
| `optionalStrings` accepts `null` | `mcp/src/oauth/types.ts:116` | No test |
| `/mcp login` prints the URL as a hyperlink plus a short `Ctrl+click to open` link in TUI mode (#10186) | `coding-agent/src/extensions/mcp/index.ts:929-937` | No upstream test. This is not in the named area, so it is left to the misc lane. Go: `coding/mcpext/command.go:268`. |
| MCP servers section intro names only the reaches in use | `coding-agent/src/extensions/mcp/index.ts:158-166` | This is a codemode prompt change, so it is left to p1-codemode. |

## Signature stubs (production, 0.99.2 behavior)

- `mcp/oauth/flow.go` `OAuthFlowOptions`: adds `Iss *string` and `AuthorizationServerMetadataURL *url.URL`. `AuthorizeMcp` ignores both.
- `mcp/oauth/types.go` `AuthorizationServerMetadata`: adds `AuthorizationResponseIssParameterSupported *bool` (`authorization_response_iss_parameter_supported`). `encoding/json` fills it, and the flow never reads it.
- `coding/mcpext/oauth.go` `McpOAuthSettings`: adds `AuthServerMetadataURL *url.URL`. Nothing sets or reads it.
- `coding/mcpext/oauth.go` `McpOAuthCredentialStore.ForServer(name, serverURL)`, `Tokens(name, serverURL)` and `Remove(name, serverURL)` take the server name and ignore it. Every production call site passes the server name: `cli.go` logout and login, `runtime.go` `NewConnection`, and `extension.go` `storedTokens`, `storedTokensLocked`, `SignIn` and `SignOut`. Existing PiG tests pass `"test"`.

## Failure list by suspected root cause

1. **Empty or `null` optional OAuth fields are rejected instead of read as absent (#10266).** `optionalString` and `optionalURL` call `requiredString`, which rejects `""` (`mcp/oauth/types.go:188, 232`). `ParseClientInformation` therefore fails on `client_secret: ""` with `Invalid client_secret`. Pi 1.0.0 adds `absent()` (`types.ts:105-108`) and reads `expires_in: null` as absent (`types.ts:181`). Two related parts of the same case are unverified: `ParseWWWAuthenticate` must read `scope=""` as absent (`discovery.ts:35`), and the scope fallback must use `||` (`flow.ts:310-312`, Go `mcp/oauth/flow.go:556-559`).
   - `TestMCPOAuthDiscoversRegistersAuthorizesWithPKCEAndRefreshesOn401`: the connect fails with `Invalid client_secret`. The later assertions are not reached: the requested scope `org:read`, the empty `refresh_token` and `null` `expires_in` in the refresh response, and the final token set.
2. **Granted scope is not recorded, and step-up requests only the challenged scopes.** Pi 1.0.0 saves `withScope(tokens, scope)` after the code exchange and the refresh (`flow.ts:266-268, 353, 364`). `adaptOAuthProvider` requests `stepUpScope(granted.scope, challenge.scope)` on `insufficient_scope` (`flow.ts:418-427`), and `signInMcpServer` merges the configured scope with `stepUpScope` (`oauth.ts:412, 441-448`). Go has none of these: `mcp/oauth/flow.go` near `:676` and `:752`, `coding/mcpext/oauth.go` near `:790`.
   - `TestMCPOAuthAsksForAuthorizationInsteadOfRefreshingWhenTheServerNeedsMoreScope`: requests `repo admin`, want `repo read:org admin`.
   - `TestMCPOAuthSignInKeepsTheGrantedScopeWhenTheServerAsksForMore`: the stored `scope` is `""`, want `issues:read`.
   - The final `scope: "org:read"` assertion of case 1 also depends on this cause.
3. **`authServerMetadataUrl` is not supported (#10172).** Five parts are missing. `discoverOAuthServerInfo` does not load a configured metadata document (`discovery.ts:145-154`). `runFlow` neither requires https (`secureEndpoint`) nor skips the discovery cache for it (`flow.ts:282-307`). `validateOAuth` in `core/mcp-servers.ts:150-156` has no Go counterpart (`coding/extension/mcp_servers.go:384`). `McpServerConnection.oauthSettings` does not map the setting (`runtime.ts:245`). `signInMcpServer` and `createMcpAuthProvider` do not pass it (`oauth.ts:282, 441`).
   - `TestMCPOAuthUsesAConfiguredAuthorizationServerMetadataDocumentAsIs`: the authorization endpoint is `/authorize` from discovery, want `/idp/authorize`.
   - `TestMCPOAuthSignInUsesTheConfiguredAuthorizationServerMetadataURL`: sign-in succeeds, want `HTTP 404 loading authorization server metadata`.
   - `TestMCPConfigValidatesTheOAuthCallbackURLScopeAndClientName`: `plainMetadata` is accepted, want rejection with `oauth.authServerMetadataUrl must be an https URL, or http on localhost, 127.0.0.1, or [::1]`.
4. **No RFC 9207 `iss` check.** `runFlow` does not compare `options.iss` with `metadata.issuer` before the code exchange when `iss` is present or the metadata sets `authorization_response_iss_parameter_supported` (`flow.ts:340-344`). `signInMcpServer` does not pass the callback or pasted-URL `iss` (`oauth.ts:337-365, 456-457`). The Go callback server already captures `iss` (`mcp/oauth/callback.go:235`). Its `OAuthCallback.Iss` is a `string`, so the fix must map "absent" to a nil `OAuthFlowOptions.Iss`. `waitForAuthorizationCode` returns only the code (`coding/mcpext/oauth.go:663`).
   - `TestMCPOAuthExchangesACodeOnlyWhenItsIssParameterNamesTheAuthorizationServer`: `other` is exchanged, want `OAuthIssuerMismatchError`.
   - `TestMCPOAuthSignInRejectsAnAuthorizationResponseFromAnotherIssuer`: sign-in succeeds, want `OAuthIssuerMismatchError`.
5. **Credentials are keyed by URL only (#10252).** Pi 1.0.0 keys the state `mcp__<namespace>|<url>` (`oauth.ts:117-123`). The first server that loads a URL-only legacy entry takes it over. `tokens()` reads the legacy entry without taking it over, and `remove()` removes the legacy entry (`oauth.ts:141-205`). Go `serverKey` uses the URL only (`coding/mcpext/oauth.go:198`).
   - `TestMCPOAuthCredentialStoreKeepsSeparateCredentialsForServersSharingAServerURL`: `work` loads `personal-token`.
   - `TestMCPOAuthCredentialStoreMovesCredentialsStoredByServerURLToTheFirstServerThatLoadsThem`: `personal` loads `legacy-token`.
   - With the fix, the PiG test `coding/mcpext/review_regression_test.go#TestMcpOAuthCredentialStoreKeepsServerOrderAndJSONStringifyEscapes` must expect the new keys. It asserts `"https://z.example/mcp"` as a top-level key.
6. **An empty `nextCursor` is treated as a cursor.** Pi 1.0.0 reads `nextCursor: ""` and `null` as the end of pagination (`client.ts:103-106`). Go stores `""` and reports `MCP tools/list returned duplicate cursor:` (`mcp/client.go:258-262`).
   - `TestClientPaginatesToolsAndPreservesProtocolToolDefinitions`
7. **Restored tools that register later are dropped (pending tools).** Pi 1.0.0 keeps `_pendingToolNames`. They are set from the transcript on restore and from the active tools on `reload()`. `_buildRuntime` activates them when they register. They are cleared when a loadout deactivates a tool or the next agent run starts (agent-session.ts:431, 1489-1501, 1762-1782, 3541-3544, 3631-3632). The Go `Session` has no pending set. A second suspected cause is that `NewSession` does not call `restoreToolsFromTranscript` at construction (agent-session.ts:497). The only Go call site is `NavigateTree` (`coding/session.go:2674`). This is not yet confirmed separately, because both causes give the same failure.
   - `TestAgentSessionMCPToolsAfterResumeAndReloadDeclaresToolsToolSearchLoadedAgainOnResumeOnceTheirServerConnects`: `mcp__docs__search` never becomes active.
   - `TestAgentSessionMCPToolsAfterResumeAndReloadRestoredToolsWhenAnExtensionSetsTheLoadoutBeforeTheyRegister/keeps_...`: active tools are `[read bash edit write tool_search]`.
   - `TestAgentSessionMCPToolsAfterResumeAndReloadDeclaresToolsToolSearchLoadedAgainAfterReload`: `mcp__docs__search` never becomes active after reload.

## Ledgers and gates

- `test/parity/interfaces/test-mapping-v1.0.0.json` keeps `mcp/test/{oauth,client}.test.ts`, `coding-agent/test/{mcp-extension,mcp-oauth-refresh,mcp-oauth-store}.test.ts` and `suite/agent-session-mcp.test.ts` as `pending`. The ports are red, so the files are not closed.
- `test/parity/interfaces/pig-go.json` changes for the three `McpOAuthCredentialStore` signatures. It was regenerated locally (`interface-go-drift: clean`) and restored before the commit, per the lane rules; the integrator regenerates it.
- `go vet ./...`, `GOOS=windows go vet ./mcp/... ./coding/mcpext/ ./coding/` and `go tool golangci-lint run ./mcp/... ./coding/mcpext/ ./coding/` are clean.
- `make -k ci-contracts` fails only on pin-move fallout outside this area. These failures are the correspondence-check `quietStartup` and `tuiMode` findings, the `test-porting-release` and `known-gaps-drift` pending hot-path files from every Pi 1.0.0 lane, the `sdk-surface-drift` `ctx.modelRegistry.generateImages`, and the `interface-go-drift` that the restored `pig-go.json` causes.
- `go test ./coding/ ./cmd/pig/ ./internal/codingagent/` reports failures outside this area: oracles still recorded from upstream 0.99.2 (`rpc33_*`, whose oracle-pin check asks for a fresh 1.0.0 oracle), and the `...ComparedWithPi` tests in `cmd/pig`, which need `extensions/sdk-ts/node_modules`. This worktree has no `node_modules`.
