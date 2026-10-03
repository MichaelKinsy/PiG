# Lane fix-992-mcp-session: `builtin:mcp` in the Session (upstream 0.99.2)

Branch `fix-992-mcp-session` from `staging/rev-port-992-mcp` (9cf7919bd). Upstream: upstream v0.99.2 (`.upstream/v0.99.2`).

Problem (review finding E of rev-port-992-mcp): `mcpext.Factory` had no Session registration, so the MCP cases of the agent-session suite, the `tool_call` waits and the `mcp_servers` prompt section ran only at the extension boundary. Pi registers the extension in `packages/coding-agent/src/extensions/index.ts` (`{ name: "mcp", factory: mcpExtension, replaceable: true, builtin: true }`) and loads it in every mode through the resource loader (`core/resource-loader.ts` `loadExtensionPaths`).

## Red

Session-level ports, with Pi's inputs and expectations:

| upstream file | Go tests |
|---|---|
| `suite/agent-session-mcp.test.ts` "AgentSession MCP integration" (33 cases) | `coding/mcp_session_upstream_test.go` (36 tests with the `it.each` runs) |
| `suite/agent-session-mcp.test.ts` "AgentSession MCP servers registered by extensions" (4 of 5) | `coding/mcp_session_registered_upstream_test.go`, Node fixtures in `coding/testdata/mcp` |
| `suite/agent-session-mcp-oauth.test.ts` (6 cases) | `coding/mcp_oauth_session_upstream_test.go`, server `coding/mcp_oauth_server_test.go` (a copy of `coding/mcpext/oauthserver_test.go`: an external test package cannot share it) |
| `suite/agent-session-codemode.test.ts` | already ported at session level in `coding/codemode_session_upstream_test.go` (the reviewed 0.99.2 cases); nothing in it reaches MCP (`grep -i mcp` finds no case) |

