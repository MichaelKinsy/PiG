# Lane port-99-f6f-node: the Node runtime for the upstream 0.99.1 extension API (family 6, the D-C sub-lane for `runtime-node/runtime.mjs`)

Branch `port-99-f6f-node` from `staging/porter/pi-0.99.1` (`113a10ba9`). Scope: `docs/plan/upgrade-0.99.1.md` D-C for the Node runtime. Owned: `coding/extension/host/subprocess/runtime-node/runtime.mjs` (and its generated archive), the Node rows in `test/extension-conformance/`, and the production wiring of `getSettings`/`executeTool`/`getCallableTools` that no SDK had (question `port-99-f6f-node.md`, 2026-09-30).

The Host side of every capability already exists (6D) and the Go, Rust and Python SDKs realize it (6F-go/rs/py); this lane makes the Node runtime speak the same wire (`protocol_extension_api.go`, `protocol.go`).

## Capability map

Each row is one upstream 0.99.1 capability (`.upstream/v0.99.1/packages/coding-agent/src/core/`) and the Node behavior. The Node spelling is Pi's own (`pi.registerMcpServer`, ...), so the interface is the upstream one; only the wire names below are PiG's.

| upstream | Node | wire |
|---|---|---|
| `registerMcpServer`, `unregisterMcpServer` (`extensions/loader.ts:456-478`) | `pi.registerMcpServer(name, config)`, `pi.unregisterMcpServer(name)`; queued in a `Map` while the factory runs, applied by the Host at the handshake | `RegisterPayload.mcp_servers`; after load calls `registerMcpServer`, `unregisterMcpServer` (synchronous, so an invalid config or a name a sibling owns throws at the call) |
| `getMcpServers` (`loader.ts:473-476`) | `pi.getMcpServers()`: copies of the replicated list | `StatePayload.mcpServers`; the reply of each call replaces the list |
| `registerVirtualModel`, `unregisterVirtualModel` (`loader.ts:480-497`) | `pi.registerVirtualModel(model)`: the declaration travels, the `route` stays in the runtime and runs with `(request, ctx)` | `RegisterPayload.virtual_models`; calls `registerVirtualModel`, `unregisterVirtualModel`; request `virtual_model_route` |
| `getSettings` (`loader.ts:411-414`, `settings-manager.ts:562-564`) | `pi.getSettings()`: a copy of the replicated settings; throws before the runtime is bound | `StatePayload.settings` |
| `ToolDefinition.{outputSchema,exposure,namespace,annotations,defaultActive,prepareLoadout}` (`types.ts:579-611`) | fields of the registered definition | `ToolDecl.{output_schema,exposure,namespace,annotations,default_active,prepares_loadout}`; request `tool_prepare_loadout` |
| tool result `structuredContent`, `isError` (`agent/src/types.ts:424-446`) | keys of the returned object, in final and partial results | `ToolResult.structured_content`, `is_error` |
| `ExtensionToolContext.tools`, `executeTool`, `ExecuteToolOptions` (`types.ts:367-395`, `runner.ts:952-985`) | only on the context of a tool call: `ctx.tools`, `await ctx.executeTool(name, args, { signal, onUpdate })` | `StatePayload.callableTools`; calls `executeTool`, `executeTool.cancel`; notify `execute_tool_update` |
| `getAllTools` `exposure`, `namespace`, `annotations` (`types.ts:2063`) | already passed through | `getAllTools` |
| `provider_stream_event`, `mcp_servers_change`, `parentToolCallId`, `structuredContent` on tool events | events pass through | events |
| provider chat/image/classifier entries (`types.ts:1929-1991`) | config passes through | `ProviderDecl.config` |

Not in this lane: `ctx.modelRegistry.{classify,findOfType,getAvailableOfType,getModelOfType,getModelsOfType,registerVirtualModel,unregisterVirtualModel}` and provider `images`/`classifiers` implementations (lane `port-99-f6h-model-types` owns the Host wire; runtime.mjs edits for them follow its wire). `Extension.replaceable` is not an SDK capability (`port-99-f6f-go.md`).

## Red

Commit: `test(extension-conformance): port upstream 0.99.1 Node extension API tests with signature stubs (red)`. Evidence: `docs/plan/evidence/port-99-f6f-node-red.txt` (each test run alone).

