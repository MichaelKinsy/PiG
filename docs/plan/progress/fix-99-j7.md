# fix-99-j7: leftovers from the host-wiring audit review

Lane: fix-99-j7. Branch: `fix-99-j7`. Base: `rev-audit-host-wiring` @ `c72e3fb72`. Pi reference: 0.99.1 (`.upstream/v0.99.1`), probed with the npm build at `/tmp/sw/pi` in `-p`, `--mode json`, `--mode rpc` and a PTY, always with a temporary `HOME` and `PI_CODING_AGENT_DIR`. Every Pig run used a temporary `HOME`, `PIG_HOME` and `PIG_CODING_AGENT_DIR`.

## Upstream tests

None of the five items has an upstream test file: upstream 0.99.1 tests neither startup virtual-model selection, warning-only exit status, interactive `model_select` counts, `ctx.compact` without callbacks nor the trust binding (`test/suite/virtual-models.test.ts`, `agent-session-model-extension.test.ts` and `trigger-compact-extension.test.ts` cover other behavior and are ported by their own lanes). The red tests therefore encode upstream 0.99.1's observed behavior, probed as above, and cite the Pi source.

## Items

| # | Pi behavior | Fix | Tests |
|---|---|---|---|
| 1 | `--model router/auto`, the saved default and the `--models` scope resolve against `modelRuntime`, whose catalog and auth include the virtual models the services registered before resolution (`main.ts:803-819`, `model-runtime.ts:285-293, 543-545, 953-958`, `agent-session-services.ts:182-193`). Pi starts on `router/auto`. | `ModelRuntime.RuntimeModels` and `ModelRuntime.HasConfiguredAuth` (`coding/virtual_models.go`) compose the registry's models with the virtual ones; `selectStartupModel` and `extensionScopedModels` read them; `buildModelFromRef` returns a virtual model from the runtime. | `TestSelectStartupModelSelectsRegisteredVirtualModel` (5 cases), `TestStartupModelFlagSelectsExtensionVirtualModel` (print, json, rpc), `TestRuntimeModelsListTheVirtualModelsAndTheirAuth` |
| 2 | Only an error diagnostic exits 1 (`main.ts:908-916`); warnings print and startup continues. | `cmd/pig/main.go` gates on `modelErr` or an error-type extension diagnostic. | `TestWarningOnlyExtensionDiagnosticsDoNotStopStartup` (print, json, rpc); interactive mode shows the warning in the chat (`showStartupDiagnostics`), checked by hand in a PTY |
| 3 | `ctx.compact` calls `session.compact` and drops the outcome without callbacks (`agent-session.ts:3369-3379`). | Interactive `compact` hands its options to `InteractiveSessionHandle.ExtensionCompact` (`Session.ExtensionCompact`); the mode's own goroutine, its stderr write and `compactForExtension` are deleted. | `TestInteractiveExtensionCompactWithoutCallbacksIsTheSessionsAndSilent`, `TestInteractiveExtensionCompactPassesCallbacksToTheSession`, `TestInteractiveExtensionCompactWithoutASessionReportsToOnError` |
| 4 | One `model_select` per changed selection, from the Session: source `set` for `/model`, `cycle` for the cycle key (`agent-session.ts:2372-2384, 2411, 2480, 2512`). | `/model` and the cycle key no longer emit; the cycle calls `InteractiveSessionHandle.CycleToModel`. `emitModelSelect` had no caller left and is deleted. | `TestInteractiveModelCommandEmitsNoModelSelectOfItsOwn`, `TestInteractiveModelCycleReachesTheSessionAsACycle`, `TestInteractiveExtensionSetModelEmitsNoModelSelectOfItsOwn`, `TestInteractiveModelSelectionEmitsOneModelSelectEach` (PTY) |
| 5 | `ctx.isProjectTrusted()` answers from the Session's settings manager (`agent-session.ts:3355`). | With no Session and no mode answer, the binding answers false. The host's own unbound default stays true (`runner.ts:368`). | `TestHeadlessIsProjectTrustedFailsClosedWithoutASession`, `TestHeadlessIsProjectTrustedUsesTheModesAnswer` |

## Red run (commit `test(host-wiring): port upstream 0.99.1 ...(red)`)

