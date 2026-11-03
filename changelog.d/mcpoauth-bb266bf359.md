### Added

- Added MCP OAuth sign-in in two halves to `mcp/oauth`, moved from the coding agent: `StartMcpSignIn` refreshes the stored tokens or returns the authorization URL, and `CompleteMcpSignIn` exchanges the code later, possibly in another request with the same state store. The code comes from a loopback callback server (`ListenForLoopbackSignIn`, `WaitForLoopbackSignIn`), the application's own callback route, or a pasted redirect URL (`ResponseFromRedirectURL`). Client ID Metadata Documents (`ClientMetadataDocuments`) and the client name are the caller's. `CreateMcpAuthProvider` refreshes tokens once across requests and, through `McpOAuthServerStore.WithRefreshLock`, across processes. `OAuthFlowOptions.RefreshOnly` makes a failed refresh report `REDIRECT` without registering a client or replacing a pending sign-in's PKCE verifier and state. The coding agent's `SignInMcpServer` is a thin wrapper over them.

### Fixed

- A failed token refresh of an MCP connection no longer registers an OAuth client or replaces the PKCE verifier of a sign-in in progress, which made that sign-in fail. MCP sign-in now refreshes the stored tokens with the stored client even when the loopback callback port changed, instead of registering a new client and opening the browser.