| file | tests | red |
|---|---:|---|
| `test/extension-conformance/node_extension_api_test.go` | 14 rows x strict and shared-ok (packed Node cell) | 11 fail for the intended reason; 3 pass on purpose as guards for behavior the runtime already has: `TestNodeGetAllToolsCarriesExposureNamespaceAndAnnotations`, `TestNodeProviderConfigWithImageAndClassifierModels`, `TestNodeToolEventsAndNewEvents` |
| `test/extension-conformance/node_session_twins_test.go` | 5 (twins of `suite/agent-session-tool-orchestration.test.ts` cases 1 and 3 and `suite/virtual-models.test.ts` cases 1, 2 and the router-state case, with Node extensions in the real Host and Session) | 5 fail: 2 on the `Session.ToolActions` stub, 3 on `pi.registerVirtualModel is not a function` |
| `test/extension-conformance/node_jev_router_test.go` | 2 (`test/jev-router-example.test.ts`, both cases, the unchanged example as a Node extension) | skipped, named: they route through `ctx.modelRegistry.findOfType/classify` (lane `port-99-f6h-model-types`) |
| `coding/extension/host/subprocess/upstream_jev_router_test.go` | 2 (every branch of `examples/extensions/jev-router.ts` that needs no classifier) | 2 fail: `pi.registerVirtualModel is not a function` |

Signature stub: `Session.ToolActions` (`coding/session_loadout.go`), exported so a bridge for subprocess extensions can bind the session's nested-call actions.

## Wiring red and green (found while reading the Host)

`HostCallbacks.GetSettings`, `GetCallableTools` and `ExecuteTool` could only be set through `UIBridge.SetActions`, which no mode called. In the real binary every subprocess SDK (Go, Rust, Python, Node) therefore saw no settings, an empty `ctx.tools` and the "Nested tool calls are not available" outcome; the conformance rows of the other SDK lanes passed only because they call `SetActions`. Lead approved the fix (`questions/port-99-f6f-node.md`).

- Red: `cmd/pig/extension_tool_context_wiring_test.go`, `TestExtensionToolContextIsWiredInEveryMode`: the real binary in print, json, rpc and interactive (PTY) mode with a Node and a Go extension whose tool reads `pi.getSettings()`, `ctx.tools` and runs `ctx.executeTool()`. 8 subtests, all red (`docs/plan/evidence/port-99-f6f-node-wiring-red.txt`).
- Green (own commit): `SetHostAction` keys `getSettings`, `getCallableTools`, `executeTool`; bound in `cmd/pig/session_extension_actions.go` (print, json, RPC) and `internal/codingagent/interactive_extensions.go`; `Session.ToolActions` exported; `SettingsManager.ExtensionSettings`; `subprocess.UnavailableNestedCall` exported. Mutations (the interactive and the print/RPC bindings each disabled) fail all 8 subtests.

## Green

Commits, in order: red `d81dbb696`; wiring red `2fc62e6da`; Node runtime green `f58fa931c` (`runtime.mjs`, regenerated archive); wiring green `cc4493ead`; unit tests; changelog and integrator rows; failed-factory probe.

