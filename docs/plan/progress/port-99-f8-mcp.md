# Lane port-99-f8-mcp: native MCP in Go (family 8a)

Lane branch `port-99-f8-mcp`, from `porter/pi-0.99.1` (40001cd42). Scope: plan Phase 2 step 8, D-B and D-H, the MCP part. Upstream sources: `.upstream/v0.99.1/packages/mcp` and `packages/coding-agent/src/{extensions/mcp,core/mcp-servers.ts}`.

Owner update: the release-age cooldown was lifted for the upstream 0.99.1 `@earendil-works/*` packages only. They were installed for probing in a scratch directory outside the repository (`npm pack` of each pinned package with a per-command override, then a normal install of the tarballs). No global config was edited.

## Design

| upstream | Go | notes |
|---|---|---|
| `packages/mcp/src` (client, protocol, transports) | `mcp/` | `Client`, `Transport`, `StdioTransport`, `StreamableHTTPTransport`, `ConsumeSSEStream`, `ToLLMContent` |
| `packages/mcp/src/oauth` | `mcp/oauth/` | discovery, PKCE flow, registration, refresh, `McpOAuthProvider`, `OAuthCallbackServer` |
| `packages/mcp/src/testing` | `mcp/mcptest/` | `NewInMemoryTransportPair` |

Async mapping: an awaited Promise is a blocking call; an `AbortSignal` is a `context.Context`; each intentionally unawaited Promise (`transport.send(...).catch`, `void handleRequest`, the SSE readers) is a goroutine the client or transport owns and drains on `Close`. Timers use an injectable source so the progress-renewal test runs on a virtual clock, as upstream's fake timers do.

## Red

`test(mcp): port upstream 0.99.1 packages/mcp tests with signature stubs (red)`. Every exported function and method is a `panic("not implemented")` stub; data types are real. Each test was run alone (a stub panic aborts a whole test binary).

| package | upstream test file | cases | red |
|---|---|---:|---|
| `mcp` | `client.test.ts` | 12 | 12 fail |
| `mcp` | `content.test.ts` | 2 | 2 fail |
| `mcp` | `stdio.test.ts` | 2 | 2 fail |
| `mcp` | `streamable-http.test.ts` | 13 | 13 fail |
| `mcp/oauth` | `oauth.test.ts` | 7 | 7 fail |

Total 36 upstream cases ported, 36 red, 0 green. Departures from the upstream tests, each forced by the language:

- The stdio fixtures `stdio-server.mjs` and `stubborn-server.mjs` run as the test binary itself (`TestMain` switches on `MCP_TEST_FIXTURE`). Their behavior is line for line the same.
- `renews the timeout on progress`: vitest's fake timers become a virtual clock injected through `SetClientAfterFuncForTest`. `advanceTimersByTimeAsync` flushes microtasks between timers; the notification crosses goroutines here, so the test waits for `onProgress` before the second advance (`client.test.ts` awaits both advances).
- Objects compared with `toEqual` are compared as JSON values, so key order is not asserted, as in vitest.

## Green (`mcp` package)

`feat(mcp): implement the upstream 0.99.1 packages/mcp client, transports and OAuth (green)`. The ported tests are unchanged since the red commit, except lint-only edits (unchecked `Fprintf`/`Close` results, `nolint:bodyclose` on helpers that close the body) and one helper removed by `go fix`. 36 upstream cases pass; 7 regression tests were added (see below).

Bugs the tests or load runs found in the first implementation:

- `InMemoryTransport.Send` delivered on a goroutine, so `notifications/initialized` was not yet visible to the server when `connect()` returned (upstream `client.test.ts` "initializes the connection" reads it right after `await connect()`). Upstream's `queueMicrotask` runs before the sender's next await continues. `Send` now returns after the receiving listeners ran. Found by `-race -count=24` under CPU burners on 4 pinned cores.
- The test fixture `sleep` used a bare `select {}`, which ends a Go process at once (deadlock exit). That made "kills a server that ignores shutdown, including its children" pass vacuously. Found by mutation. Fixtures now sleep in a loop.
- The race runtime sleeps one second at process exit (`GORACE atexit_sleep_ms`), which let the transport's SIGTERM stage fire before the fixture's own exit. Fixtures run with `atexit_sleep_ms=0`.

