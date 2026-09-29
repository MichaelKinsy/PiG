# Extension factory cache checkpoint

The four original cases in `packages/coding-agent/test/suite/regressions/extension-factory-cache.test.ts` are implemented in `TestUpstreamExtensionFactoryCache`. Each case runs through a real source extension and the shipped Node loader graph, in ordinary and explicitly isolated placement:

| Upstream line | Case | Module loads / factory runs |
|---|---|---|
|69|cached same cwd; distinct extension and runtime objects|1 / 2|
|83|direct loads do not cache|2 / 2|
|95|resource-loader reload clears the cache|2 / 2|
|115|cache changes with cwd|2 / 3|

The module body and factory increment the original counters. The same-cwd case also returns the actual reference comparisons. `TestHostUncachedFactoryModuleEvaluation` separately checks direct loading and reload through the main Go Host, including a fresh direct-load extension and the complete `module,factory,module,factory` trace. That trace replaces only the test's process-local counter mechanism; it does not alter the source or cache policy.

The original tests import private loader modules. The fixture therefore maps the compiled file locations explicitly: Pi uses `dist/core`, and the embedded PiG graph uses `core`. This is fixture navigation through the existing file map, not a claim that arbitrary package-root `/dist/core` imports work. An initial probe of that separate package-layout surface failed because the materialized package has a flattened graph. The finding is sent to the Node package/Provider owner. No vendor layout or resolver workaround is added here.

This proves the Node module-cache contract. It does not add a cached `Host.Load` API or claim native ResourceLoader, shared factory-time state, replacement transport, or Markdown lifecycle closure.

## Evidence

- Installed Pi0.87.1 produces the four records shown above, including both fresh identities.
- Targeted cache and main-Host tests pass three race repetitions.
- `57-extension-factory-cache` compares the complete four records with `output_equal`, without normalization, and passes three pairs.
- Six overlays syntax-check and compile, then fail behaviorally: bypassed module-cache hit; cache retained across cwd changes; direct loads incorrectly cached; reused Runtime identity; reused Extension identity; and cache retained across resource-loader reload. The cache-hit mutation also fails the canonical paired scenario. All overlays are outside the source tree; production source remains unchanged.
- The complete subprocess race package is still running at this checkpoint. The owned cache mapping remains pending until final qualification/review; this is not a completion claim.
- The earlier full conformance run now completes under race:786.390s, with no skipped cases. The lead explicitly authorized the compilation budget needed for the complete run.

Evidence is retained in the lane evidence directory as `factory-cache-*` and `cache-*-mutation.log`.

## Resources

`BenchmarkFactoryCacheThroughNodeHost` includes source-host startup, the selected inner loader case and joined shutdown with a warm artifact cache. Three samples of two iterations measure0.797–0.915s/op,158–187KB/op and601–626Go allocations/op on Linux amd64, Go1.27.1 and an Intel Xeon6746E. Go CPU/allocation profiles are retained as `factory-cache.cpu` and `.mem`. These are whole-host probe costs, not per-cache-call timings or V8 heap measurements. They do not establish a speed improvement.

The fixture invalidates every returned inner runtime, clears the module cache and removes its temporary files. The Go Host owns process/socket cleanup and joins shutdown. No production cache, worker, retention limit or cancellation policy changes in this checkpoint.