- `TestSelectStartupModelSelectsRegisteredVirtualModel`: 5 of 5 cases fail (`--model router/auto` selected `openrouter/auto-beta`; `--provider router` "Unknown provider"; scope and saved default selected nothing).
- `TestStartupModelFlagSelectsExtensionVirtualModel`: print, json, rpc fail (`start_model` `openrouter/openrouter/auto-beta`).
- `TestWarningOnlyExtensionDiagnosticsDoNotStopStartup`: print, json, rpc fail (exit 1, the warning line was right).
- `TestHeadlessIsProjectTrustedFailsClosedWithoutASession` fails (answered true).
- `TestInteractiveModelCommandEmitsNoModelSelectOfItsOwn`, `...CycleReachesTheSessionAsACycle`, `TestInteractiveExtensionCompactWithoutCallbacksIsTheSessionsAndSilent`, `TestInteractiveExtensionCompactPassesCallbacksToTheSession` fail.
- `TestInteractiveModelSelectionEmitsOneModelSelectEach` fails with four records: `undefined/probe-model-2` duplicates, and the cycle's Session event said source `set`.
- Passing in red by design (behavior to keep): `...ExtensionSetModelEmitsNoModelSelectOfItsOwn`, `...CompactWithoutASessionReportsToOnError`, `...UsesTheModesAnswer`.

Stubs: `InteractiveSessionHandle.CycleToModel` and `.ExtensionCompact` (`internal/codingagent/session_handle.go`); `*coding.Session` already implements both, so no other family fills anything.

## Mutation checks

- `RuntimeModels` loop over virtual providers emptied: `TestRuntimeModelsListTheVirtualModelsAndTheirAuth` fails.
- `HasConfiguredAuth` `||` turned into `&&`: same test fails.
- `buildModelFromRef` virtual branch disabled: `TestSelectStartupModelSelectsRegisteredVirtualModel` fails.
- The other fixes are red-proven by the red run above.

## Deferred

Nothing from the lane's five items. Out of scope, unchanged: the Node runtime's `pi.registerVirtualModel` (port-99-f6f-node), headless reload, MCP, and the interactive `/model` picker and cycle list, which read the static catalog plus the registry's dynamic providers and so do not list virtual models (Pi's read `modelRuntime.getAvailable()`).

## Verification

- `go build ./...`, `go vet ./...`, `GOOS=windows` and `GOOS=darwin` vet of `cmd/pig`, `coding`, `internal/codingagent`, `gofmt`, and `golangci-lint` on `./cmd/pig/... ./coding/ ./internal/codingagent/...` (0 issues). `go fix -diff` reports one pre-existing change in `coding/extension/host/runtimecell/build_failure.go`, which this lane does not touch.
- `go test` of `cmd/pig`, `internal/codingagent`, `coding`, `coding/extension` with the pinned Pi installed by `make parity-deps`: every failing test also fails on base `c72e3fb72` (`cmd/pig`: `TestLinkedPackagesCompileNoRegexpAtInit`, `TestRPCFreshPromptDeclaresStructuredSystemOnce`, four `TestRPCInputEnd...ComparedWithPi`, `TestRPCPlanModeEmptyToolsSurviveProcessResume`, `TestSystemPromptOptionsThroughHeadlessStartup`; `internal/codingagent`: `TestInteractiveThemeSelectionPresenceMatchesPi`; `coding`: `TestReplacedSession2860New`, `TestUpstreamAgentSessionCodemodeTool`, `TestUpstreamCodemodeModels`, `TestUpstreamCodemodeOptionsAndStore`). `go test -race` of `internal/codingagent`, `coding`, `coding/extension/...` shows only those.
- Load: new and changed unit tests `-race -count=24` with `GOMAXPROCS=4` on `taskset -c 0-3`: pass (`internal/codingagent`, `coding`, `cmd/pig` unit tests). The PTY and binary tests `go test -race -count=4` under `taskset -c 0-3` with four CPU burners on those cores, about 320 s: pass. A test binary run outside `go test` cannot find Node through the mise shim with a temporary `HOME`, so the binary tests run only through `go test`.
- Parity scenarios against the pinned upstream 0.99.1: `model-resolver-selector` 03, 05, 11 and `extensions-runtime` 36, 44, 45 pass. `startup/04`, `model-resolver-selector` 07, 16, 17, 21 fail on escape sequences only (PiG identity text in `startup/04`; theme colors in the others); the visible text of 16 is identical. No scenario of those runs exercises a path this lane changed, and `make parity-family` stops at the first of these. They are not attributed to this lane but were not compared against base, because the base checkout's runner could not locate the comparator.
- `TestRuntimeModelsListTheVirtualModelsAndTheirAuth` was written with the fix; it is mutation-proven (two mutations fail it), not red-proven.
- Behavior note: the cycle key's Session call still blocks on the extension round trip, as `SetModel` did before (it runs the same completion). This lane did not move it off the input loop.
