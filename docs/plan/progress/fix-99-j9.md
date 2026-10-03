# fix-99-j9: Node extension API fidelity leftovers from the port-99-f6f-node review

Base: `rev-port-99-f6f-node` at 751c13b27. upstream 0.99.1 (`.upstream/v0.99.1`). Items B-G are the "Lower-severity findings, not fixed" of that review. Every pig invocation in this lane used a temporary `HOME`, `PIG_HOME` and `PIG_CODING_AGENT_DIR`.

## Design

### B and C: factory-time `registerMcpServer` validation and `getMcpServers()` (Node only)

Pi validates the config and checks ownership when `registerMcpServer` is called and throws to the factory (loader.ts:456-468); `getMcpServers()` returns the live registry, which holds what earlier-loaded extensions committed and not what this factory queued (loader.ts:259-262, 475-478).

The Node runtime already opens an early connection while its factory runs (`ensureConnectionSync`, used by `pi.events`), and `Host.waitForRegister` already serves `loadingBusCall` methods before `register`. Two new calls use the same path:

- `mcpServers.check {name, config}`: validates with `ValidateMcpServerConfig` and checks ownership against the runtime registry for the extension's path, with no commit. It returns Pi's message as the call error. The commit still happens from the register frame, as `applyRuntimeChange` does after the factory succeeds, and the Host keeps its own check there.
- `getMcpServers`: returns the runtime registry. After the factory it is the same live call, so Node no longer keeps a replicated server list.

The Go, Rust and Python SDKs have no factory-time Pi API: `RegisterMcpServer` is an Extension method that queues before `Run`, and `GetMcpServers` exists only on the Context (after ready). They have no connection before `Run` (a fused Piglet Binary receives its pipe in `RunWithConn`). Opening the connection before the factory in three SDKs and the fused path is a startup-order change in every SDK. Question posted to the lead; this lane does B and C for Node only unless told otherwise.

### D: `unregisterVirtualModel` before bind filters the runtime-wide list (all SDKs)

The Host's `ExtensionRuntime.UnregisterVirtualModel` already filters the runtime-wide pending queue (runtime_registrations.go). Only the register frame lacks the unregistrations: every SDK drops the extension's own pending entry and forgets the call. The frame gets an ordered `unregister_virtual_models` list (provider, id) that the Host applies, through `RuntimeRegistration`, before the extension's own virtual models. Applying every unregistration first gives Pi's result for any interleaving, because the SDK already removed its own queued entries.

### E: router state identity (all SDKs)

Pi stores the state unless `route.state === request.state` (agent-session.ts:788): JavaScript identity. Across a process boundary:

- Node observes identity in its own process, so the route reply carries `stateUnchanged` when `route.state === request.state`, and the Host answers the session with the request's own bytes.
- Go, Rust and Python cannot observe identity. The Host compares the JSON values of the returned state and the request state and answers with the request's bytes when they are equal. (Residual: a Node router that returns a fresh object equal to the request state is stored by Pi; a byte-identical fresh state is skipped here. Recorded, not closed.)

### F: live `ctx.tools` (all SDKs)

The Host already holds the `getCallableTools` action. A new `getCallableTools` call replaces the `callableTools` field of the replicated state in every SDK, so the getter reads what the session lists when it reads (runner.ts:958-961).

### G: a throwing `executeTool` onUpdate rejects the call (all SDKs)

Pi's callback is `options.onUpdate?.(partial)` before `await emit(tool_execution_update)` (nested-tool-calls.ts:219-231). A throw skips the event for that update, rejects the update's promise, and `executePreparedToolCall` rethrows after the tool returned (agent-loop.ts:833-845), so `runToolCall` rejects before `finalizeExecutedToolCall` (no afterToolCall), and `execute()` rejects before `finish` and the `tool_execution_end` emit (nested-tool-calls.ts:232-248).

- `agent.ToolUpdateSink` (error-returning) replaces the callback type of `RunToolCallOptions.OnUpdate`; `agent.RunToolCall` returns `(outcome, error)`.
- `NestedToolCallRunner.Execute`, `ToolActions.ExecuteTool`, `HostCallbacks.ExecuteTool` and the session host return `(outcome, error)`.
- Host to extension: `execute_tool_update` becomes a request the Host waits on (it was a notify), so the extension's throw reaches the sink before the next update. The request is not cancelled with the call (Pi's callback has no cancellation).
- Each SDK answers the request after its callback returns, with the callback's error: Node's thrown message, Python's exception text, Go's and Rust's recovered panic. The caller's `executeTool` rejects with the first error, as the Node runtime already kept the first thrown error.

