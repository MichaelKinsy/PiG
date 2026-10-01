# audit-host-wiring: extension-host features wired only in tests

Lane: audit-host-wiring. Branch: `audit-host-wiring`. Base: `staging/porter/pi-0.99.1` @ `113a10ba9`.

## Scope and method

This lane checked every host-side capability that a subprocess extension (Node, Go, Rust, Python) reaches through the extension host:

- `HostCallbacks` fields and `UIBridge.SetHostAction` keys (`coding/extension/host/subprocess/ui_bridge.go`).
- Command-context actions (`UIBridge.BindCommandActions`).
- Model operations (`codingagent.WireModelOperations`).
- Host extension API methods (`host_extension_api.go`): virtual-model routes, provider registration, and event subscriptions.
- Host-runtime registries: virtual models and MCP servers.
- The event payloads sent to extensions.

For each capability, the audit read the production binding site in each mode and confirmed the result with the real `pig` binary. Upstream reference: Pi binds every Session action in every mode through `AgentSession._bindExtensionCore` (`.upstream/v0.99.1/packages/coding-agent/src/core/agent-session.ts:3275-3408`), called from `bindExtensions` (`agent-session.ts:3173-3197`). The mode-specific parts are `print-mode.ts:74-104`, `rpc-mode.ts:317-351` and `interactive-mode.ts` `bindExtensions`.

SDK/embedded mode does not apply. The `coding` package does not host subprocess extensions, so no `HostCallbacks` exist there. The in-process runner bindings in `coding/session_bind.go` are listed under "Deferred" below.

## Per-mode table

Status legend: **wired** means production binds it. **fixed** means this lane added the production binding. **faithful-unbound** means Pi binds nothing in that mode either. **f6f-node** means lane port-99-f6f-node owns it. **deleted** means the capability had no production caller and no SDK caller, so this lane removed it. **deferred** means the gap is documented below with its owner.

Abbreviations: `sea` = `cmd/pig/session_extension_actions.go` (`bindSessionExtensionActions`, `bindSessionReadActions`, `bindSessionAppendEntry`); `ie` = `internal/codingagent/interactive_extensions.go`; `rpc` = `cmd/pig/rpc_mode_runtime.go` `rpcHost.bindExtensionActions`; `pm` = `cmd/pig/print_mode.go` bind closure; `wmo` = `codingagent.WireModelOperations`, called by `cmd/pig/extensions.go` `wireSubprocessModelRegistry` in headless modes and by `ie` in interactive mode.

| Feature (HostCallbacks field / key) | Interactive | Print / JSON | RPC | Test sites before this lane | Status |
|---|---|---|---|---|---|
| SendMessage, SendUserMessage | ie:626, ie:742 | sea:157, sea:163 | sea | ui_bridge tests via SetActions | wired |
| AppendEntry | ie:749 | sea:388 | rpc:511 | same | wired |
| Exec / ExecContext | ie:590 | sea:254 | sea | same | wired |
| GetSessionName, SetSessionName, GetSessionID, GetSessionFile, GetLeafID | ie:531-549, ie:604 | sea:155-321 | sea | same | wired |
| GetFlag, GetActiveTools, SetActiveTools, RefreshTools | ie:377-405 | sea:168-185 | sea | same | wired |
| GetAllTools, GetCommands | ie:387, ie:394 | pm:348, pm:351 | rpc:502, rpc:505 | same | wired |
| SetLabel, GetEntriesPage, SessionRead | ie:593, ie:572, ie:587 | sea:361, sea:329, sea:372 | sea | same | wired |
| GetThinkingLevel, GetContextUsage, GetSystemPrompt, GetSystemPromptOptions, GetScopedModels | ie:422-565, ie:458 | sea:191-202 | sea | same | wired |
| IsIdle, Abort, HasPendingMessages | ie:436-459 | sea:245-247 | sea | same | wired |
| **SetModel** | ie:500 (fixed: auth check, extra model_select) | **missing → fixed** sea:206 | **missing → fixed** | SetActions only | fixed |
| **SetThinkingLevel** | ie:425 | **missing → fixed** sea:218 | **missing → fixed** | SetActions only | fixed |
| **IsProjectTrusted** | ie:437 | **missing → fixed** sea:230 | **missing → fixed** | SetActions only | fixed |
| **Compact** | ie:474 | **missing → fixed** sea:237 | **missing → fixed** | SetActions only | fixed |
| Shutdown | ie:466 | faithful-unbound (print-mode.ts binds no shutdownHandler) | rpc:495 | SetActions | wired / faithful |
| Reload | ie:473 | **missing → fixed** (`Runtime.ExtensionCommandActions`, `cmd/pig/headless_reload.go`) | **missing → fixed** | SetActions | wired (fix-99-j13) |
| GetGitBranch, GetExtensionStatuses, GetAvailableProviderCount | ie:439-441 | faithful-unbound (footer data exists only in the TUI) | faithful-unbound | SetActions | wired / faithful |
| WaitForIdle, NewSession, Fork, NavigateTree, SwitchSession | ie:283-296 (BindCommandActions) | sea:261 + pm:330 | sea + rpc:269 | SetActions | wired |
| GetModelInfo, GetModel, GetModels, GetModelAuth, Complete, StreamModel, GetModelRegistryState, GetProviderAuth, RefreshModelRegistry | wmo (ie:1486-1609) | wmo | wmo | SetActions | wired |
| GetSettings, GetCallableTools, ExecuteTool | missing | missing | missing | SetActions only | f6f-node |
| FindModel | missing | missing | missing | SetActions only | deleted (see below) |
| GetBranch, GetEntries (`getBranch`/`getEntries`) | ie (dead) | missing | missing | SetActions only | deleted: no SDK calls them; SDKs read through `sessionRead` |
| Virtual models registered by an SDK extension (`registerVirtualModel`) | **missing → fixed** | **missing → fixed** | **missing → fixed** | host runtime tests only | fixed |
| `model_select` event payload to SDK extensions | **malformed → fixed** | **malformed → fixed** | **malformed → fixed** | none | fixed |
| MCP servers registered by an SDK extension; builtin `mcp` extension | missing | missing | missing | inproc tests | deferred |
| Startup trace (`SetStartupTrace`) | installStartupTrace | same | same | — | wired |
| Runtime retention (`SetRuntimeRetention` / `Retain`) | unwired | unwired | unwired | tests only | D70 (approved divergence) |
| `Host.SetCallHandler` | — | — | — | 10 tests | test seam, not an extension capability |

