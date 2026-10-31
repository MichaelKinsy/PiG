# K-fold KReload rows (staging, host, facet, root, destination, report types, test support, host tests, examples 12/33/34, reload bench)

## (a) upstream src files
chord/next/host/staging.ts | ported | host/host_staging.go | test:chord/next/host/host_staging_test.go#TestStagingAThrowKeepsThePluginOnItsOldCodeReported (+ the per plugin cases) | stagePlugin, addStaged, Staged (`staged`), StagedPlugin, Recorded, recordFactory + recording self, migratesTables, IsPluginName (exported: the plugins package needs it). Pi's `Map` order is `namedMap`. Forced differences below.
chord/next/host/host.ts | ported | host/host_host.go | test:host_host_test.go#TestHostReload* , #TestHostLoadLeavesOut... | load per plugin (LoadReport), reload (staging, closure fixpoint, one commit, fates, report), CommittedReload, rewait, bind's anonymous refusal, unboundCause via data.Binds
chord/next/host/facet.ts | ported | host/host_facet.go | test:host_facet_test.go | ServeOptions.Accepts; a Facet carries an identity (see (e))
chord/next/host/root.ts | ported | host/host_root.go | test:host_root_test.go#TestHostRootRunsAnonymous..., #TestHostRootRefusesAnAnonymousScope... | no linked signal: the scope's own cancel + one AfterFunc on the caller; RootOptions.Anonymous (same form as lane NHttp)
chord/next/host/destination.ts | ported | host/host_destination.go | test:host_staging_test.go (closure rule, host address) | placedHere
chord/next/host/README.md | ported (intent) | host/doc.go | - | "Plugins, load and reload" section
chord/next/host/index.ts, internal.ts | designed-out | - | - | barrels
test/next/support.ts | ported | host_next_support_test.go | - | loaded/loadedPlugins return the failed plugins (hostErrors.Failed), relayed.Route (already), mustLoad

## (c) upstream tests
host.test › load › leaves out and reports invalid and duplicate plugin names ... | ported | TestHostLoadLeavesOutAndReportsInvalidAndDuplicatePluginNamesAndDoubleServesTheLaterRefusesASecondLoad
host.test › principals › a host without a principal / an address serves no remote type | ported (report) | TestHostPrincipals...
host.test › a load whose setup throws (per plugin) | ported | TestHostALoadWhoseSetupThrowsLeavesThatPluginOut...
host.test › reload: the API (7 cases) | ported | TestHostReloadTheAPI*
host.test › reload: the commit (2 cases) | ported | TestHostReloadTheCommit*
host.test › reload: registries' older readers | ported | TestHostReloadRegistriesOlderReaders...
host.test › reload across hosts, in the app's order | ported | TestHostReloadAcrossHostsInTheAppsOrder...
host.test › reload: an abort while staging | ported | TestHostReloadAnAbortWhileStaging...
staging.test (all cases) | ported | host_staging_test.go
facet.test › contribute at most one value per registry | ported | TestFacetsContributeAtMostOneValuePerRegistryOnlyWhileTheirSetupRuns
root.test › 2 new cases | ported | host_root_test.go
examples 12 (comment only), 33-reload, 34-versions | ported | TestExample33Reload, TestExample34Versions
bench/reload.ts | ported | bench_reload_test.go#BenchmarkReload
bench/links.ts, store.ts | not ported here (links bench measurement method; store bench is Durable's) | -

## (d) async contracts
host.reload serialization | `reload` chains behind the previous reload's end (a once-only promise), which Go waits on outside a section; the tail settles whatever the reload's result | test:TestHostReloadTheAPIRunsReloadsOneAfterAnother
reload abort | before the commit: `aborted`, nothing changed; after it: the wait for the report (select on every swap/retry's `checked` and ctx) ends `aborted` with details.committed | test:TestHostReloadTheAPIAnAbortBeforeTheCommit..., #AnAbortAfterTheCommit...
load/reload entry | blocking calls, entering the loop themselves (never inside a section)

## (e) new exported identifiers
host: LoadReport, PluginFailure, ReloadOptions, ReloadReport, PluginChanges, ReloadedObject, ReloadedObjectKind (ReloadSwapped, ReloadRestarted, ReloadEnded, ReloadParked, ReloadRetried), CommittedReload, Host.Reload, Host.Load now returns (LoadReport, error), IsPluginName, ServeOptions.Accepts, RootOptions.Anonymous, HostError.State, Facet identity (DefineFacet only)

## Forced differences
- A Facet is compared by identity (Pi `===`): Go func values are not comparable, so DefineFacet allocates an identity every copy shares; a Facet literal has none and is never the same.
- A recording self's handles are typed inert values, not a Proxy; a Reference made by `self.Get` is a never-bound binding's, so `.Member(...)` and `Derive` over it work; calling the methods of a zero `*documents.Singleton`/`*store.TableHandle` while declaring panics (the plugin fails staging).
- Declarations panic (Self returns no errors) and the factory runs under tryPlugin.
- "abort lands right after the commit" test: Pi spies `batch.commit`; Go cancels the ctx from the swapped object's second factory run (the first is staging's), which runs in the commit's section.

## bench rows
test/next/bench/links.ts | unchanged-observable | - | - | measurement-method change only (warm-up commits, median/p90, burst medians): no host behavior; the Go link benches (bench_links_test.go) already report per-op costs via testing.B
test/next/bench/store.ts | designed-out | - | - | Durable's `commitWrites` helper in the Durable store bench; no Go counterpart in this package

## report ordering (example 36 §4)
Host.reloadReport: after every check settled, runs the host's batch deliveries (`enter; scheduler.Wait; enter`) and then reads ctx, standing for Pi's >= 3 microtask ticks between a check's settle and the report; a watcher that aborts the ctx from its callback wins deterministically.