Changes to ported tests (each cited in the test):
- `TestNodeVirtualModelRegistrationAndRouting`: the red row copied the Python row, which expects the Host to fail a route to an unknown physical model; since `dcd2035fb` the model runtime rejects it (`model-runtime.ts:1006-1009`), so the row asserts the model comes back naming only provider and id. `TestPythonSDKVirtualModelRegistrationAndRouting` needs the same correction (also it cannot run here: no Python).
- `TestNodeToolResultCarriesStructuredContentAndIsError`: a partial result's `structuredContent` has no carrier in `agent.ToolUpdateCallback`; the row asserts content and details cross. Node sends `structured_content` in `tool_update`.
- `node_session_twins_test.go`: the router state report used `Array.join`, which prints `undefined` as an empty string; it uses `String(JSON.stringify(...))`.
- `test/parity/scenarios/extensions-runtime/testdata/factory-failure/probe.mjs` gains the six 0.99.1 methods (`TestUpstreamFailedFactoryDisablesCapturedAPI` compares the probed methods with the API's).

Design notes:
- A factory's `registerMcpServer` and `registerVirtualModel` queue (a `Map` for servers, so a replaced name keeps its position and a queued unregister removes) and reach the Host in the register frame; later calls are synchronous host calls (`callSync`), so a refused registration throws at the call and `getMcpServers` sees the reply. Validation and the owner check are the Host's (as in the Go, Rust and Python SDKs): a factory that registers an invalid server fails the load with the runtime's message instead of throwing into the factory.
- `ctx.tools` and `ctx.executeTool` exist only on the context of a tool call (non-enumerable, as `Object.defineProperties` in `runner.ts:952-985`). Without `options.signal` the nested call is tied to the calling request; with one it is detached (`Runtime.call(..., { detached })`) and its abort sends `executeTool.cancel` after the call. `onUpdate` runs from the receive loop in order before the outcome; an error it throws is raised after the outcome.
- `virtual_model_route` gives the router `(request, ctx)` with `request.model`, `previous.model`, `failed.model` normalized and `request.signal` set to the request's; the returned model goes back as returned, an undefined `state` is omitted and `null` is a state.

## Evidence

- Node conformance rows: 14 tests x strict and shared-ok (packed Node cell) pass; session twins 5 pass; `TestNodeJevRouterExample` 2 cases skipped (named, port-99-f6h-model-types); jev-router example branches 2 pass; wiring 8 pass; unit `TestNodeRuntimeExtensionAPI` and `TestNodeRuntimeRunsTheCodemodeMCPSDKExample` (the unchanged `examples/sdk/14-codemode-mcp.ts` prints its active tools with codemode and tool_search, then stops at the prompt for want of an API key) pass.
- Mutations: 11 of 11 runtime.mjs mutants killed by the unit test (structured_content, detached, queue order, `hasOwn` on exposures, restore of a replaced route, `prepares_loadout`, onUpdate after failure, already-aborted signal, parent id); 2 of 2 wiring mutants killed.
- Load: `-race -test.count=24`, `GOMAXPROCS=4`, `taskset` on 4 cores with 4 burners, the Node rows, session twins and virtual-model rows: PASS.
- `make typescript-extension-corpus`: 68/68 upstream examples load and register (jev-router included; the re-vendor lane had 69/70 with jev-router failing).
- Gates: `go build ./...`, `go vet`, `GOOS=windows go vet`, gofmt, `golangci-lint` on the five touched packages (0 issues). `go fix -diff` reports only the untouched `coding/extension/host/runtimecell/build_failure.go`. Full-package runs on this host fail only where the toolchain or the pinned Pi package is missing (Python and Rust under mise, `extensions/sdk-ts` node_modules before `npm ci`) or in other families (codemode and tool_search default tools, the classifier family, `TestReplacedSession2860New`).

## Deferred

- `TestNodeJevRouterExample` (both cases of `test/jev-router-example.test.ts`): they route through `ctx.modelRegistry.findOfType` and `classify` (`jev-router.ts:78-102`). Pending `port-99-f6h-model-types`, which owns the Host wire; its `RuntimeModelRegistry` edits in `runtime.mjs` follow it. The body is ported; delete the `t.Skip` when the wire exists.
- `ctx.modelRegistry.registerVirtualModel/unregisterVirtualModel` and provider `images`/`classifiers`: same lane.
- Doc rows and async contract for the integrator: `docs/plan/evidence/port-99-f6f-node-docs-rows.md`.

## Parity family run (`make parity-family FAMILY=extensions-runtime`, hermetic, this host)

The base commit `113a10ba9` fails 23 scenarios; this branch fails 19 to 21 across three runs. The set is a subset of the base's failures plus `36-model-registry-session-manager`, which failed once (the Pig side missed the `prompt` response) and passed on the rerun. The scenarios compare against a Pi oracle whose install differs from the pinned one on this host, so most failures are path and version differences (`.pig` versus `.pi` in `sourceInfo`), not Node runtime behavior. Scenario `56-failed-factory-api` fails at the base and passes here after the `probe.mjs` update.

## ctx.compact (audit-host-wiring)

Red `test(runtime-node): ctx.compact reports its handler blocked and stays unparented`: `TestNodeRuntimeCompactReportsItsHandlerBlockedAndStaysUnparented` fails on the direct `conn.call` (no `blocked` frame). Green: `compact()` goes through `Runtime.call(..., { detached: true })`, which reports `blocked`/`progress` on the owning request and sends an empty parent id. It is detached and not parented (as the lead's note suggested) because Pi runs compaction in an unawaited block (`agent-session.ts:3369-3378`), so a handler that returns must not cancel it; `TestNodeCompactOutlivesTheHandlerThatStartedIt` guards that. Mutation: reverting to `conn.call` or dropping `detached` fails the unit test. The in-process and Session-level ordering rows did not reproduce the hold-back (it depends on the real modes binding `Compact` and `SetThinkingLevel`, which audit-host-wiring's `253221cac` adds), so they were not committed; when that lands, audit-host-wiring's own fixture-order test covers the end-to-end order. `TestNodeRuntimeSDKSurfaceAdditions` now expects the empty parent id.

## Merge check against rev-fix-99-j5-ext

See `docs/plan/evidence/port-99-f6f-node-j5-merge.md`: one conflict (`coding/session_loadout.go`, resolution recorded there) and the classification of the failing extensions-runtime scenarios.