Real-binary tests (print, JSON, RPC) with a scripted OpenAI-compatible model and a real stdio MCP server process: `cmd/pig/mcp_builtin_binary_test.go`, fixture `cmd/pig/testdata/mcp-stdio-server.go`, plus `cmd/pig/mcp_builtin_extension_test.go` (the CLI's built-in extension list).

Not ported: "rejects names another extension registered" at session level. Pi throws the clash inside the extension's own factory; PiG queues a registration made while the factory runs and validates it when the extension registers (`docs/extension-api-parity.md` `registerMcpServer` row), so the clash fails that extension's load. The runtime checks are ported in `coding/extension/host/inproc/mcp_servers_registration_test.go`.

Stubs: `builtin.Options.Mcp` (`mcpext.Options` alias; an empty struct under `pig_strip_mcp`) and `mcpEntries`, which lists nothing. The test helper `harnessOptions.runtime` (`coding/session_recovery_test.go`) passes the extension runtime the loader shares with the runner.

Red run (commit below): `go test ./coding -run TestAgentSessionMCP`: 41 top-level tests fail, all for `Unknown built-in extension: builtin:mcp` except the registered-by-extensions tests (the same) and the orphan report (`errors = []`). `go test ./cmd/pig -run 'TestCLIBuiltInExtensionsListMCP|TestBuiltinMCPExtension'`: all 7 fail (no `codemode` declared, no `mcp_servers` section, no `mcp__docs__search` tool, no `mcp` in the built-in list).

## Green

Commits (after the red commit `eddce1e6c`):

1. `9a17ae615` `feat(mcp): register builtin:mcp with every Session mode (green)`: the replaceable built-in `mcp` (after codemode and tool-search, `extensions/index.ts`), `Context.RefreshTools`/`GetMcpServers` (`agent-session.ts:3353`), the typed `SourceInfo` fix in `mcpext.sourcePath` (a Session's codemode and tool-search tools were never found), `ReportUnhandledMcpServers` after `session_start` and reload (`agent-session.ts:3207`, `3620`), and the shared extension-host runtime (`RuntimeOptions.ExtensionRuntime`).
2. `dbcc45628` `fix(mcp): keep parallel MCP tool calls in call order (green)`: root cause of the `Promise.allSettled` wire-order flake in `TestAgentSessionMCPExposesCodemodeOnly...` (about 3 of 300 runs): Pi runs each call's synchronous prefix in call order, and Go's per-call goroutines (codemode `startCall`, the nested runner, `mcp.Client`'s `go Send`) did not. The fix is `ToolDefinition.ReserveCallOrder`, a lane per server in `mcpext`, `codemode.Tool.AwaitsInitiation`, and `mcp.OrderedSender` for stdio and in-memory transports. A `-race` run also found `agent.Agent.SetTools` racing a run that reads the tool list while a background connection registers tools; `toolsMu` guards it.
3. `00b61c758` `fix(mcp): serve builtin:mcp in interactive mode (green)`: interactive mode bound its own `getAllTools` (source strings, no exposure), `setActiveTools` (outside the Session loadout) and no `refreshTools`, so codemode was never activated (first request declared `read bash edit write` only; the interactive subtest of `TestBuiltinMCPExtensionServesCodemodeServersInEveryMode` was red with exactly that). It now uses the Session's registry and loadout.

Real-binary proof (`cmd/pig/mcp_builtin_binary_test.go`, scripted OpenAI-compatible model, real stdio MCP server process, pseudo-terminal for interactive): codemode server in print, json, rpc and interactive modes (tool hidden from the model, `mcp_servers` section lists the slow server by name and the connected one with its first instruction line, a slow server does not hold the first prompt or a script that does not name it); deferred server through `tool_search` (print, interactive); direct server declared on the first request (print, interactive); the `mcp_servers` section updated with the next prompt (rpc); a server a Node extension registers reaches the built-in (print, json, rpc, interactive); the CLI's built-in extension list shows `mcp` after `tool-search`.

Mutation checks (each fails the named tests): typed `sourcePath` disabled (all codemode-only session tests), `RefreshTools` after `RegisterTool` removed (OAuth and registered-server tests time out), `ReportUnhandledMcpServers` removed (`...ReportsRegisteredServersWhenNoExtensionConnectsThem`), `ExtensionRuntime` not shared (`TestBuiltinMCPExtensionConnectsServersRegisteredByExtensions/print`: `Tool codemode not found`), `AwaitsInitiation` off (3 of 300 runs fail the wire-order test; 0 of 200 with it on).

Load: `GOMAXPROCS=4 taskset -c 0-3` with four CPU burners, `-race`, `-test.count=24` over `TestAgentSessionMCP|TestUpstreamAgentSessionCodemode`: pass. `go test ./cmd/pig -run 'TestBuiltinMCPExtension|TestCLIBuiltInExtensionsListMCP' -count=8` under the same burners: pass.

Gates: `go build ./...`, `go vet` and `GOOS=windows go vet` over touched packages, gofmt, golangci-lint (`--build-tags=integration,live,parity`) over touched packages (0 issues), `go fix -diff` clean for touched files (`coding/extension/host/runtimecell/build_failure.go`, `test/extension-conformance/model_types_test.go` and `coding/virtual_models.go` already differ on the base), `go build -tags pig_strip_mcp ./cmd/pig` links no `coding/mcpext` symbol (489 in the full build), `check-public-claims.py`.

Test changes after the red commit (none loosens an upstream assertion): the harness binds without `ModeTUI` (Pi's `bindExtensions` default mode is print), the fake server handles requests in delivery order with only the reply on a goroutine (Pi's microtask order), `hookTransport` overrides `SendOrdered` (embedding would promote the inner transport's and bypass its `Send` override).

Failures in code this lane did not touch (environment or a pinned 0.99.1 oracle, not re-run on the base): `TestNodeVendoredTuiUpstreamTests/native-clipboard-linux`, `internal/nativeplatform` X11 tests (no Xvfb), `TestBashStructuredResultMatchesRecordedUpstream`, `TestFauxRPCObservation`, `TestTestFauxRPCObservation` (oracles pin upstream 0.99.1), `internal/testenv TestWindowsTestsCreateSymlinksThroughSymlink` (`coding/extension/host/subprocess/socket_runtime_dir_test.go`).

Deferred and why:
- Parity scenario against upstream 0.99.2: the upstream 0.99.2 npm package is cooldown-blocked (the installed `pi` is 0.87.1), so no oracle run exists. The wire facts it would check (tool declarations per exposure, the `mcp_servers` section text) are asserted in the real-binary tests from `.upstream/v0.99.2` sources.
- Session-level "rejects names another extension registered" (see Red).
- The lane's `Wait` does not observe cancellation: a call waits for earlier calls to the same server to release, and they release when their request has an id, their call ends, or the agent retires an aborted call.
- Model-level parallel batches use `ReserveMutationOrder` through the agent's existing reservation step; no dedicated test drives two MCP calls in one assistant message.
- Updates-section test runs in RPC only (the interactive driver waits for the total request count).