Regression tests added (not upstream): `TestStdioTransportTerminatesTheChildrenOfAServerThatExitsOnStdinClose`, `TestStdioTransportSendsSIGTERMAfterTheStdinGracePeriod`, `TestRefreshAuthorizationKeepsTheOldRefreshTokenWhenTheServerDoesNotRotateIt`, `TestTokenRequestsRefuseNonLoopbackHTTPEndpoints`, and two `orderedjson` tests.

Mutation checks (revert, see red, restore), all killed: remove the "token already replaced" check of `AdaptOAuthProvider`; drop `Last-Event-ID`; drop the timer generation check on progress renewal; drop `notifications/cancelled`; keep the old refresh token; drop the SIGTERM stage; drop the SIGKILL stage; drop the final group SIGTERM after the server exits; drop the wait for delivery in the in-memory transport.

Load: `go test -race -count=24` with `GOMAXPROCS=4` and `taskset -c 0-3` under six CPU burners on the same cores, for `./mcp` and `./mcp/oauth`: pass. `-count=60` for the client and stdio tests: pass.

Notes: `KillLiveProcessGroups()` is upstream's process `exit` hook (`stdio.ts` `liveProcessGroups`). Go has no exit hook, so the host must call it when it exits without closing its transports; the wiring belongs to the extension host family. `mcp/oauth` attribution is in `THIRD_PARTY_NOTICES.md`.

## Tranche 2: the built-in MCP extension (`coding/mcpext`)

