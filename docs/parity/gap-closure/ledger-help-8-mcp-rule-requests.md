# mcp ledger rows (lg-help-8): ported family and rule requests

State: integrate-042 merged, `make interface-gaps` 140 mcp rows, about 50 of them `not-exercised` under P1 (library package; closes with p1-impl's rule plus Pi-cited mutation-checked tests, none written here).

## Ported (one family, one table-driven test)
`mcp/oauth` error classes of errors.ts: `NewOAuthError(code, message, errorURI...)`, `NewOAuthIssuerMismatchError`, `NewOAuthInsecureEndpointError`, `NewMcpOAuthAuthorizationRequiredError`, each with `Name()`; the flow, discovery and mcpext sites use them. `TestOAuthErrorConstructorsMatchErrorsTs` cites errors.ts:6-54 and is mutation-checked (a wrong `Name()` and a dropped code fallback fail it). The `NewOAuthError` signature equals lg-ca-member-b's, so merging its branch is a no-op on this symbol.

## Rule requests
- D103: `stack` rows of McpError, McpAbortError, McpConnectionClosedError, McpTimeoutError, McpAuthRequiredError, McpHttpError, McpSessionExpiredError, OAuthError, OAuthInsecureEndpointError, OAuthIssuerMismatchError, McpOAuthAuthorizationRequiredError close by the D103 rule; `cause` is `Unwrap()` (these classes never set one).
- Type guards: `isJsonRpcRequest`, `isJsonRpcResponse`, `isJsonRpcNotification` -> `JSONRPCMessage.IsRequest/IsResponse/IsNotification` (mcp/jsonrpc.go:138-148). `JsonRpcResponse` (a union member of `JsonRpcMessage`) is the `JSONRPCMessage` with `Result` or `Error` set; `JsonRpcId`/`JsonRpcMessage` unions (A8) are the tagged structs `JSONRPCID`/`JSONRPCMessage`.
- `JSON_RPC_ERROR_CODES` (const object) -> the const group `JSONRPCParseError` ... `JSONRPCInternalError` (mcp/jsonrpc.go:251).
- `SupportedProtocolVersion` (`(typeof SUPPORTED_PROTOCOL_VERSIONS)[number]`) -> `mcp.SupportedProtocolVersion` string type with `SupportedProtocolVersions` (mcp/types.go:16-19).
- `AuthProvider.onUnauthorized?` (optional method): Go keeps the required `token` method in `AuthProvider` and the optional one in the consumer-owned `UnauthorizedHandler` (mcp/auth_provider.go:39, called at streamable_http.go:408); rule: optional interface member -> optional-method interface. `UnauthorizedContext.fetch(url, init)`: the Go `Fetch` is a `McpFetch` func taking a request (S4 init folded into the request).
- `McpClient.request/notify/callTool` `params`/`args` (`Record<string, unknown>`): `any`/`json.RawMessage` (T6 record against any).
- `McpFetch` (function alias) -> `mcp.McpFetch` func type (A0). `ContentBlock` discriminator `type` -> Go struct with string `Type` (U5, wire-closed by the decoder).
- oauth: `AddClientAuthentication(url: string | URL)` -> `*url.URL`; `OAuthCallbackServer.waitForCallback` result `OAuthCallback` -> `CallbackWait`; `OAuthClientInformationMixed` -> `OAuthClientInformation` (the full record embeds it).
## Held (real, not done here)
- `CallToolResult.content`/`isError` (R1 of gap-mcp-codemode-libs.md): `ContentBlock` must decode leniently and keep member presence; a separate change with its own oracle.