## Red run (tests first)

Each test below failed for the stated reason on the signature-stub tree (commit "test(extensions): port upstream 0.99.1 fidelity tests with signature stubs (red)"). The stubs are: `agent.ToolUpdateSink`, `RunToolCall` returning `(outcome, error)` and ignoring the sink's error, `NestedToolCallRunner.Execute`, `ToolActions.ExecuteTool`, `HostCallbacks.ExecuteTool` and `Session.executeNestedToolCall` returning `(outcome, error)` with nil errors, and the Rust `Extension::unregister_virtual_model` and `unregister_mcp_server` (own-queue filtering only). Existing tests and fixtures were adapted to the new signatures and `execute_tool_update` sink type; the Go and Rust fixtures gained tools and virtual models for the new rows (the callable-tool list of `TestExtensionAPIReplicatedStateAndLateRegistrationGo` and the pending list of `TestRustSDKVirtualModelRoute` follow).

| Item | Test | Red reason |
|---|---|---|
| B | `TestNodeFactoryMcpRegistrationThrowsInsideTheFactory` | the factory's registration is deferred to the Host and fails the load with "Invalid MCP server registered by extension" |
| C | `TestNodeGetMcpServersInsideTheFactoryListsTheLiveRegistry` | `getMcpServers()` in the factory returns `[]` |
| D | `TestNodeUnregisterVirtualModelBeforeBindFiltersTheRuntimeWidePendingList`, `TestPythonSDKUnregisterVirtualModelBeforeBind...`, `TestExtensionAPIUnregisterVirtualModelBeforeBind...Go` (fused, strict, packed), `TestRustSDKUnregisterVirtualModelBeforeBind...` | the other extension's pending `victim` survives |
| E | `TestNodeVirtualModelRouterReturningItsRequestStateKeepsTheRequestStateBytes`, Python, Go, Rust twins | the route's state is Node/Python/serde bytes (`{"a":"<b>&"}`, re-ordered keys), not the request's |
| F | `TestNodeCtxToolsIsLiveInsideOneHandler`, Python, Go, Rust twins; `TestExtensionToolContextIsWiredInEveryMode` (real binary, node and go, four modes) | `ctx.tools` after `setActiveTools` lists the tools replicated at request start |
| G | `TestRunToolCall_RejectsWithTheFirstUpdateSinkErrorAndSkipsAfterToolCall`, `TestNestedToolCallRunnerUpdateCallbackFailureRejectsTheCallBeforeItsEndAndAfterHook`, `TestNodeExecuteToolOnUpdateThrowRejectsTheNestedCall`, Python, Go, Rust twins; real binary rows | the Host answers the update sink with nil; the real session emitted `tool_result` and `tool_execution_end` for the rejected call and a second `tool_execution_update`; Python dropped the update after the first raise; Go recovered the panic and reported "no rejection"; Rust's panic killed the handler |

The real-binary rows also show the session-level symptom: events of the nested call `call_test_faux_1/2` were `[update update result end]` where Pi emits only the second update.

### H: calls a native handler starts without awaiting outlive the response (Go, Rust, Python)
Pi has no request scope for a host call (`loader.ts` `createExtensionRuntime`; `interactive-mode.ts:2858-2940`): a handler that starts a call and returns leaves it running; only the call's own signal ends it. The Node runtime follows this since #103. Decision: the native SDKs must match. The lead answered (a): fold the #103 lane into this branch. A `git merge` of `rev-fix-031-cancel-parent` is not possible: its merge base with this branch is `f5c98329d` (public-main history), 669 add/add and content conflicts in unrelated files, because the lane sits on the 0.3.1 line (base `409eaa4e1`) that this 0.4.0 base does not contain. Its own 17 commits touch 10 files, so they were cherry-picked with `-x -s` (`409eaa4e1..81e17e4fb`, the REVIEW commit excluded), two content conflicts resolved (`runtime.mjs` `call`: this base already has `detached`, combined with the lane's `hostCancelled`; `conn.go`/`host.go`: this base's runtime-drain handling kept next to the lane's host-call reservation) and the embedded runtime archive regenerated (`go generate`), commit `chore(port): regenerate the embedded Node runtime archive after folding in the #103 lane`. `go test ./coding/extension/host/subprocess` afterwards: only the baseline `TestPiThemeHelpersMatchThePinnedPackage` and the Xvfb row fail.