Design. `coding/mcpext` ports `packages/coding-agent/src/extensions/mcp/{config,log,oauth,runtime,tools,resources,index}.ts`; `coding/extension/mcp_servers.go` ports `core/mcp-servers.ts` (config types and validation, exposure resolution, `McpServerRegistry`). The extension does not import the runner. It talks to a five-method `Host` interface (`RegisterTool`, `GetAllTools`, `GetActiveTools`, `SetActiveTools`, `GetMcpServers`) and exposes the upstream event handlers as methods (`SessionStart`, `BeforeAgentStart`, `TurnStart`, `McpServersChange`, `SessionShutdown`). `Factory(options)` subscribes those handlers on an `mcpext.API` (`extension.API` plus `GetMcpServers` and `OnMcpServersChange`). Boundary for Piglet stripping (issue #92): nothing outside `coding/mcpext` imports it; the registration seam (family 6) is one file, guarded by `//go:build !pig_strip_mcp`, that calls `Factory`. Runtime disable is "do not call `Factory`". `pig-mcp-adapter` retirement is in the migration note at the end.

Not in this lane, by the task: the `/mcp` slash command, its terminal manager (`ui.ts`), and the `pi mcp` CLI (`cli.ts`). The extension exposes what they use: `Servers`, `Notices`, `DescribeState`, `Subscribe`, `SignIn`, `SignOut`, `Reconnect`, `SetEnabled`, `SetExposure`, `FormatStatus`, `EnsureDiscoveryActive`, `Pending`, and the config editors `UpdateMcpServerConfig`, `AddMcpServerConfig`, `RemoveMcpServerConfig`. Tool renderers (`renderCall`, `renderResult`) are the generic tool-call rendering of the interactive family.

Types added to other families' files, signature-only in intent (the owning families take them over): `extension.ToolExposure`, `ToolNamespace`, `ToolAnnotations` and the `Exposure`, `Namespace`, `Annotations`, `OutputSchema` fields on `extension.ToolDefinition` and `ToolInfo` (family 6, upstream `types.ts:509-596`); `agent.AgentToolResult.StructuredContent` (family 4, upstream `agent/src/types.ts`); `tools.TruncateMiddle` (upstream `core/tools/truncate.ts`, needed by `limitMcpContent`).

### Red (tranche 2)

`test(mcpext): port upstream 0.99.1 built-in MCP extension tests with signature stubs (red)`. Exported functions and methods are `panic("not implemented")` stubs, types are real. Every test was run alone.

| upstream test file | cases | Go tests | red |
|---|---:|---:|---|
| `mcp-extension.test.ts` (config 4, tools 5, connections 9) | 18 | 18 (the multi-byte case is in `internal/codingagent/tools`) | 18 fail |
| `mcp-oauth-refresh.test.ts` | 2 | 2 | 2 fail |
| `suite/agent-session-mcp.test.ts`, MCP side | 16 of 24 | 17 (`it.each` gives two) | 17 fail |
| regression guards (not upstream) | - | 6 | 6 fail |

Not ported here, with the owner:

- `agent-session-mcp.test.ts` "exposes codemode-only MCP tools through codemode", "keeps codemode-deferred ... callable from codemode", "finds tools from scripts with searchTools() and describeTool()", "activates tool_search for deferred MCP tools ...", "finds nothing to load ...": they run codemode scripts and `tool_search`, family 8b.
- "keeps codemode-only MCP tools callable across tree navigation", "rejects direct model calls to codemode-only MCP tools": session tool exposure and `navigateTree`, family 6.
- "rejects names another extension registered", "reports registered servers when no extension connects them": `registerMcpServer` ownership and `reportUnhandledMcpServers` in the runner, family 6. `McpServerRegistry` (mine) has its own guard test.
- `agent-session-mcp-oauth.test.ts` (5) and `mcp-command.test.ts` (8): `/mcp` command and `pi mcp` CLI, families 9 and 7. The test server they share (`mcp-oauth-server.ts`) is ported in `coding/mcpext/oauthserver_test.go`.
- The cases upstream drives through a whole AgentSession are ported at the nearest caller boundary: the extension's event methods against a host that behaves like the runner (`fakeHost`), calling the registered tools' `Execute`. The transcript-level assertions (`declaredToolNames` from `toolsAdded`) become assertions on the active tool set.

### Green (tranche 2)

`feat(mcpext): implement the built-in MCP extension, MCP server registry and TruncateMiddle (green)`. The ported tests are unchanged except lint-only edits (unused imports and helpers, `nolint` with a reason on helpers that close bodies) and the loopback-URI guard, which gained cases after a probe (below). Result: 18 + 2 + 17 ported tests and 10 regression tests pass, `-race`, `go vet ./...`, `GOOS=windows`/`darwin` vet, lint 0 issues, `go fix -diff` empty.

Probed against the installed upstream 0.99.1 package (kept outside the repository): tool names with astral characters (JavaScript regexps replace per UTF-16 code unit, so `😀` becomes two underscores), log formatting (`JSON.stringify` normalizes numbers and escapes), `isLoopbackRedirectUri` (an empty `?` or `#` counts as empty, the host is case-insensitive), config load errors (V8's `JSON.parse` messages), config rewrite (`JSON.stringify` with the file's indentation), `createMcpToolDefinition` (parameter schema merge keeps key order, null `type` becomes `object` in place, description and label fallbacks, boolean-only annotations) and `convertMcpResult`. Three real differences were found and fixed (loopback URI, log data normalization, config rewrite normalization); the rest matched on the first run and are kept as regression tests.

Bugs found and fixed in the first implementation:

- `SessionShutdown` waited up to the 60 s request timeout for a server that never answered `initialize`. A connection now aborts a connect in flight when it closes (`TestSessionShutdownReturnsWhileAServerIsStillConnecting`).
- Sign-in showed the authorization URL before registering the callback wait, so a browser that followed the redirect at once reached the loopback server before the wait existed and sign-in hung. Found by `-race -count=24` under CPU burners. The wait is now registered first (`TestSignInRegistersTheCallbackWaitBeforeShowingTheAuthorizationURL`). Upstream registers synchronously inside `waitForAuthorizationCode`, before any network I/O can complete.
- `CreateMcpToolName` sliced past the end of a short taken name (JavaScript `slice` clamps). Found by the ported "names that sanitize to one already taken" case.

Mutation checks (revert, see red, restore), all killed: the L5 disconnect handling of a dead stdio server, `McpSessionExpiredError` detach and retry, abort of a connect on close, `~` expansion, the "other extension's codemode tool" check, re-registering withdrawn tools as hidden, the token count rounding, the "token already replaced" check of the refresh, the callback wait order. One survivor is an equivalent mutant: dropping `c.client = nil` in `handleClientClose` changes nothing observable, because `GetClient` also checks the client's state, so a closed client is never returned (L5 holds by that check).

Load: `go test -race -count=24` under six CPU burners pinned to cores 0-3 with `GOMAXPROCS=4`, for `coding/mcpext`, `mcp` and `mcp/oauth`: pass, after the sign-in fix.

Also in this tranche:

- `test/parity/interface-extractor`: `packages/mcp` joins `TRACKED_PACKAGES` as an optional package (`OPTIONAL_PACKAGES`), because upstream added it in 0.99.0: the 0.87.1 mirror has no `packages/mcp`, so a required entry would break the current generators until the pin moves. With the 0.99.1 mirror the source inventory has five packages (`mcp` has three entrypoints: `.`, `./oauth`, `./testing`); with the 0.87.1 mirror it is unchanged. `extractInventory` without an explicit package list now means "every tracked package this release has". Extractor tests: 54 pass (one new test for the optional package).
- `docs/design/builtin-mcp.md`: the boundary for Piglet stripping and the plan and migration note for retiring `pi-mcp-adapter` (not deleted).
- `changelog.d/port-99-f8-mcp-native-mcp.md`.

### Proposed PORT_MAP rows and ledgers (the lead adds them at the pin move; `port-map-drift` reads `.upstream/current`, which is still the previous release)

| upstream file | Go | status |
|---|---|---|
| `packages/mcp/src/client.ts` | `mcp/client.go` | partial (server sampling, tasks, batches out of upstream scope too) |
| `packages/mcp/src/protocol/{jsonrpc,types,content}.ts` | `mcp/jsonrpc.go`, `mcp/types.go`, `mcp/content.go` | ported |
| `packages/mcp/src/transports/{transport,in-memory,stdio,streamable-http}.ts`, `auth-provider.ts` | `mcp/transport.go`, `mcp/mcptest/inmemory.go`, `mcp/stdio*.go`, `mcp/streamable_http.go`, `mcp/sse.go`, `mcp/auth_provider.go` | ported |
| `packages/mcp/src/oauth/{types,errors,discovery,flow,provider,callback,index}.ts` | `mcp/oauth/*.go` | ported |
| `packages/mcp/src/index.ts`, `testing/index.ts` | `mcp/doc.go`, `mcp/mcptest` | n/a (barrels) |
| `packages/coding-agent/src/core/mcp-servers.ts` | `coding/extension/mcp_servers.go` | ported |
| `packages/coding-agent/src/extensions/mcp/{config,log,oauth,runtime,tools,resources}.ts` | `coding/mcpext/*.go` | ported (renderers deferred) |
| `packages/coding-agent/src/extensions/mcp/index.ts` | `coding/mcpext/extension.go`, `register.go` | partial: `/mcp` command handler and manager to family 9 |
| `packages/coding-agent/src/extensions/mcp/{cli,cli.lazy,runtime.lazy}.ts`, `ui.ts` | (family 7/9, lazy wrappers designed out) | not started / n/a |
| `packages/coding-agent/src/core/tools/truncate.ts` (`truncateMiddle`) | `internal/codingagent/tools/truncate.go` | part of the existing row |

Upstream test files and their Go counterparts (hashes are those of `.upstream/v0.99.1` and are recorded by the central test-mapping regeneration): `packages/mcp/test/{client,content,stdio,streamable-http,oauth}.test.ts` in `mcp` and `mcp/oauth` (all 36 cases ported); `packages/coding-agent/test/mcp-extension.test.ts` (18 of 18) and `mcp-oauth-refresh.test.ts` (2 of 2) in `coding/mcpext` and `internal/codingagent/tools`; `suite/agent-session-mcp.test.ts` partial (16 of 24, the rest listed above); `suite/agent-session-mcp-oauth.test.ts` and `mcp-command.test.ts` handed to families 9 and 7. Upstream fixtures ported: `stdio-server.mjs` and `stubborn-server.mjs` (test-binary fixtures), `suite/mcp-oauth-server.ts` (`oauthserver_test.go`). None of these is `hot-path`-tagged in the plan's Appendix C (all are new files), and none is deferred for a credential or toolchain reason.

### Async contracts (for `test/parity/async-contracts.toml`, regenerated centrally)

| upstream source | construct | Go contract |
|---|---|---|
| `mcp/src/client.ts` `connect`, `request` | awaited Promise | blocking `Connect`, `Request` returning value and error; `AbortSignal` is the context (`McpAbortError`); the timeout is `McpTimeoutError` and progress renews it |
| `client.ts` `transport.send(...).catch`, `void handleRequest`, `cancelPending` notify | intentionally unawaited | goroutines owned by the client, counted in a `WaitGroup`, drained by `Close`; errors go to `OnError` listeners |
| `mcp/src/transports/stdio.ts` `start`, `close` | awaited spawn, awaited shutdown with timers | `Start` returns after the process started; `Close` runs stdin close, SIGTERM, SIGKILL stages and returns after exit |
| `transports/streamable-http.ts` `send` | awaited | blocking `Send`; a JSON reply is delivered before it returns |
| `streamable-http.ts` `void consumeResponseStream`, `void runGetStream` | intentionally unawaited | goroutines in the transport's `WaitGroup`; `Close` cancels the transport context, then waits; sleeps end on that context |
| `oauth/flow.ts` `adaptOAuthProvider` shared `inFlight` promise | shared Promise | one flight; the first caller's context does not bound it (`WithoutCancel`), each waiter honors its own |
| `oauth/callback.ts` `waitForCallback` | Promise registered synchronously | `WaitForCallback` registers, `CallbackWait.Wait(ctx)` blocks |
| `extensions/mcp/oauth.ts` `refresh`, `withRefreshLock`, `waitForAuthorizationCode` | shared Promise, lock, `Promise.race` | one refresh flight per provider under the store's lock; the loser of the race is a goroutine that ends when the prompt returns |
| `extensions/mcp/runtime.ts` `opening`, `void refreshTools` | shared Promise, unawaited | `openFlight`; background work counted and drained by `Connection.Close`; `Close` aborts a connect in flight |
| `extensions/mcp/index.ts` `pending`, `before_agent_start` race, `mcp_servers_change` | Promise, `Promise.race` with a timer, awaited handler | a goroutine and a channel; `select` with the startup timer; blocking handler methods; `SessionShutdown` closes connections and drains |

### Final gates (tranche 2)

- `refactor(mcp): compile MCP regexps lazily`: the wide run found `cmd/pig` `TestLinkedPackagesCompileNoRegexpAtInit` failing on `regexp.MustCompile` at package level in `coding/extension/mcp_servers.go`; every package-level MCP regexp now uses `internal/lazyregexp`. Not red-first: the existing startup-budget test is the guard and failed before the change.
- Wide run (`./agent/... ./coding/... ./internal/codingagent/... ./cmd/pig/... ./mcp/... ./tui/...`, isolated HOME, PIG_HOME and PIG_CODING_AGENT_DIR with the toolchains reached through the real mise directories): everything passes except `coding/extension/host/subprocess` `TestNodeVendoredTuiUpstreamTests/native-clipboard-linux`, which needs `Xvfb` on this machine (unrelated to this lane). One earlier concurrent run had spawn failures in packages that pass alone and under `-p 2`/`-p 3`; they are load flakes of the shared machine, not reproduced.
- Rule 17: a first wide run used the real `HOME`, so the test binaries may have written caches under `~/.pig/cache` (cells, node-compile). It touched nothing else, and no `auth.json` was read. All later runs isolated HOME.
- `make interface-go` and `make interface-recommendations-generate` were run (`pig-go.json`, `recommendations-v0.87.1.json`); drift checks are clean. `make known-gaps` fails on a missing `ai/images_models_upstream_test.go` from another family; `make parity-family` cannot run here because `parity-deps` refuses the shared `extensions/sdk-ts/node_modules` path. Neither is caused by this lane.
- `make divergence-guard -upstream .upstream/v0.99.1` reports no MCP hit; it reports two `ai/oauth_*` hits owned by another family. Without the pin move, the MCP `upstream:` markers cite files the current mirror does not have.
