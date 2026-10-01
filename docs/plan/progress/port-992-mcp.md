# Lane port-992-mcp: Pi 0.99.2 MCP and codemode changes

Branch `port-992-mcp` from `staging/porter/pi-0.99.1` (7cc499686). Upstream: Pi v0.99.2 (commit 005af57d88ee23b33778f343a9595b32e67ff788), diffed against `.upstream/v0.99.1` for `packages/{mcp,codemode,coding-agent}`.

## Red

Every 0.99.2 test change in scope is ported with upstream inputs, plus signature stubs (real data fields; zero-value functions). Run on the red commit (packages that contain lane tests): `coding/mcpext` (27 fail), `coding/extension/builtin/codemode` and `toolsearch`, `codemode` (2), `tui` (new visual-truncate tests), `internal/codingagent` (codemode renderer), `coding/extension/host/inproc` (1), `coding` (`presents callable tools per codemode.mode`). The other failures those packages show (settings, trust, login, theme, `coding/extension` ExecCommand) are not lane tests.

Not ported, with the reason:
- `packages/mcp/test/streamable-http.test.ts` "calls fetch without a receiver" (#10188): Go has no receiver binding; `net/http` clients are not method values of the transport.
- `packages/codemode/test/sandbox.test.ts` "accepts a worker path string" and `packages/coding-agent/test/codemode-worker-config.test.ts` (#10204): the string `workerUrl` names the Bun compiled-executable worker entry. The Go engine runs QuickJS on wazero in process, with no worker file, so the Windows standalone failure cannot occur.
- `suite/agent-session-mcp*.test.ts`: `builtin:mcp` has no Session registration on this base, so the cases are ported at the extension boundary (`coding/mcpext/extension_test.go`), as the 0.99.1 lane did.
- Out of scope (other lanes): `defaultTools` reload, Anthropic workload identity, model-runtime #9962, virtual-models #10198, remote catalog, extension examples (`tool-renderer-examples.test.ts`).


## Green

Commits, in order: red `58d694d1d`; green 1 `a0c498d86` (MCP extension, config, CLI, OAuth client name, provider auth, namespaces, tool names, `mcp_servers` section, `VisualLinePreview`, sign-in hyperlink); green 2 `c8eac53bb` (codemode description, `describeNamespace`, namespace aliases, `tool_search` description, renderers, `image()` validation, hidden tool list); pin-move merge `966cec0f0` (porter `6afaf2bc3`).

Corrections to tests that are not upstream tests, each justified by the 0.99.2 behavior:
- `coding/mcpext/command_test.go` harness calls `Pending()`: the first prompt no longer waits for servers without direct tools (index.ts `waitForDirectServers`).
- `tui/visual_truncate_test.go` compares ANSI-stripped, right-trimmed lines: `Text` pads every line to the width (visual-truncate.ts renders through `Text`). Upstream has no test for the file.
- `coding/extension/builtin/builtin_test.go`: `tool_search` has no `prepareLoadout` (tool-search/tool.ts `TOOL_SEARCH_DESCRIPTION`); `description_order_test.go`: the `PARTIAL` line no longer exists (tool.ts `createCodemodeDescription`).
- `test/extension-conformance/node_extension_api_test.go` "copy" row sorts the members of the returned config: the host answers in Go struct member order, not registration order (found, not fixed; see the report).
- `codemode/sandbox_test.go` and `codemode/assets/PROVENANCE.md`: the recorded `prelude.js` hash, regenerated from the 0.99.2 `PRELUDE_SOURCE` (`50819a59…`), as PROVENANCE step 3 requires.

Mutation checks (revert, red, restore):
- provider token `Token()` returns "" → `TestMCPConnectionsSendsTheProviderTokenAndAsksForTheProviderLoginWhenTheServerRejectsIt` red.
- hidden tools listed in the prompt → `TestUpstreamAgentSessionCodemodeTool` red.
- `ToolCall` does not wait → `TestAgentSessionMCPCodemodeScriptThatSearchesOrEnumeratesWaitsForEveryServer` and `…WhoseScriptIdentifiersDifferFromTheirNames` red.
- `waitForDirectServers` removed → `TestAgentSessionMCPWaitsForServersWithDirectToolsBeforeListingTheServers` red.
- `isNamespaceName` compares names only → `TestNamespaceNamesAcceptTheServerNameTheIdentifierAndTheirSuffixes` red.
- colliding names not suffixed → `TestAgentSessionMCPRoutesToolsWhoseNamesDifferOnlyInDashAndUnderscore` red.
- `keep: "start"` ignored → `TestTruncateToVisualLinesKeepsTheEndByDefaultAndTheStartOnRequest` and `TestVisualLinePreviewPlacesTheHint…` red.
- `exposure` alias not resolved → `TestMCPConfigValidatesExposureAndReadsAutoEnableCodemodeWithProjectPrecedence` red.
- `image()` signature check removed → `TestRejectsInvalidTextAndImageArguments` red.

Load: `GOMAXPROCS=4 taskset -c 0-3 go test -race -count=24 ./coding/mcpext` with four CPU burners: ok (135 s). The same for `coding/extension/builtin/...`, `coding/extension/host/inproc`, `tui` (visual preview): ok.

Not runnable here: the Pi-oracle tests (`TestColorDetectionMatchesPi` and similar) need `extensions/sdk-ts/node_modules`; they fail identically without this lane's changes. Parity scenarios for the new prompt section and help text need the Pi 0.99.2 npm package (cooldown-blocked); none was added.

## Phase 1 ledger (per upstream test file, Pi 0.99.2)

Status: credited / ported-passing / ported-FAILING. No ported test in scope fails. Production fixes were made before the method change, in their own commits: `a0c498d86` (MCP extension, config, CLI, OAuth, tools, renderers, `VisualLinePreview`), `c8eac53bb` (codemode, tool_search, prelude, hidden tool list), `d16ac1e56` (unused helper removed); `c3fd07386` regenerates `pig-go.json` and the recommendations.

| upstream file | status | Go tests |
|---|---|---|
| `coding-agent/test/mcp-extension.test.ts` | ported-passing | `coding/mcpext/{config,tools,connection,manager,servers_section,tools_render,extension}_test.go` |
| `coding-agent/test/mcp-command.test.ts` | ported-passing | `coding/mcpext/{cli,cli_upstream}_test.go`, `testdata/mcp_help.txt` |
| `coding-agent/test/codemode-renderer.test.ts` | ported-passing | `internal/codingagent/tool_codemode_renderer_upstream_test.go` |
| `coding-agent/test/tool-search.test.ts` | ported-passing | `coding/extension/builtin/toolsearch/toolsearch_upstream_test.go`, `codemode/run_test.go` |
| `coding-agent/test/suite/agent-session-codemode.test.ts` | ported-passing | `coding/codemode_session_upstream_test.go` (`presents callable tools per codemode.mode`, #10192) |
| `coding-agent/test/suite/agent-session-mcp.test.ts` | ported-passing at the extension boundary, except one case credited | `coding/mcpext/extension_test.go`. "keeps the codemode description unchanged when the server connects" is credited to `TestCodemodeDescriptionLeavesDeferredToolsAndTheirNamespacesOutEntirely`: the codemode description no longer depends on deferred tools. The session-level form needs `builtin:mcp` registered in a Session, which this base lacks. |
| `coding-agent/test/suite/agent-session-mcp-oauth.test.ts` | ported-passing at the CLI boundary | `TestMcpLoginRegistersWithTheConfiguredClientName` (fallback client name is the app name, `pig`) |
| `coding-agent/test/suite/mcp-oauth-server.ts` (helper) | ported | `coding/mcpext/oauthserver_test.go` (`registrationClientNames`) |
| `codemode/test/sandbox.test.ts` | ported-passing | `codemode/sandbox_test.go`; "accepts a worker path string" designed out (no worker in the Go engine) |
| `coding-agent/test/codemode-worker-config.test.ts` | designed-out (#10204) | no worker file in the QuickJS/wazero engine |
| `mcp/test/streamable-http.test.ts` | designed-out (#10188) | no method-value receiver in Go |
| `loader.ts` registerMcpServer clash (covered by `mcp-extension`/loader cases) | ported-passing | `coding/extension/host/inproc/mcp_servers_registration_test.go` |
| extension API `ToolNamespace.instructions`, MCP `description`/`auth`/`oauth.clientName` (SDK rows) | ported-passing | `extensions/sdk`, `extensions/sdk-rs`, `extensions/sdk-py`, `coding/extension/host/subprocess/extension_api_wire_test.go`, `test/extension-conformance` (Go, Rust, Node rows) |
| `visual-truncate.ts` (no upstream test) | ported from the source branches | `tui/visual_truncate_test.go` |
| `tool-renderer-examples`, `default-tools-setting`, `virtual-models`, `model-runtime-modify-models-compat` | out of scope (other lanes) | |
