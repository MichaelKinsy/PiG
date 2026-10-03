# fix-114-tui-models

Gap: a codemode script sees `models` in `pig -p` but not in the interactive TUI (demo-040 Gap 1).

## Root cause

Pi's `ExtensionRunner` takes the `ModelRegistry` as a constructor argument and no `bindCore` replaces it (runner.ts:363, 399-406, 835, 893-895 in v0.99.2, identical in v1.0.0). A mode's `bindExtensions` reaches `bindCore` only through `_bindExtensionCore` (agent-session.ts:3185, 3287-3313 in v0.99.2). Go's `inproc.Runner.BindCore` replaced the whole `ContextActions`, so the interactive mode's `wireInprocContextActions` (which binds no `ModelRegistry`) cleared the registry the Session had bound. Every in-process built-in then saw `ctx.modelRegistry` as nil: codemode dropped `models`, and `builtin:mcp` read no provider token. The runtime's provider actions (`registerProvider`, virtual models) were not affected: `BindProviderActions` skips a binding without a registry, so they kept the Session's binding.

Fix at the source: `BindCore` keeps the Session's registry when the mode omits it, like the existing fallbacks for `SessionManager`, `ExecuteTool`, `GetCallableTools`, `AppendEntry` and `RefreshTools`. The replacement path needs no extra field: `/new` and resume build a replacement runner that the Session binds (`coding/runtime_replacement.go:190`), and `rebindCurrentSession` then re-runs `wireInprocContextActions`, which now keeps it. No `InteractiveOptions` field is added, so the scratch patch is not needed.

## Commits

1. `273aafa5e` test(interactive): ... (red)
2. `e4dab069e` fix(extension): a mode's BindCore keeps the Session's ModelRegistry (green)

## Red run (before the fix)

- `TestRunnerBindCoreKeepsTheSessionModelRegistryAModeOmits`: `ctx.modelRegistry after a mode binding = <nil>, <nil>; want the Session's registry`.
- `TestInteractiveCodemodeScriptsReachModelsThroughReplacement`: `initial interactive Session: ctx.modelRegistry = <nil>`; the script itself failed with `ReferenceError: models is not defined` (same symptom as the TUI).
- The red commit's classifier `questions` literal had the wrong shape (a test bug, independent of the fix, hidden by the earlier failure). The green commit corrects it to Pi's shape (`{ approved: { type: "bool", ... } }`, codemode_session_upstream_test.go:539). Nothing else in the tests changed.

## Evidence

- Mutation: disabling the fallback fails both tests.
- `-race -count=24` on the inproc BindCore tests and the interactive test; `GOMAXPROCS=4`; `taskset -c 0-3` with 4 burner loops (PIDs recorded, killed): all pass.
- Whole-suite comparison on `coding/...`, `cmd/pig`, `internal/codingagent` (-short) against the base commit: no new failing test. The remaining failures exist on the base too and need `extensions/sdk-ts/node_modules` (Pi npm package, cooldown-blocked) or Node/Pi comparison fixtures.
- Gates: build, vet, `GOOS=windows go vet` (inproc, codingagent), gofmt, golangci-lint (0 issues), `go fix -diff`.

## Audit of other in-process readers of ctx.modelRegistry

- `coding/extension/builtin/codemode/models.go` (`modelRuntimeOf`): the reported bug.
- `coding/mcpext/register.go` (`ProviderToken`): same cause; covered by `requireSessionRegistry` (asserts `GetAPIKeyForProvider`).
- `inproc.Runner.BindCore` provider/virtual-model actions: not affected before the fix (`BindProviderActions` ignores a binding without a registry). The inproc unit test's `pi.registerProvider` check passes with the fix reverted; it guards that the rebind the fix now triggers targets the Session's registry.

## Not covered

- No PTY test: the faux provider (parity-bound to Pi) cannot script a codemode tool call, and a Node fixture command's context has no `executeTool`. The interactive test drives the real `InteractiveMode` (TestHarness, `wireInprocContextActions`, `rebindFromRuntime` -> `applyReplacement`) with a real Session, the built-in codemode extension and a fake classifier provider, before and after a replacement.

## Check against the v1.0.0 tag

Same contract: runner.ts:363-406 (constructor field), 468-541 (registerProvider/unregister/virtual models), 835, 893-895 (`ctx.modelRegistry`); agent-session.ts:3313-3339 (`_bindExtensionCore`). No difference. The citations use v0.99.2 lines, which match v1.0.0 in runner.ts.
