# lg-help-1: library packages env, server, chord, client, telemetry, protocol — rule requests for lg-rules

Rows are the non-`child-gap` rows of `build/interface-gaps/gaps.tsv` (integrate-042 + lg-decide). With the P1 decision (LEAD-DECISION-P1.md, rule (b) for library exports) and the three rules below, almost every row closes. Counts per package (P1 / Context S4 / Error fields / other):
env 94/25/12/6 · server 136/16/12/4 · chord 165/19/9/65 · client 58/0/5/14 · telemetry 45/1/0/32 · protocol 20/0/8/5.

## Ported
- `sshArguments(target, strictHostKeys = true, knownHostsFile = target.knownHostsFile)` is exported in Pi's `env/src/index.ts`; Go had only the one-argument `SshArguments`. `env.SshArgumentsWith(target, strictHostKeys, knownHostsFile)` is now exported (it was the unexported implementation) and the host-key scan uses it (`ssh.go` `ScanHostKey`); `SshArguments` is the defaults call (rule R2 of lg-help-1-tui-rule-requests.md: base method + `With`). Existing test `TestSshArgumentsWith…`/`TestSshArgumentsDefaults…`.

## L1. chord `Context` is Go's leading `context.Context` (S4: env 25, server 16, chord 19, telemetry 1)
Same rule as durable D1 (`lg-help-1-durable-rule-requests.md`). `RemoteExecutionEnv.*` (absolutePath, appendFile, canonicalPath, cleanup, createDir, ... writeFile, watch), `RemoteWatcher.close`, server `Server.*` and chord `Service*` calls take `ctx` as Go's first parameter.

## L2. Language-mechanic rows (designed out, no Go reader)
- JavaScript `Error` fields `name`, `stack`, `cause` on every `extends Error` class (env: HostKeyChangedError, HostKeyUnknownError, RemoteError, SshError; server 12; chord 9; client 5; protocol 8): Go errors carry `Error()`/`Unwrap()`; the discriminating name is the Go type.
- `new XError(message, {cause})` constructors (N3: HostKeyChangedError, HostKeyUnknownError, RemoteServiceError, PathError, UnsafePathError, DisconnectedError, FrameError, InMemoryTelemetryContext): Go values are struct literals or `Errorf("%w")`; a `New…` function with no extra behaviour would be a caller-free duplicate.
- `Symbol.asyncDispose` (`Client`, `TelemetryAdapterFixture`): Go `Close()`/`Disconnect()`; `[Symbol.SERVICE_TYPE]`, `[Symbol.docType]` are TypeScript phantom brands.
- `chord/context.ts` helpers `BACKGROUND_CONTEXT`, `TODO_CONTEXT`, `awaitWithContext`, `createContextKey`, `withContextValue`: `context.Background()`, `context.TODO()`, a blocking call, a typed key and `context.WithValue` (stdlib).
- `MaybePromise<T>` (server): a Go call returns `(T, error)`.
- telemetry type-level helpers (`ExactTelemetryAttributes`, `Infer*Attributes`, `SchemaTelemetrySpan`, `TelemetrySchema*`, `TypedSpanStarter`): compile-time TypeScript schema inference; Go has `telemetry.TelemetrySpan` with explicit attribute maps.
- `startSpan(callback)` S5: the callback's result `T` is a generic `StartSpan[T]` function (D2).

## L3. Generic methods as package-level functions (chord member-missing 28)
`FacetEnvironment.{use,observe,provide,provideMany,replicatedState}` and `RemoteServiceProvider.{provide,replace,spawn,use,validateReplacement,withdraw}` are the generic functions `chord.Use[T]`, `Provide[T]`, `Replace[T]`, `Spawn[T]`, `ValidateReplacement[T]`, `Withdraw[T]`, `ProvideMany[T]` (internal/chord/provider.go:125-230, facets.go:474). Same request as durable D2.

## L4. Representation (T9/T10/U5/A3 rows)
`ServiceControlCall`, `ServiceMemberSnapshot`, `WireServiceMemberSnapshot`, `ServiceProviderUpdate`, `WireServiceProviderUpdate` (U5/T10d): Go uses sealed-interface variants whose tag is the Go type (`type`/`kind` is written by the encoder, `service.go`/`provider.go` `MarshalJSON`); `Op`/`WireOp`/`isBase`/`isReplace` (delta tuples) are `delta.Op` structs; `RemoteServiceBindingOptions.services` (`{id}[]`) is `[]string` ids; `RemoteServices.use/observe(service: Service<T>)` takes the `ServiceDefinition[T]` / id string; `BundleFacetsOptions.{define,minify,platform,target}` are esbuild options Pig's bundler does not expose (lg-help-1: Pi's bundler is esbuild-in-Node, Pig builds with Go esbuild defaults; request designed-out with D-number if the owner prefers a divergence).

## L5. mcp, codemode, agent (non-P1, non-held rows)
- `AuthProvider.onUnauthorized` (optional): Go `AuthProvider` is the required `Token`; the optional member is the one-method interface `mcp.UnauthorizedHandler` (auth_provider.go:35-39) that the HTTP transport asks for by type assertion (streamable_http.go:378-408). Same as tui R3 (optional member = small interface).
- Error constructors `McpOAuthAuthorizationRequiredError`, `OAuthError`, `OAuthInsecureEndpointError`, `OAuthIssuerMismatchError` (N3): struct literals; same as L2.
- `OAuthCallbackServer.waitForCallback` result `Promise<OAuthCallback>`: `*CallbackWait` with `Wait(ctx) (OAuthCallback, error)`.
- `JSON_RPC_ERROR_CODES`, `isJsonRpcRequest/Response/Notification`, `JsonRpcResponse`, `JsonRpcMessage`, `SupportedProtocolVersion`: a Go `JSONRPCMessage` struct with `Method`/`Result`/`Error` fields; the guards are field checks in `mcp/jsonrpc.go`. Rule: a TypeScript type guard over a union is the Go struct's field test (no caller-free guard functions).
- `CodemodeSandboxOptions.wasm` (`object | Promise<object>`): `SandboxOptions.Wasm func() ([]byte, error)`; `workerUrl`, `loadQuickJSWasm`, `CodemodeWasmModule`: Pi loads QuickJS in a Node worker; Pig embeds the engine (designed out; `docs/additive-features.md` codemode).
- `renderDeclarations`, `renderToolSample`, `schemaToType` and `CallToolResult.content/isError`: `held` rows (open residuals R1/R3 in gap-mcp-codemode-libs.md), owned by the mcp/codemode lanes.
- agent `Agent.*` getters and renames (afterToolCall, beforeToolCall, convertToLlm, finishTurn, getApiKey, prepareRequest, ...): `lg-tui-a` e970e32f2 ("Agent getters named as Pi") covers them; `Agent.clear*Queue`/`continue` return values (S5 upstream void, Go returns the dropped messages) are a Go addition callers use to restore the queue.
