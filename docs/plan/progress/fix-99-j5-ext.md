# fix-99-j5-ext: joint run 5 and parity-fast fixes (extension, conformance, compaction correspondence, clipboard)

Base `113a10ba9` (staging `porter/pi-0.99.1`). Branch `fix-99-j5-ext`.

## Item 1: `ctx.sessionManager` identity (regression from 8b)

Pi's `ctx.sessionManager` is the `SessionManager` the AgentSession owns: `runner.ts:889-891` returns `runner.sessionManager`, which `agent-session.ts` binds from `this.sessionManager`. Go's counterpart is `s.inner` (`*codingagent.Session`), so `bindExtensionCore` and `bindExtensionCommandActions` bind `s.inner` again (the second one had bound the `*coding.Session` wrapper since `0e7f51d61`, so the last binding of a Session-hosted runner was the wrapper wherever no runtime replacement re-ran `bindExtensionCore`).

codemode needs no session wrapper: upstream's tool reads the branch through `ctx.sessionManager.getBranch()` (`codemode/execute.ts:285`) and writes through the closure `pi.appendEntry` (`codemode/index.ts:35`), which runs the `appendEntry` action of `agent-session.ts:3321-3327` (append, then `entry_appended`). A declarative Go extension has no `pi` closure, so `ToolContext.AppendEntry` (backed by `ToolActions.AppendEntry`, bound to `coding.Session.AppendCustomEntry`) is that path. It rides `ToolActions` (with `ExecuteTool` and `GetCallableTools`) because production modes call `Runner.BindCore` with their own `ExtensionActions` and the Session re-binds its tool actions through `BindTools` afterwards; an `ExtensionActions.AppendEntry` would be lost there. First attempt (kept in history: red commit `a2f85ae1c`) used `ExtensionActions.AppendEntry`; the second red commit corrects the test to `ContextActions.ToolActions`.

Binding `s.inner` statically goes stale when `coding.Session.ReplaceInner` swaps the log in place (`/new`, `/resume`, `session_ops.go`), and pins the replaced log (`TestIdleReplacementReleasesOldCacheWarmingSessions` failed). Pi builds a new AgentSession and binds a new runner (`agent-session-runtime.ts`), so Go's in-place swap rebinds: `Runner.BindSessionManager` (an atomic, so a rebind is race-free while handlers create contexts) is called from `ReplaceInner`. `BindCore` and `BindTools` store the manager there too, and every context snapshot goes through `Runner.actions()`.

Commits (red then green): `a2f85ae1c` red (stub `ToolContext.AppendEntry`; fails: `TestReplacedSession2860New` panic `interface {} is *coding.Session, not *codingagent.Session`, `TestRunnerToolContextAppendsEntriesThroughTheBoundAction`), `5f587b3b3` red (`TestReplaceInnerRebindsTheSessionManagerExtensionsSee`, `TestIdleReplacementReleasesOldCacheWarmingSessions` with s.inner bound), `4a6d60280` green. Added after green: `TestRunnerBindSessionManagerWhileContextsAreCreated` (race). Mutation checks: dropping `AppendEntry` from `toolActions` fails `TestUpstreamCodemodeOptionsAndStore/persists_store()…` and `…/loads_the_values…`; binding `s` fails `TestReplacedSession2860New` (panic above); removing `BindSessionManager` from `ReplaceInner` fails `TestReplaceInnerRebinds…` and `TestIdleReplacementReleases…`.

The four callers named in the task (`session_replaced_2860_upstream_test.go:88,95,135`, `session_replaced_lifetime_test.go:83`, `conformance_test.go:1541` via `session_identity_test.go`) pass unedited. `session/27-replaced-session-context` no longer fails in `make parity-family FAMILY=session` (the 5 remaining failures there, 11, 21, 28, 29, 31, are the 9a theme colors: `38;2;138;190;183` versus `38;5;5`).

## Item 2: Rust and Python virtual-model routes

