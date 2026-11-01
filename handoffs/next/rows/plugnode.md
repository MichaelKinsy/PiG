# plugnode: `chord/nextnode/plugins` evaluated for real (Pi 2c8d1feb, `packages/chord/src/next-node/plugins`)

Replaces the Builder/Evaluator seams of kfold-plugA with the real thing (owner decision 2026-11-01; design: docs/plan/chord-plugins-node-evaluator.md). The rows below supersede the kfold.md rows of the same files (`# Part: Plugins package`). Tests run the real esbuild build (Go API, 0.28.2: the scripts are byte-identical to Pi's: `TestBuildMakesTheSameScriptsAsPisBuildFacetForTheFixtures`, `TestExample39BrowserBuild` assert Pi's digests, sizes and content-hashed names) and the real Node subprocess (Node 24.22.1) running Pi's own evaluate.ts, stack.ts, runtime.ts and chord host, vendored verbatim under `chord/nextnode/plugins/node/pi`.

## (a) Upstream src files

path | disposition | PiG files | evidence | rationale
--- | --- | --- | --- | ---
next-node/plugins/build.ts | ported | plugins_build.go, plugins_sourcemap.go | test:chord/nextnode/plugins/plugins_build_test.go#TestEsbuildBuildInlinesOwnFilesAndAnotherPluginsFileKeepsHostPackagesExternal | `BuildFacet` on esbuild's Go API with every option Pi passes; `ImportRules` is an `api.Plugin`; `freeGlobals` reads the inline source map (VLQ, Node `findEntry` semantics); columns converted from bytes to UTF-16 units as the JS API reports them
next-node/plugins/evaluate.ts | ported (runs vendored, verbatim) | node/pi/chord/src/next-node/plugins/evaluate.ts, node/pi/chord/src/next-node/compression.ts (nodeCompression, vendored), node/host.mjs, plugins_runtime.go | test:chord/nextnode/plugins/plugins_evaluate_test.go#TestEvaluateThrowsAnImportTimeThrowANonFacetExportAndARefusedRequireNamingTheFacet | Go has no `vm`; Pi's file runs in the Node subprocess (AGENTS.md: subprocess-only runtimes). Go sees reports and `*NodeError`
next-node/plugins/stack.ts | ported (runs vendored, verbatim) | node/pi/chord/src/next-node/plugins/stack.ts | test:chord/nextnode/plugins/plugins_stack_test.go#TestStackMapsAPluginScriptsPositionsToThePluginsFilesAndLines | `mapStack` runs in the subprocess; a `*NodeError.Stack` is its output
next-node/plugins/runtime.ts | ported (runs vendored, verbatim) + Go client | plugins_runtime.go (`PluginRuntime`, `NodeOptions.OnActivity`, `NodeHost.Activity`, `RuntimeLoad` (no `Parent`; `CloseTimeout`, `ReadyTimeout`, `Compression`, `Heartbeat`), `PluginScript`), plugins_node.go (`NodeHost`, `NodeOptions`, `NodeError`, `NodeExit`) | test:chord/nextnode/plugins/plugins_runtime_test.go#TestRuntimeReloadsACompleteSetSameDigestsUnchangedAChangedOneAppliedALoadedOneNotShippedRemoved | per-host loaded facets, `adopt`, reload queue are Pi's, in the subprocess; the V8 compilation-cache flag runs there too
next-node/plugins/plugins.ts | ported | plugins_plugins.go | test:chord/nextnode/plugins/plugins_plugins_test.go#TestPluginsFixRound1SerializesReloadsOfOneHostTwoStartedTogetherKeepBothChanges | `CreatePlugins` returns the concrete `*Plugins`; `Load`/`Reload`/`Step` take a `*NodeHost`; a Go panic propagates (no recover into a plugin failure)
next-node/plugins/discover.ts, install.ts, watcher.ts | ported | unchanged from kfold.md | unchanged | unchanged
next-node/plugins/index.ts | designed-out | none | n/a | barrel: Go imports the package
(no Pi file) | additive | node/host.mjs, plugins_node.go, plugins_node_listen_{unix,windows}.go, automation/gen/vendor-chord-plugins-node.{sh,mjs}, node/manifest.json | test:chord/nextnode/plugins/plugins_node_test.go#TestNodeVendoredFilesAreExactlyTheManifests | the Go side of what Pi's workers do with a forked child: framed socket (kind 0 chord link message via `link.IpcLink` / Pi's `ipcLink`, kind 1 JSON control), Node discovery as runtime-node does (>= 22.19.0, Pi's `engines`), embedded tree extracted once per content hash