## Bugs found and fixed

1. **Headless modes lacked four Session actions.** Before the fix, print, JSON and RPC had no binding for `setModel`, `setThinkingLevel`, `isProjectTrusted` or `compact`. Pi binds them in every mode (`agent-session.ts:3343-3347`, `2527-2550`, `3369-3379`; `print-mode.ts:74-104`; `rpc-mode.ts:317-351`). Before the fix, Pig returned these answers:
   - `pi.setModel()` failed with "not ready".
   - `ctx.isProjectTrusted()` returned `true` under `--no-approve`.
   - `pi.setThinkingLevel()` did nothing.
   - `ctx.compact()` reported "compaction is not available".

   Fix:
   - `coding/session_extension_core.go` adds `Session.ExtensionSetModel` (false without configured credentials, model-runtime.ts `hasConfiguredAuth`) and `Session.ExtensionCompact` (an owned Session task; results go to `onComplete`/`onError`).
   - `bindSessionExtensionActions` binds all four actions for print, JSON and RPC.
   - A thinking-level persistence failure goes to the error listeners, following Pi's unawaited-action pattern (`agent-session.ts:3303-3310`).
2. **SDK virtual models never reached the Session.** A virtual model registered by an SDK extension never reached any Session's `ModelRuntime`. The host runtime binds only provider callbacks, and the Session uses a separate `ExtensionRuntime`, so the registration stayed queued and `ctx.modelRegistry.find(provider, id)` returned null.
   - Fix: `cmd/pig/extensions.go` `flushExtensionHostVirtualModels`, called by `cliRuntimeBuilder.buildResources` right after the extensions load, binds the host runtime's virtual-model actions to the Services' model registry. As in Pi's `createAgentSessionServices` (`agent-session-services.ts:182-193`), the queued registrations apply before the local refresh, model resolution and Session construction; a failed one is the startup error diagnostic `Extension "<path>" error: <message>` (fatal, no `-ne` hint, `main.ts:907-916`); later registrations and removals apply at once.
   - Review (rev-audit-host-wiring) moved this from a factory-time binding (`bindExtensionHostVirtualModels` after `low.New`), which bound too late for Session restore and reported a failed flush to no listener.
   - Print and interactive modes also pass their extension host to the Session factory (`print_mode.go`, `print_mode_build.go`, `interactive_build.go`).
3. **`model_select` reached SDK extensions in the wrong shape.** The event serialized `*ai.Model` with Go field names, so `event.model.provider` and `event.model.id` were `undefined` in every SDK and every mode. Pi passes its Model object (`agent-session.ts:2372-2384`).
   - Fix: `coding/extension/host/subprocess/event_payload.go` `wireEventPayload` projects `model` and `previousModel` through `extension.ModelInfo`. An absent previous model is omitted, so it stays undefined.
