# ExtensionRunner and ExtensionRuntime rows at Pi 1.1.0 (lg-imla-5)

Source rows: `logs/gaps-110.tsv`, 30 rows under `pkg:coding-agent/.#ExtensionRunner` and `.#ExtensionRuntime`. Pi `core/extensions/runner.ts` and `loader.ts` are byte-identical in 1.0.4 and 1.1.0 (`diff` empty for runner.ts), so none of these rows is a 1.1.0 behaviour change. Each row below was read against the Go source and the production path; no row needs new production code, so none was added (a caller-free member would reopen under the prodreach rule).

## Behaviour verified as ported through the real host path

- `ExtensionRuntime.getCommands` / `ExtensionRunner.bindCore(getCommands)`: Pi wires `getCommands` in `agent-session.ts:3361` (extension, prompt and skill commands). Go serves `pi.getCommands()` through the subprocess UIBridge host action `"getCommands"`, set in every mode: `cmd/pig/print_mode.go:368`, `cmd/pig/rpc_mode_runtime.go:565`, `internal/codingagent/interactive_extensions.go:474`, answered by `ui_bridge.go handleGetCommands`. The inproc `ExtensionActions.GetCommands` (read by `runtime.GetCommands()`, tested at `coding/extension/host/inproc/runtime_shared_state_test.go:177-217`) is the host-side runner's copy of the same action and has no production caller because production extensions are subprocess-only (AGENTS.md extension boundary). Request: reviewed placement of `getCommands` onto the UIBridge host action; the inproc action stays for the runner's `bindCore` parity test.
- `ExtensionRuntime.trackEventBusSubscription`: Pi unsubscribes every tracked bus subscription when the runtime is invalidated (`loader.ts:195-210`). Go keeps bus listeners per extension connection in `coding/extension/host/subprocess/event_bus.go` and removes them when the connection closes (`hostEventBus.closed`), which is what session replacement and reload do to the extension process. Pi's own behaviour test is ported at `coding/extension/host/subprocess/node_event_bus_lifecycle_upstream_test.go`. Request: designed-out as folded into connection teardown; no `TrackEventBusSubscription` member.
- `ExtensionRunner.emitBeforeAgentStart` (`runner.ts:1420`): Pi renders `event.systemPrompt` from the shared options with `buildSystemPrompt` after each handler. Go takes the already rendered prompt (`Session.systemPrompt()`) and a `ctx` first (`coding/session_preflight.go:56`) because the prompt builder lives in `internal/codingagent/prompts`, below `coding/extension/host/inproc`. Handlers in a subprocess return `{systemPrompt}`; they cannot edit the host's options object in place, so a later handler sees the same prompt either way. Request: reviewed placement (S4: leading `ctx`, rendered prompt passed in).

## Shape-only rows (type or parameter mapping), for lg-rules

| rows | reason | Go form |
|---|---|---|
| `ExtensionRunner::construct:0` (S3) | Pi `(extensions, runtime, cwd, sessionManager, modelRegistry)`; Go `NewRunner(extensions, cwd, sharedRuntime...)`, session manager and model registry arrive in `BindCore` (`ContextActions.SessionManager`, `.ModelRegistry`, `coding/session_bind.go:25-40`) | constructor plus bind |
| `bindCore::call:0`, `pendingNativeProviderRegistrations`, `registerNativeProvider::call:0` (T9t) | Pi `Provider.auth: ProviderAuth` object; Go `extension.NativeProvider` carries the auth methods as the callbacks `CheckAuth`, `ResolveAuth`, `ResolveRefreshCredential` (`coding/extension/native_provider.go:13-30`) | callbacks instead of an auth object |
| `createToolContext::call:0` (T9) | result `ExtensionToolContext`; Go returns `context.Context` carrying the tool context (`inproc/tool_context.go:18`) | context value |
| `emit::call:0` (T9) | `RunnerEmitResult<TEvent>` conditional type; Go returns `any` (godoc at `runner.go:2637-2642`) | TS conditional type |
| `emitMessageEnd::call:0` (T9) | event type `MessageEndEvent` against `extension.AgentMessage` (opaque alias, `coding/extension/opaque_types.go`) | opaque alias |
| `sendMessage::call:0` (T3) | upstream parameter flagged boolean against Go `any`: the `options` bag, carried as `any` through `ExtensionActions.SendMessage` | options bag |
| `emitContext`, `emitInput`, `getModelRegistry` `::call:0` (reviewed-gap, shard 3) and `setModel::call:0` (shard 1) | already decided by their own port-needed rows | n/a |

I will re-run the detector on integrate-042 after any of these lands and take whatever remains open.
