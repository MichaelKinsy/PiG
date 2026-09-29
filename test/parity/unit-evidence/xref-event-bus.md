# Cross-process references and the shared pi.events bus (D83, D77, D73)

Design: `plans/0.3.x/gap-xproc.md`. Pi contract: `packages/coding-agent/src/core/event-bus.ts:12-33`, `packages/tui/src/tui.ts:745-775`.

## What changed

- Every Node process is one xref realm (`runtime-node/xref.mjs`, `xref.go`). A single realm keeps Pi's in-heap `EventEmitter`; with several realms the Host keeps the ordered listener registry (`event_bus.go`) and runs each listener's synchronous prefix in its own realm while the emitter waits. Payloads cross as references with a Host lease ledger. D77 is retired; D83 is narrowed to the named cross-realm boundaries.
- `OverlayHandle.unfocus({target})` focuses null, the installed editor, or another mounted overlay of the extension (D73 narrowed).

## Evidence (all on Linux, Node 26.7, Go 1.27.1)

Differential tests run each scenario against Pi's own vendored `createEventBus` in one heap and against packed, strict-isolation and mixed PiG topologies, with exact string equality:

- `TestXrefEventBusForeignPrefixMutationMatchesPi` (the checkpoint's red `{n:0}` versus `{n:1}` assertion), `TestXrefEventBusMatchesPiAcrossRealms` (identity, cycles, functions with receivers, private fields, accessors, Map/Date, symbols, non-configurable properties, first-generation post-await ordering, retained aliases, reentrant emit), `TestXrefEventBusListenerOrderMatchesPi`, `TestXrefEventBusReentrantDispatchMatchesPi` (four levels, three runs), `TestXrefEventBusCrossingEmittersDoNotDeadlock`, `TestXrefEventBusPayloadLifetimeMatchesPi` (forced GC; Host ledger empty), `TestXrefOwnerExitFailsRetainedAlias`.
- Mutation checks: skipping removed listeners in a routed snapshot fails the two routed topologies of the order test; suppressing importer releases fails the lifetime test.
- `-race -count=3` of all `TestXrefEventBus*`, `TestNodeRuntimeEventBusMatchesPi`, `TestNodeCellInterleavedGoFactoryKeepsOrderAndBus` passes.
- Recovery tests (`node_recovery*_test.go`) now assert both the shared bus and the unchanged process placement.
- Strict parity scenarios `extensions-runtime/31-extension-runtime-surface` and `52-interleaved-node-admission` pass against real Pi 0.87.1 with their declared runs.
- D73: `TestNodeOverlayUnfocusSendsExplicitTarget`, `TestUIBridgeResolvesOverlayUnfocusTarget`, `TestRemoteOverlayUnfocusExplicitTargetMatchesPi` (mutation: ignoring the target fails it).
- `make test-porting-release`, `divergence-consistency`, `divergence-quality`, `divergence-guard`, `source-hygiene`, `docs-drift` pass. The only failing subprocess test in the environment is `TestNodeVendoredTuiUpstreamTests/native-clipboard-linux` (needs xcb headers), unrelated to this change.

## Cost

`BenchmarkXrefEventBusEmit` (200 emits per tool call, listener reads and writes the payload): one realm 4.8 us/emit, 89 allocs per call; two realms 1.58 ms/emit (about 470 allocations per emit: the emit call, the dispatch request, two foreign property operations, the settle call). A single foreign property read costs about 0.4-0.6 ms round trip on this machine, bound by cross-process wake-up (Node IO worker, Host lane, owner IO worker), not by CPU: the Host profile shows mostly futex and syscall time. The cost applies only when several Node realms exist; it runs in the extension process, not on the TUI loop.

## Residuals (D83)

Atomicity at synchronous waits and timing of independent macrotasks across realms; microtask order beyond a foreign listener's first post-await generation; cross-realm reference cycles until a realm exits; proxy brand checks; native SDK realms by value. A listener reached while its realm already waits in a synchronous call runs its continuations when that wait returns.

## Not done

- Native SDK (Go, Rust, Python) readers/owners of the xref wire (gap-d78 builds on `plans/0.3.x/gap-xproc.md` section 4). No SDK was touched by this lane.
- Live Go-owned Main Screen components as xref objects and prototype patches (D73 keeps them).