4. **Interactive `setModel` emitted an extra `model_select` and skipped the credential check.** The interactive `setModel` action emitted its own `model_select` in addition to the Session's. That second event was malformed (no provider), and it also fired when the model had not changed. The action also skipped Pi's credential check.
   - Fix: the action calls `InteractiveSessionHandle.ExtensionSetModel`. The Session then answers false without credentials and emits `model_select` only for a changed model.
5. **Caller-free host callbacks deleted:**
   - `HostCallbacks.FindModel`: `ModelRuntime.ResolveModel` resolves the route's provider/id itself (`model-runtime.ts:1000-1009`), so the route now names only provider and id.
   - `HostCallbacks.GetBranch` and `HostCallbacks.GetEntries`, with their keys, handlers and interactive bindings: no SDK calls them.

   The affected tests in `extension_api_wire_test.go`, `frame_limit_test.go` and `test/extension-conformance` now use the surviving paths.

## Guard

The guard tests are table-driven over the production wiring constructors (reflection over `HostCallbacks`, not a grep):

- `cmd/pig/extension_host_wiring_guard_test.go`:
  - `TestPrintModesBindEveryHostCallback` runs `runPrintMode` (text and JSON) with a real bridge.
  - `TestRPCModeBindsEveryHostCallback` runs `rpcHost.bindExtensionActions`.
  - `TestBuildFlushesTheExtensionHostVirtualModels` covers queued, conflicting, late and unregistered virtual models.
  - `TestExtensionVirtualModelFlushFailureStopsStartup` (binary, print/JSON/RPC) matches upstream 0.99.1's startup error for a conflicting virtual model.
- `internal/codingagent/extension_host_wiring_guard_test.go` `TestInteractiveModeBindsEveryHostCallback` runs `attachSubprocess` and `wireInprocContextActions`.

Each allowance set is checked in both directions. A mode that leaves a field unbound without an allowance fails. An allowance for a field that the mode now binds also fails, so port-99-f6f-node must delete its three allowance entries when its bindings land. The allowance sets are:

- `nestedToolAllowances`: the f6f-node trio.
- `headlessFooterAllowances`: faithful.
- Print `Shutdown`: faithful.
- `headlessReloadAllowance`: deleted by fix-99-j13; the guards require `Reload` bound in print, JSON and RPC mode.

## Real-binary tests

- `TestHeadlessExtensionSessionActionsMatchPi` (print, JSON, RPC) with fixture `cmd/pig/testdata/host-wiring-session-actions.mjs`.
- `TestSubprocessVirtualModelReachesTheModelRuntime` (print, JSON, RPC) with fixture `cmd/pig/testdata/host-wiring-virtual-model.py`.
- `TestInteractiveExtensionHostWiringMatchesHeadlessModes` (Linux PTY) with both fixtures.
- Unit test `TestModelSelectEventCarriesPiModelShape`.

The expected records come from Pi 0.87.1 run on the same fixture with `--no-approve` in print, JSON and RPC mode. All three modes produced identical records. The npm upstream 0.99.1 package is not installed in this environment. The bound code paths are unchanged from 0.87.1 to 0.99.1 (`.upstream/v0.99.1/.../agent-session.ts` lines above).

These tests were derived from upstream behavior; no upstream test covers this wiring, so none were ported. Pi's records on the final fixture:

```
{"event":"trusted","value":false}
{"event":"model_select","model":"probe/probe-model-2"}
{"event":"setModel","value":true}
{"event":"compact","error":"Nothing to compact (session too small)"}
{"event":"thinking_level_select","level":"high"}
{"event":"entries","value":["model_change:probe/probe-model","thinking_level_change:medium","model_change:probe/probe-model-2","thinking_level_change:high"]}
```

## Red / green evidence

- Red `939806f51`: the tests and a panicking `rpcHost.bindExtensionActions` stub.
  - All guards failed, and the RPC guard panicked on the stub.
  - Headless session actions and the virtual model test failed in print, JSON and RPC. Pig answered `isProjectTrusted` true, `setModel` "not ready", no `thinking_level_select`, compact "not available", and virtual model null.
  - The interactive guard failed because interactive mode left `HostCallbacks.FindModel` unbound. Green deletes that callback.
- Green `253221cac`: every test above passes.
- Tests added in green, and the mutations that make them fail:
  - Disabling `wireEventPayload`'s projection makes `TestModelSelectEventCarriesPiModelShape` fail.
  - Restoring the interactive explicit `model_select` emit makes the PTY test fail with a duplicate, malformed `model_select`.
- Fixture change in green: the red fixture called `setThinkingLevel` before awaiting `ctx.compact` callbacks. Pig then delivered `thinking_level_select` after the compact error because of the Node runtime bug below.
  - The green fixture compacts first and switches to a second model, so it also proves `model_select`.
  - Its expectations are Pi's output for that fixture.
