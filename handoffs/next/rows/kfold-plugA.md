# kfold-plugA: `chord/nextnode/plugins` (Pi slice 7c plugins component)

Package `github.com/MichaelKinsy/PiG/chord/nextnode/plugins` (`chord/nextnode/plugins/`, files `plugins_<stem>.go`, in-package tests). Pi `6d4b7443`, `packages/chord/src/next-node/plugins/`.
Not written (lead): `plugins.ts` (component), `runtime.ts`, `index.ts`. Design decision (lead): esbuild build, `vm.Script` evaluation and JS source-map stack mapping are the consumer-owned seams `Builder` / `Evaluator`.

## (a) Upstream src files

path | disposition | PiG files | evidence | rationale
--- | --- | --- | --- | ---
next-node/plugins/discover.ts | ported | plugins_discover.go | test:chord/nextnode/plugins/plugins_discover_test.go#TestDiscoveryNamesAPluginByItsPackageJSONListsItsFacetFilesByKindItsDependenciesAndExternals | `DiscoverPlugins`, `Plugin`, `Discovery`, `DiscoveryProblem`, `DiscoveryOptions`; `isPluginName` is a private copy of host/staging.ts's regex (the host's is unexported, see open questions)
next-node/plugins/install.ts | ported | plugins_install.go | test:chord/nextnode/plugins/plugins_install_test.go#TestInstallDependenciesRunsNpmInstallWithoutDevDependenciesPeersScriptsAuditOrFundingInThePluginsRealDirectory | `InstallDependencies`, `InstallOptions`
next-node/plugins/watcher.ts | ported | plugins_watcher.go (+ plugins_watcher_linux_test.go) | test:chord/nextnode/plugins/plugins_watcher_test.go#TestTheWatchersMarksFromRealFileEventsPublishingTheMarksToAWatch | `pluginWatcher` is a `state.Source`; fsnotify replaces `fs.watch`
next-node/plugins/build.ts | designed-out (esbuild) / ported (data) | plugins_build.go | test:chord/nextnode/plugins/plugins_build_test.go#TestBuildingAFacetReturnsASyntaxErrorAndAMissingFacetAsFailedResultsNeverAThrow | the esbuild build, `ImportRules`, `freeGlobals`, `describe` are the `Builder` seam (Go cannot run esbuild in process). Ported: `BuildOptions`, `BuildInput`, `BuiltFacet` (plain data), `BuildFacet` (missing-facet failed result, abort, delegate), `inside`
next-node/plugins/evaluate.ts | designed-out (`vm.Script`) / ported (identity) | plugins_evaluate.go | test:chord/nextnode/plugins/plugins_evaluate_test.go#TestEvaluatingAFacetRunsAScriptUnderAStableFilenamePerPluginKindAndDigest | `require`, `interop`, `registerScript`, `evaluateFacet` are the `Evaluator` seam (the default-export-is-a-facet check is the evaluator's error). Ported: `HostPackages`, `EvaluateOptions`, `ScriptFilename` (stable filename per plugin/kind/digest), `PluginFile` (= `fileOf`: path in the plugin's directory, `..` out refused). `PLUGINS_SPECIFIER`, `packageName`, the exported throwing `pluginFile` stub are JS-evaluation/esbuild internals: designed-out (no Go caller)
next-node/plugins/stack.ts | designed-out | none | n/a | `mapStack`/`registerScript`/`inlineSourceMap` map V8 stack positions through esbuild's inline source maps of `vm.Script` scripts; there is no JS script in Go
next-node/plugins/runtime.ts | ported | plugins_runtime.go | test:chord/nextnode/plugins/plugins_runtime_test.go#TestLoadingAndReloadingWithCompleteSetsReloadsACompleteSetSameDigestsUnchangedAChangedOneAppliedALoadedOneNotShippedRemoved | `NewPluginRuntime(evaluator)`, `PluginRuntime.Load/Reload`, `PluginScript`, `RuntimeLoad`; V8 compilation-cache switch designed out; per-host reload queue in call order (`queue`/`run`); `Forget(host)` replaces the WeakMap
next-node/plugins/plugins.ts | ported | plugins_plugins.go | test:chord/nextnode/plugins/plugins_plugins_test.go, plugins_endtoend_test.go, plugins_examples_test.go | `CreatePlugins`, `Plugins`, `PluginsOptions`, `KindDefinition`, `Built`, `RolloutStep/Host/Result`, `PluginsError`; state/watcher touched only in sections of the component's own `host.Loop`
next-node/plugins/index.ts | designed-out | none | n/a | barrel: Go imports the package
next-node/plugins/README.md | read | n/a | n/a | package doc in doc.go summarizes the seam split

