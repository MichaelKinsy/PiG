# verify-ctx-signal: is extension `ctx.signal` Pi-faithful?

Base: `staging/porter/pi-0.99.1`. Upstream 0.99.1 and 0.99.2 are identical for this surface (`runner.ts`, `agent-session.ts` `getSignal`, `agent.ts` `get signal()`). Public `main` (`staging/mirror/public-main`) and `release/0.3.1` (`502cc0a4`, Pi 0.87.1) carry the same `runtime.mjs` code, and Pi 0.87.1 has the same `getSignal` binding (`agent-session.ts:3100`, `runner.ts:858-860`).

## Verdict: the report is correct, and the difference is wider than reported

Pi: `get signal()` returns `runner.getSignalFn()`, bound to `() => this.agent.signal` (`.upstream/v0.99.1/packages/coding-agent/src/core/agent-session.ts:3368`, `runner.ts:917-920`, `agent.ts:336-338`): the signal of the run in progress. It is `undefined` while no run is active, one object for the whole run, aborted with the run, and cleared before `agent_settled`.

PiG, Node runtime (before this lane): the base context's `signal` was `undefined`, but every request context redefined it as that request's own `AbortController` signal (`runtime.mjs` request context, former line ~2004). The report's `runtime.mjs:838` is only the base context, so the Node behavior was "always defined, per request" rather than "undefined". The Go, Python and Rust SDKs had no run signal at all: Go `ctx.Done()`/`Err()` and Rust/Python `is_cancelled()` were the request's cancellation, and `docs/extension-api-parity.md` listed `ctx.signal` as "complete" against that stand-in.

Probed with one fixture (`cmd/pig/testdata/ctx-signal.mjs`) under the real Pi package (0.99.1 build) in RPC mode and under PiG RPC (`TestExtensionContextSignalComparedWithPi`):

| point | Pi | PiG before |
|---|---|---|
| `session_start`, idle command | `undefined` | defined, a fresh signal each |
| `agent_start`, `tool_execution_start`, `tool_call`, tool `execute`, command issued during the run | one object (`s1`), `aborted:false` | a different signal per request |
| handler in flight when the user aborts (`turn_start` held) | `aborted:true`, handler resumes | never aborted (timed out after 3 s) |
| tool in flight when the user aborts | `ctx.signal.aborted === true` | `false` (only the tool's own request signal aborted) |
| `agent_end` after an abort | same object, `aborted:true` | new signal, `aborted:false` |
| `agent_settled` | `undefined` | defined |

Question 3 of the task: it is the run's signal in Pi (aborting the run aborts it while a handler's request is in flight); PiG's was the request's only.

## Fix (Pi-faithful, every path)

- Host (`coding/extension/host/subprocess/run_signal.go`): `ctx.signal` is replicated, as `isIdle` is, because a runtime cannot read the host's `context.Context`. A `run_signal` notify `{run, active, aborted}` precedes every request a runtime serves (`pushStateTo`), and a watcher (`context.AfterFunc` on the run's signal) forwards the abort to every runtime at once. `Conn.beforeCancel` sends the abort before the cancel frame of a cancelled request, so a handler never sees its own cancellation before `ctx.signal` aborted (Pi aborts one signal for both). Per-Conn state is updated under one lock, so a later state never overtakes an earlier one.
- Binding: `ContextActions.GetSignal` / `HostCallbacks.GetSignal` (Pi `ExtensionContextActions.getSignal`, `types.ts:2158`) is bound to the agent's run signal in every mode: `Session.Signal` (SDK Session, `bindExtensionCore`, `bindExtensionCommandActions`), `cmd/pig/session_extension_actions.go` (print, JSON, RPC, bridge host action `getSignal`), and interactive mode (`InteractiveMode.extensionSignal`, context actions and bridge). `TestInteractiveModeBindsEveryHostCallback` now also guards the new callback.
- Node: `ctx.signal` is a getter of the replicated run signal (`runtime.runSignal`, `applyRunSignal`); the request's own AbortSignal moves to a private symbol key (`requestSignal`) used by the runtime's internals (tool `signal` parameter, provider and OAuth callbacks, `event.signal` of `session_before_compact`/`session_before_tree`, autocomplete).
- Go SDK `Context.Signal() context.Context` (nil while no run is active); Python `Context.signal` and Rust `Context::signal()` (`ProviderSignal`, `None` while no run is active); in-process `extension.Context.Signal()`. `Done`/`Err`/`is_cancelled` keep reporting the handler's own request. Answer to question 4: before, a Go extension used `ctx.Done()` (request cancellation); it was never wired to the turn abort except through the host cancelling a tool request, and it was documented as `ctx.signal` "complete".
- Docs: `docs/extension-authoring.md`, `docs/extension-api-parity.md` rows, SDK READMEs, `test/parity/sdk-surface.toml` (the `ctx.signal` row is no longer a stand-in), changelog fragment.

### Tool `signal` parameter identity (lead answer: option b, no divergence)