Red (commit `105487331`, all three SDKs): `TestExtensionAPIHostCallStartedByAHandlerOutlivesItsResponseGo` (fused, strict, packed), `TestRustSDKHostCallStartedByAHandlerOutlivesItsResponse` (isolated, packed), `TestPythonSDKHostCallStartedByAHandlerOutlivesItsResponse` (strict, shared-ok). Each fixture's tool starts a nested `wait_release` call on a goroutine/thread, waits for the test to report the host has it pending, and returns. Red symptom in every mode: the thread's call fails with `host call executeTool cancelled with its parent request`.

Green: Go `conn.respond` no longer calls `cancelParentCalls` for a completed request; it detaches the request's pending calls (`pendingParents[id] = ""`), and `waitCall` hands the wait from the request context (cancelled when the handler returns) to the runtime context once the parent completed (`pendingCall.parent`); a cancelled request never completes, so its calls still end. Rust `respond_serialized` detaches (`parent_request_id = None`) instead of dropping the senders. Python `_respond` detaches instead of cancelling. Cancel frames still cancel the calls of a request that has not completed (existing `nested_cancel` and cancelled-call rows). Test-fixture corrections in my own red commit: the Python fixture read the outcome as an object (it is a dict), fixed once the call no longer failed before that line; the callable-tool lists in two Go rows gained `detached_call`.

Existing tests changed because they encoded the old contract (not upstream ports): `extensions/sdk/request_lifetime_race_test.go` `TestCompletingContextCancelsUnsettledParentCall` asserted "unsettled call survived originating request cleanup"; it is now `TestCompletingContextLeavesUnsettledParentCallRunning` (the host's result reaches the call). Rust `tests/extension_api_wire.rs` (`getters_answer_from_the_replicated_state`, `execute_tool_runs_a_nested_call_and_delivers_updates_in_order`) and Python `tests/test_extension_api_099.py` (two rows) follow F and G: the host is asked for `getCallableTools`, and updates are requests with an answer.

## Green run (B-H)
- Commits: red `668049250`, green `676d42348`, docs `46520f30b`, #103 lane cherry-picks `93eafd357..c98155c8f`, regenerated runtime `a86274626`, H red `105487331`, H green (this commit).
- Mutation checks (each red, restored): skip the host's `unregister_virtual_models` (D red: Node, Go); the update sink ignores the extension's error (G red: Node, Rust); router `routeState` ignores JSON equality (E red: Go, Python).
- Load: `-race -count=24` on `agent` and `coding` nested-call tests, `-race -count=12` on `extensions/sdk`, `-race -count=3` on `coding/extension/host/subprocess`, the B-G conformance rows under `GOMAXPROCS=4 taskset -c 0-3` with four CPU burners, count 8: PASS. H rows re-run under the same load after the H green.
- Baseline failures that also fail on `751c13b27` with `.upstream` at v0.99.1: `TestUpstreamAgentSessionCodemodeTool`, `TestUpstreamCodemodeOptionsAndStore`, `TestUpstreamCodemodeModels`, `TestReplacedSession2860New`, `TestPiThemeHelpersMatchThePinnedPackage`, `TestInteractiveThemeSelectionPresenceMatchesPi`, the RPCInputEnd rows compared with Pi, `TestNodeVendoredTuiUpstreamTests/native-clipboard-linux` (Xvfb).

## Residuals
- B and C are Node only (lead's ruling): Go, Rust and Python queue `RegisterMcpServer` before `Run` and the host validates on register; they have no factory-time Pi object.
- E: a Node router that returns a fresh object equal to the request state is skipped here when the JSON values are equal; Pi stores it (identity, `agent-session.ts:788`). The state bytes are the same, so the observable effect is none for JSON-equal values.
- Stubs for other families: none.