- More mutations:
  - Disabling the build-time `flushExtensionHostVirtualModels` call fails the virtual model test and the flush-failure test in all three headless modes.
  - Dropping the headless `setModel` binding fails the print, JSON and RPC guards with "leaves HostCallbacks.SetModel unbound".
- Load test: `GOMAXPROCS=4 taskset -c 0-3` with 4 CPU burners, `-race -count=24`, on the new cmd/pig guards and binary tests, the interactive guard and the event-payload test. All passed (cmd/pig in 514s).
- Broad run (`./coding/... ./internal/codingagent/... ./cmd/pig/... ./test/extension-conformance/...`): every failure also fails on the base commit `113a10ba9`, with identical failing test sets. The causes are the missing pinned npm Pi package and other lanes' red stubs. This includes `TestPythonSDKVirtualModelRegistrationAndRouting` and `TestRustSDKVirtualModelRoute`, which expect the host route itself to reject an unknown physical model and fail the same way on base.

## Deferred, with owners

- **MCP servers registered by SDK extensions**: they are stored in the host runtime. The Session runner and the `mcp` builtin read the Session's runtime, so no `mcp_servers_change` event fires and no unhandled-server report is made. The builtin `mcp` extension (`extensions/index.ts`) is not registered in production; only `pig mcp` exists. The fix needs runtime unification plus the builtin registration seam. Owner: the MCP family (family 8).
- **Node `ctx.compact()` ordering**:
  - Cause: `runtime.mjs` `compact()` sends its host call without the parent request, so an awaiting handler never reports `blocked`. Later events to the same extension (`thinking_level_select`, `session_before_compact`) then queue behind the handler.
  - Repro: in the red fixture, adding one awaited host call between the two calls restores Pi's order.
  - Hand-off: posted to `questions/port-99-f6f-node.md`.
- **Interactive `/model` and model cycling**: they still emit their own `model_select` through `modelToExtModel` (no provider) after `SessionHandle.SetModel` has emitted one. The sites are `internal/codingagent/interactive_commands.go:697` and `interactive_models.go:234`. Cycling also needs the Session to emit source `cycle`. Owner: model-resolver-selector family.
- **In-process runner**: `coding/session_bind.go` `bindExtensionCore` lacks `setModel` and related actions for in-process runners. This is outside the subprocess host scope.
- **Test seam**: `Host.SetCallHandler` is used only by 10 tests. Deleting it is a follow-up.
- **Stale docs**: `docs/additive-features.md` D20 says Session replacement "keeps one Host". Retention (`SetRuntimeRetention`/`Retain`) is unwired, as the approved divergence D70 records. That D20 sentence needs correction by the docs owner.

## Stubs for other families

None. The red stub `rpcHost.bindExtensionActions` is implemented in this lane. port-99-f6f-node adds its three `SetHostAction` bindings to `bindSessionExtensionActions`/`rpcHost.bindExtensionActions` and to `ie`, then deletes their entries from `nestedToolAllowances` (`cmd/pig/extension_host_wiring_guard_test.go`) and from `allowed` (`internal/codingagent/extension_host_wiring_guard_test.go`).

## Gates

- `go build ./...`, `go vet` and `GOOS=windows go vet` on the touched packages: clean.
- gofmt, `go fix -diff`, and `go tool golangci-lint run --allow-parallel-runners --build-tags=integration,live,parity` on `cmd/pig`, `coding`, `coding/extension/host/subprocess`, `internal/codingagent` and `test/extension-conformance`: clean (0 issues).
- `go test -race` on `coding`, `coding/extension/host/subprocess`, `internal/codingagent` and `cmd/pig`:
  - No data races.
  - Every failing test also fails on base `113a10ba9`.
  - `TestNodeRuntimeShimsExportEveryPinnedPiValue` passed in the non-race run but failed in the race run. It also fails when run alone on base.
- `make parity-family FAMILY=extensions-runtime`: 19 failures, all of which also fail on base. Base fails the same 19 plus `65-dynamic-tools` and `66-session-replacement-lifecycle`. The installed comparator is Pi 0.87.1 because the npm upstream 0.99.1 package is cooldown-blocked.
- `make generate`:
  - It stops at `interface-proposals` because `test/parity/interface-extractor` `npm test` throws `ReferenceError: os is not defined` (`behavior-input-inventory.test.mjs:247`). This is a base bug, not caused by this lane.
  - `known-gaps` and `custom-factory-ledger` fail on base on upstream-test hash and theme-member drift.
  - `test-inventory-generate` rewrites `upstream-tests-v0.99.1.json` in ways unrelated to this lane. Those changes were left for the lead's central regeneration.
  - The only generated file this lane affects is `test/parity/interfaces/pig-go.json` (regenerated; `interface-go-drift` is clean).
- `python3 automation/ci/check-public-claims.py`: no contradicted claims.