Pi passes a tool's `execute(id, params, signal, onUpdate, ctx)` the run's signal, which is `ctx.signal` (probed `signal === ctx.signal`: true), and a nested call made with `options.signal` runs with that signal (`runner.ts:979-981`). Implemented: every SDK (Node, Go, Python, Rust) and the in-process `ToolContext.ExecuteTool` mark a call that passed a signal (`ownSignal` on the `executeTool` call, `extension.WithOwnSignal` in-process). The Host puts `own_signal` on the nested `tool_call` request, plus `execute_id` when the caller is on the same connection (executeIds are per connection). The Node runtime (`toolSignal`) hands a tool `ctx.signal` (the run's signal) for every other call, the caller's own signal object for a marked call whose caller is in the same runtime, and the request's cancellation for a marked call from another extension (the callee never held that object) or when no run is active. Go, Python and Rust tools have no signal parameter (the analogue is `Done`/`is_cancelled`), so identity is observable only in Node.

Known edge: a top-level tool whose request is cancelled while the run is not aborted (a runtime shutdown cancelling it) no longer sees its `signal` parameter abort, as in Pi, where the only signal is the run's.

## Red → green evidence

Round 1 (ctx.signal is the run's). Red commit `daaa74788` (`test(extensions): port Pi 0.99.2 ctx.signal tests with signature stubs (red)`): stubs return no signal. Green `0fe41127f`.

| test | red result (before the fix) |
|---|---|
| `cmd/pig` `TestExtensionContextSignalComparedWithPi` (the real Pi package, a 0.99.1 build installed as 0.99.2, Node RPC; scenarios idle, abort a running tool, abort while a turn_start handler is in flight; pig and pi) | pi: pass; pig: fail (rows in the table above) |
| `test/extension-conformance` `TestContextSignalIsTheActiveRunsSignalInEverySDK` (inproc-go, subprocess go/node/rust/python, node packed, fused-go, packed go/rust/python) | 10/10 fail: Node `signal:live` when idle; the rest `signal:none` during a run |
| `coding` `TestSessionBindsExtensionSignalToTheActiveRun` | fail: `ctx.signal is nil during agent_start` |
| `internal/codingagent` `TestExtensionSignalIsTheAgentRunSignal` | fail: `ctx.signal = <nil> during a run` |
| parity scenario `extensions-runtime/68-extension-context-signal-abort` (interactive tmux, Escape abort) | run on the base tree: pig stuck at `signal-live:true`, Pi shows `signal-aborted:true signal-end:true signal-settled:true` |

Round 2 (tool `signal` identity, lead answer b). Red commit `7261a5fee` (`test(extensions): port Pi tool signal identity tests with signature stubs (red)`), green `f57d74a48`.

| test | red result |
|---|---|
| `TestExtensionContextSignalComparedWithPi` (`signal === ctx.signal` in a top-level tool; nested call without and with `options.signal`) | pig: fail (`sameAsCtx` false; the explicit nested tool never saw its caller's signal object, 120 s wait); pi passes |
| `sdk` `TestExecuteToolMarksAnExplicitSignalOnTheWire`, Rust `execute_tool_marks_an_explicit_signal`, Python `test_execute_tool_marks_an_explicit_signal` (the Python test runs under a local `pytest` stub: pytest is not installed here) | fail: no `ownSignal` on the executeTool call |
| conformance own-signal rows (Go, Python, Node placements) and `inproc` `TestRunnerToolContextRunsNestedCallsForTheCallingTool` | fail: the host action's context is not marked |

Green: all pass; scenario 68 matches Pi byte for byte; `make parity-family FAMILY=extensions-runtime` fails only the same nine scenarios that fail on the unchanged base tree (the base fails two more; vendored Pi package in this environment is the previous 0.99.1 build) and `FAMILY=rpc` passes (48/48).

Mutation checks (each applied, the suite failed, restored): no state sync before requests (conformance 9 of 10 fail, RPC 2 fail); no `beforeCancel` hook (RPC tool scenario fails); no abort watcher (conformance 9 fail, RPC held-handler scenario fails); Go SDK ignores `run_signal` (subprocess-go, packed-go, fused-go fail); Python ignores the abort (python, packed-python fail); Rust ignores the abort (rust, packed-rust fail); tool parameter always the request signal (RPC abort and nested scenarios fail); own-signal lookup removed (nested scenario fails); host never marks tool requests (nested scenario fails); Go SDK drops `ownSignal`; in-process drops the mark.

Load: `-race -count=24`, `GOMAXPROCS=4`, `taskset -c 0-3` with four CPU burners for the host run-signal tests, the Session and interactive tests, the SDK `ExecuteTool` tests and the in-process ToolContext test; `-race -count=3` for the Node RPC comparison and `-race -count=2` for the ten-realization conformance test. All green.

### Tests that encoded the old semantics (adapted, not Pi ports)

Each asserted the request's cancellation through `ctx.signal`. Citation for every change: Pi `runner.ts:917-920` (`ctx.signal` is the run's), and `types.ts:1096-1104` (`UserBashEvent` carries no signal).

- `retained_context_node_test.go`: the cancelled command now never settles instead of waiting on `ctx.signal`.
- `runtime_node_extension_api_test.go`, `runtime_node_sdk_surface_test.go`: the fake request context carries the request's signal under `requestSignal`.
- `runtime_node_detached_methods_test.go`: dropped the assignment to the getter-only `ctx.signal`.
- `invocation_admission_test.go`, `node_liveness_test.go`: the fake host sends the `run_signal` abort before its cancel frame, as the real host does.
- `internal/codingagent/interactive_user_bash_subprocess_test.go`: the handler returns after a delay and records `ctx.signal === undefined`; Pi gives a user-bash handler no cancellation.

## Gates and environment notes

The Pi comparison needs `extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent` at the pinned version. 0.99.2 is not installable yet (cooldown), so this worktree used a local copy of 0.99.1 (identical for this surface) with its `package.json` version set to 0.99.2; the directory is gitignored. Failures that predate this lane in `coding` (RPC33 oracle pins 0.99.1), `subprocess` (vendored dist and Pi package 0.99.1 vs pin 0.99.2, Xvfb) are unchanged on the base tree.

## Deferred

- The Node runtime does not call `assertActive` on `ctx.signal` (Pi does for every context getter); no other getter in `RuntimeContext` does either, so the gap is a separate family-wide item.
- Python SDK unit tests need `pytest`, which is not installed in this environment; the Python realization is covered by the conformance rows (subprocess and packed).

## Stubs other families must fill

None.