## (b) PORT_MAP rows

next-node/plugins/build.ts, evaluate.ts, stack.ts, runtime.ts, plugins.ts: partial -> behavioral (tests above). The `designed-out (esbuild / vm)` rationale of kfold.md is withdrawn.

## (c) Upstream tests

Pi file | PiG test file | notes
--- | --- | ---
build.test.ts (all 11 `it`) | plugins_build_test.go (`TestEsbuild...`), plugins_sourcemap_test.go | real esbuild; each comment names its `it`
evaluate.test.ts (all 8) | plugins_evaluate_test.go | Pi's evaluateFacet runs in the subprocess through the probe (plugins_probe_test.go); error cases also on the LoadReport
stack.test.ts (all 5) | plugins_stack_test.go | mapStack/GC cases in a subprocess started with `--expose-gc`
runtime.test.ts (all 4 incl. "loads no esbuild") | plugins_runtime_test.go | real NodeHost
plugins.test.ts (all, incl. fix round 1, GC and end to end) | plugins_plugins_test.go, plugins_endtoend_test.go | adaptations (process boundary) are named in each test's comment: abort after the commit holds `Attempt.checked` in the subprocess instead of spying `batch.commit`; the end to end has one observing server Go host that routes `Worker` to the worker's NodeHost (Pi's shape) and the server kind's NodeHost for the probe that reads `Index`
discover.test.ts, install.test.ts, watcher.test.ts | unchanged | unchanged
examples 35, 36, 37, 38, 39 | plugins_examples_test.go (`TestExample35..39`), testdata/examples | Pi's plugin directories byte for byte; Pi's own example scripts were run on Node and their output compared line by line
bench/plugins.ts | bench_plugins_test.go | real build and real Node evaluation/reload; Pi's bench imports a file the build refuses (`../contracts/contract.ts`), the port imports `@bench/contracts/contract` and fails when a build is not a script
(not upstream) | plugins_node_test.go, plugins_node_unix_test.go | the subprocess's lifecycle: start (aborted, timed out, old or invalid Node), close, crash, fatal ordering, output, flags, link attach and heartbeat, extraction (marker, repair, concurrent), manifest against the pin
(not upstream) | plugnode-differences.md | every remaining observable difference from Pi, with whether and at what cost it closes

## (d) Async contracts

`buildFacet` (async) | blocking `BuildFacet`: an error only for an aborted ctx (`context.Cause`), a failed build is a result | plugins_build_test.go#TestEsbuildBuildAnAbortedContextIsItsCause
`evaluateFacet`, `createPluginRuntime().load/reload` (async) | blocking `PluginRuntime.Load/Reload` over the control channel; ctx abort sends `abort`, the wait goes on until the subprocess answers (`aborted`, with `details.committed` after a commit) or ends; requests reach the subprocess in call order and Pi's runtime queues one reload after another per host | plugins_plugins_test.go#TestPluginsFixRound1AdoptsWhatAReloadCommittedWhenItsWaitAbortsAfterTheCommitALaterReloadKeepsIt, TestPluginsFixRound1SerializesReloadsOfOneHostTwoStartedTogetherKeepBothChanges
process lifetime | a NodeHost is never restarted; process exit or channel end fails pending requests with `*NodeExit`; `Close` waits 3 s then kills | plugins_node_test.go

## (e) Interface IDs

New public Go API in `chord/nextnode/plugins`: `PluginRuntime`, `NewPluginRuntime`, `NodeOptions`, `NodeHost` (`Exited`, `Err`, `Close`, `Link`), `NodeStream`, `NodeHostError` (every field of the `HostError` kind, typed), `NodeError` (+`Cause`), `NodeExit`, `CoreFacet`, `HostPackages` (now `map[string]string`), `RuntimeLoad` (no `Parent`; `CloseTimeout`, `ReadyTimeout`, `Compression`, `Heartbeat`), `PluginsLoad` (+`Address`, `Principal`, `Peer`, `Heartbeat`, `CloseTimeout`, `ReadyTimeout`, `Compression`; no Go host: the app attaches `NodeHost.Link()` with `Host.Route` or `Host.Accept`), `PluginsOptions.Node`, `Plugins` (struct), `BuildFacet(plugin, kind, options, ctx)`. Removed: `Builder`, `Evaluator`, `EvaluateOptions`, `ScriptFilename`, `PluginFile`, `Plugins` interface, `Plugins.Forget`, `PluginRuntime.Forget`.