`dcd2035fb` made the subprocess route return the model naming the provider and id the extension answered and left the rejection to the model runtime: `model-runtime.ts:1000-1009` (`… routed to <p>/<id>, which is not a physical model.`) and `loader.ts:485-487` (the extension's route is returned as is). It updated `TestVirtualModelDeclarationRoutesThroughTheExtension` but not the Rust and Python conformance rows, which still expected the route call to fail. Both now assert the route names the pair the router answered (`conformance/phys-retry-2-false`, `anthropic/py-picked-retry`) with a nil error. This is a test-expectation fix with the upstream citation; no production change (the Node and Go SDK rows already pass). Not red-proven in the usual order: the failure (`route error = <nil>`) predates this lane.

## Item 3: `TestSelectedToolsPiContract`

`selected-tools-pi.mjs` builds an `AgentSession` with `Object.create(AgentSession.prototype)`, which skips class field initializers. 0.99.1's `_getToolExposure` (called from `_applyToolLoadout`) reads `this._toolDefinitions`, declared `private _toolDefinitions: Map<...> = new Map()` at `agent-session.ts:442`. The harness now sets `_toolDefinitions: new Map()`. The probe needs `extensions/sdk-ts/node_modules` (upstream 0.99.1 installed; ignored by git, copied from another worktree because the npm cooldown blocks a fresh install here).

## Item 4: compaction correspondence

`coding/session.go#compact` has no call to `compaction.Compact` because 0.99.1 moved the summary call into `_runDefaultCompaction` (`agent-session.ts:2633-2661`), the one caller of the lower-level `compact()` for manual and automatic compaction; Go mirrors it as `Session.runDefaultCompaction` in `coding/session_summarization_auth.go`, which calls `compaction.Compact` once. The code is faithful; the correspondence caller spec (`test/parity/correspondence/golang.go`) and its fixture now name that caller. `TestAddGoFunctionCallersRetainsAllReviewedCallSites` and `TestExtractGoCurrentSettingsAndPrompts` pass and the `has 0 calls to compaction.Compact` error is gone from `cmd/correspondence`. `TestCompactionSettingsCorrespondenceCurrentPin`, `TestExtractTypeScriptCurrentPin` and the `cmd/correspondence` current-pin tests still fail on the settings table (`fullscreen-wheel-scroll-lines` missing, `settings-selector` order; 40 versus 41 mappings): a different owner (settings, not in this lane's list).

## Item 5: `TestRunClipboardCommandEncodesJavaScriptUTF8`

Reproduced under 13 CPU burners pinned to one core (`taskset -c 2`): five of the seven rows hit the fixed 5 s deadline (`signal: killed`). The child is this test binary; a `-race` build alone takes 1.1 s at exit (`GORACE` `atexit_sleep_ms` defaults to 1000) and start takes seconds on a loaded host. `TestRunClipboardCommand` already uses a 30 s deadline for that reason; this test now uses the same. The deadline is not under test (the 3 s default of `clipboard-command.ts` applies to Pi's callers). After the change the same load passes. The sibling `TestClipboardImageCommandEnforcesDefaultBufferLimit` (same file family, not on the list) failed the same way on the production 3 s deadline, even with four burners on the four pinned cores (`52428800` row, `signal: killed`); its subject is the 50 MiB bound, so it uses the same generous deadline and passes three runs under that load in a `-race` build.

## Gates run

`go build ./...`, `go vet` and `GOOS=windows go vet` for `coding`, `coding/extension`, `coding/extension/host/inproc`, `coding/extension/builtin/codemode`, `internal/codingagent`, `test/parity/correspondence`; `gofmt`; `go fix -diff` (touched packages: empty); `golangci-lint` on those packages plus `test/extension-conformance` (0 issues). `go test -race` for `coding/extension`, `inproc`, `codemode`; `go test -race -count=24` with `GOMAXPROCS=4`, `taskset -c 0-3` and four burners for `inproc` and the replacement, codemode-store and `ReplaceInner` tests in `coding`. `go test ./test/extension-conformance` (848 s) passes.

## Failures that remain, none introduced here (same at `a2f85ae1c` unless noted)

- `coding`: `TestUpstreamAgentSessionCodemodeTool` (two rows: `structuredContent`, 6G), `TestUpstreamCodemodeOptionsAndStore/resolves_bash_calls…` (`structuredContent`), `TestUpstreamCodemodeModels` (classifier family).
- `cmd/pig`: the family 7 RPC `InputEnd…` rows, `TestRPCPlanModeEmptyToolsSurviveProcessResume`, `TestRPCFreshPromptDeclaresStructuredSystemOnce`.
- `internal/codingagent`: `TestInteractiveThemeSelectionPresenceMatchesPi` (9a).
- `test/parity/cmd/sdksurface`, `test/parity/cmd/correspondence` settings-table findings: other lanes.

## Deferred and stubs for other families

No stubs for other families. Deferred: nothing from the five items. Generated files (interface inventory and recommendations) are regenerated in the last `chore(port-99)` commit.
