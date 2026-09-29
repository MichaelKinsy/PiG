# Failed Node factory API

## Contract and scope

Pi 0.87.1 `packages/coding-agent/src/core/extensions/loader.ts:238-243,462-467,547-552` awaits the factory and disables its API when it rejects. Every later API call throws the failed-extension error synchronously before validation or effects, including methods that normally return Promises. Factory-owned bus listeners are removed; a healthy sibling remains active.

`Runtime.buildAPI` previously had no failed-state guard. Registrations, getters and bus calls remained usable after `discardLoad`; action methods instead reported initialization or connection errors, sometimes asynchronously. The fix guards the public API before invoking each method. It reuses the existing factory state and does not change Provider composition, request parents, connection generations, shutdown, or the helper-owned resource-loader/event-bus lifecycle.

Both original cases in `8423-extension-factory-failure.test.ts` are ported through the production subprocess Host. The line12 case retains its exact flag/provider inputs and checks absent failed shared defaults, no failed provider publication or removal of the working provider, bus disposal and exact captured-API failure. A scheduled post-failure callback must reject too. The line57 case overlaps two direct factory loads against one Host: the failed factory signals a socket barrier after its registration, the working factory finishes, then the controller releases the failure. Provider publication and ownership are checked before and after rejection. The test owns and joins both workers and closes its sockets on every exit. No sleep or timing assumption creates the overlap.

Subprocess registration is the factory's unpublished journal. The Host only receives declarations after the factory succeeds, so a failing factory cannot publish its flag/provider changes. The tests assert that production boundary rather than reimplementing a test-only TypeScript transaction. This closes8423, not the distinct native shared-loader, module cache or runner queue/bind obligations. No case is designed out.

## Red and mutation evidence

- `TestUpstreamFailedFactoryDisablesCapturedAPI` fails before the fix because `on` succeeds instead of throwing. The complete method table also checks synchronous errors, unchanged registration collections, listener disposal and a still-live sibling.
- `TestFailedFactoryCapturedAPIThroughNodeCell` loads the real failing and surviving modules through `Host.LoadAll`. Before the fix, registrations succeed, illegal event emission reaches the sibling and a late listener remains usable.
- `56-failed-factory-api` loads the same modules with actual installed Pi0.87.1 and the production PiG Host. It compares the complete factory diagnostic, every method's complete error and synchronous-result marker, flag-during-load and listener counts with `output_equal`. The original paired run fails. Replacing the failed-state predicate with `false` passes Node syntax checking, compiles the Go caller and fails both tests and the scenario. The predicate is restored.
- `TestUpstreamFactoryFailureDiscardsRuntimeChangesAndDisablesFailedAPI` adds the working-provider and failed shared-state assertions from the original first case. A compiled embedded-runtime overlay disabling the guard exposes a third sibling event, one late listener call and a successful timer registration. The extended scenario56 fails the same mutation and passes three restored pairs.
- `TestUpstreamFactoryFailurePreservesConcurrentlyLoadedProvider` preserves the original second case's concurrency with a deterministic socket barrier. A compiled Go overlay changes failed-load cleanup to stop every published sibling; the test fails because `working-provider` is unregistered. Restored tests pass five race repetitions. A direct installed-Pi probe confirms the same pending working registration before and after failure.
- The final targeted source/schema/flag regressions pass five race repetitions. Related Node bus/provider ownership/load-error guards pass three race repetitions.

Evidence is retained in the lane evidence directory as `factory-api-*`. The pre-fix logs, final mutation logs and restored scenario JSON records are separate files.

## Broader verification findings

Three older notification fixtures supplied only `SetNotifyFunc`, which observes calls but does not bind UI availability. Under Pi's correct no-op UI behavior (`runner.ts:320-324,522`), Node flag clearing and packed flag/schema tests waited for a notification that could not occur. Independent runs reproduce those waits. The tests now bind the existing test/recording UI and assert that fixture prerequisite. Every original default, reset, schema, packing and reload assertion remains. Packed Go/Python/Rust flag/schema tests pass three race repetitions.

The older schema402 test scraped stderr for an `Error:` line even though the loader now returns its structured factory error. It failed with an empty scraped value. It now asserts the complete error and path expected by upstream `extensions-runner.test.ts:402-425` through `FactoryLoadError` and the reported inner error. No production error behavior changed.

Final verification:

- Full `coding/extension/host/subprocess` race suite after the complete8423 ports: PASS,343.655s.
- Nine scenarios pass three exact pairs each:56 failed API;31 runtime surface;32 load failure;32 CLI flags;36 defaults;37 multiline factory error;43 tool schema;52 interleaved admission; providers-registry22 producer streaming.
- Native full vet, Windows touched vet, full `make lint`, serialized parity-tagged touched lint, staged own-base `lint-changed`, fixture compilation and scenario lint: PASS.
- SDK-surface package: PASS.
- Full extension-conformance race run after the lead-authorized completion rerun: PASS,786.390s (`factory-api-conformance-complete.log`). The earlier10-minute budget failure in Rust compilation remains in its original log; it is not a skipped or passing run.
- `test/upstream-parity`: blocked by the existing divergence header saying32 active entries while33 sections exist. The owning Provider/divergence lane is notified; this change does not alter D78 or its approval.
- `ci-contracts`: reaches217 remaining release findings after local inventory regeneration;8423 is no longer a blocker, while the other incomplete KEEP files remain.
- `ci-drift`: reaches the existing unapproved D78 record after local normalization/coverage regeneration. Generated outputs are not part of this lane commit.

## Resources

The guard allocates wrappers when a Runtime builds its fixed API. It introduces no IPC, worker, queue, timer, request parent or cancellation scope. Failed calls cannot add registrations or bus subscriptions. The existing Host owns process/socket cleanup.

`BenchmarkFailedFactoryAPIThroughNodeCell` measures cached startup, a failed member, the survivor's full API probe and joined shutdown. Three samples of three iterations on Linux amd64, Go1.27.1 and an Intel Xeon6746E measure364–373ms/op,198–215KB/op and685–699Go allocations/op. Go CPU/allocation profiles are retained as `factory-api-final.cpu` and `.mem`. The whole-process profiles include untimed initial runtime materialization, whose embedded-file copying dominates allocation samples. These are not V8 allocation measurements or a claim about API-call or terminal latency.