## (c) Upstream test cases

upstream test file › case | disposition | PiG test
--- | --- | ---
discover.test.ts › names a plugin by its package.json, lists its facet files by kind, its dependencies and externals | ported | plugins_discover_test.go#TestDiscoveryNamesAPluginByItsPackageJSONListsItsFacetFilesByKindItsDependenciesAndExternals
discover.test.ts › reports a directory without package.json, with an invalid one, an invalid name or two files of a kind | ported | ...#TestDiscoveryReportsADirectoryWithoutPackageJSONWithAnInvalidOneAnInvalidNameOrTwoFilesOfAKind
discover.test.ts › keeps the first root's plugin of a duplicate name and reports the other; a missing root is empty | ported | ...#TestDiscoveryKeepsTheFirstRootsPluginOfADuplicateNameAndReportsTheOtherAMissingRootIsEmpty
discover.test.ts › follows a symlinked plugin directory to its real path | ported | ...#TestDiscoveryFollowsASymlinkedPluginDirectoryToItsRealPath
install.test.ts › runs npm install without dev dependencies, peers, scripts, audit or funding, in the plugin's real directory | ported (fake npm is a `sh` script recording cwd and args, not a Node script) | plugins_install_test.go#TestInstallDependenciesRunsNpm...
install.test.ts › rejects with the command's output when it fails, and when the context aborts | ported; Node's `name: "AbortError"` is the abort reason (`errors.Is(err, stop)`, `context.Cause`) | ...#TestInstallDependenciesRejectsWithTheCommandsOutputWhenItFailsAndWhenTheContextAborts
(own, not upstream) discovery returns the abort's reason before each directory, nothing at entry | added for `throwIfAborted` | plugins_discover_test.go#TestDiscoveryReturnsTheAbortsReasonBeforeEachDirectoryAndNothingForNoDirectory
(own, not upstream) empty `npmCommand` refused; aborted ctx refuses before running | added for the source branches | ...#TestInstallDependenciesRefusesAnEmptyCommandAndAnAbortedContextBeforeRunningAnything
watcher.test.ts › the watcher's marks › dirties every plugin whose facets used a changed file, and the plugin whose directory contains it | ported | plugins_watcher_test.go#TestTheWatchersMarksDirtiesEveryPlugin...
watcher.test.ts › ... › dirties the plugin of a file no facet read yet; asks for an install for a package.json | ported | ...#TestTheWatchersMarksDirtiesThePluginOfAFileNoFacetReadYet...
watcher.test.ts › ... › asks for discovery for a directory under a root and a facet file at a plugin's top; base for base code | ported | ...#TestTheWatchersMarksAsksForDiscoveryForADirectoryUnderARoot...
watcher.test.ts › ... › ignores node_modules and .git | ported | ...#TestTheWatchersMarksIgnoresNodeModulesAndGit
watcher.test.ts › ... › keeps the mark of a change during a build: only an unchanged mark clears when applied | ported | ...#TestTheWatchersMarksKeepsTheMarkOfAChangeDuringABuild...
watcher.test.ts › ... › marks from real file events, publishing the marks to a watch | ported (a Go watch: `Source.Watch` + `chord.WatchHandle.Start`, delivery waited with `DeliveryScheduler.Wait`) | ...#TestTheWatchersMarksFromRealFileEventsPublishingTheMarksToAWatch
watcher.test.ts › fix round 1 › never lets an older build's applied snapshot clear a newer edit's mark (Astra) | ported | ...#TestTheWatcherFixRound1NeverLetsAnOlderBuildsAppliedSnapshot...
watcher.test.ts › fix round 1 › drops the install mark of a plugin the next discovery no longer has (example 38) | ported | ...#TestTheWatcherFixRound1DropsTheInstallMark...
watcher.test.ts › fix round 1 › puts no watch on node_modules or .git (Opus N2): 200 packages add no inotify watch | ported; `skipIf(platform !== "linux")` is a `//go:build linux` file, no `t.Skip` | plugins_watcher_linux_test.go#TestTheWatcherFixRound1PutsNoWatchOnNodeModulesOrGit...
watcher.test.ts › fix round 1 › watches a directory that appears later, and marks the files in it | ported | plugins_watcher_test.go#TestTheWatcherFixRound1WatchesADirectoryThatAppearsLater...
watcher.test.ts › the watcher and links › survives a link loop created under a root (Sol) | ported | ...#TestTheWatcherAndLinksSurvivesALinkLoop...
build.test.ts › returns a syntax error and a missing facet as failed results, never a throw | ported at the seam: the missing facet half is `BuildFacet`'s own; the syntax error half is the reference builder's failed result passed through unchanged (esbuild message text is not reproduced) | plugins_build_test.go#TestBuildingAFacetReturnsASyntaxErrorAndAMissingFacetAsFailedResultsNeverAThrow
(own, not upstream) `BuildFacet` throws only when ctx aborts | added for `throwIfAborted` | plugins_build_test.go#TestBuildingAFacetThrowsOnlyWhenItsContextAborts
evaluate.test.ts › runs a script under a stable filename per plugin, kind and digest | ported at the seam: the filename (`ScriptFilename`), not the V8 stack text | plugins_evaluate_test.go#TestEvaluatingAFacetRunsAScriptUnderAStableFilenamePerPluginKindAndDigest
evaluate.test.ts › gives each facet pluginFile for its own plugin's directory and refuses .. out of it | ported at the seam: `PluginFile` path and the refusal message; the `createHost` load of the evaluated facet and "outside an evaluated facet it has none" (`pluginFile("x")` throws) are JS-only | plugins_evaluate_test.go#TestEvaluatingAFacetGivesEachFacetPluginFile...
build.test.ts › inlines the plugin's own files and another plugin's file (copied); keeps host packages external | designed-out | esbuild import table and inline source maps (Builder seam)
build.test.ts › hashes each input as esbuild read it; the digest is the source's hash | designed-out | the hashes and the digest are computed by the Builder (a Builder is free to hash; the reference builder in plugins_seam_test.go uses sha256 hex and is checked in plugins_build_test.go only for `built.Digest == sha256(source)` of itself); production code never hashes
build.test.ts › gives the same script and digest from two checkout paths (relative source maps) | designed-out | source maps relative to the plugin are esbuild output
build.test.ts › inlines a package of its dependencies; keeps chord.external packages and Node builtins external on Node | designed-out | esbuild
build.test.ts › refuses a chord.external package and a Node builtin in a browser build | designed-out | esbuild
build.test.ts › collects a browser build's CSS into one stylesheet and its assets into content-hashed files | designed-out | esbuild; `CSS`/`Asset` are the data it fills
build.test.ts › refuses CSS and assets in a Node build | designed-out | esbuild
build.test.ts › refuses import.meta.url, __dirname, __filename, a dynamic import() and top-level await, pointing to pluginFile | designed-out | esbuild + source-map positions of JS
build.test.ts › refuses a path outside the plugin, a package not in its dependencies, and a package in a copied file | designed-out | esbuild resolution rules
build.test.ts › imports another plugin's file by name from any root; refuses relative paths into it, names a missing plugin | designed-out | esbuild resolution rules
evaluate.test.ts › gives host packages as the host's instances: a ChordError crosses the plugin as one | designed-out | `vm.Script` `require` (Evaluator seam)
evaluate.test.ts › answers default and named imports of an ESM and a CommonJS host package | designed-out | JS module interop
evaluate.test.ts › loads chord.external packages and Node builtins from the plugin's directory | designed-out | Node `require`
evaluate.test.ts › throws an import-time throw, a non-facet export and a refused require, naming the facet | designed-out | JS evaluation (the facet check is the Evaluator's error; the reference evaluator returns the same message for an unknown script, plugins_evaluate_test.go)
evaluate.test.ts › runs a computed import() through the facet's require | designed-out | JS
evaluate.test.ts › ESM host packages' wrappers › wraps a namespace once, and keeps no namespace nobody else holds | designed-out | JS module namespaces, WeakRef/GC
stack.test.ts › mapStack › maps a plugin script's positions ...; lets a script's source map go with its facet | designed-out | stack.ts is JS source-map mapping
stack.test.ts › mapStack, fix round 1 › maps an import-time throw / maps a throw while its default export is read / keeps a script's map while any evaluation of it lives | designed-out | stack.ts
`fixtures/plugins/{contracts,notes}` are copied to `chord/nextnode/plugins/testdata/plugins/` (used by discover and watcher tests); `files` is only used by evaluate/plugins cases that are JavaScript (the `PluginFile` case builds its own plugins)

## (d) Async contracts

item | contract | evidence
--- | --- | ---
`discoverPlugins` (async) | blocking `DiscoverPlugins(roots, options, ctx)`: roots listed in order, each root fully listed before its directories are read (as `await directoriesOf`), abort checked before each directory (`ctx.Err()` -> `context.Cause(ctx)`; never at entry or after the last), a read error is the returned error | plugins_discover_test.go#TestDiscoveryReturnsTheAbortsReasonBeforeEachDirectoryAndNothingForNoDirectory (own test) and the ported cases (order, errors)
`installDependencies` (async, `spawn` with the context's signal) | blocking `InstallDependencies(plugin, ctx, options)`: aborted ctx returns its reason before starting; while running, abort sends SIGTERM and returns the reason; non-zero exit returns `plugin "<name>": <cmd> install failed (exit <code>): <output>`; stdout and stderr share one buffer (written order) | plugins_install_test.go (fail text, abort kills a `sleep 30` and returns the cause, pre-aborted never runs the command)
`buildFacet` (async) | blocking `BuildFacet`: missing facet -> failed result without abort check (upstream order), then abort check, then the Builder; failed builds are results, errors only for abort | plugins_build_test.go
`PluginWatcher` microtask publication (`queueMicrotask` + `batch.within`) | `Batch.Soon` (same host microtask queue), at most one pending (`scheduled`); published only when watched or derived from | TestTheWatchersMarksFromRealFileEventsPublishingTheMarksToAWatch (frames delivered after the marks change)
`fs.watch` callbacks (event loop) | one goroutine owned by the watcher reads fsnotify's events/errors and runs each as a host section via `Batch.Section` (`Loop.Run`); `close` stops fsnotify without waiting, `join` waits for the goroutine (call outside sections); events after close are dropped | race run: `-race -count=5` ok

## (e) New exported identifiers

`Plugin`, `DiscoveryProblem`, `Discovery`, `DiscoveryOptions`, `DiscoverPlugins`; `InstallOptions`, `InstallDependencies`; `Platform`, `PlatformNode`, `PlatformBrowser`, `BuildOptions`, `BuildInput`, `CSS`, `Asset`, `BuiltKind`, `BuiltFacetScript`, `BuiltFacetFailed`, `BuiltFacet`, `Builder`, `BuildFacet`; `HostPackages`, `EvaluateOptions`, `Evaluator`, `ScriptFilename`, `PluginFile`; `PluginsState`, `MarkSnapshot`, `DiscoverySnapshot`.
Unexported (Pi's `PluginWatcher`, `WatcherOptions`, `FACET_EXTENSIONS`, `isPluginName`, `inside`): `pluginWatcher`, `newPluginWatcher`, `watcherOptions`, `facetExtensions`, `isPluginName`, `inside`. Watcher members used by the component (all unexported, Pi names): `know`, `used`, `snapshot`, `applied`, `installed`, `mark`, `close`, `join` (Go-only, see below), `needsDiscovery`, `discoverySnapshot`; JSON watchable via `Value`/`Deliver`/promoted `Watch`.

Go forced differences: `BuiltFacet` is a struct with `Kind` (`BuiltFacetScript`/`BuiltFacetFailed`) instead of a union; `Plugin.Facets` is a Go map (Pi's record keeps insertion = `Kinds` order: iterate the app's kinds in order and look up, do not range the map); `MarkSnapshot` carries `Names` for Pi's `Map` key order; `PublicPath` is `*string` (undefined vs `""`); `InstallOptions.NpmCommand` nil = undefined (npm), empty non-nil = refused; `ctx` is last as everywhere in Chord's Go port.

## How the component should use it

- Build the watcher inside the host's batch: `newPluginWatcher(watcherOptions{Roots, Base, Kinds, Failed}, batch)` where the batch has `UseMicrotasks(loop.Queue)` and `UseSection(loop.Run)` (the host's already does). Every method runs in a section; file events enter through `Batch.Section`.
- `Failed` runs in a section; wrap it into the `watch_failed` report.
- `close()` in the component's `Close()`; `join()` (outside sections) when it must drain.
- `Plugins.State()` is the watcher itself as a `state.SourceHandle` (its `Value()` is the JSON object `{dirty, install, set, base}`; `PluginsState.json()` is the encoder, a typed reader is not exported).
- A `Builder` and `Evaluator` come from the consumer; the plugins component owns `BuildFacet(builder, ...)` use. Test doubles: `referenceBuilder`, `referenceEvaluator` in plugins_seam_test.go (in-package, the component's tests can use them).

## Open questions

1. `isPluginName` (host/staging.ts exports it, "the plugins component checks it too"): the Go host keeps its regex unexported (`host_host.go: pluginName`). I copied the regex into the plugins package (same pattern); when the host lane exports `IsPluginName`, replace the copy.
2. Node reports an event with no file name (the watched directory itself removed/moved/chmod'd) as `join(dir, basename(dir))`; fsnotify reports the directory's own path. For a watched root or base directory that is removed Pi marks `set` or `base` (by that accident), the Go port marks nothing. I did not port the translation because I could not run Node on Linux to confirm the libuv behavior (`uv__basename_r` for nameless events): `[INFERENCE]`. Decide whether to port it.
3. After `close()` the Go watcher never watches again (a later `know` does not re-add watches); Pi has no closed flag and a `know` after `close` would add watches again (a leak). Behavior difference intended (resource guard), observable only after `close`.
4. No test for `watch_failed` (`failed`): no upstream case and no portable way to make fsnotify's Add fail other than ENOENT in a test container.
5. Error texts that embed `JSON.parse` messages (`package.json: <message>`) carry Go's `encoding/json` message; only the `package.json: ` prefix is common (the upstream test matches the prefix only).
6. A `Builder` is trusted to return `BuiltFacet{Plugin, Facet}` set to the plugin and kind it built; `BuildFacet` does not rewrite them.
7. fsnotify v1.5.4 on macOS (kqueue) opens a descriptor per file of a watched directory; Linux (inotify, the CI container) is one watch per directory as upstream. The `node_modules`/`.git` exclusion keeps the large trees out on both.
8. foundA/state: nothing needed; only `state.Source`, `Batch.Section`, `Batch.Soon`, `Source.Publish/Changed/Watched/DependentSet` are used.

## Second round: runtime.ts, plugins.ts, plugins.test.ts, runtime.test.ts, examples

Plugins, hosts and edits of these tests are written in a small facet language (plugins_seam_test.go, documented there) read by `referenceBuilder` and `dslEvaluator`, instead of TypeScript; upstream's layouts, edits, plugin names and expectations are kept. Contract names are `test.<dir>` (plugins.test) and `example.*` (examples) as upstream's.

upstream test file › case | disposition | PiG test
--- | --- | ---
runtime.test.ts › loads a set and leaves out a plugin whose build or evaluation failed, reported with the host's report | ported | plugins_runtime_test.go#TestLoadingAndReloadingWithCompleteSetsLoadsASetAndLeavesOut...
runtime.test.ts › reloads a complete set: same digests unchanged, a changed one applied, a loaded one not shipped removed | ported | ...#TestLoadingAndReloadingWithCompleteSetsReloadsACompleteSet...
runtime.test.ts › keeps the loaded facet of a plugin whose new build or evaluation failed; an added failing one is left out | ported | ...#TestLoadingAndReloadingWithCompleteSetsKeepsTheLoadedFacet...
runtime.test.ts › the runtime's imports › loads no esbuild | designed-out | an import hook run in a Node process; a PluginRuntime takes an Evaluator and has no Builder
(own) reload of a host the runtime did not load fails | added | ...#TestPluginRuntimeReloadOfAHostItDidNotLoadFails
plugins.test.ts › per plugin at startup › leaves out a plugin whose build fails, whose evaluation throws or whose setup throws; loads the others and core | ported | plugins_plugins_test.go#TestPerPluginAtStartupLeavesOut...
plugins.test.ts › per plugin at reload › rebuilds one plugin of three | ported | ...#TestPerPluginAtReloadRebuildsOnePluginOfThree...
plugins.test.ts › ... › keeps the loaded facet of a plugin whose build fails or whose evaluation throws | ported | ...#TestPerPluginAtReloadKeepsTheLoadedFacet...
plugins.test.ts › ... › adds a plugin discovered since, removes one no longer there | ported | ...#TestPerPluginAtReloadAddsAPluginDiscoveredSince...
plugins.test.ts › ... › keeps a consumer edited together with a provider whose new build fails (the closure rule through builds) | ported | ...#TestPerPluginAtReloadKeepsAConsumerEditedTogetherWithAProvider...
plugins.test.ts › rollout › runs the steps in order ... / counts a host's error ... (D12) / keeps dirty a plugin whose built kind no step shipped / stops at a step that rejects | ported (4 cases) | ...#TestRollout...
plugins.test.ts › current sets › scripts(kind): built on first use ... | ported | ...#TestCurrentSetsScriptsKind...
plugins.test.ts › installs through the component | ported (fake npm is the sh script of install_test) | ...#TestInstallsThroughTheComponent...
plugins.test.ts › GC › collects old facets over 200 reloads; the heap stays flat after warm-up | ported: `runtime.ReadMemStats` heap after GCs (< 2 MiB growth) and weak pointers for the made states | ...#TestGCCollectsOldFacetsOver200Reloads...
plugins.test.ts › end to end: a server and a worker | ported (real memory link, remote `Notes`, a count document) | plugins_endtoend_test.go#TestEndToEndAServerAndAWorker...
plugins.test.ts › fix round 1 › serializes reloads of one host | ported (two goroutines; each report keeps both changes whichever runs first) | plugins_plugins_test.go#TestFixRound1SerializesReloadsOfOneHost...
plugins.test.ts › fix round 1 › applies the removals of the build it was given | ported | ...#TestFixRound1AppliesTheRemovals...
plugins.test.ts › fix round 1 › adopts what a reload committed when its wait aborts after the commit | adapted: upstream spies `batch.commit` of the host; the Go host has no such hook, so the runtime's half (adopt of `details.committed`) is tested with a fake host returning the `aborted` error (plugins_runtime_test.go#TestRuntimeAdoptsWhatAReloadCommitted...), and the real host's abort after its commit is exercised by example 36 part 4 | both
plugins.test.ts › fix round 1 › leaves a plugin out of a host whose kind its rebuilt facets no longer have | ported | plugins_plugins_test.go#TestFixRound1LeavesAPluginOut...
plugins.test.ts › fix round 1 › reloads a structured clone of a build as the build itself | ported: a copy of the `Built` struct (`clone := *built`) | ...#TestFixRound1ReloadsACopyOfABuild...
plugins.test.ts › fix round 1 › reads a changed package.json again at the next build | ported (the new package in `node_modules` is esbuild's resolution and is not written) | ...#TestFixRound1ReadsAChangedPackageJSONAgain...
plugins.test.ts › the reload queues and garbage collection › lets a rejected new facet go once its report is dropped | ported (weak pointers) | ...#TestTheReloadQueuesAndGarbageCollection...
examples/35-plugins | ported | plugins_examples_test.go#TestExample35PluginsLoadEditMarkBuildRollOut
examples/36-plugin-failures | ported (mapped JavaScript stacks, `mapStack`, are designed-out: failures asserted by message; §4's abort window is a microtask race in Pi (attempt.ts settles the swap's first check at the first parked check; host.ts awaits exactly it), which a Go watch on its own goroutine cannot order: the test asserts what holds either way (commit stands, `aborted` names it when returned, a later reload keeps it), the abort branch is the fake-host runtime test) | ...#TestExample36PluginFailures
examples/37-plugin-imports | designed-out | esbuild's import rules and refusals, `?url` assets, JSON imports, native and CommonJS packages, `fake-npm.mjs` (the Builder seam); the install through the component is covered by TestInstallsThroughTheComponent...
examples/38-watch-and-rollout | ported | ...#TestExample38WatchAndRollout
examples/39-browser-build | designed-out | esbuild's browser build: stylesheet and assets (`CSS`, `Asset` are the data a Builder fills)

Expected values of examples 35/36/38 are derived from Pi's code (`watcher.ts`, `plugins.ts`, `runtime.ts`): Pi's examples could not be run (Node on macOS only; FSEvents' events differ from Linux's inotify, and the output of the unmodified script on this machine was not the example's). Example 38 reproduces: marks after each edit, the three-step rollout reports, the stopped rollout, the failed plugin staying dirty, the edit during a build keeping its mark.

Test helpers that Go forces: file edits of the examples go through a temporary file and a rename and wait for the watcher's events (`edit`/`quiet`), because a write in place is several events and exact marks cannot be asserted after the first one; Pi's examples print the marks after the first event and are racy by design (`until`).

### Component notes (Go forms)

- `CreatePlugins(options, ctx) (Plugins, error)`; `Plugins` methods: `State() chord.Watchable[delta.JsonValue]`, `Marks() PluginsState`, `Discovery()`, `Build`, `Scripts`, `Current`, `Load`, `Reload`, `Step`, `Rollout`, `InstallDependencies`, `Forget(host)`, `Close()`. `Rollout` returns `(RolloutResult, error)` with the stopped step in `Stopped` (a rejecting step is a result, as upstream); the error is for the component's own failures (a Go forced channel).
- `Built` returned by `Build` is a `*Built`; its unexported `self` pointer and `snapshot` replace upstream's `WeakMap<Built, MarkSnapshot>`: a copy of the struct has another address and clears no marks.
- `PluginsOptions.Kinds` is a slice of `KindDefinition{Name, Platform, HostPackages}` (upstream's record keeps its order; a Go map does not). Facet files per plugin are `Plugin.Facets[kind]`; iterate the kinds in the options' order.
- Concurrency (AGENTS.md async parity): `Build` runs one build per facet concurrently (`Promise.all`: owned, joined, results by index, the first error by index, a Builder panic is a failed result) and calls the Builder from several goroutines; `Reload` of one host are serialized in the order their sets were decided (a ticket taken in the same section that applies the build and reads the set); everything else of the component is touched in sections of its own loop.
- `Forget(host)` (Plugins and PluginRuntime): Go has no weak table keyed by a host; a closed host is forgotten by its owner.
- New exported identifiers of the second round: `PluginScript`, `RuntimeLoad`, `PluginRuntime`, `NewPluginRuntime`, `KindDefinition`, `PluginsErrorKind` (+ `PluginsListenerError`, `PluginsDeliveryFailed`, `PluginsWatchFailed`), `PluginsError`, `PluginsOptions`, `Built`, `RolloutHostKind` (+ `RolloutReported`, `RolloutStopped`, `RolloutErrored`), `RolloutHost`, `StepResult`, `RolloutStep`, `RolloutStepResult`, `StoppedStep`, `RolloutResult`, `PluginsLoad`, `Plugins`, `CreatePlugins`.

### Async contracts (second round)

item | contract | evidence
--- | --- | ---
`PluginRuntime.reload` queue (`state.reloading.then`) | blocking `Reload`; queued in call order per host by a ticket (`queue`), the ticket's turn waits for the previous reload to end (not for ctx); a rejected reload does not break the chain | TestFixRound1SerializesReloadsOfOneHost..., mutation: no wait on the previous turn fails it
`plugins.reload` (`ship` + `runtime.reload` in one tick) | `ship`, the set's copy and the ticket are one section | same
`plugins.build` `Promise.all` | `buildAll`: one goroutine per facet in a `sync.WaitGroup`, results by index | every build test
`plugins.rollout` | steps run in order, each after shipping its kinds; a step's error stops it (result, not error); marks applied once after the last step, from the snapshot of the built pointer | TestRollout..., TestExample38...
`committed` after an aborted reload | the runtime adopts `details.committed.failed` of the host's `aborted` error | TestRuntimeAdopts..., TestExample36 part 4 (real host)

### Open questions (second round)

1. Pi reports the plugin left out at startup (example 36's weather, whose setup throws) failed again by every reload, since the runtime passes its script again and the host stages its throwing setup again; the Go port does the same (asserted in example 36). Confirm this is what Pi does (it follows from `runtime.ts` `reload`: loaded has no entry for it, so it is `prepare`d and passed as an added plugin).
2. `Plugins.State()` is a `chord.Watchable[delta.JsonValue]` of the component's own loop; its `Watch` opens the watch in a section. The typed reader is `Marks()`.
3. Tests that need the host were run against a snapshot of the working tree's host with one line of `host_reference.go` (`ReferenceKeyed.Get`, line 1171) patched locally, because the worktree's host did not compile (the patched copy is /tmp/kfold/plugA-live; the worktree is untouched).
